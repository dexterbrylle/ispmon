package store

import (
	"database/sql"
	"fmt"
)

type migrationFunc func(tx *sql.Tx) error

var migrations = []migrationFunc{
	migrateV1,
}

func migrateV1(tx *sql.Tx) error {
	queries := []string{
		`CREATE TABLE network_checks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('ok','measurement_error')),
  engine TEXT NOT NULL,
  download_mbps REAL, upload_mbps REAL,
  latency_ms REAL, jitter_ms REAL, packet_loss_pct REAL,
  server_id INTEGER, server_name TEXT, server_location TEXT,
  result_url TEXT,
  public_ip TEXT,
  below_threshold INTEGER NOT NULL DEFAULT 0,
  error TEXT
);`,
		`CREATE INDEX idx_checks_ts ON network_checks(timestamp);`,
		`CREATE TABLE sla_breach_episodes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at TEXT NOT NULL, ended_at TEXT,
  worst_download_mbps REAL, worst_upload_mbps REAL,
  violating_check_count INTEGER NOT NULL,
  total_check_count INTEGER NOT NULL,
  violation_pct REAL NOT NULL
);`,
		`CREATE INDEX idx_sla_open ON sla_breach_episodes(ended_at);`,
		`CREATE TABLE outage_episodes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at TEXT NOT NULL, ended_at TEXT,
  duration_seconds INTEGER,
  reason TEXT
);`,
		`CREATE INDEX idx_outage_open ON outage_episodes(ended_at);`,
	}

	for _, q := range queries {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("failed executing migration v1 statement: %w", err)
		}
	}
	return nil
}

// GetUserVersion retrieves the current PRAGMA user_version.
func GetUserVersion(db *sql.DB) (int, error) {
	var version int
	row := db.QueryRow("PRAGMA user_version;")
	if err := row.Scan(&version); err != nil {
		return 0, fmt.Errorf("failed reading pragma user_version: %w", err)
	}
	return version, nil
}

// SetUserVersion sets the PRAGMA user_version.
func SetUserVersion(db *sql.DB, version int) error {
	_, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d;", version))
	if err != nil {
		return fmt.Errorf("failed setting pragma user_version: %w", err)
	}
	return nil
}

// ApplyMigrations applies all pending migrations in order.
func ApplyMigrations(db *sql.DB) error {
	currentVersion, err := GetUserVersion(db)
	if err != nil {
		return err
	}

	for i := currentVersion; i < len(migrations); i++ {
		targetVersion := i + 1
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed beginning transaction for migration %d: %w", targetVersion, err)
		}

		if err := migrations[i](tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed migration %d: %w", targetVersion, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed committing migration %d: %w", targetVersion, err)
		}

		if err := SetUserVersion(db, targetVersion); err != nil {
			return err
		}
	}

	return nil
}
