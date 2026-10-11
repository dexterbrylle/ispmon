package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dexterbrylle/isp-speed-monitor/internal/notify"
	"github.com/dexterbrylle/isp-speed-monitor/internal/sla"
	"github.com/dexterbrylle/isp-speed-monitor/internal/store"
)

func TestDiscordNotifier_ColorsAndPayload(t *testing.T) {
	var receivedPayload map[string]interface{}
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			_ = r.ParseMultipartForm(1 << 20)
			_ = json.Unmarshal([]byte(r.FormValue("payload_json")), &receivedPayload)
		} else {
			_ = json.NewDecoder(r.Body).Decode(&receivedPayload)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	d := notify.NewDiscordNotifier(ts.URL, ts.Client())

	// 1. SLA Breach (Must be Red)
	worstDown := 95.0
	worstUp := 90.0
	url := "https://www.speedtest.net/result/c/123"
	breachEval := &sla.EvaluationResult{
		Status:            sla.StatusBreach,
		ViolatingCount:    10,
		TotalValidCount:   40,
		ViolationPct:      25.0,
		WorstDownloadMbps: &worstDown,
		WorstUploadMbps:   &worstUp,
		BreachedChecks: []store.NetworkCheck{
			{
				DownloadMbps: &worstDown,
				ResultURL:    &url,
			},
		},
	}
	breachEvent := notify.BuildSLABreachEvent("Test Plan", breachEval, 150.0, 150.0, 24, []byte("csv-data"))

	err := d.Notify(context.Background(), breachEvent)
	if err != nil {
		t.Fatalf("unexpected discord notify error: %v", err)
	}

	mu.Lock()
	embeds := receivedPayload["embeds"].([]interface{})
	firstEmbed := embeds[0].(map[string]interface{})
	color := int(firstEmbed["color"].(float64))
	if color != notify.ColorRed {
		t.Errorf("expected breach color %d, got %d", notify.ColorRed, color)
	}
	title := firstEmbed["title"].(string)
	if !strings.Contains(title, "SLA Breach Declared") {
		t.Errorf("expected title to mention SLA Breach, got %q", title)
	}
	fields := firstEmbed["fields"].([]interface{})
	var foundURL bool
	for _, f := range fields {
		fieldMap := f.(map[string]interface{})
		if strings.Contains(fieldMap["value"].(string), "https://www.speedtest.net/result/c/123") {
			foundURL = true
		}
	}
	if !foundURL {
		t.Errorf("expected proof URL in breach card fields")
	}
	mu.Unlock()

	// 2. SLA Resolved (Must be Green)
	ep := &store.SLABreachEpisode{
		ID:           42,
		StartedAt:    time.Now().Add(-2 * time.Hour),
		ViolationPct: 25.0,
	}
	resolvedEvent := notify.BuildSLAResolvedEvent("Test Plan", ep, time.Now())
	err = d.Notify(context.Background(), resolvedEvent)
	if err != nil {
		t.Fatalf("unexpected notify error: %v", err)
	}

	mu.Lock()
	embeds = receivedPayload["embeds"].([]interface{})
	firstEmbed = embeds[0].(map[string]interface{})
	color = int(firstEmbed["color"].(float64))
	if color != notify.ColorGreen {
		t.Errorf("expected resolved color %d, got %d", notify.ColorGreen, color)
	}
	mu.Unlock()
}

func TestSMTPNotifier_BuildMIME(t *testing.T) {
	e := notify.Event{
		Kind:  notify.EventDailyReport,
		Title: "Daily Report - Test",
		Body:  "Summary of the last 24h",
		Fields: []notify.Field{
			{Name: "Total Checks", Value: "48"},
		},
		Attachments: []notify.Attachment{
			{
				Name:     "report.pdf",
				MIMEType: "application/pdf",
				Data:     []byte("%PDF-sample"),
			},
		},
	}

	var sentAddr, sentFrom string
	var sentTo []string
	var sentMsg []byte

	mockSender := func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		sentAddr = addr
		sentFrom = from
		sentTo = to
		sentMsg = msg
		return nil
	}

	s := notify.NewSMTPNotifier("smtp.example.com", 587, "user@example.com", "pass", "admin@example.com")
	s.Sender = mockSender

	err := s.Notify(context.Background(), e)
	if err != nil {
		t.Fatalf("unexpected error from smtp notify: %v", err)
	}

	if sentAddr != "smtp.example.com:587" {
		t.Errorf("unexpected smtp addr: %s", sentAddr)
	}
	if sentFrom != "user@example.com" || len(sentTo) != 1 || sentTo[0] != "admin@example.com" {
		t.Errorf("unexpected recipients: from=%s, to=%v", sentFrom, sentTo)
	}

	msgStr := string(sentMsg)
	if !strings.Contains(msgStr, "Subject: Daily Report - Test") {
		t.Errorf("missing subject in mime message")
	}
	if !strings.Contains(msgStr, "filename=\"report.pdf\"") {
		t.Errorf("missing attachment filename in mime message")
	}
	if !strings.Contains(msgStr, "Content-Type: application/pdf") {
		t.Errorf("missing application/pdf in mime message")
	}
}

type fakeNotifier struct {
	name      string
	callCount int32
	failCount int32
	isNetErr  bool
	panics    bool
}

func (f *fakeNotifier) Name() string { return f.name }

type fakeNetError struct {
	msg string
}

func (e fakeNetError) Error() string   { return e.msg }
func (e fakeNetError) Timeout() bool   { return true }
func (e fakeNetError) Temporary() bool { return true }

func (f *fakeNotifier) Notify(ctx context.Context, e notify.Event) error {
	count := atomic.AddInt32(&f.callCount, 1)
	if f.panics {
		panic("simulated notifier panic")
	}
	if count <= f.failCount {
		if f.isNetErr {
			return fakeNetError{msg: "connection reset by peer"}
		}
		return errors.New("permanent failure")
	}
	return nil
}

func TestDispatcher_IsolationAndRetry(t *testing.T) {
	// Notifier 1: always succeeds
	n1 := &fakeNotifier{name: "good"}
	// Notifier 2: fails with permanent error (no retry)
	n2 := &fakeNotifier{name: "failing", failCount: 10, isNetErr: false}
	// Notifier 3: fails once with transient net error, succeeds on immediate retry
	n3 := &fakeNotifier{name: "transient", failCount: 1, isNetErr: true}
	// Notifier 4: panics! Dispatcher must recover and continue.
	n4 := &fakeNotifier{name: "panicking", panics: true}

	disp := notify.NewDispatcher([]notify.Notifier{n1, n2, n3, n4}, nil)

	event := notify.Event{
		Kind:  notify.EventOutageStarted,
		Title: "Outage Test",
	}

	// Dispatch must not block, panic, or fail
	disp.Dispatch(context.Background(), event)

	if atomic.LoadInt32(&n1.callCount) != 1 {
		t.Errorf("expected n1 to be called once, got %d", n1.callCount)
	}
	if atomic.LoadInt32(&n2.callCount) != 1 {
		t.Errorf("expected n2 to be called once (no retry for permanent error), got %d", n2.callCount)
	}
	if atomic.LoadInt32(&n3.callCount) != 2 {
		t.Errorf("expected n3 to be called twice (initial + 1 immediate retry), got %d", n3.callCount)
	}
	if atomic.LoadInt32(&n4.callCount) != 1 {
		t.Errorf("expected n4 to be called once, got %d", n4.callCount)
	}
}
