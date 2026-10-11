package config_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/dexterbrylle/isp-speed-monitor/internal/config"
)

func validEnvMap() map[string]string {
	return map[string]string{
		"ISP_PLAN_NAME":             "GFiber 500",
		"ISP_PLAN_SPEED":            "500",
		"ISP_PLAN_UPLOAD_SPEED":     "500",
		"ISP_PLAN_THRESHOLD":        "150",
		"ISP_PLAN_UPLOAD_THRESHOLD": "150",
	}
}

func mockLookup(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}

func TestConfig_ValidDefaults(t *testing.T) {
	env := validEnvMap()
	cfg, err := config.LoadFromLookup(mockLookup(env))
	if err != nil {
		t.Fatalf("unexpected error loading valid config: %v", err)
	}

	if cfg.ISPPlanName != "GFiber 500" {
		t.Errorf("expected plan name 'GFiber 500', got %q", cfg.ISPPlanName)
	}
	if cfg.ISPPlanSpeed != 500 || cfg.ISPPlanUploadSpeed != 500 {
		t.Errorf("expected speeds 500/500, got %v/%v", cfg.ISPPlanSpeed, cfg.ISPPlanUploadSpeed)
	}
	if cfg.ISPPlanThreshold != 150 || cfg.ISPPlanUploadThreshold != 150 {
		t.Errorf("expected thresholds 150/150, got %v/%v", cfg.ISPPlanThreshold, cfg.ISPPlanUploadThreshold)
	}
	if cfg.SLAWindowHours != 24 {
		t.Errorf("expected default SLAWindowHours 24, got %d", cfg.SLAWindowHours)
	}
	if cfg.SLAViolationPct != 20.0 {
		t.Errorf("expected default SLAViolationPct 20.0, got %v", cfg.SLAViolationPct)
	}
	if cfg.SLAMinCoverage != 0.75 {
		t.Errorf("expected default SLAMinCoverage 0.75, got %v", cfg.SLAMinCoverage)
	}
	if cfg.SpeedAlertDownload != 500 || cfg.SpeedAlertUpload != 500 {
		t.Errorf("expected speed alerts default to plan speeds (500/500), got %v/%v", cfg.SpeedAlertDownload, cfg.SpeedAlertUpload)
	}
	if cfg.DailyReportTime != "23:59" || cfg.DailyReportHour != 23 || cfg.DailyReportMinute != 59 {
		t.Errorf("expected default DailyReportTime 23:59, got %q (%d:%d)", cfg.DailyReportTime, cfg.DailyReportHour, cfg.DailyReportMinute)
	}
	if cfg.DBPath != "data/ispmon.db" {
		t.Errorf("expected default DBPath 'data/ispmon.db', got %q", cfg.DBPath)
	}
	if cfg.RetentionDays != 90 {
		t.Errorf("expected default RetentionDays 90, got %d", cfg.RetentionDays)
	}
	if len(cfg.ProbeTargets) != 2 || cfg.ProbeTargets[0] != "1.1.1.1:443" {
		t.Errorf("expected default probe targets, got %v", cfg.ProbeTargets)
	}
	if cfg.DiscordEnabled() {
		t.Errorf("expected discord disabled by default")
	}
	if cfg.EmailEnabled() {
		t.Errorf("expected email disabled by default")
	}
}

func TestConfig_ValidationFailures(t *testing.T) {
	tests := []struct {
		name        string
		mutator     func(map[string]string)
		errContains string
	}{
		{
			name: "missing ISP_PLAN_NAME",
			mutator: func(m map[string]string) {
				delete(m, "ISP_PLAN_NAME")
			},
			errContains: "ISP_PLAN_NAME is missing",
		},
		{
			name: "missing ISP_PLAN_SPEED",
			mutator: func(m map[string]string) {
				delete(m, "ISP_PLAN_SPEED")
			},
			errContains: "ISP_PLAN_SPEED is missing",
		},
		{
			name: "non-numeric ISP_PLAN_SPEED",
			mutator: func(m map[string]string) {
				m["ISP_PLAN_SPEED"] = "fast"
			},
			errContains: "ISP_PLAN_SPEED must be numeric",
		},
		{
			name: "download threshold exceeds speed",
			mutator: func(m map[string]string) {
				m["ISP_PLAN_SPEED"] = "100"
				m["ISP_PLAN_THRESHOLD"] = "150"
			},
			errContains: "ISP_PLAN_THRESHOLD (150.00) cannot exceed ISP_PLAN_SPEED (100.00)",
		},
		{
			name: "upload threshold exceeds upload speed",
			mutator: func(m map[string]string) {
				m["ISP_PLAN_UPLOAD_SPEED"] = "100"
				m["ISP_PLAN_UPLOAD_THRESHOLD"] = "200"
			},
			errContains: "ISP_PLAN_UPLOAD_THRESHOLD (200.00) cannot exceed ISP_PLAN_UPLOAD_SPEED (100.00)",
		},
		{
			name: "SLA_MIN_COVERAGE zero",
			mutator: func(m map[string]string) {
				m["SLA_MIN_COVERAGE"] = "0"
			},
			errContains: "SLA_MIN_COVERAGE must be in range (0, 1]",
		},
		{
			name: "SLA_MIN_COVERAGE greater than 1",
			mutator: func(m map[string]string) {
				m["SLA_MIN_COVERAGE"] = "1.2"
			},
			errContains: "SLA_MIN_COVERAGE must be in range (0, 1]",
		},
		{
			name: "malformed DAILY_REPORT_TIME string",
			mutator: func(m map[string]string) {
				m["DAILY_REPORT_TIME"] = "invalid"
			},
			errContains: "DAILY_REPORT_TIME is malformed",
		},
		{
			name: "malformed DAILY_REPORT_TIME hour out of range",
			mutator: func(m map[string]string) {
				m["DAILY_REPORT_TIME"] = "25:00"
			},
			errContains: "DAILY_REPORT_TIME is malformed",
		},
		{
			name: "invalid TZ",
			mutator: func(m map[string]string) {
				m["TZ"] = "NonExistent/Timezone"
			},
			errContains: "invalid TZ timezone",
		},
		{
			name: "invalid PROBE_TARGETS entry",
			mutator: func(m map[string]string) {
				m["PROBE_TARGETS"] = "invalid-target-no-port"
			},
			errContains: "invalid PROBE_TARGETS entry",
		},
		{
			name: "negative RETENTION_DAYS",
			mutator: func(m map[string]string) {
				m["RETENTION_DAYS"] = "-5"
			},
			errContains: "RETENTION_DAYS must be non-negative",
		},
		{
			name: "non-integer RETENTION_DAYS",
			mutator: func(m map[string]string) {
				m["RETENTION_DAYS"] = "forever"
			},
			errContains: "RETENTION_DAYS must be an integer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnvMap()
			tt.mutator(env)
			_, err := config.LoadFromLookup(mockLookup(env))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.errContains)
			}
			if !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("expected error to contain %q, got %q", tt.errContains, err.Error())
			}
		})
	}
}

func TestConfig_NotifiersAndLogging(t *testing.T) {
	env := validEnvMap()
	env["DISCORD_WEBHOOK_URL"] = "https://discord.com/api/webhooks/123/secret"
	env["SMTP_SERVER"] = "smtp.example.com"
	env["SMTP_PORT"] = "587"
	env["SMTP_USER"] = "user@example.com"
	env["SMTP_PASSWORD"] = "topsecret"
	env["EMAIL_RECIPIENT"] = "admin@example.com"

	cfg, err := config.LoadFromLookup(mockLookup(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cfg.DiscordEnabled() {
		t.Errorf("expected discord to be enabled")
	}
	if !cfg.EmailEnabled() {
		t.Errorf("expected email to be enabled")
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	cfg.LogNotifiers(logger)

	logOutput := buf.String()
	if strings.Contains(logOutput, "secret") || strings.Contains(logOutput, "topsecret") {
		t.Fatalf("logger exposed secrets: %s", logOutput)
	}
	if !strings.Contains(logOutput, "discord_enabled=true") || !strings.Contains(logOutput, "smtp_enabled=true") {
		t.Errorf("expected notifier status in log, got: %s", logOutput)
	}
}

func TestDiscordWebhookRequiresHTTPS(t *testing.T) {
	for _, url := range []string{"http://discord.com/hook", "not-a-url", "https:///hook", "https://"} {
		t.Run(url, func(t *testing.T) {
			env := validEnvMap()
			env["DISCORD_WEBHOOK_URL"] = url
			if _, err := config.LoadFromLookup(mockLookup(env)); err == nil {
				t.Fatalf("invalid webhook accepted: %s", url)
			}
		})
	}
	env := validEnvMap()
	env["DISCORD_WEBHOOK_URL"] = "https://discord.com/api/webhooks/test"
	if _, err := config.LoadFromLookup(mockLookup(env)); err != nil {
		t.Fatal(err)
	}
}
func TestPartialSMTPWarnsWithoutSecrets(t *testing.T) {
	env := validEnvMap()
	env["SMTP_PASSWORD"] = "do-not-log-this"
	cfg, err := config.LoadFromLookup(mockLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	cfg.LogNotifiers(slog.New(slog.NewTextHandler(&buf, nil)))
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "partial SMTP") {
		t.Fatalf("missing partial SMTP warning: %s", buf.String())
	}
	if strings.Contains(buf.String(), "do-not-log-this") {
		t.Fatal("password logged")
	}
}

func TestConfig_RetentionDaysExplicit(t *testing.T) {
	env := validEnvMap()
	env["RETENTION_DAYS"] = "0"
	cfg, err := config.LoadFromLookup(mockLookup(env))
	if err != nil {
		t.Fatalf("unexpected error with RETENTION_DAYS=0: %v", err)
	}
	if cfg.RetentionDays != 0 {
		t.Errorf("expected RetentionDays 0, got %d", cfg.RetentionDays)
	}

	env["RETENTION_DAYS"] = "30"
	cfg, err = config.LoadFromLookup(mockLookup(env))
	if err != nil {
		t.Fatalf("unexpected error with RETENTION_DAYS=30: %v", err)
	}
	if cfg.RetentionDays != 30 {
		t.Errorf("expected RetentionDays 30, got %d", cfg.RetentionDays)
	}
}
