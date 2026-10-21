package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"argus/internal/store"
)

// Remote probes refresh their job list this often, and run each monitor on
// its own interval in between.
const (
	remoteJobRefresh = 30 * time.Second
	remoteTick       = 500 * time.Millisecond
	remoteWorkers    = 16
)

type remoteEngine struct {
	apiURL string
	token  string
	client *http.Client
	pinger *Pinger

	mu       sync.Mutex
	monitors map[int64]store.Monitor
	nextRun  map[int64]time.Time
	running  map[int64]bool
	slots    chan struct{}
	now      func() time.Time
	run      func(ctx context.Context, m store.Monitor)
}

func newRemoteEngine(apiURL, token string) *remoteEngine {
	e := &remoteEngine{
		apiURL:   trimSlash(apiURL),
		token:    token,
		client:   &http.Client{Timeout: 30 * time.Second},
		monitors: map[int64]store.Monitor{},
		nextRun:  map[int64]time.Time{},
		running:  map[int64]bool{},
		slots:    make(chan struct{}, remoteWorkers),
		now:      time.Now,
	}
	e.run = e.runOne
	return e
}

func RunRemote(ctx context.Context, apiURL, token string) error {
	if apiURL == "" || token == "" {
		return fmt.Errorf("ARGUS_API_URL and ARGUS_PROBE_TOKEN are required for a remote probe")
	}
	e := newRemoteEngine(apiURL, token)
	e.pinger = NewPinger(maxPingTimeout)
	defer e.pinger.Close()

	e.refresh(ctx)
	refresh := time.NewTicker(remoteJobRefresh)
	defer refresh.Stop()
	tick := time.NewTicker(remoteTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-refresh.C:
			e.refresh(ctx)
		case <-tick.C:
			e.dispatchDue(ctx)
		}
	}
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// refresh fetches this node's monitors and reschedules them the way the local
// engine does: a new or re-timed monitor runs half an interval from now, and
// the rest keep their place.
func (e *remoteEngine) refresh(ctx context.Context) {
	jobs, err := e.fetchJobs(ctx)
	if err != nil {
		slog.Error("probe jobs request failed", "error", err)
		return
	}
	e.mu.Lock()
	e.monitors, e.nextRun = mergeSchedule(e.monitors, e.nextRun, jobs, e.now())
	e.mu.Unlock()
}

func (e *remoteEngine) fetchJobs(ctx context.Context) ([]store.Monitor, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.apiURL+"/api/probe/jobs", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("probe jobs rejected: status %d", resp.StatusCode)
	}
	var jobs []store.Monitor
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return nil, fmt.Errorf("probe jobs decode failed: %w", err)
	}
	return jobs, nil
}

// dispatchDue starts every monitor whose time has come, a bounded number at
// once, and never a second run of a monitor still being checked.
func (e *remoteEngine) dispatchDue(ctx context.Context) {
	now := e.now()
	e.mu.Lock()
	var due []store.Monitor
	for id, m := range e.monitors {
		if e.running[id] || now.Before(e.nextRun[id]) {
			continue
		}
		interval := time.Duration(max(m.IntervalSeconds, 10)) * time.Second
		e.nextRun[id] = now.Add(interval)
		e.running[id] = true
		due = append(due, m)
	}
	e.mu.Unlock()
	for _, m := range due {
		select {
		case e.slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		go func(m store.Monitor) {
			defer func() {
				<-e.slots
				e.mu.Lock()
				delete(e.running, m.ID)
				e.mu.Unlock()
			}()
			e.run(ctx, m)
		}(m)
	}
}

func (e *remoteEngine) runOne(ctx context.Context, m store.Monitor) {
	outcome, pingErr := Check(ctx, e.pinger, m)
	if errors.Is(pingErr, errAwaitingPush) {
		return
	}
	success := pingErr == nil
	body := map[string]any{
		"monitor_id": m.ID,
		"success":    success,
		"evidence":   outcome.Evidence,
	}
	if outcome.LatencyMs > 0 || success {
		body["latency_ms"] = outcome.LatencyMs
	}
	if pingErr != nil {
		body["error"] = pingErr.Error()
	}
	if outcome.TLSExpiresAt != nil {
		body["tls_expires_at"] = outcome.TLSExpiresAt.UTC().Format(time.RFC3339)
	}
	if outcome.ExpiresAt != nil {
		body["expires_at"] = outcome.ExpiresAt.UTC().Format(time.RFC3339)
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.apiURL+"/api/probe/results", bytes.NewReader(raw))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		slog.Error("probe result post failed", "monitor", m.Name, "error", err)
		return
	}
	_ = resp.Body.Close()
}
