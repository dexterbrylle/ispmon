package engine

import (
	"context"
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
	Dispatcher    *notify.Dispatcher
	HeartbeatPath string
	Logger        *slog.Logger
	Now           func() time.Time

	checkMu      sync.Mutex
	isChecking   bool
	lastDailyDay int // day of year daily report was last dispatched
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
		Config:        cfg,
		Store:         st,
		Runner:        runner,
		Prober:        prober,
		OutageSM:      outageSM,
		Dispatcher:    dispatcher,
		HeartbeatPath: health.HeartbeatPath(cfg.DBPath),
		Logger:        logger,
		Now: func() time.Time {
			return time.Now().UTC()
		},
		lastDailyDay: -1,
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
		id, err := e.Store.StartSLABreach(ctx, action.Episode)
		if err != nil {
			e.Logger.Error("failed starting SLA breach episode in store", "error", err)
			return nil
		}
		action.Episode.ID = id
		e.Logger.Warn("SLA breach declared, alerting notifiers", "episode_id", id, "violation_pct", eval.ViolationPct)

		csvData, _ := report.BuildCSV(eval.BreachedChecks)
		event := notify.BuildSLABreachEvent(
			e.Config.ISPPlanName,
			&eval,
			e.Config.ISPPlanThreshold,
			e.Config.ISPPlanUploadThreshold,
			e.Config.SLAWindowHours,
			csvData,
		)
		e.Dispatcher.Dispatch(ctx, event)

	case sla.ActionUpdateBreach:
		if err := e.Store.UpdateSLABreach(ctx, action.Episode); err != nil {
			e.Logger.Error("failed updating SLA breach episode in store", "error", err)
		} else {
			e.Logger.Debug("SLA breach episode updated silently", "episode_id", action.Episode.ID)
		}

	case sla.ActionResolveBreach:
		if err := e.Store.EndSLABreach(ctx, action.EpisodeIDToEnd, now); err != nil {
			e.Logger.Error("failed closing SLA breach episode in store", "error", err)
			return nil
		}
		e.Logger.Info("SLA breach resolved, sending recovery alert", "episode_id", action.EpisodeIDToEnd)
		event := notify.BuildSLAResolvedEvent(e.Config.ISPPlanName, action.Episode, now)
		e.Dispatcher.Dispatch(ctx, event)
	}

	return nil
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

		event := notify.BuildOutageStartedEvent(e.Config.ISPPlanName, action.Reason)
		e.Dispatcher.Dispatch(ctx, event)

	case outage.ActionRestore:
		e.Logger.Info("connectivity restored, closing outage episode",
			"episode_id", action.EpisodeID,
			"duration_seconds", action.DurationSeconds,
		)
		if action.EpisodeID > 0 {
			if err := e.Store.EndOutage(ctx, action.EpisodeID, action.EndedAt, action.DurationSeconds); err != nil {
				e.Logger.Error("failed ending outage episode in store", "error", err)
			}
		}

		event := notify.BuildOutageRestoredEvent(e.Config.ISPPlanName, action.DurationSeconds)
		e.Dispatcher.Dispatch(ctx, event)
	}

	return nil
}

// SendPeriodicSummary compiles and sends a summary report for the past N hours.
func (e *Engine) SendPeriodicSummary(ctx context.Context, hours int) error {
	now := e.Now().UTC()
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
	)

	e.Logger.Info("dispatching periodic network summary", "hours", hours)
	event := notify.BuildSummaryEvent(summary)
	e.Dispatcher.Dispatch(ctx, event)
	return nil
}

// SendDailyReport compiles and sends the 24-hour daily report with CSV and PDF evidence.
func (e *Engine) SendDailyReport(ctx context.Context) error {
	now := e.Now().UTC()
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
	)

	csvBytes, err := report.BuildCSV(checks)
	if err != nil {
		e.Logger.Error("failed building daily report csv", "error", err)
	}

	pdfBytes, err := report.BuildPDF(summary, e.Config.Location)
	if err != nil {
		e.Logger.Error("failed building daily report pdf", "error", err)
	}

	e.Logger.Info("dispatching daily report with attachments")
	event := notify.BuildDailyReportEvent(summary, csvBytes, pdfBytes)
	e.Dispatcher.Dispatch(ctx, event)
	return nil
}

// Start launches the daemon loops (speed check, probe, reports) under ctx until cancelled.
func (e *Engine) Start(ctx context.Context) error {
	// Touch heartbeat at startup
	if err := health.WriteHeartbeat(e.HeartbeatPath); err != nil {
		e.Logger.Warn("failed writing startup heartbeat", "error", err)
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		summaryTicker := time.NewTicker(time.Duration(e.Config.SummaryIntervalHours) * time.Hour)
		defer summaryTicker.Stop()

		minuteTicker := time.NewTicker(30 * time.Second)
		defer minuteTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-summaryTicker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							e.Logger.Error("recovered panic in summary report loop",
								"panic", r,
								"stack", string(debug.Stack()),
							)
						}
					}()
					_ = e.SendPeriodicSummary(ctx, e.Config.SummaryIntervalHours)
				}()

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

					nowInTZ := e.Now().In(e.Config.Location)
					if nowInTZ.Hour() == e.Config.DailyReportHour && nowInTZ.Minute() == e.Config.DailyReportMinute {
						currentDay := nowInTZ.YearDay()
						if e.lastDailyDay != currentDay {
							e.lastDailyDay = currentDay
							_ = e.SendDailyReport(ctx)
						}
					}
				}()
			}
		}
	}()

	<-ctx.Done()
	e.Logger.Info("daemon context canceled, waiting for loops to terminate")
	wg.Wait()
	e.Logger.Info("all daemon loops exited cleanly")
	return nil
}
