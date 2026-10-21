package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"argus/internal/alerting"
	"argus/internal/store"
)

// Applier turns one location's check into state, events, incidents, and
// alerts. The local probe uses it for its own checks and the API for results
// that remote probe nodes send in, so a monitor alerts the same way whichever
// location saw it fail.
type Applier struct {
	store    *store.Store
	dispatch *alerting.Dispatcher
}

func NewApplier(st *store.Store, disp *alerting.Dispatcher) *Applier {
	return &Applier{store: st, dispatch: disp}
}

// Apply records a check that nodeID ran and acts on any change it causes.
func (a *Applier) Apply(ctx context.Context, m store.Monitor, nodeID int64, outcome CheckOutcome, pingErr error) {
	success := pingErr == nil

	res := store.PingResult{
		MonitorID:    m.ID,
		TS:           time.Now().UTC(),
		Success:      success,
		TLSExpiresAt: outcome.TLSExpiresAt,
		ExpiresAt:    outcome.ExpiresAt,
	}
	if success && !outcome.NoLatency {
		v := outcome.LatencyMs
		res.LatencyMs = &v
	} else if !success {
		ev := outcome.Evidence
		res.Evidence = &ev
		if outcome.LatencyMs > 0 {
			v := outcome.LatencyMs
			res.LatencyMs = &v
		}
	}

	errText := ""
	if pingErr != nil {
		errText = pingErr.Error()
	}
	transition, err := a.store.ApplyResult(ctx, res, store.ApplyOptions{
		NodeID:          nodeID,
		Retries:         m.Retries,
		ThresholdMs:     m.LatencyThresholdMs,
		IntervalSeconds: m.IntervalSeconds,
		Locations:       store.Locations(m),
		Confirm:         m.ConfirmLocations,
		ErrorText:       errText,
	})
	if err != nil {
		slog.Error("apply result failed", "monitor", m.Name, "error", err)
		return
	}

	payload := wsResult{
		Type:         "result",
		MonitorID:    m.ID,
		TS:           res.TS.Format(time.RFC3339Nano),
		LatencyMs:    res.LatencyMs,
		Success:      success,
		Status:       transition.NewStatus,
		Slow:         transition.Slow,
		TLSExpiresAt: rfc3339Ptr(outcome.TLSExpiresAt),
	}
	alertType, eventType, detail := transitionAlert(transition, success, outcome.LatencyMs, pingErr)
	if eventType == "down" {
		detail = locationDetail(transition) + detail
	}
	if eventType != "" {
		payload.Event = eventType
	}
	if raw, err := json.Marshal(payload); err == nil {
		if err := a.store.PublishEvent(ctx, string(raw)); err != nil {
			slog.Warn("publish event failed", "error", err)
		}
	}

	if eventType != "" {
		_ = a.store.InsertEvent(ctx, m.ID, eventType, detail)
		slog.Info("monitor state changed", "monitor", m.Name, "from", transition.OldStatus, "to", transition.NewStatus, "slow", transition.Slow, "event", eventType)

		maint, err := a.store.MaintenanceActive(ctx, m.ID, res.TS)
		if err != nil {
			slog.Error("maintenance check failed", "error", err)
		}
		muted, _, err := a.store.AlertSilence(ctx, m.ID, res.TS)
		if err != nil {
			slog.Error("silence check failed", "error", err)
		}

		if eventType == "down" {
			if maint || muted {
				// No incident while it is suppressed. If it is still down
				// when that ends, the probe opens one then (Engine.owedSweep).
				_ = a.store.SetIncidentOwed(ctx, m.ID, true)
			} else {
				inc, opened, err := a.store.OpenAutoIncident(ctx, m.ID, m.Name, detail, res.TS)
				if err != nil {
					slog.Error("open incident failed", "monitor", m.Name, "error", err)
				} else if opened && inc != nil {
					a.dispatch.NotifyIncidentAsync(*inc)
				}
			}
		}
		if eventType == "up" && transition.OldStatus == "down" {
			_ = a.store.SetIncidentOwed(ctx, m.ID, false)
			inc, err := a.store.ResolveAutoIncident(ctx, m.ID, res.TS)
			if err != nil {
				slog.Error("resolve incident failed", "monitor", m.Name, "error", err)
			} else if inc != nil {
				a.dispatch.NotifyIncidentAsync(*inc)
			}
		}

		reason := ""
		switch {
		case maint:
			reason = "maintenance window"
		case muted:
			reason = "mute"
		case a.dependencyDown(ctx, m):
			reason = "dependency"
		}
		p := alerting.Payload{
			Monitor:      m,
			Type:         alertType,
			Of:           recoveryOf(alertType, transition),
			At:           res.TS,
			Detail:       detail,
			ExtraLatency: res.LatencyMs,
		}
		// An outage beginning or ending is sent against what the recipients
		// were last told, recorded with the alert as it is queued. Nothing
		// is recorded when it is suppressed, so the monitor is left down
		// with nobody told, or up with everybody still told it is down, and
		// the probe sends what is owed once the window, mute or parent
		// outage is over.
		switch {
		case reason != "":
			slog.Info("alert not sent now: suppressed by "+reason, "monitor", m.Name, "type", alertType)
		case alertType == "down":
			a.dispatch.Dispatch(p, store.ClaimToldDown(m.ID, res.TS))
		case alertType == "recovery" && transition.OldStatus == "down":
			// Not claimed when the outage was never reported: nobody was
			// told it was down, so nobody is told it is back.
			if !a.dispatch.Dispatch(p, store.ClaimToldUp(m.ID)) {
				slog.Info("recovery not sent: its outage was never reported", "monitor", m.Name)
			}
		default:
			a.dispatch.DispatchAsync(p)
		}
	}

	// It came back from an outage and over its latency threshold at once.
	// The recovery went out above; this is the slowness.
	if eventType == "up" && transition.OldStatus == "down" && transition.BecameSlow {
		_ = a.store.InsertEvent(ctx, m.ID, "slow", detail)
		if a.suppressedBy(ctx, m, res.TS) == "" {
			a.dispatch.DispatchAsync(alerting.Payload{Monitor: m, Type: "slow", At: res.TS, Detail: detail, ExtraLatency: res.LatencyMs})
		}
	}

	a.maybeCertWarn(ctx, m, res.TS, outcome.TLSExpiresAt != nil)
	a.maybeDomainWarn(ctx, m, res.TS, outcome.ExpiresAt != nil)
}

func (a *Applier) maybeCertWarn(ctx context.Context, m store.Monitor, at time.Time, sawTLS bool) {
	if (m.Type != "http" && m.Type != "cert") || !sawTLS {
		return
	}
	maint, err := a.store.MaintenanceActive(ctx, m.ID, at)
	if err != nil {
		slog.Error("cert maintenance check failed", "monitor", m.Name, "error", err)
		return
	}
	if maint {
		return
	}
	muted, _, err := a.store.AlertSilence(ctx, m.ID, at)
	if err != nil {
		slog.Error("cert silence check failed", "monitor", m.Name, "error", err)
		return
	}
	if muted {
		return
	}
	expires, err := a.store.CertWarningDue(ctx, m.ID, at, m.Config.WarnDays)
	if err != nil {
		slog.Error("cert warn check failed", "monitor", m.Name, "error", err)
		return
	}
	if expires == nil {
		return
	}
	days := int(expires.Sub(at).Hours() / 24)
	var detail string
	if days < 0 {
		detail = fmt.Sprintf("certificate expired %s", expires.Format("2006-01-02"))
	} else {
		detail = fmt.Sprintf("certificate expires %s (%d days)", expires.Format("2006-01-02"), days)
	}
	// The warning is spent as its alert is queued, not before: spent first,
	// a stop at the wrong moment left it warned to nobody for good.
	if a.dispatch.Dispatch(alerting.Payload{Monitor: m, Type: "cert", At: at, Detail: detail},
		store.ClaimCertWarning(m.ID, *expires, at)) {
		_ = a.store.InsertEvent(ctx, m.ID, "cert", detail)
	}
}

func (a *Applier) maybeDomainWarn(ctx context.Context, m store.Monitor, at time.Time, sawExpiry bool) {
	if m.Type != "domain" || !sawExpiry {
		return
	}
	if maint, err := a.store.MaintenanceActive(ctx, m.ID, at); err != nil || maint {
		return
	}
	if muted, _, err := a.store.AlertSilence(ctx, m.ID, at); err != nil || muted {
		return
	}
	warnDays := m.Config.WarnDays
	expires, err := a.store.DomainWarningDue(ctx, m.ID, at, warnDays)
	if err != nil {
		slog.Error("domain warn check failed", "monitor", m.Name, "error", err)
		return
	}
	if expires == nil {
		return
	}
	days := int(expires.Sub(at).Hours() / 24)
	detail := fmt.Sprintf("domain registration expires %s (%d days)", expires.Format("2006-01-02"), days)
	if days < 0 {
		detail = fmt.Sprintf("domain registration expired %s", expires.Format("2006-01-02"))
	}
	if a.dispatch.Dispatch(alerting.Payload{Monitor: m, Type: "domain", At: at, Detail: detail},
		store.ClaimDomainWarning(m.ID, *expires)) {
		_ = a.store.InsertEvent(ctx, m.ID, "domain", detail)
	}
}

// suppressedBy says what is holding a monitor's alerts back right now, or ""
// when nothing is.
func (a *Applier) suppressedBy(ctx context.Context, m store.Monitor, now time.Time) string {
	if maint, err := a.store.MaintenanceActive(ctx, m.ID, now); err == nil && maint {
		return "maintenance window"
	}
	if muted, _, err := a.store.AlertSilence(ctx, m.ID, now); err == nil && muted {
		return "mute"
	}
	if a.dependencyDown(ctx, m) {
		return "dependency"
	}
	return ""
}

// recoveryOf says what a recovery is a recovery from, so it closes the
// alert that problem opened and no other.
func recoveryOf(alertType string, t store.StateTransition) string {
	if alertType == "recovery" && t.RecoveredSlow && t.OldStatus != "down" {
		return "slow"
	}
	return ""
}

func (a *Applier) dependencyDown(ctx context.Context, m store.Monitor) bool {
	if m.DependsOn == nil || *m.DependsOn <= 0 {
		return false
	}
	down, err := a.store.WatchedAndDown(ctx, *m.DependsOn)
	return err == nil && down
}

// locationDetail says where a monitor was seen down, for monitors checked
// from more than one place: "down from 2 of 3 locations (Frankfurt, Virginia): ".
func locationDetail(t store.StateTransition) string {
	if t.Locations <= 1 || len(t.DownLocations) == 0 {
		return ""
	}
	return fmt.Sprintf("down from %d of %d locations (%s): ",
		len(t.DownLocations), t.Reporting, strings.Join(t.DownLocations, ", "))
}
