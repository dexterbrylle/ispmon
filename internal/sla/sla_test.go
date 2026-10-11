package sla_test

import (
	"testing"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/sla"
	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

func defaultTestConfig() sla.EvaluationConfig {
	return sla.EvaluationConfig{
		PlanDownloadThreshold: 150.0,
		PlanUploadThreshold:   150.0,
		SLAWindowHours:        24,
		SLAViolationPct:       20.0,
		SLAMinCoverage:        0.75,
		CheckIntervalMinutes:  30,
	}
}

func makeCheck(ts time.Time, status string, down, up float64) store.NetworkCheck {
	var d, u *float64
	if status == "ok" {
		d = &down
		u = &up
	}
	return store.NetworkCheck{
		Timestamp:    ts,
		Status:       status,
		Engine:       "ookla-cli",
		DownloadMbps: d,
		UploadMbps:   u,
	}
}

func TestSLA_IsBelowThreshold(t *testing.T) {
	tests := []struct {
		name     string
		down, up float64
		expected bool
	}{
		{"both above", 500.0, 500.0, false},
		{"at threshold boundary", 150.0, 150.0, false},
		{"download below", 149.9, 500.0, true},
		{"upload below", 500.0, 149.9, true},
		{"both below", 100.0, 100.0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sla.IsBelowThreshold(tt.down, tt.up, 150.0, 150.0)
			if got != tt.expected {
				t.Errorf("IsBelowThreshold(%.1f, %.1f) = %v; want %v", tt.down, tt.up, got, tt.expected)
			}
		})
	}
}

func TestSLA_EmptyWindow(t *testing.T) {
	cfg := defaultTestConfig()
	now := time.Now().UTC()

	res := sla.Evaluate(nil, now, cfg)
	if res.Status != sla.StatusInsufficientData {
		t.Errorf("expected InsufficientData on empty window, got %v", res.Status)
	}
	if res.TotalValidCount != 0 {
		t.Errorf("expected 0 valid checks, got %d", res.TotalValidCount)
	}
}

func TestSLA_CoverageBoundary(t *testing.T) {
	// expected = 24 * 60 / 30 = 48. minRequired = 48 * 0.75 = 36.
	cfg := defaultTestConfig()
	now := time.Now().UTC()

	// 1. One below coverage boundary: 35 valid checks
	var checks []store.NetworkCheck
	for i := 0; i < 35; i++ {
		checks = append(checks, makeCheck(now.Add(-time.Duration(i*30)*time.Minute), "ok", 500.0, 500.0))
	}

	resBelow := sla.Evaluate(checks, now, cfg)
	if resBelow.Status != sla.StatusInsufficientData {
		t.Fatalf("expected InsufficientData with 35/36 checks, got %v", resBelow.Status)
	}
	if resBelow.MinRequiredCount != 36 {
		t.Errorf("expected MinRequiredCount 36, got %d", resBelow.MinRequiredCount)
	}

	// 2. Exactly at coverage boundary: 36 valid checks
	checks = append(checks, makeCheck(now.Add(-time.Duration(35*30)*time.Minute), "ok", 500.0, 500.0))
	resBoundary := sla.Evaluate(checks, now, cfg)
	if resBoundary.Status != sla.StatusOK {
		t.Fatalf("expected OK at boundary with 36/36 checks, got %v", resBoundary.Status)
	}
	if resBoundary.TotalValidCount != 36 {
		t.Errorf("expected 36 valid checks, got %d", resBoundary.TotalValidCount)
	}
}

func TestSLA_PercentageBoundary(t *testing.T) {
	// 50 valid checks. SLAViolationPct = 20.0.
	// Rule: breach if strictly greater than 20.0%.
	cfg := defaultTestConfig()
	now := time.Now().UTC()

	// Exactly at 20.0% (10 out of 50 violating)
	var checks []store.NetworkCheck
	for i := 0; i < 40; i++ {
		checks = append(checks, makeCheck(now.Add(-time.Duration(i*20)*time.Minute), "ok", 500.0, 500.0))
	}
	for i := 0; i < 10; i++ {
		checks = append(checks, makeCheck(now.Add(-time.Duration((40+i)*20)*time.Minute), "ok", 100.0, 100.0))
	}

	resExact := sla.Evaluate(checks, now, cfg)
	if resExact.ViolationPct != 20.0 {
		t.Fatalf("expected exactly 20.0%% violation, got %v", resExact.ViolationPct)
	}
	if resExact.Status != sla.StatusOK {
		t.Errorf("20.0%% should NOT be a breach (must be strictly > 20.0%%), got %v", resExact.Status)
	}

	// Strictly greater than 20.0% (11 out of 50 = 22.0% violating)
	checks[0] = makeCheck(now, "ok", 80.0, 80.0) // turn 1 more into violating
	resBreach := sla.Evaluate(checks, now, cfg)
	if resBreach.ViolationPct != 22.0 {
		t.Fatalf("expected 22.0%% violation, got %v", resBreach.ViolationPct)
	}
	if resBreach.Status != sla.StatusBreach {
		t.Errorf("22.0%% should be a breach, got %v", resBreach.Status)
	}
}

func TestSLA_AllBelowAndAllAbove(t *testing.T) {
	cfg := defaultTestConfig()
	now := time.Now().UTC()

	// All below: 48/48 violating
	var allBelow []store.NetworkCheck
	for i := 0; i < 48; i++ {
		allBelow = append(allBelow, makeCheck(now.Add(-time.Duration(i*30)*time.Minute), "ok", 90.0, 80.0))
	}
	resBelow := sla.Evaluate(allBelow, now, cfg)
	if resBelow.Status != sla.StatusBreach {
		t.Errorf("expected breach for all-below, got %v", resBelow.Status)
	}
	if resBelow.ViolationPct != 100.0 {
		t.Errorf("expected 100%% violation, got %v", resBelow.ViolationPct)
	}

	// All above: 0/48 violating
	var allAbove []store.NetworkCheck
	for i := 0; i < 48; i++ {
		allAbove = append(allAbove, makeCheck(now.Add(-time.Duration(i*30)*time.Minute), "ok", 500.0, 500.0))
	}
	resAbove := sla.Evaluate(allAbove, now, cfg)
	if resAbove.Status != sla.StatusOK {
		t.Errorf("expected OK for all-above, got %v", resAbove.Status)
	}
	if resAbove.ViolationPct != 0.0 {
		t.Errorf("expected 0%% violation, got %v", resAbove.ViolationPct)
	}
}

func TestSLA_ExcludeMeasurementErrors(t *testing.T) {
	cfg := defaultTestConfig()
	now := time.Now().UTC()

	// 30 ok/fast, 10 ok/slow, 8 measurement_error = 40 valid checks.
	// 10/40 = 25.0% > 20% -> breach!
	var checks []store.NetworkCheck
	for i := 0; i < 30; i++ {
		checks = append(checks, makeCheck(now.Add(-time.Duration(i*30)*time.Minute), "ok", 500.0, 500.0))
	}
	for i := 0; i < 10; i++ {
		checks = append(checks, makeCheck(now.Add(-time.Duration((30+i)*30)*time.Minute), "ok", 100.0, 100.0))
	}
	for i := 0; i < 8; i++ {
		checks = append(checks, makeCheck(now.Add(-time.Duration((40+i)*30)*time.Minute), "measurement_error", 0, 0))
	}

	res := sla.Evaluate(checks, now, cfg)
	if res.TotalValidCount != 40 {
		t.Errorf("expected 40 valid checks (excluding 8 measurement errors), got %d", res.TotalValidCount)
	}
	if res.ViolatingCount != 10 {
		t.Errorf("expected 10 violating checks, got %d", res.ViolatingCount)
	}
	if res.ViolationPct != 25.0 {
		t.Errorf("expected 25.0%% violation, got %v", res.ViolationPct)
	}
	if res.Status != sla.StatusBreach {
		t.Errorf("expected breach status, got %v", res.Status)
	}
}

func TestSLA_DecideAction(t *testing.T) {
	now := time.Now().UTC()
	worstDown := 90.0
	worstUp := 85.0

	breachEval := sla.EvaluationResult{
		Status:            sla.StatusBreach,
		ViolatingCount:    12,
		TotalValidCount:   48,
		ViolationPct:      25.0,
		WorstDownloadMbps: &worstDown,
		WorstUploadMbps:   &worstUp,
	}

	okEval := sla.EvaluationResult{
		Status:          sla.StatusOK,
		ViolatingCount:  2,
		TotalValidCount: 48,
		ViolationPct:    4.1,
	}

	// 1. No open episode + breach -> ActionOpenBreach
	act1 := sla.DecideAction(nil, breachEval, now)
	if act1.Kind != sla.ActionOpenBreach {
		t.Fatalf("expected ActionOpenBreach, got %v", act1.Kind)
	}
	if act1.Episode == nil || act1.Episode.ViolationPct != 25.0 {
		t.Errorf("expected new episode with 25.0%%, got %+v", act1.Episode)
	}

	// 2. Open episode + breach -> ActionUpdateBreach
	existingEp := &store.SLABreachEpisode{
		ID:                  42,
		StartedAt:           now.Add(-2 * time.Hour),
		WorstDownloadMbps:   &worstDown,
		WorstUploadMbps:     &worstUp,
		ViolatingCheckCount: 10,
		TotalCheckCount:     48,
		ViolationPct:        20.8,
	}
	act2 := sla.DecideAction(existingEp, breachEval, now)
	if act2.Kind != sla.ActionUpdateBreach {
		t.Fatalf("expected ActionUpdateBreach, got %v", act2.Kind)
	}
	if act2.Episode.ViolatingCheckCount != 12 {
		t.Errorf("expected updated violating count 12, got %d", act2.Episode.ViolatingCheckCount)
	}

	// 3. Open episode + OK -> ActionResolveBreach (recovery)
	act3 := sla.DecideAction(existingEp, okEval, now)
	if act3.Kind != sla.ActionResolveBreach {
		t.Fatalf("expected ActionResolveBreach, got %v", act3.Kind)
	}
	if act3.EpisodeIDToEnd != 42 {
		t.Errorf("expected episode ID 42 to end, got %d", act3.EpisodeIDToEnd)
	}

	// 4. No open episode + OK -> ActionNone
	act4 := sla.DecideAction(nil, okEval, now)
	if act4.Kind != sla.ActionNone {
		t.Fatalf("expected ActionNone, got %v", act4.Kind)
	}
}
