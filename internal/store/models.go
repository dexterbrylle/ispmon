package store

import (
	"errors"
	"time"
)

// ErrOpenEpisodeExists is returned when attempting to start an episode while one is already open.
var ErrOpenEpisodeExists = errors.New("an open episode already exists")

// ErrNotFound is returned when an entity is not found.
var ErrNotFound = errors.New("entity not found")

// NetworkCheck represents a single network speed/status measurement.
type NetworkCheck struct {
	ID             int64
	Timestamp      time.Time
	Status         string // 'ok' or 'measurement_error'
	Engine         string // 'ookla-cli'
	DownloadMbps   *float64
	UploadMbps     *float64
	LatencyMs      *float64
	JitterMs       *float64
	PacketLossPct  *float64
	ServerID       *int64
	ServerName     *string
	ServerLocation *string
	ResultURL      *string
	PublicIP       *string
	BelowThreshold bool
	Error          *string
}

// SLABreachEpisode records an ongoing or resolved SLA breach window.
type SLABreachEpisode struct {
	ID                  int64
	StartedAt           time.Time
	EndedAt             *time.Time
	WorstDownloadMbps   *float64
	WorstUploadMbps     *float64
	ViolatingCheckCount int
	TotalCheckCount     int
	ViolationPct        float64
}

// OutageEpisode records a connectivity outage period.
type OutageEpisode struct {
	ID              int64
	StartedAt       time.Time
	EndedAt         *time.Time
	DurationSeconds *int
	Reason          string
}
