package notify

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestSMTPMIMEUTF8HeadersAndEscaping(t *testing.T) {
	ev := Event{Title: "雪 <script>", Body: "雪 <b>&", Fields: []Field{{Name: "<name>", Value: "<value>"}}, Attachments: []Attachment{{Name: "<proof>.csv", Data: []byte("proof")}}}
	a, err := BuildMIMEMessage("from@example.com", "to@example.com", ev)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := BuildMIMEMessage("from@example.com", "to@example.com", ev)
	msg, err := mail.ReadMessage(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mail.ParseDate(msg.Header.Get("Date")); err != nil {
		t.Error("missing Date")
	}
	if msg.Header.Get("Message-ID") == "" {
		t.Error("missing Message-ID")
	}
	_, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	other, _ := mail.ReadMessage(bytes.NewReader(b))
	_, params2, _ := mime.ParseMediaType(other.Header.Get("Content-Type"))
	if params["boundary"] == params2["boundary"] {
		t.Error("boundary reused")
	}
	part, err := multipart.NewReader(msg.Body, params["boundary"]).NextRawPart()
	if err != nil {
		t.Fatal(err)
	}
	if part.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
		t.Errorf("HTML encoding = %q", part.Header.Get("Content-Transfer-Encoding"))
	}
	var body []byte
	if part.Header.Get("Content-Transfer-Encoding") == "quoted-printable" {
		body, _ = io.ReadAll(quotedprintable.NewReader(part))
	} else {
		body, _ = io.ReadAll(part)
	}
	if strings.Contains(string(body), "<script>") || strings.Contains(string(body), "<b>") || strings.Contains(string(body), "<name>") || strings.Contains(string(body), "<value>") || strings.Contains(string(body), "<proof>") {
		t.Errorf("unescaped HTML: %s", body)
	}
	if !strings.Contains(string(body), "雪") {
		t.Error("UTF8 lost")
	}

}
func TestSMTPCancellationClosesConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := listener.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	host, _, _ := net.SplitHostPort(listener.Addr().String())
	s := NewSMTPNotifier(host, listener.Addr().(*net.TCPAddr).Port, "from@example.com", "pass", "to@example.com")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Notify(ctx, Event{}) }()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("no SMTP connection")
	}
	defer conn.Close()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP did not cancel")
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err = conn.Read(make([]byte, 1))
	if err != io.EOF {
		t.Fatalf("connection leaked after cancellation: %v", err)
	}
}

func TestSMTP465StartsWithTLSHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	first := make(chan byte, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		buf := make([]byte, 1)
		_, _ = c.Read(buf)
		first <- buf[0]
	}()
	s := NewSMTPNotifier("localhost", 465, "user", "password", "recipient")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = s.send(ctx, listener.Addr().String(), nil, "user", []string{"recipient"}, nil)
	if b := <-first; b != 0x16 {
		t.Fatalf("port 465 first byte = %x, want TLS handshake 16", b)
	}
}
