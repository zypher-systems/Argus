package store

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"argus/internal/secrets"
)

var ErrNotFound = errors.New("not found")

func scanMonitorWithState(scan func(dest ...any) error) (MonitorWithState, error) {
	var m MonitorWithState
	var tags string
	var cfg []byte
	var evRaw []byte
	err := scan(
		&m.ID, &m.Name, &m.Type, &m.Target, &m.IntervalSeconds, &m.Retries,
		&m.EscalateMinutes, &m.EscalateRecipients, &m.Enabled, &tags, &cfg, &m.CreatedAt,
		&m.LatencyThresholdMs, &m.ResendMinutes, &m.ParentID, &m.DependsOn,
		&m.State.Status, &m.State.LastLatencyMs, &m.State.LastCheckAt, &m.State.LastChangeAt,
		&m.State.ConsecutiveFails, &m.State.DownSince, &m.State.EscalatedAt, &m.State.Uptime24h,
		&m.State.TLSExpiresAt, &m.State.SlowSince, &m.ChannelIDs, &m.SLOTarget, &m.ProbeNodeIDs,
		&m.State.MutedUntil, &m.State.AckedAt, &m.State.AckedUntil, &evRaw, &m.State.LastFailureAt,
		&m.LastPushAt, &m.State.ExpiresAt, &m.ConfirmLocations, &m.OrgID,
	)
	m.Tags = tags
	if err != nil {
		return m, err
	}
	m.Config = decodeMonitorConfig(cfg)
	m.State.LastFailureEvidence = decodeCheckEvidence(evRaw)
	m.State.Slow = m.State.SlowSince != nil
	m.State.Stale = MonitorStale(m.Enabled, m.IntervalSeconds, m.State.LastCheckAt, time.Now())
	return m, nil
}

func MonitorStale(enabled bool, intervalSeconds int, lastCheck *time.Time, now time.Time) bool {
	if !enabled || lastCheck == nil || intervalSeconds <= 0 {
		return false
	}
	return now.Sub(*lastCheck) > time.Duration(intervalSeconds*2+15)*time.Second
}

// secretBox encrypts monitor credentials on their way into the database. It
// is process-wide because config encoding happens deep in row scanning; both
// the api and the probe set it at startup from ARGUS_SECRET_KEY.
var secretBox *secrets.Box

// SetSecretBox installs the credential encryption key.
func SetSecretBox(b *secrets.Box) { secretBox = b }

func decodeMonitorConfig(raw []byte) MonitorConfig {
	var cfg MonitorConfig
	if len(raw) == 0 || string(raw) == "null" {
		return cfg
	}
	_ = json.Unmarshal(raw, &cfg)
	// Credentials are stored encrypted when a key is configured. A value that
	// will not decrypt is dropped rather than passed to a driver as
	// ciphertext; the check then fails with an auth error and this line says
	// why.
	for _, f := range []*string{&cfg.AuthPassword, &cfg.AuthToken} {
		if !secrets.IsEncrypted(*f) {
			continue
		}
		plain, err := secretBox.Open(*f)
		if err != nil {
			slog.Error("monitor credential could not be decrypted; check ARGUS_SECRET_KEY", "error", err)
			*f = ""
			continue
		}
		*f = plain
	}
	for name, v := range cfg.Secrets {
		if !secrets.IsEncrypted(v) {
			continue
		}
		plain, err := secretBox.Open(v)
		if err != nil {
			slog.Error("transaction secret could not be decrypted; check ARGUS_SECRET_KEY", "secret", name, "error", err)
			cfg.Secrets[name] = ""
			continue
		}
		cfg.Secrets[name] = plain
	}
	return cfg
}

func encodeMonitorConfig(cfg MonitorConfig) ([]byte, error) {
	if cfg.Headers != nil && len(cfg.Headers) == 0 {
		cfg.Headers = nil
	}
	for _, f := range []*string{&cfg.AuthPassword, &cfg.AuthToken} {
		sealed, err := secretBox.Seal(*f)
		if err != nil {
			return nil, err
		}
		*f = sealed
	}
	if len(cfg.Secrets) > 0 {
		// A copy, so sealing never rewrites the caller's map.
		sealed := make(map[string]string, len(cfg.Secrets))
		for name, v := range cfg.Secrets {
			enc, err := secretBox.Seal(v)
			if err != nil {
				return nil, err
			}
			sealed[name] = enc
		}
		cfg.Secrets = sealed
	} else {
		cfg.Secrets = nil
	}
	return json.Marshal(cfg)
}

func decodeCheckEvidence(raw []byte) *CheckEvidence {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var ev CheckEvidence
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil
	}
	return &ev
}

func encodeCheckEvidence(ev *CheckEvidence) []byte {
	if ev == nil {
		return nil
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return nil
	}
	return b
}

const monitorColumns = `m.id, m.name, m.type, m.target, m.interval_seconds, m.retries,
	m.escalate_minutes, m.escalate_recipients, m.enabled, m.tags, m.config, m.created_at,
	m.latency_threshold_ms, m.resend_minutes, m.parent_id, m.depends_on,
	s.status, s.last_latency_ms, s.last_check_at, s.last_change_at,
	s.consecutive_failures, s.down_since, s.escalated_at, s.uptime_24h, s.tls_expires_at, s.slow_since,
	m.channel_ids, m.slo_target, COALESCE(m.probe_node_ids, '{}'),
	s.muted_until, s.acked_at, s.acked_until, s.last_failure_evidence, s.last_failure_at,
	s.last_push_at, s.expires_at, m.confirm_locations, m.org_id`

func (s *Store) ListMonitors(ctx context.Context) ([]MonitorWithState, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+monitorColumns+`
		FROM monitors m LEFT JOIN monitor_state s ON s.monitor_id = m.id
		WHERE ($1 = 0 OR m.org_id = $1)
		ORDER BY m.name ASC`, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MonitorWithState
	for rows.Next() {
		m, err := scanMonitorWithState(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ListEnabledMonitors(ctx context.Context) ([]Monitor, error) {
	rows, err := s.Pool.Query(ctx, `SELECT m.id, m.name, m.type, m.target, m.interval_seconds, m.retries,
		m.escalate_minutes, m.escalate_recipients, m.enabled, m.tags, m.config, m.created_at,
		m.latency_threshold_ms, m.channel_ids, m.resend_minutes, m.parent_id, m.depends_on, s.last_push_at,
		m.slo_target, COALESCE(m.probe_node_ids, '{}'), m.confirm_locations, m.org_id
		FROM monitors m LEFT JOIN monitor_state s ON s.monitor_id = m.id
		WHERE m.enabled = TRUE ORDER BY m.id`)
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
			&m.LatencyThresholdMs, &m.ChannelIDs, &m.ResendMinutes, &m.ParentID, &m.DependsOn, &m.LastPushAt,
			&m.SLOTarget, &m.ProbeNodeIDs, &m.ConfirmLocations, &m.OrgID); err != nil {
			return nil, err
		}
		m.Config = decodeMonitorConfig(cfg)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetMonitor(ctx context.Context, id int64) (*MonitorWithState, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+monitorColumns+`
		FROM monitors m LEFT JOIN monitor_state s ON s.monitor_id = m.id
		WHERE m.id = $1 AND ($2 = 0 OR m.org_id = $2)`, id, OrgID(ctx))
	m, err := scanMonitorWithState(row.Scan)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, pgxErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m.Regions, _ = s.ListMonitorRegions(ctx, m.ID)
	return &m, nil
}

func (s *Store) CreateMonitor(ctx context.Context, m *Monitor) error {
	cfg, err := encodeMonitorConfig(m.Config)
	if err != nil {
		return err
	}
	if m.SLOTarget <= 0 {
		m.SLOTarget = 99.9
	}
	if m.ProbeNodeIDs == nil {
		m.ProbeNodeIDs = []int64{}
	}
	if m.ConfirmLocations <= 0 {
		m.ConfirmLocations = 1
	}
	err = s.Pool.QueryRow(ctx, `INSERT INTO monitors
		(name, type, target, interval_seconds, retries, escalate_minutes, escalate_recipients, enabled, tags, config, latency_threshold_ms, channel_ids, resend_minutes, parent_id, depends_on, slo_target, probe_node_ids, org_id, confirm_locations)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		RETURNING id, created_at`,
		m.Name, m.Type, m.Target, m.IntervalSeconds, m.Retries,
		m.EscalateMinutes, m.EscalateRecipients, m.Enabled, m.Tags, cfg, m.LatencyThresholdMs, m.ChannelIDs,
		m.ResendMinutes, m.ParentID, m.DependsOn, m.SLOTarget, m.ProbeNodeIDs, writeOrg(ctx), m.ConfirmLocations,
	).Scan(&m.ID, &m.CreatedAt)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO monitor_state (monitor_id) VALUES ($1)`, m.ID)
	if err != nil {
		return err
	}
	return s.NotifyConfig(ctx)
}

func (s *Store) UpdateMonitor(ctx context.Context, m *Monitor) error {
	cfg, err := encodeMonitorConfig(m.Config)
	if err != nil {
		return err
	}
	if m.SLOTarget <= 0 {
		m.SLOTarget = 99.9
	}
	if m.ProbeNodeIDs == nil {
		m.ProbeNodeIDs = []int64{}
	}
	if m.ConfirmLocations <= 0 {
		m.ConfirmLocations = 1
	}
	// Saved as enabled when it was not: that is a resume, like the button.
	var wasEnabled bool
	if err := s.Pool.QueryRow(ctx, `SELECT enabled FROM monitors WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, m.ID, OrgID(ctx)).Scan(&wasEnabled); err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE monitors SET
		name=$1, type=$2, target=$3, interval_seconds=$4, retries=$5,
		escalate_minutes=$6, escalate_recipients=$7, enabled=$8, tags=$9, config=$10,
		latency_threshold_ms=$11, channel_ids=$12, resend_minutes=$13, parent_id=$14, depends_on=$15,
		slo_target=$16, probe_node_ids=$17, confirm_locations=$20
		WHERE id=$18 AND ($19 = 0 OR org_id=$19)`,
		m.Name, m.Type, m.Target, m.IntervalSeconds, m.Retries,
		m.EscalateMinutes, m.EscalateRecipients, m.Enabled, m.Tags, cfg,
		m.LatencyThresholdMs, m.ChannelIDs, m.ResendMinutes, m.ParentID, m.DependsOn,
		m.SLOTarget, m.ProbeNodeIDs, m.ID, OrgID(ctx), m.ConfirmLocations)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// A location taken off the monitor no longer votes or shows.
	if _, err := s.Pool.Exec(ctx, `DELETE FROM probe_node_state
		WHERE monitor_id=$1 AND NOT (node_id = ANY($2))`, m.ID, Locations(*m)); err != nil {
		return err
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE monitor_state SET consecutive_failures=0, consecutive_slow=0, slow_since=NULL, escalated_at=NULL WHERE monitor_id=$1`, m.ID); err != nil {
		return err
	}
	if m.Enabled && !wasEnabled {
		if err := s.markResumed(ctx, m.ID); err != nil {
			return err
		}
	}
	return s.NotifyConfig(ctx)
}

func (s *Store) DeleteMonitor(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM monitors WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, id, OrgID(ctx))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return s.NotifyConfig(ctx)
}

func (s *Store) SetMonitorEnabled(ctx context.Context, id int64, enabled bool) error {
	matched, _, err := s.setEnabled(ctx, []int64{id}, enabled, "")
	if err != nil {
		return err
	}
	if matched == 0 {
		return ErrNotFound
	}
	return s.NotifyConfig(ctx)
}

func (s *Store) InsertEvent(ctx context.Context, monitorID int64, eventType, detail string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO monitor_events (monitor_id, type, detail) VALUES ($1,$2,$3)`,
		monitorID, eventType, detail)
	return err
}

func (s *Store) ListEvents(ctx context.Context, monitorID int64, limit int) ([]Event, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, monitor_id, ts, type, detail
		FROM monitor_events WHERE monitor_id=$1 ORDER BY ts DESC, id DESC LIMIT $2`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.MonitorID, &e.TS, &e.Type, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) InsertResult(ctx context.Context, r PingResult) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO monitor_results (monitor_id, ts, latency_ms, success) VALUES ($1,$2,$3,$4)`,
		r.MonitorID, r.TS, r.LatencyMs, r.Success)
	return err
}

func (s *Store) ResultsRaw(ctx context.Context, monitorID int64, from time.Time) ([]RawPoint, error) {
	rows, err := s.Pool.Query(ctx, `SELECT ts, latency_ms, success FROM monitor_results
		WHERE monitor_id=$1 AND ts >= $2 ORDER BY ts ASC`, monitorID, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RawPoint
	for rows.Next() {
		var p RawPoint
		if err := rows.Scan(&p.TS, &p.LatencyMs, &p.Success); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Rollups(ctx context.Context, table string, monitorID int64, from time.Time) ([]RollupPoint, error) {
	if table != "rollup_hourly" && table != "rollup_daily" {
		return nil, errors.New("invalid rollup table")
	}
	rows, err := s.Pool.Query(ctx, `SELECT bucket, total, failures, avg_latency, max_latency FROM `+table+`
		WHERE monitor_id=$1 AND bucket >= $2 ORDER BY bucket ASC`, monitorID, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RollupPoint
	for rows.Next() {
		var p RollupPoint
		if err := rows.Scan(&p.TS, &p.Total, &p.Failures, &p.AvgLatency, &p.MaxLatency); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetMonitorByPushToken(ctx context.Context, token string) (*Monitor, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrNotFound
	}
	row := s.Pool.QueryRow(ctx, `SELECT id, name, type, target, interval_seconds, retries,
		escalate_minutes, escalate_recipients, enabled, tags, config, created_at, latency_threshold_ms,
		channel_ids, resend_minutes, parent_id, depends_on
		FROM monitors WHERE type='push' AND config->>'push_token' = $1`, token)
	var m Monitor
	var cfg []byte
	if err := row.Scan(&m.ID, &m.Name, &m.Type, &m.Target, &m.IntervalSeconds, &m.Retries,
		&m.EscalateMinutes, &m.EscalateRecipients, &m.Enabled, &m.Tags, &cfg, &m.CreatedAt,
		&m.LatencyThresholdMs, &m.ChannelIDs, &m.ResendMinutes, &m.ParentID, &m.DependsOn); err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m.Config = decodeMonitorConfig(cfg)
	return &m, nil
}

// RecordPush notes a heartbeat and whether it reported the job as working.
func (s *Store) RecordPush(ctx context.Context, monitorID int64, at time.Time, ok bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE monitor_state SET last_push_at=$2, last_push_ok=$3 WHERE monitor_id=$1`, monitorID, at, ok)
	return err
}

// LastPush is the newest heartbeat for a monitor: when it came and what it
// said. at is nil when none has arrived.
func (s *Store) LastPush(ctx context.Context, monitorID int64) (at *time.Time, ok bool, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT last_push_at, last_push_ok FROM monitor_state WHERE monitor_id=$1`, monitorID).Scan(&at, &ok)
	if err == pgxErrNoRows {
		return nil, true, nil
	}
	return at, ok, err
}

func (s *Store) TouchLastAlert(ctx context.Context, monitorID int64, at time.Time) error {
	_, err := s.Pool.Exec(ctx, `UPDATE monitor_state SET last_alert_at=$2 WHERE monitor_id=$1`, monitorID, at)
	return err
}

func (s *Store) LatencyPercentiles(ctx context.Context, monitorID int64, from time.Time) (LatencyPercentiles, error) {
	var out LatencyPercentiles
	err := s.Pool.QueryRow(ctx, `SELECT
		percentile_cont(0.5) WITHIN GROUP (ORDER BY latency_ms),
		percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms),
		percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ms),
		count(*)::int
		FROM monitor_results
		WHERE monitor_id=$1 AND ts >= $2 AND success AND latency_ms IS NOT NULL`,
		monitorID, from).Scan(&out.P50, &out.P95, &out.P99, &out.Samples)
	if err != nil {
		return LatencyPercentiles{}, err
	}
	return out, nil
}

func (s *Store) MonitorStatus(ctx context.Context, id int64) (string, error) {
	var status string
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(s.status, 'pending') FROM monitors m
		LEFT JOIN monitor_state s ON s.monitor_id = m.id WHERE m.id=$1 AND ($2 = 0 OR m.org_id=$2)`, id, OrgID(ctx)).Scan(&status)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return status, nil
}

func (s *Store) ValidateMonitorRefs(ctx context.Context, id int64, parentID, dependsOn *int64) error {
	if parentID != nil {
		if *parentID == id && id > 0 {
			return errors.New("parent cannot be the monitor itself")
		}
		var kind string
		err := s.Pool.QueryRow(ctx, `SELECT type FROM monitors WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, *parentID, OrgID(ctx)).Scan(&kind)
		if err != nil {
			if errors.Is(err, pgxErrNoRows) {
				return errors.New("parent monitor not found")
			}
			return err
		}
		if kind != "group" {
			return errors.New("parent must be a group monitor")
		}
		if id > 0 {
			if err := s.assertParentAcyclic(ctx, id, *parentID); err != nil {
				return err
			}
		}
	}
	if dependsOn != nil {
		if *dependsOn == id && id > 0 {
			return errors.New("depends_on cannot be the monitor itself")
		}
		var exists bool
		err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM monitors WHERE id=$1 AND ($2 = 0 OR org_id=$2))`, *dependsOn, OrgID(ctx)).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("depends_on monitor not found")
		}
	}
	return nil
}

func (s *Store) assertParentAcyclic(ctx context.Context, id, parentID int64) error {
	seen := map[int64]bool{id: true}
	cur := parentID
	for cur > 0 {
		if seen[cur] {
			return errors.New("parent would create a cycle")
		}
		seen[cur] = true
		var next *int64
		err := s.Pool.QueryRow(ctx, `SELECT parent_id FROM monitors WHERE id=$1`, cur).Scan(&next)
		if err != nil {
			if errors.Is(err, pgxErrNoRows) {
				return errors.New("parent monitor not found")
			}
			return err
		}
		if next == nil {
			return nil
		}
		cur = *next
	}
	return nil
}

func (s *Store) RefreshGroupStatus(ctx context.Context, groupID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE monitor_state SET
		status = COALESCE((
			SELECT CASE
				WHEN COUNT(*) FILTER (WHERE c.enabled AND cs.status = 'down') > 0 THEN 'down'
				WHEN COUNT(*) FILTER (WHERE c.enabled) = 0 THEN 'up'
				WHEN COUNT(*) FILTER (WHERE c.enabled AND cs.status = 'pending') = COUNT(*) FILTER (WHERE c.enabled) THEN 'pending'
				ELSE 'up'
			END
			FROM monitors c
			LEFT JOIN monitor_state cs ON cs.monitor_id = c.id
			WHERE c.parent_id = $1
		), 'up'),
		slow_since = CASE WHEN EXISTS (
			SELECT 1 FROM monitors c JOIN monitor_state cs ON cs.monitor_id = c.id
			WHERE c.parent_id = $1 AND c.enabled AND cs.slow_since IS NOT NULL
		) THEN COALESCE(slow_since, now()) ELSE NULL END,
		last_check_at = now()
		WHERE monitor_id = $1`, groupID)
	return err
}

func (s *Store) ListGroupIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM monitors WHERE type='group' AND enabled = TRUE AND ($1 = 0 OR org_id=$1)`, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CountMonitors is the monitor count a plan's monitor band is measured against.
func (s *Store) CountMonitors(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM monitors WHERE ($1 = 0 OR org_id=$1)`, OrgID(ctx)).Scan(&n)
	return n, err
}

// WatchedAndDown says whether a monitor is down and still being checked. A
// paused monitor keeps the status it had when it was paused; one paused
// while down must not go on suppressing the alerts of everything that
// depends on it.
func (s *Store) WatchedAndDown(ctx context.Context, id int64) (bool, error) {
	var down bool
	err := s.Pool.QueryRow(ctx, `SELECT m.enabled AND COALESCE(s.status, 'pending') = 'down'
		FROM monitors m LEFT JOIN monitor_state s ON s.monitor_id = m.id WHERE m.id=$1`, id).Scan(&down)
	if errors.Is(err, pgxErrNoRows) {
		return false, nil
	}
	return down, err
}
