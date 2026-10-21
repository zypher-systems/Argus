package store

import (
	"context"
	"testing"
)

// rollupMonitor makes a monitor and gives it one result a minute across the
// whole hour (or day) that holds now()-ago, half of them failures.
func rollupMonitor(t *testing.T, st *Store, unit, ago string, minutes int) int64 {
	t.Helper()
	ctx := context.Background()
	m := Monitor{Name: "rollup-" + unit, Type: "icmp", Target: "127.0.0.1", IntervalSeconds: 60, Retries: 1, Enabled: true}
	if err := st.CreateMonitor(ctx, &m); err != nil {
		t.Fatal(err)
	}
	_, err := st.Pool.Exec(ctx, `INSERT INTO monitor_results (monitor_id, ts, success, latency_ms)
		SELECT $1, date_trunc('`+unit+`', now() - interval '`+ago+`') + make_interval(mins => g), g % 2 = 0, 10
		FROM generate_series(0, $2 - 1) g`, m.ID, minutes)
	if err != nil {
		t.Fatal(err)
	}
	return m.ID
}

func bucketTotals(t *testing.T, st *Store, table string, monitorID int64) (total, failures int) {
	t.Helper()
	err := st.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(sum(total),0), COALESCE(sum(failures),0) FROM `+table+` WHERE monitor_id=$1`, monitorID).Scan(&total, &failures)
	if err != nil {
		t.Fatal(err)
	}
	return total, failures
}

// The hour and the day at the far edge of the refresh window are only partly
// inside "the last 3 hours" and "the last 3 days". They must still be rolled
// up whole: a bucket holding the slice that was left in the window is what
// made SLA reports and status page bars report a few minutes as a day.
func TestRefreshRollupsKeepsEdgeBucketsWhole(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	hourly := rollupMonitor(t, st, "hour", "3 hours", 60)
	daily := rollupMonitor(t, st, "day", "3 days", 24*60)

	if err := st.RefreshRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if total, failures := bucketTotals(t, st, "rollup_hourly", hourly); total != 60 || failures != 30 {
		t.Fatalf("hour at the edge of the window: total=%d failures=%d, want 60 and 30", total, failures)
	}
	if total, failures := bucketTotals(t, st, "rollup_daily", daily); total != 1440 || failures != 720 {
		t.Fatalf("day at the edge of the window: total=%d failures=%d, want 1440 and 720", total, failures)
	}
}

// Raw results are purged on their own schedule. A refresh that runs after
// part of a bucket's results are gone must leave the bucket as it was.
func TestRefreshRollupsNeverShrinksABucket(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	id := rollupMonitor(t, st, "day", "2 days", 24*60)
	if err := st.RefreshRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM monitor_results WHERE monitor_id=$1
		AND ts < date_trunc('day', now() - interval '2 days') + interval '23 hours'`, id); err != nil {
		t.Fatal(err)
	}
	if err := st.RefreshRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if total, failures := bucketTotals(t, st, "rollup_daily", id); total != 1440 || failures != 720 {
		t.Fatalf("after a purge: total=%d failures=%d, want the full 1440 and 720", total, failures)
	}
}

// Buckets an older version left short, and hours nothing was running to
// build, are put right from the raw results that are still kept.
func TestRebuildRollupsRepairsShortAndMissingBuckets(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	short := rollupMonitor(t, st, "hour", "5 hours", 60)
	missing := rollupMonitor(t, st, "hour", "9 hours", 60)
	if _, err := st.Pool.Exec(ctx, `INSERT INTO rollup_hourly (monitor_id, bucket, total, failures, avg_latency, max_latency)
		VALUES ($1, date_trunc('hour', now() - interval '5 hours'), 4, 0, 10, 10)`, short); err != nil {
		t.Fatal(err)
	}

	if err := st.RefreshRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if total, _ := bucketTotals(t, st, "rollup_hourly", short); total != 4 {
		t.Fatalf("the refresh reaches back 3 hours, not 5: total=%d", total)
	}
	if err := st.RebuildRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if total, failures := bucketTotals(t, st, "rollup_hourly", short); total != 60 || failures != 30 {
		t.Fatalf("short bucket: total=%d failures=%d, want 60 and 30", total, failures)
	}
	if total, failures := bucketTotals(t, st, "rollup_hourly", missing); total != 60 || failures != 30 {
		t.Fatalf("missing bucket: total=%d failures=%d, want 60 and 30", total, failures)
	}
}
