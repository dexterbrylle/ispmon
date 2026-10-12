package report_test

import (
	"bytes"
	"encoding/csv"
	"testing"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/report"
	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

func sampleData() report.SummaryData {
	base := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	dl1, ul1, lat1, jit1, loss1 := 500.0, 480.0, 10.0, 1.2, 0.0
	srvID := int64(1234)
	srvName := "Manila Server"
	srvLoc := "Manila, PH"
	resURL := "https://www.speedtest.net/result/c/123"
	pubIP := "120.28.1.2"

	dl2, ul2, lat2 := 120.0, 110.0, 35.0

	errStr := "Ookla CLI timeout"

	checks := []store.NetworkCheck{
		{
			Timestamp:      base.Add(1 * time.Hour),
			Status:         "ok",
			Engine:         "ookla-cli",
			DownloadMbps:   &dl1,
			UploadMbps:     &ul1,
			LatencyMs:      &lat1,
			JitterMs:       &jit1,
			PacketLossPct:  &loss1,
			ServerID:       &srvID,
			ServerName:     &srvName,
			ServerLocation: &srvLoc,
			ResultURL:      &resURL,
			PublicIP:       &pubIP,
			BelowThreshold: false,
		},
		{
			Timestamp:      base.Add(2 * time.Hour),
			Status:         "ok",
			Engine:         "ookla-cli",
			DownloadMbps:   &dl2,
			UploadMbps:     &ul2,
			LatencyMs:      &lat2,
			ServerName:     &srvName,
			BelowThreshold: true,
		},
		{
			Timestamp: base.Add(3 * time.Hour),
			Status:    "measurement_error",
			Engine:    "ookla-cli",
			Error:     &errStr,
		},
	}

	worstDown, worstUp := 120.0, 110.0
	breaches := []store.SLABreachEpisode{
		{
			ID:                  1,
			StartedAt:           base.Add(2 * time.Hour),
			WorstDownloadMbps:   &worstDown,
			WorstUploadMbps:     &worstUp,
			ViolatingCheckCount: 1,
			TotalCheckCount:     2,
			ViolationPct:        50.0,
		},
	}

	dur := 180
	outages := []store.OutageEpisode{
		{
			ID:              1,
			StartedAt:       base.Add(1*time.Hour + 30*time.Minute),
			DurationSeconds: &dur,
			Reason:          "all targets failed",
		},
	}

	return report.BuildSummaryData(
		base, base.Add(24*time.Hour),
		checks, breaches, outages, nil, nil,
		500.0, 500.0, "GFiber 500",
	)
}

func TestReport_BuildSummaryData(t *testing.T) {
	data := sampleData()

	if data.TotalChecks != 3 {
		t.Errorf("expected 3 total checks, got %d", data.TotalChecks)
	}
	if data.OKChecks != 2 {
		t.Errorf("expected 2 ok checks, got %d", data.OKChecks)
	}
	if data.MeasurementErrors != 1 {
		t.Errorf("expected 1 measurement error, got %d", data.MeasurementErrors)
	}
	if data.SlowDownloadChecks != 1 {
		t.Errorf("expected 1 slow download check (< 500), got %d", data.SlowDownloadChecks)
	}
	if data.AvgDownloadMbps == nil || *data.AvgDownloadMbps != 310.0 {
		t.Errorf("expected avg download (500+120)/2 = 310.0, got %v", data.AvgDownloadMbps)
	}
	if data.MinDownloadMbps == nil || *data.MinDownloadMbps != 120.0 {
		t.Errorf("expected min download 120.0, got %v", data.MinDownloadMbps)
	}
	if data.MaxDownloadMbps == nil || *data.MaxDownloadMbps != 500.0 {
		t.Errorf("expected max download 500.0, got %v", data.MaxDownloadMbps)
	}
	if data.TotalDowntimeSeconds != 180 {
		t.Errorf("expected 180s downtime, got %d", data.TotalDowntimeSeconds)
	}
}

func TestReport_BuildCSV(t *testing.T) {
	data := sampleData()
	csvBytes, err := report.BuildCSV(data.Checks)
	if err != nil {
		t.Fatalf("unexpected error building csv: %v", err)
	}

	reader := csv.NewReader(bytes.NewReader(csvBytes))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("failed parsing generated csv: %v", err)
	}

	if len(records) != 4 { // 1 header + 3 rows
		t.Fatalf("expected 4 csv records, got %d", len(records))
	}

	headers := records[0]
	if headers[0] != "Timestamp" || headers[2] != "Engine" || headers[9] != "Server Name" || headers[11] != "Result URL" {
		t.Errorf("unexpected headers in csv: %v", headers)
	}

	row1 := records[1]
	if row1[1] != "ok" || row1[2] != "ookla-cli" || row1[3] != "500.00" || row1[9] != "Manila Server" {
		t.Errorf("unexpected row 1 in csv: %v", row1)
	}

	row3 := records[3] // measurement_error
	if row3[1] != "measurement_error" || row3[14] != "Ookla CLI timeout" {
		t.Errorf("unexpected row 3 in csv: %v", row3)
	}
}

func TestReport_BuildPDF(t *testing.T) {
	data := sampleData()
	pdfBytes, err := report.BuildPDF(data, time.UTC)
	if err != nil {
		t.Fatalf("unexpected error building pdf: %v", err)
	}

	if len(pdfBytes) == 0 {
		t.Fatalf("pdf output is empty")
	}

	// In PDF format, files always begin with "%PDF"
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF")) {
		t.Fatalf("expected pdf output to begin with '%%PDF', got prefix %q", string(pdfBytes[:10]))
	}
}

func TestReport_SummaryText(t *testing.T) {
	data := sampleData()

	utcText := report.SummaryText(data, time.UTC)
	expectedUTC := "Performance overview for period 00:00 to 00:00 UTC."
	if utcText != expectedUTC {
		t.Errorf("expected UTC summary text %q, got %q", expectedUTC, utcText)
	}

	nyLoc := time.FixedZone("EDT", -4*3600)
	nyText := report.SummaryText(data, nyLoc)
	expectedNY := "Performance overview for period 20:00 to 20:00 EDT."
	if nyText != expectedNY {
		t.Errorf("expected EDT summary text %q, got %q", expectedNY, nyText)
	}

	tokyoLoc := time.FixedZone("JST", 9*3600)
	tokyoText := report.SummaryText(data, tokyoLoc)
	expectedTokyo := "Performance overview for period 09:00 to 09:00 JST."
	if tokyoText != expectedTokyo {
		t.Errorf("expected JST summary text %q, got %q", expectedTokyo, tokyoText)
	}
}

func TestReport_BuildPDF_WithTimezone(t *testing.T) {
	data := sampleData()
	nyLoc := time.FixedZone("EDT", -4*3600)
	pdfBytes, err := report.BuildPDF(data, nyLoc)
	if err != nil {
		t.Fatalf("unexpected error building pdf with custom timezone: %v", err)
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF")) {
		t.Fatalf("expected pdf output to begin with '%%PDF'")
	}
}
