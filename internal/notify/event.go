package notify

import "context"

// EventKind specifies the category of notification.
type EventKind string

const (
	EventSLABreach      EventKind = "sla_breach"
	EventSLAResolved    EventKind = "sla_resolved"
	EventOutageStarted  EventKind = "outage_started"
	EventOutageRestored EventKind = "outage_restored"
	EventSummary        EventKind = "summary"
	EventDailyReport    EventKind = "daily_report"
)

// Field represents a key-value attribute in an alert.
type Field struct {
	Name   string
	Value  string
	Inline bool
}

// Attachment holds an in-memory document (e.g. CSV or PDF).
type Attachment struct {
	Name     string
	MIMEType string
	Data     []byte
}

// Event contains notification details and evidence attachments.
type Event struct {
	Kind        EventKind
	Title       string
	Body        string
	Fields      []Field
	Attachments []Attachment
}

// Notifier sends events to external destinations.
type Notifier interface {
	Name() string
	Notify(ctx context.Context, e Event) error
}
