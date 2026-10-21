package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"argus/internal/store"
)

func TestConfirmLocationsValidation(t *testing.T) {
	base := func() monitorRequest {
		return monitorRequest{Name: "shop", Type: "http", Target: "https://shop.example.com", IntervalSeconds: 60}
	}
	b := base()
	if msg := b.validate(); msg != "" || b.ConfirmLocations != 1 {
		t.Fatalf("default should be 1: %q %d", msg, b.ConfirmLocations)
	}
	b = base()
	b.ConfirmLocations = 2
	if msg := b.validate(); msg == "" {
		t.Fatal("confirm 2 with one location must be rejected")
	}
	b = base()
	b.ProbeNodeIDs = []int64{0, 4, 5}
	b.ConfirmLocations = 2
	if msg := b.validate(); msg != "" {
		t.Fatalf("2 of 3 locations should be accepted: %s", msg)
	}
	b.ConfirmLocations = 4
	if msg := b.validate(); msg == "" || !strings.Contains(msg, "3") {
		t.Fatalf("more than the locations must be rejected, naming the count: %q", msg)
	}
	push := monitorRequest{Name: "backup", Type: "push", Target: "nightly", IntervalSeconds: 3600,
		ProbeNodeIDs: []int64{4}, ConfirmLocations: 3}
	if msg := push.validate(); msg != "" || len(push.ProbeNodeIDs) != 0 || push.ConfirmLocations != 1 {
		t.Fatalf("a push monitor is always local: %q %+v", msg, push)
	}
}

// probeResult posts a result as a remote probe node would.
func probeResult(t *testing.T, s *Server, token string, body map[string]any) int {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/probe/results", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec.Code
}

// TestRemoteFailuresAlert covers the bug 2.9.0 fixed: results from remote
// probe nodes changed a monitor's state but never produced an event, an
// incident, or an alert.
func TestRemoteFailuresAlert(t *testing.T) {
	s := testAPI(t)
	ctx := context.Background()
	eu, err := s.store.CreateProbeNode(ctx, "Frankfurt", "eu-central")
	if err != nil {
		t.Fatal(err)
	}
	us, err := s.store.CreateProbeNode(ctx, "Virginia", "us-east")
	if err != nil {
		t.Fatal(err)
	}
	m := store.Monitor{Name: "shop", Type: "http", Target: "https://shop.example.com", IntervalSeconds: 60,
		Retries: 1, Enabled: true, ProbeNodeIDs: []int64{eu.ID, us.ID}, ConfirmLocations: 2}
	if err := s.store.CreateMonitor(ctx, &m); err != nil {
		t.Fatal(err)
	}
	fail := func(token string) {
		t.Helper()
		ev := store.CheckEvidence{Kind: "http", At: time.Now().UTC(), Error: "status 503", StatusCode: 503}
		if code := probeResult(t, s, token, map[string]any{"monitor_id": m.ID, "success": false, "error": "status 503", "evidence": ev}); code != 200 {
			t.Fatalf("result rejected: %d", code)
		}
	}

	// Both nodes are running: they ask for work before they check.
	for _, tok := range []string{eu.Token, us.Token} {
		req := httptest.NewRequest(http.MethodGet, "/api/probe/jobs", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"shop"`) {
			t.Fatalf("jobs %d %s", rec.Code, rec.Body.String())
		}
	}
	fail(eu.Token)
	if st, _ := s.store.MonitorStatus(ctx, m.ID); st == "down" {
		t.Fatal("one of two locations must not take it down with confirm 2")
	}
	fail(us.Token)
	if st, _ := s.store.MonitorStatus(ctx, m.ID); st != "down" {
		t.Fatalf("both locations agree, so it is down; got %s", st)
	}

	events, err := s.store.ListEvents(ctx, m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var down *store.Event
	for i := range events {
		if events[i].Type == "down" {
			down = &events[i]
		}
	}
	if down == nil {
		t.Fatalf("a remote-confirmed outage must be recorded as an event: %+v", events)
	}
	if !strings.Contains(down.Detail, "2 of 2 locations") || !strings.Contains(down.Detail, "Frankfurt") ||
		!strings.Contains(down.Detail, "Virginia") || !strings.Contains(down.Detail, "status 503") {
		t.Fatalf("the event should say where it was seen and why: %q", down.Detail)
	}
	incidents, err := s.store.ListIncidents(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 {
		t.Fatalf("a remote-confirmed outage should open an incident, got %d", len(incidents))
	}
	got, _ := s.store.GetMonitor(ctx, m.ID)
	if got.State.LastFailureEvidence == nil || got.State.LastFailureEvidence.StatusCode != 503 {
		t.Fatalf("evidence from the node should be kept: %+v", got.State.LastFailureEvidence)
	}

	// A node that is not one of the monitor's locations cannot vote.
	other, _ := s.store.CreateProbeNode(ctx, "Tokyo", "ap-northeast")
	if code := probeResult(t, s, other.Token, map[string]any{"monitor_id": m.ID, "success": true}); code != http.StatusForbidden {
		t.Fatalf("unassigned node: want 403, got %d", code)
	}
}

// TestPushDownAlerts covers the same gap for heartbeats: a job reporting
// ?status=down changed state without paging anyone.
func TestPushDownAlerts(t *testing.T) {
	s := testAPI(t)
	ctx := context.Background()
	m := store.Monitor{Name: "backup", Type: "push", Target: "nightly", IntervalSeconds: 3600, Retries: 1, Enabled: true,
		Config: store.MonitorConfig{PushToken: "push-token-123"}}
	if err := s.store.CreateMonitor(ctx, &m); err != nil {
		t.Fatal(err)
	}
	push := func(q string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/push/push-token-123"+q, nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("push %s: %d %s", q, rec.Code, rec.Body.String())
		}
	}
	push("?status=up")
	push("?status=down&msg=disk%20full")
	incidents, _ := s.store.ListIncidents(ctx, true)
	if len(incidents) != 1 {
		t.Fatalf("a pushed failure should open an incident, got %d", len(incidents))
	}
	events, _ := s.store.ListEvents(ctx, m.ID, 10)
	found := false
	for _, e := range events {
		if e.Type == "down" && strings.Contains(e.Detail, "disk full") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the job's message should be the event detail: %+v", events)
	}
	push("?status=up")
	if open, _ := s.store.ListIncidents(ctx, true); len(open) != 0 {
		t.Fatal("recovery should resolve the incident")
	}
	// A heartbeat without ?ping= has nothing to chart.
	var withLatency int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM monitor_results WHERE monitor_id=$1 AND latency_ms IS NOT NULL`, m.ID).Scan(&withLatency); err != nil {
		t.Fatal(err)
	}
	if withLatency != 0 {
		t.Fatalf("heartbeats without a ping should record no latency, found %d", withLatency)
	}
}
