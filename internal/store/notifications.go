package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type PendingNotification struct {
	ID        int64
	Table     string
	EpisodeID int64
	Payload   []byte
}

func (s *Store) QueueNotification(ctx context.Context, table string, id int64, event any) error {
	return queueNotification(ctx, s.db, table, id, event)
}

type notificationWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func queueNotification(ctx context.Context, writer notificationWriter, table string, id int64, event any) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = writer.ExecContext(ctx, "INSERT INTO notification_outbox (episode_table,episode_id,payload) VALUES (?,?,?)", table, id, string(data))
	return err
}
func (s *Store) QueueUnsentSLABreach(ctx context.Context, id int64, event any) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO notification_outbox (episode_table,episode_id,payload)
 SELECT 'sla_breach_episodes',id,? FROM sla_breach_episodes
 WHERE id=? AND notified_at IS NULL AND NOT EXISTS
 (SELECT 1 FROM notification_outbox WHERE episode_table='sla_breach_episodes' AND episode_id=?)`, string(data), id, id)
	return err
}

func (s *Store) PendingNotifications(ctx context.Context) ([]PendingNotification, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, episode_table, episode_id, payload FROM notification_outbox ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pending []PendingNotification
	for rows.Next() {
		var p PendingNotification
		if err := rows.Scan(&p.ID, &p.Table, &p.EpisodeID, &p.Payload); err != nil {
			return nil, err
		}
		pending = append(pending, p)
	}
	return pending, rows.Err()
}
func (s *Store) CompleteNotification(ctx context.Context, p PendingNotification, now time.Time) error {
	if p.Table != "sla_breach_episodes" && p.Table != "outage_episodes" {
		return fmt.Errorf("invalid episode table %q", p.Table)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE "+p.Table+" SET notified_at = ? WHERE id = ?", now.UTC().Format(time.RFC3339), p.EpisodeID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM notification_outbox WHERE id = ?", p.ID); err != nil {
		return err
	}
	return tx.Commit()
}
