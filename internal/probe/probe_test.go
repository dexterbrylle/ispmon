package probe_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dexterbrylle/isp-speed-monitor/internal/probe"
)

func TestProber_AllTargetsSucceed(t *testing.T) {
	targets := []string{"1.1.1.1:443", "9.9.9.9:443"}
	p := probe.New(targets, 3)
	p.Dial = func(ctx context.Context, network, address string) error {
		return nil
	}

	res := p.CheckRound(context.Background())
	if !res.OK {
		t.Fatalf("expected round to be OK when all targets succeed, got false with reason: %s", res.Reason)
	}
	if res.Reason != "" {
		t.Errorf("expected empty reason on success, got %q", res.Reason)
	}
	if len(res.Results) != 2 {
		t.Errorf("expected 2 target results, got %d", len(res.Results))
	}
}

func TestProber_PartialTargetFailureIsOK(t *testing.T) {
	targets := []string{"1.1.1.1:443", "9.9.9.9:443"}
	p := probe.New(targets, 3)
	p.Dial = func(ctx context.Context, network, address string) error {
		if address == "1.1.1.1:443" {
			return errors.New("timeout")
		}
		return nil // 9.9.9.9 succeeds
	}

	res := p.CheckRound(context.Background())
	if !res.OK {
		t.Fatalf("expected round OK when at least one target succeeds, got failed: %s", res.Reason)
	}
}

func TestProber_AllTargetsFail(t *testing.T) {
	targets := []string{"1.1.1.1:443", "9.9.9.9:443"}
	p := probe.New(targets, 3)
	p.Dial = func(ctx context.Context, network, address string) error {
		return errors.New("connection refused")
	}

	res := p.CheckRound(context.Background())
	if res.OK {
		t.Fatalf("expected round to fail when all targets fail, got OK")
	}
	if !strings.Contains(res.Reason, "all probe targets failed") {
		t.Errorf("expected reason to state all probe targets failed, got %q", res.Reason)
	}
	if !strings.Contains(res.Reason, "1.1.1.1:443") || !strings.Contains(res.Reason, "9.9.9.9:443") {
		t.Errorf("expected reason to list targets, got %q", res.Reason)
	}
}
