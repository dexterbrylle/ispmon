package speedtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dexterbrylle/isp-speed-monitor/internal/speedtest"
)

func loadFixture(t *testing.T, filename string) []byte {
	t.Helper()
	path := filepath.Join("testdata", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed reading fixture %s: %v", filename, err)
	}
	return data
}

func TestParseOoklaJSON_NormalFixture(t *testing.T) {
	data := loadFixture(t, "normal.json")
	res, err := speedtest.ParseOoklaJSON(data)
	if err != nil {
		t.Fatalf("unexpected error parsing normal.json: %v", err)
	}

	// 62,500,000 bytes * 8 / 1e6 = 500.0 Mbps
	if res.DownloadMbps != 500.0 {
		t.Errorf("expected download 500.0 Mbps, got %v", res.DownloadMbps)
	}
	// 60,000,000 bytes * 8 / 1e6 = 480.0 Mbps
	if res.UploadMbps != 480.0 {
		t.Errorf("expected upload 480.0 Mbps, got %v", res.UploadMbps)
	}
	if res.LatencyMs != 8.42 {
		t.Errorf("expected latency 8.42 ms, got %v", res.LatencyMs)
	}
	if res.JitterMs != 1.25 {
		t.Errorf("expected jitter 1.25 ms, got %v", res.JitterMs)
	}
	if res.PacketLossPct == nil || *res.PacketLossPct != 0.0 {
		t.Errorf("expected packetLoss 0.0, got %v", res.PacketLossPct)
	}
	if res.ServerID != 12345 {
		t.Errorf("expected server ID 12345, got %d", res.ServerID)
	}
	if res.ServerName != "Manila Premier Server" {
		t.Errorf("expected server name 'Manila Premier Server', got %q", res.ServerName)
	}
	if res.ServerLocation != "Manila" {
		t.Errorf("expected server location 'Manila', got %q", res.ServerLocation)
	}
	expectedURL := "https://www.speedtest.net/result/c/a1b2c3d4-e5f6-7890-abcd-ef0123456789"
	if res.ResultURL != expectedURL {
		t.Errorf("expected result URL %q, got %q", expectedURL, res.ResultURL)
	}
}

func TestParseOoklaJSON_MissingPacketLossFixture(t *testing.T) {
	data := loadFixture(t, "missing_packet_loss.json")
	res, err := speedtest.ParseOoklaJSON(data)
	if err != nil {
		t.Fatalf("unexpected error parsing missing_packet_loss.json: %v", err)
	}

	if res.PacketLossPct != nil {
		t.Errorf("expected nil packet loss, got %v", *res.PacketLossPct)
	}
	// 37,500,000 bytes * 8 / 1e6 = 300.0 Mbps
	if res.DownloadMbps != 300.0 {
		t.Errorf("expected download 300.0 Mbps, got %v", res.DownloadMbps)
	}
	// 25,000,000 bytes * 8 / 1e6 = 200.0 Mbps
	if res.UploadMbps != 200.0 {
		t.Errorf("expected upload 200.0 Mbps, got %v", res.UploadMbps)
	}
	if res.ServerName != "Makati High Speed" {
		t.Errorf("expected server name 'Makati High Speed', got %q", res.ServerName)
	}
}

func TestParseOoklaJSON_ErrorPayloadFixture(t *testing.T) {
	data := loadFixture(t, "error_payload.json")
	_, err := speedtest.ParseOoklaJSON(data)
	if err == nil {
		t.Fatalf("expected error from error_payload.json, got nil")
	}
	if !strings.Contains(err.Error(), "Cannot retrieve configuration") {
		t.Errorf("expected error message to contain configuration failure, got: %v", err)
	}
}

func TestParseOoklaJSON_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		errContains string
	}{
		{
			name:        "empty payload",
			input:       "",
			errContains: "empty speedtest output",
		},
		{
			name:        "malformed json",
			input:       "{not-json}",
			errContains: "failed to parse speedtest json",
		},
		{
			name:        "zero download bandwidth",
			input:       `{"download":{"bandwidth":0},"upload":{"bandwidth":50000000}}`,
			errContains: "invalid or zero download bandwidth",
		},
		{
			name:        "zero upload bandwidth",
			input:       `{"download":{"bandwidth":50000000},"upload":{"bandwidth":0}}`,
			errContains: "invalid or zero upload bandwidth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := speedtest.ParseOoklaJSON([]byte(tt.input))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.errContains)
			}
			if !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("expected error containing %q, got %v", tt.errContains, err)
			}
		})
	}
}
