package probe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"argus/internal/alerting"
	"argus/internal/store"
)

type Engine struct {
	store    *store.Store
	dispatch *alerting.Dispatcher
	applier  *Applier
	workers  int
	mu       sync.Mutex
	monitors map[int64]store.Monitor
	nextRun  map[int64]time.Time
	pinger   *Pinger
	// run performs one check. It is Check, except in tests.
	run func(context.Context, *Pinger, store.Monitor) (CheckOutcome, error)
	// checkNow bounds the "check now" requests in flight.
	checkNow chan struct{}
}

const (
	schedulerTick  = 500 * time.Millisecond
	defaultWorkers = 64
	// checkNowWorkers is how many operator-requested checks run at once.
	checkNowWorkers = 8
	// maxPingTimeout is the longest an ICMP monitor's timeout can make a
	// ping wait.
	maxPingTimeout = 120 * time.Second
)

type wsResult struct {
	Type         string   `json:"type"`
	MonitorID    int64    `json:"monitor_id"`
	TS           string   `json:"ts"`
	LatencyMs    *float64 `json:"latency_ms"`
	Success      bool     `json:"success"`
	Status       string   `json:"status"`
	Slow         bool     `json:"slow"`
	Event        string   `json:"event,omitempty"`
	TLSExpiresAt *string  `json:"tls_expires_at,omitempty"`
}

func rfc3339Ptr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func NewEngine(st *store.Store, disp *alerting.Dispatcher) *Engine {
	return &Engine{
		store:    st,
		dispatch: disp,
		applier:  NewApplier(st, disp),
		workers:  defaultWorkers,
		monitors: map[int64]store.Monitor{},
		nextRun:  map[int64]time.Time{},
		// The ceiling for one ping; each check stops at its monitor's timeout.
		pinger:   NewPinger(maxPingTimeout),
		run:      Check,
		checkNow: make(chan struct{}, checkNowWorkers),
	}
}

func (e *Engine) Run(ctx context.Context) error {
	reload := make(chan struct{}, 1)
	go e.listenConfig(ctx, reload)
	go e.listenCheckNow(ctx)

	if err := e.reload(ctx); err != nil {
		return fmt.Errorf("initial monitor load: %w", err)
	}

	jobs := make(chan store.Monitor, 256)
	var wg sync.WaitGroup
	for i := 0; i < e.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range jobs {
				e.check(ctx, m)
			}
		}()
	}

	// The heartbeat says the probe is alive. It has its own loop, so slow
	// checks, a full queue or a slow sweep cannot stop it and have the api
	// announce "checks have stopped" when they are only running late.
	go e.heartbeatLoop(ctx)

	ticker := time.NewTicker(schedulerTick)
	escTicker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer escTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			e.pinger.Close()
			return ctx.Err()
		case <-reload:
			if err := e.reload(ctx); err != nil {
				slog.Error("monitor reload failed", "error", err)
			}
		case <-escTicker.C:
			e.owedSweep(ctx)
			e.escalationSweep(ctx)
			e.resendSweep(ctx)
			e.groupSweep(ctx)
		case <-ticker.C:
			e.drainCheckNow(ctx)
			e.scheduleDue(jobs)
		}
	}
}

func (e *Engine) listenCheckNow(ctx context.Context) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		err := e.store.Listen(ctx, store.ChannelCheckNow, func(payload string) {
			id, err := strconv.ParseInt(strings.TrimSpace(payload), 10, 64)
			if err != nil || id <= 0 {
				return
			}
			// Left for the scheduler's next tick when every slot is taken.
			if !e.takeCheckNowSlot() {
				return
			}
			req, err := e.store.ClaimCheckNow(ctx, id)
			if err != nil || req == nil {
				e.freeCheckNowSlot()
				return
			}
			go e.runClaimedCheckNow(ctx, req)
		})
		if ctx.Err() != nil {
			return
		}
		slog.Warn("check-now listener dropped, retrying", "error", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// drainCheckNow starts the "check now" requests that are waiting, as far as
// there are slots, and returns at once. They used to run here one after
// another: two slow targets stopped all scheduling, and the heartbeat with
// it, for as long as their timeouts.
func (e *Engine) drainCheckNow(ctx context.Context) {
	for e.takeCheckNowSlot() {
		req, err := e.store.ClaimPendingCheckNow(ctx)
		if err != nil || req == nil {
			e.freeCheckNowSlot()
			if err != nil {
				slog.Error("claim check-now failed", "error", err)
			}
			return
		}
		go e.runClaimedCheckNow(ctx, req)
	}
}

func (e *Engine) takeCheckNowSlot() bool {
	select {
	case e.checkNow <- struct{}{}:
		return true
	default:
		return false
	}
}

func (e *Engine) freeCheckNowSlot() { <-e.checkNow }

// runClaimedCheckNow runs one claimed request in the slot taken for it.
func (e *Engine) runClaimedCheckNow(ctx context.Context, req *store.CheckNowRequest) {
	defer e.freeCheckNowSlot()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("check-now panicked", "monitor", req.MonitorID, "panic", r, "stack", string(debug.Stack()))
			_ = e.store.CompleteCheckNow(context.WithoutCancel(ctx), req.ID, false, nil, "the check failed unexpectedly", nil)
		}
	}()
	e.runCheckNow(ctx, req)
}

func (e *Engine) runCheckNow(ctx context.Context, req *store.CheckNowRequest) {
	mws, err := e.store.GetMonitor(ctx, req.MonitorID)
	if err != nil {
		_ = e.store.CompleteCheckNow(ctx, req.ID, false, nil, "monitor not found", nil)
		return
	}
	m := mws.Monitor
	e.freshPush(ctx, &m)
	if strings.EqualFold(m.Type, "group") {
		_ = e.store.CompleteCheckNow(ctx, req.ID, false, nil, "group is not probed", nil)
		return
	}
	outcome, pingErr := e.run(ctx, e.pinger, m)
	if errors.Is(pingErr, errAwaitingPush) {
		ev := outcome.Evidence
		_ = e.store.CompleteCheckNow(ctx, req.ID, false, nil, pingErr.Error(), &ev)
		return
	}
	// A monitor checked only from probe nodes gets its answer, but this
	// server is not one of its locations, so the result does not vote.
	if m.Enabled && store.AssignedToNode(m, store.LocalProbeNodeID) {
		e.applier.Apply(ctx, m, store.LocalProbeNodeID, outcome, pingErr)
	} else if pingErr != nil {
		ev := outcome.Evidence
		_ = e.store.SetLastFailureEvidence(ctx, m.ID, time.Now().UTC(), &ev)
	}
	errText := ""
	if pingErr != nil {
		errText = pingErr.Error()
	}
	lat := &outcome.LatencyMs
	ev := outcome.Evidence
	_ = e.store.CompleteCheckNow(ctx, req.ID, pingErr == nil, lat, errText, &ev)
}

func (e *Engine) listenConfig(ctx context.Context, reload chan<- struct{}) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		err := e.store.Listen(ctx, store.ChannelConfig, func(string) {
			select {
			case reload <- struct{}{}:
			default:
			}
		})
		if ctx.Err() != nil {
			return
		}
		slog.Warn("config listener dropped, retrying", "error", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func mergeSchedule(prevMonitors map[int64]store.Monitor, prevNext map[int64]time.Time, incoming []store.Monitor, now time.Time) (map[int64]store.Monitor, map[int64]time.Time) {
	want := make(map[int64]store.Monitor, len(incoming))
	for _, m := range incoming {
		want[m.ID] = m
	}
	next := make(map[int64]time.Time, len(incoming))
	for id := range prevNext {
		if _, ok := want[id]; ok {
			next[id] = prevNext[id]
		}
	}
	for id, m := range want {
		old, existed := prevMonitors[id]
		if !existed {
			next[id] = now.Add(firstCheckDelay(m))
			continue
		}
		if old.IntervalSeconds != m.IntervalSeconds {
			next[id] = now.Add(time.Duration(m.IntervalSeconds) * time.Second / 2)
		}
	}
	return want, next
}

// firstCheckDelay is how long a monitor the probe has just learned of waits
// for its first check: half its interval, as before, but never more than a
// minute. Half of a daily interval is twelve hours, and a probe restarted
// more often than that never checked the monitor at all. Monitors are spread
// across the delay so a start does not check everything at once.
func firstCheckDelay(m store.Monitor) time.Duration {
	d := time.Duration(m.IntervalSeconds) * time.Second / 2
	if d > time.Minute {
		d = time.Minute/2 + time.Duration(m.ID%30)*time.Second
	}
	return d
}

func (e *Engine) reload(ctx context.Context) error {
	monitors, err := e.store.ListEnabledMonitorsForNode(ctx, store.LocalProbeNodeID)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	before := len(e.monitors)
	e.monitors, e.nextRun = mergeSchedule(e.monitors, e.nextRun, monitors, time.Now())
	if len(monitors) != before {
		slog.Info("monitors loaded", "count", len(monitors))
	}
	return nil
}

func (e *Engine) heartbeatLoop(ctx context.Context) {
	e.heartbeat(ctx)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.heartbeat(ctx)
		}
	}
}

func (e *Engine) heartbeat(ctx context.Context) {
	if err := e.store.TouchProbeHeartbeat(ctx, time.Now().UTC()); err != nil {
		slog.Error("probe heartbeat failed", "error", err)
	}
}

// scheduleDue hands the workers every check that is due, most overdue
// first, and never waits for them. When the queue is full the rest stay due
// and are offered again on the next tick: no check is dropped, and the loop
// that also runs the sweeps and reloads is not held up behind slow targets.
func (e *Engine) scheduleDue(jobs chan<- store.Monitor) {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	var due []store.Monitor
	for id, m := range e.monitors {
		if strings.EqualFold(m.Type, "group") {
			continue
		}
		if !e.nextRun[id].After(now) {
			due = append(due, m)
		}
	}
	sort.Slice(due, func(i, j int) bool { return e.nextRun[due[i].ID].Before(e.nextRun[due[j].ID]) })
	for _, m := range due {
		select {
		case jobs <- m:
			e.nextRun[m.ID] = now.Add(time.Duration(m.IntervalSeconds) * time.Second)
		default:
			return // full: what is left stays due
		}
	}
}

func (e *Engine) check(ctx context.Context, m store.Monitor) {
	// One check that panics must not take the probe, and every other
	// monitor's checks, down with it.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("check panicked", "monitor", m.Name, "type", m.Type, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	if strings.EqualFold(m.Type, "group") {
		return
	}
	e.freshPush(ctx, &m)
	outcome, pingErr := e.run(ctx, e.pinger, m)
	if errors.Is(pingErr, errAwaitingPush) {
		return
	}
	e.applier.Apply(ctx, m, store.LocalProbeNodeID, outcome, pingErr)
}

// freshPush reads a heartbeat monitor's newest ping. The monitor in hand
// came from a list re-read once a minute, and a heartbeat judged against a
// ping time that old is called overdue when it is not.
func (e *Engine) freshPush(ctx context.Context, m *store.Monitor) {
	if !strings.EqualFold(m.Type, "push") {
		return
	}
	at, ok, err := e.store.LastPush(ctx, m.ID)
	if err != nil {
		return
	}
	m.LastPushAt, m.LastPushDown = at, !ok
}

func transitionAlert(t store.StateTransition, success bool, latencyMs float64, pingErr error) (alertType, eventType, detail string) {
	if success {
		detail = fmt.Sprintf("latency %.1fms", latencyMs)
	} else if pingErr != nil {
		detail = pingErr.Error()
	}

	if t.Changed && t.NewStatus == "down" {
		return "down", "down", detail
	}
	// Back from an outage is a recovery even when it comes back over its
	// latency threshold. Calling that "slow" left the outage's incident and
	// its pager alert open. Apply reports the slowness as well.
	if t.Changed && t.NewStatus == "up" && t.OldStatus == "down" {
		return "recovery", "up", detail
	}
	if t.BecameSlow {
		return "slow", "slow", detail
	}
	if t.RecoveredSlow {
		return "recovery", "up", detail
	}
	return "", "", detail
}

// maybeDomainWarn fires one alert per expiry date when a domain registration
// is inside the warning window.

func (e *Engine) escalationSweep(ctx context.Context) {
	candidates, err := e.store.EscalationDue(ctx, time.Now().UTC())
	if err != nil {
		slog.Error("escalation query failed", "error", err)
		return
	}
	for _, c := range candidates {
		maint, err := e.store.MaintenanceActive(ctx, c.MonitorID, time.Now().UTC())
		if err == nil && maint {
			continue
		}
		// The whole monitor, so the escalation goes to the channels this
		// monitor is routed to and can say how long it waited.
		full, err := e.store.GetMonitor(ctx, c.MonitorID)
		if err != nil || !full.Enabled {
			continue
		}
		m := full.Monitor
		if e.applier.dependencyDown(ctx, m) {
			continue
		}
		// Claimed while it is still down, with the alert, so it cannot be
		// sent twice or land after the recovery.
		e.dispatch.Dispatch(alerting.Payload{
			Monitor: m,
			Type:    "escalation",
			At:      time.Now().UTC(),
			Detail:  fmt.Sprintf("down since %s", c.DownSince.Format("15:04:05 MST")),
		}, store.ClaimEscalation(c.MonitorID))
	}
}

// owedSweep brings recipients and incidents back in step with monitors.
// A monitor is owed an alert when it is down and nobody has been told, or up
// and everybody is still told it is down, and nothing suppresses it now.
//
// That is the state an outage is left in when it begins during a
// maintenance window, a mute or its parent's outage: sent here when that
// ends, if it is still true. It is also the state left by a process that
// stopped between recording an outage and queuing its alert. Because the
// rule is about how things stand and not about what happened, no order of
// outages, recoveries and suppression can leave one unreported.
//
// Each alert is claimed against the monitor's status in the transaction
// that queues it, so a monitor that changes while this runs is not sent a
// stale alert, and this and the check that saw the change send one between
// them.
func (e *Engine) owedSweep(ctx context.Context) {
	owed, err := e.store.OwedAlerts(ctx)
	if err != nil {
		slog.Error("owed alert query failed", "error", err)
		return
	}
	now := time.Now().UTC()
	for _, o := range owed {
		full, err := e.store.GetMonitor(ctx, o.MonitorID)
		if err != nil {
			continue
		}
		m := full.Monitor
		if e.applier.suppressedBy(ctx, m, now) != "" {
			continue
		}
		p := alerting.Payload{Monitor: m, At: now}
		switch {
		case o.Status == "down" && !o.ToldDown:
			p.Type = "down"
			p.Detail = "down; this alert was delayed"
			if o.DownSince != nil {
				p.Detail = fmt.Sprintf("down since %s; this alert was delayed", o.DownSince.UTC().Format("2 Jan 15:04 MST"))
			}
			if !e.dispatch.Dispatch(p, store.ClaimToldDown(m.ID, now)) {
				continue // it came back, or the check that saw it sent this
			}
			slog.Info("delayed alert sent", "monitor", m.Name, "type", p.Type)
			e.openOwedIncident(ctx, m, p.Detail, now)
		case o.Status == "down" && o.IncidentOwed:
			// Its recipients know it is down, from an outage before this
			// one, but this outage has no incident.
			e.openOwedIncident(ctx, m, "down", now)
		case o.Status == "up" && o.ToldDown:
			p.Type = "recovery"
			p.Detail = "recovered; this alert was delayed"
			if e.dispatch.Dispatch(p, store.ClaimToldUp(m.ID)) {
				slog.Info("delayed alert sent", "monitor", m.Name, "type", p.Type)
			}
		}
	}
}

// openOwedIncident opens the incident for an outage that could not have one
// when it began, if the monitor is still down, and tells the status page's
// subscribers only once it is open. The debt is settled with the incident
// or not at all, so a failure here is tried again on the next sweep.
func (e *Engine) openOwedIncident(ctx context.Context, m store.Monitor, detail string, now time.Time) {
	inc, opened, err := e.store.OpenOwedIncident(ctx, m.ID, m.Name, detail, now)
	if err != nil {
		slog.Error("open incident failed; will try again", "monitor", m.Name, "error", err)
		return
	}
	if opened && inc != nil {
		e.dispatch.NotifyIncidentAsync(*inc)
	}
}

func (e *Engine) resendSweep(ctx context.Context) {
	due, err := e.store.ResendDue(ctx, time.Now().UTC())
	if err != nil {
		slog.Error("resend query failed", "error", err)
		return
	}
	now := time.Now().UTC()
	for _, m := range due {
		maint, err := e.store.MaintenanceActive(ctx, m.ID, now)
		if err == nil && maint {
			continue
		}
		if e.applier.dependencyDown(ctx, m) {
			continue
		}
		e.dispatch.Dispatch(alerting.Payload{
			Monitor: m,
			Type:    "resend",
			At:      now,
			Detail:  fmt.Sprintf("still down, reminder every %d min", m.ResendMinutes),
		}, store.ClaimResend(m.ID, now, time.Duration(m.ResendMinutes)*time.Minute))
	}
}

func (e *Engine) groupSweep(ctx context.Context) {
	ids, err := e.store.ListGroupIDs(ctx)
	if err != nil {
		slog.Error("group list failed", "error", err)
		return
	}
	for _, id := range ids {
		if err := e.store.RefreshGroupStatus(ctx, id); err != nil {
			slog.Error("group status failed", "group", id, "error", err)
		}
	}
}
