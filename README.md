# ispmon: ISP Speed and SLA Monitor

`ispmon` monitors your internet connection and collects evidence for ISP disputes.
It runs the official Ookla `speedtest` CLI, stores results in SQLite, checks speed
against your contract, and detects outages with separate TCP probes. It sends
Discord and email notifications and creates CSV and PDF reports.

## Quick start

### Docker Compose

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

### Binary setup

#### 1. Install the official Ookla Speedtest CLI

Install the official Ookla CLI tool.

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

#### 2. Build the program

Building requires Go 1.26 or newer.

Build the binary:

```bash
make build
```

Or install the binary into your Go binary directory:

```bash
go install ./cmd/ispmon
```

#### 3. Run the program

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

## Service deployment

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

## Configuration reference

`ispmon` reads environment variables and loads an optional `.env` file.

| Variable | Required | Default | Description |
|---|---|---|---|
| `ISP_PLAN_NAME` | Yes | None | Plan name shown in alerts and reports. |
| `ISP_PLAN_SPEED` | Yes | None | Advertised download bandwidth in Mbps. |
| `ISP_PLAN_UPLOAD_SPEED` | Yes | None | Advertised upload bandwidth in Mbps. |
| `ISP_PLAN_THRESHOLD` | Yes | None | Minimum download speed in Mbps. |
| `ISP_PLAN_UPLOAD_THRESHOLD` | Yes | None | Minimum upload speed in Mbps. |
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
| `DISCORD_WEBHOOK_URL` | No | Empty | Discord webhook URL. Leave empty to disable. |
| `SMTP_SERVER` | No | Empty | SMTP server hostname. |
| `SMTP_PORT` | No | `587` | SMTP port (uses STARTTLS). |
| `SMTP_USER` | No | Empty | SMTP username. |
| `SMTP_PASSWORD` | No | Empty | SMTP password. The program does not log passwords. |
| `EMAIL_RECIPIENT` | No | Empty | Recipient email address for reports. |

Email notifications require a server, username, password, and recipient.
`SMTP_PORT` defaults to `587`.

## Outages and speed checks

TCP probes and speed tests run independently. A failed speed test records
`measurement_error` and does not open an outage.

### Outages

By default, the probe dials every target in `PROBE_TARGETS` every 60 seconds.
A round fails only when all targets fail. After three consecutive failed rounds,
an outage opens with the first failed round as its start time. The first
successful round closes it. Each transition sends one alert, and the database
records the downtime.

### Speed SLA

A successful check violates the contract when download or upload speed falls
below `ISP_PLAN_THRESHOLD` or `ISP_PLAN_UPLOAD_THRESHOLD`. Failed checks do not
count toward the violation rate or coverage.

The default 24-hour window expects 48 checks at 30-minute intervals.
Evaluation requires 75% coverage, or 36 valid checks, which takes about 18 hours.
Until then, the status is `insufficient_data`, and breaches cannot open or resolve.
`SLA_WINDOW_HOURS`, `CHECK_INTERVAL_MINUTES`, and `SLA_MIN_COVERAGE` control these
settings.

A breach opens when more than 20% of valid checks violate the contract, as set by
`SLA_VIOLATION_PCT`. It sends one alert with CSV evidence. Later checks update the
breach metrics without repeating the alert.

A breach resolves when coverage is sufficient and the violation rate falls to
20% or less. It sends one resolution notice. An outage or missing data leaves an
open breach unresolved until valid checks confirm recovery.

## Notifications and evidence

### Discord alerts

Discord cards are red when a breach or outage starts and green when it ends.

### Email reports

Email uses SMTP with STARTTLS, on port 587 by default. Breach alerts include an
HTML summary and CSV evidence. Daily reports run at `DAILY_REPORT_TIME` and include
an HTML summary, CSV, and PDF.

## Database and backups

`ispmon` stores all data in a local SQLite file (`data/ispmon.db`).
The database uses Write-Ahead Logging (WAL) mode.

### Back up the database

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

## License and Ookla notice

`ispmon` is open-source software under the [MIT License](LICENSE).
Copyright (c) 2026 Dexter Brylle.

The Ookla binary is not included in this repository.
Users install the binary directly from official Ookla repositories.
Users must accept the Ookla Terms of Use and Privacy Policy.
`ispmon` runs the CLI with `--accept-license` and `--accept-gdpr` for the user.
