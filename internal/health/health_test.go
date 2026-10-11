package health_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/health"
)

func TestHealth_Lifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "data", "ispmon.db")
	hbPath := health.HeartbeatPath(dbPath)

	expectedPath := filepath.Join(tmpDir, "data", ".heartbeat")
	if hbPath != expectedPath {
		t.Fatalf("expected heartbeat path %q, got %q", expectedPath, hbPath)
	}

	// 1. Missing heartbeat check fails
	now := time.Now().UTC()
	err := health.CheckHeartbeat(hbPath, 5*time.Minute, now)
	if err == nil {
		t.Fatalf("expected error on missing heartbeat file")
	}

	// 2. Write heartbeat
	if err := health.WriteHeartbeat(hbPath); err != nil {
		t.Fatalf("failed writing heartbeat: %v", err)
	}

	// 3. Fresh check succeeds
	if err := health.CheckHeartbeat(hbPath, 5*time.Minute, now); err != nil {
		t.Fatalf("expected fresh heartbeat to pass: %v", err)
	}

	// 4. Stale check fails
	future := now.Add(10 * time.Minute)
	err = health.CheckHeartbeat(hbPath, 5*time.Minute, future)
	if err == nil {
		t.Fatalf("expected error on stale heartbeat")
	}

	// Verify file content format
	content, err := os.ReadFile(hbPath)
	if err != nil {
		t.Fatalf("failed reading heartbeat file: %v", err)
	}
	if len(content) == 0 {
		t.Fatalf("heartbeat content is empty")
	}

	// 5. Probe heartbeat path verification
	probeHbPath := health.ProbeHeartbeatPath(dbPath)
	expectedProbePath := filepath.Join(tmpDir, "data", ".probe_heartbeat")
	if probeHbPath != expectedProbePath {
		t.Fatalf("expected probe heartbeat path %q, got %q", expectedProbePath, probeHbPath)
	}

	// 6. CheckHealth fails when probe heartbeat is missing
	if err := health.CheckHealth(hbPath, probeHbPath, 5*time.Minute, 2*time.Minute, now); err == nil {
		t.Fatalf("expected CheckHealth to fail when probe heartbeat missing")
	}

	// 7. Write probe heartbeat
	if err := health.WriteHeartbeat(probeHbPath); err != nil {
		t.Fatalf("failed writing probe heartbeat: %v", err)
	}

	// 8. CheckHealth passes when both are fresh
	if err := health.CheckHealth(hbPath, probeHbPath, 5*time.Minute, 2*time.Minute, now); err != nil {
		t.Fatalf("expected CheckHealth to pass: %v", err)
	}

	// 9. CheckHealth fails when probe heartbeat is stale
	probeFuture := now.Add(3 * time.Minute)
	if err := health.CheckHealth(hbPath, probeHbPath, 5*time.Minute, 2*time.Minute, probeFuture); err == nil {
		t.Fatalf("expected CheckHealth to fail when probe heartbeat is stale")
	}

	// 10. CheckHealth fails when speed heartbeat is stale
	speedFuture := now.Add(6 * time.Minute)
	// Refresh probe heartbeat so only speed heartbeat is stale
	_ = health.WriteHeartbeat(probeHbPath)
	if err := health.CheckHealth(hbPath, probeHbPath, 5*time.Minute, 2*time.Minute, speedFuture); err == nil {
		t.Fatalf("expected CheckHealth to fail when speed heartbeat is stale")
	}
}

