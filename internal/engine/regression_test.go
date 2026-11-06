package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/config"
	"github.com/dexterbrylle/isp-speed-monitor/internal/health"
	"github.com/dexterbrylle/isp-speed-monitor/internal/notify"
	"github.com/dexterbrylle/isp-speed-monitor/internal/outage"
	"github.com/dexterbrylle/isp-speed-monitor/internal/probe"
	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

type blockingNotifier struct {
	called  chan notify.Event
	release chan struct{}
}

func (n *blockingNotifier) Name() string { return "blocking" }
func (n *blockingNotifier) Notify(ctx context.Context, ev notify.Event) error {
	n.called <- ev
	select {
	case <-n.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestProbeDoesNotNotifyWhileDownOrBlockOnRestore(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	n := &blockingNotifier{make(chan notify.Event, 2), make(chan struct{})}

	p := probe.New([]string{"test:443"}, 1)
	up := false
	p.Dial = func(context.Context, string, string) error {
		if up {
			return nil
		}
		return errors.New("offline")
	}
	e := New(&config.Config{DBPath: filepath.Join(t.TempDir(), "ispmon.db")}, st, nil, p, outage.NewStateMachine(1, nil), notify.NewDispatcher([]notify.Notifier{n}, nil), nil)
	defer func() { close(n.release); e.notifyWG.Wait() }()
	done := make(chan struct{})
	go func() { _ = e.RunProbeRound(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("down probe blocked on notification")
	}
	select {
	case <-n.called:
		t.Fatal("notification attempted while down")
	default:
	}
	up = true
	done = make(chan struct{})
	go func() { _ = e.RunProbeRound(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("restore probe blocked on notification")
	}
	select {
	case ev := <-n.called:
		if ev.Kind != notify.EventOutageRestored {
			t.Fatal(ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("missing restored event")
	}
}

type failingNotifier struct {
	calls int
	fail  bool
}

func (n *failingNotifier) Name() string { return "failure" }
func (n *failingNotifier) Notify(context.Context, notify.Event) error {
	n.calls++
	if n.fail {
		return errors.New("rejected")
	}
	return nil
}
func TestPendingAlertRetriesAfterRestart(t *testing.T) {
	path := t.TempDir() + "/db"
	st, err := store.New(path)
	if err != nil {
		t.Fatal(err)
	}
	n := &failingNotifier{fail: true}
	e := New(&config.Config{}, st, nil, nil, nil, notify.NewDispatcher([]notify.Notifier{n}, nil), nil)
	ep := &store.SLABreachEpisode{StartedAt: time.Now()}
	id, err := st.StartSLABreach(context.Background(), ep)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.QueueNotification(context.Background(), "sla_breach_episodes", id, notify.Event{Kind: notify.EventSLABreach}); err != nil {
		t.Fatal(err)
	}
	e.deliverPending(context.Background())
	st.Close()
	st, err = store.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	e.Store = st
	n.fail = false
	e.deliverPending(context.Background())
	e.deliverPending(context.Background())
	if n.calls != 2 {
		t.Fatalf("want two attempts, got %d", n.calls)
	}
}

type testRunner struct{ check *store.NetworkCheck }

func (r testRunner) Run(context.Context) (*store.NetworkCheck, error) { return r.check, nil }
func TestSLABreachRetriesOnLaterCheck(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	n := &failingNotifier{fail: true}
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	speed := 50.0
	cfg := &config.Config{DBPath: t.TempDir() + "/db", ISPPlanThreshold: 100, ISPPlanUploadThreshold: 100, SLAWindowHours: 1, SLAViolationPct: 20, SLAMinCoverage: 1, CheckIntervalMinutes: 60}
	r := testRunner{&store.NetworkCheck{Timestamp: now, Status: "ok", Engine: "test", DownloadMbps: &speed, UploadMbps: &speed}}
	e := New(cfg, st, r, nil, nil, notify.NewDispatcher([]notify.Notifier{n}, nil), nil)
	e.Now = func() time.Time { return now }
	if err := e.RunCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	active, err := st.GetActiveSLABreach(context.Background())
	if err != nil || active == nil {
		t.Fatalf("missing breach: %v", err)
	}
	n.fail = false
	now = now.Add(time.Minute)
	r.check.Timestamp = now
	if err := e.RunCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.RunCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.calls != 2 {
		t.Fatalf("breach delivery attempts = %d, want 2", n.calls)
	}
}

func TestDailyReportCatchesUpAndSurvivesRestart(t *testing.T) {
	path := t.TempDir() + "/db"
	st, err := store.New(path)
	if err != nil {
		t.Fatal(err)
	}
	n := &failingNotifier{}
	cfg := &config.Config{Location: time.UTC, DailyReportHour: 10, SummaryIntervalHours: 6}
	e := New(cfg, st, nil, nil, nil, notify.NewDispatcher([]notify.Notifier{n}, nil), nil)
	now := time.Date(2026, 10, 11, 10, 5, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	if err := e.RunReportTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.calls != 1 {
		t.Fatalf("missed target: reports = %d, want 1", n.calls)
	}
	st.Close()
	st, err = store.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	e.Store = st
	if err := e.RunReportTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.calls != 1 {
		t.Fatalf("duplicate after restart: %d", n.calls)
	}
	now = now.Add(24 * time.Hour)
	n.fail = true
	if err := e.RunReportTick(context.Background()); err == nil {
		t.Fatal("failed report treated as sent")
	}
	n.fail = false
	if err := e.RunReportTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.calls != 3 {
		t.Fatalf("daily retry attempts = %d", n.calls)
	}
}

func TestSummaryIntervalSurvivesRestart(t *testing.T) {
	path := t.TempDir() + "/db"
	st, err := store.New(path)
	if err != nil {
		t.Fatal(err)
	}
	n := &failingNotifier{}
	cfg := &config.Config{SummaryIntervalHours: 6}
	e := New(cfg, st, nil, nil, nil, notify.NewDispatcher([]notify.Notifier{n}, nil), nil)
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	if err := e.RunSummaryTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Hour)
	st.Close()
	st, err = store.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	e.Store = st
	if err := e.RunSummaryTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.calls != 0 {
		t.Fatal("summary sent too early")
	}
	now = now.Add(time.Hour)
	if err := e.RunSummaryTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.calls != 1 {
		t.Fatalf("elapsed summary reports = %d, want 1", n.calls)
	}
	if err := e.RunSummaryTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.calls != 1 {
		t.Fatal("duplicate summary")
	}
}

func TestOutageRecordedOnRestoreAfterStartFails(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := probe.New([]string{"test:443"}, 1)
	up := false
	p.Dial = func(context.Context, string, string) error {
		if up {
			return nil
		}
		return errors.New("offline")
	}
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	p.Now = func() time.Time { return now }
	e := New(&config.Config{DBPath: filepath.Join(t.TempDir(), "ispmon.db")}, st, nil, p, outage.NewStateMachine(1, nil), notify.NewDispatcher(nil, nil), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = e.RunProbeRound(ctx)
	up = true
	now = now.Add(time.Minute)
	if err := e.RunProbeRound(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.notifyWG.Wait()
	episodes, err := st.GetOutageEpisodesBetween(context.Background(), now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 1 {
		t.Fatalf("restored outage records = %d, want 1", len(episodes))
	}
	if episodes[0].EndedAt == nil || *episodes[0].DurationSeconds != 60 {
		t.Fatalf("missing restore duration: %+v", episodes[0])
	}
}

func TestEngine_RunRetentionPrune(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{RetentionDays: 90}
	e := New(cfg, st, nil, nil, nil, notify.NewDispatcher(nil, nil), nil)
	e.Now = func() time.Time { return now }

	ctx := context.Background()

	oldCheck := &store.NetworkCheck{
		Timestamp: now.AddDate(0, 0, -100),
		Status:    "ok",
		Engine:    "ookla-cli",
	}
	if _, err := st.SaveCheck(ctx, oldCheck); err != nil {
		t.Fatal(err)
	}

	if err := e.RunRetentionPrune(ctx); err != nil {
		t.Fatalf("unexpected error in RunRetentionPrune: %v", err)
	}

	checks, err := st.GetRecentChecks(ctx, now.AddDate(0, 0, -200))
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 0 {
		t.Fatalf("expected 0 checks after prune, got %d", len(checks))
	}

	val, err := st.GetValue(ctx, "last_retention_date")
	if err != nil {
		t.Fatal(err)
	}
	if val != "2026-10-11" {
		t.Fatalf("expected last_retention_date '2026-10-11', got %q", val)
	}

	if err := e.RunRetentionPrune(ctx); err != nil {
		t.Fatalf("unexpected error in second RunRetentionPrune: %v", err)
	}
}

func TestEngine_ProbeHeartbeatTouched(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "ispmon.db")
	cfg := &config.Config{
		DBPath:               dbPath,
		ProbeIntervalSeconds: 60,
		ProbeFailRounds:      3,
	}

	p := probe.New([]string{"127.0.0.1:443"}, 1)
	p.Dial = func(context.Context, string, string) error { return nil }

	e := New(cfg, st, nil, p, outage.NewStateMachine(3, nil), notify.NewDispatcher(nil, nil), nil)

	ctx := context.Background()

	if err := health.CheckHeartbeat(e.ProbeHeartbeatPath, time.Minute, time.Now()); err == nil {
		t.Fatal("expected probe heartbeat to not exist yet")
	}

	if err := e.RunProbeRound(ctx); err != nil {
		t.Fatalf("unexpected error in RunProbeRound: %v", err)
	}

	if err := health.CheckHeartbeat(e.ProbeHeartbeatPath, time.Minute, time.Now().UTC()); err != nil {
		t.Fatalf("expected probe heartbeat to be fresh: %v", err)
	}
}

func TestEngine_SummaryTimezoneLabel(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var dispatched notify.Event
	mockNotifier := &recordingNotifier{
		onNotify: func(e notify.Event) {
			dispatched = e
		},
	}

	nyLoc := time.FixedZone("EDT", -4*3600)
	cfg := &config.Config{
		ISPPlanName: "FastFiber",
		Location:    nyLoc,
	}

	e := New(cfg, st, nil, nil, nil, notify.NewDispatcher([]notify.Notifier{mockNotifier}, nil), nil)
	e.Now = func() time.Time {
		return time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC) // 16:00 EDT
	}

	if err := e.SendPeriodicSummary(context.Background(), 6); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedBody := "Performance overview for period 10:00 to 16:00 EDT."
	if dispatched.Body != expectedBody {
		t.Errorf("expected summary body %q, got %q", expectedBody, dispatched.Body)
	}
}

type recordingNotifier struct {
	onNotify func(notify.Event)
}

func (r *recordingNotifier) Name() string { return "recorder" }
func (r *recordingNotifier) Notify(ctx context.Context, e notify.Event) error {
	if r.onNotify != nil {
		r.onNotify(e)
	}
	return nil
}
