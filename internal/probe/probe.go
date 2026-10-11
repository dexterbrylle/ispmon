package probe

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// DialFunc defines the dialer signature used for probing targets.
type DialFunc func(ctx context.Context, network, address string) error

// TargetResult holds the outcome of dialing a single target.
type TargetResult struct {
	Target string
	Err    error
}

// RoundResult holds the aggregated result of a probe round across all targets.
type RoundResult struct {
	OK        bool
	Timestamp time.Time
	Results   []TargetResult
	Reason    string
}

// Prober dials targets to verify TCP connectivity.
type Prober struct {
	Targets []string
	Timeout time.Duration
	Dial    DialFunc
	Now     func() time.Time
}

// New creates a Prober with default net.Dialer and system clock.
func New(targets []string, timeoutSeconds int) *Prober {
	timeout := time.Duration(timeoutSeconds) * time.Second
	return &Prober{
		Targets: targets,
		Timeout: timeout,
		Dial: func(ctx context.Context, network, address string) error {
			dialer := net.Dialer{Timeout: timeout}
			conn, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return err
			}
			_ = conn.Close()
			return nil
		},
		Now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

// CheckRound dials all targets concurrently.
// A round is considered failed ONLY if ALL targets fail.
func (p *Prober) CheckRound(ctx context.Context) RoundResult {
	now := p.Now().UTC()
	if len(p.Targets) == 0 {
		return RoundResult{
			OK:        false,
			Timestamp: now,
			Reason:    "no probe targets configured",
		}
	}

	results := make([]TargetResult, len(p.Targets))
	var wg sync.WaitGroup

	for i, target := range p.Targets {
		wg.Add(1)
		go func(idx int, tgt string) {
			defer wg.Done()
			dialCtx, cancel := context.WithTimeout(ctx, p.Timeout)
			defer cancel()

			err := p.Dial(dialCtx, "tcp", tgt)
			results[idx] = TargetResult{
				Target: tgt,
				Err:    err,
			}
		}(i, target)
	}

	wg.Wait()

	anySuccess := false
	var failMsgs []string
	for _, res := range results {
		if res.Err == nil {
			anySuccess = true
		} else {
			failMsgs = append(failMsgs, fmt.Sprintf("%s (%v)", res.Target, res.Err))
		}
	}

	var reason string
	if !anySuccess {
		reason = fmt.Sprintf("all probe targets failed: %s", strings.Join(failMsgs, ", "))
	}

	return RoundResult{
		OK:        anySuccess,
		Timestamp: now,
		Results:   results,
		Reason:    reason,
	}
}
