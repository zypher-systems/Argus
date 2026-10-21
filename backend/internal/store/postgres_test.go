package store

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

var testStoreMu sync.Mutex

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("ARGUS_TEST_DB_URL")
	if dsn == "" {
		t.Skip("ARGUS_TEST_DB_URL unset")
	}
	testStoreMu.Lock()
	t.Cleanup(testStoreMu.Unlock)

	ctx := context.Background()
	var st *Store
	var err error
	for i := 0; i < 30; i++ {
		st, err = New(ctx, dsn)
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		t.Fatalf("test db: %v", err)
	}
	t.Cleanup(st.Close)
	_, err = st.Pool.Exec(ctx, `TRUNCATE monitors, probe_heartbeat, alerts, notification_channels,
		users, sessions, maintenance_windows, api_tokens, incidents, incident_updates,
		status_subscribers, status_pages, probe_nodes, probe_node_state, audit_log, alert_outbox,
		realms, organizations RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureDefaultOrg(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestHeartbeatAndStale(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := st.TouchProbeHeartbeat(ctx, now); err != nil {
		t.Fatal(err)
	}
	hb, err := st.GetProbeHeartbeat(ctx)
	if err != nil || hb == nil {
		t.Fatalf("heartbeat: %v %#v", err, hb)
	}
	if ProbeStale(hb, now.Add(10*time.Second), 45*time.Second) {
		t.Fatal("fresh should not be stale")
	}
	if changed, err := st.SetProbeAlerted(ctx, true); err != nil || !changed {
		t.Fatalf("first mark: changed=%v err=%v", changed, err)
	}
	// A second api replica marking the same silence must not alert again.
	if changed, err := st.SetProbeAlerted(ctx, true); err != nil || changed {
		t.Fatalf("second mark: changed=%v err=%v", changed, err)
	}
	hb, _ = st.GetProbeHeartbeat(ctx)
	if !hb.AlertedDown {
		t.Fatal("expected alerted")
	}
}

func TestApplyResultRoundTrip(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	m := Monitor{Name: "icmp-1", Type: "icmp", Target: "127.0.0.1", IntervalSeconds: 60, Retries: 3, Enabled: true}
	if err := st.CreateMonitor(ctx, &m); err != nil {
		t.Fatal(err)
	}
	lat := 12.5
	tr, err := st.ApplyResult(ctx, PingResult{MonitorID: m.ID, TS: time.Now().UTC(), LatencyMs: &lat, Success: true}, ApplyOptions{Retries: 3})
	if err != nil {
		t.Fatal(err)
	}
	if tr.NewStatus != "up" {
		t.Fatalf("status %s", tr.NewStatus)
	}
	got, err := st.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State.Status != "up" || got.State.LastLatencyMs == nil {
		t.Fatalf("state %+v", got.State)
	}
}

func TestMuteAckAndEvidence(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	m := Monitor{Name: "web", Type: "http", Target: "https://example.com", IntervalSeconds: 60, Retries: 1, Enabled: true}
	if err := st.CreateMonitor(ctx, &m); err != nil {
		t.Fatal(err)
	}
	until, err := MuteUntil(time.Now().UTC(), 60)
	if err != nil || until == nil {
		t.Fatalf("mute until: %v %v", until, err)
	}
	if err := st.MuteMonitor(ctx, m.ID, until); err != nil {
		t.Fatal(err)
	}
	muted, acked, err := st.AlertSilence(ctx, m.ID, time.Now().UTC())
	if err != nil || !muted || acked {
		t.Fatalf("silence muted=%v acked=%v err=%v", muted, acked, err)
	}

	now := time.Now().UTC()
	ev := &CheckEvidence{Kind: "http", At: now, Success: false, Error: "status 500", StatusCode: 500, BodySnippet: "nope"}
	_, err = st.ApplyResult(ctx, PingResult{MonitorID: m.ID, TS: now, Success: false, Evidence: ev}, ApplyOptions{Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State.Status != "down" {
		t.Fatalf("status %s", got.State.Status)
	}
	if got.State.LastFailureEvidence == nil || got.State.LastFailureEvidence.StatusCode != 500 {
		t.Fatalf("evidence %+v", got.State.LastFailureEvidence)
	}

	if err := st.AckMonitor(ctx, m.ID, nil); err != nil {
		t.Fatal(err)
	}
	muted, acked, err = st.AlertSilence(ctx, m.ID, time.Now().UTC())
	if err != nil || !muted || !acked {
		t.Fatalf("after ack muted=%v acked=%v err=%v", muted, acked, err)
	}

	_, err = st.ApplyResult(ctx, PingResult{MonitorID: m.ID, TS: now.Add(time.Minute), Success: true, LatencyMs: f64(12)}, ApplyOptions{Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.ApplyResult(ctx, PingResult{MonitorID: m.ID, TS: now.Add(2 * time.Minute), Success: false, Evidence: ev}, ApplyOptions{Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, acked, err = st.AlertSilence(ctx, m.ID, time.Now().UTC())
	if err != nil || acked {
		t.Fatalf("ack should clear on new down, acked=%v err=%v", acked, err)
	}
	got, _ = st.GetMonitor(ctx, m.ID)
	if got.State.LastFailureEvidence == nil {
		t.Fatal("success then failure should keep last failure evidence")
	}
}

func TestCheckNowQueue(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	m := Monitor{Name: "web", Type: "http", Target: "https://example.com", IntervalSeconds: 60, Retries: 1, Enabled: true}
	if err := st.CreateMonitor(ctx, &m); err != nil {
		t.Fatal(err)
	}
	id1, err := st.EnqueueCheckNow(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := st.EnqueueCheckNow(ctx, m.ID)
	if err != nil || id2 != id1 {
		t.Fatalf("expected reuse of pending request %d vs %d err=%v", id1, id2, err)
	}
	req, err := st.ClaimCheckNow(ctx, id1)
	if err != nil || req == nil {
		t.Fatalf("claim: %v %#v", err, req)
	}
	if err := st.CompleteCheckNow(ctx, id1, false, nil, "status 500", &CheckEvidence{Kind: "http", Error: "status 500"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetCheckNow(ctx, id1)
	if err != nil || got.Status != "failed" || got.Evidence == nil {
		t.Fatalf("complete %+v err=%v", got, err)
	}
}

func TestListMonitorsHundreds(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	const n = 250
	for i := 0; i < n; i++ {
		m := Monitor{
			Name:            fmt.Sprintf("m-%03d", i),
			Type:            "icmp",
			Target:          "127.0.0.1",
			IntervalSeconds: 60,
			Retries:         3,
			Enabled:         true,
		}
		if err := st.CreateMonitor(ctx, &m); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	list, err := st.ListMonitors(ctx)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != n {
		t.Fatalf("listed %d", len(list))
	}
	if elapsed > 2*time.Second {
		t.Fatalf("ListMonitors(%d) took %s", n, elapsed)
	}
	t.Logf("ListMonitors(%d) = %s", n, elapsed)
}

func TestPurgeOldEventsAndAlerts(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	m := Monitor{Name: "old", Type: "icmp", Target: "127.0.0.1", IntervalSeconds: 60, Retries: 3, Enabled: true}
	if err := st.CreateMonitor(ctx, &m); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertEvent(ctx, m.ID, "down", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE monitor_events SET ts = now() - interval '40 days'`); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertAlert(ctx, m.ID, "down", "sent", "ops@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE alerts SET created_at = now() - interval '40 days'`); err != nil {
		t.Fatal(err)
	}
	if err := st.PurgeOld(ctx, 14, 90, 365); err != nil {
		t.Fatal(err)
	}
	var events, alerts int
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM monitor_events`).Scan(&events)
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM alerts`).Scan(&alerts)
	if events != 0 || alerts != 0 {
		t.Fatalf("purge left events=%d alerts=%d", events, alerts)
	}
}

func TestInsertAlertSystemNullable(t *testing.T) {
	st := openTestStore(t)
	if err := st.InsertAlert(context.Background(), 0, "probe_down", "sent", "ops", ""); err != nil {
		t.Fatal(err)
	}
}

func TestMonitorStale(t *testing.T) {
	now := time.Now()
	last := now.Add(-3 * time.Minute)
	if !MonitorStale(true, 60, &last, now) {
		t.Fatal("expected stale")
	}
	if MonitorStale(true, 60, &now, now) {
		t.Fatal("fresh check is not stale")
	}
	if MonitorStale(false, 60, &last, now) {
		t.Fatal("paused is not stale")
	}
}

func TestOpenAndResolveIncident(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	m := Monitor{Name: "edge", Type: "icmp", Target: "127.0.0.1", IntervalSeconds: 60, Retries: 3, Enabled: true}
	if err := st.CreateMonitor(ctx, &m); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	inc, opened, err := st.OpenAutoIncident(ctx, m.ID, m.Name, "connection refused", at)
	if err != nil || !opened || inc == nil {
		t.Fatalf("open: %v opened=%v %#v", err, opened, inc)
	}
	again, opened, err := st.OpenAutoIncident(ctx, m.ID, m.Name, "still down", at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if opened || again != nil {
		t.Fatal("second open should no-op")
	}
	resolved, err := st.ResolveAutoIncident(ctx, m.ID, at.Add(2*time.Minute))
	if err != nil || resolved == nil || resolved.ResolvedAt == nil {
		t.Fatalf("resolve: %v %#v", err, resolved)
	}
	pub, err := st.PublicIncidents(ctx, []int64{m.ID}, at.Add(-time.Hour), 10)
	if err != nil || len(pub) != 1 {
		t.Fatalf("public: %v %d", err, len(pub))
	}
	if pub[0].Title != "edge is down" || len(pub[0].Updates) < 2 {
		t.Fatalf("public incident %#v", pub[0])
	}
}

// TestLegacyAccentReadsAsUnset covers installs that saved branding before
// 2.5.0: the old indigo default was stored without being chosen, and must
// not override the current design's accent.
func TestLegacyAccentReadsAsUnset(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, stored := range []string{"#6366f1", "#6366F1"} {
		if err := s.SetBrandingSettings(ctx, BrandingSettings{ProductName: "Acme", AccentColor: stored}); err != nil {
			t.Fatal(err)
		}
		all, err := s.LoadSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if all.Branding.AccentColor != "" {
			t.Fatalf("stored %s should read as unset, got %q", stored, all.Branding.AccentColor)
		}
	}
	if err := s.SetBrandingSettings(ctx, BrandingSettings{ProductName: "Acme", AccentColor: "#aa3300"}); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.LoadSettings(ctx); all.Branding.AccentColor != "#aa3300" {
		t.Fatalf("a chosen accent must be kept, got %q", all.Branding.AccentColor)
	}
	if err := s.SetBrandingSettings(ctx, BrandingSettings{ProductName: "Acme"}); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.LoadSettings(ctx); all.Branding.AccentColor != "" {
		t.Fatalf("no accent should stay empty, got %q", all.Branding.AccentColor)
	}
}

// TestSubscriberUnsubscribeToken covers the queries behind subscriber emails:
// the list admins see, the list incident emails go to, and the token those
// emails carry.
func TestSubscriberUnsubscribeToken(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	m := Monitor{Name: "shop", Type: "http", Target: "https://shop.example.com", IntervalSeconds: 60, Retries: 1, Enabled: true}
	if err := st.CreateMonitor(ctx, &m); err != nil {
		t.Fatal(err)
	}
	page := StatusPage{Name: "Acme", Slug: "acme", Published: true, MonitorIDs: []int64{m.ID}}
	if err := st.CreateStatusPage(ctx, &page); err != nil {
		t.Fatal(err)
	}
	sub, err := st.CreateSubscriber(ctx, page.ID, "email", "customer@example.org", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConfirmSubscriber(ctx, sub.Token); err != nil {
		t.Fatal(err)
	}
	if list, err := st.ListSubscribers(ctx, page.ID); err != nil || len(list) != 1 {
		t.Fatalf("admin list: %v %v", list, err)
	}
	recipients, err := st.ConfirmedSubscribersForMonitor(ctx, m.ID)
	if err != nil || len(recipients) != 1 {
		t.Fatalf("incident recipients: %v %v", recipients, err)
	}
	unsub := recipients[0].UnsubscribeToken
	if len(unsub) < 32 || unsub == sub.Token {
		t.Fatalf("each subscriber needs its own emailable token: %q", unsub)
	}
	if err := st.DeleteSubscriberByToken(ctx, unsub); err != nil {
		t.Fatalf("unsubscribe by the emailed token: %v", err)
	}
	if list, _ := st.ListSubscribers(ctx, page.ID); len(list) != 0 {
		t.Fatal("the subscription should be gone")
	}
	if err := st.DeleteSubscriberByToken(ctx, unsub); err != ErrNotFound {
		t.Fatalf("a used token finds nothing: %v", err)
	}
}
