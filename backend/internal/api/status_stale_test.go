package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"argus/internal/store"
)

// staleFixture adds a monitor with the given last known state. age is how
// long ago it was last checked; negative means it never has been.
func staleFixture(t *testing.T, h *apiHarness, name, kind, status string, slow bool, interval int, age time.Duration) int64 {
	t.Helper()
	ctx := context.Background()
	m := store.Monitor{Name: name, Type: kind, Target: "https://" + name + ".example.com", IntervalSeconds: interval, Retries: 1, Enabled: true, ConfirmLocations: 1}
	if err := h.s.store.CreateMonitor(ctx, &m); err != nil {
		t.Fatal(err)
	}
	var checked, slowSince *time.Time
	if age >= 0 {
		at := time.Now().Add(-age)
		checked = &at
	}
	if slow {
		at := time.Now().Add(-time.Hour)
		slowSince = &at
	}
	if _, err := h.s.store.Pool.Exec(ctx, `UPDATE monitor_state SET status=$1, last_check_at=$2, slow_since=$3 WHERE monitor_id=$4`,
		status, checked, slowSince, m.ID); err != nil {
		t.Fatal(err)
	}
	return m.ID
}

func TestPublicMonitorsDoNotCallAQuietMonitorUp(t *testing.T) {
	h := newAPIHarness(t)
	const hour = time.Hour
	cases := []struct {
		name       string
		kind       string
		status     string
		slow       bool
		interval   int
		age        time.Duration
		wantStatus string
		wantSlow   bool
		wantOver   string
	}{
		{name: "fresh", kind: "http", status: "up", interval: 60, age: time.Second, wantStatus: "up", wantOver: "operational"},
		{name: "just-inside-the-limit", kind: "http", status: "up", interval: 60, age: 130 * time.Second, wantStatus: "up", wantOver: "operational"},
		{name: "past-the-limit", kind: "http", status: "up", interval: 60, age: hour, wantStatus: "unknown", wantOver: "unknown"},
		{name: "fresh-and-slow", kind: "http", status: "up", slow: true, interval: 60, age: time.Second, wantStatus: "up", wantSlow: true, wantOver: "degraded"},
		{name: "slow-then-quiet", kind: "http", status: "up", slow: true, interval: 60, age: hour, wantStatus: "unknown", wantOver: "unknown"},
		// The probe stopping is no reason to take an outage off the page.
		{name: "down-then-quiet", kind: "http", status: "down", interval: 60, age: hour, wantStatus: "down", wantOver: "down"},
		{name: "never-checked", kind: "http", status: "pending", interval: 60, age: -1, wantStatus: "pending", wantOver: "unknown"},
		// A group is refreshed by a sweep, whatever interval it holds.
		{name: "group-between-sweeps", kind: "group", status: "up", interval: 10, age: 45 * time.Second, wantStatus: "up", wantOver: "operational"},
		{name: "group-after-sweeps-stop", kind: "group", status: "up", interval: 10, age: hour, wantStatus: "unknown", wantOver: "unknown"},
		{name: "long-group-between-sweeps", kind: "group", status: "up", interval: 3600, age: 45 * time.Second, wantStatus: "up", wantOver: "operational"},
		{name: "long-group-after-sweeps-stop", kind: "group", status: "up", interval: 3600, age: 5 * time.Minute, wantStatus: "unknown", wantOver: "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := staleFixture(t, h, c.name, c.kind, c.status, c.slow, c.interval, c.age)
			got, err := h.s.store.PublicMonitors(context.Background(), []int64{id})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("%d monitors", len(got))
			}
			if got[0].Status != c.wantStatus || got[0].Slow != c.wantSlow {
				t.Fatalf("status %q slow %v, want %q %v", got[0].Status, got[0].Slow, c.wantStatus, c.wantSlow)
			}
			if over := overallStatus(got); over != c.wantOver {
				t.Fatalf("overall %q, want %q", over, c.wantOver)
			}
		})
	}
}

func TestOverallStatusOrder(t *testing.T) {
	m := func(status string, slow bool) store.PublicMonitor {
		return store.PublicMonitor{Status: status, Slow: slow}
	}
	cases := []struct {
		want     string
		monitors []store.PublicMonitor
	}{
		{"operational", []store.PublicMonitor{m("up", false), m("up", false)}},
		{"unknown", []store.PublicMonitor{m("up", false), m("unknown", false)}},
		{"unknown", []store.PublicMonitor{m("up", false), m("pending", false)}},
		{"degraded", []store.PublicMonitor{m("unknown", false), m("up", true)}},
		{"down", []store.PublicMonitor{m("unknown", false), m("up", true), m("down", false)}},
	}
	for _, c := range cases {
		if got := overallStatus(c.monitors); got != c.want {
			t.Errorf("overallStatus(%+v) = %q, want %q", c.monitors, got, c.want)
		}
	}
}

// The page and its badge are what a visitor sees, so check them end to end.
func TestStatusPageAndBadgeSayUnknownWhenChecksStop(t *testing.T) {
	h := newAPIHarness(t)
	id := staleFixture(t, h, "shop", "http", "up", false, 60, time.Hour)
	if rec := h.do(http.MethodPost, "/api/status-pages", fmt.Sprintf(`{"name":"Acme","slug":"acme","published":true,"monitor_ids":[%d]}`, id)); rec.Code != http.StatusCreated {
		t.Fatalf("create page %d %s", rec.Code, rec.Body.String())
	}
	rec := h.public("/api/status/acme", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var page store.PublicStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Overall != "unknown" || len(page.Monitors) != 1 || page.Monitors[0].Status != "unknown" {
		t.Fatalf("page %+v", page)
	}
	rec = h.public("/api/status/acme/badge.svg", "")
	if !strings.Contains(rec.Body.String(), "status: unknown") {
		t.Fatalf("badge %s", rec.Body.String())
	}
}
