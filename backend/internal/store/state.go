package store

import (
	"context"
	"errors"
	"time"
)

type StateTransition struct {
	Changed       bool
	OldStatus     string
	NewStatus     string
	Slow          bool
	BecameSlow    bool
	RecoveredSlow bool
	// DownLocations names the locations that see the monitor down; Reporting
	// is how many locations are currently checking it, Needed how many must
	// agree, and Locations how many it is assigned to.
	DownLocations []string
	Reporting     int
	Needed        int
	Locations     int
}

func nextStatus(current string, success bool, consecutiveFails, retries int) (string, bool) {
	if success {
		if current == "up" {
			return "up", false
		}
		return "up", true
	}
	if current == "down" {
		return "down", false
	}
	if consecutiveFails >= retries {
		return "down", true
	}
	return current, false
}

// nextSlow tracks over-limit successes. A timeout/failure is never slow.
// Down is applied by the caller and supersedes. thresholdMs <= 0 disables tracking.
func nextSlow(wasSlow bool, consecutiveSlow, retries, thresholdMs int, success bool, latencyMs *float64) (slow bool, consecutive int) {
	if thresholdMs <= 0 {
		return false, 0
	}
	if retries < 1 {
		retries = 1
	}
	if !success {
		if wasSlow {
			return true, 0
		}
		return false, 0
	}
	over := latencyMs != nil && *latencyMs > float64(thresholdMs)
	if !over {
		return false, 0
	}
	n := consecutiveSlow + 1
	if wasSlow || n >= retries {
		return true, n
	}
	return false, n
}

// ApplyOptions says where a result came from and how the monitor decides.
type ApplyOptions struct {
	// NodeID is the location that ran the check; LocalProbeNodeID is this server.
	NodeID          int64
	Retries         int
	ThresholdMs     int
	IntervalSeconds int
	// Locations are all the places the monitor is checked from, and Confirm
	// is how many of them must see it down.
	Locations []int64
	Confirm   int
	// ErrorText is what the location reports when the check failed.
	ErrorText string
}

// probeAliveWindow is how recently a probe must have been heard from to be
// counted as able to check. Remote nodes ask for work every 30 seconds.
const probeAliveWindow = 2 * time.Minute

// locationStale is how long a location may go without reporting before its
// vote stops counting. A probe node that has gone quiet must neither hold a
// monitor down nor stop it going down.
func locationStale(intervalSeconds int) time.Duration {
	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}
	return time.Duration(2*intervalSeconds+30) * time.Second
}

// ApplyResult records one check from one location and works out the
// monitor's status from every location still reporting. Each location
// counts its own consecutive failures against retries; the monitor is down
// when at least Confirm of the reporting locations are down (fewer, if fewer
// are reporting), and up again once that is no longer true and a location
// reports up. With one location this is the single-probe behaviour exactly.
func (s *Store) ApplyResult(ctx context.Context, r PingResult, o ApplyOptions) (StateTransition, error) {
	retries := o.Retries
	thresholdMs := o.ThresholdMs
	locations := o.Locations
	if len(locations) == 0 {
		locations = []int64{LocalProbeNodeID}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return StateTransition{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	var consecutive int
	var consecutiveSlow int
	var slowSince *time.Time
	var existingDownSince *time.Time
	var existingEscalated any
	if err := tx.QueryRow(ctx, `SELECT status, consecutive_failures, consecutive_slow, slow_since, down_since, escalated_at
		FROM monitor_state WHERE monitor_id=$1 FOR UPDATE`, r.MonitorID).Scan(
		&status, &consecutive, &consecutiveSlow, &slowSince, &existingDownSince, &existingEscalated); err != nil {
		return StateTransition{}, err
	}

	// This location's own confirmed state.
	locStatus, locConsecutive := "pending", 0
	if err := tx.QueryRow(ctx, `SELECT status, consecutive_failures FROM probe_node_state
		WHERE monitor_id=$1 AND node_id=$2`, r.MonitorID, o.NodeID).Scan(&locStatus, &locConsecutive); err != nil && !errors.Is(err, pgxErrNoRows) {
		return StateTransition{}, err
	}
	locStatus, _ = nextStatus(locStatus, r.Success, locConsecutive+1, retries)
	if r.Success {
		locConsecutive = 0
	} else {
		locConsecutive++
	}
	if _, err := tx.Exec(ctx, `INSERT INTO probe_node_state
		(monitor_id, node_id, status, consecutive_failures, last_latency_ms, last_check_at, last_error)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (monitor_id, node_id) DO UPDATE SET
			status=$3, consecutive_failures=$4, last_latency_ms=$5, last_check_at=$6, last_error=$7`,
		r.MonitorID, o.NodeID, locStatus, locConsecutive, r.LatencyMs, r.TS, o.ErrorText); err != nil {
		return StateTransition{}, err
	}

	// Votes from every location that is still reporting.
	rows, err := tx.Query(ctx, `SELECT p.node_id, p.status, p.consecutive_failures, COALESCE(n.name, '')
		FROM probe_node_state p LEFT JOIN probe_nodes n ON n.id = p.node_id
		WHERE p.monitor_id=$1 AND p.node_id = ANY($2) AND p.last_check_at >= $3
		ORDER BY p.node_id`,
		r.MonitorID, locations, r.TS.Add(-locationStale(o.IntervalSeconds)))
	if err != nil {
		return StateTransition{}, err
	}
	var reporting, upVotes, worst int
	var downNames []string
	for rows.Next() {
		var id int64
		var st, name string
		var n int
		if err := rows.Scan(&id, &st, &n, &name); err != nil {
			rows.Close()
			return StateTransition{}, err
		}
		reporting++
		worst = max(worst, n)
		switch st {
		case "down":
			if id == LocalProbeNodeID || name == "" {
				name = "this server"
			}
			downNames = append(downNames, name)
		case "up":
			upVotes++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return StateTransition{}, err
	}
	// How many must agree is capped by how many locations are able to
	// check: a probe that has stopped must not make an outage impossible to
	// confirm. A live probe that has not reached this monitor yet still
	// counts, so the first location to fail cannot decide alone.
	var alive int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM unnest($1::bigint[]) AS l(id)
		WHERE l.id = $2
		   OR (l.id = 0 AND EXISTS (SELECT 1 FROM probe_heartbeat WHERE last_seen >= $3))
		   OR (l.id <> 0 AND EXISTS (SELECT 1 FROM probe_nodes n WHERE n.id = l.id AND n.enabled AND n.last_seen >= $3))`,
		locations, o.NodeID, r.TS.Add(-probeAliveWindow)).Scan(&alive); err != nil {
		return StateTransition{}, err
	}
	checking := max(alive, reporting)
	needed := max(1, min(o.Confirm, alive))

	newStatus := status
	switch {
	case len(downNames) >= needed:
		newStatus = "down"
	case upVotes > 0:
		newStatus = "up"
	}
	changed := newStatus != status
	consecutive = worst

	wasSlow := slowSince != nil

	var slow bool
	if newStatus == "down" {
		slow = false
		consecutiveSlow = 0
	} else {
		slow, consecutiveSlow = nextSlow(wasSlow, consecutiveSlow, retries, thresholdMs, r.Success, r.LatencyMs)
	}

	becameSlow := !wasSlow && slow
	recoveredSlow := wasSlow && !slow && newStatus != "down"

	now := r.TS
	var lastChange any
	if changed || becameSlow || recoveredSlow {
		lastChange = now
	} else {
		lastChange = nil
	}

	var downSince any
	if newStatus == "down" {
		if changed {
			downSince = now
		} else {
			downSince = existingDownSince
		}
	}

	var escalatedAt any
	if changed && newStatus == "down" {
		escalatedAt = nil
	} else {
		escalatedAt = existingEscalated
	}

	var newSlowSince any
	if slow {
		if wasSlow && slowSince != nil {
			newSlowSince = *slowSince
		} else {
			newSlowSince = now
		}
	}

	clearAck := changed && newStatus == "down"
	evRaw := []byte(nil)
	if !r.Success {
		evRaw = encodeCheckEvidence(r.Evidence)
	}

	_, err = tx.Exec(ctx, `UPDATE monitor_state SET
		status=$1, last_latency_ms=$2, last_check_at=$3, consecutive_failures=$4,
		last_change_at=COALESCE($5, last_change_at), down_since=$6, escalated_at=$7,
		expires_at=COALESCE($14, expires_at),
		expiry_warned=CASE
			WHEN $14::timestamptz IS NULL THEN expiry_warned
			WHEN $14 IS DISTINCT FROM expires_at THEN NULL
			ELSE expiry_warned
		END,
		tls_expires_at=COALESCE($8, tls_expires_at),
		consecutive_slow=$9, slow_since=$10,
		acked_at=CASE WHEN $12 THEN NULL ELSE acked_at END,
		acked_until=CASE WHEN $12 THEN NULL ELSE acked_until END,
		last_failure_evidence=CASE WHEN $13::jsonb IS NOT NULL THEN $13 ELSE last_failure_evidence END,
		last_failure_at=CASE WHEN $13::jsonb IS NOT NULL THEN $3 ELSE last_failure_at END
		WHERE monitor_id=$11`,
		newStatus, r.LatencyMs, now, consecutive, lastChange, downSince, escalatedAt, r.TLSExpiresAt,
		consecutiveSlow, newSlowSince, r.MonitorID, clearAck, evRaw, r.ExpiresAt)
	if err != nil {
		return StateTransition{}, err
	}

	if _, err := tx.Exec(ctx, `INSERT INTO monitor_results (monitor_id, ts, latency_ms, success)
		VALUES ($1,$2,$3,$4)`, r.MonitorID, r.TS, r.LatencyMs, r.Success); err != nil {
		return StateTransition{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return StateTransition{}, err
	}
	return StateTransition{
		Changed:       changed,
		OldStatus:     status,
		NewStatus:     newStatus,
		Slow:          slow,
		BecameSlow:    becameSlow,
		RecoveredSlow: recoveredSlow,
		DownLocations: downNames,
		Reporting:     checking,
		Needed:        needed,
		Locations:     len(locations),
	}, nil
}

const DefaultCertWarnDays = 14

// CertWarningDue returns the certificate expiry a monitor should be warned
// about now: the one last seen, if it is within warnDays and has not been
// warned about. Nothing is recorded; ClaimCertWarning does that, with the
// alert.
//
// Every expiry warned about is remembered, not only the last. A name served
// by several machines shows a different certificate from check to check:
// remembering one date repeated the warning each time they alternated, and
// remembering only the latest never warned for the one that expires first.
// Dates more than a month past, and all but the newest few, are forgotten.
func (s *Store) CertWarningDue(ctx context.Context, monitorID int64, now time.Time, warnDays int) (*time.Time, error) {
	if warnDays <= 0 {
		warnDays = DefaultCertWarnDays
	}
	return s.expiryDue(ctx, `SELECT tls_expires_at FROM monitor_state
		WHERE monitor_id = $1 AND tls_expires_at IS NOT NULL AND tls_expires_at <= $2
		  AND NOT (tls_expires_at = ANY(tls_warned_expiries))`, monitorID, now, warnDays)
}

// DefaultDomainWarnDays is how far ahead a domain expiry is worth shouting
// about: renewals need more lead time than certificate renewals.
const DefaultDomainWarnDays = 30

// DomainWarningDue is CertWarningDue for a domain registration expiry.
func (s *Store) DomainWarningDue(ctx context.Context, monitorID int64, now time.Time, warnDays int) (*time.Time, error) {
	if warnDays <= 0 {
		warnDays = DefaultDomainWarnDays
	}
	return s.expiryDue(ctx, `SELECT expires_at FROM monitor_state
		WHERE monitor_id = $1 AND expires_at IS NOT NULL AND expires_at <= $2
		  AND expiry_warned IS DISTINCT FROM expires_at`, monitorID, now, warnDays)
}

func (s *Store) expiryDue(ctx context.Context, sql string, monitorID int64, now time.Time, warnDays int) (*time.Time, error) {
	var expires time.Time
	err := s.Pool.QueryRow(ctx, sql, monitorID, now.Add(time.Duration(warnDays)*24*time.Hour)).Scan(&expires)
	if err == pgxErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &expires, nil
}

func (s *Store) MaintenanceActive(ctx context.Context, monitorID int64, now time.Time) (bool, error) {
	var exists bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM maintenance_windows
		WHERE (monitor_id IS NULL OR monitor_id = $1) AND starts_at <= $2 AND ends_at >= $2
	)`, monitorID, now).Scan(&exists)
	return exists, err
}

type EscalationCandidate struct {
	MonitorID  int64
	Name       string
	Target     string
	DownSince  time.Time
	Recipients string
	DependsOn  *int64
}

func (s *Store) EscalationDue(ctx context.Context, now time.Time) ([]EscalationCandidate, error) {
	rows, err := s.Pool.Query(ctx, `SELECT m.id, m.name, m.target, s.down_since, m.escalate_recipients, m.depends_on
		FROM monitors m JOIN monitor_state s ON s.monitor_id = m.id
		WHERE s.status='down' AND m.escalate_minutes > 0 AND s.escalated_at IS NULL
		AND s.down_since IS NOT NULL AND s.down_since + make_interval(mins => m.escalate_minutes) <= $1
		AND (s.muted_until IS NULL OR s.muted_until <= $1)
		AND (s.acked_at IS NULL OR (s.acked_until IS NOT NULL AND s.acked_until <= $1))`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EscalationCandidate
	for rows.Next() {
		var c EscalationCandidate
		if err := rows.Scan(&c.MonitorID, &c.Name, &c.Target, &c.DownSince, &c.Recipients, &c.DependsOn); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ResendDue(ctx context.Context, now time.Time) ([]Monitor, error) {
	rows, err := s.Pool.Query(ctx, `SELECT m.id, m.name, m.type, m.target, m.interval_seconds, m.retries,
		m.escalate_minutes, m.escalate_recipients, m.enabled, m.tags, m.config, m.created_at,
		m.latency_threshold_ms, m.channel_ids, m.resend_minutes, m.parent_id, m.depends_on
		FROM monitors m JOIN monitor_state s ON s.monitor_id = m.id
		WHERE s.status='down' AND m.enabled AND m.resend_minutes > 0
		AND (s.last_alert_at IS NULL OR s.last_alert_at + make_interval(mins => m.resend_minutes) <= $1)
		AND (s.muted_until IS NULL OR s.muted_until <= $1)
		AND (s.acked_at IS NULL OR (s.acked_until IS NOT NULL AND s.acked_until <= $1))`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Monitor
	for rows.Next() {
		var m Monitor
		var cfg []byte
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Target, &m.IntervalSeconds, &m.Retries,
			&m.EscalateMinutes, &m.EscalateRecipients, &m.Enabled, &m.Tags, &cfg, &m.CreatedAt,
			&m.LatencyThresholdMs, &m.ChannelIDs, &m.ResendMinutes, &m.ParentID, &m.DependsOn); err != nil {
			return nil, err
		}
		m.Config = decodeMonitorConfig(cfg)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ListMaintenanceWindows(ctx context.Context) ([]MaintenanceWindow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, monitor_id, starts_at, ends_at, note, public, created_at
		FROM maintenance_windows WHERE ($1 = 0 OR org_id=$1) ORDER BY starts_at DESC`, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MaintenanceWindow
	for rows.Next() {
		var w MaintenanceWindow
		if err := rows.Scan(&w.ID, &w.MonitorID, &w.StartsAt, &w.EndsAt, &w.Note, &w.Public, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) CreateMaintenanceWindow(ctx context.Context, w *MaintenanceWindow) error {
	return s.Pool.QueryRow(ctx, `INSERT INTO maintenance_windows (monitor_id, starts_at, ends_at, note, public, org_id)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, created_at`,
		w.MonitorID, w.StartsAt, w.EndsAt, w.Note, w.Public, writeOrg(ctx)).Scan(&w.ID, &w.CreatedAt)
}

// PublicMaintenanceWindows are the announced windows a status page shows:
// anything running now, and anything starting within the next two weeks.
func (s *Store) PublicMaintenanceWindows(ctx context.Context, monitorIDs []int64, now time.Time) ([]PublicMaintenance, error) {
	rows, err := s.Pool.Query(ctx, `SELECT w.starts_at, w.ends_at, w.note, COALESCE(m.name, '')
		FROM maintenance_windows w
		LEFT JOIN monitors m ON m.id = w.monitor_id
		WHERE w.public
		  AND w.ends_at > $1
		  AND w.starts_at < $2
		  AND (w.monitor_id IS NULL OR $3::bigint[] IS NULL OR w.monitor_id = ANY($3))
		ORDER BY w.starts_at ASC`,
		now, now.Add(14*24*time.Hour), monitorIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PublicMaintenance{}
	for rows.Next() {
		var w PublicMaintenance
		if err := rows.Scan(&w.StartsAt, &w.EndsAt, &w.Note, &w.Monitor); err != nil {
			return nil, err
		}
		w.Active = !w.StartsAt.After(now) && w.EndsAt.After(now)
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) DeleteMaintenanceWindow(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM maintenance_windows WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, id, OrgID(ctx))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
