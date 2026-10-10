# ISP SLA Monitor — Implementation Plan

## Decisions (from grilling session)

| Decision | Choice |
|---|---|
| Speed tier definition | Static config in `.env` — Plan 1999, 500/500 Mbps |
| Breach threshold | Below 150 Mbps (30% of advertised) for download or upload |
| Violation rule | >20% of non-null checks below threshold in rolling 24h window |
| Assessment window | Rolling 24-hour (48 checks at 30min intervals) |
| Alert channel | Discord webhook (real-time) + HTML email with CSV + PDF |
| Alert frequency | Once per breach episode, silence until resolved |
| Evidence packet | HTML email body + CSV + PDF attachments |
| Discord format | Compact breach card with embed |
| Dashboard | **Scratched** — Grafana LGTM reads PostgreSQL directly |
| Code structure | Modularize, single Docker container |
| Upload threshold | Same 150 Mbps (symmetric plan) |
| Episode tracking | New `sla_breach_episodes` table |
| Two threshold systems | `SPEED_ALERT_*` = user-defined "unusually slow" for reports; `ISP_PLAN_*` = contractual SLA minimum for breach detection |
| NULL speed handling | Failed tests → `below_threshold = False`, count toward `total_check_count` (conservative / ISP-favorable) |
| Startup (< 48 checks) | Defer SLA evaluation, log insufficient data. Only evaluate when full window exists. |

---

## Environment Variables

Replace in `.env` / `.env.example`:

```ini
# Speed alert thresholds (for periodic report "slow incident" counts)
# These are user-defined — "unusually slow for my connection"
SPEED_ALERT_DOWNLOAD=500
SPEED_ALERT_UPLOAD=300

# ISP Plan Configuration (contractual SLA)
ISP_PLAN_NAME="Plan 1999"
ISP_PLAN_SPEED=500        # Advertised download Mbps
ISP_PLAN_UPLOAD_SPEED=500 # Advertised upload Mbps
ISP_PLAN_THRESHOLD=150    # Contractual minimum download Mbps (30% of advertised)
ISP_PLAN_UPLOAD_THRESHOLD=150

# Discord Alerts
DISCORD_WEBHOOK_URL=

# Database Configuration
DB_HOST=db
DB_NAME=network_monitor
DB_USER=your_db_user
DB_PASSWORD=your_secure_password

# Email Configuration
SMTP_SERVER=smtp.gmail.com
SMTP_PORT=587
SMTP_USER=your_email@gmail.com
SMTP_PASSWORD=your_app_password
EMAIL_RECIPIENT=recipient@example.com

# Monitoring Configuration
CHECK_INTERVAL_MINUTES=30
SUMMARY_INTERVAL_HOURS=6
DAILY_REPORT_TIME=23:59
```

**Removed**: `DOWNLOAD_THRESHOLD`, `UPLOAD_THRESHOLD` (replaced by `SPEED_ALERT_DOWNLOAD`, `SPEED_ALERT_UPLOAD`).

---

## Architecture

```
network_monitor.py (417 lines monolithic)
        ↓ refactor into ↓

main.py                    — Entry point, runs scheduler
monitor/
  __init__.py
  config.py                — Env loading, validation, constants
  db.py                    — All PostgreSQL operations (connect, init, save, query, episode CRUD)
  speedtest.py             — perform_speed_test(), get_public_ip()
  sla.py                   — check_sla_breach(), handle_breach_state()
  alerts.py                — send_discord_alert(), send_breach_email()
  reports.py               — get_report_data(), generate_csv(), generate_pdf(), send_email_report()
  scheduler.py             — Schedule setup, perform_network_check()
```

Single Docker container (existing `Dockerfile` and `docker-compose.yml` stay mostly intact).

---

## Database Changes

### 1. Add column to `network_checks`
```sql
ALTER TABLE network_checks 
ADD COLUMN IF NOT EXISTS below_threshold BOOLEAN DEFAULT FALSE;
```
Per-check flag. Single check below ISP contractual minimum, not an SLA breach itself.

### 2. New table `sla_breach_episodes`
```sql
CREATE TABLE IF NOT EXISTS sla_breach_episodes (
    id SERIAL PRIMARY KEY,
    started_at TIMESTAMP NOT NULL,
    ended_at TIMESTAMP,
    worst_download_speed FLOAT,
    worst_upload_speed FLOAT,
    violating_check_count INT NOT NULL,
    total_check_count INT NOT NULL,
    violation_pct FLOAT NOT NULL
);
```
- `ended_at` is NULL while episode is active
- `worst_download_speed` / `worst_upload_speed` track the lowest observed speeds during the episode
- Grafana can query this for breach duration and severity charts

---

## Module Details

### `monitor/config.py`
- Load all env vars via `dotenv`
- Validate required vars: `DB_USER`, `DB_PASSWORD`, `SMTP_USER`, `SMTP_PASSWORD`, `EMAIL_RECIPIENT`, `DISCORD_WEBHOOK_URL`
- Export constants:
  - `SPEED_ALERT_DOWNLOAD`, `SPEED_ALERT_UPLOAD` — for periodic report incident counts
  - `ISP_PLAN_NAME`, `ISP_PLAN_THRESHOLD`, `ISP_PLAN_UPLOAD_THRESHOLD` — SLA breach thresholds
  - `CHECK_INTERVAL_MINUTES`, `SUMMARY_INTERVAL_HOURS`, `DAILY_REPORT_TIME`
  - `SLA_WINDOW_HOURS = 24`
  - `SLA_VIOLATION_MAX = 9` — breach when 10+ checks (20% of 48) are below threshold

### `monitor/db.py`
- `get_connection()` — DRY up the host:port parsing + psycopg2.connect (currently repeated 6x)
- `init_db()` — CREATE DATABASE, then CREATE TABLE for `network_checks` (with `below_threshold` column) and `sla_breach_episodes`
- `save_check_results(download, upload, latency, ip, below_threshold)` — insert one check row
- `get_recent_checks(hours=24)` — returns last N rows for SLA computation
- `get_check_count(hours=24)` — returns count of checks in window (for startup edge case)
- `start_breach_episode(violating_count, total_count, violation_pct, worst_down, worst_up)` — INSERT new episode, returns episode id
- `update_breach_episode(episode_id, worst_down, worst_up, violating_count, total_count, violation_pct)` — UPDATE active episode with running stats (LEAST for worst speeds)
- `end_breach_episode(episode_id)` — SET `ended_at = NOW()` to close the episode
- `get_active_breach_episode()` — returns row where `ended_at IS NULL`, or None

### `monitor/speedtest.py`
- `perform_speed_test()` — unchanged logic, moved from main file
- `get_public_ip()` — unchanged

### `monitor/sla.py`
- **`evaluate_check(download, upload)`** → bool
  - Returns `True` if download < `ISP_PLAN_THRESHOLD` OR upload < `ISP_PLAN_UPLOAD_THRESHOLD`
  - Returns `False` if either value is `None` (failed test → not a violation, conservative)
  
- **`check_sla_breach(db)`** → dict
  - Queries last 24h of checks from DB
  - If fewer than 48 rows: return `{status: "insufficient_data", check_count: N}`
  - Counts non-null checks where evaluate_check() is True
  - Returns `{status: "breach"|"ok", violating_count, total_count, violation_pct, worst_download, worst_upload, breached_checks: [...]}`

- **`handle_breach_state(result, db, alerts)`** — state machine:
  ```
  if status == "insufficient_data":
      log, return
  if status == "breach" and no active_episode:
      episode_id = db.start_breach_episode(...)
      alerts.send_discord_alert(...)
      alerts.send_breach_email(...)
  elif status == "breach" and active_episode:
      db.update_breach_episode(episode_id, ...)   # UPDATE worst speeds
  elif status == "ok" and active_episode:
      db.end_breach_episode(episode_id)
  ```

### `monitor/alerts.py`
- **`send_discord_alert(violating_count, total_count, violation_pct, worst_down, worst_up, plan_name, breached_checks)`**
  - POST to `DISCORD_WEBHOOK_URL` with Discord embed
  - Embed fields: plan name, breach count ("10/48 checks"), violation %, worst speeds, time span
  - Compact — glanceable from phone

- **`send_breach_email(violating_count, total_count, violation_pct, worst_down, worst_up, plan_name, breached_checks, start_time, end_time)`**
  - HTML email body with summary table
  - CSV attachment: all 48 checks in the window with columns: `timestamp, download_speed, upload_speed, latency, ip_address, below_threshold`
  - PDF attachment: same data with trend summary (reuse existing reportlab generation)
  - Subject: "ISP SLA Breach — [Plan Name]"

### `monitor/reports.py`
- Extract existing `get_report_data()`, `generate_pdf_report()`, `generate_csv_report()` from main file
- `get_report_data(hours)` — updated: use `SPEED_ALERT_DOWNLOAD` / `SPEED_ALERT_UPLOAD` for "Slow Download Incidents" / "Slow Upload Incidents" counts
- `send_email_report(hours)` — unchanged flow, uses modularized functions

### `monitor/scheduler.py`
- `perform_network_check()`:
  1. Run speed test → download, upload, latency
  2. Get public IP
  3. Compute `below_threshold` via `sla.evaluate_check(download, upload)`
  4. Save to DB via `db.save_check_results(...)`
  5. Run `sla.check_sla_breach(db)` → `sla.handle_breach_state(...)` 

### `main.py`
- Thin entry point: `db.init_db()`, schedule setup, infinite loop

---

## Files to Modify

| File | Action |
|---|---|
| `network_monitor.py` | **Delete** (replaced by modules) |
| `main.py` | **Create** — thin entry point |
| `monitor/__init__.py` | **Create** — empty |
| `monitor/config.py` | **Create** |
| `monitor/db.py` | **Create** |
| `monitor/speedtest.py` | **Create** |
| `monitor/sla.py` | **Create** |
| `monitor/alerts.py` | **Create** |
| `monitor/reports.py` | **Create** |
| `monitor/scheduler.py` | **Create** |
| `.env.example` | **Update** — rename thresholds, add ISP plan + Discord vars |
| `Dockerfile` | **Update** — `COPY monitor/ monitor/`, `COPY main.py .`, change `CMD` |
| `tests/test_network_monitor.py` | **Update** — change imports to new module paths, add SLA + NULL tests |
| `tests/test_email_reports.py` | **Update** — change imports, add Discord + breach email tests |
| `requirements.txt` | No changes needed (Discord uses `requests` already present) |

---

## Edge Cases Handled

| Case | Behavior |
|---|---|
| **Startup (< 48 checks)** | `check_sla_breach()` returns `insufficient_data`. SLA evaluation skipped. Logged. |
| **Failed speed test (None values)** | `evaluate_check()` returns `False` (conservative). `below_threshold = False` saved. Counts in total but not in violating. |
| **All 48 checks below threshold** | `violation_pct = 100%`. Breach fires. Episode tracks full severity. |
| **Zero checks below threshold** | `status = "ok"`. If episode was active, it ends. |
| **Episode spanning multiple days** | Episode stays open across midnight. `ended_at` only set when SLA clears. |
| **Discord webhook down** | Log error, don't crash. Email still sends (separate try/except). |
| **DB connection lost during episode update** | `end_breach_episode()` won't fire. Episode stays "open" until next successful check and resolution. Acceptable — Grafana can filter stale episodes. |

---

## Grafana Integration

Post-setup (out of scope), point Grafana at the same PostgreSQL:

- **Speed over time**: `SELECT timestamp, download_speed, upload_speed FROM network_checks ORDER BY timestamp`
- **Below threshold markers**: `SELECT timestamp FROM network_checks WHERE below_threshold = true`
- **SLA breach episodes**: `SELECT * FROM sla_breach_episodes ORDER BY started_at`
- **Violation % (rolling 24h)**:
  ```sql
  SELECT 
    COUNT(*) FILTER (WHERE below_threshold) AS violating,
    COUNT(*) AS total,
    ROUND(COUNT(*) FILTER (WHERE below_threshold) * 100.0 / COUNT(*), 1) AS pct
  FROM network_checks 
  WHERE timestamp > NOW() - INTERVAL '24 hours'
  ```

---

## Verification

1. **Unit tests**: Run `pytest tests/` — all existing tests pass with updated imports
2. **SLA logic test**: Feed mock DB with controlled data (10 checks below 150, 38 above) → verify breach fires
3. **NULL handling test**: Mock checks with None speeds → verify `below_threshold=False`, counts in total
4. **Startup test**: DB with 5 checks → verify `insufficient_data` returned, no alert
5. **Episode lifecycle**: Breach starts → row created. Update called → worst speeds tracked. Resolution → `ended_at` set
6. **Discord alert**: Mock `requests.post` → verify embed payload structure
7. **Email**: Mock SMTP → verify HTML body, CSV attachment columns, PDF generation
8. **Docker**: `docker-compose up --build` → verify container starts, runs checks, logs cleanly
