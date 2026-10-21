package probe

import (
	"fmt"
	"strings"
	"time"

	"argus/internal/store"
)

func checkPush(m store.Monitor, now time.Time) (CheckOutcome, error) {
	if strings.TrimSpace(m.Config.Schedule) != "" {
		return checkScheduledPush(m, now)
	}
	if m.LastPushAt == nil {
		if now.Sub(m.CreatedAt) < time.Duration(m.IntervalSeconds)*time.Second {
			return CheckOutcome{}, errAwaitingPush
		}
		return CheckOutcome{}, fmt.Errorf("awaiting first push")
	}
	grace := m.Config.GraceSeconds
	if grace < 0 {
		grace = 0
	}
	limit := time.Duration(m.IntervalSeconds+grace) * time.Second
	age := now.Sub(*m.LastPushAt)
	if age > limit {
		return CheckOutcome{NoLatency: true}, fmt.Errorf("heartbeat overdue (%s)", age.Truncate(time.Second))
	}
	if m.LastPushDown {
		return CheckOutcome{NoLatency: true}, errReportedDown
	}
	return CheckOutcome{NoLatency: true}, nil
}

// errReportedDown is a heartbeat that arrived on time and said the job
// failed. Arriving is not the same as working: without this the freshness
// check answered "up" between the job's own failure reports, and the two
// took turns so the failures never added up to an alert.
var errReportedDown = fmt.Errorf("the last heartbeat reported a failure")

var errAwaitingPush = fmt.Errorf("awaiting first push")

// checkScheduledPush holds a heartbeat to its cron times: after each ping the
// next one is due at the schedule's next time, plus grace.
func checkScheduledPush(m store.Monitor, now time.Time) (CheckOutcome, error) {
	out := CheckOutcome{NoLatency: true}
	due, err := NextExpectedPing(m)
	if err != nil {
		return out, fmt.Errorf("schedule: %w", err)
	}
	if !now.After(due.Add(ScheduleGrace(m))) {
		if m.LastPushAt == nil {
			// Not due yet and never pinged: stay pending, not up.
			return out, errAwaitingPush
		}
		if m.LastPushDown {
			return out, errReportedDown
		}
		return out, nil
	}
	loc := due.Location()
	when := due.In(loc).Format("Mon 2 Jan 15:04 MST")
	if m.LastPushAt == nil {
		return out, fmt.Errorf("no ping yet; the first was due %s", when)
	}
	return out, fmt.Errorf("ping due %s did not arrive; last ping %s ago", when, now.Sub(*m.LastPushAt).Truncate(time.Minute))
}
