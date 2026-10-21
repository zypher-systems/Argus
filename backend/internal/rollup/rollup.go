package rollup

import (
	"context"
	"log/slog"
	"time"

	"argus/internal/store"
)

type Job struct {
	store *store.Store
}

func New(st *store.Store) *Job {
	return &Job{store: st}
}

func (j *Job) Run(ctx context.Context) {
	j.rebuild(ctx)
	j.tick(ctx)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.tick(ctx)
		}
	}
}

// rebuild runs once at start, over every raw result still kept, so buckets
// left short or never built are put right before the regular refresh takes
// over.
func (j *Job) rebuild(ctx context.Context) {
	began := time.Now()
	if err := j.store.RebuildRollups(ctx); err != nil {
		slog.Error("rollup rebuild failed", "error", err)
		return
	}
	slog.Info("rollups rebuilt from kept results", "took", time.Since(began).Round(time.Millisecond))
}

func (j *Job) tick(ctx context.Context) {
	if err := j.store.RefreshRollups(ctx); err != nil {
		slog.Error("rollup refresh failed", "error", err)
	}
	if err := j.store.RefreshUptime24h(ctx); err != nil {
		slog.Error("uptime refresh failed", "error", err)
	}
	if err := j.store.PurgeExpiredSessions(ctx); err != nil {
		slog.Error("session purge failed", "error", err)
	}
	settings, err := j.store.LoadSettings(ctx)
	if err == nil {
		if err := j.store.PurgeOld(ctx, settings.Retention.RawDays, settings.Retention.HourlyDays, settings.Retention.DailyDays); err != nil {
			slog.Error("retention purge failed", "error", err)
		}
	}
}
