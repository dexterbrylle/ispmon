package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
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

	embed := discordEmbed{Title: truncate(e.Title, 256), Description: truncate(e.Body, 4096), Color: color}
	remaining := 6000 - len([]rune(embed.Title)) - len([]rune(embed.Description))
	for _, f := range e.Fields {
		if len(embed.Fields) >= 25 || remaining < 2 {
			break
		}
		name := truncate(f.Name, 256)
		if strings.TrimSpace(name) == "" {
			name = "—"
		}
		value := f.Value
		if strings.TrimSpace(value) == "" {
			value = "—"
		}
		name = truncate(name, remaining-1)
		value = truncate(value, min(1024, remaining-len([]rune(name))))
		embed.Fields = append(embed.Fields, discordField{Name: name, Value: value, Inline: f.Inline})
		remaining -= len([]rune(name)) + len([]rune(value))
	}

	payload := discordPayload{
		Embeds: []discordEmbed{embed},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed marshaling discord payload: %w", err)
	}

	contentType := "application/json"
	if len(e.Attachments) > 0 {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		if err := writer.WriteField("payload_json", string(body)); err != nil {
			return err
		}
		for i, att := range e.Attachments {
			part, err := writer.CreateFormFile(fmt.Sprintf("files[%d]", i), att.Name)
			if err != nil {
				return err
			}
			if _, err := part.Write(att.Data); err != nil {
				return err
			}
		}
		if err := writer.Close(); err != nil {
			return err
		}
		body = buf.Bytes()
		contentType = writer.FormDataContentType()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed creating discord request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

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

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}
