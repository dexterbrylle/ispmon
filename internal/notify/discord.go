package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Discord colors
const (
	ColorRed   = 0xD32F2F // SLA breach, Outage started
	ColorGreen = 0x2E7D32 // SLA resolved, Outage restored
	ColorBlue  = 0x1976D2 // Summaries, Reports
)

// DiscordNotifier posts alerts to a Discord webhook.
type DiscordNotifier struct {
	WebhookURL string
	Client     *http.Client
}

// NewDiscordNotifier creates a DiscordNotifier.
func NewDiscordNotifier(webhookURL string, client *http.Client) *DiscordNotifier {
	if client == nil {
		client = http.DefaultClient
	}
	return &DiscordNotifier{
		WebhookURL: webhookURL,
		Client:     client,
	}
}

func (d *DiscordNotifier) Name() string {
	return "discord"
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Color       int            `json:"color"`
	Fields      []discordField `json:"fields,omitempty"`
}

type discordPayload struct {
	Embeds []discordEmbed `json:"embeds"`
}

func (d *DiscordNotifier) Notify(ctx context.Context, e Event) error {
	color := ColorBlue
	switch e.Kind {
	case EventSLABreach, EventOutageStarted:
		color = ColorRed
	case EventSLAResolved, EventOutageRestored:
		color = ColorGreen
	}

	var fields []discordField
	for _, f := range e.Fields {
		fields = append(fields, discordField(f))
	}

	embed := discordEmbed{
		Title:       e.Title,
		Description: e.Body,
		Color:       color,
		Fields:      fields,
	}

	payload := discordPayload{
		Embeds: []discordEmbed{embed},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed marshaling discord payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed creating discord request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.Client.Do(req)
	if err != nil {
		return fmt.Errorf("failed sending discord webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook returned unexpected status %d", resp.StatusCode)
	}

	return nil
}
