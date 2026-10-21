package alerting

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"argus/internal/store"
)

func testPayload() Payload {
	return Payload{
		Monitor: store.Monitor{ID: 7, Name: "edge", Type: "http", Target: "https://app.internal/health"},
		Type:    "down",
		At:      time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
		Detail:  "status 500",
	}
}

func TestValidHookURL(t *testing.T) {
	if _, err := ValidHookURL("not-a-url"); err == nil {
		t.Fatal("expected error")
	}
	got, err := ValidHookURL("https://hooks.slack.com/services/T/B/x")
	if err != nil || got == "" {
		t.Fatalf("valid url rejected: %v %q", err, got)
	}
	got, err = ValidHookURL("  ")
	if err != nil || got != "" {
		t.Fatalf("blank should be empty: %v %q", err, got)
	}
}

func TestWebhookBody(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(WebhookBody(testPayload()), &m); err != nil {
		t.Fatal(err)
	}
	if m["event"] != "down" {
		t.Fatalf("event %v", m["event"])
	}
	mon := m["monitor"].(map[string]any)
	if mon["name"] != "edge" {
		t.Fatalf("monitor %v", mon)
	}
}

func TestSlowWebhookTitle(t *testing.T) {
	p := testPayload()
	p.Type = "slow"
	if eventTitle(p) != "[Argus] SLOW: edge" {
		t.Fatalf("title %q", eventTitle(p))
	}
	hex, _, slack := eventColor("slow")
	if hex != "#f59e0b" || slack != "warning" {
		t.Fatalf("color %s %s", hex, slack)
	}
}

func TestPostJSONOK(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "Argus/0.6" {
			t.Errorf("ua %s", r.Header.Get("User-Agent"))
		}
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := PostJSON(context.Background(), srv.URL, SlackBody(testPayload())); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(got) {
		t.Fatalf("invalid json %s", got)
	}
}

func TestPostJSONStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	if err := PostJSON(context.Background(), srv.URL, []byte(`{}`)); err == nil {
		t.Fatal("expected http error")
	}
}
