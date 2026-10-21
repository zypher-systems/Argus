package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"argus/internal/store"
)

const probeStaleAfter = 45 * time.Second

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Pool.Ping(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.healthPayload(r.Context()))
}

func (s *Server) healthPayload(ctx context.Context) map[string]any {
	out := map[string]any{"status": "ok"}
	hb, err := s.store.GetProbeHeartbeat(ctx)
	probe := map[string]any{"ok": false}
	if err == nil && hb != nil {
		ok := !store.ProbeStale(hb, time.Now(), probeStaleAfter)
		probe["ok"] = ok
		probe["last_seen"] = hb.LastSeen.UTC().Format(time.RFC3339)
	}
	out["probe"] = probe
	return out
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	up, down, pending, err := s.store.MonitorSummary(ctx)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "metrics unavailable")
		return
	}
	hb, _ := s.store.GetProbeHeartbeat(ctx)
	probeUp := 0
	if hb != nil && !store.ProbeStale(hb, time.Now(), probeStaleAfter) {
		probeUp = 1
	}
	dbUp := 1
	if err := s.store.Pool.Ping(ctx); err != nil {
		dbUp = 0
	}

	var b strings.Builder
	b.WriteString("# HELP argus_monitors Number of monitors by status.\n")
	b.WriteString("# TYPE argus_monitors gauge\n")
	fmt.Fprintf(&b, "argus_monitors{status=\"up\"} %d\n", up)
	fmt.Fprintf(&b, "argus_monitors{status=\"down\"} %d\n", down)
	fmt.Fprintf(&b, "argus_monitors{status=\"pending\"} %d\n", pending)
	b.WriteString("# HELP argus_probe_up 1 if the probe heartbeat is fresh.\n")
	b.WriteString("# TYPE argus_probe_up gauge\n")
	fmt.Fprintf(&b, "argus_probe_up %d\n", probeUp)
	b.WriteString("# HELP argus_db_up 1 if Postgres answers ping.\n")
	b.WriteString("# TYPE argus_db_up gauge\n")
	fmt.Fprintf(&b, "argus_db_up %d\n", dbUp)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}
