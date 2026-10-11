package report

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

// BuildCSV generates CSV bytes for a collection of network checks.
// Pure function taking data and returning bytes without file I/O.
func BuildCSV(checks []store.NetworkCheck) ([]byte, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	headers := []string{
		"Timestamp",
		"Status",
		"Engine",
		"Download (Mbps)",
		"Upload (Mbps)",
		"Latency (ms)",
		"Jitter (ms)",
		"Packet Loss (%)",
		"Server ID",
		"Server Name",
		"Server Location",
		"Result URL",
		"Public IP",
		"Below Threshold",
		"Error",
	}

	if err := writer.Write(headers); err != nil {
		return nil, fmt.Errorf("failed writing csv header: %w", err)
	}

	for _, c := range checks {
		var (
			downStr, upStr string
			latStr, jitStr string
			lossStr        string
			srvIDStr       string
			srvNameStr     string
			srvLocStr      string
			urlStr, ipStr  string
			belowStr       string
			errStr         string
		)

		if c.DownloadMbps != nil {
			downStr = fmt.Sprintf("%.2f", *c.DownloadMbps)
		}
		if c.UploadMbps != nil {
			upStr = fmt.Sprintf("%.2f", *c.UploadMbps)
		}
		if c.LatencyMs != nil {
			latStr = fmt.Sprintf("%.2f", *c.LatencyMs)
		}
		if c.JitterMs != nil {
			jitStr = fmt.Sprintf("%.2f", *c.JitterMs)
		}
		if c.PacketLossPct != nil {
			lossStr = fmt.Sprintf("%.2f", *c.PacketLossPct)
		}
		if c.ServerID != nil {
			srvIDStr = fmt.Sprintf("%d", *c.ServerID)
		}
		if c.ServerName != nil {
			srvNameStr = *c.ServerName
		}
		if c.ServerLocation != nil {
			srvLocStr = *c.ServerLocation
		}
		if c.ResultURL != nil {
			urlStr = *c.ResultURL
		}
		if c.PublicIP != nil {
			ipStr = *c.PublicIP
		}
		if c.BelowThreshold {
			belowStr = "1"
		} else {
			belowStr = "0"
		}
		if c.Error != nil {
			errStr = *c.Error
		}

		row := []string{
			c.Timestamp.UTC().Format(time.RFC3339),
			c.Status,
			c.Engine,
			downStr,
			upStr,
			latStr,
			jitStr,
			lossStr,
			srvIDStr,
			srvNameStr,
			srvLocStr,
			urlStr,
			ipStr,
			belowStr,
			errStr,
		}

		if err := writer.Write(row); err != nil {
			return nil, fmt.Errorf("failed writing csv row: %w", err)
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, fmt.Errorf("csv flush error: %w", err)
	}

	return buf.Bytes(), nil
}
