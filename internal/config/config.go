package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all validated configuration values for ispmon.
type Config struct {
	ISPPlanName            string
	ISPPlanSpeed           float64
	ISPPlanUploadSpeed     float64
	ISPPlanThreshold       float64
	ISPPlanUploadThreshold float64

	SLAWindowHours     int
	SLAViolationPct    float64
	SLAMinCoverage     float64
	SpeedAlertDownload float64
	SpeedAlertUpload   float64

	CheckIntervalMinutes int
	SummaryIntervalHours int
	DailyReportTime      string
	DailyReportHour      int
	DailyReportMinute    int
	TZ                   string
	Location             *time.Location

	DBPath               string
	RetentionDays        int
	ProbeTargets         []string
	ProbeIntervalSeconds int
	ProbeFailRounds      int
	ProbeTimeoutSeconds  int

	DiscordWebhookURL string
	SMTPServer        string
	SMTPPort          int
	SMTPUser          string
	SMTPPassword      string
	EmailRecipient    string
}

// DiscordEnabled reports whether Discord webhook notifications are configured.
func (c *Config) DiscordEnabled() bool {
	return strings.TrimSpace(c.DiscordWebhookURL) != ""
}

// EmailEnabled reports whether SMTP email notifications are fully configured.
func (c *Config) EmailEnabled() bool {
	return strings.TrimSpace(c.SMTPServer) != "" &&
		strings.TrimSpace(c.SMTPUser) != "" &&
		strings.TrimSpace(c.SMTPPassword) != "" &&
		strings.TrimSpace(c.EmailRecipient) != ""
}

// LogNotifiers logs the active notifiers without exposing any secret tokens or credentials.
func (c *Config) LogNotifiers(logger *slog.Logger) {
	if !c.EmailEnabled() && (strings.TrimSpace(c.SMTPServer) != "" || strings.TrimSpace(c.SMTPUser) != "" || strings.TrimSpace(c.SMTPPassword) != "" || strings.TrimSpace(c.EmailRecipient) != "") {
		logger.Warn("partial SMTP configuration; email notifications disabled")
	}
	logger.Info("notifiers configured",
		"discord_enabled", c.DiscordEnabled(),
		"smtp_enabled", c.EmailEnabled(),
	)
}

// Load loads configuration from an optional .env file and environment variables.
func Load() (*Config, error) {
	_ = godotenv.Load()
	return LoadFromLookup(os.LookupEnv)
}

// LoadFromLookup loads and validates configuration using a custom lookup function.
func LoadFromLookup(lookup func(string) (string, bool)) (*Config, error) {
	var errs []error

	getVal := func(key string) (string, bool) {
		val, ok := lookup(key)
		if !ok {
			return "", false
		}
		return strings.TrimSpace(val), true
	}

	getRequiredString := func(key string) string {
		val, ok := getVal(key)
		if !ok || val == "" {
			errs = append(errs, fmt.Errorf("required environment variable %s is missing", key))
			return ""
		}
		return val
	}

	getRequiredFloat := func(key string) float64 {
		val, ok := getVal(key)
		if !ok || val == "" {
			errs = append(errs, fmt.Errorf("required environment variable %s is missing", key))
			return 0
		}
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("required environment variable %s must be numeric, got %q", key, val))
			return 0
		}
		if f <= 0 {
			errs = append(errs, fmt.Errorf("required environment variable %s must be greater than 0, got %v", key, f))
			return 0
		}
		return f
	}

	getOptionalInt := func(key string, def int) int {
		val, ok := getVal(key)
		if !ok || val == "" {
			return def
		}
		i, err := strconv.Atoi(val)
		if err != nil {
			errs = append(errs, fmt.Errorf("environment variable %s must be an integer, got %q", key, val))
			return def
		}
		return i
	}

	getOptionalFloat := func(key string, def float64) float64 {
		val, ok := getVal(key)
		if !ok || val == "" {
			return def
		}
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("environment variable %s must be numeric, got %q", key, val))
			return def
		}
		return f
	}

	cfg := &Config{}
	cfg.ISPPlanName = getRequiredString("ISP_PLAN_NAME")
	cfg.ISPPlanSpeed = getRequiredFloat("ISP_PLAN_SPEED")
	cfg.ISPPlanUploadSpeed = getRequiredFloat("ISP_PLAN_UPLOAD_SPEED")
	cfg.ISPPlanThreshold = getRequiredFloat("ISP_PLAN_THRESHOLD")
	cfg.ISPPlanUploadThreshold = getRequiredFloat("ISP_PLAN_UPLOAD_THRESHOLD")

	if cfg.ISPPlanSpeed > 0 && cfg.ISPPlanThreshold > 0 && cfg.ISPPlanThreshold > cfg.ISPPlanSpeed {
		errs = append(errs, fmt.Errorf("ISP_PLAN_THRESHOLD (%.2f) cannot exceed ISP_PLAN_SPEED (%.2f)", cfg.ISPPlanThreshold, cfg.ISPPlanSpeed))
	}
	if cfg.ISPPlanUploadSpeed > 0 && cfg.ISPPlanUploadThreshold > 0 && cfg.ISPPlanUploadThreshold > cfg.ISPPlanUploadSpeed {
		errs = append(errs, fmt.Errorf("ISP_PLAN_UPLOAD_THRESHOLD (%.2f) cannot exceed ISP_PLAN_UPLOAD_SPEED (%.2f)", cfg.ISPPlanUploadThreshold, cfg.ISPPlanUploadSpeed))
	}

	cfg.SLAWindowHours = getOptionalInt("SLA_WINDOW_HOURS", 24)
	if cfg.SLAWindowHours <= 0 {
		errs = append(errs, fmt.Errorf("SLA_WINDOW_HOURS must be greater than 0, got %d", cfg.SLAWindowHours))
	}

	cfg.SLAViolationPct = getOptionalFloat("SLA_VIOLATION_PCT", 20.0)
	if cfg.SLAViolationPct < 0 || cfg.SLAViolationPct > 100 {
		errs = append(errs, fmt.Errorf("SLA_VIOLATION_PCT must be between 0.0 and 100.0, got %v", cfg.SLAViolationPct))
	}

	cfg.SLAMinCoverage = getOptionalFloat("SLA_MIN_COVERAGE", 0.75)
	if cfg.SLAMinCoverage <= 0 || cfg.SLAMinCoverage > 1.0 {
		errs = append(errs, fmt.Errorf("SLA_MIN_COVERAGE must be in range (0, 1], got %v", cfg.SLAMinCoverage))
	}

	// Default SPEED_ALERT_* to plan speeds if unset
	if val, ok := getVal("SPEED_ALERT_DOWNLOAD"); ok && val != "" {
		cfg.SpeedAlertDownload = getOptionalFloat("SPEED_ALERT_DOWNLOAD", cfg.ISPPlanSpeed)
		if cfg.SpeedAlertDownload <= 0 {
			errs = append(errs, fmt.Errorf("SPEED_ALERT_DOWNLOAD must be greater than 0, got %v", cfg.SpeedAlertDownload))
		}
	} else {
		cfg.SpeedAlertDownload = cfg.ISPPlanSpeed
	}

	if val, ok := getVal("SPEED_ALERT_UPLOAD"); ok && val != "" {
		cfg.SpeedAlertUpload = getOptionalFloat("SPEED_ALERT_UPLOAD", cfg.ISPPlanUploadSpeed)
		if cfg.SpeedAlertUpload <= 0 {
			errs = append(errs, fmt.Errorf("SPEED_ALERT_UPLOAD must be greater than 0, got %v", cfg.SpeedAlertUpload))
		}
	} else {
		cfg.SpeedAlertUpload = cfg.ISPPlanUploadSpeed
	}

	cfg.CheckIntervalMinutes = getOptionalInt("CHECK_INTERVAL_MINUTES", 30)
	if cfg.CheckIntervalMinutes <= 0 {
		errs = append(errs, fmt.Errorf("CHECK_INTERVAL_MINUTES must be greater than 0, got %d", cfg.CheckIntervalMinutes))
	}

	cfg.SummaryIntervalHours = getOptionalInt("SUMMARY_INTERVAL_HOURS", 6)
	if cfg.SummaryIntervalHours <= 0 {
		errs = append(errs, fmt.Errorf("SUMMARY_INTERVAL_HOURS must be greater than 0, got %d", cfg.SummaryIntervalHours))
	}

	dailyReportTime, ok := getVal("DAILY_REPORT_TIME")
	if !ok || dailyReportTime == "" {
		dailyReportTime = "23:59"
	}
	cfg.DailyReportTime = dailyReportTime
	parts := strings.Split(dailyReportTime, ":")
	if len(parts) != 2 {
		errs = append(errs, fmt.Errorf("DAILY_REPORT_TIME is malformed (expected HH:MM), got %q", dailyReportTime))
	} else {
		h, errH := strconv.Atoi(parts[0])
		m, errM := strconv.Atoi(parts[1])
		if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			errs = append(errs, fmt.Errorf("DAILY_REPORT_TIME is malformed (expected HH:MM with 00<=HH<=23 and 00<=MM<=59), got %q", dailyReportTime))
		} else {
			cfg.DailyReportHour = h
			cfg.DailyReportMinute = m
		}
	}

	tz, ok := getVal("TZ")
	if !ok || tz == "" {
		tz = "UTC"
	}
	cfg.TZ = tz
	loc, err := time.LoadLocation(tz)
	if err != nil {
		errs = append(errs, fmt.Errorf("invalid TZ timezone %q: %w", tz, err))
	} else {
		cfg.Location = loc
	}

	dbPath, ok := getVal("DB_PATH")
	if !ok || dbPath == "" {
		dbPath = "data/ispmon.db"
	}
	cfg.DBPath = dbPath

	cfg.RetentionDays = getOptionalInt("RETENTION_DAYS", 90)
	if cfg.RetentionDays < 0 {
		errs = append(errs, fmt.Errorf("RETENTION_DAYS must be non-negative (0 to keep forever), got %d", cfg.RetentionDays))
	}

	probeTargetsRaw, ok := getVal("PROBE_TARGETS")
	if !ok || probeTargetsRaw == "" {
		cfg.ProbeTargets = []string{"1.1.1.1:443", "9.9.9.9:443"}
	} else {
		rawList := strings.Split(probeTargetsRaw, ",")
		var targets []string
		for _, t := range rawList {
			trimmed := strings.TrimSpace(t)
			if trimmed != "" {
				_, _, err := net.SplitHostPort(trimmed)
				if err != nil {
					errs = append(errs, fmt.Errorf("invalid PROBE_TARGETS entry %q: %w", trimmed, err))
				} else {
					targets = append(targets, trimmed)
				}
			}
		}
		if len(targets) == 0 {
			errs = append(errs, errors.New("PROBE_TARGETS must contain at least one valid host:port target"))
		}
		cfg.ProbeTargets = targets
	}

	cfg.ProbeIntervalSeconds = getOptionalInt("PROBE_INTERVAL_SECONDS", 60)
	if cfg.ProbeIntervalSeconds <= 0 {
		errs = append(errs, fmt.Errorf("PROBE_INTERVAL_SECONDS must be greater than 0, got %d", cfg.ProbeIntervalSeconds))
	}

	cfg.ProbeFailRounds = getOptionalInt("PROBE_FAIL_ROUNDS", 3)
	if cfg.ProbeFailRounds <= 0 {
		errs = append(errs, fmt.Errorf("PROBE_FAIL_ROUNDS must be greater than 0, got %d", cfg.ProbeFailRounds))
	}

	cfg.ProbeTimeoutSeconds = getOptionalInt("PROBE_TIMEOUT_SECONDS", 3)
	if cfg.ProbeTimeoutSeconds <= 0 {
		errs = append(errs, fmt.Errorf("PROBE_TIMEOUT_SECONDS must be greater than 0, got %d", cfg.ProbeTimeoutSeconds))
	}

	if val, ok := getVal("DISCORD_WEBHOOK_URL"); ok {
		cfg.DiscordWebhookURL = val
		if val != "" {
			parsed, err := url.Parse(val)
			if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
				errs = append(errs, errors.New("DISCORD_WEBHOOK_URL must be an https URL with a host"))
			}
		}

	}

	if val, ok := getVal("SMTP_SERVER"); ok {
		cfg.SMTPServer = val
	}
	cfg.SMTPPort = getOptionalInt("SMTP_PORT", 587)
	if cfg.SMTPPort <= 0 || cfg.SMTPPort > 65535 {
		errs = append(errs, fmt.Errorf("SMTP_PORT must be between 1 and 65535, got %d", cfg.SMTPPort))
	}
	if val, ok := getVal("SMTP_USER"); ok {
		cfg.SMTPUser = val
	}
	if val, ok := getVal("SMTP_PASSWORD"); ok {
		cfg.SMTPPassword = val
	}
	if val, ok := getVal("EMAIL_RECIPIENT"); ok {
		cfg.EmailRecipient = val
	}

	if len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		return nil, fmt.Errorf("configuration errors:\n - %s", strings.Join(msgs, "\n - "))
	}

	return cfg, nil
}
