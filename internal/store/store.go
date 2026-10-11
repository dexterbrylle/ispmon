package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store encapsulates SQLite database operations for ispmon.
type Store struct {
	db *sql.DB
}

// New opens an SQLite database at path, configures pragmas, and applies migrations.
func New(path string) (*Store, error) {
	if path != ":memory:" {
		parent := filepath.Dir(path)
		if parent != "" && parent != "." {
			if err := os.MkdirAll(parent, 0755); err != nil {
				return nil, fmt.Errorf("failed creating db parent directory: %w", err)
			}
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed opening sqlite db: %w", err)
	}

	db.SetMaxOpenConns(1)

	if path != ":memory:" {
		if _, err := db.Exec("PRAGMA journal_mode = WAL;"); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed setting journal_mode WAL: %w", err)
		}
	}

	if _, err := db.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed setting busy_timeout: %w", err)
	}

	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed setting foreign_keys: %w", err)
	}

	// Enable incremental auto-vacuum for newly created databases
	_, _ = db.Exec("PRAGMA auto_vacuum = INCREMENTAL;")

	if err := ApplyMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed applying migrations: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying database handle.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Invariant: all stored timestamps must be UTC RFC 3339 ("Z") so SQLite string range comparisons remain chronologically valid.
// SaveCheck persists a network check to SQLite.
func (s *Store) SaveCheck(ctx context.Context, c *NetworkCheck) (int64, error) {
	query := `INSERT INTO network_checks (
		timestamp, status, engine, download_mbps, upload_mbps,
		latency_ms, jitter_ms, packet_loss_pct,
		server_id, server_name, server_location,
		result_url, public_ip, below_threshold, error
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	belowVal := 0
	if c.BelowThreshold {
		belowVal = 1
	}

	ts := c.Timestamp.UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, query,
		ts, c.Status, c.Engine, c.DownloadMbps, c.UploadMbps,
		c.LatencyMs, c.JitterMs, c.PacketLossPct,
		c.ServerID, c.ServerName, c.ServerLocation,
		c.ResultURL, c.PublicIP, belowVal, c.Error,
	)
	if err != nil {
		return 0, fmt.Errorf("failed inserting network check: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed fetching last insert id: %w", err)
	}
	c.ID = id
	return id, nil
}

func scanCheck(scanner interface{ Scan(...any) error }) (*NetworkCheck, error) {
	var (
		c              NetworkCheck
		tsStr          string
		belowVal       int
		down, up       sql.NullFloat64
		lat, jit, loss sql.NullFloat64
		srvID          sql.NullInt64
		srvName        sql.NullString
		srvLoc         sql.NullString
		resURL         sql.NullString
		pubIP          sql.NullString
		errStr         sql.NullString
	)

	err := scanner.Scan(
		&c.ID, &tsStr, &c.Status, &c.Engine,
		&down, &up, &lat, &jit, &loss,
		&srvID, &srvName, &srvLoc,
		&resURL, &pubIP, &belowVal, &errStr,
	)
	if err != nil {
		return nil, err
	}

	t, err := time.Parse(time.RFC3339, tsStr)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp %q: %w", tsStr, err)
	}
	c.Timestamp = t.UTC()

	if down.Valid {
		c.DownloadMbps = &down.Float64
	}
	if up.Valid {
		c.UploadMbps = &up.Float64
	}
	if lat.Valid {
		c.LatencyMs = &lat.Float64
	}
	if jit.Valid {
		c.JitterMs = &jit.Float64
	}
	if loss.Valid {
		c.PacketLossPct = &loss.Float64
	}
	if srvID.Valid {
		c.ServerID = &srvID.Int64
	}
	if srvName.Valid {
		c.ServerName = &srvName.String
	}
	if srvLoc.Valid {
		c.ServerLocation = &srvLoc.String
	}
	if resURL.Valid {
		c.ResultURL = &resURL.String
	}
	if pubIP.Valid {
		c.PublicIP = &pubIP.String
	}
	if errStr.Valid {
		c.Error = &errStr.String
	}
	c.BelowThreshold = belowVal == 1

	return &c, nil
}

// GetRecentChecks retrieves all checks on or after the given time, ordered chronologically.
func (s *Store) GetRecentChecks(ctx context.Context, since time.Time) ([]NetworkCheck, error) {
	query := `SELECT id, timestamp, status, engine, download_mbps, upload_mbps,
		latency_ms, jitter_ms, packet_loss_pct, server_id, server_name, server_location,
		result_url, public_ip, below_threshold, error
		FROM network_checks
		WHERE timestamp >= ?
		ORDER BY timestamp ASC`

	rows, err := s.db.QueryContext(ctx, query, since.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("failed querying recent checks: %w", err)
	}
	defer rows.Close()

	var checks []NetworkCheck
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, fmt.Errorf("failed scanning check row: %w", err)
		}
		checks = append(checks, *c)
	}
	return checks, rows.Err()
}

// GetChecksBetween retrieves checks in the [start, end] window, ordered chronologically.
func (s *Store) GetChecksBetween(ctx context.Context, start, end time.Time) ([]NetworkCheck, error) {
	query := `SELECT id, timestamp, status, engine, download_mbps, upload_mbps,
		latency_ms, jitter_ms, packet_loss_pct, server_id, server_name, server_location,
		result_url, public_ip, below_threshold, error
		FROM network_checks
		WHERE timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp ASC`

	rows, err := s.db.QueryContext(ctx, query, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("failed querying checks between: %w", err)
	}
	defer rows.Close()

	var checks []NetworkCheck
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, fmt.Errorf("failed scanning check row: %w", err)
		}
		checks = append(checks, *c)
	}
	return checks, rows.Err()
}

// GetActiveSLABreach returns the open SLA breach episode, or nil if none is open.
func (s *Store) GetActiveSLABreach(ctx context.Context) (*SLABreachEpisode, error) {
	query := `SELECT id, started_at, ended_at, worst_download_mbps, worst_upload_mbps,
		violating_check_count, total_check_count, violation_pct
		FROM sla_breach_episodes
		WHERE ended_at IS NULL
		ORDER BY started_at DESC
		LIMIT 1`

	row := s.db.QueryRowContext(ctx, query)
	var (
		ep        SLABreachEpisode
		startStr  string
		endStr    sql.NullString
		worstDown sql.NullFloat64
		worstUp   sql.NullFloat64
	)

	err := row.Scan(
		&ep.ID, &startStr, &endStr,
		&worstDown, &worstUp,
		&ep.ViolatingCheckCount, &ep.TotalCheckCount, &ep.ViolationPct,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed reading active sla breach: %w", err)
	}

	st, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		return nil, fmt.Errorf("invalid started_at %q: %w", startStr, err)
	}
	ep.StartedAt = st.UTC()

	if worstDown.Valid {
		ep.WorstDownloadMbps = &worstDown.Float64
	}
	if worstUp.Valid {
		ep.WorstUploadMbps = &worstUp.Float64
	}

	return &ep, nil
}

// StartSLABreach opens a new SLA breach episode. Fails if an open episode already exists.
func (s *Store) StartSLABreach(ctx context.Context, ep *SLABreachEpisode, events ...any) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var openCount int
	err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sla_breach_episodes WHERE ended_at IS NULL").Scan(&openCount)
	if err != nil {
		return 0, fmt.Errorf("failed checking open sla episodes: %w", err)
	}
	if openCount > 0 {
		return 0, ErrOpenEpisodeExists
	}

	query := `INSERT INTO sla_breach_episodes (
		started_at, ended_at, worst_download_mbps, worst_upload_mbps,
		violating_check_count, total_check_count, violation_pct
	) VALUES (?, NULL, ?, ?, ?, ?, ?)`

	res, err := tx.ExecContext(ctx, query,
		ep.StartedAt.UTC().Format(time.RFC3339),
		ep.WorstDownloadMbps, ep.WorstUploadMbps,
		ep.ViolatingCheckCount, ep.TotalCheckCount, ep.ViolationPct,
	)
	if err != nil {
		return 0, fmt.Errorf("failed inserting sla breach episode: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed getting sla breach id: %w", err)
	}

	for _, event := range events {
		if err := queueNotification(ctx, tx, "sla_breach_episodes", id, event); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed committing sla breach: %w", err)
	}

	ep.ID = id
	return id, nil
}

// UpdateSLABreach updates metrics for an active SLA breach episode.
func (s *Store) UpdateSLABreach(ctx context.Context, ep *SLABreachEpisode) error {
	query := `UPDATE sla_breach_episodes
		SET worst_download_mbps = CASE
				WHEN worst_download_mbps IS NULL OR ? < worst_download_mbps THEN ?
				ELSE worst_download_mbps
			END,
			worst_upload_mbps = CASE
				WHEN worst_upload_mbps IS NULL OR ? < worst_upload_mbps THEN ?
				ELSE worst_upload_mbps
			END,
			violating_check_count = ?,
			total_check_count = ?,
			violation_pct = ?
		WHERE id = ? AND ended_at IS NULL`

	res, err := s.db.ExecContext(ctx, query,
		ep.WorstDownloadMbps, ep.WorstDownloadMbps,
		ep.WorstUploadMbps, ep.WorstUploadMbps,
		ep.ViolatingCheckCount, ep.TotalCheckCount, ep.ViolationPct,
		ep.ID,
	)
	if err != nil {
		return fmt.Errorf("failed updating sla breach episode: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed fetching affected rows: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// EndSLABreach closes an active SLA breach episode.
func (s *Store) EndSLABreach(ctx context.Context, id int64, endedAt time.Time, events ...any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `UPDATE sla_breach_episodes
		SET ended_at = ?
		WHERE id = ? AND ended_at IS NULL`

	res, err := tx.ExecContext(ctx, query, endedAt.UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("failed closing sla breach episode: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed fetching affected rows: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	for _, event := range events {
		if err := queueNotification(ctx, tx, "sla_breach_episodes", id, event); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetSLABreachEpisodesBetween returns SLA breach episodes overlapping or starting in [start, end].
func (s *Store) GetSLABreachEpisodesBetween(ctx context.Context, start, end time.Time) ([]SLABreachEpisode, error) {
	query := `SELECT id, started_at, ended_at, worst_download_mbps, worst_upload_mbps,
		violating_check_count, total_check_count, violation_pct
		FROM sla_breach_episodes
		WHERE started_at <= ? AND (ended_at IS NULL OR ended_at >= ?)
		ORDER BY started_at ASC`

	rows, err := s.db.QueryContext(ctx, query, end.UTC().Format(time.RFC3339), start.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("failed querying sla breaches: %w", err)
	}
	defer rows.Close()

	var eps []SLABreachEpisode
	for rows.Next() {
		var (
			ep        SLABreachEpisode
			startStr  string
			endStr    sql.NullString
			worstDown sql.NullFloat64
			worstUp   sql.NullFloat64
		)
		err := rows.Scan(
			&ep.ID, &startStr, &endStr,
			&worstDown, &worstUp,
			&ep.ViolatingCheckCount, &ep.TotalCheckCount, &ep.ViolationPct,
		)
		if err != nil {
			return nil, fmt.Errorf("failed scanning sla breach row: %w", err)
		}
		st, err := time.Parse(time.RFC3339, startStr)
		if err != nil {
			return nil, fmt.Errorf("invalid started_at %q: %w", startStr, err)
		}
		ep.StartedAt = st.UTC()
		if endStr.Valid {
			et, err := time.Parse(time.RFC3339, endStr.String)
			if err != nil {
				return nil, fmt.Errorf("invalid ended_at %q: %w", endStr.String, err)
			}
			t := et.UTC()
			ep.EndedAt = &t
		}
		if worstDown.Valid {
			ep.WorstDownloadMbps = &worstDown.Float64
		}
		if worstUp.Valid {
			ep.WorstUploadMbps = &worstUp.Float64
		}
		eps = append(eps, ep)
	}
	return eps, rows.Err()
}

// GetActiveOutage returns the open outage episode, or nil if none is open.
func (s *Store) GetActiveOutage(ctx context.Context) (*OutageEpisode, error) {
	query := `SELECT id, started_at, ended_at, duration_seconds, reason
		FROM outage_episodes
		WHERE ended_at IS NULL
		ORDER BY started_at DESC
		LIMIT 1`

	row := s.db.QueryRowContext(ctx, query)
	var (
		ep       OutageEpisode
		startStr string
		endStr   sql.NullString
		dur      sql.NullInt64
		reason   sql.NullString
	)

	err := row.Scan(&ep.ID, &startStr, &endStr, &dur, &reason)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed reading active outage: %w", err)
	}

	st, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		return nil, fmt.Errorf("invalid started_at %q: %w", startStr, err)
	}
	ep.StartedAt = st.UTC()

	if reason.Valid {
		ep.Reason = reason.String
	}
	return &ep, nil
}

// StartOutage opens a new outage episode. Fails if an open outage already exists.
func (s *Store) StartOutage(ctx context.Context, ep *OutageEpisode) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var openCount int
	err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM outage_episodes WHERE ended_at IS NULL").Scan(&openCount)
	if err != nil {
		return 0, fmt.Errorf("failed checking open outages: %w", err)
	}
	if openCount > 0 {
		return 0, ErrOpenEpisodeExists
	}

	query := `INSERT INTO outage_episodes (
		started_at, ended_at, duration_seconds, reason
	) VALUES (?, NULL, NULL, ?)`

	res, err := tx.ExecContext(ctx, query,
		ep.StartedAt.UTC().Format(time.RFC3339),
		ep.Reason,
	)
	if err != nil {
		return 0, fmt.Errorf("failed inserting outage episode: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed getting outage id: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed committing outage: %w", err)
	}

	ep.ID = id
	return id, nil
}

// EndOutage closes an active outage episode and records duration.
func (s *Store) EndOutage(ctx context.Context, id int64, endedAt time.Time, durationSeconds int, events ...any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `UPDATE outage_episodes
		SET ended_at = ?, duration_seconds = ?
		WHERE id = ? AND ended_at IS NULL`

	res, err := tx.ExecContext(ctx, query,
		endedAt.UTC().Format(time.RFC3339),
		durationSeconds,
		id,
	)
	if err != nil {
		return fmt.Errorf("failed closing outage episode: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed fetching affected rows: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	for _, event := range events {
		if err := queueNotification(ctx, tx, "outage_episodes", id, event); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetOutageEpisodesBetween returns outage episodes in the given time window.
func (s *Store) GetOutageEpisodesBetween(ctx context.Context, start, end time.Time) ([]OutageEpisode, error) {
	query := `SELECT id, started_at, ended_at, duration_seconds, reason
		FROM outage_episodes
		WHERE started_at <= ? AND (ended_at IS NULL OR ended_at >= ?)
		ORDER BY started_at ASC`

	rows, err := s.db.QueryContext(ctx, query, end.UTC().Format(time.RFC3339), start.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("failed querying outages: %w", err)
	}
	defer rows.Close()

	var eps []OutageEpisode
	for rows.Next() {
		var (
			ep       OutageEpisode
			startStr string
			endStr   sql.NullString
			dur      sql.NullInt64
			reason   sql.NullString
		)
		err := rows.Scan(&ep.ID, &startStr, &endStr, &dur, &reason)
		if err != nil {
			return nil, fmt.Errorf("failed scanning outage row: %w", err)
		}
		st, err := time.Parse(time.RFC3339, startStr)
		if err != nil {
			return nil, fmt.Errorf("invalid started_at %q: %w", startStr, err)
		}
		ep.StartedAt = st.UTC()
		if endStr.Valid {
			et, err := time.Parse(time.RFC3339, endStr.String)
			if err != nil {
				return nil, fmt.Errorf("invalid ended_at %q: %w", endStr.String, err)
			}
			t := et.UTC()
			ep.EndedAt = &t
		}
		if dur.Valid {
			d := int(dur.Int64)
			ep.DurationSeconds = &d
		}
		if reason.Valid {
			ep.Reason = reason.String
		}
		eps = append(eps, ep)
	}
	return eps, rows.Err()
}

// PruneOldData deletes network_checks and closed episodes (SLA breaches and outages) older than retentionDays.
// If retentionDays <= 0, no pruning is performed.
// Open episodes (where ended_at IS NULL) are never deleted.
// After deleting, Vacuum is invoked to reclaim storage space.
func (s *Store) PruneOldData(ctx context.Context, retentionDays int, now time.Time) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}

	cutoff := now.UTC().AddDate(0, 0, -retentionDays)
	cutoffStr := cutoff.Format(time.RFC3339)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed beginning prune transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var totalDeleted int64

	// 1. Delete old network checks
	res, err := tx.ExecContext(ctx, "DELETE FROM network_checks WHERE timestamp < ?", cutoffStr)
	if err != nil {
		return 0, fmt.Errorf("failed pruning network_checks: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil {
		totalDeleted += n
	}

	// 2. Delete old closed SLA breach episodes
	res, err = tx.ExecContext(ctx, "DELETE FROM sla_breach_episodes WHERE ended_at IS NOT NULL AND ended_at < ?", cutoffStr)
	if err != nil {
		return 0, fmt.Errorf("failed pruning sla_breach_episodes: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil {
		totalDeleted += n
	}

	// 3. Delete old closed outage episodes
	res, err = tx.ExecContext(ctx, "DELETE FROM outage_episodes WHERE ended_at IS NOT NULL AND ended_at < ?", cutoffStr)
	if err != nil {
		return 0, fmt.Errorf("failed pruning outage_episodes: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil {
		totalDeleted += n
	}

	// 4. Clean up any notification_outbox references for deleted episodes
	_, _ = tx.ExecContext(ctx, "DELETE FROM notification_outbox WHERE episode_table = 'sla_breach_episodes' AND episode_id NOT IN (SELECT id FROM sla_breach_episodes)")
	_, _ = tx.ExecContext(ctx, "DELETE FROM notification_outbox WHERE episode_table = 'outage_episodes' AND episode_id NOT IN (SELECT id FROM outage_episodes)")

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed committing prune transaction: %w", err)
	}

	// Reclaim storage sensibly outside transaction
	if err := s.Vacuum(ctx); err != nil {
		return totalDeleted, fmt.Errorf("failed vacuuming database after prune: %w", err)
	}

	return totalDeleted, nil
}

// Vacuum reclaims freed database pages using incremental_vacuum if auto_vacuum is INCREMENTAL,
// or VACUUM otherwise.
func (s *Store) Vacuum(ctx context.Context) error {
	var autoVacuum int
	_ = s.db.QueryRowContext(ctx, "PRAGMA auto_vacuum;").Scan(&autoVacuum)
	if autoVacuum == 2 {
		_, err := s.db.ExecContext(ctx, "PRAGMA incremental_vacuum;")
		return err
	}
	_, err := s.db.ExecContext(ctx, "VACUUM;")
	return err
}

