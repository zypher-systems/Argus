package store

import (
	"context"
	"time"
)

const ProbeHeartbeatID = "default"

type ProbeHeartbeat struct {
	ID          string
	LastSeen    time.Time
	StartedAt   time.Time
	AlertedDown bool
}

func (s *Store) TouchProbeHeartbeat(ctx context.Context, now time.Time) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO probe_heartbeat (id, last_seen, started_at, alerted_down)
		VALUES ($1, $2, $2, FALSE)
		ON CONFLICT (id) DO UPDATE SET last_seen = EXCLUDED.last_seen`,
		ProbeHeartbeatID, now)
	return err
}

func (s *Store) GetProbeHeartbeat(ctx context.Context) (*ProbeHeartbeat, error) {
	var hb ProbeHeartbeat
	err := s.Pool.QueryRow(ctx, `SELECT id, last_seen, started_at, alerted_down
		FROM probe_heartbeat WHERE id = $1`, ProbeHeartbeatID).Scan(
		&hb.ID, &hb.LastSeen, &hb.StartedAt, &hb.AlertedDown)
	if err != nil {
		if err == pgxErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &hb, nil
}

// SetProbeAlerted flips the "probe alerted as down" mark outside an alert.
// It reports false when the mark was already there. Alerts take the mark
// with ClaimProbeAlerted, in the transaction that queues them.
func (s *Store) SetProbeAlerted(ctx context.Context, alerted bool) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE probe_heartbeat SET alerted_down = $1 WHERE id = $2 AND alerted_down <> $1`,
		alerted, ProbeHeartbeatID)
	return tag.RowsAffected() == 1, err
}

func ProbeStale(hb *ProbeHeartbeat, now time.Time, staleAfter time.Duration) bool {
	if hb == nil {
		return true
	}
	return now.Sub(hb.LastSeen) > staleAfter
}
