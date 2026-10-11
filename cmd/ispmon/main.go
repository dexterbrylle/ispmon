package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
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
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "version", "--version", "-v":
		fmt.Printf("ispmon %s (commit %s, built %s)\n", version, commit, date)
		return

	case "healthcheck":
		runHealthcheck()

	case "run":
		runDaemon()

	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\nUsage: ispmon [run|healthcheck|version]\n", cmd)
		os.Exit(1)
	}
}

func runHealthcheck() {
	cfg, err := config.Load()
	dbPath := "data/ispmon.db"
	checkInterval := 30
	if err == nil && cfg != nil {
		dbPath = cfg.DBPath
		checkInterval = cfg.CheckIntervalMinutes
	}

	hbPath := health.HeartbeatPath(dbPath)
	maxAge := time.Duration(2*checkInterval) * time.Minute

	if err := health.CheckHeartbeat(hbPath, maxAge, time.Now().UTC()); err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("healthcheck OK")
	os.Exit(0)
}

func runDaemon() {
	// Logging via log/slog to stdout only
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	logger.Info("configuration loaded successfully", "plan_name", cfg.ISPPlanName)
	cfg.LogNotifiers(logger)

	// Verify speedtest binary is installed
	binPath, err := speedtest.LookPath()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	logger.Info("located speedtest CLI binary", "path", binPath)

	// Open DB and run migrations
	st, err := store.New(cfg.DBPath)
	if err != nil {
		logger.Error("failed opening database", "path", cfg.DBPath, "error", err)
		os.Exit(1)
	}
	defer st.Close()
	logger.Info("database opened and migrations applied", "path", cfg.DBPath)

	// Prepare notifiers
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

	// Recover existing outage state if active in DB
	activeOutage, err := st.GetActiveOutage(context.Background())
	if err != nil {
		logger.Warn("could not query active outage on startup", "error", err)
	}
	outageSM := outage.NewStateMachine(cfg.ProbeFailRounds, activeOutage)

	runner := speedtest.NewRunner(binPath)
	prober := probe.New(cfg.ProbeTargets, cfg.ProbeTimeoutSeconds)

	eng := engine.New(cfg, st, runner, prober, outageSM, dispatcher, logger)

	// Graceful shutdown on SIGINT / SIGTERM within 10s
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Info("starting ispmon daemon loops")
	if err := eng.Start(ctx); err != nil {
		logger.Error("engine error", "error", err)
	}

	logger.Info("ispmon daemon exited cleanly")
}
