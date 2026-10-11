package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/config"
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
	defer close(n.release)
	p := probe.New([]string{"test:443"}, 1)
	up := false
	p.Dial = func(context.Context, string, string) error {
		if up {
			return nil
		}
		return errors.New("offline")
	}
	e := New(&config.Config{}, st, nil, p, outage.NewStateMachine(1, nil), notify.NewDispatcher([]notify.Notifier{n}, nil), nil)
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
