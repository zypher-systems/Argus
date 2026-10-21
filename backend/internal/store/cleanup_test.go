package store

import (
	"context"
	"slices"
	"testing"
)

func channelIDs(t *testing.T, st *Store, monitorID int64) []int64 {
	t.Helper()
	var ids []int64
	if err := st.Pool.QueryRow(context.Background(), `SELECT channel_ids FROM monitors WHERE id=$1`, monitorID).Scan(&ids); err != nil {
		t.Fatal(err)
	}
	return ids
}

func probeNodes(t *testing.T, st *Store, monitorID int64) ([]int64, int) {
	t.Helper()
	var ids []int64
	var confirm int
	if err := st.Pool.QueryRow(context.Background(), `SELECT probe_node_ids, confirm_locations FROM monitors WHERE id=$1`, monitorID).Scan(&ids, &confirm); err != nil {
		t.Fatal(err)
	}
	return ids, confirm
}

func cleanupMonitor(t *testing.T, st *Store, name string, channels, nodes []int64, confirm int) int64 {
	t.Helper()
	m := Monitor{Name: name, Type: "http", Target: "http://127.0.0.1:1", IntervalSeconds: 60, Retries: 1, Enabled: true,
		ChannelIDs: channels, ProbeNodeIDs: nodes, ConfirmLocations: confirm}
	if err := st.CreateMonitor(context.Background(), &m); err != nil {
		t.Fatal(err)
	}
	return m.ID
}

// A monitor attached only to a channel that is then deleted must not be
// left alerting nobody: it goes back to the default, every channel.
func TestDeletingAChannelDoesNotSilenceItsMonitors(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	a := Channel{Name: "slack", Type: ChannelWebhook, Dest: "https://hooks.example.com/a", Events: DefaultChannelEvents}
	b := Channel{Name: "pager", Type: ChannelWebhook, Dest: "https://hooks.example.com/b", Events: DefaultChannelEvents}
	for _, c := range []*Channel{&a, &b} {
		if err := st.CreateChannel(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	only := cleanupMonitor(t, st, "only", []int64{a.ID}, nil, 1)
	both := cleanupMonitor(t, st, "both", []int64{a.ID, b.ID}, nil, 1)
	none := cleanupMonitor(t, st, "none", []int64{}, nil, 1)

	if err := st.DeleteChannel(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if ids := channelIDs(t, st, only); ids != nil {
		t.Fatalf("a monitor that lost its only channel should use the default (NULL), got %v", ids)
	}
	if ids := channelIDs(t, st, both); !slices.Equal(ids, []int64{b.ID}) {
		t.Fatalf("the other channel stays: %v", ids)
	}
	if ids := channelIDs(t, st, none); ids == nil || len(ids) != 0 {
		t.Fatalf("a monitor set to alert nobody stays that way: %v", ids)
	}
}

// A monitor assigned to a node that is then deleted must not be left with
// nobody checking it.
func TestDeletingAProbeNodeHandsItsMonitorsBack(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	a, err := st.CreateProbeNode(ctx, "Frankfurt", "eu")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateProbeNode(ctx, "Virginia", "us")
	if err != nil {
		t.Fatal(err)
	}
	only := cleanupMonitor(t, st, "only", nil, []int64{a.ID}, 1)
	both := cleanupMonitor(t, st, "both", nil, []int64{a.ID, b.ID}, 2)

	if _, err := st.DeleteProbeNode(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if ids, confirm := probeNodes(t, st, only); len(ids) != 0 || confirm != 1 {
		t.Fatalf("with no node left the monitor is checked from this server: %v confirm %d", ids, confirm)
	}
	if ids, confirm := probeNodes(t, st, both); !slices.Equal(ids, []int64{b.ID}) || confirm != 1 {
		t.Fatalf("one location cannot agree twice: %v confirm %d", ids, confirm)
	}
	local, err := st.ListEnabledMonitorsForNode(ctx, LocalProbeNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(local, func(m Monitor) bool { return m.ID == only }) {
		t.Fatal("this server should be checking the monitor that lost its node")
	}
}

// Migration 025 repairs the rows that deletions before it left behind.
func TestMigration025RepairsDanglingChannelsAndNodes(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	live := Channel{Name: "pager", Type: ChannelWebhook, Dest: "https://hooks.example.com/b", Events: DefaultChannelEvents}
	if err := st.CreateChannel(ctx, &live); err != nil {
		t.Fatal(err)
	}
	node, err := st.CreateProbeNode(ctx, "Virginia", "us")
	if err != nil {
		t.Fatal(err)
	}
	deadOnly := cleanupMonitor(t, st, "dead-only", []int64{9001}, []int64{9002}, 1)
	mixed := cleanupMonitor(t, st, "mixed", []int64{9001, live.ID}, []int64{9002, 0, node.ID}, 3)
	intact := cleanupMonitor(t, st, "intact", []int64{live.ID}, []int64{node.ID}, 1)
	chosenNone := cleanupMonitor(t, st, "none", []int64{}, nil, 1)

	// What two deleted nodes and one live one last reported for a monitor.
	if _, err := st.Pool.Exec(ctx, `INSERT INTO probe_node_state (monitor_id, node_id, status) VALUES ($1, 9002, 'down'), ($1, $2, 'up'), ($1, 0, 'up')`, mixed, node.ID); err != nil {
		t.Fatal(err)
	}

	sql, err := migrationsFS.ReadFile("migrations/025_held_alerts.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	if ids := channelIDs(t, st, deadOnly); ids != nil {
		t.Fatalf("only dead channels: want the default (NULL), got %v", ids)
	}
	if ids, confirm := probeNodes(t, st, deadOnly); len(ids) != 0 || confirm != 1 {
		t.Fatalf("only dead nodes: want this server, got %v confirm %d", ids, confirm)
	}
	if ids := channelIDs(t, st, mixed); !slices.Equal(ids, []int64{live.ID}) {
		t.Fatalf("mixed channels: %v", ids)
	}
	// It needed all three of its locations. Two are left, so it needs two:
	// dropping to one would let a single location take it down.
	if ids, confirm := probeNodes(t, st, mixed); !slices.Equal(ids, []int64{0, node.ID}) || confirm != 2 {
		t.Fatalf("mixed nodes keep this server (0) and the live node, and need both: %v confirm %d", ids, confirm)
	}
	var stale, kept int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE node_id = 9002), count(*) FILTER (WHERE node_id <> 9002)
		FROM probe_node_state WHERE monitor_id=$1`, mixed).Scan(&stale, &kept); err != nil || stale != 0 || kept != 2 {
		t.Fatalf("a deleted node's last report should go and the others stay: stale=%d kept=%d %v", stale, kept, err)
	}
	if ids := channelIDs(t, st, intact); !slices.Equal(ids, []int64{live.ID}) {
		t.Fatalf("a monitor with nothing dead is untouched: %v", ids)
	}
	if ids, _ := probeNodes(t, st, intact); !slices.Equal(ids, []int64{node.ID}) {
		t.Fatalf("a monitor with nothing dead is untouched: %v", ids)
	}
	if ids := channelIDs(t, st, chosenNone); ids == nil {
		t.Fatal("a monitor set to alert nobody must not be switched to every channel")
	}
}

// Migration 027 gives domain warnings to channels made before the event
// existed, and leaves alone the ones that chose their events since.
func TestMigration027GivesOlderChannelsDomainWarnings(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	make := func(name string, events []string, created string) int64 {
		c := Channel{Name: name, Type: ChannelWebhook, Dest: "https://hooks.example.com/" + name, Events: events}
		if err := st.CreateChannel(ctx, &c); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Pool.Exec(ctx, `UPDATE notification_channels SET created_at=$2::timestamptz WHERE id=$1`, c.ID, created); err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	old := make("old", []string{"down", "recovery", "cert"}, "2026-09-10T00:00:00Z")
	oldNoCert := make("old-no-cert", []string{"down", "recovery"}, "2026-09-10T00:00:00Z")
	recent := make("recent", []string{"down", "recovery", "cert"}, "2026-10-01T00:00:00Z")
	has := []string{"down", "cert", "domain"}
	already := make("already", has, "2026-09-10T00:00:00Z")

	sql, err := migrationsFS.ReadFile("migrations/027_domain_event.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // applying it twice changes nothing more
		if _, err := st.Pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	events := func(id int64) []string {
		c, err := st.GetChannel(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return c.Events
	}
	if e := events(old); !slices.Contains(e, "domain") {
		t.Fatalf("a channel from before 2.7.0 that takes cert warnings: %v", e)
	}
	if e := events(oldNoCert); slices.Contains(e, "domain") {
		t.Fatalf("a channel that never took expiry warnings: %v", e)
	}
	if e := events(recent); slices.Contains(e, "domain") {
		t.Fatalf("a channel made since, which chose to leave domain out: %v", e)
	}
	if e := events(already); len(e) != len(has) {
		t.Fatalf("a channel that already has it: %v", e)
	}
}

// Migration 028 turns "an alert is being held" into "what were they last
// told", without sending anyone an alert for an outage they know about.
func TestMigration028RecordsWhatRecipientsWereTold(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := st.Pool.Exec(ctx, `ALTER TABLE monitor_state ADD COLUMN IF NOT EXISTS held_alert TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
	state := func(name, status, held string) int64 {
		id := cleanupMonitor(t, st, name, nil, nil, 1)
		if _, err := st.Pool.Exec(ctx, `UPDATE monitor_state SET status=$2, held_alert=$3, told_down=FALSE,
			tls_warned_expiry = now() + interval '5 days', tls_warned_expiries='{}' WHERE monitor_id=$1`, id, status, held); err != nil {
			t.Fatal(err)
		}
		return id
	}
	cases := []struct {
		name, status, held string
		told               bool
	}{
		{"down and alerted", "down", "", true},
		{"down, alert was held", "down", "down", false},
		{"up", "up", "", false},
		{"up, recovery was held", "up", "recovery", true},
	}
	ids := map[string]int64{}
	for _, c := range cases {
		ids[c.name] = state(c.name, c.status, c.held)
	}
	sql, err := migrationsFS.ReadFile("migrations/028_alert_ordering.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		var told bool
		var warned int
		if err := st.Pool.QueryRow(ctx, `SELECT told_down, cardinality(tls_warned_expiries) FROM monitor_state WHERE monitor_id=$1`, ids[c.name]).Scan(&told, &warned); err != nil {
			t.Fatal(err)
		}
		if told != c.told {
			t.Errorf("%s: told_down=%v, want %v", c.name, told, c.told)
		}
		if warned != 1 {
			t.Errorf("%s: the certificate date already warned about should carry over, got %d", c.name, warned)
		}
	}
	// Only the two whose alerts were held are owed anything.
	owed, err := st.OwedAlerts(ctx)
	if err != nil || len(owed) != 2 {
		t.Fatalf("owed after the migration: %+v %v", owed, err)
	}
}

// Migration 029 gives alerts queued before streams existed the stream they
// belong to, so they are ordered and replaced like any other.
func TestMigration029GivesQueuedAlertsTheirStream(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	queued := func(monitorID int64, kind, payload string) int64 {
		var id int64
		if err := st.Pool.QueryRow(ctx, `INSERT INTO alert_outbox (monitor_id, type, dest, payload) VALUES ($1,$2,'pager',$3) RETURNING id`,
			monitorID, kind, payload).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	cases := []struct {
		monitor       int64
		kind, payload string
		stream        string
		resolves      bool
	}{
		{7, "down", `{}`, "argus-monitor-7", false},
		{7, "recovery", `{}`, "argus-monitor-7", true},
		{7, "resend", `{}`, "argus-monitor-7", false},
		{7, "slow", `{}`, "argus-monitor-7-slow", false},
		{7, "recovery", `{"of":"slow"}`, "argus-monitor-7-slow", true},
		{7, "cert", `{}`, "argus-monitor-7-cert", false},
		{0, "probe_down", `{}`, "argus-probe", false},
		{0, "probe_up", `{"key":"argus-probe-node-3"}`, "argus-probe-node-3", true},
		{0, "test", `{}`, "", false},
	}
	ids := make([]int64, len(cases))
	for i, c := range cases {
		ids[i] = queued(c.monitor, c.kind, c.payload)
	}
	sql, err := migrationsFS.ReadFile("migrations/029_alert_edges.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		var stream string
		var resolves bool
		if err := st.Pool.QueryRow(ctx, `SELECT stream, resolves FROM alert_outbox WHERE id=$1`, ids[i]).Scan(&stream, &resolves); err != nil {
			t.Fatal(err)
		}
		if stream != c.stream || resolves != c.resolves {
			t.Errorf("%s %s: stream %q resolves %v, want %q %v", c.kind, c.payload, stream, resolves, c.stream, c.resolves)
		}
	}
}

// Resuming marks a monitor as not yet checked, which holds back anything
// owed for it until it has been. Only a monitor that was actually paused,
// and only this organization's: a selection on the dashboard includes
// monitors that are already running, and an id can be anybody's.
func TestResumeMarksOnlyWhatItSwitchesBackOn(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	other, err := st.CreateOrg(ctx, "Other", "other", PlanCommunity)
	if err != nil {
		t.Fatal(err)
	}
	mine := WithOrg(ctx, 1)
	create := func(ctx context.Context, name string, enabled bool) int64 {
		m := Monitor{Name: name, Type: "http", Target: "http://127.0.0.1:1", IntervalSeconds: 60, Retries: 1, Enabled: enabled, ConfirmLocations: 1}
		if err := st.CreateMonitor(ctx, &m); err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	paused := create(mine, "paused", false)
	running := create(mine, "running", true)
	theirsPaused := create(WithOrg(ctx, other.ID), "theirs-paused", false)
	theirsRunning := create(WithOrg(ctx, other.ID), "theirs-running", true)
	resumed := func(id int64) bool {
		var set bool
		if err := st.Pool.QueryRow(ctx, `SELECT resumed_at IS NOT NULL FROM monitor_state WHERE monitor_id=$1`, id).Scan(&set); err != nil {
			t.Fatal(err)
		}
		return set
	}
	enabled := func(id int64) bool {
		var on bool
		if err := st.Pool.QueryRow(ctx, `SELECT enabled FROM monitors WHERE id=$1`, id).Scan(&on); err != nil {
			t.Fatal(err)
		}
		return on
	}
	events := func(id int64) int {
		var n int
		if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM monitor_events WHERE monitor_id=$1 AND type IN ('paused','resumed')`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// The whole selection, as the dashboard sends it, plus ids that are not ours.
	n, err := st.BulkSetEnabled(mine, []int64{paused, running, theirsPaused, theirsRunning}, true)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("two of the four are this organization's: got %d", n)
	}
	if !enabled(paused) || !resumed(paused) || events(paused) != 1 {
		t.Fatalf("the paused monitor: enabled=%v marked=%v events=%d", enabled(paused), resumed(paused), events(paused))
	}
	if resumed(running) || events(running) != 0 {
		t.Fatalf("a monitor that was already running must not be held back or logged: marked=%v events=%d", resumed(running), events(running))
	}
	if enabled(theirsPaused) || resumed(theirsPaused) || events(theirsPaused) != 0 {
		t.Fatalf("another organization's paused monitor was touched: enabled=%v marked=%v events=%d", enabled(theirsPaused), resumed(theirsPaused), events(theirsPaused))
	}
	if resumed(theirsRunning) || events(theirsRunning) != 0 {
		t.Fatalf("another organization's running monitor was touched: marked=%v events=%d", resumed(theirsRunning), events(theirsRunning))
	}

	// One at a time: the same rules, and somebody else's is not found.
	if err := st.SetMonitorEnabled(mine, running, true); err != nil {
		t.Fatal(err)
	}
	if resumed(running) || events(running) != 0 {
		t.Fatal("resuming a monitor that was already running marked or logged it")
	}
	if err := st.SetMonitorEnabled(mine, theirsPaused, true); err != ErrNotFound {
		t.Fatalf("another organization's monitor: %v", err)
	}
	if enabled(theirsPaused) || resumed(theirsPaused) {
		t.Fatal("another organization's monitor was resumed")
	}
	// Pausing logs it and marks nothing.
	if err := st.SetMonitorEnabled(mine, running, false); err != nil {
		t.Fatal(err)
	}
	if enabled(running) || resumed(running) || events(running) != 1 {
		t.Fatalf("pausing: enabled=%v marked=%v events=%d", enabled(running), resumed(running), events(running))
	}
}
