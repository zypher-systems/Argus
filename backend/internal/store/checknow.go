package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

type CheckNowRequest struct {
	ID          int64
	MonitorID   int64
	Status      string
	Success     *bool
	LatencyMs   *float64
	Error       string
	Evidence    *CheckEvidence
	RequestedAt time.Time
	FinishedAt  *time.Time
}

func (s *Store) EnqueueCheckNow(ctx context.Context, monitorID int64) (int64, error) {
	var existing int64
	err := s.Pool.QueryRow(ctx, `SELECT r.id FROM check_now_requests r
		JOIN monitors m ON m.id = r.monitor_id
		WHERE r.monitor_id=$1 AND r.status IN ('pending','running')
		AND ($2 = 0 OR m.org_id=$2)
		ORDER BY r.id DESC LIMIT 1`, monitorID, OrgID(ctx)).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgxErrNoRows) {
		return 0, err
	}

	var id int64
	err = s.Pool.QueryRow(ctx, `INSERT INTO check_now_requests (monitor_id)
		SELECT m.id FROM monitors m WHERE m.id=$1 AND ($2 = 0 OR m.org_id=$2)
		RETURNING id`, monitorID, OrgID(ctx)).Scan(&id)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return id, nil
}

func (s *Store) GetCheckNow(ctx context.Context, id int64) (*CheckNowRequest, error) {
	row := s.Pool.QueryRow(ctx, `SELECT r.id, r.monitor_id, r.status, r.success, r.latency_ms, r.error,
		r.evidence, r.requested_at, r.finished_at
		FROM check_now_requests r
		JOIN monitors m ON m.id = r.monitor_id
		WHERE r.id=$1 AND ($2 = 0 OR m.org_id=$2)`, id, OrgID(ctx))
	return scanCheckNow(row.Scan)
}

func (s *Store) ClaimCheckNow(ctx context.Context, id int64) (*CheckNowRequest, error) {
	row := s.Pool.QueryRow(ctx, `UPDATE check_now_requests SET status='running'
		WHERE id=$1 AND status='pending'
		RETURNING id, monitor_id, status, success, latency_ms, error, evidence, requested_at, finished_at`, id)
	req, err := scanCheckNow(row.Scan)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return req, nil
}

func (s *Store) ClaimPendingCheckNow(ctx context.Context) (*CheckNowRequest, error) {
	_, _ = s.Pool.Exec(ctx, `UPDATE check_now_requests SET status='failed', error='timed out', finished_at=now()
		WHERE status IN ('pending','running') AND requested_at < now() - interval '2 minutes'`)
	row := s.Pool.QueryRow(ctx, `UPDATE check_now_requests SET status='running'
		WHERE id = (
			SELECT id FROM check_now_requests WHERE status='pending' ORDER BY id LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, monitor_id, status, success, latency_ms, error, evidence, requested_at, finished_at`)
	req, err := scanCheckNow(row.Scan)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return req, nil
}

func (s *Store) CompleteCheckNow(ctx context.Context, id int64, success bool, latency *float64, errText string, ev *CheckEvidence) error {
	status := "done"
	if !success {
		status = "failed"
	}
	_, err := s.Pool.Exec(ctx, `UPDATE check_now_requests SET
		status=$2, success=$3, latency_ms=$4, error=$5, evidence=$6, finished_at=now()
		WHERE id=$1`, id, status, success, latency, strings.TrimSpace(errText), encodeCheckEvidence(ev))
	return err
}

func scanCheckNow(scan func(dest ...any) error) (*CheckNowRequest, error) {
	var r CheckNowRequest
	var evRaw []byte
	if err := scan(&r.ID, &r.MonitorID, &r.Status, &r.Success, &r.LatencyMs, &r.Error, &evRaw, &r.RequestedAt, &r.FinishedAt); err != nil {
		return nil, err
	}
	r.Evidence = decodeCheckEvidence(evRaw)
	return &r, nil
}
