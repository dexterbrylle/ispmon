package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"html"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// SMTPSender defines the signature for dispatching an email message.
type SMTPSender func(ctx context.Context, addr string, a smtp.Auth, from string, to []string, msg []byte) error

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

	sender := s.Sender
	if sender == nil {
		sender = s.send
	}
	if err := sender(ctx, addr, auth, s.User, []string{s.Recipient}, msg); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("smtp send failed to %s: %w", s.Recipient, err)
	}
	return nil

}

// BuildMIMEMessage formats an Event into a valid multipart/mixed MIME message.
func BuildMIMEMessage(from, to string, e Event) ([]byte, error) {
	boundary := multipart.NewWriter(&bytes.Buffer{}).Boundary()
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	var buf bytes.Buffer

	// Headers
	buf.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	buf.WriteString(fmt.Sprintf("Message-ID: <%x@ispmon>\r\n", id))
	buf.WriteString(fmt.Sprintf("From: %s\r\n", from))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", to))
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", e.Title)))
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", boundary))

	// HTML Body Part
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")

	htmlBody := buildHTMLBody(e)
	writer := quotedprintable.NewWriter(&buf)
	if _, err := writer.Write([]byte(htmlBody)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
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
	sb.WriteString(fmt.Sprintf("<h2 style=\"color: #1976D2; margin-top: 0;\">%s</h2>", html.EscapeString(e.Title)))
	if e.Body != "" {
		sb.WriteString(fmt.Sprintf("<p>%s</p>", strings.ReplaceAll(html.EscapeString(e.Body), "\n", "<br>")))
	}

	if len(e.Fields) > 0 {
		sb.WriteString("<table style=\"width: 100%; max-width: 600px; border-collapse: collapse; margin: 15px 0;\">")
		for _, f := range e.Fields {
			sb.WriteString("<tr style=\"border-bottom: 1px solid #eee;\">")
			sb.WriteString(fmt.Sprintf("<td style=\"padding: 8px; font-weight: bold; width: 40%%;\">%s</td>", html.EscapeString(f.Name)))
			sb.WriteString(fmt.Sprintf("<td style=\"padding: 8px;\">%s</td>", html.EscapeString(f.Value)))
			sb.WriteString("</tr>")
		}
		sb.WriteString("</table>")
	}

	if len(e.Attachments) > 0 {
		sb.WriteString("<p style=\"font-size: 0.9em; color: #666;\">Attachments included: ")
		var names []string
		for _, a := range e.Attachments {
			names = append(names, html.EscapeString(a.Name))
		}
		sb.WriteString(strings.Join(names, ", "))
		sb.WriteString("</p>")
	}

	sb.WriteString("</body></html>")
	return sb.String()
}

func (s *SMTPNotifier) send(ctx context.Context, addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	tlsConfig := &tls.Config{ServerName: s.Server, MinVersion: tls.VersionTLS12}
	if s.Port == 465 {
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, s.Server)
	if err != nil {
		return err
	}
	defer client.Close()
	if s.Port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				return err
			}
		}
	}
	if ok, _ := client.Extension("AUTH"); ok {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
