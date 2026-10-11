# Project Roadmap & Scope

This document tracks the completed architecture and future roadmap for `ispmon`.

## Done (v2.0 Go Rewrite)

- **Go 1.22+ Standard Library Foundation:** Pure Go implementation with zero CGO dependencies.
- **Embedded SQLite Storage:** Powered by `modernc.org/sqlite` with WAL mode and `PRAGMA user_version` migrations.
- **Two-Engine Separation:**
  - Dedicated lightweight TCP connectivity probe (`internal/probe`, `internal/outage`) for outage tracking without diluting speed ratios.
  - Contractual SLA compliance engine (`internal/sla`) running over rolling evaluation windows.
- **Official Ookla CLI Integration:** Parsing bandwidth, latency, jitter, packet loss, and official proof links (`speedtest.net/result/c/...`).
- **Dispute-Ready Reporting:** In-memory CSV and PDF report generators (`internal/report`) powered by `github.com/go-pdf/fpdf`.
- **Fault-Tolerant Multi-Channel Alerts:** Concurrent fan-out dispatcher (`internal/notify`) supporting Discord webhooks and SMTP with STARTTLS.
- **Multi-Stage Container & Deployment Units:** Debian-based container with build-time Ookla CLI install, plus launchd plist and systemd service units.

---

## Next (Planned Enhancements)

1. **GitHub Actions & GoReleaser:**
   - Automated multi-platform binary releases (macOS darwin/arm64 & darwin/amd64, Linux amd64 & arm64).
   - Automated publication of multi-arch Docker images to GitHub Container Registry (GHCR).

2. **Generic Webhook Notifier:**
   - Optional configurable JSON webhook sender supporting custom headers and payload templates for Slack, Telegram, or Home Assistant integrations.

3. **Database Retention Pruning:**
   - Background retention worker to automatically prune raw check records older than 12 months while preserving episode summaries.

4. **Grafana Monitoring Profile:**
   - Pre-provisioned Grafana configuration mounting the SQLite database read-only (`:ro`) using the `frser-sqlite-datasource` plugin.
