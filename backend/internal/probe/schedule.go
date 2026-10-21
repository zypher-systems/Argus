package probe

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"argus/internal/store"
)

// DefaultScheduleGrace is how late a scheduled heartbeat may be when the
// monitor does not say. Jobs take time to run; without it, one due at 02:00
// that takes ten minutes would be late at 02:00:01 every night.
const DefaultScheduleGrace = 5 * time.Minute

// ParseSchedule reads a five-field cron expression (or @hourly, @daily,
// @weekly, @monthly, @yearly) in the named time zone, UTC when empty.
// @every is refused: a fixed period is what the plain interval is for.
func ParseSchedule(expr, tz string) (*cron.SpecSchedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, errors.New("schedule is empty")
	}
	if strings.HasPrefix(expr, "@every") {
		return nil, errors.New("use the check interval for a fixed period; a schedule is for cron times")
	}
	if strings.Contains(strings.ToUpper(expr), "TZ=") {
		return nil, errors.New("set the time zone separately, not in the expression")
	}
	loc := time.UTC
	if tz = strings.TrimSpace(tz); tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return nil, fmt.Errorf("unknown time zone %q", tz)
		}
		loc = l
	}
	parsed, err := cron.ParseStandard(expr)
	if err != nil {
		return nil, fmt.Errorf("not a cron schedule: %v", err)
	}
	spec, ok := parsed.(*cron.SpecSchedule)
	if !ok {
		return nil, errors.New("not a cron schedule")
	}
	spec.Location = loc
	return spec, nil
}

// NextRuns lists the next n times a schedule fires after from.
func NextRuns(expr, tz string, from time.Time, n int) ([]time.Time, error) {
	spec, err := ParseSchedule(expr, tz)
	if err != nil {
		return nil, err
	}
	out := make([]time.Time, 0, n)
	t := from
	for i := 0; i < n; i++ {
		t = spec.Next(t)
		if t.IsZero() {
			break
		}
		out = append(out, t)
	}
	return out, nil
}

// NextExpectedPing is when a scheduled heartbeat is next due: the first
// scheduled time after the last ping, or after the monitor was created.
func NextExpectedPing(m store.Monitor) (time.Time, error) {
	spec, err := ParseSchedule(m.Config.Schedule, m.Config.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	if m.LastPushAt == nil {
		// In the schedule's own zone, so "due 02:00" means 02:00 where it runs.
		return spec.Next(m.CreatedAt).In(spec.Location), nil
	}
	ping := m.LastPushAt.In(spec.Location)

	// A ping a moment before its time is that time's ping. A job started by
	// cron on a machine a couple of seconds fast pings at 01:59:58; counted
	// as late for yesterday, it left today's 02:00 "missing" and the monitor
	// went down. How early still counts scales with the schedule, so an
	// every-minute job is not given five minutes.
	first := spec.Next(ping)
	early := first.Sub(ping)
	if gap := spec.Next(first).Sub(first); early <= earlyPing(gap) {
		first = spec.Next(first)
	}

	// When clocks go back an hour, a time in the repeated hour comes twice.
	// A job that ran at 01:30 is not due again at the second 01:30 the same
	// night: cron runs a fixed-time job once.
	if sameDay(first, ping) && !wallClockAfter(first, ping) {
		first = spec.Next(first)
	}
	return first.In(spec.Location), nil
}

// earlyPing is how far ahead of its scheduled time a ping may arrive and
// still count for that time: a tenth of the gap between runs, at most five
// minutes.
func earlyPing(gap time.Duration) time.Duration {
	return min(gap/10, 5*time.Minute)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// wallClockAfter says whether a's time of day, as a clock on the wall would
// show it, is later than b's.
func wallClockAfter(a, b time.Time) bool {
	as := a.Hour()*3600 + a.Minute()*60 + a.Second()
	bs := b.Hour()*3600 + b.Minute()*60 + b.Second()
	return as > bs
}

// ScheduleGrace is how late a scheduled ping may be.
func ScheduleGrace(m store.Monitor) time.Duration {
	if m.Config.GraceSeconds > 0 {
		return time.Duration(m.Config.GraceSeconds) * time.Second
	}
	return DefaultScheduleGrace
}
