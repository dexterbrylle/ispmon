package store_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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

	ep2 := &store.OutageEpisode{
		StartedAt: now,
		Reason:    "Secondary drop",
	}
	_, err = s.StartOutage(ctx, ep2)
	if !errors.Is(err, store.ErrOpenEpisodeExists) {
		t.Fatalf("expected ErrOpenEpisodeExists, got %v", err)
	}

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
	out1 := &store.OutageEpisode{StartedAt: base.Add(1 * time.Hour), Reason: "Drop 1"}
	id1, err := s.StartOutage(ctx, out1)
	if err != nil {
		t.Fatalf("failed starting out1: %v", err)
	}
	if err := s.EndOutage(ctx, id1, base.Add(1*time.Hour+10*time.Minute), 600); err != nil {
		t.Fatalf("failed closing out1: %v", err)
	}

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

func TestStore_PruneOldData(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

	n, err := s.PruneOldData(ctx, 0, now)
	if err != nil {
		t.Fatalf("unexpected error with retention 0: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 deleted for retention 0, got %d", n)
	}

	oldCheck := &store.NetworkCheck{
		Timestamp: now.AddDate(0, 0, -100),
		Status:    "ok",
		Engine:    "ookla-cli",
	}
	freshCheck := &store.NetworkCheck{
		Timestamp: now.AddDate(0, 0, -10),
		Status:    "ok",
		Engine:    "ookla-cli",
	}
	if _, err := s.SaveCheck(ctx, oldCheck); err != nil {
		t.Fatalf("failed saving old check: %v", err)
	}
	if _, err := s.SaveCheck(ctx, freshCheck); err != nil {
		t.Fatalf("failed saving fresh check: %v", err)
	}

	oldBreach := &store.SLABreachEpisode{
		StartedAt:           now.AddDate(0, 0, -100),
		ViolatingCheckCount: 5,
		TotalCheckCount:     10,
		ViolationPct:        50.0,
	}
	oldBreachID, err := s.StartSLABreach(ctx, oldBreach)
	if err != nil {
		t.Fatalf("failed starting old breach: %v", err)
	}
	if err := s.EndSLABreach(ctx, oldBreachID, now.AddDate(0, 0, -95)); err != nil {
		t.Fatalf("failed ending old breach: %v", err)
	}

	freshBreach := &store.SLABreachEpisode{
		StartedAt:           now.AddDate(0, 0, -10),
		ViolatingCheckCount: 3,
		TotalCheckCount:     10,
		ViolationPct:        30.0,
	}
	freshBreachID, err := s.StartSLABreach(ctx, freshBreach)
	if err != nil {
		t.Fatalf("failed starting fresh breach: %v", err)
	}
	if err := s.EndSLABreach(ctx, freshBreachID, now.AddDate(0, 0, -5)); err != nil {
		t.Fatalf("failed ending fresh breach: %v", err)
	}

	oldOutage := &store.OutageEpisode{
		StartedAt: now.AddDate(0, 0, -100),
		Reason:    "old drop",
	}
	oldOutageID, err := s.StartOutage(ctx, oldOutage)
	if err != nil {
		t.Fatalf("failed starting old outage: %v", err)
	}
	if err := s.EndOutage(ctx, oldOutageID, now.AddDate(0, 0, -95), 300); err != nil {
		t.Fatalf("failed ending old outage: %v", err)
	}

	freshOutage := &store.OutageEpisode{
		StartedAt: now.AddDate(0, 0, -10),
		Reason:    "fresh drop",
	}
	freshOutageID, err := s.StartOutage(ctx, freshOutage)
	if err != nil {
		t.Fatalf("failed starting fresh outage: %v", err)
	}
	if err := s.EndOutage(ctx, freshOutageID, now.AddDate(0, 0, -5), 120); err != nil {
		t.Fatalf("failed ending fresh outage: %v", err)
	}

	openOutage := &store.OutageEpisode{
		StartedAt: now.AddDate(0, 0, -120),
		Reason:    "long-running open outage",
	}
	if _, err := s.StartOutage(ctx, openOutage); err != nil {
		t.Fatalf("failed starting open outage: %v", err)
	}

	deleted, err := s.PruneOldData(ctx, 90, now)
	if err != nil {
		t.Fatalf("failed running PruneOldData: %v", err)
	}
	if deleted != 3 { // 1 old check + 1 old SLA breach + 1 old outage
		t.Errorf("expected 3 deleted rows, got %d", deleted)
	}

	checks, err := s.GetRecentChecks(ctx, now.AddDate(0, 0, -200))
	if err != nil {
		t.Fatalf("failed getting checks: %v", err)
	}
	if len(checks) != 1 {
		t.Fatalf("expected 1 remaining check, got %d", len(checks))
	}
	if checks[0].ID != freshCheck.ID {
		t.Errorf("expected fresh check to survive, got check ID %d", checks[0].ID)
	}

	breaches, err := s.GetSLABreachEpisodesBetween(ctx, now.AddDate(0, 0, -200), now)
	if err != nil {
		t.Fatalf("failed getting breaches: %v", err)
	}
	if len(breaches) != 1 {
		t.Fatalf("expected 1 remaining breach, got %d", len(breaches))
	}
	if breaches[0].ID != freshBreachID {
		t.Errorf("expected fresh breach to survive, got breach ID %d", breaches[0].ID)
	}

	outages, err := s.GetOutageEpisodesBetween(ctx, now.AddDate(0, 0, -200), now)
	if err != nil {
		t.Fatalf("failed getting outages: %v", err)
	}
	if len(outages) != 2 {
		t.Fatalf("expected 2 remaining outages (1 fresh closed + 1 open), got %d", len(outages))
	}

	activeOutage, err := s.GetActiveOutage(ctx)
	if err != nil {
		t.Fatalf("failed querying active outage: %v", err)
	}
	if activeOutage == nil {
		t.Fatal("expected active open outage to be preserved")
	}
}

func TestStore_TimestampUTCZInvariant(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	tokyoLoc := time.FixedZone("JST", 9*3600)
	tokyoTime := time.Date(2026, 10, 11, 18, 30, 45, 0, tokyoLoc) // 09:30:45 UTC

	nyLoc := time.FixedZone("EDT", -4*3600)
	nyTime := time.Date(2026, 10, 11, 5, 30, 45, 0, nyLoc) // 09:30:45 UTC

	c1 := &store.NetworkCheck{
		Timestamp: tokyoTime,
		Status:    "ok",
		Engine:    "ookla-cli",
	}
	id1, err := s.SaveCheck(ctx, c1)
	if err != nil {
		t.Fatalf("failed saving check 1: %v", err)
	}

	c2 := &store.NetworkCheck{
		Timestamp: nyTime,
		Status:    "ok",
		Engine:    "ookla-cli",
	}
	id2, err := s.SaveCheck(ctx, c2)
	if err != nil {
		t.Fatalf("failed saving check 2: %v", err)
	}

	for _, id := range []int64{id1, id2} {
		var rawTs string
		if err := s.DB().QueryRowContext(ctx, "SELECT timestamp FROM network_checks WHERE id = ?", id).Scan(&rawTs); err != nil {
			t.Fatalf("failed querying raw timestamp for check %d: %v", id, err)
		}
		if !strings.HasSuffix(rawTs, "Z") {
			t.Errorf("expected stored timestamp to end in 'Z', got %q", rawTs)
		}
		if rawTs != "2026-10-11T09:30:45Z" {
			t.Errorf("expected raw timestamp '2026-10-11T09:30:45Z', got %q", rawTs)
		}
	}

	start := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	checks, err := s.GetChecksBetween(ctx, start, end)
	if err != nil {
		t.Fatalf("failed GetChecksBetween: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("expected 2 checks in range, got %d", len(checks))
	}
	for _, ch := range checks {
		if ch.Timestamp.Location() != time.UTC {
			t.Errorf("expected read check location to be UTC, got %v", ch.Timestamp.Location())
		}
		if !ch.Timestamp.Equal(tokyoTime) {
			t.Errorf("expected time %v to equal %v", ch.Timestamp, tokyoTime)
		}
	}
}
