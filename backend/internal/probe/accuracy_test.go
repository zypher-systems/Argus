package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"argus/internal/store"
)

func httpMonitor(target string, cfg store.MonitorConfig) store.Monitor {
	cfg.TimeoutSeconds = 5
	return store.Monitor{Name: "site", Type: "http", Target: target, Config: cfg}
}

// Every check opens its own connection. A reused one kept a monitor green
// while the server was refusing new visitors.
func TestHTTPCheckOpensAConnectionEachTime(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Config.ConnState = func(c net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	for range 4 {
		if _, err := checkHTTP(context.Background(), httpMonitor(srv.URL, store.MonitorConfig{})); err != nil {
			t.Fatal(err)
		}
	}
	if n := conns.Load(); n != 4 {
		t.Fatalf("4 checks used %d connection(s)", n)
	}
}

// A Host header on the monitor is the host the request is made for.
func TestHTTPCheckSendsTheHostHeader(t *testing.T) {
	var host atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { host.Store(r.Host) }))
	defer srv.Close()
	m := httpMonitor(srv.URL, store.MonitorConfig{Headers: map[string]string{"host": "shop.example.com", "X-Other": "1"}})
	if _, err := checkHTTP(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if got := host.Load(); got != "shop.example.com" {
		t.Fatalf("the server saw Host %q", got)
	}
}

// Expecting a redirect means the redirect is the answer.
func TestHTTPCheckCanExpectARedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
		}
	}))
	defer srv.Close()
	if _, err := checkHTTP(context.Background(), httpMonitor(srv.URL+"/old", store.MonitorConfig{ExpectStatus: 301})); err != nil {
		t.Fatalf("expecting the 301 it returns: %v", err)
	}
	if _, err := checkHTTP(context.Background(), httpMonitor(srv.URL+"/old", store.MonitorConfig{ExpectStatus: 302})); err == nil {
		t.Fatal("a 301 is not the 302 that was expected")
	}
	// With nothing expected the redirect is followed, as before.
	if out, err := checkHTTP(context.Background(), httpMonitor(srv.URL+"/old", store.MonitorConfig{})); err != nil || out.Evidence.StatusCode != 200 {
		t.Fatalf("following the redirect: %d %v", out.Evidence.StatusCode, err)
	}
}

func certSeen(e *Engine, m store.Monitor, expires time.Time) {
	e.applier.Apply(context.Background(), m, store.LocalProbeNodeID, CheckOutcome{LatencyMs: 5, TLSExpiresAt: &expires}, nil)
}

// The warning window on a certificate monitor is the one the monitor was
// given, not always fourteen days.
func TestCertificateWarningUsesTheMonitorsWindow(t *testing.T) {
	e, st := alertEngine(t)
	sink := newHookSink(t, st, "ops")
	in20 := time.Now().UTC().Add(20 * 24 * time.Hour)
	def := newMonitor(t, st, store.Monitor{Name: "default-window", Type: "cert", Target: "a.example.com:443"})
	certSeen(e, def, in20)
	sink.none("twenty days out, on the default fourteen")
	wide := newMonitor(t, st, store.Monitor{Name: "thirty-days", Type: "cert", Target: "b.example.com:443", Config: store.MonitorConfig{WarnDays: 30}})
	certSeen(e, wide, in20)
	if ev := sink.next("twenty days out, on a thirty-day window"); ev != "cert" {
		t.Fatalf("want a cert warning, got %q", ev)
	}
}

// A name served by machines whose certificates expire on different days
// shows a different date from check to check. Each is warned about once.
func TestCertificateWarningDoesNotRepeatForAlternatingCertificates(t *testing.T) {
	e, st := alertEngine(t)
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "pool", Type: "cert", Target: "pool.example.com:443"})
	sooner := time.Now().UTC().Add(5 * 24 * time.Hour).Truncate(time.Second)
	later := time.Now().UTC().Add(9 * 24 * time.Hour).Truncate(time.Second)
	// The later date first: the one that expires sooner is the urgent one,
	// and must not go unwarned because a later date was seen before it.
	certSeen(e, m, later)
	if ev := sink.next("the first certificate seen"); ev != "cert" {
		t.Fatalf("got %q", ev)
	}
	certSeen(e, m, sooner)
	if ev := sink.next("the certificate that expires sooner, seen second"); ev != "cert" {
		t.Fatalf("got %q", ev)
	}
	for range 3 {
		certSeen(e, m, sooner)
		certSeen(e, m, later)
	}
	sink.none("the same two certificates, seen again")

	// Renewed: a later date than anything warned, warned when its time comes.
	renewed := time.Now().UTC().Add(12 * 24 * time.Hour).Truncate(time.Second)
	certSeen(e, m, renewed)
	if ev := sink.next("a renewed certificate inside the window"); ev != "cert" {
		t.Fatalf("got %q", ev)
	}
}

// A monitor that comes back from an outage over its latency threshold has
// recovered. Reporting only "slow" left the incident and the pager alert open.
func TestRecoveryThatComesBackSlowIsStillARecovery(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop", LatencyThresholdMs: 100})
	e.fail(m)
	if ev := sink.next("the outage"); ev != "down" {
		t.Fatalf("got %q", ev)
	}
	if n := openIncidents(t, st, m.ID); n != 1 {
		t.Fatalf("incident: %d open", n)
	}
	e.applier.Apply(ctx, m, store.LocalProbeNodeID, CheckOutcome{LatencyMs: 900}, nil)
	first, second := sink.next("back, but slow"), sink.next("back, but slow")
	seen := map[string]int{}
	seen[first]++
	seen[second]++
	if seen["recovery"] != 1 || seen["slow"] != 1 {
		t.Fatalf("want a recovery and a slow alert, got %q and %q", first, second)
	}
	if n := openIncidents(t, st, m.ID); n != 0 {
		t.Fatalf("the outage's incident should be resolved: %d open", n)
	}
}

// A parent paused while it was down is not being watched. It must not go on
// holding back the alerts of everything that depends on it.
func TestPausedParentDoesNotHoldItsChildrensAlerts(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	parent := newMonitor(t, st, store.Monitor{Name: "router"})
	child := newMonitor(t, st, store.Monitor{Name: "shop", DependsOn: &parent.ID})
	e.fail(parent)
	sink.next("the parent's outage")
	if err := st.SetMonitorEnabled(ctx, parent.ID, false); err != nil {
		t.Fatal(err)
	}
	e.fail(child)
	if ev := sink.next("the child, whose parent is paused"); ev != "down" {
		t.Fatalf("got %q", ev)
	}
}

// Two probes running the same sweep send one escalation and one reminder.
func TestSweepClaimsAreTakenOnce(t *testing.T) {
	e, st := alertEngine(t)
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "shop", EscalateMinutes: 5, ResendMinutes: 10})
	e.fail(m)
	sink.next("the outage")
	if !e.dispatch.Dispatch(alertingPayload(m, "escalation"), store.ClaimEscalation(m.ID)) {
		t.Fatal("first escalation claim")
	}
	if e.dispatch.Dispatch(alertingPayload(m, "escalation"), store.ClaimEscalation(m.ID)) {
		t.Fatal("second escalation claim")
	}
	later := time.Now().UTC().Add(11 * time.Minute)
	if !e.dispatch.Dispatch(alertingPayload(m, "resend"), store.ClaimResend(m.ID, later, 10*time.Minute)) {
		t.Fatal("first reminder claim")
	}
	if e.dispatch.Dispatch(alertingPayload(m, "resend"), store.ClaimResend(m.ID, later, 10*time.Minute)) {
		t.Fatal("second reminder claim")
	}
	got := []string{sink.next("escalation or reminder"), sink.next("escalation or reminder")}
	want := map[string]bool{"escalation": true, "resend": true}
	if !want[got[0]] || !want[got[1]] || got[0] == got[1] {
		t.Fatalf("want one escalation and one reminder, got %v", got)
	}
	sink.none("a second of either")
}

// A monitor the probe has just learned of is checked within a minute. Half
// of a daily interval is twelve hours: a probe restarted more often than
// that never checked it.
func TestFirstCheckComesSoonWhateverTheInterval(t *testing.T) {
	for id := int64(1); id <= 60; id++ {
		if d := firstCheckDelay(store.Monitor{ID: id, IntervalSeconds: 86400}); d < 30*time.Second || d > time.Minute {
			t.Fatalf("a daily monitor (id %d) waits %s for its first check", id, d)
		}
	}
	if d := firstCheckDelay(store.Monitor{ID: 1, IntervalSeconds: 30}); d != 15*time.Second {
		t.Fatalf("a 30-second monitor waits %s", d)
	}
}

func scheduled(expr, tz string, lastPing time.Time) store.Monitor {
	return store.Monitor{Type: "push", CreatedAt: lastPing.Add(-48 * time.Hour), LastPushAt: &lastPing,
		Config: store.MonitorConfig{Schedule: expr, Timezone: tz}}
}

// A ping a couple of seconds before its time is that time's ping.
func TestScheduledHeartbeatAcceptsAPingAMomentEarly(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	early := time.Date(2026, 10, 14, 1, 59, 58, 0, ny) // the 02:00 job, on a clock two seconds fast
	next, err := NextExpectedPing(scheduled("0 2 * * *", "America/New_York", early))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 15, 2, 0, 0, 0, ny); !next.Equal(want) {
		t.Fatalf("after an early ping the next is due %s, want %s", next, want)
	}
	// Twenty minutes early is not this run: that is yesterday's, very late.
	late := time.Date(2026, 10, 14, 1, 40, 0, 0, ny)
	next, _ = NextExpectedPing(scheduled("0 2 * * *", "America/New_York", late))
	if want := time.Date(2026, 10, 14, 2, 0, 0, 0, ny); !next.Equal(want) {
		t.Fatalf("a ping twenty minutes before: next due %s, want %s", next, want)
	}
	// An every-minute job is given seconds, not minutes.
	tick := time.Date(2026, 10, 14, 12, 0, 20, 0, ny)
	next, _ = NextExpectedPing(scheduled("* * * * *", "America/New_York", tick))
	if want := time.Date(2026, 10, 14, 12, 1, 0, 0, ny); !next.Equal(want) {
		t.Fatalf("every minute, pinged at :20: next due %s, want %s", next, want)
	}
}

// The night the clocks go back, 01:30 comes twice. A nightly job that ran at
// the first is not due at the second.
func TestScheduledHeartbeatOnTheNightClocksGoBack(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	// 2026-11-01 01:30 EDT, the first of the two.
	ran := time.Date(2026, 11, 1, 5, 30, 4, 0, time.UTC)
	next, err := NextExpectedPing(scheduled("30 1 * * *", "America/New_York", ran))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 2, 1, 30, 0, 0, ny); !next.Equal(want) {
		t.Fatalf("after running at 01:30 EDT the next is due %s, want %s", next, want)
	}
	// An ordinary night is untouched.
	ran = time.Date(2026, 10, 14, 1, 30, 4, 0, ny)
	next, _ = NextExpectedPing(scheduled("30 1 * * *", "America/New_York", ran))
	if want := time.Date(2026, 10, 15, 1, 30, 0, 0, ny); !next.Equal(want) {
		t.Fatalf("an ordinary night: next due %s, want %s", next, want)
	}
	// So is a job that runs twice a day.
	ran = time.Date(2026, 10, 14, 1, 30, 4, 0, ny)
	next, _ = NextExpectedPing(scheduled("30 1,13 * * *", "America/New_York", ran))
	if want := time.Date(2026, 10, 14, 13, 30, 0, 0, ny); !next.Equal(want) {
		t.Fatalf("twice a day: next due %s, want %s", next, want)
	}
}

// An ICMP monitor waits as long as it was told to, not always five seconds.
func TestPingUsesTheMonitorsTimeout(t *testing.T) {
	p := NewPinger(maxPingTimeout)
	p.open = func(v6 bool) (icmpConn, bool, error) { return &silentConn{}, true, nil }
	m := store.Monitor{Type: "icmp", Target: "192.0.2.1", Config: store.MonitorConfig{TimeoutSeconds: 1}}
	start := time.Now()
	if _, err := checkICMP(context.Background(), p, m); err == nil {
		t.Fatal("a host that never answers reported a reply")
	}
	if took := time.Since(start); took < 900*time.Millisecond || took > 2500*time.Millisecond {
		t.Fatalf("a one-second timeout waited %s", took.Round(time.Millisecond))
	}
}

// A certificate warning is spent as its alert is queued, not before. Looking
// for one that is due records nothing, so a stop between the look and the
// alert leaves it to be found again.
func TestCertificateWarningIsSpentWithItsAlert(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	sink := newHookSink(t, st, "ops")
	m := newMonitor(t, st, store.Monitor{Name: "site", Type: "cert", Target: "site.example.com:443"})
	expires := time.Now().UTC().Add(5 * 24 * time.Hour).Truncate(time.Second)
	if _, err := st.Pool.Exec(ctx, `UPDATE monitor_state SET tls_expires_at=$2 WHERE monitor_id=$1`, m.ID, expires); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for range 2 { // looking twice finds it twice
		due, err := st.CertWarningDue(ctx, m.ID, now, 0)
		if err != nil || due == nil || !due.Equal(expires) {
			t.Fatalf("due: %v %v", due, err)
		}
	}
	sink.none("looking sends nothing")
	p := alertingPayload(m, "cert")
	// Claimed for a date that is no longer the one seen: not ours.
	if e.dispatch.Dispatch(p, store.ClaimCertWarning(m.ID, expires.Add(time.Hour), now)) {
		t.Fatal("a warning was claimed for a certificate that is not the one in place")
	}
	if !e.dispatch.Dispatch(p, store.ClaimCertWarning(m.ID, expires, now)) {
		t.Fatal("the warning should be claimed with its alert")
	}
	if ev := sink.next("the warning"); ev != "cert" {
		t.Fatalf("got %q", ev)
	}
	if e.dispatch.Dispatch(p, store.ClaimCertWarning(m.ID, expires, now)) {
		t.Fatal("the warning was claimed twice")
	}
	if due, _ := st.CertWarningDue(ctx, m.ID, now, 0); due != nil {
		t.Fatalf("still due after it was sent: %v", due)
	}

	// The same for a domain registration.
	d := newMonitor(t, st, store.Monitor{Name: "example.com", Type: "domain", Target: "example.com"})
	if _, err := st.Pool.Exec(ctx, `UPDATE monitor_state SET expires_at=$2 WHERE monitor_id=$1`, d.ID, expires); err != nil {
		t.Fatal(err)
	}
	if due, err := st.DomainWarningDue(ctx, d.ID, now, 0); err != nil || due == nil {
		t.Fatalf("domain due: %v %v", due, err)
	}
	if !e.dispatch.Dispatch(alertingPayload(d, "domain"), store.ClaimDomainWarning(d.ID, expires)) {
		t.Fatal("the domain warning should be claimed with its alert")
	}
	if ev := sink.next("the domain warning"); ev != "domain" {
		t.Fatalf("got %q", ev)
	}
	if e.dispatch.Dispatch(alertingPayload(d, "domain"), store.ClaimDomainWarning(d.ID, expires)) {
		t.Fatal("the domain warning was claimed twice")
	}
}

// Saving a paused monitor as enabled is a resume, the same as the button.
func TestEnablingThroughAnEditCountsAsAResume(t *testing.T) {
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
	m.Enabled = true
	if err := st.UpdateMonitor(ctx, &m); err != nil {
		t.Fatal(err)
	}
	e.owedSweep(ctx)
	sink.none("enabled by an edit, not yet checked")
}
