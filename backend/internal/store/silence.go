package store

import (
	"context"
	"errors"
	"time"
)

const maxSilenceMinutes = 10080 // 7 days
const unmuteHold = 10 * 365 * 24 * time.Hour

func (s *Store) AlertSilence(ctx context.Context, monitorID int64, now time.Time) (muted, acked bool, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT
		muted_until IS NOT NULL AND muted_until > $2,
		acked_at IS NOT NULL AND (acked_until IS NULL OR acked_until > $2)
		FROM monitor_state WHERE monitor_id=$1`, monitorID, now).Scan(&muted, &acked)
	if err != nil {
		if err == pgxErrNoRows {
			return false, false, nil
		}
		return false, false, err
	}
	return muted, acked, nil
}

func (s *Store) MuteMonitor(ctx context.Context, id int64, until *time.Time) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE monitor_state s SET muted_until=$2
		FROM monitors m WHERE s.monitor_id=m.id AND m.id=$1 AND ($3 = 0 OR m.org_id=$3)`,
		id, until, OrgID(ctx))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetLastFailureEvidence(ctx context.Context, id int64, at time.Time, ev *CheckEvidence) error {
	if ev == nil {
		return nil
	}
	_, err := s.Pool.Exec(ctx, `UPDATE monitor_state SET last_failure_evidence=$2, last_failure_at=$3 WHERE monitor_id=$1`,
		id, encodeCheckEvidence(ev), at)
	return err
}

func (s *Store) AckMonitor(ctx context.Context, id int64, until *time.Time) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE monitor_state s SET acked_at=now(), acked_until=$2
		FROM monitors m WHERE s.monitor_id=m.id AND m.id=$1 AND ($3 = 0 OR m.org_id=$3)
		AND s.status='down'`,
		id, until, OrgID(ctx))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func MuteUntil(now time.Time, minutes int) (*time.Time, error) {
	switch {
	case minutes == 0:
		return nil, nil
	case minutes == -1:
		t := now.Add(unmuteHold)
		return &t, nil
	case minutes < 0 || minutes > maxSilenceMinutes:
		return nil, ErrSilenceMinutes
	default:
		t := now.Add(time.Duration(minutes) * time.Minute)
		return &t, nil
	}
}

func AckUntil(now time.Time, minutes int) (*time.Time, error) {
	switch {
	case minutes == 0:
		return nil, nil // until next down
	case minutes < 0 || minutes > maxSilenceMinutes:
		return nil, ErrSilenceMinutes
	default:
		t := now.Add(time.Duration(minutes) * time.Minute)
		return &t, nil
	}
}

var ErrSilenceMinutes = errors.New("minutes must be 0 (clear / until next down), -1 (until unmute), or 1-10080")
