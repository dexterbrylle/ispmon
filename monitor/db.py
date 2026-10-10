import os
import sqlite3
import logging
from datetime import datetime, timezone
from pathlib import Path
from monitor.config import DB_PATH

logger = logging.getLogger(__name__)


def get_connection(db_path=None):
    """Get an SQLite connection with WAL mode and row factory configured."""
    target_path = db_path if db_path is not None else DB_PATH

    if target_path != ':memory:':
        parent = os.path.dirname(target_path)
        if parent:
            Path(parent).mkdir(parents=True, exist_ok=True)

    conn = sqlite3.connect(target_path, timeout=10.0)
    conn.row_factory = sqlite3.Row

    try:
        if target_path != ':memory:':
            conn.execute("PRAGMA journal_mode = WAL;")
        conn.execute("PRAGMA busy_timeout = 5000;")
        conn.execute("PRAGMA foreign_keys = ON;")
    except Exception as e:
        logger.warning(f"Could not configure pragmas on SQLite connection: {e}")

    return conn


def init_db(db_path=None):
    """Initialize database tables and indexes."""
    conn = get_connection(db_path)
    try:
        with conn:
            conn.execute("""
                CREATE TABLE IF NOT EXISTS network_checks (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    timestamp TEXT NOT NULL,
                    download_speed REAL,
                    upload_speed REAL,
                    latency REAL,
                    jitter REAL,
                    packet_loss REAL,
                    ip_address TEXT,
                    server_name TEXT,
                    result_url TEXT,
                    below_threshold INTEGER DEFAULT 0,
                    status TEXT NOT NULL DEFAULT 'ok',
                    error TEXT
                );
            """)

            conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_network_checks_timestamp 
                ON network_checks(timestamp);
            """)

            conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_network_checks_status 
                ON network_checks(status);
            """)

            conn.execute("""
                CREATE TABLE IF NOT EXISTS sla_breach_episodes (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    started_at TEXT NOT NULL,
                    ended_at TEXT,
                    worst_download_speed REAL,
                    worst_upload_speed REAL,
                    violating_check_count INTEGER NOT NULL,
                    total_check_count INTEGER NOT NULL,
                    violation_pct REAL NOT NULL
                );
            """)

            conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_sla_episodes_active 
                ON sla_breach_episodes(ended_at);
            """)

            conn.execute("""
                CREATE TABLE IF NOT EXISTS outage_episodes (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    started_at TEXT NOT NULL,
                    ended_at TEXT,
                    duration_seconds INTEGER,
                    reason TEXT
                );
            """)

            conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_outage_episodes_active 
                ON outage_episodes(ended_at);
            """)

        logger.info("Database initialized successfully (SQLite)")
    finally:
        conn.close()


def save_check_results(
    download_speed,
    upload_speed,
    latency,
    ip_address,
    below_threshold=False,
    status='ok',
    error=None,
    jitter=None,
    packet_loss=None,
    server_name=None,
    result_url=None,
    timestamp=None,
    db_path=None
):
    """Save a network check result to the SQLite database."""
    conn = get_connection(db_path)
    now_str = timestamp or datetime.now(timezone.utc).isoformat()
    try:
        with conn:
            cur = conn.cursor()
            cur.execute("""
                INSERT INTO network_checks (
                    timestamp, download_speed, upload_speed, latency,
                    jitter, packet_loss, ip_address, server_name, result_url,
                    below_threshold, status, error
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """, (
                now_str,
                download_speed,
                upload_speed,
                latency,
                jitter,
                packet_loss,
                ip_address,
                server_name,
                result_url,
                1 if below_threshold else 0,
                status,
                error
            ))
            row_id = cur.lastrowid
            logger.info(f"Check result saved to database (ID: {row_id}, Status: {status})")
            return row_id
    except Exception as e:
        logger.error(f"Database save error: {e}")
        return None
    finally:
        conn.close()


def get_recent_checks(hours=24, db_path=None):
    """Get checks from the last N hours for SLA and outage evaluation."""
    conn = get_connection(db_path)
    try:
        cur = conn.cursor()
        cur.execute("""
            SELECT *
            FROM network_checks
            WHERE timestamp >= datetime('now', '-' || ? || ' hours')
            ORDER BY timestamp ASC
        """, (hours,))
        return [dict(r) for r in cur.fetchall()]
    except Exception as e:
        logger.error(f"Error getting recent checks: {e}")
        return []
    finally:
        conn.close()


def get_check_count(hours=24, db_path=None):
    """Get total check count in the given rolling window."""
    conn = get_connection(db_path)
    try:
        cur = conn.cursor()
        cur.execute("""
            SELECT COUNT(*)
            FROM network_checks
            WHERE timestamp >= datetime('now', '-' || ? || ' hours')
        """, (hours,))
        row = cur.fetchone()
        return row[0] if row else 0
    except Exception as e:
        logger.error(f"Error getting check count: {e}")
        return 0
    finally:
        conn.close()


def get_report_data(hours=24, db_path=None):
    """Get raw records for periodic reports."""
    conn = get_connection(db_path)
    try:
        cur = conn.cursor()
        cur.execute("""
            SELECT *
            FROM network_checks
            WHERE timestamp >= datetime('now', '-' || ? || ' hours')
            ORDER BY timestamp DESC
        """, (hours,))
        results = cur.fetchall()
        return [dict(r) for r in results] if results else None
    except Exception as e:
        logger.error(f"Error getting report data: {e}")
        return None
    finally:
        conn.close()


def start_breach_episode(violating_count, total_count, violation_pct, worst_down, worst_up, db_path=None):
    """Start a new SLA breach episode. Returns the episode ID."""
    conn = get_connection(db_path)
    now_str = datetime.now(timezone.utc).isoformat()
    try:
        with conn:
            cur = conn.cursor()
            cur.execute("""
                INSERT INTO sla_breach_episodes (
                    started_at, worst_download_speed, worst_upload_speed,
                    violating_check_count, total_check_count, violation_pct
                )
                VALUES (?, ?, ?, ?, ?, ?)
            """, (now_str, worst_down, worst_up, violating_count, total_count, violation_pct))
            episode_id = cur.lastrowid
            logger.info(f"SLA breach episode {episode_id} started")
            return episode_id
    except Exception as e:
        logger.error(f"Error starting breach episode: {e}")
        return None
    finally:
        conn.close()


def update_breach_episode(episode_id, worst_down, worst_up, violating_count, total_count, violation_pct, db_path=None):
    """Update an active breach episode with latest stats."""
    conn = get_connection(db_path)
    try:
        with conn:
            conn.execute("""
                UPDATE sla_breach_episodes
                SET worst_download_speed = CASE 
                        WHEN worst_download_speed IS NULL OR ? < worst_download_speed THEN ? 
                        ELSE worst_download_speed 
                    END,
                    worst_upload_speed = CASE 
                        WHEN worst_upload_speed IS NULL OR ? < worst_upload_speed THEN ? 
                        ELSE worst_upload_speed 
                    END,
                    violating_check_count = ?,
                    total_check_count = ?,
                    violation_pct = ?
                WHERE id = ? AND ended_at IS NULL
            """, (worst_down, worst_down, worst_up, worst_up, violating_count, total_count, violation_pct, episode_id))
    except Exception as e:
        logger.error(f"Error updating breach episode: {e}")
    finally:
        conn.close()


def end_breach_episode(episode_id, db_path=None):
    """Close an active breach episode."""
    conn = get_connection(db_path)
    now_str = datetime.now(timezone.utc).isoformat()
    try:
        with conn:
            conn.execute("""
                UPDATE sla_breach_episodes
                SET ended_at = ?
                WHERE id = ? AND ended_at IS NULL
            """, (now_str, episode_id))
            logger.info(f"SLA breach episode {episode_id} ended")
    except Exception as e:
        logger.error(f"Error ending breach episode: {e}")
    finally:
        conn.close()


def get_active_breach_episode(db_path=None):
    """Get the currently active SLA breach episode, or None."""
    conn = get_connection(db_path)
    try:
        cur = conn.cursor()
        cur.execute("""
            SELECT *
            FROM sla_breach_episodes
            WHERE ended_at IS NULL
            ORDER BY started_at DESC
            LIMIT 1
        """)
        row = cur.fetchone()
        return dict(row) if row else None
    except Exception as e:
        logger.error(f"Error getting active breach episode: {e}")
        return None
    finally:
        conn.close()


def start_outage_episode(reason, db_path=None):
    """Start an outage episode. Returns episode ID."""
    conn = get_connection(db_path)
    now_str = datetime.now(timezone.utc).isoformat()
    try:
        with conn:
            cur = conn.cursor()
            cur.execute("""
                INSERT INTO outage_episodes (started_at, reason)
                VALUES (?, ?)
            """, (now_str, reason))
            episode_id = cur.lastrowid
            logger.warning(f"Outage episode {episode_id} started: {reason}")
            return episode_id
    except Exception as e:
        logger.error(f"Error starting outage episode: {e}")
        return None
    finally:
        conn.close()


def end_outage_episode(episode_id, db_path=None):
    """End an outage episode and compute duration."""
    conn = get_connection(db_path)
    now = datetime.now(timezone.utc)
    try:
        with conn:
            cur = conn.cursor()
            cur.execute("SELECT started_at FROM outage_episodes WHERE id = ?", (episode_id,))
            row = cur.fetchone()
            duration = None
            if row and row['started_at']:
                try:
                    start_dt = datetime.fromisoformat(row['started_at'])
                    duration = int((now - start_dt).total_seconds())
                except Exception:
                    duration = None

            conn.execute("""
                UPDATE outage_episodes
                SET ended_at = ?, duration_seconds = ?
                WHERE id = ? AND ended_at IS NULL
            """, (now.isoformat(), duration, episode_id))
            logger.info(f"Outage episode {episode_id} ended (Duration: {duration}s)")
            return duration
    except Exception as e:
        logger.error(f"Error ending outage episode: {e}")
        return None
    finally:
        conn.close()


def get_active_outage_episode(db_path=None):
    """Get the currently active outage episode, or None."""
    conn = get_connection(db_path)
    try:
        cur = conn.cursor()
        cur.execute("""
            SELECT *
            FROM outage_episodes
            WHERE ended_at IS NULL
            ORDER BY started_at DESC
            LIMIT 1
        """)
        row = cur.fetchone()
        return dict(row) if row else None
    except Exception as e:
        logger.error(f"Error getting active outage episode: {e}")
        return None
    finally:
        conn.close()
