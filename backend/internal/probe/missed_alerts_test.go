package probe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"argus/internal/alerting"
	"argus/internal/store"
)

func alertingPayload(m store.Monitor, kind string) alerting.Payload {
	return alerting.Payload{Monitor: m, Type: kind, At: time.Now().UTC()}
}

// hookSink is a webhook channel that keeps the alerts sent to it.
type hookSink struct {
	t       *testing.T
	Channel store.Channel
	got     chan string
	mu      sync.Mutex
	details map[string]string
}

func newHookSink(t *testing.T, st *store.Store, name string) *hookSink {
	t.Helper()
	h := &hookSink{t: t, got: make(chan string, 32), details: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct{ Event, Message, Detail string }
		_ = json.Unmarshal(raw, &body)
		h.mu.Lock()
		h.details[body.Event] = body.Message + " | " + body.Detail
		h.mu.Unlock()
		h.got <- body.Event
	}))
	t.Cleanup(srv.Close)
	h.Channel = store.Channel{Name: name, Type: store.ChannelWebhook, Dest: srv.URL, Events: store.DefaultChannelEvents}
	if err := st.CreateChannel(context.Background(), &h.Channel); err != nil {
		t.Fatal(err)
	}
	return h
}

// next is the next alert this channel was sent.
func (h *hookSink) next(what string) string {
	h.t.Helper()
	select {
	case ev := <-h.got:
		return ev
	case <-time.After(5 * time.Second):
		h.t.Fatalf("no alert arrived: %s", what)
		return ""
	}
}

// none fails the test if an alert arrives within a moment.
func (h *hookSink) none(what string) {
	h.t.Helper()
	select {
	case ev := <-h.got:
		h.t.Fatalf("%s: a %q alert was sent", what, ev)
	case <-time.After(400 * time.Millisecond):
	}
}

func (h *hookSink) detail(event string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.details[event]
}

// testEngine is an engine on a fresh set of monitor and alert rows.
func testEngine(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	dsn := os.Getenv("ARGUS_TEST_DB_URL")
	if dsn == "" {
		t.Skip("ARGUS_TEST_DB_URL unset")
	}
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Pool.Exec(ctx, `TRUNCATE monitors, probe_nodes, probe_node_state, alerts, alert_outbox RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureDefaultOrg(ctx); err != nil {
		t.Fatal(err)
	}
	return NewEngine(st, alerting.NewDispatcher(st, alerting.NewMailer(st))), st
}

// alertEngine is testEngine with the tables an alert touches emptied.
func alertEngine(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	e, st := testEngine(t)
	if _, err := st.Pool.Exec(context.Background(), `TRUNCATE notification_channels, maintenance_windows, incidents, incident_updates RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	return e, st
}

func newMonitor(t *testing.T, st *store.Store, m store.Monitor) store.Monitor {
	t.Helper()
	if m.Type == "" {
		m.Type = "http"
	}
	if m.Target == "" {
		m.Target = "http://127.0.0.1:1"
	}
	if m.IntervalSeconds == 0 {
		m.IntervalSeconds = 60
	}
	if m.Retries == 0 {
		m.Retries = 1
	}
	m.Enabled, m.ConfirmLocations = true, 1
	if err := st.CreateMonitor(context.Background(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func (e *Engine) fail(m store.Monitor) {
	e.applier.Apply(context.Background(), m, store.LocalProbeNodeID, CheckOutcome{}, errors.New("connection refused"))
}

func (e *Engine) pass(m store.Monitor) {
	e.applier.Apply(context.Background(), m, store.LocalProbeNodeID, CheckOutcome{LatencyMs: 12}, nil)
}

func openIncidents(t *testing.T, st *store.Store, monitorID int64) int {
	t.Helper()
	var n int
	if err := st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM incidents WHERE monitor_id=$1 AND resolved_at IS NULL`, monitorID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func window(t *testing.T, st *store.Store, monitorID int64) *store.MaintenanceWindow {
	t.Helper()
	w := &store.MaintenanceWindow{MonitorID: &monitorID, StartsAt: time.Now().UTC().Add(-time.Minute), EndsAt: time.Now().UTC().Add(time.Hour)}
	if err := st.CreateMaintenanceWindow(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	return w
}

func endWindow(t *testing.T, st *store.Store, w *store.MaintenanceWindow) {
	t.Helper()
	if _, err := st.Pool.Exec(context.Background(), `UPDATE maintenance_windows SET ends_at = now() - interval '1 second' WHERE id=$1`, w.ID); err != nil {
		t.Fatal(err)
	}
}

// A monitor that goes down during a maintenance window and is still down
// when the window ends is a real outage nobody has been told about. It is
// alerted then, and its incident opened.
func TestOutageDuringMaintenanceIsAlertedWhenItEnds(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop"})
	w := window(t, st, m.ID)

	e.fail(m)
	sink.none("during the window")
	if n := openIncidents(t, st, m.ID); n != 0 {
		t.Fatalf("a maintenance window keeps the incident closed: %d open", n)
	}
	e.owedSweep(ctx)
	sink.none("the sweep, while the window is still open")

	endWindow(t, st, w)
	e.owedSweep(ctx)
	if ev := sink.next("the outage, once the window ended"); ev != "down" {
		t.Fatalf("want the down alert, got %q", ev)
	}
	if !strings.Contains(sink.detail("down"), "delayed") {
		t.Fatalf("the alert should say it was delayed: %q", sink.detail("down"))
	}
	if n := openIncidents(t, st, m.ID); n != 1 {
		t.Fatalf("the incident should open with the alert: %d open", n)
	}
	e.owedSweep(ctx)
	sink.none("a second sweep")

	e.pass(m)
	if ev := sink.next("the recovery"); ev != "recovery" {
		t.Fatalf("want the recovery, got %q", ev)
	}
}

// An outage that begins and ends inside the window was never news.
func TestOutageWhollyInsideMaintenanceIsNotAlerted(t *testing.T) {
	e, st := alertEngine(t)
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop"})
	w := window(t, st, m.ID)
	e.fail(m)
	e.pass(m)
	endWindow(t, st, w)
	e.owedSweep(context.Background())
	sink.none("after a window that contained the whole outage")
}

// When the outage was alerted and the monitor recovers while it is muted,
// the recovery is still owed: it is what closes the incident the outage
// opened in PagerDuty or Opsgenie.
func TestRecoveryDuringMuteIsSentWhenTheMuteEnds(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop"})
	e.fail(m)
	if ev := sink.next("the outage"); ev != "down" {
		t.Fatalf("want down, got %q", ev)
	}
	until := time.Now().UTC().Add(time.Hour)
	if err := st.MuteMonitor(ctx, m.ID, &until); err != nil {
		t.Fatal(err)
	}
	e.pass(m)
	e.owedSweep(ctx)
	sink.none("while muted")

	past := time.Now().UTC().Add(-time.Second)
	if err := st.MuteMonitor(ctx, m.ID, &past); err != nil {
		t.Fatal(err)
	}
	e.owedSweep(ctx)
	if ev := sink.next("the recovery, once unmuted"); ev != "recovery" {
		t.Fatalf("want recovery, got %q", ev)
	}
	e.owedSweep(ctx)
	sink.none("a second sweep")
}

// A child that goes down while its parent is down is held, and alerted if
// it is still down when the parent comes back.
func TestOutageBehindADownParentIsAlertedWhenTheParentRecovers(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	parent := newMonitor(t, st, store.Monitor{Name: "router"})
	child := newMonitor(t, st, store.Monitor{Name: "shop", DependsOn: &parent.ID})
	e.fail(parent)
	if ev := sink.next("the parent's outage"); ev != "down" {
		t.Fatalf("want down, got %q", ev)
	}
	e.fail(child)
	e.owedSweep(ctx)
	sink.none("the child, while its parent is down")

	e.pass(parent)
	if ev := sink.next("the parent's recovery"); ev != "recovery" {
		t.Fatalf("want recovery, got %q", ev)
	}
	e.owedSweep(ctx)
	if ev := sink.next("the child, still down"); ev != "down" || !strings.Contains(sink.detail("down"), "shop") {
		t.Fatalf("want the child's down alert, got %q %q", ev, sink.detail("down"))
	}
}

// A heartbeat can say its job failed. That is a failure even though the
// heartbeat arrived on time, every time the monitor is looked at, until one
// says otherwise.
func TestHeartbeatReportingFailureTakesTheMonitorDown(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "backup", Type: "push", Target: "backup", Retries: 3,
		Config: store.MonitorConfig{PushToken: "tok"}})
	status := func() string {
		s, err := st.MonitorStatus(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	now := time.Now().UTC()
	if err := st.RecordPush(ctx, m.ID, now, true); err != nil {
		t.Fatal(err)
	}
	e.check(ctx, m)
	if s := status(); s != "up" {
		t.Fatalf("a heartbeat that reported success: %q", s)
	}

	// The job reports a failure once; the probe looks three times.
	if err := st.RecordPush(ctx, m.ID, time.Now().UTC(), false); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		e.check(ctx, m)
	}
	if s := status(); s != "down" {
		t.Fatalf("a job that reported a failure should be down, got %q", s)
	}
	if ev := sink.next("the failed job"); ev != "down" {
		t.Fatalf("want down, got %q", ev)
	}

	if err := st.RecordPush(ctx, m.ID, time.Now().UTC(), true); err != nil {
		t.Fatal(err)
	}
	e.check(ctx, m)
	if s := status(); s != "up" {
		t.Fatalf("after a successful heartbeat: %q", s)
	}
}

// The probe's list of monitors is re-read once a minute. A heartbeat is
// judged against the ping in the database, not the one in that list.
func TestHeartbeatIsJudgedAgainstTheNewestPing(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	m := newMonitor(t, st, store.Monitor{Name: "backup", Type: "push", Target: "backup",
		Config: store.MonitorConfig{PushToken: "tok"}})
	if err := st.RecordPush(ctx, m.ID, time.Now().UTC(), true); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().UTC().Add(-10 * time.Minute)
	m.LastPushAt = &stale // what a list read ten minutes ago would say
	e.check(ctx, m)
	if s, _ := st.MonitorStatus(ctx, m.ID); s != "up" {
		t.Fatalf("a monitor that just pinged was judged on an old ping: %q", s)
	}
}

// An escalation goes where the monitor's alerts go.
func TestEscalationFollowsTheMonitorsChannels(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	everyone := newHookSink(t, st, "everyone")
	onCall := newHookSink(t, st, "on-call")
	m := newMonitor(t, st, store.Monitor{Name: "shop", EscalateMinutes: 5, ChannelIDs: []int64{onCall.Channel.ID}})
	e.fail(m)
	if ev := onCall.next("the outage"); ev != "down" {
		t.Fatalf("want down, got %q", ev)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE monitor_state SET down_since = now() - interval '6 minutes' WHERE monitor_id=$1`, m.ID); err != nil {
		t.Fatal(err)
	}
	e.escalationSweep(ctx)
	if ev := onCall.next("the escalation"); ev != "escalation" {
		t.Fatalf("want escalation, got %q", ev)
	}
	everyone.none("a channel the monitor is not routed to")
}

// A paused monitor is not being watched, so it does not escalate.
func TestPausedMonitorDoesNotEscalate(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop", EscalateMinutes: 5})
	e.fail(m)
	sink.next("the outage")
	if _, err := st.Pool.Exec(ctx, `UPDATE monitor_state SET down_since = now() - interval '6 minutes' WHERE monitor_id=$1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatal(err)
	}
	e.escalationSweep(ctx)
	sink.none("a paused monitor")
}

// One check that panics is logged and the probe goes on to the next.
func TestACheckThatPanicsDoesNotStopTheProbe(t *testing.T) {
	e, st := alertEngine(t)
	bad := newMonitor(t, st, store.Monitor{Name: "bad"})
	good := newMonitor(t, st, store.Monitor{Name: "good"})
	e.run = func(ctx context.Context, p *Pinger, m store.Monitor) (CheckOutcome, error) {
		if m.ID == bad.ID {
			var reply []byte
			_ = reply[4] // what a malformed answer from a target can do
		}
		return CheckOutcome{LatencyMs: 1}, nil
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("the panic reached the worker loop: %v", r)
			}
		}()
		e.check(context.Background(), bad)
	}()
	e.check(context.Background(), good)
	if s, _ := st.MonitorStatus(context.Background(), good.ID); s != "up" {
		t.Fatalf("the next monitor should still be checked: %q", s)
	}
}

// A child that goes down behind its parent and comes back before its alert
// went out was never reported down, so its recovery is not reported.
func TestRecoveryOfAnUnreportedOutageIsNotSent(t *testing.T) {
	e, st := alertEngine(t)
	sink := newHookSink(t, st, "ops")
	parent := newMonitor(t, st, store.Monitor{Name: "router"})
	child := newMonitor(t, st, store.Monitor{Name: "shop", DependsOn: &parent.ID})
	e.fail(parent)
	sink.next("the parent's outage")
	e.fail(child) // held behind the parent
	e.pass(parent)
	sink.next("the parent's recovery")
	e.pass(child) // back before the sweep ran
	sink.none("a recovery for an outage nobody heard of")
	e.owedSweep(context.Background())
	sink.none("the sweep, with nothing owed")
}

// Pausing changes nothing that is owed. The trace that lost an outage: a
// monitor goes down during maintenance, with nobody told, and is paused,
// which is routine during maintenance. When it is switched back on and is
// still down, that is an outage nobody has heard of, and it is reported
// with its incident. Not before it has been checked again, though: until
// then its status is only what it was when it was paused.
func TestOutageHeldThenPausedIsReportedWhenItResumesStillDown(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop"})
	w := window(t, st, m.ID)
	e.fail(m)
	if err := st.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatal(err)
	}
	endWindow(t, st, w)
	e.owedSweep(ctx)
	sink.none("a paused monitor")
	if err := st.SetMonitorEnabled(ctx, m.ID, true); err != nil {
		t.Fatal(err)
	}
	e.owedSweep(ctx)
	sink.none("switched back on, not yet checked")

	e.fail(m) // the first check after it resumes: still down
	e.owedSweep(ctx)
	if ev := sink.next("an outage nobody had been told of"); ev != "down" {
		t.Fatalf("got %q", ev)
	}
	if n := openIncidents(t, st, m.ID); n != 1 {
		t.Fatalf("it should have its incident: %d open", n)
	}
	e.pass(m)
	if ev := sink.next("the recovery"); ev != "recovery" {
		t.Fatalf("got %q", ev)
	}
}

// The other way it can go: it is fine again when it is switched back on.
// Nobody was told it was down, so nobody is told it is back.
func TestOutageHeldThenPausedIsForgottenWhenItResumesUp(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop"})
	w := window(t, st, m.ID)
	e.fail(m)
	if err := st.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatal(err)
	}
	endWindow(t, st, w)
	if err := st.SetMonitorEnabled(ctx, m.ID, true); err != nil {
		t.Fatal(err)
	}
	e.pass(m)
	e.owedSweep(ctx)
	sink.none("a recovery for an outage nobody heard of")
	if n := openIncidents(t, st, m.ID); n != 0 {
		t.Fatalf("no incident for an outage that is over: %d open", n)
	}
}

// A monitor that was reported down and is paused is still owed its
// recovery when it comes back up, which is what closes it on a pager.
func TestReportedOutagePausedIsOwedItsRecovery(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop"})
	e.fail(m)
	sink.next("the outage")
	if err := st.SetMonitorEnabled(ctx, m.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMonitorEnabled(ctx, m.ID, true); err != nil {
		t.Fatal(err)
	}
	// Still showing "down" from before the pause, and not yet checked: no
	// reminder or escalation on the strength of that.
	later := time.Now().UTC().Add(time.Hour)
	if e.dispatch.Dispatch(alertingPayload(m, "resend"), store.ClaimResend(m.ID, later, time.Minute)) {
		t.Fatal("a reminder was sent for a monitor not checked since it was switched back on")
	}
	e.pass(m)
	if ev := sink.next("the recovery"); ev != "recovery" {
		t.Fatalf("got %q", ev)
	}
}

// "Still down" is not how anyone should learn of an outage. An escalation
// or a reminder waits until the outage itself has been reported: queued
// first, it would also sit ahead of the outage alert in the queue.
func TestNoEscalationOrReminderBeforeTheOutageIsReported(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop", EscalateMinutes: 5, ResendMinutes: 10})
	w := window(t, st, m.ID)
	e.fail(m) // down, nobody told
	endWindow(t, st, w)
	// The window has just ended and the escalation and reminder sweeps run
	// before the owed sweep has been round.
	later := time.Now().UTC().Add(time.Hour)
	if e.dispatch.Dispatch(alertingPayload(m, "escalation"), store.ClaimEscalation(m.ID)) {
		t.Fatal("an escalation was claimed before the outage was reported")
	}
	if e.dispatch.Dispatch(alertingPayload(m, "resend"), store.ClaimResend(m.ID, later, 10*time.Minute)) {
		t.Fatal("a reminder was claimed before the outage was reported")
	}
	sink.none("before the outage alert")
	e.owedSweep(ctx)
	if ev := sink.next("the outage, first"); ev != "down" {
		t.Fatalf("got %q", ev)
	}
	if !e.dispatch.Dispatch(alertingPayload(m, "escalation"), store.ClaimEscalation(m.ID)) {
		t.Fatal("once reported, it can be escalated")
	}
	if ev := sink.next("the escalation, after"); ev != "escalation" {
		t.Fatalf("got %q", ev)
	}
}

// An incident that could not be opened stays owed and is tried again; it is
// not written off before it exists.
func TestOwedIncidentSurvivesAFailureToOpenIt(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop"})
	e.fail(m)
	until := time.Now().UTC().Add(time.Hour)
	if err := st.MuteMonitor(ctx, m.ID, &until); err != nil {
		t.Fatal(err)
	}
	e.pass(m)
	e.fail(m) // a second outage inside the mute: its incident is owed
	past := time.Now().UTC().Add(-time.Second)
	if err := st.MuteMonitor(ctx, m.ID, &past); err != nil {
		t.Fatal(err)
	}
	// The database refuses the incident, as it would if it were briefly
	// unavailable: here, by a constraint that cannot be met.
	drop := func() {
		_, _ = st.Pool.Exec(context.Background(), `ALTER TABLE incidents DROP CONSTRAINT IF EXISTS test_refuse`)
	}
	t.Cleanup(drop) // whatever happens, the table is left as it was
	if _, err := st.Pool.Exec(ctx, `ALTER TABLE incidents ADD CONSTRAINT test_refuse CHECK (title <> 'shop is down') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	e.owedSweep(ctx)
	drop()
	if n := openIncidents(t, st, m.ID); n != 0 {
		t.Fatalf("the incident could not have been opened: %d open", n)
	}
	e.owedSweep(ctx)
	if n := openIncidents(t, st, m.ID); n != 1 {
		t.Fatalf("the next sweep should open the incident that is still owed: %d open", n)
	}
}

// The interleaving that left an ongoing outage with no incident and nothing
// owed: the outage is reported, the monitor is muted, it recovers and then
// fails again inside the mute, and is still down when the mute ends. Its
// recipients' last word is "down", which is true, so no second alert goes
// out; but this outage must have its incident.
func TestSecondOutageInsideAMuteGetsItsIncident(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop"})
	e.fail(m)
	if ev := sink.next("the first outage"); ev != "down" {
		t.Fatalf("got %q", ev)
	}
	until := time.Now().UTC().Add(time.Hour)
	if err := st.MuteMonitor(ctx, m.ID, &until); err != nil {
		t.Fatal(err)
	}
	e.pass(m) // resolves the first outage's incident; the recovery is not sent
	if n := openIncidents(t, st, m.ID); n != 0 {
		t.Fatalf("the first incident should be resolved: %d open", n)
	}
	e.fail(m) // down again, still muted
	e.owedSweep(ctx)
	sink.none("while muted")
	if n := openIncidents(t, st, m.ID); n != 0 {
		t.Fatalf("no incident while muted: %d open", n)
	}

	past := time.Now().UTC().Add(-time.Second)
	if err := st.MuteMonitor(ctx, m.ID, &past); err != nil {
		t.Fatal(err)
	}
	e.owedSweep(ctx)
	if n := openIncidents(t, st, m.ID); n != 1 {
		t.Fatalf("an outage still going on when the mute ends must have an incident: %d open", n)
	}
	sink.none("recipients already told it is down")
	e.owedSweep(ctx)
	if n := openIncidents(t, st, m.ID); n != 1 {
		t.Fatalf("a second sweep opened another incident: %d open", n)
	}

	// And when it does come back, they are told, and the incident closes.
	e.pass(m)
	if ev := sink.next("the recovery"); ev != "recovery" {
		t.Fatalf("got %q", ev)
	}
	if n := openIncidents(t, st, m.ID); n != 0 {
		t.Fatalf("the incident should resolve with the recovery: %d open", n)
	}
}

// The other half: muted while down, it recovers, fails and recovers again
// inside the mute. Its recipients were told it was down and never that it
// was back, so they are owed a recovery however many times it flapped.
func TestRecoveryIsOwedHoweverManyTimesItFlappedWhileMuted(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop"})
	e.fail(m)
	sink.next("the outage")
	until := time.Now().UTC().Add(time.Hour)
	if err := st.MuteMonitor(ctx, m.ID, &until); err != nil {
		t.Fatal(err)
	}
	e.pass(m)
	e.fail(m)
	e.pass(m)
	past := time.Now().UTC().Add(-time.Second)
	if err := st.MuteMonitor(ctx, m.ID, &past); err != nil {
		t.Fatal(err)
	}
	e.owedSweep(ctx)
	if ev := sink.next("the recovery owed since before the mute"); ev != "recovery" {
		t.Fatalf("got %q", ev)
	}
	e.owedSweep(ctx)
	sink.none("a second sweep")
}

// A monitor that has come back by the time the sweep gets to it is not sent
// the outage, and gets no incident for an outage that is over. The sweep's
// list is a moment old; the claim is made against the monitor as it is.
func TestSweepDoesNotReportAnOutageThatHasEnded(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop"})
	w := window(t, st, m.ID)
	e.fail(m)
	endWindow(t, st, w)
	stale, err := st.OwedAlerts(ctx) // what a sweep would have read
	if err != nil || len(stale) != 1 {
		t.Fatalf("owed before it recovers: %v %v", stale, err)
	}
	e.pass(m) // it recovers before the sweep acts on that list
	sink.none("an outage nobody was told about, now over")

	// The sweep acts on its stale list.
	if e.dispatch.Dispatch(alertingPayload(m, "down"), store.ClaimToldDown(m.ID, time.Now().UTC())) {
		t.Fatal("the outage alert was claimed for a monitor that is up")
	}
	e.openOwedIncident(ctx, m, "down", time.Now().UTC())
	// Not opened and then closed again, which would tell a status page's
	// subscribers about an incident that never was: not opened at all.
	var ever int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM incidents WHERE monitor_id=$1`, m.ID).Scan(&ever); err != nil || ever != 0 {
		t.Fatalf("an incident was opened for an outage that had ended: %d recorded, %v", ever, err)
	}
	e.owedSweep(ctx)
	sink.none("nothing is owed")
}

// An alert that was never queued, because the process stopped between
// recording the outage and queuing it, is sent on the next sweep.
func TestSweepSendsAnAlertThatWasNeverQueued(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop"})
	// The state change, as ApplyResult commits it, with nothing after it.
	if _, err := st.ApplyResult(ctx, store.PingResult{MonitorID: m.ID, TS: time.Now().UTC(), Success: false},
		store.ApplyOptions{NodeID: store.LocalProbeNodeID, Retries: 1, IntervalSeconds: 60, Locations: store.Locations(m), Confirm: 1, ErrorText: "refused"}); err != nil {
		t.Fatal(err)
	}
	sink.none("nothing was queued")
	e.owedSweep(ctx)
	if ev := sink.next("the outage, from the sweep"); ev != "down" {
		t.Fatalf("got %q", ev)
	}
	e.owedSweep(ctx)
	sink.none("a second sweep")
}

// An escalation or a reminder is for a monitor that is down. Claimed against
// the monitor as it is, it cannot go out after the recovery.
func TestNoEscalationOrReminderOnceItHasRecovered(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop", EscalateMinutes: 5, ResendMinutes: 10})
	e.fail(m)
	sink.next("the outage")
	e.pass(m)
	sink.next("the recovery")
	now := time.Now().UTC().Add(time.Hour)
	if e.dispatch.Dispatch(alertingPayload(m, "escalation"), store.ClaimEscalation(m.ID)) {
		t.Fatal("an escalation was claimed for a monitor that is up")
	}
	if e.dispatch.Dispatch(alertingPayload(m, "resend"), store.ClaimResend(m.ID, now, 10*time.Minute)) {
		t.Fatal("a reminder was claimed for a monitor that is up")
	}
	sink.none("after the recovery")
	_ = ctx
}

// What recipients were told changes once per outage and once per recovery,
// and only when it is true: a second "down" is not theirs to receive while
// they already know, nor a "recovered" while the monitor is still down.
func TestToldStateIsClaimedOnceAndOnlyWhenTrue(t *testing.T) {
	e, st := alertEngine(t)
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop"})
	now := time.Now().UTC()
	if e.dispatch.Dispatch(alertingPayload(m, "recovery"), store.ClaimToldUp(m.ID)) {
		t.Fatal("a recovery was claimed for a monitor that was never reported down")
	}
	e.fail(m)
	if ev := sink.next("the outage"); ev != "down" {
		t.Fatalf("got %q", ev)
	}
	if e.dispatch.Dispatch(alertingPayload(m, "down"), store.ClaimToldDown(m.ID, now)) {
		t.Fatal("the outage was claimed a second time")
	}
	if e.dispatch.Dispatch(alertingPayload(m, "recovery"), store.ClaimToldUp(m.ID)) {
		t.Fatal("a recovery was claimed for a monitor that is still down")
	}
	sink.none("while it is still down and they know it")
	e.pass(m)
	if ev := sink.next("the recovery"); ev != "recovery" {
		t.Fatalf("got %q", ev)
	}
	if e.dispatch.Dispatch(alertingPayload(m, "recovery"), store.ClaimToldUp(m.ID)) {
		t.Fatal("the recovery was claimed a second time")
	}
	sink.none("after the recovery")
}
