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
}
