# ispmon: ISP Speed and SLA Monitor

`ispmon` is a background program written in Go.
It monitors your internet connection.
It collects evidence when your Internet Service Provider (ISP) does not deliver the contracted speed.

---

## Purpose

Internet contracts specify a minimum guaranteed speed.
Contracts also specify an uptime commitment.
When your network connection becomes slow, customer service asks for proof.

`ispmon` helps you collect this proof:
1. It runs the official Ookla `speedtest` Command Line Interface (CLI) at a set interval.
2. It saves speed, latency, jitter, packet loss, and proof links in a local SQLite database.
3. It evaluates contract compliance across a rolling time window.
4. It detects network outages with separate TCP probes.
5. It creates dispute packets with CSV and PDF reports.
6. It sends notifications through Discord webhooks and email.

---

## Quick Start

### Option 1: Docker Compose (Recommended)

1. Copy the example configuration file:
   ```bash
   cp .env.example .env
   ```

2. Open the `.env` file and set your plan values:
   ```bash
   nano .env
   ```

3. Start the container:
   ```bash
   docker compose up -d
   ```

4. View the status and logs:
   ```bash
   docker compose ps
   docker compose logs -f
   ```

---

### Option 2: Binary Setup

#### 1. Install the Official Ookla Speedtest CLI

You must install the official Ookla CLI tool.

- **macOS:**
  ```bash
  brew install speedtest
  ```

- **Debian and Ubuntu:**
  ```bash
  curl -s https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.deb.sh | sudo bash
  sudo apt-get install speedtest
  ```

- **RedHat and Fedora:**
  ```bash
  curl -s https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.rpm.sh | sudo bash
  sudo dnf install speedtest
  ```

#### 2. Build the Program

You must have Go 1.22 or newer to build the program.

Build the binary:
```bash
make build
```

Or install the binary into your Go binary directory:
```bash
go install ./cmd/ispmon
```

#### 3. Run the Program

1. Copy the configuration file:
   ```bash
   cp .env.example .env
   ```

2. Set your configuration values in `.env`.

3. Start the daemon:
   ```bash
   ./bin/ispmon run
   ```

Available commands:
- `ispmon run`: Starts the monitoring daemon.
- `ispmon healthcheck`: Checks heartbeat files for liveness.
- `ispmon version`: Shows version and build details.

---

## Service Deployment

You can run `ispmon` as a system service.
Service templates are in the `deploy/` directory.

### macOS (launchd)

1. Open `deploy/launchd/com.dexterbrylle.ispmon.plist`.
2. Replace the placeholder values with your username and file paths.
3. Copy the file to `~/Library/LaunchAgents/`.
4. Load the service:
   ```bash
   launchctl load ~/Library/LaunchAgents/com.dexterbrylle.ispmon.plist
   ```

### Linux (systemd)

1. Open `deploy/systemd/ispmon.service`.
2. Replace the placeholder values with your username and file paths.
3. Copy the file to `/etc/systemd/system/ispmon.service`.
4. Enable and start the service:
   ```bash
   sudo systemctl enable --now ispmon
   ```

---

## Configuration Reference

The program reads all configuration values from environment variables.
You can set them in `.env`.

| Variable | Required | Default | Description |
|---|---|---|---|
| `ISP_PLAN_NAME` | **Yes** | *None* | Plan name shown in alerts and reports. |
| `ISP_PLAN_SPEED` | **Yes** | *None* | Advertised download bandwidth in Mbps. |
| `ISP_PLAN_UPLOAD_SPEED` | **Yes** | *None* | Advertised upload bandwidth in Mbps. |
| `ISP_PLAN_THRESHOLD` | **Yes** | *None* | Minimum download speed in Mbps. |
| `ISP_PLAN_UPLOAD_THRESHOLD` | **Yes** | *None* | Minimum upload speed in Mbps. |
| `SLA_WINDOW_HOURS` | No | `24` | Rolling evaluation window in hours. |
| `SLA_VIOLATION_PCT` | No | `20.0` | Violation percentage to declare a breach. |
| `SLA_MIN_COVERAGE` | No | `0.75` | Minimum ratio of checks needed to evaluate. |
| `SPEED_ALERT_DOWNLOAD` | No | Plan speed | Download threshold for slow check counts. |
| `SPEED_ALERT_UPLOAD` | No | Plan speed | Upload threshold for slow check counts. |
| `CHECK_INTERVAL_MINUTES` | No | `30` | Minutes between speed tests. |
| `SUMMARY_INTERVAL_HOURS` | No | `6` | Hours between summary alerts. |
| `DAILY_REPORT_TIME` | No | `23:59` | Daily report time (`HH:MM` in `TZ`). |
| `TZ` | No | `UTC` | IANA timezone name. |
| `DB_PATH` | No | `data/ispmon.db` | File path for SQLite database. |
| `RETENTION_DAYS` | No | `90` | Data retention period in days (`0` = keep forever). |
| `PROBE_TARGETS` | No | `1.1.1.1:443,9.9.9.9:443` | Comma-separated list of TCP targets. |
| `PROBE_INTERVAL_SECONDS` | No | `60` | Seconds between probe rounds. |
| `PROBE_FAIL_ROUNDS` | No | `3` | Failed rounds needed to open an outage. |
| `PROBE_TIMEOUT_SECONDS` | No | `3` | Dial timeout per target in seconds. |
| `DISCORD_WEBHOOK_URL` | No | *Empty* | Discord webhook URL. Leave empty to disable. |
| `SMTP_SERVER` | No | *Empty* | SMTP server hostname. |
| `SMTP_PORT` | No | `587` | SMTP port (uses STARTTLS). |
| `SMTP_USER` | No | *Empty* | SMTP username. |
| `SMTP_PASSWORD` | No | *Empty* | SMTP password. The program does not log passwords. |
| `EMAIL_RECIPIENT` | No | *Empty* | Recipient email address for reports. |

*Note: Email notifications activate only when you set the server, port, user, password, and recipient.*

---

## Two-Engine Architecture

`ispmon` separates speed tests from outage detection.
This separation prevents connection drops from corrupting speed calculations.

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

### 1. Outage Engine

- The probe dials all targets in `PROBE_TARGETS` with TCP every 60 seconds.
- A round fails only if all targets fail to connect.
- An outage episode starts after 3 consecutive failed rounds.
- The start time is set to the time of the first failed round.
- The outage episode closes on the first successful round.
- The engine calculates the total downtime in seconds.
- The engine sends one alert when the outage starts.
- The engine sends one alert when the connection restores.

### 2. Speed SLA Engine

- **Valid Checks Only:**
  - The engine evaluates only successful speed checks (`status='ok'`).
  - It ignores checks that fail with errors (`measurement_error`).
  - It ignores checks during network outages.
  - This rule prevents failed connections from corrupting speed metrics.

- **Threshold Checks:**
  - A check violates the contract if download or upload speed is below the minimum threshold.
  - The thresholds are `ISP_PLAN_THRESHOLD` and `ISP_PLAN_UPLOAD_THRESHOLD`.

- **Rolling Window and Warm-Up Period:**
  - The engine evaluates checks across a rolling window (`SLA_WINDOW_HOURS`).
  - With default settings (`SLA_WINDOW_HOURS=24`, `CHECK_INTERVAL_MINUTES=30`), a window expects 48 checks: `(24 * 60) / 30 = 48`.
  - The engine requires at least 75% coverage (`SLA_MIN_COVERAGE=0.75`).
  - This coverage requires 36 valid checks: `48 * 0.75 = 36`.
  - At 30-minute intervals, 36 checks take approximately 18 hours.
  - During this 18-hour warm-up period, the status is `insufficient_data`.
  - The engine does not open or resolve breaches during this period.

- **Breach Declaration:**
  - A breach occurs when more than 20% (`SLA_VIOLATION_PCT`) of valid checks violate the threshold.
  - The engine opens a breach episode.
  - It sends one alert with CSV evidence.

- **Metric Updates:**
  - Subsequent checks update the breach metrics in the database.
  - The engine does not send duplicate alert messages while the breach remains open.

- **Recovery Rules:**
  - An open breach resolves only when two conditions are true:
    1. The window contains at least 36 valid checks.
    2. The violation rate is 20% or less.
  - When these conditions are met, the engine closes the episode.
  - It sends one resolution notification.

- **Insufficient Data Invariant:**
  - An open breach cannot resolve when data is insufficient.
  - If a long network outage occurs, valid test data stops.
  - The breach remains open during this time.

- **Decision on Auto-Closing:**
  - Open breaches do not close automatically when data is insufficient.
  - A resolution notice proves that network speed returned to contract levels.
  - If breaches closed during outages, the system would send false recovery alerts.
  - Closing breaches without data would also split one long failure into multiple short episodes.
  - This would harm evidence collection for ISP disputes.
  - Therefore, a breach stays open until valid tests confirm recovery.

---

## Notifications and Evidence

### Discord Alerts

The program sends embed cards to Discord:
- **Red Card:** Sent when an SLA breach starts or when an outage starts.
- **Green Card:** Sent when an SLA breach resolves or when an outage ends.

### Email Reports

The program sends emails through SMTP with STARTTLS on port 587:
- **Breach Alert:** Contains an HTML summary and an in-memory CSV evidence file.
- **Daily Report:** Sent at `DAILY_REPORT_TIME`. Contains an HTML summary, a full CSV file, and a PDF document.

---

## Database and Backups

`ispmon` stores all data in a local SQLite file (`data/ispmon.db`).
The database uses Write-Ahead Logging (WAL) mode.

### How to Back Up the Database

When you copy a database in WAL mode, you must handle active write files.

- **Offline copy (when the daemon is stopped):**
  ```bash
  cp data/ispmon.db /path/to/backup/ispmon.db
  ```

- **Online copy (while the daemon runs):**
  Use the SQLite backup command to create a safe copy:
  ```bash
  sqlite3 data/ispmon.db ".backup /path/to/backup/ispmon-backup.db"
  ```

---

## Troubleshooting

- **Error: `speedtest CLI not found in PATH`:**
  Install the official Ookla CLI tool.
  Make sure the binary is in your system `PATH`.

- **Error: Health check failure:**
  `ispmon healthcheck` verifies the speed heartbeat (`.heartbeat`) and probe heartbeat (`.probe_heartbeat`).
  If either loop stops, the heartbeat file becomes stale and the check fails.

- **SMTP connection failure:**
  Make sure port 587 is open.
  Make sure STARTTLS is supported.
  Use an application password for Google accounts.

---

## License and Ookla Notice

`ispmon` is open-source software under the [MIT License](LICENSE).
Copyright (c) 2026 Dexter Brylle.

**Ookla Speedtest Notice:**
The Ookla binary is not included in this repository.
Users install the binary directly from official Ookla repositories.
Users must accept the Ookla Terms of Use and Privacy Policy.
`ispmon` runs the CLI with `--accept-license` and `--accept-gdpr` for the user.
