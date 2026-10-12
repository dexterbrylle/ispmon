package outage_test

import (
	"testing"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/outage"
	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

func TestOutage_TransientFailsDoNotOpenOutage(t *testing.T) {
	sm := outage.NewStateMachine(3, nil)
	base := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)

	act1 := sm.Step(false, "drop 1", base)
	if act1.Kind != outage.ActionNone {
		t.Fatalf("expected ActionNone on 1st fail, got %v", act1.Kind)
	}

	act2 := sm.Step(false, "drop 2", base.Add(60*time.Second))
	if act2.Kind != outage.ActionNone {
		t.Fatalf("expected ActionNone on 2nd fail, got %v", act2.Kind)
	}

	act3 := sm.Step(true, "", base.Add(120*time.Second))
	if act3.Kind != outage.ActionNone {
		t.Fatalf("expected ActionNone when recovering before threshold, got %v", act3.Kind)
	}
	if sm.IsOpen() {
		t.Fatalf("expected state machine to be closed")
	}
}

func TestOutage_FullLifecycle(t *testing.T) {
	sm := outage.NewStateMachine(3, nil)
	t1 := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(60 * time.Second)
	t3 := t2.Add(60 * time.Second)
	t4 := t3.Add(60 * time.Second)
	t5 := t4.Add(60 * time.Second)

	act1 := sm.Step(false, "targets timed out", t1)
	if act1.Kind != outage.ActionNone {
		t.Fatalf("expected ActionNone on round 1, got %v", act1.Kind)
	}

	act2 := sm.Step(false, "targets timed out", t2)
	if act2.Kind != outage.ActionNone {
		t.Fatalf("expected ActionNone on round 2, got %v", act2.Kind)
	}

	act3 := sm.Step(false, "targets timed out", t3)
	if act3.Kind != outage.ActionOpen {
		t.Fatalf("expected ActionOpen on round 3, got %v", act3.Kind)
	}
	if !act3.StartedAt.Equal(t1) {
		t.Errorf("expected started_at = t1 (%v), got %v", t1, act3.StartedAt)
	}
	if act3.Reason != "targets timed out" {
		t.Errorf("expected reason 'targets timed out', got %q", act3.Reason)
	}
	if !sm.IsOpen() {
		t.Errorf("expected state machine to be open")
	}

	sm.SetActiveEpisodeID(77)

	act4 := sm.Step(false, "still down", t4)
	if act4.Kind != outage.ActionNone {
		t.Fatalf("expected ActionNone on continuing outage, got %v", act4.Kind)
	}

	act5 := sm.Step(true, "", t5)
	if act5.Kind != outage.ActionRestore {
		t.Fatalf("expected ActionRestore on round 5, got %v", act5.Kind)
	}
	if !act5.EndedAt.Equal(t5) {
		t.Errorf("expected ended_at = t5 (%v), got %v", t5, act5.EndedAt)
	}
	if act5.DurationSeconds != 240 {
		t.Errorf("expected duration 240s, got %d", act5.DurationSeconds)
	}
	if act5.EpisodeID != 77 {
		t.Errorf("expected episode ID 77, got %d", act5.EpisodeID)
	}
	if sm.IsOpen() {
		t.Errorf("expected state machine to be closed after restore")
	}

	act6 := sm.Step(true, "", t5.Add(60*time.Second))
	if act6.Kind != outage.ActionNone {
		t.Fatalf("expected ActionNone on subsequent success, got %v", act6.Kind)
	}
}

func TestOutage_StartWithActiveEpisode(t *testing.T) {
	t0 := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	existingEp := &store.OutageEpisode{
		ID:        99,
		StartedAt: t0,
		Reason:    "Prior outage",
	}

	sm := outage.NewStateMachine(3, existingEp)
	if !sm.IsOpen() {
		t.Fatalf("expected state machine to initialize as open")
	}

	t1 := t0.Add(15 * time.Minute)
	act := sm.Step(true, "", t1)
	if act.Kind != outage.ActionRestore {
		t.Fatalf("expected ActionRestore, got %v", act.Kind)
	}
	if act.EpisodeID != 99 {
		t.Errorf("expected episode ID 99, got %d", act.EpisodeID)
	}
	if act.DurationSeconds != 900 {
		t.Errorf("expected duration 900s, got %d", act.DurationSeconds)
	}
}
