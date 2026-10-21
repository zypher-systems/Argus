package alerting

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"argus/internal/store"
)

// queueFixture is a dispatcher on a real database with a clock the test moves.
type queueFixture struct {
	t   *testing.T
	st  *store.Store
	d   *Dispatcher
	m   store.Monitor
	mu  sync.Mutex
	now time.Time
}

func newQueueFixture(t *testing.T) *queueFixture {
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
	if _, err := st.Pool.Exec(ctx, `TRUNCATE monitors, notification_channels, alerts, alert_outbox RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM settings WHERE key IN ('general','smtp','branding')`); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureDefaultOrg(ctx); err != nil {
		t.Fatal(err)
	}
	f := &queueFixture{t: t, st: st, now: time.Now().UTC()}
	f.d = f.dispatcher()
	// Alert history hangs off a monitor, so there has to be one.
	f.m = store.Monitor{Name: "shop", Type: "http", Target: "https://shop.example.com", IntervalSeconds: 60, Retries: 1, Enabled: true, ConfirmLocations: 1}
	if err := st.CreateMonitor(ctx, &f.m); err != nil {
		t.Fatal(err)
	}
	return f
}

// dispatcher is a new dispatcher on the same database and clock: what a
// restarted process would be.
func (f *queueFixture) dispatcher() *Dispatcher {
	d := NewDispatcher(f.st, NewMailer(f.st))
	d.now = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.now
	}
	return d
}

func (f *queueFixture) advance(by time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(by)
	f.mu.Unlock()
}

// hook is a webhook channel whose server the test can break and mend.
type hook struct {
	ch     store.Channel
	broken  atomic.Bool
	delay   atomic.Int64 // nanoseconds each request is held
	holding atomic.Int32 // requests that have latched a delay and not finished it
	mu     sync.Mutex
	events []string
}

func (f *queueFixture) hook(name string) *hook {
	f.t.Helper()
	h := &hook{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if d := time.Duration(h.delay.Load()); d > 0 {
			// Latch before sleeping. A test may clear delay once it sees
			// holding, and that must not release a request already in flight.
			h.holding.Add(1)
			select {
			case <-time.After(d):
				h.holding.Add(-1)
			case <-r.Context().Done():
				h.holding.Add(-1)
				return
			}
		}
		if h.broken.Load() {
			http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
			return
		}
		var body struct{ Event string }
		_ = json.Unmarshal(raw, &body)
		h.mu.Lock()
		h.events = append(h.events, body.Event)
		h.mu.Unlock()
	}))
	f.t.Cleanup(srv.Close)
	h.ch = store.Channel{Name: name, Type: store.ChannelWebhook, Dest: srv.URL, Events: store.DefaultChannelEvents}
	if err := f.st.CreateChannel(context.Background(), &h.ch); err != nil {
		f.t.Fatal(err)
	}
	return h
}

func (h *hook) got() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.events, ",")
}

func (f *queueFixture) alert(kind string) Payload {
	return Payload{Monitor: f.m, Type: kind, At: f.d.now()}
}

func (f *queueFixture) queue(p Payload) {
	f.t.Helper()
	if _, err := f.d.enqueue(context.Background(), p, nil); err != nil {
		f.t.Fatal(err)
	}
}

// statuses is the queue's rows as "type:status", oldest first.
func (f *queueFixture) statuses() string {
	f.t.Helper()
	rows, err := f.st.Pool.Query(context.Background(), `SELECT type || ':' || status FROM alert_outbox ORDER BY id`)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

// history is the alert history a monitor's page shows, oldest first.
func (f *queueFixture) history() string {
	f.t.Helper()
	list, err := f.st.ListAlerts(context.Background(), nil, 50)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for i := len(list) - 1; i >= 0; i-- {
		out = append(out, list[i].Type+":"+list[i].Status)
	}
	return strings.Join(out, ",")
}

// A pager or mail server that is down for ten minutes used to cost the
// alert: three retries over about eighty seconds, then "failed" for good,
// with the monitor already down so nothing raised it again.
func TestAlertOutlastsAProviderOutage(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	pager := f.hook("pager")
	pager.broken.Store(true)

	f.queue(f.alert("down"))
	for range 5 { // 5s, 15s, 1m, 5m: the provider is still down
		f.d.DeliverDue(ctx)
		f.advance(6 * time.Minute)
	}
	if got := pager.got(); got != "" {
		t.Fatalf("nothing can have arrived yet: %q", got)
	}
	if s := f.statuses(); s != "down:pending" {
		t.Fatalf("the alert should still be waiting: %s", s)
	}
	if h := f.history(); h != "down:retrying" {
		t.Fatalf("alert history should say it is being retried, once: %s", h)
	}

	pager.broken.Store(false)
	f.advance(31 * time.Minute)
	f.d.DeliverDue(ctx)
	if got := pager.got(); got != "down" {
		t.Fatalf("the alert should arrive once the provider is back: %q", got)
	}
	if s := f.statuses(); s != "down:sent" {
		t.Fatalf("queue: %s", s)
	}
	if h := f.history(); h != "down:retrying,down:sent" {
		t.Fatalf("history: %s", h)
	}
	f.advance(time.Hour)
	f.d.DeliverDue(ctx)
	if got := pager.got(); got != "down" {
		t.Fatalf("a delivered alert must not be sent again: %q", got)
	}
}

// The alert is in the database before the send is tried, so a process that
// stops mid-send leaves it for the next one.
func TestAlertSurvivesARestart(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	pager := f.hook("pager")
	f.queue(f.alert("down"))

	// The first process claims the alert and dies before the send finishes.
	claimed, err := f.st.ClaimDueAlert(ctx, f.d.now(), sendLease)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}

	next := f.dispatcher()
	next.DeliverDue(ctx)
	if got := pager.got(); got != "" {
		t.Fatalf("another process must leave a claimed alert alone while it may still be sending: %q", got)
	}
	f.advance(sendLease + time.Second)
	next.DeliverDue(ctx)
	if got := pager.got(); got != "down" {
		t.Fatalf("the next process should deliver what the last one left: %q", got)
	}
}

// Each destination is its own queue row. A mail server or webhook that
// hangs holds up itself, not the pager behind it in the alphabet.
func TestOneSlowChannelDoesNotHoldUpTheOthers(t *testing.T) {
	f := newQueueFixture(t)
	slow := f.hook("a-slow")
	slow.delay.Store(int64(3 * time.Second))
	pager := f.hook("pager")
	f.d.sendTimeout = 10 * time.Second
	f.queue(f.alert("down"))

	done := make(chan struct{})
	go func() { f.d.DeliverDue(context.Background()); close(done) }()
	deadline := time.Now().Add(1500 * time.Millisecond)
	for pager.got() == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if pager.got() != "down" {
		t.Fatal("the pager waited for the slow channel")
	}
	if slow.got() != "" {
		t.Fatal("the slow channel should still be in flight")
	}
	<-done
	if slow.got() != "down" {
		t.Fatalf("the slow channel should get its alert too: %q", slow.got())
	}
}

// A recovery makes an outage alert that is still waiting untrue. Delivered
// after the recovery it would reopen in a pager what the recovery closed.
func TestRecoveryDropsAnUndeliveredOutageAlert(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	pager := f.hook("pager")
	pager.broken.Store(true)
	f.queue(f.alert("down"))
	f.d.DeliverDue(ctx)
	f.queue(f.alert("resend"))
	f.queue(f.alert("recovery"))
	if s := f.statuses(); s != "down:superseded,resend:superseded,recovery:pending" {
		t.Fatalf("queue after the recovery: %s", s)
	}
	pager.broken.Store(false)
	f.advance(time.Hour)
	f.d.DeliverDue(ctx)
	if got := pager.got(); got != "recovery" {
		t.Fatalf("only the recovery should arrive: %q", got)
	}

	// A slow monitor coming back under its threshold is not an outage ending.
	f.queue(f.alert("down"))
	p := f.alert("recovery")
	p.Of = "slow"
	f.queue(p)
	if s := f.statuses(); !strings.HasSuffix(s, "down:pending,recovery:pending") {
		t.Fatalf("a slow recovery must leave an outage alert alone: %s", s)
	}
}

// The other direction, and the one that hides an outage: a recovery still
// waiting when the monitor goes down again. Delivered after the new outage's
// alert, it closed the pager incident for an outage that was still going on.
func TestNewOutageDropsAnUndeliveredRecovery(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	pager := f.hook("pager")
	pager.broken.Store(true)
	f.queue(f.alert("down")) // outage one
	f.d.DeliverDue(ctx)
	f.queue(f.alert("recovery")) // back, with the pager still unreachable
	f.queue(f.alert("down"))     // and down again
	if s := f.statuses(); s != "down:superseded,recovery:superseded,down:pending" {
		t.Fatalf("queue after the second outage: %s", s)
	}
	pager.broken.Store(false)
	f.advance(time.Hour)
	f.d.DeliverDue(ctx)
	if got := pager.got(); got != "down" {
		t.Fatalf("the pager should be left showing an outage, and was sent %q", got)
	}
}

// An alert being sent this moment is not replaced, and the one that follows
// it waits for it: on a pager the outage lands first and the recovery
// closes it, never the other way round.
func TestRecoveryWaitsForAnOutageAlertInFlight(t *testing.T) {
	f := newQueueFixture(t)
	pager := f.hook("pager")
	// Long enough that a slow status query still observes the send in
	// flight. A short delay lets the send finish before the check, and
	// the recovery then looks like it replaced an alert that had already
	// landed.
	pager.delay.Store(int64(5 * time.Second))
	f.d.sendTimeout = 10 * time.Second
	f.queue(f.alert("down"))

	inFlight := make(chan struct{})
	go func() { f.d.DeliverDue(context.Background()); close(inFlight) }()
	deadline := time.Now().Add(3 * time.Second)
	for (f.statuses() != "down:sending" || pager.holding.Load() == 0) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s := f.statuses(); s != "down:sending" || pager.holding.Load() == 0 {
		t.Fatalf("outage alert never went in flight: %s holding=%d", s, pager.holding.Load())
	}
	// The request in flight already latched its delay. From here the pager
	// answers at once, so a recovery sent now would arrive before the
	// outage alert still on its way.
	pager.delay.Store(0)
	f.queue(f.alert("recovery"))
	if s := f.statuses(); s != "down:sending,recovery:pending" {
		t.Fatalf("an alert in flight must be left to finish: %s", s)
	}
	// A second worker looking now must not send the recovery ahead of it.
	f.dispatcher().DeliverDue(context.Background())
	if got := pager.got(); got != "" {
		t.Fatalf("the recovery was sent while the outage alert was still in flight: %q", got)
	}
	<-inFlight
	f.d.DeliverDue(context.Background())
	if got := pager.got(); got != "down,recovery" {
		t.Fatalf("the pager must see the outage, then the recovery, and saw %q", got)
	}
	if s := f.statuses(); s != "down:sent,recovery:sent" {
		t.Fatalf("queue: %s", s)
	}
}

// An outage alert that fails while a recovery waits behind it is not tried
// again: by then it is no longer true.
func TestFailedOutageAlertIsNotRetriedPastARecovery(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	pager := f.hook("pager")
	pager.broken.Store(true)
	pager.delay.Store(int64(400 * time.Millisecond))
	f.queue(f.alert("down"))
	failing := make(chan struct{})
	go func() { f.d.DeliverDue(ctx); close(failing) }()
	deadline := time.Now().Add(2 * time.Second)
	for f.statuses() != "down:sending" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	f.queue(f.alert("recovery")) // arrives while the outage alert is failing
	<-failing
	pager.broken.Store(false)
	pager.delay.Store(0)
	f.advance(time.Hour)
	f.d.DeliverDue(ctx)
	if got := pager.got(); got != "recovery" {
		t.Fatalf("only the recovery should arrive: %q", got)
	}
	if s := f.statuses(); s != "down:superseded,recovery:sent" {
		t.Fatalf("queue: %s", s)
	}
}

// The probe's own alerts follow the same rule, and each probe node's.
func TestProbeRecoveryDropsAnUndeliveredProbeDown(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	pager := f.hook("pager")
	pager.broken.Store(true)
	probe := func(kind, key string) Payload {
		return Payload{Monitor: store.Monitor{Name: "Argus probe", Target: "probe"}, Type: kind, Key: key, At: f.d.now()}
	}
	f.queue(probe("probe_down", ""))
	f.queue(probe("probe_down", "argus-probe-node-3"))
	f.d.DeliverDue(ctx)
	f.queue(probe("probe_up", "")) // the probe is back; node 3 is not
	if s := f.statuses(); s != "probe_down:superseded,probe_down:pending,probe_up:pending" {
		t.Fatalf("queue: %s", s)
	}
	pager.broken.Store(false)
	f.advance(time.Hour)
	f.d.DeliverDue(ctx)
	if got := pager.got(); !strings.Contains(got, "probe_up") || strings.Count(got, "probe_down") != 1 {
		t.Fatalf("want the probe's recovery and node 3's outage, got %q", got)
	}
}

// A claim that is not ours stores nothing and replaces nothing.
func TestAlertWithoutItsClaimIsNotQueued(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	f.hook("pager").broken.Store(true)
	f.queue(f.alert("down"))
	f.d.DeliverDue(ctx)
	// The monitor is not up, so the recovery is not ours to send.
	claimed, err := f.d.enqueue(ctx, f.alert("recovery"), store.ClaimToldUp(f.m.ID))
	if err != nil || claimed {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
	if s := f.statuses(); s != "down:pending" {
		t.Fatalf("a refused recovery must leave the outage alert waiting: %s", s)
	}
}

// A slow monitor coming back under its threshold closes the slow alert on a
// pager, so it waits for room under the hourly limit like any recovery.
func TestRateLimitHoldsASlowRecovery(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	pager := f.hook("pager")
	if err := f.st.SetGeneralSettings(ctx, store.GeneralSettings{NotifyMaxPerHour: 1}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.st.SetGeneralSettings(context.Background(), store.GeneralSettings{}) })
	if err := f.st.InsertAlert(ctx, 0, "cert", "sent", "pager", ""); err != nil {
		t.Fatal(err)
	}
	p := f.alert("recovery")
	p.Of = "slow"
	f.queue(p)
	f.d.DeliverDue(ctx)
	if s := f.statuses(); s != "recovery:pending" {
		t.Fatalf("a slow recovery waits, it is not skipped: %s", s)
	}
	f.advance(61 * time.Minute)
	f.d.DeliverDue(ctx)
	if got := pager.got(); got != "recovery" {
		t.Fatalf("it should arrive once there is room: %q", got)
	}
}

func TestAlertIsGivenUpAfterADay(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	pager := f.hook("pager")
	pager.broken.Store(true)
	f.queue(f.alert("down"))
	f.d.DeliverDue(ctx)
	f.advance(23 * time.Hour)
	f.d.DeliverDue(ctx)
	if s := f.statuses(); s != "down:pending" {
		t.Fatalf("still inside the day: %s", s)
	}
	f.advance(2 * time.Hour)
	f.d.DeliverDue(ctx)
	if s := f.statuses(); s != "down:failed" {
		t.Fatalf("after a day: %s", s)
	}
	if h := f.history(); h != "down:retrying,down:failed" {
		t.Fatalf("history should record the failure: %s", h)
	}
}

// With the hourly limit spent, a reminder is skipped, as before. An outage
// waits for room instead of being dropped.
func TestRateLimitHoldsAnOutageAlertInsteadOfDroppingIt(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	pager := f.hook("pager")
	if err := f.st.SetGeneralSettings(ctx, store.GeneralSettings{NotifyMaxPerHour: 1}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.st.SetGeneralSettings(context.Background(), store.GeneralSettings{}) })
	if err := f.st.InsertAlert(ctx, 0, "cert", "sent", "pager", ""); err != nil { // the hour's one alert
		t.Fatal(err)
	}

	f.queue(f.alert("resend"))
	f.queue(f.alert("down"))
	f.d.DeliverDue(ctx)
	if got := pager.got(); got != "" {
		t.Fatalf("the limit is spent: %q", got)
	}
	if s := f.statuses(); s != "resend:skipped,down:pending" {
		t.Fatalf("a reminder is skipped and an outage waits: %s", s)
	}

	f.advance(61 * time.Minute) // the earlier alert is now more than an hour old
	f.d.DeliverDue(ctx)
	if got := pager.got(); got != "down" {
		t.Fatalf("the outage should arrive once there is room: %q", got)
	}
}

// An alert for a channel deleted while it waited is closed, not retried for a day.
func TestAlertForADeletedChannelIsClosed(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	pager := f.hook("pager")
	f.queue(f.alert("down"))
	if err := f.st.DeleteChannel(ctx, pager.ch.ID); err != nil {
		t.Fatal(err)
	}
	f.d.DeliverDue(ctx)
	if s := f.statuses(); s != "down:failed" {
		t.Fatalf("queue: %s", s)
	}
}

// The queue holds what the alert says, not the monitor's configuration.
func TestQueuedAlertCarriesNoCredentials(t *testing.T) {
	p := Payload{Type: "down", Detail: "connection refused", Monitor: store.Monitor{
		ID: 7, Name: "shop", Type: "http", Target: "https://shop.example.com", EscalateMinutes: 5,
		Config: store.MonitorConfig{AuthUser: "svc", AuthPassword: "s3cret-pw", AuthToken: "s3cret-token",
			Secrets: map[string]string{"key": "s3cret-value"}},
	}}
	raw, err := pack(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "s3cret") {
		t.Fatalf("a credential was written to the queue: %s", raw)
	}
	back, err := unpack(raw)
	if err != nil || back.Monitor.Name != "shop" || back.Monitor.EscalateMinutes != 5 || back.Detail != p.Detail || back.Type != "down" {
		t.Fatalf("round trip: %+v %v", back, err)
	}
}

// However many alerts are waiting for a destination that hangs, it holds a
// couple of workers and no more, so alerts for other destinations behind
// them in the queue still go out.
func TestHungChannelDoesNotTakeEveryWorker(t *testing.T) {
	f := newQueueFixture(t)
	hung := f.hook("a-hung")
	hung.delay.Store(int64(2 * time.Second))
	f.d.sendTimeout = 10 * time.Second
	ctx := context.Background()
	// Twelve alerts for the hung channel, each for a monitor of its own so
	// that none waits behind another, queued ahead of one for the pager.
	only := func(m store.Monitor, ch *hook) Payload {
		m.ChannelIDs = []int64{ch.ch.ID}
		return Payload{Monitor: m, Type: "down", At: f.d.now()}
	}
	for i := range 12 {
		m := store.Monitor{Name: "m", Type: "http", Target: "https://x.example.com", IntervalSeconds: 60, Retries: 1, Enabled: true, ConfirmLocations: 1}
		m.Name = "hung-" + string(rune('a'+i))
		if err := f.st.CreateMonitor(ctx, &m); err != nil {
			t.Fatal(err)
		}
		f.queue(only(m, hung))
	}
	pager := f.hook("pager")
	f.queue(only(f.m, pager))

	done := make(chan struct{})
	go func() { f.d.DeliverDue(ctx); close(done) }()
	deadline := time.Now().Add(1500 * time.Millisecond)
	for pager.got() == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if pager.got() != "down" {
		t.Fatal("the pager's alert waited behind a destination that hangs")
	}
	var sending int
	if err := f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM alert_outbox WHERE status='sending' AND dest='a-hung'`).Scan(&sending); err != nil {
		t.Fatal(err)
	}
	if sending > 2 {
		t.Fatalf("%d sends in flight to one destination", sending)
	}
	hung.delay.Store(0)
	<-done
}

// A channel is its id. Renaming it while its alerts wait must not split
// their order: under the old name and the new they are the same destination.
func TestRenamingAChannelKeepsItsAlertsInOrder(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	pager := f.hook("pager")
	pager.broken.Store(true)
	f.queue(f.alert("down"))
	f.d.DeliverDue(ctx)
	pager.ch.Name = "on-call"
	if err := f.st.UpdateChannel(ctx, &pager.ch); err != nil {
		t.Fatal(err)
	}
	f.queue(f.alert("recovery"))
	if s := f.statuses(); s != "down:superseded,recovery:pending" {
		t.Fatalf("the recovery should replace the outage alert waiting for the same channel: %s", s)
	}
	// And the other way, which is decided per destination: a new outage
	// replaces the recovery waiting for this channel under its old name.
	pager.ch.Name = "primary"
	if err := f.st.UpdateChannel(ctx, &pager.ch); err != nil {
		t.Fatal(err)
	}
	f.queue(f.alert("down"))
	if s := f.statuses(); s != "down:superseded,recovery:superseded,down:pending" {
		t.Fatalf("the new outage should replace the recovery waiting for the same channel: %s", s)
	}
}

// Only so many alerts are taken for one destination at a time, however many
// workers, drains and processes ask at the same instant. Counting what is
// in flight and taking one more is a single step: without that, each asker
// counted before the others had taken theirs and all of them took one.
func TestClaimsForOneDestinationAreCounted(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	hung := f.hook("a-hung")
	for i := range 16 {
		m := store.Monitor{Name: "many-" + string(rune('a'+i)), Type: "http", Target: "https://x.example.com", IntervalSeconds: 60, Retries: 1, Enabled: true, ConfirmLocations: 1}
		if err := f.st.CreateMonitor(ctx, &m); err != nil {
			t.Fatal(err)
		}
		m.ChannelIDs = []int64{hung.ch.ID}
		f.queue(Payload{Monitor: m, Type: "down", At: f.d.now()})
	}
	for round := range 5 {
		if _, err := f.st.Pool.Exec(ctx, `UPDATE alert_outbox SET status='pending', next_attempt_at=$1`, f.d.now()); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var took atomic.Int32
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if it, err := f.st.ClaimDueAlert(ctx, f.d.now(), sendLease); err == nil && it != nil {
					took.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if n := took.Load(); n != 2 {
			t.Fatalf("round %d: 16 claims at once took %d alerts for one destination, want 2", round, n)
		}
	}
}

// An alert queued while another destination hangs is sent at once. The one
// drain goes on looking while sends are in flight; it does not wait for
// them, and whoever queues an alert wakes it.
func TestAlertQueuedDuringAHungSendIsNotHeldUp(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	hung := f.hook("a-hung")
	hung.delay.Store(int64(2 * time.Second))
	f.d.sendTimeout = 10 * time.Second
	only := func(ch *hook) Payload {
		p := f.alert("down")
		p.Monitor.ChannelIDs = []int64{ch.ch.ID}
		return p
	}
	f.queue(only(hung))
	done := make(chan struct{})
	go func() { f.d.DeliverDue(ctx); close(done) }()
	deadline := time.Now().Add(time.Second)
	for f.statuses() != "down:sending" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	pager := f.hook("pager")
	other := store.Monitor{Name: "other", Type: "http", Target: "https://y.example.com", IntervalSeconds: 60, Retries: 1, Enabled: true, ConfirmLocations: 1}
	if err := f.st.CreateMonitor(ctx, &other); err != nil {
		t.Fatal(err)
	}
	other.ChannelIDs = []int64{pager.ch.ID}
	f.queue(Payload{Monitor: other, Type: "down", At: f.d.now()})
	began := time.Now()
	f.d.DeliverDue(ctx) // what queuing an alert does: wake the drain
	if waited := time.Since(began); waited > 300*time.Millisecond {
		t.Fatalf("waking the drain took %s; it should return at once", waited.Round(time.Millisecond))
	}
	deadline = time.Now().Add(900 * time.Millisecond)
	for pager.got() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if pager.got() != "down" {
		t.Fatal("an alert queued while another send hung waited for it")
	}
	if hung.got() != "" {
		t.Fatal("the hung send should still be in flight")
	}
	hung.delay.Store(0)
	<-done
}

// What a new alert replaces is what was waiting for the destinations it is
// going to. A channel taken off the monitor mid-outage is not going to get
// the next alert, so it keeps the recovery that closes what it was sent.
func TestReplacingAnAlertLeavesOtherDestinationsTheirs(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	a, p := f.hook("a-team"), f.hook("pager")
	a.broken.Store(true)
	p.broken.Store(true)
	both := f.alert("recovery")
	f.queue(both) // the first outage's recovery, waiting for both
	f.d.DeliverDue(ctx)

	onlyA := f.alert("down") // the pager was taken off the monitor since
	onlyA.Monitor.ChannelIDs = []int64{a.ch.ID}
	f.queue(onlyA)
	rows, err := f.st.Pool.Query(ctx, `SELECT dest || ' ' || type || ':' || status FROM alert_outbox ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		got = append(got, s)
	}
	want := "a-team recovery:superseded, pager recovery:pending, a-team down:pending"
	if strings.Join(got, ", ") != want {
		t.Fatalf("queue:\n got %s\nwant %s", strings.Join(got, ", "), want)
	}
}

// The cap on what is in flight to one destination holds when several
// drains run at once, in this process and in others: the api and the probe
// both deliver, and an alert queued a moment after another starts a drain
// of its own.
func TestHungChannelCapHoldsAcrossOverlappingDrains(t *testing.T) {
	f := newQueueFixture(t)
	hung := f.hook("a-hung")
	hung.delay.Store(int64(1500 * time.Millisecond))
	ctx := context.Background()
	for i := range 12 {
		m := store.Monitor{Name: "hung-" + string(rune('a'+i)), Type: "http", Target: "https://x.example.com", IntervalSeconds: 60, Retries: 1, Enabled: true, ConfirmLocations: 1}
		if err := f.st.CreateMonitor(ctx, &m); err != nil {
			t.Fatal(err)
		}
		m.ChannelIDs = []int64{hung.ch.ID}
		f.queue(Payload{Monitor: m, Type: "down", At: f.d.now()})
	}
	// Three processes' worth of dispatchers, each asked to drain several
	// times at the same moment.
	var wg sync.WaitGroup
	for range 3 {
		d := f.dispatcher()
		d.sendTimeout = 10 * time.Second
		for range 4 {
			wg.Add(1)
			go func() { defer wg.Done(); d.DeliverDue(ctx) }()
		}
	}
	most := 0
	deadline := time.Now().Add(1200 * time.Millisecond)
	for time.Now().Before(deadline) {
		var sending int
		if err := f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM alert_outbox WHERE status='sending'`).Scan(&sending); err != nil {
			t.Fatal(err)
		}
		most = max(most, sending)
		time.Sleep(15 * time.Millisecond)
	}
	hung.delay.Store(0)
	wg.Wait()
	if most > 2 {
		t.Fatalf("%d alerts were in flight to one destination at once", most)
	}
	if most == 0 {
		t.Fatal("nothing was ever in flight; the test did not exercise the cap")
	}
}

// A recovery goes wherever the outage went. A channel that was sent the
// outage and has since been taken off the monitor still gets the recovery;
// one that never got the outage, because it could not be reached, has that
// alert dropped and is sent nothing.
func TestRecoveryFollowsTheOutageToChannelsSinceRemoved(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	told, unreached, current := f.hook("a-told"), f.hook("b-unreached"), f.hook("c-current")
	unreached.broken.Store(true)
	f.queue(f.alert("down")) // to all three
	f.d.DeliverDue(ctx)
	if told.got() != "down" || current.got() != "down" || unreached.got() != "" {
		t.Fatalf("setup: told %q current %q unreached %q", told.got(), current.got(), unreached.got())
	}

	// The monitor is re-routed to one channel while it is down.
	back := f.alert("recovery")
	back.Monitor.ChannelIDs = []int64{current.ch.ID}
	f.queue(back)
	unreached.broken.Store(false)
	f.advance(time.Hour)
	f.d.DeliverDue(ctx)
	if got := current.got(); got != "down,recovery" {
		t.Fatalf("the channel still on the monitor: %q", got)
	}
	if got := told.got(); got != "down,recovery" {
		t.Fatalf("a channel that was sent the outage must be sent the recovery, though it is off the monitor now: %q", got)
	}
	if got := unreached.got(); got != "" {
		t.Fatalf("a channel that never got the outage should get neither alert: %q", got)
	}
}

// An escalation to extra addresses that is still waiting when the monitor
// recovers is dropped with everything else about that outage.
func TestRecoveryDropsAWaitingEscalationToExtraAddresses(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	f.hook("pager")
	esc := f.alert("escalation")
	esc.Monitor.EscalateRecipients = "boss@example.com"
	f.queue(esc) // one row for the pager, one for the address; no mail server, so the second waits
	f.d.DeliverDue(ctx)
	f.queue(f.alert("recovery"))
	var waiting int
	if err := f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM alert_outbox WHERE type='escalation' AND status IN ('pending','sending')`).Scan(&waiting); err != nil {
		t.Fatal(err)
	}
	if waiting != 0 {
		t.Fatalf("%d escalation(s) still waiting after the recovery", waiting)
	}
}

// A process has a fixed number of sends in flight at most, however many
// destinations are waiting. The rest are taken as workers come free.
func TestSendsInFlightAreBounded(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	f.d.sendTimeout = 10 * time.Second
	hooks := make([]*hook, 12)
	for i := range hooks {
		hooks[i] = f.hook("slow-" + string(rune('a'+i)))
		hooks[i].delay.Store(int64(600 * time.Millisecond))
	}
	f.queue(f.alert("down")) // one row for each of the twelve
	done := make(chan struct{})
	go func() { f.d.DeliverDue(ctx); close(done) }()
	most := 0
	for {
		select {
		case <-done:
			if most != deliverWorkers {
				t.Fatalf("at most %d sends were in flight at once, want exactly the bound of %d", most, deliverWorkers)
			}
			for _, h := range hooks {
				if h.got() != "down" {
					t.Fatalf("%s was sent %q", h.ch.Name, h.got())
				}
			}
			return
		default:
		}
		var sending int
		if err := f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM alert_outbox WHERE status='sending'`).Scan(&sending); err != nil {
			t.Fatal(err)
		}
		most = max(most, sending)
		time.Sleep(10 * time.Millisecond)
	}
}
