package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// Dispatcher coordinates fan-out delivery across all active notifiers.
type Dispatcher struct {
	notifiers []Notifier
	logger    *slog.Logger
	timeout   time.Duration
}

// NewDispatcher creates a Dispatcher with 15s per-send timeout.
func NewDispatcher(notifiers []Notifier, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{
		notifiers: notifiers,
		logger:    logger,
		timeout:   15 * time.Second,
	}
}

// isTransient determines whether an error is a transient network fault eligible for one immediate retry.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "timeout") ||
		strings.Contains(s, "connection refused") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe")
}

// Dispatch sends the event to all configured notifiers concurrently.
// A failing notifier will never block other notifiers or crash the process.
func (d *Dispatcher) Dispatch(ctx context.Context, e Event) error {
	if len(d.notifiers) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	for _, n := range d.notifiers {
		wg.Add(1)
		go func(target Notifier) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					failures = append(failures, fmt.Errorf("notifier panic: %v", r))
					mu.Unlock()
					d.logger.Error("notifier panic recovered", "notifier", target.Name(), "panic", r)
				}
			}()

			sendCtx, cancel := context.WithTimeout(ctx, d.timeout)
			err := target.Notify(sendCtx, e)
			cancel()

			if err != nil && isTransient(err) && ctx.Err() == nil {
				d.logger.Warn("transient failure sending notification, retrying once",
					"notifier", target.Name(),
					"error", err,
				)
				retryCtx, cancelRetry := context.WithTimeout(ctx, d.timeout)
				err = target.Notify(retryCtx, e)
				cancelRetry()
			}

			if err != nil {
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
				d.logger.Error("failed to dispatch notification",
					"notifier", target.Name(),
					"kind", string(e.Kind),
					"error", err,
				)
			}
		}(n)
	}

	wg.Wait()
	return errors.Join(failures...)
}
