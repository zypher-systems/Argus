package probe

import (
	"testing"
	"time"

	"argus/internal/store"
)

func TestMergeScheduleKeepsNextRun(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	prevMon := map[int64]store.Monitor{
		1: {ID: 1, IntervalSeconds: 60},
		2: {ID: 2, IntervalSeconds: 60},
	}
	prevNext := map[int64]time.Time{
		1: now.Add(40 * time.Second),
		2: now.Add(10 * time.Second),
	}
	incoming := []store.Monitor{
		{ID: 1, IntervalSeconds: 60},
		{ID: 3, IntervalSeconds: 30},
	}
	gotMon, gotNext := mergeSchedule(prevMon, prevNext, incoming, now)
	if len(gotMon) != 2 {
		t.Fatalf("monitors %d", len(gotMon))
	}
	if _, ok := gotMon[2]; ok {
		t.Fatal("deleted monitor 2 should be gone")
	}
	if !gotNext[1].Equal(prevNext[1]) {
		t.Fatalf("kept nextRun = %s", gotNext[1])
	}
	if !gotNext[3].Equal(now.Add(15 * time.Second)) {
		t.Fatalf("new monitor nextRun = %s", gotNext[3])
	}
}

func TestMergeScheduleReschedulesIntervalChange(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	prevMon := map[int64]store.Monitor{1: {ID: 1, IntervalSeconds: 60}}
	prevNext := map[int64]time.Time{1: now.Add(40 * time.Second)}
	incoming := []store.Monitor{{ID: 1, IntervalSeconds: 120}}
	_, gotNext := mergeSchedule(prevMon, prevNext, incoming, now)
	if !gotNext[1].Equal(now.Add(60 * time.Second)) {
		t.Fatalf("interval change nextRun = %s", gotNext[1])
	}
}

func TestProbeStale(t *testing.T) {
	now := time.Now()
	if !store.ProbeStale(nil, now, 45*time.Second) {
		t.Fatal("missing heartbeat is stale")
	}
	hb := &store.ProbeHeartbeat{LastSeen: now.Add(-10 * time.Second)}
	if store.ProbeStale(hb, now, 45*time.Second) {
		t.Fatal("fresh heartbeat should not be stale")
	}
	hb.LastSeen = now.Add(-time.Minute)
	if !store.ProbeStale(hb, now, 45*time.Second) {
		t.Fatal("old heartbeat should be stale")
	}
}
