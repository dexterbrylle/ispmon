package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("failed to create in-memory store: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
	})
	return s
}

func TestStore_Migrations_V0_to_V1(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed opening sqlite: %v", err)
	}
	defer db.Close()

	v0, err := store.GetUserVersion(db)
	if err != nil {
		t.Fatalf("failed getting initial version: %v", err)
	}
	if v0 != 0 {
		t.Fatalf("expected initial version 0, got %d", v0)
	}

	if err := store.ApplyMigrations(db); err != nil {
		t.Fatalf("failed applying migrations: %v", err)
	}

	v1, err := store.GetUserVersion(db)
	if err != nil {
		t.Fatalf("failed getting migrated version: %v", err)
	}
	if v1 != 2 {
		t.Fatalf("expected version 1 after migration, got %d", v1)
	}

	// Idempotency check: running ApplyMigrations again should be a no-op
	if err := store.ApplyMigrations(db); err != nil {
		t.Fatalf("subsequent migration run failed: %v", err)
	}
	v1After, _ := store.GetUserVersion(db)
	if v1After != 2 {
		t.Fatalf("expected version to remain 1, got %d", v1After)
	}
}

func TestStore_SaveAndRetrieveChecks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	dl := 450.5
	ul := 420.2
	lat := 12.3
	jit := 1.5
	loss := 0.0
	srvID := int64(1234)
	srvName := "Manila Server"
	srvLoc := "Manila, PH"
	resURL := "https://www.speedtest.net/result/c/123"
	pubIP := "120.28.1.2"

	check1 := &store.NetworkCheck{
		Timestamp:      now.Add(-10 * time.Minute),
		Status:         "ok",
		Engine:         "ookla-cli",
		DownloadMbps:   &dl,
		UploadMbps:     &ul,
		LatencyMs:      &lat,
		JitterMs:       &jit,
		PacketLossPct:  &loss,
		ServerID:       &srvID,
		ServerName:     &srvName,
		ServerLocation: &srvLoc,
		ResultURL:      &resURL,
		PublicIP:       &pubIP,
		BelowThreshold: false,
	}

	id1, err := s.SaveCheck(ctx, check1)
	if err != nil {
		t.Fatalf("failed saving check 1: %v", err)
	}
	if id1 <= 0 {
		t.Fatalf("expected positive ID, got %d", id1)
	}

	errMsg := "Ookla CLI timed out"
	check2 := &store.NetworkCheck{
		Timestamp:      now,
		Status:         "measurement_error",
		Engine:         "ookla-cli",
		BelowThreshold: false,
		Error:          &errMsg,
	}
	id2, err := s.SaveCheck(ctx, check2)
	if err != nil {
		t.Fatalf("failed saving check 2: %v", err)
	}
	if id2 <= id1 {
		t.Fatalf("expected id2 > id1, got %d <= %d", id2, id1)
	}

	checks, err := s.GetRecentChecks(ctx, now.Add(-30*time.Minute))
	if err != nil {
		t.Fatalf("failed fetching recent checks: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(checks))
	}

	if checks[0].DownloadMbps == nil || *checks[0].DownloadMbps != dl {
		t.Errorf("expected download %.1f, got %v", dl, checks[0].DownloadMbps)
	}
	if checks[0].ServerName == nil || *checks[0].ServerName != srvName {
		t.Errorf("expected server name %q, got %v", srvName, checks[0].ServerName)
	}
	if checks[1].Status != "measurement_error" {
		t.Errorf("expected status 'measurement_error', got %q", checks[1].Status)
	}
	if checks[1].Error == nil || *checks[1].Error != errMsg {
		t.Errorf("expected error %q, got %v", errMsg, checks[1].Error)
	}
	if checks[1].DownloadMbps != nil {
		t.Errorf("expected nil download on measurement error, got %v", checks[1].DownloadMbps)
	}
}

func TestStore_OneOpenSLABreachInvariant(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	active, err := s.GetActiveSLABreach(ctx)
	if err != nil {
		t.Fatalf("failed checking initial active breach: %v", err)
	}
	if active != nil {
		t.Fatalf("expected no active breach, got %+v", active)
	}

	worstDown := 95.0
	worstUp := 90.0
	ep1 := &store.SLABreachEpisode{
		StartedAt:           now.Add(-2 * time.Hour),
		WorstDownloadMbps:   &worstDown,
		WorstUploadMbps:     &worstUp,
		ViolatingCheckCount: 5,
		TotalCheckCount:     20,
		ViolationPct:        25.0,
	}

	id1, err := s.StartSLABreach(ctx, ep1)
	if err != nil {
		t.Fatalf("failed starting first breach episode: %v", err)
	}
	if id1 <= 0 {
		t.Fatalf("expected positive ID, got %d", id1)
	}

	// Invariant test: Starting another open breach must fail!
	ep2 := &store.SLABreachEpisode{
		StartedAt:           now,
		ViolatingCheckCount: 6,
		TotalCheckCount:     21,
		ViolationPct:        28.5,
	}
	_, err = s.StartSLABreach(ctx, ep2)
	if !errors.Is(err, store.ErrOpenEpisodeExists) {
		t.Fatalf("expected ErrOpenEpisodeExists, got %v", err)
	}

	// Update active breach with worse speeds
	worseDown := 75.0
	ep1.WorstDownloadMbps = &worseDown
	ep1.ViolatingCheckCount = 6
	ep1.TotalCheckCount = 21
	ep1.ViolationPct = 28.5
	if err := s.UpdateSLABreach(ctx, ep1); err != nil {
		t.Fatalf("failed updating active breach: %v", err)
	}

	active, err = s.GetActiveSLABreach(ctx)
	if err != nil {
		t.Fatalf("failed getting active breach: %v", err)
	}
	if active == nil || active.WorstDownloadMbps == nil || *active.WorstDownloadMbps != 75.0 {
		t.Fatalf("expected updated worst download 75.0, got %+v", active)
	}

	// End active breach
	endedAt := now
	if err := s.EndSLABreach(ctx, ep1.ID, endedAt); err != nil {
		t.Fatalf("failed closing breach: %v", err)
	}

	activeAfter, err := s.GetActiveSLABreach(ctx)
	if err != nil {
		t.Fatalf("failed getting active breach after close: %v", err)
	}
	if activeAfter != nil {
		t.Fatalf("expected no active breach after close, got %+v", activeAfter)
	}

	// Now a new breach can be started
	id3, err := s.StartSLABreach(ctx, ep2)
	if err != nil {
		t.Fatalf("failed starting new breach after prior closed: %v", err)
	}
	if id3 <= 0 {
		t.Fatalf("expected positive id, got %d", id3)
	}
}

func TestStore_OneOpenOutageInvariant(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	active, err := s.GetActiveOutage(ctx)
	if err != nil {
		t.Fatalf("failed checking initial active outage: %v", err)
	}
	if active != nil {
		t.Fatalf("expected no active outage, got %+v", active)
	}

	ep1 := &store.OutageEpisode{
		StartedAt: now.Add(-5 * time.Minute),
		Reason:    "All probe targets timed out (1.1.1.1:443, 9.9.9.9:443)",
	}

	id1, err := s.StartOutage(ctx, ep1)
	if err != nil {
		t.Fatalf("failed starting outage: %v", err)
	}
	if id1 <= 0 {
		t.Fatalf("expected positive id, got %d", id1)
	}

	// Invariant test: Starting another open outage must fail!
	ep2 := &store.OutageEpisode{
		StartedAt: now,
		Reason:    "Secondary drop",
	}
	_, err = s.StartOutage(ctx, ep2)
	if !errors.Is(err, store.ErrOpenEpisodeExists) {
		t.Fatalf("expected ErrOpenEpisodeExists, got %v", err)
	}

	// Close outage
	endedAt := now
	duration := 300
	if err := s.EndOutage(ctx, ep1.ID, endedAt, duration); err != nil {
		t.Fatalf("failed closing outage: %v", err)
	}

	activeAfter, err := s.GetActiveOutage(ctx)
	if err != nil {
		t.Fatalf("failed checking active outage after close: %v", err)
	}
	if activeAfter != nil {
		t.Fatalf("expected no active outage, got %+v", activeAfter)
	}

	// Now starting a new outage should succeed
	id2, err := s.StartOutage(ctx, ep2)
	if err != nil {
		t.Fatalf("failed starting subsequent outage: %v", err)
	}
	if id2 <= 0 {
		t.Fatalf("expected positive id, got %d", id2)
	}
}

func TestStore_EpisodesBetween(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	// Outage 1: 01:00 to 01:10
	out1 := &store.OutageEpisode{StartedAt: base.Add(1 * time.Hour), Reason: "Drop 1"}
	id1, err := s.StartOutage(ctx, out1)
	if err != nil {
		t.Fatalf("failed starting out1: %v", err)
	}
	if err := s.EndOutage(ctx, id1, base.Add(1*time.Hour+10*time.Minute), 600); err != nil {
		t.Fatalf("failed closing out1: %v", err)
	}

	// Outage 2: 05:00 (still open)
	out2 := &store.OutageEpisode{StartedAt: base.Add(5 * time.Hour), Reason: "Drop 2"}
	if _, err := s.StartOutage(ctx, out2); err != nil {
		t.Fatalf("failed starting out2: %v", err)
	}

	eps, err := s.GetOutageEpisodesBetween(ctx, base, base.Add(6*time.Hour))
	if err != nil {
		t.Fatalf("failed getting outages between: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("expected 2 outages, got %d", len(eps))
	}
	if eps[0].DurationSeconds == nil || *eps[0].DurationSeconds != 600 {
		t.Errorf("expected out1 duration 600, got %v", eps[0].DurationSeconds)
	}
	if eps[1].EndedAt != nil {
		t.Errorf("expected out2 to be still open, got %v", eps[1].EndedAt)
	}
}
