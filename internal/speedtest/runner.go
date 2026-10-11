package speedtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

// ErrNotFound is returned when the Ookla speedtest CLI binary cannot be located.
var ErrNotFound = errors.New("speedtest CLI not found in PATH. Please install official Ookla speedtest CLI (see README.md: 'brew install speedtest' on macOS or official apt/rpm repository on Linux)")

// LookPath locates the Ookla speedtest binary using exec.LookPath.
func LookPath() (string, error) {
	path, err := exec.LookPath("speedtest")
	if err != nil {
		return "", ErrNotFound
	}
	return path, nil
}

// Runner defines the interface for running a network speed test.
type Runner interface {
	Run(ctx context.Context) (*store.NetworkCheck, error)
}

// OoklaRunner executes the Ookla speedtest CLI.
type OoklaRunner struct {
	BinaryPath string
	FetchIP    func(ctx context.Context) (string, error)
	CmdRunner  func(ctx context.Context, name string, args ...string) ([]byte, error)
	Now        func() time.Time
}

// NewRunner creates a new OoklaRunner configured with default system execution.
func NewRunner(binaryPath string) *OoklaRunner {
	return &OoklaRunner{
		BinaryPath: binaryPath,
		FetchIP: func(ctx context.Context) (string, error) {
			return FetchPublicIP(ctx, nil)
		},
		CmdRunner: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil && stderr.Len() > 0 {
				err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
			}
			return out, err
		},
		Now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

// Run executes a speed test with a 120s timeout and records metrics or measurement_error.
func (r *OoklaRunner) Run(ctx context.Context) (*store.NetworkCheck, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	now := r.Now().UTC()

	// Public IP check with 10s timeout (non-fatal)
	var publicIP *string
	if r.FetchIP != nil {
		ipCtx, cancelIP := context.WithTimeout(ctx, 10*time.Second)
		ip, err := r.FetchIP(ipCtx)
		cancelIP()
		if err == nil && ip != "" {
			publicIP = &ip
		}
	}

	// Ookla speed test with 120s timeout
	runCtx, cancelRun := context.WithTimeout(ctx, 120*time.Second)
	defer cancelRun()

	out, err := r.CmdRunner(runCtx, r.BinaryPath, "--format=json", "--accept-license", "--accept-gdpr")
	if ctx.Err() != nil {
		// Parent context was canceled (daemon shutdown)
		return nil, ctx.Err()
	}

	if err != nil {
		errMsg := fmt.Sprintf("speedtest execution failed: %v", err)
		if runCtx.Err() == context.DeadlineExceeded {
			errMsg = "speedtest timed out after 120s"
		}
		return &store.NetworkCheck{
			Timestamp:      now,
			Status:         "measurement_error",
			Engine:         "ookla-cli",
			PublicIP:       publicIP,
			BelowThreshold: false,
			Error:          &errMsg,
		}, nil
	}

	res, err := ParseOoklaJSON(out)
	if err != nil {
		errMsg := fmt.Sprintf("speedtest parse failed: %v", err)
		return &store.NetworkCheck{
			Timestamp:      now,
			Status:         "measurement_error",
			Engine:         "ookla-cli",
			PublicIP:       publicIP,
			BelowThreshold: false,
			Error:          &errMsg,
		}, nil
	}

	return &store.NetworkCheck{
		Timestamp:      now,
		Status:         "ok",
		Engine:         "ookla-cli",
		DownloadMbps:   &res.DownloadMbps,
		UploadMbps:     &res.UploadMbps,
		LatencyMs:      &res.LatencyMs,
		JitterMs:       &res.JitterMs,
		PacketLossPct:  res.PacketLossPct,
		ServerID:       &res.ServerID,
		ServerName:     &res.ServerName,
		ServerLocation: &res.ServerLocation,
		ResultURL:      &res.ResultURL,
		PublicIP:       publicIP,
		BelowThreshold: false,
	}, nil
}
