package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) CountSentAlertsSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE status='sent' AND created_at >= $1`, since).Scan(&n)
	return n, err
}

func (s *Store) InsertAlert(ctx context.Context, monitorID int64, alertType, status, recipients, alertErr string) error {
	var mid any
	if monitorID > 0 {
		mid = monitorID
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO alerts (monitor_id, type, status, recipients, error)
		VALUES ($1,$2,$3,$4,$5)`, mid, alertType, status, recipients, alertErr)
	return err
}

func (s *Store) ListAlerts(ctx context.Context, monitorID *int64, limit int) ([]Alert, error) {
	var rows pgx.Rows
	var err error
	if monitorID != nil {
		rows, err = s.Pool.Query(ctx, `SELECT id, monitor_id, type, status, recipients, error, created_at
			FROM alerts WHERE monitor_id=$1 ORDER BY created_at DESC, id DESC LIMIT $2`, *monitorID, limit)
	} else {
		rows, err = s.Pool.Query(ctx, `SELECT id, monitor_id, type, status, recipients, error, created_at
			FROM alerts ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		var a Alert
		var mid *int64
		if err := rows.Scan(&a.ID, &mid, &a.Type, &a.Status, &a.Recipients, &a.Error, &a.CreatedAt); err != nil {
			return nil, err
		}
		if mid != nil {
			a.MonitorID = *mid
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
