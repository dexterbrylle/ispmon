package notify

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/smtp"
	"strings"
)

// SMTPSender defines the signature for dispatching an email message.
type SMTPSender func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

// SMTPNotifier sends HTML email alerts with memory-backed MIME attachments via STARTTLS.
type SMTPNotifier struct {
	Server    string
	Port      int
	User      string
	Password  string
	Recipient string
	Sender    SMTPSender
}

// NewSMTPNotifier creates an SMTPNotifier.
func NewSMTPNotifier(server string, port int, user, password, recipient string) *SMTPNotifier {
	return &SMTPNotifier{
		Server:    server,
		Port:      port,
		User:      user,
		Password:  password,
		Recipient: recipient,
		Sender:    smtp.SendMail,
	}
}

func (s *SMTPNotifier) Name() string {
	return "smtp"
}

func (s *SMTPNotifier) Notify(ctx context.Context, e Event) error {
	addr := fmt.Sprintf("%s:%d", s.Server, s.Port)
	auth := smtp.PlainAuth("", s.User, s.Password, s.Server)

	msg, err := BuildMIMEMessage(s.User, s.Recipient, e)
	if err != nil {
		return fmt.Errorf("failed building mime message: %w", err)
	}

	sendErr := make(chan error, 1)
	go func() {
		sendErr <- s.Sender(addr, auth, s.User, []string{s.Recipient}, msg)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-sendErr:
		if err != nil {
			return fmt.Errorf("smtp send failed to %s: %w", s.Recipient, err)
		}
		return nil
	}
}

// BuildMIMEMessage formats an Event into a valid multipart/mixed MIME message.
func BuildMIMEMessage(from, to string, e Event) ([]byte, error) {
	boundary := fmt.Sprintf("boundary_%d_ispmon", 1234567890)
	var buf bytes.Buffer

	// Headers
	buf.WriteString(fmt.Sprintf("From: %s\r\n", from))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", to))
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", e.Title)))
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", boundary))

	// HTML Body Part
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")

	htmlBody := buildHTMLBody(e)
	buf.WriteString(htmlBody)
	buf.WriteString("\r\n\r\n")

	// Attachments
	for _, att := range e.Attachments {
		buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		mimeType := att.MIMEType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		buf.WriteString(fmt.Sprintf("Content-Type: %s; name=\"%s\"\r\n", mimeType, att.Name))
		buf.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", att.Name))
		buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")

		encoded := base64.StdEncoding.EncodeToString(att.Data)
		// Wrap lines at 76 characters
		for len(encoded) > 76 {
			buf.WriteString(encoded[:76] + "\r\n")
			encoded = encoded[76:]
		}
		buf.WriteString(encoded + "\r\n\r\n")
	}

	buf.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	return buf.Bytes(), nil
}

func buildHTMLBody(e Event) string {
	var sb strings.Builder
	sb.WriteString("<html><body style=\"font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; color: #333; line-height: 1.5; padding: 15px;\">")
	sb.WriteString(fmt.Sprintf("<h2 style=\"color: #1976D2; margin-top: 0;\">%s</h2>", e.Title))
	if e.Body != "" {
		sb.WriteString(fmt.Sprintf("<p>%s</p>", strings.ReplaceAll(e.Body, "\n", "<br>")))
	}

	if len(e.Fields) > 0 {
		sb.WriteString("<table style=\"width: 100%; max-width: 600px; border-collapse: collapse; margin: 15px 0;\">")
		for _, f := range e.Fields {
			sb.WriteString("<tr style=\"border-bottom: 1px solid #eee;\">")
			sb.WriteString(fmt.Sprintf("<td style=\"padding: 8px; font-weight: bold; width: 40%%;\">%s</td>", f.Name))
			sb.WriteString(fmt.Sprintf("<td style=\"padding: 8px;\">%s</td>", f.Value))
			sb.WriteString("</tr>")
		}
		sb.WriteString("</table>")
	}

	if len(e.Attachments) > 0 {
		sb.WriteString("<p style=\"font-size: 0.9em; color: #666;\">Attachments included: ")
		var names []string
		for _, a := range e.Attachments {
			names = append(names, a.Name)
		}
		sb.WriteString(strings.Join(names, ", "))
		sb.WriteString("</p>")
	}

	sb.WriteString("</body></html>")
	return sb.String()
}
