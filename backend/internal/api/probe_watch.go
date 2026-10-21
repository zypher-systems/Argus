package api

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"argus/internal/alerting"
	"argus/internal/store"
)

func (s *Server) watchProbe(ctx context.Context) {
	s.checkProbe(ctx)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkProbe(ctx)
			s.checkProbeNodes(ctx, time.Now())
		}
	}
}

// probeNodeStaleAfter is how long a node may go without asking for work
// before it is called down. Nodes ask every 30 seconds.
const probeNodeStaleAfter = 3 * time.Minute

// checkProbeNodes alerts when a remote probe node with monitors assigned to
// it stops reporting, and again when it returns. Monitors checked only from
// such a node get no results and keep their last status, so without this a
// node dying with the site it watches looked like everything staying up.
func (s *Server) checkProbeNodes(ctx context.Context, now time.Time) {
	nodes, err := s.store.WatchedProbeNodes(ctx)
	if err != nil {
		slog.Error("probe node watch failed", "error", err)
		return
	}
	for _, n := range nodes {
		who := store.Monitor{Name: "Probe node " + n.Name, Target: n.Region}
		key := probeNodeKey(n.ID)
		since := n.CreatedAt
		if n.LastSeen != nil {
			since = *n.LastSeen
		}
		stale := n.Monitors > 0 && now.Sub(since) > probeNodeStaleAfter
		switch {
		case stale && !n.AlertedDown:
			detail := fmt.Sprintf("has not reported since %s; %d monitor(s) assigned to it are not being checked from there", since.UTC().Format("2 Jan 15:04 MST"), n.Monitors)
			if n.LastSeen == nil {
				detail = fmt.Sprintf("has never connected; %d monitor(s) assigned to it are not being checked from there", n.Monitors)
			}
			// The mark is taken with the alert, so two api replicas send
			// one and a failed queue leaves it to be tried again.
			if s.dispatch.Dispatch(alerting.Payload{Monitor: who, Type: "probe_down", Key: key, At: now.UTC(), Detail: detail},
				store.ClaimProbeNodeAlerted(n.ID, true)) {
				slog.Error("probe node stopped reporting", "node", n.Name, "monitors", n.Monitors)
			}
		case !stale && n.AlertedDown:
			detail := "is reporting again"
			if n.Monitors == 0 {
				detail = "no longer has monitors assigned to it"
			}
			if s.dispatch.Dispatch(alerting.Payload{Monitor: who, Type: "probe_up", Key: key, At: now.UTC(), Detail: detail},
				store.ClaimProbeNodeAlerted(n.ID, false)) {
				slog.Info("probe node reporting again", "node", n.Name)
			}
		}
	}
}

func (s *Server) checkProbe(ctx context.Context) {
	hb, err := s.store.GetProbeHeartbeat(ctx)
	if err != nil {
		slog.Error("probe heartbeat read failed", "error", err)
		return
	}
	now := time.Now()
	stale := store.ProbeStale(hb, now, probeStaleAfter)
	if hb == nil {
		return
	}
	// The mark is taken with the alert, in one transaction: two api replicas
	// send one alert between them, and a recovery that follows an alert still
	// waiting replaces it in the queue.
	if stale && !hb.AlertedDown {
		if s.dispatch.Dispatch(alerting.Payload{
			Monitor: store.Monitor{Name: "Argus probe", Target: "probe"},
			Type:    "probe_down",
			At:      now.UTC(),
			Detail:  "probe heartbeat is stale; monitors are frozen at last-known status",
		}, store.ClaimProbeAlerted(true)) {
			slog.Error("probe heartbeat stale — Argus is not checking monitors")
		}
		return
	}
	if !stale && hb.AlertedDown {
		if s.dispatch.Dispatch(alerting.Payload{
			Monitor: store.Monitor{Name: "Argus probe", Target: "probe"},
			Type:    "probe_up",
			At:      now.UTC(),
			Detail:  "probe heartbeat is fresh again",
		}, store.ClaimProbeAlerted(false)) {
			slog.Info("probe heartbeat recovered")
		}
	}
}

// probeNodeKey names a probe node's alerts for PagerDuty and Opsgenie.
func probeNodeKey(id int64) string { return fmt.Sprintf("argus-probe-node-%d", id) }
