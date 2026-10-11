package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDiscordAttachmentsAndLimits(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error("missing multipart attachments")
			w.WriteHeader(400)
			return
		}
		f, _, err := r.FormFile("files[0]")
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		data, _ := io.ReadAll(f)
		if string(data) != "proof" {
			t.Errorf("attachment = %q", data)
		}
		var p discordPayload
		if err := json.Unmarshal([]byte(r.FormValue("payload_json")), &p); err != nil {
			t.Error(err)
			return
		}
		e := p.Embeds[0]
		total := utf8.RuneCountInString(e.Title + e.Description)
		for i, f := range e.Fields {
			n := utf8.RuneCountInString(f.Value)
			if n == 0 || n > 1024 {
				t.Errorf("field length %d", n)
			}
			if i == 0 && f.Value != "—" {
				t.Errorf("empty field = %q", f.Value)
			}
			total += utf8.RuneCountInString(f.Name) + n
		}
		if total > 6000 {
			t.Errorf("embed total %d", total)
		}
		w.WriteHeader(204)
	}))
	defer ts.Close()
	e := Event{Title: strings.Repeat("t", 300), Body: strings.Repeat("b", 5000), Fields: []Field{{Name: "empty"}}, Attachments: []Attachment{{Name: "proof.csv", Data: []byte("proof")}}}
	for i := 0; i < 25; i++ {
		e.Fields = append(e.Fields, Field{Name: "test", Value: strings.Repeat("界", 2000)})
	}
	if err := NewDiscordNotifier(ts.URL, ts.Client()).Notify(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}
