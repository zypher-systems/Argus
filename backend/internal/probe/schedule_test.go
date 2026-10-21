package probe

import (
	"errors"
	"strings"
	"testing"
	"time"

	"argus/internal/store"
)

func TestParseSchedule(t *testing.T) {
	for _, ok := range []string{"0 2 * * *", "*/15 * * * *", "30 6 * * 1-5", "@daily", "@hourly", "@weekly"} {
		if _, err := ParseSchedule(ok, "Europe/Berlin"); err != nil {
			t.Errorf("%q should parse: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "61 * * * *", "0 2 * *", "@every 5m", "CRON_TZ=UTC 0 2 * * *", "nonsense"} {
		if _, err := ParseSchedule(bad, ""); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if _, err := ParseSchedule("0 2 * * *", "Mars/Olympus"); err == nil || !strings.Contains(err.Error(), "time zone") {
		t.Errorf("an unknown zone should say so: %v", err)
	}
}

func TestScheduleUsesItsTimeZone(t *testing.T) {
	from := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	next, err := NextRuns("0 2 * * *", "America/New_York", from, 2)
	if err != nil {
		t.Fatal(err)
	}
	// 02:00 in New York in September is 06:00 UTC.
	if got := next[0].UTC(); !got.Equal(time.Date(2026, 9, 23, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("first run %v, want 06:00 UTC", got)
	}
	if next[1].Sub(next[0]) != 24*time.Hour {
		t.Fatalf("daily schedule should be a day apart: %v", next)
	}
}

// TestScheduledHeartbeat follows a nightly job through two days.
func TestScheduledHeartbeat(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	at := func(day, h, m int) time.Time { return time.Date(2026, 9, day, h, m, 0, 0, berlin) }
	m := store.Monitor{Type: "push", CreatedAt: at(22, 12, 0),
		Config: store.MonitorConfig{Schedule: "0 2 * * *", Timezone: "Europe/Berlin", GraceSeconds: 600}}

	// Created at noon; the first ping is due 02:00 tomorrow.
	if _, err := checkPush(m, at(22, 18, 0)); !errors.Is(err, errAwaitingPush) {
		t.Fatalf("before the first due time it waits: %v", err)
	}
	if _, err := checkPush(m, at(23, 2, 9)); !errors.Is(err, errAwaitingPush) {
		t.Fatalf("inside the grace period it still waits: %v", err)
	}
	if _, err := checkPush(m, at(23, 2, 11)); err == nil || !strings.Contains(err.Error(), "no ping yet") {
		t.Fatalf("past due and grace with no ping is down: %v", err)
	}

	// The job runs at 02:00, takes a few minutes, and pings at 02:04.
	ping := at(23, 2, 4)
	m.LastPushAt = &ping
	for _, now := range []time.Time{at(23, 2, 5), at(23, 14, 0), at(24, 2, 9)} {
		if _, err := checkPush(m, now); err != nil {
			t.Fatalf("%v: a ping for last night keeps it up until tonight's is late: %v", now, err)
		}
	}
	if _, err := checkPush(m, at(24, 2, 11)); err == nil || !strings.Contains(err.Error(), "did not arrive") {
		t.Fatalf("tonight's ping missing past grace is down: %v", err)
	}
	// Times read from the database are UTC; the message is in the job's zone.
	utcPing := ping.UTC()
	m.LastPushAt = &utcPing
	if _, err := checkPush(m, at(24, 2, 11)); err == nil || !strings.Contains(err.Error(), "02:00 CEST") {
		t.Fatalf("the due time should read in the schedule's zone: %v", err)
	}
	due, _ := NextExpectedPing(m)
	if !due.Equal(at(24, 2, 0)) {
		t.Fatalf("next due %v, want 02:00 on the 24th", due)
	}
}

func TestScheduleGraceDefault(t *testing.T) {
	if ScheduleGrace(store.Monitor{}) != DefaultScheduleGrace {
		t.Fatal("a schedule without grace gets the default")
	}
	if ScheduleGrace(store.Monitor{Config: store.MonitorConfig{GraceSeconds: 1800}}) != 30*time.Minute {
		t.Fatal("an explicit grace is used")
	}
}
