package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// AlertClaim is the right to send one alert, taken inside the transaction
// that queues it. It returns false when the alert is not this caller's to
// send: someone else took it, or it is no longer true.
//
// Taking the claim and queuing the alert together is what keeps alerts in
// order. A monitor's state row is locked while its status changes, so a
// claim that reads the status cannot be passed by a change of status, and
// the alert it queues cannot land behind one for a later change.
type AlertClaim func(ctx context.Context, tx pgx.Tx) (bool, error)

func claimRow(ctx context.Context, tx pgx.Tx, sql string, args ...any) (bool, error) {
	tag, err := tx.Exec(ctx, sql, args...)
	return tag.RowsAffected() == 1, err
}

// ClaimToldDown claims the alert that a monitor is down: it must be down
// now, and its recipients not yet told.
func ClaimToldDown(monitorID int64, now time.Time) AlertClaim {
	return func(ctx context.Context, tx pgx.Tx) (bool, error) {
		return claimRow(ctx, tx, `UPDATE monitor_state SET told_down = TRUE, last_alert_at = $2
			WHERE monitor_id = $1 AND status = 'down' AND NOT told_down`, monitorID, now)
	}
}

// ClaimToldUp claims the alert that a monitor is back: it must be up now,
// and its recipients have been told it was down. A monitor whose outage was
// never reported has no recovery to report.
func ClaimToldUp(monitorID int64) AlertClaim {
	return func(ctx context.Context, tx pgx.Tx) (bool, error) {
		return claimRow(ctx, tx, `UPDATE monitor_state SET told_down = FALSE
			WHERE monitor_id = $1 AND status = 'up' AND told_down`, monitorID)
	}
}

// checkedSinceResume is the SQL for "this monitor's status is from a check
// made since it was last switched on". Until then it shows what it showed
// when it was paused.
const checkedSinceResume = `(resumed_at IS NULL OR last_check_at > resumed_at)`

// ClaimEscalation claims a down monitor's one escalation. Its recipients
// must have been told it is down first: "still down" is not how anyone
// should learn of an outage, and on a pager the first alert is the one that
// pages.
func ClaimEscalation(monitorID int64) AlertClaim {
	return func(ctx context.Context, tx pgx.Tx) (bool, error) {
		return claimRow(ctx, tx, `UPDATE monitor_state SET escalated_at = now()
			WHERE monitor_id = $1 AND status = 'down' AND told_down AND escalated_at IS NULL
			  AND `+checkedSinceResume, monitorID)
	}
}

// ClaimResend claims a down monitor's turn for a "still down" reminder, on
// the same condition.
func ClaimResend(monitorID int64, now time.Time, every time.Duration) AlertClaim {
	return func(ctx context.Context, tx pgx.Tx) (bool, error) {
		return claimRow(ctx, tx, `UPDATE monitor_state SET last_alert_at = $2
			WHERE monitor_id = $1 AND status = 'down' AND told_down
			  AND (last_alert_at IS NULL OR last_alert_at <= $3)
			  AND `+checkedSinceResume, monitorID, now, now.Add(-every))
	}
}

// ClaimCertWarning claims the one warning for a certificate expiring at
// expires, the date read a moment ago. Taken with the alert it queues: a
// claim made on its own and followed by a crash spent the warning on nobody,
// with nothing to offer it again.
func ClaimCertWarning(monitorID int64, expires, now time.Time) AlertClaim {
	return func(ctx context.Context, tx pgx.Tx) (bool, error) {
		return claimRow(ctx, tx, `UPDATE monitor_state SET tls_warned_expiry = tls_expires_at,
				tls_warned_expiries = (ARRAY(SELECT e FROM unnest(tls_warned_expiries) e WHERE e > $3 ORDER BY e DESC LIMIT 15) || tls_expires_at)
			WHERE monitor_id = $1 AND tls_expires_at = $2
			  AND NOT (tls_expires_at = ANY(tls_warned_expiries))`, monitorID, expires, now.Add(-30*24*time.Hour))
	}
}

// ClaimDomainWarning is ClaimCertWarning for a domain registration.
func ClaimDomainWarning(monitorID int64, expires time.Time) AlertClaim {
	return func(ctx context.Context, tx pgx.Tx) (bool, error) {
		return claimRow(ctx, tx, `UPDATE monitor_state SET expiry_warned = expires_at
			WHERE monitor_id = $1 AND expires_at = $2 AND expiry_warned IS DISTINCT FROM expires_at`, monitorID, expires)
	}
}

// ClaimProbeAlerted claims the alert that the probe stopped, or returned.
func ClaimProbeAlerted(down bool) AlertClaim {
	return func(ctx context.Context, tx pgx.Tx) (bool, error) {
		return claimRow(ctx, tx, `UPDATE probe_heartbeat SET alerted_down = $1
			WHERE id = $2 AND alerted_down <> $1`, down, ProbeHeartbeatID)
	}
}

// ClaimProbeNodeAlerted claims the alert that a probe node stopped, or returned.
func ClaimProbeNodeAlerted(nodeID int64, down bool) AlertClaim {
	return func(ctx context.Context, tx pgx.Tx) (bool, error) {
		return claimRow(ctx, tx, `UPDATE probe_nodes SET alerted_down = $2
			WHERE id = $1 AND alerted_down <> $2`, nodeID, down)
	}
}

// OwedAlert is a monitor whose recipients, or whose incident, are out of
// step with it.
type OwedAlert struct {
	MonitorID int64
	Status    string
	// ToldDown is what the recipients last heard.
	ToldDown bool
	// IncidentOwed is set when it went down while no incident could be opened.
	IncidentOwed bool
	DownSince    *time.Time
}

// OwedAlerts lists enabled monitors that are down with nobody told, up with
// everybody still told they are down, or down without the incident they are
// owed. Whether anything is still suppressing them is the caller's to check.
//
// A monitor switched back on is left out until it has been checked: until
// then its status is the one it was paused with.
func (s *Store) OwedAlerts(ctx context.Context) ([]OwedAlert, error) {
	rows, err := s.Pool.Query(ctx, `SELECT m.id, s.status, s.told_down, s.incident_owed, s.down_since
		FROM monitors m JOIN monitor_state s ON s.monitor_id = m.id
		WHERE m.enabled AND m.type <> 'group'
		  AND (s.resumed_at IS NULL OR s.last_check_at > s.resumed_at)
		  AND ((s.status = 'down' AND (NOT s.told_down OR s.incident_owed))
		    OR (s.status = 'up' AND s.told_down))
		ORDER BY m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OwedAlert
	for rows.Next() {
		var o OwedAlert
		if err := rows.Scan(&o.MonitorID, &o.Status, &o.ToldDown, &o.IncidentOwed, &o.DownSince); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// SetIncidentOwed records that a monitor went down while its incident could
// not be opened, or that the debt is settled.
func (s *Store) SetIncidentOwed(ctx context.Context, monitorID int64, owed bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE monitor_state SET incident_owed = $2 WHERE monitor_id = $1 AND incident_owed <> $2`, monitorID, owed)
	return err
}

// markResumed notes that a monitor has just been switched back on. Pausing
// changes nothing that is owed: a monitor paused while down with nobody told
// is still owed its alert if the first check after it resumes finds it down,
// and one that was reported down is still owed its recovery.
//
// The caller has already established that the monitor is this
// organization's and that it was off until now; setEnabled does both and
// this in one statement.
func (s *Store) markResumed(ctx context.Context, monitorID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE monitor_state SET resumed_at = now() WHERE monitor_id = $1`, monitorID)
	return err
}

// OpenOwedIncident opens the incident a down monitor is owed and settles
// the debt, together, or does neither. The monitor's state row is locked
// while it looks, the same row a check locks to change the status, so the
// monitor cannot recover between the look and the incident: either it is
// down and gets its incident, which the recovery that follows will resolve,
// or it has already recovered and gets none. An error leaves the debt
// standing for the next sweep.
func (s *Store) OpenOwedIncident(ctx context.Context, monitorID int64, name, detail string, at time.Time) (*Incident, bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM monitor_state WHERE monitor_id=$1 FOR UPDATE`, monitorID).Scan(&status); err != nil {
		if err == pgxErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	var inc *Incident
	opened := false
	if status == "down" {
		if inc, opened, err = openAutoIncident(ctx, tx, monitorID, name, detail, at); err != nil {
			return nil, false, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE monitor_state SET incident_owed = FALSE WHERE monitor_id=$1`, monitorID); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return inc, opened, nil
}

// ToldDownAmong returns those of the given monitors whose recipients have
// been told they are down, with what an alert about them needs. Asked before
// they are deleted, so that what was opened for them can be closed.
func (s *Store) ToldDownAmong(ctx context.Context, ids []int64) ([]Monitor, error) {
	rows, err := s.Pool.Query(ctx, `SELECT m.id, m.name, m.type, m.target, m.channel_ids
		FROM monitors m JOIN monitor_state s ON s.monitor_id = m.id
		WHERE m.id = ANY($1) AND s.told_down AND ($2 = 0 OR m.org_id = $2)`, ids, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Monitor
	for rows.Next() {
		var m Monitor
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Target, &m.ChannelIDs); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
