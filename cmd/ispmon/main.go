package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/config"
	"github.com/dexterbrylle/isp-speed-monitor/internal/engine"
	"github.com/dexterbrylle/isp-speed-monitor/internal/health"
	"github.com/dexterbrylle/isp-speed-monitor/internal/notify"
	"github.com/dexterbrylle/isp-speed-monitor/internal/outage"
	"github.com/dexterbrylle/isp-speed-monitor/internal/probe"
	"github.com/dexterbrylle/isp-speed-monitor/internal/speedtest"
	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	return runWithIO(args, os.Stdout, os.Stderr)
}

func runWithIO(args []string, stdout, stderr io.Writer) int {
	cmd := "run"
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "ispmon %s (commit %s, built %s)\n", version, commit, date)
		return 0

	case "healthcheck":
		return runHealthcheck(stdout, stderr)

	case "run":
		return runDaemon(stdout, stderr)

	default:
		fmt.Fprintf(stderr, "unknown command %q\nUsage: ispmon [run|healthcheck|version]\n", cmd)
		return 1
	}
}

func runHealthcheck(stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	dbPath := "data/ispmon.db"
	checkInterval := 30
	probeInterval := 60
	if err == nil && cfg != nil {
		dbPath = cfg.DBPath
		checkInterval = cfg.CheckIntervalMinutes
		probeInterval = cfg.ProbeIntervalSeconds
	} else {
		if envDB := strings.TrimSpace(os.Getenv("DB_PATH")); envDB != "" {
			dbPath = envDB
		}
		if envCI := strings.TrimSpace(os.Getenv("CHECK_INTERVAL_MINUTES")); envCI != "" {
			if v, err := strconv.Atoi(envCI); err == nil && v > 0 {
				checkInterval = v
			}
		}
		if envPI := strings.TrimSpace(os.Getenv("PROBE_INTERVAL_SECONDS")); envPI != "" {
			if v, err := strconv.Atoi(envPI); err == nil && v > 0 {
				probeInterval = v
			}
		}
	}

	speedHbPath := health.HeartbeatPath(dbPath)
	probeHbPath := health.ProbeHeartbeatPath(dbPath)
	speedMaxAge := time.Duration(2*checkInterval) * time.Minute
	probeMaxAge := time.Duration(2*probeInterval) * time.Second

	if err := health.CheckHealth(speedHbPath, probeHbPath, speedMaxAge, probeMaxAge, time.Now().UTC()); err != nil {
		fmt.Fprintf(stderr, "healthcheck failed: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, "healthcheck OK")
	return 0
}

func runDaemon(stdout, stderr io.Writer) int {
	logger := slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	logger.Info("configuration loaded successfully", "plan_name", cfg.ISPPlanName)
	cfg.LogNotifiers(logger)

	binPath, err := speedtest.LookPath()
	if err != nil {
		logger.Error(err.Error())
		return 1
	}
	logger.Info("located speedtest CLI binary", "path", binPath)

	st, err := store.New(cfg.DBPath)
	if err != nil {
		logger.Error("failed opening database", "path", cfg.DBPath, "error", err)
		return 1
	}
	defer st.Close()
	logger.Info("database opened and migrations applied", "path", cfg.DBPath)

	var notifiers []notify.Notifier
	if cfg.DiscordEnabled() {
		notifiers = append(notifiers, notify.NewDiscordNotifier(cfg.DiscordWebhookURL, nil))
	}
	if cfg.EmailEnabled() {
		notifiers = append(notifiers, notify.NewSMTPNotifier(
			cfg.SMTPServer,
			cfg.SMTPPort,
			cfg.SMTPUser,
			cfg.SMTPPassword,
			cfg.EmailRecipient,
		))
	}
	dispatcher := notify.NewDispatcher(notifiers, logger)

	activeOutage, err := st.GetActiveOutage(context.Background())
	if err != nil {
		logger.Warn("could not query active outage on startup", "error", err)
	}
	outageSM := outage.NewStateMachine(cfg.ProbeFailRounds, activeOutage)

	runner := speedtest.NewRunner(binPath)
	prober := probe.New(cfg.ProbeTargets, cfg.ProbeTimeoutSeconds)

	eng := engine.New(cfg, st, runner, prober, outageSM, dispatcher, logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Info("starting ispmon daemon loops")
	if err := eng.Start(ctx); err != nil {
		logger.Error("engine error", "error", err)
		return 1
	}

	logger.Info("ispmon daemon exited cleanly")
	return 0
}
