package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"argus/internal/probe"
	"argus/internal/store"
)

// alertHook is a webhook channel that keeps the alerts sent to it.
func alertHook(t *testing.T, st *store.Store) (next func(what string) (event, text string), none func(what string)) {
	t.Helper()
	type alert struct{ Event, Message, Detail string }
	got := make(chan alert, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var a alert
		_ = json.Unmarshal(raw, &a)
		got <- a
	}))
	t.Cleanup(srv.Close)
	ch := store.Channel{Name: "ops", Type: store.ChannelWebhook, Dest: srv.URL, Events: store.DefaultChannelEvents}
	if err := st.CreateChannel(context.Background(), &ch); err != nil {
		t.Fatal(err)
	}
	next = func(what string) (string, string) {
		t.Helper()
		select {
		case a := <-got:
			return a.Event, a.Message + " | " + a.Detail
		case <-time.After(5 * time.Second):
			t.Fatalf("no alert arrived: %s", what)
			return "", ""
		}
	}
	none = func(what string) {
		t.Helper()
		select {
		case a := <-got:
			t.Fatalf("%s: a %q alert was sent (%s)", what, a.Event, a.Message)
		case <-time.After(400 * time.Millisecond):
		}
	}
	return next, none
}

// Monitors checked only from a remote probe node get no results when that
// node stops, and keep showing their last status. The node going quiet is
// the only sign, so it is alerted like the local probe going quiet.
func TestSilentProbeNodeIsAlerted(t *testing.T) {
	h := newAPIHarness(t)
	ctx := context.Background()
	next, none := alertHook(t, h.s.store)
	node := func(name string) store.ProbeNode {
		rec := h.do(http.MethodPost, "/api/probe-nodes", `{"name":"`+name+`","region":"eu"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create node %d %s", rec.Code, rec.Body.String())
		}
		var n store.ProbeNode
		_ = json.Unmarshal(rec.Body.Bytes(), &n)
		return n
	}
	busy, idle := node("Frankfurt"), node("Spare")
	rec := h.do(http.MethodPost, "/api/monitors", fmt.Sprintf(`{"name":"shop","type":"http","target":"http://127.0.0.1:1","interval_seconds":60,"enabled":true,"probe_node_ids":[%d]}`, busy.ID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create monitor %d %s", rec.Code, rec.Body.String())
	}
	seen := func(id int64, ago time.Duration) {
		if _, err := h.s.store.Pool.Exec(ctx, `UPDATE probe_nodes SET last_seen = now() - make_interval(secs => $2) WHERE id=$1`, id, ago.Seconds()); err != nil {
			t.Fatal(err)
		}
	}
	now := func() time.Time { return time.Now() }

	seen(busy.ID, 10*time.Second)
	seen(idle.ID, time.Hour) // quiet, but nothing is assigned to it
	h.s.checkProbeNodes(ctx, now())
	none("a node that is reporting, and one with no monitors")

	seen(busy.ID, 10*time.Minute)
	h.s.checkProbeNodes(ctx, now())
	event, text := next("the node going quiet")
	if event != "probe_down" || !strings.Contains(text, "Frankfurt") || !strings.Contains(text, "1 monitor") {
		t.Fatalf("want a probe_down naming the node and its monitors, got %q %q", event, text)
	}
	h.s.checkProbeNodes(ctx, now())
	none("a second look at the same silence")

	seen(busy.ID, 5*time.Second)
	h.s.checkProbeNodes(ctx, now())
	if event, text := next("the node returning"); event != "probe_up" || !strings.Contains(text, "Frankfurt") {
		t.Fatalf("want a probe_up naming the node, got %q %q", event, text)
	}
}

// A heartbeat that says its job failed is recorded as such, so the probe's
// own look at the monitor agrees with it.
func TestPushRecordsWhatTheHeartbeatSaid(t *testing.T) {
	h := newAPIHarness(t)
	rec := h.do(http.MethodPost, "/api/monitors", `{"name":"backup","type":"push","target":"backup","interval_seconds":60,"enabled":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var m store.Monitor
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	full, err := h.s.store.GetMonitor(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	said := func(query string, wantOK bool) {
		t.Helper()
		if rec := h.public("/api/push/"+full.Config.PushToken+query, ""); rec.Code != 200 {
			t.Fatalf("push%s: %d %s", query, rec.Code, rec.Body.String())
		}
		at, ok, err := h.s.store.LastPush(context.Background(), m.ID)
		if err != nil || at == nil || ok != wantOK {
			t.Fatalf("push%s recorded at=%v ok=%v err=%v, want ok=%v", query, at, ok, err, wantOK)
		}
	}
	said("", true)
	said("?status=down&msg=disk+full", false)
	said("?status=up", true)
}

// A node that was reported silent and then deleted will never report back.
// What its silence opened on a pager is closed.
func TestProbeNodeAlertIsClosedWhenTheNodeStopsMattering(t *testing.T) {
	h := newAPIHarness(t)
	ctx := context.Background()
	next, none := alertHook(t, h.s.store)
	silent := func(name string) store.ProbeNode {
		rec := h.do(http.MethodPost, "/api/probe-nodes", `{"name":"`+name+`","region":"eu"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create node %d %s", rec.Code, rec.Body.String())
		}
		var n store.ProbeNode
		_ = json.Unmarshal(rec.Body.Bytes(), &n)
		rec = h.do(http.MethodPost, "/api/monitors", fmt.Sprintf(`{"name":"on-%s","type":"http","target":"http://127.0.0.1:1","interval_seconds":60,"enabled":true,"probe_node_ids":[%d]}`, name, n.ID))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create monitor %d %s", rec.Code, rec.Body.String())
		}
		if _, err := h.s.store.Pool.Exec(ctx, `UPDATE probe_nodes SET last_seen = now() - interval '10 minutes' WHERE id=$1`, n.ID); err != nil {
			t.Fatal(err)
		}
		return n
	}
	gone, kept := silent("Frankfurt"), silent("Paris")
	h.s.checkProbeNodes(ctx, time.Now())
	for range 2 {
		if event, _ := next("each node going quiet"); event != "probe_down" {
			t.Fatalf("got %q", event)
		}
	}

	if rec := h.do(http.MethodDelete, fmt.Sprintf("/api/probe-nodes/%d", gone.ID), ""); rec.Code != 200 {
		t.Fatalf("delete %d %s", rec.Code, rec.Body.String())
	}
	if event, text := next("the deleted node's alert being closed"); event != "probe_up" || !strings.Contains(text, "Frankfurt") || !strings.Contains(text, "removed") {
		t.Fatalf("want a probe_up saying Frankfurt was removed, got %q %q", event, text)
	}
	h.s.checkProbeNodes(ctx, time.Now())
	none("a second look leaves the remaining node alone")
	_ = kept
}

// A monitor deleted while its recipients have been told it is down will
// never be reported back up. Deleting it closes what was opened for it.
func TestDeletingADownMonitorClosesItsAlert(t *testing.T) {
	h := newAPIHarness(t)
	ctx := context.Background()
	next, none := alertHook(t, h.s.store)
	create := func(name string) store.Monitor {
		rec := h.do(http.MethodPost, "/api/monitors", `{"name":"`+name+`","type":"http","target":"http://127.0.0.1:1","interval_seconds":60,"retries":1,"enabled":true}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %d %s", rec.Code, rec.Body.String())
		}
		var m store.Monitor
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		full, err := h.s.store.GetMonitor(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		return full.Monitor
	}
	down, fine := create("down"), create("fine")
	h.s.applier.Apply(ctx, down, store.LocalProbeNodeID, probe.CheckOutcome{}, errors.New("refused"))
	if event, _ := next("the outage"); event != "down" {
		t.Fatalf("got %q", event)
	}
	h.s.applier.Apply(ctx, fine, store.LocalProbeNodeID, probe.CheckOutcome{LatencyMs: 5}, nil)

	if rec := h.do(http.MethodDelete, fmt.Sprintf("/api/monitors/%d", fine.ID), ""); rec.Code != 200 {
		t.Fatalf("delete %d", rec.Code)
	}
	none("deleting a monitor that is up")
	if rec := h.do(http.MethodDelete, fmt.Sprintf("/api/monitors/%d", down.ID), ""); rec.Code != 200 {
		t.Fatalf("delete %d", rec.Code)
	}
	if event, text := next("the deleted monitor's alert being closed"); event != "recovery" || !strings.Contains(text, "deleted") {
		t.Fatalf("want a recovery saying it was deleted, got %q %q", event, text)
	}
}
