package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) Listen(ctx context.Context, channel string, onNotify func(payload string)) error {
	conn, err := pgx.Connect(ctx, s.Pool.Config().ConnString())
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()

	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		onNotify(n.Payload)
	}
}

func (s *Store) PurgeOld(ctx context.Context, rawDays, hourlyDays, dailyDays int) error {
	if rawDays > 0 {
		if _, err := s.Pool.Exec(ctx, `DELETE FROM monitor_results WHERE ts < now() - make_interval(days => $1)`, rawDays); err != nil {
			return err
		}
	}
	if hourlyDays > 0 {
		if _, err := s.Pool.Exec(ctx, `DELETE FROM rollup_hourly WHERE bucket < now() - make_interval(days => $1)`, hourlyDays); err != nil {
			return err
		}
	}
	if dailyDays > 0 {
		if _, err := s.Pool.Exec(ctx, `DELETE FROM rollup_daily WHERE bucket < now() - make_interval(days => $1)`, dailyDays); err != nil {
			return err
		}
	}
	if err := s.PurgeAlertOutbox(ctx, 7*24*time.Hour); err != nil {
		return err
	}
	if rawDays > 0 {
		if _, err := s.Pool.Exec(ctx, `DELETE FROM monitor_events WHERE ts < now() - make_interval(days => $1)`, rawDays); err != nil {
			return err
		}
		if _, err := s.Pool.Exec(ctx, `DELETE FROM alerts WHERE created_at < now() - make_interval(days => $1)`, rawDays); err != nil {
			return err
		}
	}
	return nil
}

// RefreshRollups folds the last few hours and days of raw results into the
// hourly and daily tables. It runs every few minutes.
func (s *Store) RefreshRollups(ctx context.Context) error {
	if err := s.foldResults(ctx, "rollup_hourly", "hour", "3 hours"); err != nil {
		return err
	}
	return s.foldResults(ctx, "rollup_daily", "day", "3 days")
}

// RebuildRollups folds every raw result still kept. It repairs buckets an
// older version left short and fills the hours the api was not running to
// build, as far back as raw retention reaches.
func (s *Store) RebuildRollups(ctx context.Context) error {
	if err := s.foldResults(ctx, "rollup_hourly", "hour", ""); err != nil {
		return err
	}
	return s.foldResults(ctx, "rollup_daily", "day", "")
}

// foldResults aggregates raw results into table, one row per monitor and
// unit, starting with the bucket that holds now()-window, or with the oldest
// raw result when window is empty.
//
// The window opens on a bucket boundary. Opening it at now()-window, partway
// through a bucket, rewrote that bucket on each run with the shrinking part
// still inside, so every bucket ended up holding its last few minutes.
//
// A bucket is never replaced by a smaller count: raw results are purged on
// their own schedule, which can be shorter than the window, and a bucket
// built from what the purge left would be short in the same way.
func (s *Store) foldResults(ctx context.Context, table, unit, window string) error {
	since := ""
	if window != "" {
		since = `WHERE ts >= date_trunc('` + unit + `', now() - interval '` + window + `')`
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO `+table+` AS r (monitor_id, bucket, total, failures, avg_latency, max_latency)
		SELECT monitor_id, date_trunc('`+unit+`', ts), count(*), count(*) FILTER (WHERE NOT success), avg(latency_ms), max(latency_ms)
		FROM monitor_results `+since+`
		GROUP BY 1, 2
		ON CONFLICT (monitor_id, bucket) DO UPDATE
		SET total = EXCLUDED.total, failures = EXCLUDED.failures,
			avg_latency = EXCLUDED.avg_latency, max_latency = EXCLUDED.max_latency
		WHERE r.total <= EXCLUDED.total`)
	return err
}
