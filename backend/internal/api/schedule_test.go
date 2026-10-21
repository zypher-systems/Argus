package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"argus/internal/store"
)

func TestScheduledPushValidation(t *testing.T) {
	b := monitorRequest{Name: "backup", Type: "push", Target: "nightly", IntervalSeconds: 3600,
		Config: store.MonitorConfig{Schedule: "  0   2 * * * ", Timezone: "Europe/Berlin"}}
	if msg := b.validate(); msg != "" {
		t.Fatalf("valid schedule rejected: %s", msg)
	}
	if b.Config.Schedule != "0 2 * * *" || b.IntervalSeconds != 60 {
		t.Fatalf("schedule should be tidied and checked each minute: %q %d", b.Config.Schedule, b.IntervalSeconds)
	}
	for _, bad := range []store.MonitorConfig{
		{Schedule: "0 25 * * *"},
		{Schedule: "@every 1h"},
		{Schedule: "0 2 * * *", Timezone: "Nowhere/Special"},
	} {
		r := monitorRequest{Name: "backup", Type: "push", Target: "nightly", Config: bad}
		if msg := r.validate(); msg == "" {
			t.Errorf("%+v should be refused", bad)
		}
	}
	// No schedule means no time zone either.
	plain := monitorRequest{Name: "backup", Type: "push", Target: "nightly", Config: store.MonitorConfig{Timezone: "Europe/Berlin"}}
	if msg := plain.validate(); msg != "" || plain.Config.Timezone != "" {
		t.Fatalf("a time zone alone is dropped: %q %+v", msg, plain.Config)
	}
}

func TestSchedulePreviewAndNextPing(t *testing.T) {
	s := testAPI(t)
	_, admin := signIn(t, s, "admin@example.com", "change-me-too", "198.51.100.10")

	rec := call(s, admin, http.MethodPost, "/api/schedule/preview", `{"schedule":"0 2 * * *","timezone":"UTC"}`)
	var prev struct {
		Next  []time.Time `json:"next"`
		Error string      `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &prev)
	if rec.Code != 200 || prev.Error != "" || len(prev.Next) != 3 || prev.Next[0].UTC().Hour() != 2 {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body.String())
	}
	rec = call(s, admin, http.MethodPost, "/api/schedule/preview", `{"schedule":"every night"}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &prev)
	if rec.Code != 200 || prev.Error == "" || len(prev.Next) != 0 {
		t.Fatalf("an invalid schedule is an answer with an error: %d %s", rec.Code, rec.Body.String())
	}

	rec = call(s, admin, http.MethodPost, "/api/monitors",
		`{"name":"backup","type":"push","target":"nightly","config":{"schedule":"0 2 * * *","timezone":"UTC"}}`)
	if rec.Code != 201 {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var m store.Monitor
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	rec = call(s, admin, http.MethodGet, fmt.Sprintf("/api/monitors/%d", m.ID), "")
	var got store.MonitorWithState
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.State.NextPingAt == nil || got.State.NextPingAt.UTC().Hour() != 2 || got.IntervalSeconds != 60 {
		t.Fatalf("the monitor should say when the next ping is due: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"schedule":"0 2 * * *"`) {
		t.Fatal("the schedule is part of the config")
	}
}
