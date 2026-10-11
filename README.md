# ispmon — ISP Speed & SLA Breach Monitor

A lightweight, self-hosted Go daemon for people who pay for a fixed-speed internet plan and want dispute-ready evidence when their ISP fails to deliver.

---

## Why ispmon?

Most home and business internet contracts specify a **minimum guaranteed speed** (often 30% to 80% of advertised bandwidth) and an **uptime commitment**. However, when your connection degrades, customer support often demands proof over extended periods.

`ispmon`:
1. Executes the official Ookla `speedtest` CLI on a regular interval and logs bandwidth, latency, jitter, packet loss, and official `speedtest.net` verification URLs to SQLite.
2. Evaluates contractual SLA compliance over a rolling window (e.g. 24 hours). Declares a breach when speeds repeatedly violate your contract and alerts you with official proof links.
3. Detects connectivity outages separately via lightweight TCP probes without distorting speed ratios.
4. Generates dispute packets (in-memory CSV and PDF evidence reports) and dispatches alerts via Discord webhooks and SMTP email.

---

## Quickstart

### Option 1: Docker Compose (Recommended)

1. **Clone the repository and prepare configuration:**
   ```bash
   cp .env.example .env
   nano .env
   ```
   *Set your ISP plan name, advertised speeds, contractual thresholds, and optional Discord/SMTP details.*

2. **Start the monitor:**
   ```bash
   docker compose up -d
   ```

3. **Inspect logs and health:**
   ```bash
   docker compose logs -f
   docker compose ps
   ```

---

### Option 2: Binary / Local Setup

#### 1. Install the Official Ookla Speedtest CLI

`ispmon` uses the official Ookla binary to capture Gigabit-accurate speeds and official dispute proof links.

- **macOS (Homebrew):**
  ```bash
  brew install speedtest
  ```

- **Debian / Ubuntu:**
  ```bash
  curl -s https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.deb.sh | sudo bash
  sudo apt-get install speedtest
  ```

- **RedHat / Fedora / CentOS:**
  ```bash
  curl -s https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.rpm.sh | sudo bash
  sudo dnf install speedtest
  ```

#### 2. Build and Install ispmon

Requires **Go 1.22+**.

```bash
# Build binary
make build

# Or install to $GOPATH/bin
go install ./cmd/ispmon
```

#### 3. Run the Daemon

```bash
cp .env.example .env
# Edit .env with your plan parameters
./bin/ispmon run
```

Subcommands:
- `ispmon run`: Run monitoring daemon (default)
- `ispmon healthcheck`: Check heartbeat freshness (exit 0 if healthy, 1 if stale)
- `ispmon version`: Print version and build details

---

## Non-Docker Service Deployment

Service templates with commented placeholders are provided under [`deploy/`](deploy/):

- **macOS (launchd):**
  Edit [`deploy/launchd/com.dexterbrylle.ispmon.plist`](deploy/launchd/com.dexterbrylle.ispmon.plist) and install to `~/Library/LaunchAgents/com.dexterbrylle.ispmon.plist`. Load with `launchctl load ~/Library/LaunchAgents/com.dexterbrylle.ispmon.plist`.
- **Linux (systemd):**
  Edit [`deploy/systemd/ispmon.service`](deploy/systemd/ispmon.service), copy to `/etc/systemd/system/ispmon.service`, and enable with `sudo systemctl enable --now ispmon`.

---

## Configuration Reference

Configuration is managed via environment variables (loaded from `.env` if present).

| Variable | Required | Default | Description |
|---|---|---|---|
| `ISP_PLAN_NAME` | **Yes** | *None* | Free text description of your internet plan, displayed in alerts and reports. |
| `ISP_PLAN_SPEED` | **Yes** | *None* | Advertised download bandwidth in Mbps (e.g. `500`). |
| `ISP_PLAN_UPLOAD_SPEED` | **Yes** | *None* | Advertised upload bandwidth in Mbps (e.g. `500`). |
| `ISP_PLAN_THRESHOLD` | **Yes** | *None* | Contractual minimum download Mbps. Speeds below this violate SLA. |
| `ISP_PLAN_UPLOAD_THRESHOLD` | **Yes** | *None* | Contractual minimum upload Mbps. Speeds below this violate SLA. |
| `SLA_WINDOW_HOURS` | No | `24` | Rolling evaluation window in hours. |
| `SLA_VIOLATION_PCT` | No | `20.0` | Percentage threshold. Breach declared if strictly greater than this share of valid checks is below minimum. |
| `SLA_MIN_COVERAGE` | No | `0.75` | Minimum ratio of expected checks required in window before evaluating SLA (range `(0, 1]`). |
| `SPEED_ALERT_DOWNLOAD` | No | Plan speed | Download threshold for counting "slow" incidents in summary reports. |
| `SPEED_ALERT_UPLOAD` | No | Plan speed | Upload threshold for counting "slow" incidents in summary reports. |
| `CHECK_INTERVAL_MINUTES` | No | `30` | Interval in minutes between speed test measurements. |
| `SUMMARY_INTERVAL_HOURS` | No | `6` | Interval in hours between periodic summary alerts. |
| `DAILY_REPORT_TIME` | No | `23:59` | Time of day (`HH:MM` in `TZ`) to dispatch the daily dispute report with CSV & PDF. |
| `TZ` | No | `UTC` | IANA timezone name (e.g. `America/New_York`, `Asia/Manila`, `UTC`). |
| `DB_PATH` | No | `data/ispmon.db` | Filesystem path for SQLite database (parent directories created automatically). |
| `PROBE_TARGETS` | No | `1.1.1.1:443,9.9.9.9:443` | Comma-separated list of `host:port` targets for TCP connectivity checks. |
| `PROBE_INTERVAL_SECONDS` | No | `60` | Frequency in seconds for connectivity probe rounds. |
| `PROBE_FAIL_ROUNDS` | No | `3` | Consecutive failed probe rounds required before declaring an outage. |
| `PROBE_TIMEOUT_SECONDS` | No | `3` | Dial timeout per probe target in seconds. |
| `DISCORD_WEBHOOK_URL` | No | *Empty* | Discord incoming webhook URL (e.g. `https://discord.com/api/webhooks/...`). Empty disables Discord. |
| `SMTP_SERVER` | No | *Empty* | SMTP server hostname (e.g. `smtp.gmail.com`). |
| `SMTP_PORT` | No | `587` | SMTP port (uses STARTTLS). |
| `SMTP_USER` | No | *Empty* | SMTP username / email address. |
| `SMTP_PASSWORD` | No | *Empty* | SMTP password or app password. Never logged. |
| `EMAIL_RECIPIENT` | No | *Empty* | Destination recipient address for email reports. |

*Note: Email notifications are activated only when `SMTP_SERVER`, `SMTP_USER`, `SMTP_PASSWORD`, and `EMAIL_RECIPIENT` are all configured.*

---

## How Detection Works: Two-Engine Architecture

`ispmon` strictly separates speed measurements from connectivity drops:

```
+-----------------------------------------------------------------------+
|                             ispmon Daemon                             |
|                                                                       |
|   +-----------------------+               +-----------------------+   |
|   |   TCP Outage Probe    |               |    Ookla Speed Test   |   |
|   |   (Every 60s)         |               |    (Every 30m)        |   |
|   +-----------+-----------+               +-----------+-----------+   |
|               |                                       |               |
|       All targets fail?                      Speedtest success?       |
|        /             \                        /               \       |
|      Yes              No                    Yes                No     |
|       |               |                      |                  |     |
|   3 Rounds?        Restore?             Status 'ok'    'measurement_  |
|       |               |                      |          error' (never |
|  Open Outage   Close Outage          Evaluate SLA       opens outage) |
|   (started_at = 1st drop)             over 24h window                 |
+-----------------------------------------------------------------------+
```

### 1. The Outage Engine (`internal/probe` & `internal/outage`)
- Dials `PROBE_TARGETS` (TCP) every 60 seconds with a 3-second timeout.
- A round is failed **only if ALL targets fail**. If one target responds, connectivity is considered up.
- An outage episode opens after **3 consecutive failed rounds**. Crucially, the episode's `started_at` timestamp is pinned to the time of the **first failed round**, capturing the true outage duration.
- Closes immediately on the first successful round and computes total downtime seconds.
- Exactly one alert on outage start and one on restoration.

### 2. The Speed SLA Engine (`internal/sla`)
- Runs after each successful check (`status='ok'`).
- Flags a check as below contractual minimum if `download < ISP_PLAN_THRESHOLD` or `upload < ISP_PLAN_UPLOAD_THRESHOLD`.
- Assesses valid checks across the rolling window (`SLA_WINDOW_HOURS`). Failed tests (`measurement_error`) and outages are excluded from the ratio so they do not dilute or corrupt speed statistics.
- Coverage Guard: SLA evaluation is deferred (`insufficient_data`) until at least 75% (`SLA_MIN_COVERAGE`) of expected checks exist in the rolling window.
- If violation rate exceeds `SLA_VIOLATION_PCT` (strictly `> 20.0%`), an SLA breach episode opens, sending **one alert with CSV evidence**.
- Subsequent slow checks update the incident statistics silently without spamming notifications.
- When speeds stabilize back within contractual standards, the breach episode closes and dispatches a recovery notification.

---

## Notifications & Evidence

### Discord Alerts
Compact embed cards delivered directly to your Discord channel:
- **Red Card:** Fired on SLA breach declaration (displaying worst speeds, violation %, evaluation window, and direct Ookla `speedtest.net` verification URL) and on outage detection.
- **Green Card:** Fired when SLA standards recover or connectivity is restored (detailing total incident duration).

### Email & Attachments
Delivered via SMTP STARTTLS on port 587:
- **SLA Breach Incident:** HTML summary table with in-memory CSV evidence attachment ready to submit to your ISP support ticket.
- **Daily 24-Hour Dispute Report:** Scheduled at `DAILY_REPORT_TIME` in your local `TZ`. Includes an HTML summary, a complete raw CSV log of all checks (with server names and official verification URLs), and a clean, dispute-ready PDF summary report.

---

## Database & Backups

`ispmon` stores all data in a single embedded SQLite database file (`data/ispmon.db`), using Write-Ahead Logging (`PRAGMA journal_mode=WAL;`) and standard database migrations (`PRAGMA user_version`).

### Safe Backups
Because SQLite in WAL mode writes active transactions to `ispmon.db-wal`:
- **Simple copy (when daemon is stopped):**
  ```bash
  cp data/ispmon.db /path/to/backup/ispmon.db
  ```
- **Live atomic backup (while daemon is running):**
  Use SQLite's online backup API or CLI:
  ```bash
  sqlite3 data/ispmon.db ".backup /path/to/backup/ispmon-backup.db"
  ```

---

## Troubleshooting

- **`speedtest CLI not found in PATH`:**
  `ispmon` requires the official Ookla binary. Verify installation with `speedtest --version` and ensure it is located in your system `PATH`.
- **Healthcheck reports unhealthy:**
  `ispmon healthcheck` verifies that `data/.heartbeat` was updated within `2 * CHECK_INTERVAL_MINUTES`. If the process terminates or hangs, the heartbeat file goes stale and Docker marks the container unhealthy.
- **SMTP Authentication Errors:**
  Ensure you are using port `587` with STARTTLS. For Gmail or Google Workspace accounts, generate and use a dedicated **App Password**.

---

## License & Ookla Notice

`ispmon` is open source software released under the [MIT License](LICENSE), Copyright (c) 2026 Dexter Brylle.

**Notice regarding Ookla Speedtest:**
The Ookla `speedtest` CLI binary is not bundled, modified, or redistributed with this repository. Users install the binary directly from Ookla's distribution channels subject to Ookla's [Terms of Use](https://www.speedtest.net/about/terms) and Privacy Policy. `ispmon` executes the CLI with `--accept-license --accept-gdpr` on behalf of the user.
