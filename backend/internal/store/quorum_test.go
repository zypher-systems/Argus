package store

import (
	"context"
	"testing"
	"time"
)

// quorumFixture is a monitor checked from this server and two probe nodes.
type quorumFixture struct {
	t    *testing.T
	st   *Store
	m    Monitor
	eu   int64
	us   int64
	now  time.Time
	opts func(node int64, errText string) ApplyOptions
}

func newQuorumFixture(t *testing.T, retries, confirm int) *quorumFixture {
	t.Helper()
	st := openTestStore(t)
	ctx := context.Background()
	eu, err := st.CreateProbeNode(ctx, "Frankfurt", "eu-central")
	if err != nil {
		t.Fatal(err)
	}
	us, err := st.CreateProbeNode(ctx, "Virginia", "us-east")
	if err != nil {
		t.Fatal(err)
	}
	m := Monitor{Name: "shop", Type: "http", Target: "https://shop.example.com", IntervalSeconds: 60,
		Retries: retries, Enabled: true, ProbeNodeIDs: []int64{LocalProbeNodeID, eu.ID, us.ID}, ConfirmLocations: confirm}
	if err := st.CreateMonitor(ctx, &m); err != nil {
		t.Fatal(err)
	}
	f := &quorumFixture{t: t, st: st, m: m, eu: eu.ID, us: us.ID, now: time.Now().UTC()}
	f.opts = func(node int64, errText string) ApplyOptions {
		return ApplyOptions{NodeID: node, Retries: retries, IntervalSeconds: 60,
			Locations: Locations(m), Confirm: confirm, ErrorText: errText}
	}
	return f
}

// alive records that node's probe has been heard from now.
func (f *quorumFixture) alive(node int64) {
	f.t.Helper()
	ctx := context.Background()
	var err error
	if node == LocalProbeNodeID {
		err = f.st.TouchProbeHeartbeat(ctx, f.now)
	} else {
		_, err = f.st.Pool.Exec(ctx, `UPDATE probe_nodes SET last_seen=$2 WHERE id=$1`, node, f.now)
	}
	if err != nil {
		f.t.Fatal(err)
	}
}

// check records one result from node, a few seconds after the last.
func (f *quorumFixture) check(node int64, ok bool) StateTransition {
	f.t.Helper()
	f.now = f.now.Add(5 * time.Second)
	errText := ""
	if !ok {
		errText = "connection refused"
	}
	// A probe that reports is alive.
	f.alive(node)
	tr, err := f.st.ApplyResult(context.Background(), PingResult{MonitorID: f.m.ID, TS: f.now, Success: ok}, f.opts(node, errText))
	if err != nil {
		f.t.Fatal(err)
	}
	return tr
}

func TestOneRegionFailingDoesNotTakeTheMonitorDown(t *testing.T) {
	f := newQuorumFixture(t, 2, 2)
	for _, n := range []int64{LocalProbeNodeID, f.eu, f.us} {
		f.check(n, true)
	}
	// Frankfurt fails repeatedly; the others are fine.
	for i := 0; i < 5; i++ {
		if tr := f.check(f.eu, false); tr.NewStatus != "up" {
			t.Fatalf("one of three locations down must not take the monitor down (attempt %d): %+v", i, tr)
		}
		f.check(LocalProbeNodeID, true)
		f.check(f.us, true)
	}
}

func TestTwoRegionsAgreeingTakeItDownAndNameThem(t *testing.T) {
	f := newQuorumFixture(t, 2, 2)
	for _, n := range []int64{LocalProbeNodeID, f.eu, f.us} {
		f.check(n, true)
	}
	f.check(f.eu, false)
	f.check(f.eu, false) // Frankfurt confirmed down (2 retries)
	f.check(f.us, false)
	tr := f.check(f.us, false) // Virginia confirmed down too
	if !tr.Changed || tr.NewStatus != "down" {
		t.Fatalf("two of three confirmed down should take the monitor down: %+v", tr)
	}
	if len(tr.DownLocations) != 2 || tr.Reporting != 3 || tr.Needed != 2 || tr.Locations != 3 {
		t.Fatalf("transition should describe the vote: %+v", tr)
	}
	names := map[string]bool{}
	for _, n := range tr.DownLocations {
		names[n] = true
	}
	if !names["Frankfurt"] || !names["Virginia"] {
		t.Fatalf("down locations should be named: %v", tr.DownLocations)
	}

	// One recovers: below the threshold again, and a location reports up.
	if tr := f.check(f.eu, true); !tr.Changed || tr.NewStatus != "up" {
		t.Fatalf("dropping below two down should recover: %+v", tr)
	}
}

func TestEachRegionCountsItsOwnRetries(t *testing.T) {
	// Three retries each. Interleaved single failures from two places must
	// not add up to one location's three.
	f := newQuorumFixture(t, 3, 1)
	for _, n := range []int64{LocalProbeNodeID, f.eu, f.us} {
		f.check(n, true)
	}
	f.check(f.eu, false)
	f.check(f.us, false)
	if tr := f.check(LocalProbeNodeID, false); tr.NewStatus != "up" {
		t.Fatalf("three failures from three places is not three from one: %+v", tr)
	}
	f.check(f.eu, false)
	if tr := f.check(f.eu, false); tr.NewStatus != "down" {
		t.Fatalf("with confirm 1, one location's third failure takes it down: %+v", tr)
	}
}

func TestSilentRegionStopsVoting(t *testing.T) {
	f := newQuorumFixture(t, 1, 2)
	for _, n := range []int64{LocalProbeNodeID, f.eu, f.us} {
		f.check(n, true)
	}
	// Virginia goes quiet (its node dies) while Frankfurt sees the outage.
	f.check(f.eu, false)
	// Well past the stale window for a 60s interval (2*60+30s).
	f.now = f.now.Add(3 * time.Minute)
	f.check(f.eu, false)
	tr := f.check(LocalProbeNodeID, false)
	if tr.Reporting != 2 {
		t.Fatalf("the silent location should not count as reporting: %+v", tr)
	}
	if tr.NewStatus != "down" {
		t.Fatalf("both reporting locations agree, so it is down: %+v", tr)
	}
}

func TestQuorumLargerThanReportingIsCapped(t *testing.T) {
	// Confirm 3, but only one location has ever reported: a dead fleet must
	// not make alerting impossible.
	f := newQuorumFixture(t, 1, 3)
	if tr := f.check(LocalProbeNodeID, false); tr.NewStatus != "down" || tr.Needed != 1 {
		t.Fatalf("with one reporting location it decides alone: %+v", tr)
	}
}

func TestRecoveryNeedsALocationThatIsUp(t *testing.T) {
	// A location nobody has heard from before is pending, and a pending
	// location is not evidence of recovery.
	f := newQuorumFixture(t, 1, 1)
	f.check(LocalProbeNodeID, false)
	if tr := f.check(f.eu, false); tr.NewStatus != "down" {
		t.Fatalf("setup: %+v", tr)
	}
	f.check(LocalProbeNodeID, true)
	// Frankfurt is still down; this server is up; confirm 1 keeps it down.
	if tr := f.check(f.eu, false); tr.NewStatus != "down" {
		t.Fatalf("one location still down with confirm 1 keeps it down: %+v", tr)
	}
	if tr := f.check(f.eu, true); tr.NewStatus != "up" {
		t.Fatalf("all locations up recovers it: %+v", tr)
	}
}

func TestUnassignedLocationsDoNotVote(t *testing.T) {
	f := newQuorumFixture(t, 1, 1)
	ctx := context.Background()
	f.check(f.us, false)
	// Take Virginia off the monitor.
	f.m.ProbeNodeIDs = []int64{LocalProbeNodeID, f.eu}
	if err := f.st.UpdateMonitor(ctx, &f.m); err != nil {
		t.Fatal(err)
	}
	regions, err := f.st.ListMonitorRegions(ctx, f.m.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range regions {
		if r.NodeID == f.us {
			t.Fatal("a removed location should no longer be listed")
		}
	}
	f.opts = func(node int64, errText string) ApplyOptions {
		return ApplyOptions{NodeID: node, Retries: 1, IntervalSeconds: 60, Locations: Locations(f.m), Confirm: 1, ErrorText: errText}
	}
	if tr := f.check(LocalProbeNodeID, true); tr.NewStatus != "up" || tr.Reporting != 1 {
		t.Fatalf("only assigned locations vote: %+v", tr)
	}
}

func TestLocationsDefaultsAndLocalOnlyTypes(t *testing.T) {
	if got := Locations(Monitor{Type: "http"}); len(got) != 1 || got[0] != LocalProbeNodeID {
		t.Fatalf("no nodes means this server: %v", got)
	}
	if got := Locations(Monitor{Type: "http", ProbeNodeIDs: []int64{3, 3, 0}}); len(got) != 2 {
		t.Fatalf("duplicates collapse: %v", got)
	}
	for _, kind := range []string{"push", "group"} {
		if got := Locations(Monitor{Type: kind, ProbeNodeIDs: []int64{3}}); len(got) != 1 || got[0] != LocalProbeNodeID {
			t.Fatalf("%s is only ever local: %v", kind, got)
		}
	}
}

// TestUpgradeSeedsLocationsFromConfirmedState covers the 021 migration:
// before 2.9.0 a location row held its last raw result, so a single failure
// could leave "down" there while the monitor was up.
func TestUpgradeSeedsLocationsFromConfirmedState(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	m := Monitor{Name: "old", Type: "http", Target: "https://example.com", IntervalSeconds: 60, Retries: 3, Enabled: true}
	if err := st.CreateMonitor(ctx, &m); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := st.Pool.Exec(ctx, `UPDATE monitor_state SET status='up', consecutive_failures=0 WHERE monitor_id=$1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO probe_node_state (monitor_id, node_id, status, last_check_at)
		VALUES ($1, 0, 'down', $2)`, m.ID, now); err != nil {
		t.Fatal(err)
	}
	sql, err := migrationsFS.ReadFile("migrations/021_location_quorum.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	tr, err := st.ApplyResult(ctx, PingResult{MonitorID: m.ID, TS: now.Add(time.Second), Success: false},
		ApplyOptions{Retries: 3, IntervalSeconds: 60, Locations: Locations(m), Confirm: 1})
	if err != nil {
		t.Fatal(err)
	}
	if tr.NewStatus != "up" {
		t.Fatalf("one failure after the upgrade must not be an outage: %+v", tr)
	}
}

// TestFirstFailingLocationCannotDecideAlone is the start-up case: a new
// monitor whose first result is a failure from one region, while the other
// region's probe is alive but has not reached it yet.
func TestFirstFailingLocationCannotDecideAlone(t *testing.T) {
	f := newQuorumFixture(t, 1, 2)
	f.alive(f.us)
	f.alive(LocalProbeNodeID)
	if tr := f.check(f.eu, false); tr.NewStatus == "down" || tr.Needed != 2 {
		t.Fatalf("live probes that have not checked yet still count: %+v", tr)
	}
	if tr := f.check(f.us, false); tr.NewStatus != "down" {
		t.Fatalf("the second location agreeing confirms it: %+v", tr)
	}
}
