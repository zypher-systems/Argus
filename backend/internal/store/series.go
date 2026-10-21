package store

import (
	"context"
	"time"
)

type SeriesPoint struct {
	MonitorID int64     `json:"monitor_id"`
	Bucket    time.Time `json:"ts"`
	Avg       *float64  `json:"avg_latency"`
	Max       *float64  `json:"max_latency"`
	Failures  int       `json:"failures"`
	Total     int       `json:"total"`
}

// SparkSeries returns the most recent raw checks per monitor (newest `limit` each)
// so dashboard trendlines appear before hourly rollups exist.
func (s *Store) SparkSeries(ctx context.Context, limit int) ([]SeriesPoint, error) {
	if limit <= 0 {
		limit = 80
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT monitor_id, ts, latency_ms, latency_ms,
			CASE WHEN success THEN 0 ELSE 1 END, 1
		FROM (
			SELECT r.monitor_id, r.ts, r.latency_ms, r.success,
				row_number() OVER (PARTITION BY r.monitor_id ORDER BY r.ts DESC) AS rn
			FROM monitor_results r
			JOIN monitors m ON m.id = r.monitor_id
			WHERE r.ts >= now() - interval '24 hours'
				AND ($2 = 0 OR m.org_id = $2)
		) recent
		WHERE rn <= $1
		ORDER BY monitor_id, ts ASC`, limit, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeriesPoint
	for rows.Next() {
		var p SeriesPoint
		if err := rows.Scan(&p.MonitorID, &p.Bucket, &p.Avg, &p.Max, &p.Failures, &p.Total); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) HourlySeries(ctx context.Context, since time.Time) ([]SeriesPoint, error) {
	rows, err := s.Pool.Query(ctx, `SELECT r.monitor_id, r.bucket, r.avg_latency, r.max_latency, r.failures, r.total
		FROM rollup_hourly r
		JOIN monitors m ON m.id = r.monitor_id
		WHERE r.bucket >= $1 AND ($2 = 0 OR m.org_id=$2)
		ORDER BY r.bucket ASC`, since, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeriesPoint
	for rows.Next() {
		var p SeriesPoint
		if err := rows.Scan(&p.MonitorID, &p.Bucket, &p.Avg, &p.Max, &p.Failures, &p.Total); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) MonitorSummary(ctx context.Context) (int, int, int, error) {
	var up, down, pending int
	err := s.Pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status = 'up'),
		count(*) FILTER (WHERE status = 'down'),
		count(*) FILTER (WHERE status = 'pending' OR status IS NULL)
		FROM monitor_state`).Scan(&up, &down, &pending)
	return up, down, pending, err
}
