package speedtest

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Result represents the parsed metrics from an Ookla speedtest CLI run.
type Result struct {
	DownloadMbps   float64
	UploadMbps     float64
	LatencyMs      float64
	JitterMs       float64
	PacketLossPct  *float64
	ServerID       int64
	ServerName     string
	ServerLocation string
	ResultURL      string
}

type rawOoklaPayload struct {
	Type       string   `json:"type"`
	Level      string   `json:"level"`
	Message    string   `json:"message"`
	PacketLoss *float64 `json:"packetLoss"`
	Ping       struct {
		Latency float64 `json:"latency"`
		Jitter  float64 `json:"jitter"`
	} `json:"ping"`
	Download struct {
		Bandwidth int64 `json:"bandwidth"`
	} `json:"download"`
	Upload struct {
		Bandwidth int64 `json:"bandwidth"`
	} `json:"upload"`
	Server struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Location string `json:"location"`
	} `json:"server"`
	Result struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	} `json:"result"`
}

// ParseOoklaJSON parses raw Ookla CLI JSON output into a Result.
// It returns an error if the payload represents an error, contains malformed JSON,
// or has zero/missing bandwidth.
func ParseOoklaJSON(data []byte) (*Result, error) {
	if len(data) == 0 {
		return nil, errors.New("empty speedtest output")
	}

	var raw rawOoklaPayload
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse speedtest json: %w", err)
	}

	if raw.Type == "log" || raw.Level == "error" || raw.Message != "" {
		msg := raw.Message
		if msg == "" {
			msg = "unknown error reported by speedtest cli"
		}
		return nil, fmt.Errorf("speedtest cli error: %s", msg)
	}

	if raw.Download.Bandwidth <= 0 {
		return nil, errors.New("invalid or zero download bandwidth")
	}
	if raw.Upload.Bandwidth <= 0 {
		return nil, errors.New("invalid or zero upload bandwidth")
	}

	// Bandwidth is in bytes per second: Mbps = bytes * 8 / 1,000,000
	downloadMbps := float64(raw.Download.Bandwidth) * 8.0 / 1e6
	uploadMbps := float64(raw.Upload.Bandwidth) * 8.0 / 1e6

	return &Result{
		DownloadMbps:   downloadMbps,
		UploadMbps:     uploadMbps,
		LatencyMs:      raw.Ping.Latency,
		JitterMs:       raw.Ping.Jitter,
		PacketLossPct:  raw.PacketLoss,
		ServerID:       raw.Server.ID,
		ServerName:     raw.Server.Name,
		ServerLocation: raw.Server.Location,
		ResultURL:      raw.Result.URL,
	}, nil
}
