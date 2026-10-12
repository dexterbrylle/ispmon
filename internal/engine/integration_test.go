package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/config"
	"github.com/dexterbrylle/isp-speed-monitor/internal/engine"
	"github.com/dexterbrylle/isp-speed-monitor/internal/health"
	"github.com/dexterbrylle/isp-speed-monitor/internal/notify"
	"github.com/dexterbrylle/isp-speed-monitor/internal/outage"
	"github.com/dexterbrylle/isp-speed-monitor/internal/probe"
	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

type mockSpeedtestRunner struct {
	mu        sync.Mutex
	nextCheck *store.NetworkCheck
	nextErr   error
}

func (m *mockSpeedtestRunner) Run(ctx context.Context) (*store.NetworkCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nextErr != nil {
		return nil, m.nextErr
	}
	return m.nextCheck, nil
}

func (m *mockSpeedtestRunner) set(check *store.NetworkCheck, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextCheck = check
	m.nextErr = err
}

type recordingNotifier struct {
	mu     sync.Mutex
	events []notify.Event
}

func (r *recordingNotifier) Name() string { return "recorder" }

func (r *recordingNotifier) Notify(ctx context.Context, e notify.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recordingNotifier) getEvents() []notify.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]notify.Event, len(r.events))
	copy(cp, r.events)
	return cp
}

func (r *recordingNotifier) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

func TestPipeline_Integration(t *testing.T) {
	ctx := context.Background()

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("failed creating store: %v", err)
	}
	defer st.Close()

	tmpDir := t.TempDir()
	hbPath := health.HeartbeatPath(tmpDir + "/ispmon.db")

	cfg := &config.Config{
		ISPPlanName:            "GFiber 500 Plan",
		ISPPlanSpeed:           500.0,
		ISPPlanUploadSpeed:     500.0,
		ISPPlanThreshold:       150.0,
		ISPPlanUploadThreshold: 150.0,
		SLAWindowHours:         24,
		SLAViolationPct:        20.0,
		SLAMinCoverage:         0.75,
		CheckIntervalMinutes:   30,
		ProbeIntervalSeconds:   60,
		ProbeFailRounds:        3,
		ProbeTimeoutSeconds:    3,
		DBPath:                 tmpDir + "/ispmon.db",
	}

	currentTime := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	getTime := func() time.Time { return currentTime }

	runner := &mockSpeedtestRunner{}

	probeTargets := []string{"1.1.1.1:443", "9.9.9.9:443"}
	prober := probe.New(probeTargets, 3)
	proberDialSuccess := true
	prober.Dial = func(ctx context.Context, network, address string) error {
		if !proberDialSuccess {
			return errors.New("network unreachable")
		}
		return nil
	}
	prober.Now = getTime

	outageSM := outage.NewStateMachine(cfg.ProbeFailRounds, nil)
	recorder := &recordingNotifier{}
	dispatcher := notify.NewDispatcher([]notify.Notifier{recorder}, nil)

	eng := engine.New(cfg, st, runner, prober, outageSM, dispatcher, nil)
	eng.HeartbeatPath = hbPath
	eng.Now = getTime

	dl500, ul500 := 500.0, 500.0
	for i := 35; i >= 1; i-- {
		chk := &store.NetworkCheck{
			Timestamp:      currentTime.Add(-time.Duration(i*30) * time.Minute),
			Status:         "ok",
			Engine:         "ookla-cli",
			DownloadMbps:   &dl500,
			UploadMbps:     &ul500,
			BelowThreshold: false,
		}
		if _, err := st.SaveCheck(ctx, chk); err != nil {
			t.Fatalf("failed seeding check: %v", err)
		}
	}

	runner.set(&store.NetworkCheck{
		Timestamp:      currentTime,
		Status:         "ok",
		Engine:         "ookla-cli",
		DownloadMbps:   &dl500,
		UploadMbps:     &ul500,
		BelowThreshold: false,
	}, nil)

	if err := eng.RunCheck(ctx); err != nil {
		t.Fatalf("RunCheck failed: %v", err)
	}

	if err := health.CheckHeartbeat(hbPath, 5*time.Minute, currentTime); err != nil {
		t.Errorf("heartbeat was not written on check completion: %v", err)
	}

	if len(recorder.getEvents()) != 0 {
		t.Fatalf("expected no events on normal check, got %d", len(recorder.getEvents()))
	}

	currentTime = currentTime.Add(30 * time.Minute)
	runner.set(nil, errors.New("ookla cli timed out"))

	proberDialSuccess = true
	if err := eng.RunProbeRound(ctx); err != nil {
		t.Fatalf("RunProbeRound failed: %v", err)
	}

	if err := eng.RunCheck(ctx); err != nil {
		t.Fatalf("RunCheck on measurement error failed: %v", err)
	}

	recentChecks, err := st.GetRecentChecks(ctx, currentTime.Add(-1*time.Minute))
	if err != nil || len(recentChecks) == 0 {
		t.Fatalf("failed retrieving recent checks: %v", err)
	}
	latest := recentChecks[len(recentChecks)-1]
	if latest.Status != "measurement_error" {
		t.Errorf("expected status 'measurement_error', got %q", latest.Status)
	}
	if latest.Error == nil || *latest.Error != "ookla cli timed out" {
		t.Errorf("expected recorded error message, got %v", latest.Error)
	}

	if activeOutage, _ := st.GetActiveOutage(ctx); activeOutage != nil {
		t.Errorf("measurement error should NOT open an outage")
	}
	if len(recorder.getEvents()) != 0 {
		t.Errorf("measurement error should not trigger notifications")
	}

	dl100, ul100 := 100.0, 100.0
	testURL := "https://www.speedtest.net/result/c/slow-1"
	for i := 0; i < 10; i++ {
		currentTime = currentTime.Add(30 * time.Minute)
		runner.set(&store.NetworkCheck{
			Timestamp:      currentTime,
			Status:         "ok",
			Engine:         "ookla-cli",
			DownloadMbps:   &dl100,
			UploadMbps:     &ul100,
			ResultURL:      &testURL,
			BelowThreshold: true,
		}, nil)
		if err := eng.RunCheck(ctx); err != nil {
			t.Fatalf("RunCheck failed on slow tick: %v", err)
		}
	}

	activeBreach, err := st.GetActiveSLABreach(ctx)
	if err != nil || activeBreach == nil {
		t.Fatalf("expected open SLA breach episode, got nil")
	}
	if activeBreach.WorstDownloadMbps == nil || *activeBreach.WorstDownloadMbps != 100.0 {
		t.Errorf("expected worst download 100.0, got %v", activeBreach.WorstDownloadMbps)
	}

	events := recorder.getEvents()
	var breachAlerts int
	for _, ev := range events {
		if ev.Kind == notify.EventSLABreach {
			breachAlerts++
		}
	}
	if breachAlerts != 1 {
		t.Fatalf("expected exactly 1 SLA breach alert, got %d", breachAlerts)
	}

	recorder.clear()

	for i := 0; i < 48; i++ {
		currentTime = currentTime.Add(30 * time.Minute)
		runner.set(&store.NetworkCheck{
			Timestamp:      currentTime,
			Status:         "ok",
			Engine:         "ookla-cli",
			DownloadMbps:   &dl500,
			UploadMbps:     &ul500,
			BelowThreshold: false,
		}, nil)
		if err := eng.RunCheck(ctx); err != nil {
			t.Fatalf("RunCheck failed on recovery tick: %v", err)
		}
	}

	activeBreachAfter, err := st.GetActiveSLABreach(ctx)
	if err != nil {
		t.Fatalf("failed checking breach after recovery: %v", err)
	}
	if activeBreachAfter != nil {
		t.Errorf("expected SLA breach episode to be closed after recovery")
	}

	events = recorder.getEvents()
	var resolvedAlerts int
	for _, ev := range events {
		if ev.Kind == notify.EventSLAResolved {
			resolvedAlerts++
		}
	}
	if resolvedAlerts != 1 {
		t.Fatalf("expected exactly 1 SLA resolved alert, got %d", resolvedAlerts)
	}

	recorder.clear()

	t1 := currentTime.Add(60 * time.Second)
	currentTime = t1
	proberDialSuccess = false
	if err := eng.RunProbeRound(ctx); err != nil {
		t.Fatalf("probe round 1 failed: %v", err)
	}
	if len(recorder.getEvents()) != 0 {
		t.Errorf("outage should not open after only 1 round failure")
	}

	t2 := t1.Add(60 * time.Second)
	currentTime = t2
	if err := eng.RunProbeRound(ctx); err != nil {
		t.Fatalf("probe round 2 failed: %v", err)
	}
	if len(recorder.getEvents()) != 0 {
		t.Errorf("outage should not open after only 2 round failures")
	}

	t3 := t2.Add(60 * time.Second)
	currentTime = t3
	if err := eng.RunProbeRound(ctx); err != nil {
		t.Fatalf("probe round 3 failed: %v", err)
	}

	activeOutage, err := st.GetActiveOutage(ctx)
	if err != nil || activeOutage == nil {
		t.Fatalf("expected active outage episode in store, got nil")
	}
	if !activeOutage.StartedAt.Equal(t1) {
		t.Errorf("expected outage started_at to be t1 (%v), got %v", t1, activeOutage.StartedAt)
	}

	events = recorder.getEvents()
	if len(events) != 0 {
		t.Fatalf("expected no alert while offline, got %v", events)
	}

	currentTime = t3.Add(60 * time.Second)
	if err := eng.RunProbeRound(ctx); err != nil {
		t.Fatalf("probe round 4 failed: %v", err)
	}
	if len(recorder.getEvents()) != 0 {
		t.Errorf("expected no duplicate outage alert while still down")
	}

	t5 := currentTime.Add(60 * time.Second)
	currentTime = t5
	proberDialSuccess = true
	if err := eng.RunProbeRound(ctx); err != nil {
		t.Fatalf("probe round 5 failed: %v", err)
	}

	activeOutageAfter, err := st.GetActiveOutage(ctx)
	if err != nil {
		t.Fatalf("failed checking outage after restoration: %v", err)
	}
	if activeOutageAfter != nil {
		t.Errorf("expected outage episode to be closed after connectivity restored")
	}

	deadline := time.Now().Add(time.Second)
	for len(recorder.getEvents()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	events = recorder.getEvents()
	if len(events) != 1 || events[0].Kind != notify.EventOutageRestored {
		t.Fatalf("expected 1 outage_restored event, got %v", events)
	}
}
