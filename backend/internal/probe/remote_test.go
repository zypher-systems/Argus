package probe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"argus/internal/store"
)

// TestRemoteProbeHonoursIntervals is the bug 2.9.0 fixed: a remote node ran
// every monitor on every two-second poll, whatever its interval.
func TestRemoteProbeHonoursIntervals(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	e := newRemoteEngine("http://unused", "token")
	e.now = func() time.Time { return now }
	var mu sync.Mutex
	runs := map[int64]int{}
	e.run = func(_ context.Context, m store.Monitor) {
		mu.Lock()
		runs[m.ID]++
		mu.Unlock()
	}
	e.monitors, e.nextRun = mergeSchedule(nil, nil, []store.Monitor{
		{ID: 1, IntervalSeconds: 60},
		{ID: 2, IntervalSeconds: 300},
	}, now)

	// Ten minutes of 500ms ticks, letting each started check finish as a
	// real one would well within its interval.
	for i := 0; i < 1200; i++ {
		now = now.Add(remoteTick)
		e.dispatchDue(context.Background())
		waitIdle(t, e)
	}
	mu.Lock()
	defer mu.Unlock()
	// First run at half an interval, then every interval: 10 for 60s, 2 for 300s.
	if runs[1] < 9 || runs[1] > 11 {
		t.Fatalf("a 60s monitor over ten minutes should run about 10 times, ran %d", runs[1])
	}
	if runs[2] < 1 || runs[2] > 3 {
		t.Fatalf("a 300s monitor over ten minutes should run about twice, ran %d", runs[2])
	}
}

func TestRemoteProbeNeverOverlapsAMonitor(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	e := newRemoteEngine("http://unused", "token")
	e.now = func() time.Time { return now }
	release := make(chan struct{})
	var concurrent, peak int32
	e.run = func(_ context.Context, _ store.Monitor) {
		n := atomic.AddInt32(&concurrent, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		<-release
		atomic.AddInt32(&concurrent, -1)
	}
	e.monitors, e.nextRun = mergeSchedule(nil, nil, []store.Monitor{{ID: 7, IntervalSeconds: 10}}, now)
	// The check hangs far longer than its interval.
	for i := 0; i < 200; i++ {
		now = now.Add(remoteTick)
		e.dispatchDue(context.Background())
	}
	time.Sleep(50 * time.Millisecond)
	if p := atomic.LoadInt32(&peak); p != 1 {
		t.Fatalf("a slow check must not be started again while running, peak %d", p)
	}
	close(release)
}

func TestRemoteProbeReportsEvidence(t *testing.T) {
	var got map[string]any
	var mu sync.Mutex
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/probe/jobs":
			_ = json.NewEncoder(w).Encode([]store.Monitor{{ID: 9, Type: "tcp", Target: "127.0.0.1:9", IntervalSeconds: 60,
				Config: store.MonitorConfig{TimeoutSeconds: 2}}})
		case "/api/probe/results":
			mu.Lock()
			_ = json.NewDecoder(r.Body).Decode(&got)
			mu.Unlock()
		}
	}))
	t.Cleanup(api.Close)
	e := newRemoteEngine(api.URL, "token")
	jobs, err := e.fetchJobs(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %v %v", jobs, err)
	}
	e.runOne(context.Background(), jobs[0])
	mu.Lock()
	defer mu.Unlock()
	if got["success"] != false || got["error"] == "" {
		t.Fatalf("a refused connection should be reported as a failure: %v", got)
	}
	ev, ok := got["evidence"].(map[string]any)
	if !ok || ev["kind"] != "tcp" || ev["error"] == "" {
		t.Fatalf("the result should carry evidence: %v", got["evidence"])
	}
}

func waitIdle(t *testing.T, e *remoteEngine) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		n := len(e.running)
		e.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("checks did not finish")
}
