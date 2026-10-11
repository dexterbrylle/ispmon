package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/config"
	"github.com/dexterbrylle/isp-speed-monitor/internal/health"
	"github.com/dexterbrylle/isp-speed-monitor/internal/notify"
	"github.com/dexterbrylle/isp-speed-monitor/internal/outage"
	"github.com/dexterbrylle/isp-speed-monitor/internal/probe"
	"github.com/dexterbrylle/isp-speed-monitor/internal/report"
	"github.com/dexterbrylle/isp-speed-monitor/internal/sla"
	"github.com/dexterbrylle/isp-speed-monitor/internal/speedtest"
	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

// Engine orchestrates speed tests, probe loops, SLA evaluations, and reporting.
type Engine struct {
	Config        *config.Config
	Store         *store.Store
	Runner        speedtest.Runner
	Prober        *probe.Prober
	OutageSM      *outage.StateMachine
	Dispatcher         *notify.Dispatcher
	HeartbeatPath      string
	ProbeHeartbeatPath string
	Logger             *slog.Logger
	Now                func() time.Time

	notifyMu   sync.Mutex
	notifyWG   sync.WaitGroup
	checkMu    sync.Mutex
	isChecking bool
	reportMu   sync.Mutex
}

// New creates an Engine instance.
func New(
	cfg *config.Config,
	st *store.Store,
	runner speedtest.Runner,
	prober *probe.Prober,
	outageSM *outage.StateMachine,
	dispatcher *notify.Dispatcher,
	logger *slog.Logger,
) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		Config:             cfg,
		Store:              st,
		Runner:             runner,
		Prober:             prober,
		OutageSM:           outageSM,
		Dispatcher:         dispatcher,
		HeartbeatPath:      health.HeartbeatPath(cfg.DBPath),
		ProbeHeartbeatPath: health.ProbeHeartbeatPath(cfg.DBPath),
		Logger:             logger,
		Now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

// RunCheck executes one network speed check, updates the database and SLA engine, and writes the heartbeat.
func (e *Engine) RunCheck(ctx context.Context) error {
	e.checkMu.Lock()
	if e.isChecking {
		e.checkMu.Unlock()
		e.Logger.Warn("speed test is already running, skipping scheduled tick")
		return nil
	}
	e.isChecking = true
	e.checkMu.Unlock()

	defer func() {
		e.checkMu.Lock()
		e.isChecking = false
		e.checkMu.Unlock()
	}()

	defer func() { e.notifyMu.Lock(); defer e.notifyMu.Unlock(); e.deliverPending(ctx) }()
	now := e.Now().UTC()
	e.Logger.Info("starting scheduled speed test check")

	check, err := e.Runner.Run(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		errMsg := err.Error()
		check = &store.NetworkCheck{
			Timestamp:      now,
			Status:         "measurement_error",
			Engine:         "ookla-cli",
			BelowThreshold: false,
			Error:          &errMsg,
		}
	}

	if check.Status == "ok" && check.DownloadMbps != nil && check.UploadMbps != nil {
		check.BelowThreshold = sla.IsBelowThreshold(
			*check.DownloadMbps, *check.UploadMbps,
			e.Config.ISPPlanThreshold, e.Config.ISPPlanUploadThreshold,
		)
	}

	// Persist check result
	_, saveErr := e.Store.SaveCheck(ctx, check)
	if saveErr != nil {
		e.Logger.Error("failed saving check to store", "error", saveErr)
	}

	// Touch heartbeat on check completion (success or measurement_error)
	if hbErr := health.WriteHeartbeat(e.HeartbeatPath); hbErr != nil {
		e.Logger.Warn("failed writing heartbeat", "path", e.HeartbeatPath, "error", hbErr)
	}

	if check.Status != "ok" {
		e.Logger.Warn("speed test finished with measurement error", "error", check.Error)
		return nil
	}

	e.Logger.Info("speed test completed successfully",
		"download_mbps", *check.DownloadMbps,
		"upload_mbps", *check.UploadMbps,
		"below_threshold", check.BelowThreshold,
	)

	// Evaluate SLA across rolling window
	windowCutoff := now.Add(-time.Duration(e.Config.SLAWindowHours) * time.Hour)
	checks, err := e.Store.GetRecentChecks(ctx, windowCutoff)
	if err != nil {
		e.Logger.Error("failed fetching recent checks for SLA evaluation", "error", err)
		return nil
	}

	evalCfg := sla.EvaluationConfig{
		PlanDownloadThreshold: e.Config.ISPPlanThreshold,
		PlanUploadThreshold:   e.Config.ISPPlanUploadThreshold,
		SLAWindowHours:        e.Config.SLAWindowHours,
		SLAViolationPct:       e.Config.SLAViolationPct,
		SLAMinCoverage:        e.Config.SLAMinCoverage,
		CheckIntervalMinutes:  e.Config.CheckIntervalMinutes,
	}

	eval := sla.Evaluate(checks, now, evalCfg)
	if eval.Status == sla.StatusInsufficientData {
		e.Logger.Info("SLA evaluation deferred: insufficient data",
			"valid_checks", eval.TotalValidCount,
			"min_required", eval.MinRequiredCount,
		)
		return nil
	}

	activeBreach, err := e.Store.GetActiveSLABreach(ctx)
	if err != nil {
		e.Logger.Error("failed querying active SLA breach episode", "error", err)
		return nil
	}

	action := sla.DecideAction(activeBreach, eval, now)
	switch action.Kind {
	case sla.ActionOpenBreach:
		csvData, _ := report.BuildCSV(eval.BreachedChecks)
		event := notify.BuildSLABreachEvent(
			e.Config.ISPPlanName,
			&eval,
			e.Config.ISPPlanThreshold,
			e.Config.ISPPlanUploadThreshold,
			e.Config.SLAWindowHours,
			e.Config.SLAViolationPct,
			csvData,
		)
		id, err := e.Store.StartSLABreach(ctx, action.Episode, event)
		if err != nil {
			e.Logger.Error("failed starting SLA breach episode in store", "error", err)
			return nil
		}
		action.Episode.ID = id
		e.Logger.Warn("SLA breach declared, alerting notifiers", "episode_id", id, "violation_pct", eval.ViolationPct)

	case sla.ActionUpdateBreach:
		if err := e.Store.UpdateSLABreach(ctx, action.Episode); err != nil {
			e.Logger.Error("failed updating SLA breach episode in store", "error", err)
		} else {
			e.Logger.Debug("SLA breach episode updated silently", "episode_id", action.Episode.ID)
		}

		csvData, _ := report.BuildCSV(eval.BreachedChecks)
		event := notify.BuildSLABreachEvent(e.Config.ISPPlanName, &eval, e.Config.ISPPlanThreshold, e.Config.ISPPlanUploadThreshold, e.Config.SLAWindowHours, e.Config.SLAViolationPct, csvData)
		if err := e.Store.QueueUnsentSLABreach(ctx, action.Episode.ID, event); err != nil {
			return err
		}

	case sla.ActionResolveBreach:
		event := notify.BuildSLAResolvedEvent(e.Config.ISPPlanName, action.Episode, now)
		if err := e.Store.EndSLABreach(ctx, action.EpisodeIDToEnd, now, event); err != nil {
			e.Logger.Error("failed closing SLA breach episode in store", "error", err)
			return nil
		}
		e.Logger.Info("SLA breach resolved, sending recovery alert", "episode_id", action.EpisodeIDToEnd)
	}

	_ = e.RunRetentionPrune(ctx)
	return nil
}

// RunRetentionPrune runs data retention pruning once per day if RetentionDays > 0.
func (e *Engine) RunRetentionPrune(ctx context.Context) error {
	if e.Config.RetentionDays <= 0 {
		return nil
	}
	now := e.Now().UTC()
	day := now.Format("2006-01-02")
	last, err := e.Store.GetValue(ctx, "last_retention_date")
	if err != nil {
		return err
	}
	if last == day {
		return nil
	}
	e.Logger.Info("pruning database records older than retention window", "retention_days", e.Config.RetentionDays)
	if _, err := e.Store.PruneOldData(ctx, e.Config.RetentionDays, now); err != nil {
		e.Logger.Error("failed pruning old data", "error", err)
		return err
	}
	return e.Store.SetValue(ctx, "last_retention_date", day)
}

// RunProbeRound executes one TCP connectivity probe across configured targets.
func (e *Engine) RunProbeRound(ctx context.Context) error {
	res := e.Prober.CheckRound(ctx)
	action := e.OutageSM.Step(res.OK, res.Reason, res.Timestamp)

	switch action.Kind {
	case outage.ActionOpen:
		e.Logger.Warn("connectivity outage detected, opening episode", "reason", action.Reason)
		ep := &store.OutageEpisode{
			StartedAt: action.StartedAt,
			Reason:    action.Reason,
		}
		id, err := e.Store.StartOutage(ctx, ep)
		if err != nil {
			e.Logger.Error("failed recording outage episode in store", "error", err)
		} else {
			e.OutageSM.SetActiveEpisodeID(id)
		}

	case outage.ActionRestore:
		e.Logger.Info("connectivity restored, closing outage episode",
			"episode_id", action.EpisodeID,
			"duration_seconds", action.DurationSeconds,
		)

		ep := &store.OutageEpisode{ID: action.EpisodeID, StartedAt: action.StartedAt, Reason: action.Reason}
		if ep.ID == 0 {
			id, err := e.Store.StartOutage(ctx, ep)
			if err != nil {
				e.Logger.Error("failed recording outage on restore", "error", err)
				e.OutageSM = outage.NewStateMachine(e.Config.ProbeFailRounds, ep)
				return err
			}
			action.EpisodeID = id
		}
		event := notify.BuildOutageRestoredEvent(e.Config.ISPPlanName, action.DurationSeconds)
		if err := e.Store.EndOutage(ctx, action.EpisodeID, action.EndedAt, action.DurationSeconds, event); err != nil {
			e.Logger.Error("failed ending outage episode in store", "error", err)
			e.OutageSM = outage.NewStateMachine(e.Config.ProbeFailRounds, ep)
			return err
		}

	}

	if res.OK {
		e.retryNotifications(ctx)
	}

	// Touch probe heartbeat on probe round completion
	if hbErr := health.WriteHeartbeat(e.ProbeHeartbeatPath); hbErr != nil {
		e.Logger.Warn("failed writing probe heartbeat", "path", e.ProbeHeartbeatPath, "error", hbErr)
	}

	return nil
}

// SendPeriodicSummary compiles and sends a summary report for the past N hours.
func (e *Engine) SendPeriodicSummary(ctx context.Context, hours int) error {
	loc := e.Config.Location
	if loc == nil {
		loc = time.UTC
	}
	now := e.Now().In(loc)
	start := now.Add(-time.Duration(hours) * time.Hour)

	checks, err := e.Store.GetChecksBetween(ctx, start, now)
	if err != nil {
		return fmt.Errorf("failed fetching checks for summary: %w", err)
	}
	breaches, _ := e.Store.GetSLABreachEpisodesBetween(ctx, start, now)
	outages, _ := e.Store.GetOutageEpisodesBetween(ctx, start, now)
	openBreach, _ := e.Store.GetActiveSLABreach(ctx)
	openOutage, _ := e.Store.GetActiveOutage(ctx)

	summary := report.BuildSummaryData(
		start, now, checks, breaches, outages, openBreach, openOutage,
		e.Config.SpeedAlertDownload, e.Config.SpeedAlertUpload,
		e.Config.ISPPlanName,
		loc,
	)

	e.Logger.Info("dispatching periodic network summary", "hours", hours)
	event := notify.BuildSummaryEvent(summary)
	event.Body = report.SummaryText(summary, loc)
	return e.Dispatcher.Dispatch(ctx, event)
}

// SendDailyReport compiles and sends the 24-hour daily report with CSV and PDF evidence.
func (e *Engine) SendDailyReport(ctx context.Context) error {
	loc := e.Config.Location
	if loc == nil {
		loc = time.UTC
	}
	now := e.Now().In(loc)
	start := now.Add(-24 * time.Hour)

	checks, err := e.Store.GetChecksBetween(ctx, start, now)
	if err != nil {
		return fmt.Errorf("failed fetching checks for daily report: %w", err)
	}
	breaches, _ := e.Store.GetSLABreachEpisodesBetween(ctx, start, now)
	outages, _ := e.Store.GetOutageEpisodesBetween(ctx, start, now)
	openBreach, _ := e.Store.GetActiveSLABreach(ctx)
	openOutage, _ := e.Store.GetActiveOutage(ctx)

	summary := report.BuildSummaryData(
		start, now, checks, breaches, outages, openBreach, openOutage,
		e.Config.SpeedAlertDownload, e.Config.SpeedAlertUpload,
		e.Config.ISPPlanName,
		loc,
	)

	csvBytes, err := report.BuildCSV(checks)
	if err != nil {
		e.Logger.Error("failed building daily report csv", "error", err)
	}

	pdfBytes, err := report.BuildPDF(summary, loc)
	if err != nil {
		e.Logger.Error("failed building daily report pdf", "error", err)
	}

	e.Logger.Info("dispatching daily report with attachments")
	event := notify.BuildDailyReportEvent(summary, csvBytes, pdfBytes)
	return e.Dispatcher.Dispatch(ctx, event)
}

// Start launches the daemon loops (speed check, probe, reports) under ctx until cancelled.
func (e *Engine) Start(ctx context.Context) error {
	// Touch heartbeat at startup
	if err := health.WriteHeartbeat(e.HeartbeatPath); err != nil {
		e.Logger.Warn("failed writing startup heartbeat", "error", err)
	}
	if err := health.WriteHeartbeat(e.ProbeHeartbeatPath); err != nil {
		e.Logger.Warn("failed writing startup probe heartbeat", "error", err)
	}

	// Run initial check immediately
	e.Logger.Info("running initial speed check on startup")
	_ = e.RunCheck(ctx)

	var wg sync.WaitGroup

	// Loop 1: Speed test loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Duration(e.Config.CheckIntervalMinutes) * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							e.Logger.Error("recovered panic in speed check loop",
								"panic", r,
								"stack", string(debug.Stack()),
							)
						}
					}()
					_ = e.RunCheck(ctx)
				}()
			}
		}
	}()

	// Loop 2: TCP probe loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Duration(e.Config.ProbeIntervalSeconds) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							e.Logger.Error("recovered panic in probe loop",
								"panic", r,
								"stack", string(debug.Stack()),
							)
						}
					}()
					_ = e.RunProbeRound(ctx)
				}()
			}
		}
	}()

	// Loop 3: Report scheduler loop (checks every 30s)
	if err := e.RunSummaryTick(ctx); err != nil {
		e.Logger.Error("summary tick failed", "error", err)
	}
	if err := e.RunReportTick(ctx); err != nil {
		e.Logger.Error("startup report tick failed", "error", err)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()

		minuteTicker := time.NewTicker(30 * time.Second)
		defer minuteTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return

			case <-minuteTicker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							e.Logger.Error("recovered panic in daily report loop",
								"panic", r,
								"stack", string(debug.Stack()),
							)
						}
					}()

					if err := e.RunSummaryTick(ctx); err != nil {
						e.Logger.Error("summary tick failed", "error", err)
					}
					if err := e.RunReportTick(ctx); err != nil {
						e.Logger.Error("daily report tick failed", "error", err)
					}
				}()
			}
		}
	}()

	<-ctx.Done()
	e.Logger.Info("daemon context canceled, waiting for loops to terminate")
	wg.Wait()
	e.notifyWG.Wait()
	e.Logger.Info("all daemon loops exited cleanly")
	return nil
}

func (e *Engine) retryNotifications(ctx context.Context) {
	if !e.notifyMu.TryLock() {
		return
	}
	e.notifyWG.Add(1)
	go func() { defer e.notifyWG.Done(); defer e.notifyMu.Unlock(); e.deliverPending(ctx) }()
}
func (e *Engine) deliverPending(ctx context.Context) {
	pending, err := e.Store.PendingNotifications(ctx)
	if err != nil {
		e.Logger.Error("reading pending alerts", "error", err)
		return
	}
	for _, p := range pending {
		var event notify.Event
		if err := json.Unmarshal(p.Payload, &event); err != nil {
			e.Logger.Error("decoding pending alert", "error", err)
			continue
		}
		if err := e.Dispatcher.Dispatch(ctx, event); err != nil {
			continue
		}
		if err := e.Store.CompleteNotification(ctx, p, e.Now()); err != nil {
			e.Logger.Error("marking alert delivered", "error", err)
		}
	}
}

// RunReportTick sends today's report once its configured local time has passed.
func (e *Engine) RunReportTick(ctx context.Context) error {
	e.reportMu.Lock()
	defer e.reportMu.Unlock()
	now := e.Now().In(e.Config.Location)
	target := time.Date(now.Year(), now.Month(), now.Day(), e.Config.DailyReportHour, e.Config.DailyReportMinute, 0, 0, e.Config.Location)
	if now.Before(target) {
		return nil
	}
	day := now.Format("2006-01-02")
	last, err := e.Store.GetValue(ctx, "last_daily_date")
	if err != nil {
		return err
	}
	if last == day {
		return nil
	}
	if err := e.SendDailyReport(ctx); err != nil {
		return err
	}
	return e.Store.SetValue(ctx, "last_daily_date", day)
}

// RunSummaryTick sends a summary when the persisted interval has elapsed.
func (e *Engine) RunSummaryTick(ctx context.Context) error {
	e.reportMu.Lock()
	defer e.reportMu.Unlock()
	now := e.Now().UTC()
	value, err := e.Store.GetValue(ctx, "last_summary_time")
	if err != nil {
		return err
	}
	if value == "" {
		return e.Store.SetValue(ctx, "last_summary_time", now.Format(time.RFC3339Nano))
	}
	last, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return err
	}
	if now.Sub(last) < time.Duration(e.Config.SummaryIntervalHours)*time.Hour {
		return nil
	}
	if err := e.SendPeriodicSummary(ctx, e.Config.SummaryIntervalHours); err != nil {
		return err
	}
	return e.Store.SetValue(ctx, "last_summary_time", now.Format(time.RFC3339Nano))
}
