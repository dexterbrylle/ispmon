package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/health"
)

func TestRun_Version(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		var stdout, stderr bytes.Buffer
		code := runWithIO([]string{arg}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("runWithIO(%q) = %d, want 0", arg, code)
		}
		if !strings.Contains(stdout.String(), "ispmon") {
			t.Errorf("expected version output to contain 'ispmon', got: %s", stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("expected empty stderr, got: %s", stderr.String())
		}

		if directCode := run([]string{arg}); directCode != 0 {
			t.Errorf("run(%q) = %d, want 0", arg, directCode)
		}
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	for _, arg := range []string{"unknown", "--bad-flag", "help"} {
		var stdout, stderr bytes.Buffer
		code := runWithIO([]string{arg}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("runWithIO(%q) = %d, want 1", arg, code)
		}
		if !strings.Contains(stderr.String(), "unknown command") {
			t.Errorf("expected stderr to contain 'unknown command', got: %s", stderr.String())
		}
		if stdout.Len() != 0 {
			t.Errorf("expected empty stdout, got: %s", stdout.String())
		}

		if directCode := run([]string{arg}); directCode != 1 {
			t.Errorf("run(%q) = %d, want 1", arg, directCode)
		}
	}
}

func TestRun_Healthcheck(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "ispmon.db")
	t.Setenv("DB_PATH", dbPath)
	t.Setenv("CHECK_INTERVAL_MINUTES", "30")
	t.Setenv("PROBE_INTERVAL_SECONDS", "60")

	speedHb := health.HeartbeatPath(dbPath)
	probeHb := health.ProbeHeartbeatPath(dbPath)

	t.Run("missing heartbeats returns 1", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runWithIO([]string{"healthcheck"}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("runWithIO(healthcheck) = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "healthcheck failed") {
			t.Errorf("expected stderr to contain 'healthcheck failed', got: %s", stderr.String())
		}

		if directCode := run([]string{"healthcheck"}); directCode != 1 {
			t.Errorf("run(healthcheck) = %d, want 1", directCode)
		}
	})

	t.Run("speed heartbeat only returns 1", func(t *testing.T) {
		if err := health.WriteHeartbeat(speedHb); err != nil {
			t.Fatalf("TouchHeartbeat: %v", err)
		}
		defer os.Remove(speedHb)

		var stdout, stderr bytes.Buffer
		code := runWithIO([]string{"healthcheck"}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("runWithIO(healthcheck) = %d, want 1", code)
		}

		if directCode := run([]string{"healthcheck"}); directCode != 1 {
			t.Errorf("run(healthcheck) = %d, want 1", directCode)
		}
	})

	t.Run("probe heartbeat only returns 1", func(t *testing.T) {
		if err := health.WriteHeartbeat(probeHb); err != nil {
			t.Fatalf("TouchHeartbeat: %v", err)
		}
		defer os.Remove(probeHb)

		var stdout, stderr bytes.Buffer
		code := runWithIO([]string{"healthcheck"}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("runWithIO(healthcheck) = %d, want 1", code)
		}

		if directCode := run([]string{"healthcheck"}); directCode != 1 {
			t.Errorf("run(healthcheck) = %d, want 1", directCode)
		}
	})

	t.Run("both fresh heartbeats returns 0", func(t *testing.T) {
		if err := health.WriteHeartbeat(speedHb); err != nil {
			t.Fatalf("TouchHeartbeat(speed): %v", err)
		}
		if err := health.WriteHeartbeat(probeHb); err != nil {
			t.Fatalf("TouchHeartbeat(probe): %v", err)
		}
		defer func() {
			os.Remove(speedHb)
			os.Remove(probeHb)
		}()

		var stdout, stderr bytes.Buffer
		code := runWithIO([]string{"healthcheck"}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("runWithIO(healthcheck) = %d, want 0; stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "healthcheck OK") {
			t.Errorf("expected stdout to contain 'healthcheck OK', got: %s", stdout.String())
		}

		if directCode := run([]string{"healthcheck"}); directCode != 0 {
			t.Errorf("run(healthcheck) = %d, want 0", directCode)
		}
	})

	t.Run("stale speed heartbeat returns 1", func(t *testing.T) {
		if err := health.WriteHeartbeat(speedHb); err != nil {
			t.Fatalf("TouchHeartbeat(speed): %v", err)
		}
		if err := health.WriteHeartbeat(probeHb); err != nil {
			t.Fatalf("TouchHeartbeat(probe): %v", err)
		}
		defer func() {
			os.Remove(speedHb)
			os.Remove(probeHb)
		}()

		staleTime := time.Now().Add(-3 * time.Hour)
		if err := os.Chtimes(speedHb, staleTime, staleTime); err != nil {
			t.Fatalf("os.Chtimes: %v", err)
		}

		var stdout, stderr bytes.Buffer
		code := runWithIO([]string{"healthcheck"}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("runWithIO(healthcheck) = %d, want 1", code)
		}

		if directCode := run([]string{"healthcheck"}); directCode != 1 {
			t.Errorf("run(healthcheck) = %d, want 1", directCode)
		}
	})

	t.Run("stale probe heartbeat returns 1", func(t *testing.T) {
		if err := health.WriteHeartbeat(speedHb); err != nil {
			t.Fatalf("TouchHeartbeat(speed): %v", err)
		}
		if err := health.WriteHeartbeat(probeHb); err != nil {
			t.Fatalf("TouchHeartbeat(probe): %v", err)
		}
		defer func() {
			os.Remove(speedHb)
			os.Remove(probeHb)
		}()

		staleTime := time.Now().Add(-5 * time.Minute)
		if err := os.Chtimes(probeHb, staleTime, staleTime); err != nil {
			t.Fatalf("os.Chtimes: %v", err)
		}

		var stdout, stderr bytes.Buffer
		code := runWithIO([]string{"healthcheck"}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("runWithIO(healthcheck) = %d, want 1", code)
		}

		if directCode := run([]string{"healthcheck"}); directCode != 1 {
			t.Errorf("run(healthcheck) = %d, want 1", directCode)
		}
	})
}
