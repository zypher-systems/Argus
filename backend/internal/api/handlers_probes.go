package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"argus/internal/alerting"
	"argus/internal/probe"
	"argus/internal/store"
)

func (s *Server) handleListProbeNodes(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListProbeNodes(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list probe nodes")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateProbeNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Region string `json:"region"`
	}
	if err := decodeJSON(r, &body, 4096); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	n, err := s.store.CreateProbeNode(r.Context(), body.Name, body.Region)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) handleDeleteProbeNode(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid probe node id")
		return
	}
	node, err := s.store.DeleteProbeNode(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "probe node not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to delete probe node")
		return
	}
	// A node that was reported silent will never report back. Close what
	// that opened, or it stays open on a pager for good.
	if node.AlertedDown {
		s.dispatch.DispatchAsync(alerting.Payload{
			Monitor: store.Monitor{Name: "Probe node " + node.Name, Target: node.Region},
			Type:    "probe_up", Key: probeNodeKey(node.ID), At: time.Now().UTC(),
			Detail: "was removed; its monitors are checked from their other locations, or from this server",
		})
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) probeNodeFromRequest(r *http.Request) (*store.ProbeNode, error) {
	tok := bearerToken(r)
	if tok == "" {
		tok = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return s.store.ProbeNodeByToken(r.Context(), tok)
}

func (s *Server) handleProbeJobs(w http.ResponseWriter, r *http.Request) {
	node, err := s.probeNodeFromRequest(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid probe token")
		return
	}
	list, err := s.store.ListEnabledMonitorsForNode(r.Context(), node.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list jobs")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type probeResultBody struct {
	MonitorID    int64    `json:"monitor_id"`
	Success      bool     `json:"success"`
	LatencyMs    *float64 `json:"latency_ms"`
	Error        string   `json:"error"`
	TLSExpiresAt *string  `json:"tls_expires_at"`
	// Sent by 2.9.0 probe nodes; older ones leave them out.
	ExpiresAt *string              `json:"expires_at"`
	Evidence  *store.CheckEvidence `json:"evidence"`
}

// handleProbeResults applies a check a remote probe node ran. It goes
// through the same path as a local check, so the location votes and a
// failure it confirms alerts, opens an incident, and updates dashboards.
func (s *Server) handleProbeResults(w http.ResponseWriter, r *http.Request) {
	node, err := s.probeNodeFromRequest(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid probe token")
		return
	}
	var body probeResultBody
	if err := decodeJSON(r, &body, 65536); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	m, err := s.store.GetMonitor(r.Context(), body.MonitorID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "monitor not found")
		return
	}
	if !store.AssignedToNode(m.Monitor, node.ID) {
		writeErr(w, http.StatusForbidden, "monitor is not assigned to this probe node")
		return
	}
	if !m.Enabled {
		// Paused while the node was checking; nothing to record.
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	outcome := probe.CheckOutcome{}
	if body.LatencyMs != nil {
		outcome.LatencyMs = *body.LatencyMs
	}
	if body.TLSExpiresAt != nil && *body.TLSExpiresAt != "" {
		if t, err := parseTime(*body.TLSExpiresAt); err == nil {
			outcome.TLSExpiresAt = &t
		}
	}
	if body.ExpiresAt != nil && *body.ExpiresAt != "" {
		if t, err := parseTime(*body.ExpiresAt); err == nil {
			outcome.ExpiresAt = &t
		}
	}
	if body.Evidence != nil {
		outcome.Evidence = *body.Evidence
	}
	var checkErr error
	if !body.Success {
		msg := strings.TrimSpace(body.Error)
		if msg == "" {
			msg = "check failed"
		}
		checkErr = errors.New(msg)
		if outcome.Evidence.Error == "" {
			outcome.Evidence.Error = msg
		}
		outcome.Evidence.Success = false
	}
	s.applier.Apply(r.Context(), m.Monitor, node.ID, outcome, checkErr)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
