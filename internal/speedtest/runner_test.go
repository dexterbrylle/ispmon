package speedtest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/speedtest"
)

func TestOoklaRunner_RunSuccess(t *testing.T) {
	fixtureData, err := os.ReadFile(filepath.Join("testdata", "normal.json"))
	if err != nil {
		t.Fatalf("failed reading normal.json: %v", err)
	}

	mockTime := time.Date(2026, 10, 11, 4, 15, 30, 0, time.UTC)
	runner := speedtest.NewRunner("/usr/local/bin/speedtest")
	runner.Now = func() time.Time { return mockTime }
	runner.FetchIP = func(ctx context.Context) (string, error) {
		return "120.28.1.2", nil
	}
	runner.CmdRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "/usr/local/bin/speedtest" {
			t.Errorf("unexpected command name %q", name)
		}
		return fixtureData, nil
	}

	check, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error from Run: %v", err)
	}
	if check.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", check.Status)
	}
	if check.Engine != "ookla-cli" {
		t.Errorf("expected engine 'ookla-cli', got %q", check.Engine)
	}
	if check.DownloadMbps == nil || *check.DownloadMbps != 500.0 {
		t.Errorf("expected download 500.0, got %v", check.DownloadMbps)
	}
	if check.UploadMbps == nil || *check.UploadMbps != 480.0 {
		t.Errorf("expected upload 480.0, got %v", check.UploadMbps)
	}
	if check.PublicIP == nil || *check.PublicIP != "120.28.1.2" {
		t.Errorf("expected public IP '120.28.1.2', got %v", check.PublicIP)
	}
	if check.Error != nil {
		t.Errorf("expected nil error on success, got %v", check.Error)
	}
}

func TestOoklaRunner_PublicIPFailureNonFatal(t *testing.T) {
	fixtureData, err := os.ReadFile(filepath.Join("testdata", "normal.json"))
	if err != nil {
		t.Fatalf("failed reading normal.json: %v", err)
	}

	runner := speedtest.NewRunner("/usr/local/bin/speedtest")
	runner.FetchIP = func(ctx context.Context) (string, error) {
		return "", errors.New("ipify connection refused")
	}
	runner.CmdRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return fixtureData, nil
	}

	check, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error from Run: %v", err)
	}
	if check.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", check.Status)
	}
	if check.PublicIP != nil {
		t.Errorf("expected nil public IP when lookup fails, got %v", *check.PublicIP)
	}
}

func TestOoklaRunner_CommandFailureBecomesMeasurementError(t *testing.T) {
	runner := speedtest.NewRunner("/usr/local/bin/speedtest")
	runner.FetchIP = func(ctx context.Context) (string, error) {
		return "120.28.1.2", nil
	}
	runner.CmdRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, errors.New("exit status 1")
	}

	check, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if check.Status != "measurement_error" {
		t.Errorf("expected status 'measurement_error', got %q", check.Status)
	}
	if check.Error == nil || !strings.Contains(*check.Error, "exit status 1") {
		t.Errorf("expected error message to contain exit status 1, got %v", check.Error)
	}
	if check.DownloadMbps != nil {
		t.Errorf("expected nil download on measurement error")
	}
}

func TestOoklaRunner_BadJSONBecomesMeasurementError(t *testing.T) {
	runner := speedtest.NewRunner("/usr/local/bin/speedtest")
	runner.CmdRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("{corrupt-json"), nil
	}

	check, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if check.Status != "measurement_error" {
		t.Errorf("expected status 'measurement_error', got %q", check.Status)
	}
	if check.Error == nil || !strings.Contains(*check.Error, "speedtest parse failed") {
		t.Errorf("expected parse failure error message, got %v", check.Error)
	}
}

func TestOoklaRunnerStoresStderr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speedtest")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'No servers available' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	r := speedtest.NewRunner(path)
	r.FetchIP = nil
	check, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if check.Error == nil || !strings.Contains(*check.Error, "No servers available") {
		t.Fatalf("stderr missing from stored error: %v", check.Error)
	}
}
