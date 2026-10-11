package health

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// HeartbeatPath returns the location of the heartbeat file adjacent to dbPath.
func HeartbeatPath(dbPath string) string {
	dir := filepath.Dir(dbPath)
	if dir == "" || dir == "." {
		return ".heartbeat"
	}
	return filepath.Join(dir, ".heartbeat")
}

// WriteHeartbeat touches/writes the heartbeat file with the current UTC timestamp.
func WriteHeartbeat(heartbeatPath string) error {
	dir := filepath.Dir(heartbeatPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed creating heartbeat directory: %w", err)
		}
	}

	data := []byte(time.Now().UTC().Format(time.RFC3339) + "\n")
	if err := os.WriteFile(heartbeatPath, data, 0644); err != nil {
		return fmt.Errorf("failed writing heartbeat file: %w", err)
	}
	return nil
}

// CheckHeartbeat verifies that the heartbeat file exists and was modified within maxAge.
func CheckHeartbeat(heartbeatPath string, maxAge time.Duration, now time.Time) error {
	fi, err := os.Stat(heartbeatPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("heartbeat file %q does not exist", heartbeatPath)
		}
		return fmt.Errorf("failed inspecting heartbeat file: %w", err)
	}

	age := now.Sub(fi.ModTime())
	if age > maxAge {
		return fmt.Errorf("heartbeat is stale: age %v exceeds maximum allowed age %v", age.Round(time.Second), maxAge)
	}

	return nil
}
