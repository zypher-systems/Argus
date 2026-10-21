package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"argus/internal/store"
)

const (
	// giveUpAfter is how long an alert is retried before it is recorded as
	// failed. Long enough to outlast a mail server or pager outage; after a
	// day the news is no longer news.
	giveUpAfter = 24 * time.Hour
	// sendLease is how long a claimed alert is left alone. A process that
	// dies mid-send leaves the alert to be tried again after this.
	sendLease = 2 * time.Minute
	// deliverWorkers bounds sends in flight. Each destination is its own
	// queue row, so a mail server that hangs holds up one worker, not the
	// pager behind it.
	deliverWorkers = 8
	// rateLimitDefer is how long an outage alert waits when the hourly
	// notification limit is spent.
	rateLimitDefer = 5 * time.Minute
)

// retryDelays is the wait after each failed attempt; the last repeats.
var retryDelays = []time.Duration{
	5 * time.Second, 15 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute,
}

func retryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > len(retryDelays) {
		attempts = len(retryDelays)
	}
	return retryDelays[attempts-1]
}

// queued is the part of a Payload kept in the queue. It leaves out the
// monitor's configuration, which holds credentials the alert never needs.
type queued struct {
	MonitorID       int64     `json:"monitor_id"`
	Name            string    `json:"name"`
	MonitorType     string    `json:"monitor_type"`
	Target          string    `json:"target"`
	EscalateMinutes int       `json:"escalate_minutes,omitempty"`
	Type            string    `json:"type"`
	Of              string    `json:"of,omitempty"`
	Key             string    `json:"key,omitempty"`
	At              time.Time `json:"at"`
	Detail          string    `json:"detail,omitempty"`
	ExtraLatency    *float64  `json:"latency_ms,omitempty"`
}

func pack(p Payload) ([]byte, error) {
	return json.Marshal(queued{
		MonitorID: p.Monitor.ID, Name: p.Monitor.Name, MonitorType: p.Monitor.Type, Target: p.Monitor.Target,
		EscalateMinutes: p.Monitor.EscalateMinutes,
		Type:            p.Type, Of: p.Of, Key: p.Key, At: p.At, Detail: p.Detail, ExtraLatency: p.ExtraLatency,
	})
}

func unpack(raw []byte) (Payload, error) {
	var q queued
	if err := json.Unmarshal(raw, &q); err != nil {
		return Payload{}, err
	}
	return Payload{
		Monitor: store.Monitor{ID: q.MonitorID, Name: q.Name, Type: q.MonitorType, Target: q.Target, EscalateMinutes: q.EscalateMinutes},
		Type:    q.Type, Of: q.Of, Key: q.Key, At: q.At, Detail: q.Detail, ExtraLatency: q.ExtraLatency,
	}, nil
}

// mustArrive says whether an alert opens or closes something on a pager:
// a monitor, or the probe, going down or coming back, or a slow monitor
// coming back under its threshold. These wait when the hourly limit is
// spent; dropping one leaves an outage unreported or an incident open with
// nothing left to close it.
func mustArrive(p Payload) bool {
	switch p.Type {
	case "down", "recovery", "probe_down", "probe_up":
		return true
	}
	return false
}

// stream is the problem an alert is about, as a pager groups it. Alerts in
// one stream are delivered in order and replace each other. A channel test
// stands alone.
func stream(p Payload) string {
	if p.Type == "test" {
		return ""
	}
	return alertKey(p)
}

// enqueue stores one queue row per destination the alert should reach, in
// one transaction with claim, the right to send it. It reports false when
// the claim was not ours and nothing was stored. A nil claim always holds.
func (d *Dispatcher) enqueue(ctx context.Context, p Payload, claim store.AlertClaim) (bool, error) {
	chs, err := d.store.ListChannels(ctx)
	if err != nil {
		return false, err
	}
	raw, err := pack(p)
	if err != nil {
		return false, err
	}
	key, resolves := stream(p), resolving(p.Type)
	var items []store.OutboxItem
	for _, ch := range SelectChannels(chs, p.Monitor.ChannelIDs, p.Type) {
		if ch.Type == store.ChannelEmail && len(SplitRecipients(ch.Dest)) == 0 {
			continue
		}
		id := ch.ID
		items = append(items, store.OutboxItem{MonitorID: p.Monitor.ID, Type: p.Type, ChannelID: &id, Dest: ch.Name, Stream: key, Resolves: resolves, Payload: raw})
	}
	if extra := d.escalationExtras(p); len(extra) > 0 {
		items = append(items, store.OutboxItem{MonitorID: p.Monitor.ID, Type: p.Type, Dest: strings.Join(extra, ","), Stream: key, Resolves: resolves, Payload: raw})
	}
	// A recovery goes wherever the outage went. A channel that was sent the
	// alert and has since been taken off the monitor would otherwise keep it
	// open for good.
	if resolves {
		owed, err := d.store.UnresolvedChannels(ctx, key)
		if err != nil {
			return false, err
		}
		for _, ch := range chs {
			if !slices.Contains(owed, ch.ID) || !ch.Allows(p.Type) ||
				slices.ContainsFunc(items, func(it store.OutboxItem) bool { return it.ChannelID != nil && *it.ChannelID == ch.ID }) {
				continue
			}
			id := ch.ID
			items = append(items, store.OutboxItem{MonitorID: p.Monitor.ID, Type: p.Type, ChannelID: &id, Dest: ch.Name, Stream: key, Resolves: resolves, Payload: raw})
		}
	}
	// With nowhere to send it the claim is still taken: the alert has had
	// its turn, and is recorded as having gone nowhere.
	claimed, err := d.store.EnqueueAlerts(ctx, items, d.now().UTC(), claim)
	if err != nil || !claimed {
		return claimed, err
	}
	if len(items) == 0 {
		return true, d.store.InsertAlert(ctx, p.Monitor.ID, p.Type, "skipped", "", "no notification channels configured")
	}
	return true, nil
}

// Run delivers queued alerts until ctx ends: the retries that have come due,
// and anything left behind by a process that stopped mid-send. The api and
// the probe each run one; an alert is claimed by whichever gets to it.
func (d *Dispatcher) Run(ctx context.Context) {
	d.DeliverDue(ctx)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.DeliverDue(ctx)
		}
	}
}

// DeliverDue sends every queued alert that is due and may be sent, and
// returns when none is left and none is in flight.
//
// One drain runs at a time in a process, with up to deliverWorkers sends in
// flight. It takes alerts one at a time and goes on looking while sends are
// in flight, so an alert queued while a destination hangs is taken at once
// and does not wait for that send to give up. A caller that finds a drain
// running wakes it and returns.
func (d *Dispatcher) DeliverDue(ctx context.Context) {
	d.drainMu.Lock()
	if d.draining {
		select {
		case d.wake <- struct{}{}:
		default:
		}
		d.drainMu.Unlock()
		return
	}
	d.draining = true
	d.drainMu.Unlock()
	defer recoverSend("queued alerts", "")

	done := make(chan struct{}, deliverWorkers)
	inFlight := 0
	finish := func() {
		for ; inFlight > 0; inFlight-- {
			<-done
		}
		d.drainMu.Lock()
		d.draining = false
		d.drainMu.Unlock()
	}
	for {
		if ctx.Err() != nil {
			finish()
			return
		}
		if inFlight < deliverWorkers {
			it, err := d.store.ClaimDueAlert(ctx, d.now().UTC(), sendLease)
			if err != nil && ctx.Err() == nil {
				slog.Error("alert queue could not be read", "error", err)
			}
			if err == nil && it != nil {
				inFlight++
				go func() {
					defer func() { done <- struct{}{} }()
					defer recoverSend("alert", it.Dest)
					d.deliver(ctx, *it)
				}()
				continue
			}
		}
		if inFlight == 0 {
			// Nothing to take and nothing in flight. A caller that queued
			// an alert a moment ago left a wake under this same lock, so it
			// is either seen here or the caller finds no drain running and
			// starts its own.
			d.drainMu.Lock()
			select {
			case <-d.wake:
				d.drainMu.Unlock()
				continue
			default:
			}
			d.draining = false
			d.drainMu.Unlock()
			return
		}
		// Something is in flight. Look again when one finishes, which may
		// free the alert queued behind it, or when a new alert is queued.
		select {
		case <-done:
			inFlight--
		case <-d.wake:
		case <-ctx.Done():
		}
	}
}

// errChannelGone is an alert for a channel that was deleted while it waited.
var errChannelGone = errors.New("the channel was deleted before the alert could be sent")

// deliver makes one attempt at one queued alert and records how it went.
func (d *Dispatcher) deliver(ctx context.Context, it store.OutboxItem) {
	// What is recorded must not be lost because the caller is shutting down.
	note, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	now := d.now().UTC()

	p, err := unpack(it.Payload)
	if err != nil {
		_ = d.store.FinishAlert(note, it.ID, store.OutboxFailed, "unreadable alert: "+err.Error())
		return
	}
	settings, err := d.store.LoadSettings(ctx)
	if err == nil {
		d.decorate(ctx, &p, settings)
		if max := settings.General.NotifyMaxPerHour; max > 0 {
			if n, err := d.store.CountSentAlertsSince(ctx, now.Add(-time.Hour)); err == nil && n >= max {
				d.rateLimited(note, it, p, now)
				return
			}
		}
	}

	// A newer alert in this stream says the opposite: the monitor recovered
	// while this outage alert waited, or went down again while this recovery
	// did. Sending it now would report something that has stopped being
	// true, and on a pager it would reopen, or close, the wrong thing.
	if overtaken, err := d.store.OvertakenAlert(ctx, it); err == nil && overtaken {
		_ = d.store.FinishAlert(note, it.ID, store.OutboxSuperseded, "")
		slog.Info("alert not sent: a newer one replaced it", "monitor", p.Monitor.Name, "type", p.Type, "to", it.Dest)
		return
	}

	sendCtx, cancelSend := context.WithTimeout(ctx, d.sendTimeout)
	err = d.send(sendCtx, it, p)
	cancelSend()
	switch {
	case err == nil:
		_ = d.store.FinishAlert(note, it.ID, store.OutboxSent, "")
		_ = d.store.InsertAlert(note, p.Monitor.ID, p.Type, "sent", it.Dest, "")
		slog.Info("alert sent", "monitor", p.Monitor.Name, "type", p.Type, "to", it.Dest, "attempt", it.Attempts)
	case errors.Is(err, errChannelGone), now.Sub(it.CreatedAt) >= giveUpAfter:
		_ = d.store.FinishAlert(note, it.ID, store.OutboxFailed, err.Error())
		_ = d.store.InsertAlert(note, p.Monitor.ID, p.Type, "failed", it.Dest, err.Error())
		slog.Error("alert not delivered; giving up", "monitor", p.Monitor.Name, "type", p.Type, "to", it.Dest, "attempts", it.Attempts, "error", err)
	default:
		wait := retryDelay(it.Attempts)
		_ = d.store.RetryAlert(note, it.ID, now.Add(wait), err.Error())
		if it.Attempts == 1 {
			// Say so in the monitor's alert history now, not a day from now.
			_ = d.store.InsertAlert(note, p.Monitor.ID, p.Type, "retrying", it.Dest, err.Error())
		}
		slog.Warn("alert not delivered; will retry", "monitor", p.Monitor.Name, "type", p.Type, "to", it.Dest, "attempt", it.Attempts, "in", wait, "error", err)
	}
}

// rateLimited handles an alert whose turn came with the hourly limit spent.
// An outage or a recovery waits for room: dropping it left a monitor down
// with nobody told, or an incident open that nothing would close. Anything
// else is a reminder or a warning, and is skipped as before.
//
// The limit is a brake on floods, not an exact count: sends in flight at
// the same moment each see the same total, so it can be passed by as many
// alerts as there are workers.
func (d *Dispatcher) rateLimited(ctx context.Context, it store.OutboxItem, p Payload, now time.Time) {
	if mustArrive(p) && now.Sub(it.CreatedAt) < giveUpAfter {
		_ = d.store.DeferAlert(ctx, it.ID, now.Add(rateLimitDefer), "waiting: notification rate limit")
		slog.Warn("alert held by the notification rate limit", "monitor", p.Monitor.Name, "type", p.Type, "to", it.Dest)
		return
	}
	_ = d.store.FinishAlert(ctx, it.ID, store.OutboxSkipped, "notification rate limit")
	_ = d.store.InsertAlert(ctx, p.Monitor.ID, p.Type, "skipped", it.Dest, "notification rate limit")
	slog.Warn("alert rate limited", "monitor", p.Monitor.Name, "type", p.Type, "to", it.Dest)
}

// send delivers p to the destination a queue row names.
func (d *Dispatcher) send(ctx context.Context, it store.OutboxItem, p Payload) error {
	if it.ChannelID == nil {
		return d.mailer.SendEmail(ctx, buildEmail(p), SplitRecipients(it.Dest))
	}
	ch, err := d.store.GetChannel(ctx, *it.ChannelID)
	if errors.Is(err, store.ErrNotFound) {
		return errChannelGone
	}
	if err != nil {
		return err
	}
	if ch.Type == store.ChannelEmail {
		return d.mailer.SendEmail(ctx, buildEmail(p), SplitRecipients(ch.Dest))
	}
	return Deliver(ctx, *ch, p)
}
