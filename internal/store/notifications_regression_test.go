package store

import (
	"context"
	"testing"
	"time"
)

func TestEpisodeAndAlertAreAtomic(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec("CREATE TRIGGER reject_alert BEFORE INSERT ON notification_outbox BEGIN SELECT RAISE(ABORT,'outbox unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	ep := &SLABreachEpisode{}
	_, err = s.StartSLABreach(context.Background(), ep, map[string]string{"Kind": "sla_breach"})
	if err == nil {
		t.Fatal("episode committed without alert")
	}
	active, err := s.GetActiveSLABreach(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if active != nil {
		t.Fatal("episode survived failed outbox insert")
	}
}

func TestUnsentExistingBreachQueuedOnce(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	ep := &SLABreachEpisode{}
	id, err := s.StartSLABreach(ctx, ep)
	if err != nil {
		t.Fatal(err)
	}
	event := map[string]string{"Kind": "sla_breach"}
	for i := 0; i < 2; i++ {
		if err := s.QueueUnsentSLABreach(ctx, id, event); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := s.PendingNotifications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("queued alerts = %d, want 1", len(pending))
	}
	if err := s.CompleteNotification(ctx, pending[0], time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueUnsentSLABreach(ctx, id, event); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingNotifications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatal("delivered episode queued again")
	}
}
