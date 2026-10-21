package probe

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"argus/internal/store"
)

// silentConn is an ICMP socket whose target never answers.
type silentConn struct {
	mu       sync.Mutex
	deadline time.Time
}

func (c *silentConn) WriteTo(b []byte, dst net.Addr) (int, error) { return len(b), nil }
func (c *silentConn) Close() error                                { return nil }
func (c *silentConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}
func (c *silentConn) ReadFrom(b []byte) (int, net.Addr, error) {
	c.mu.Lock()
	d := c.deadline
	c.mu.Unlock()
	time.Sleep(time.Until(d))
	return 0, nil, os.ErrDeadlineExceeded
}

// Hosts that do not answer each cost their own timeout, at the same time.
// Behind one shared socket and its lock they cost it one after another:
// twenty of them here would take six seconds, not a third of one.
func TestUnreachableHostsArePingedSideBySide(t *testing.T) {
	p := NewPinger(300 * time.Millisecond)
	p.open = func(v6 bool) (icmpConn, bool, error) { return &silentConn{}, true, nil }
	const hosts = 20
	start := time.Now()
	var wg sync.WaitGroup
	for i := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Ping(context.Background(), net.IPv4(192, 0, 2, byte(i+1))); err == nil {
				t.Error("a host that never answers reported a reply")
			}
		}()
	}
	wg.Wait()
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Fatalf("%d unanswered pings took %s; they are waiting on each other", hosts, took.Round(time.Millisecond))
	}
}

// A ping gives up at the caller's deadline when that comes first.
func TestPingHonoursTheCallersDeadline(t *testing.T) {
	p := NewPinger(5 * time.Second)
	p.open = func(v6 bool) (icmpConn, bool, error) { return &silentConn{}, true, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _ = p.Ping(ctx, net.IPv4(192, 0, 2, 1))
	if took := time.Since(start); took > time.Second {
		t.Fatalf("the ping ran for %s past a 200ms deadline", took.Round(time.Millisecond))
	}
}

func TestPingReportsASocketItCannotOpen(t *testing.T) {
	p := NewPinger(time.Second)
	p.open = func(v6 bool) (icmpConn, bool, error) { return nil, false, errors.New("operation not permitted") }
	if _, err := p.Ping(context.Background(), net.IPv4(192, 0, 2, 1)); err == nil {
		t.Fatal("want the error")
	}
}

func scheduleEngine(n int) (*Engine, []store.Monitor) {
	e := &Engine{monitors: map[int64]store.Monitor{}, nextRun: map[int64]time.Time{}}
	var list []store.Monitor
	for i := 1; i <= n; i++ {
		m := store.Monitor{ID: int64(i), Type: "http", IntervalSeconds: 60}
		e.monitors[m.ID] = m
		// Lower ids have been waiting longer.
		e.nextRun[m.ID] = time.Now().Add(-time.Duration(n-i+1) * time.Second)
		list = append(list, m)
	}
	return e, list
}

// With every worker busy and the queue full, the scheduler does not wait:
// what does not fit stays due, most overdue first, and nothing is dropped.
// Waiting here held up the heartbeat until the api announced the probe dead.
func TestSchedulerDoesNotWaitOnAFullQueue(t *testing.T) {
	e, _ := scheduleEngine(5)
	jobs := make(chan store.Monitor, 2)

	done := make(chan struct{})
	go func() { e.scheduleDue(jobs); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the scheduler is waiting for room in the queue")
	}
	if a, b := <-jobs, <-jobs; a.ID != 1 || b.ID != 2 {
		t.Fatalf("the two longest overdue go first, got %d and %d", a.ID, b.ID)
	}

	// Nothing was lost: the other three are still due and go next.
	big := make(chan store.Monitor, 10)
	e.scheduleDue(big)
	close(big)
	var rest []int64
	for m := range big {
		rest = append(rest, m.ID)
	}
	if len(rest) != 3 || rest[0] != 3 || rest[1] != 4 || rest[2] != 5 {
		t.Fatalf("the checks that did not fit should be offered again, in order: %v", rest)
	}
	// And the two that were queued are not due again until their interval.
	again := make(chan store.Monitor, 10)
	e.scheduleDue(again)
	if len(again) != 0 {
		t.Fatalf("%d check(s) were queued twice", len(again))
	}
}

// "Check now" requests run beside the schedule, not in it.
func TestCheckNowDoesNotHoldUpTheScheduler(t *testing.T) {
	e, st := alertEngine(t)
	ctx := context.Background()
	release := make(chan struct{})
	e.run = func(ctx context.Context, p *Pinger, m store.Monitor) (CheckOutcome, error) {
		<-release // a target that takes its whole timeout
		return CheckOutcome{LatencyMs: 1}, nil
	}
	var ids []int64
	for _, name := range []string{"slow-a", "slow-b"} {
		m := newMonitor(t, st, store.Monitor{Name: name})
		id, err := st.EnqueueCheckNow(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	done := make(chan struct{})
	go func() { e.drainCheckNow(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the scheduler waited for a check-now to finish")
	}

	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for _, id := range ids {
		for {
			req, err := st.GetCheckNow(ctx, id)
			if err == nil && req != nil && req.Status == "done" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("check-now %d never finished: %+v %v", id, req, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}
