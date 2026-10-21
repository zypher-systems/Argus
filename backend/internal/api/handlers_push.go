package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"argus/internal/probe"
	"argus/internal/store"
)

func ensurePushToken(cfg *store.MonitorConfig) error {
	if strings.TrimSpace(cfg.PushToken) != "" {
		return nil
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	cfg.PushToken = hex.EncodeToString(raw)
	return nil
}

// maxPushTokenLen is longer than any token Argus issues (48 hex characters)
// or accepts on import.
const maxPushTokenLen = 128

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	ip := s.rateKey(r)
	// Limiter keys are kept in memory, so they are built from the caller's
	// address and the monitor found, never from the token in the URL: that
	// is the caller's to choose, at any length, a new one every request.
	var m *store.Monitor
	err := store.ErrNotFound
	if len(token) <= maxPushTokenLen {
		m, err = s.store.GetMonitorByPushToken(r.Context(), token)
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			if !s.statusLimit.allow("push-unknown:" + ip) {
				writeErr(w, http.StatusTooManyRequests, "too many push requests")
				return
			}
			writeErr(w, http.StatusNotFound, "unknown push token")
			return
		}
		writeErr(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if !s.statusLimit.allow(fmt.Sprintf("push:%d:%s", m.ID, ip)) {
		writeErr(w, http.StatusTooManyRequests, "too many push requests")
		return
	}
	if !m.Enabled {
		writeErr(w, http.StatusForbidden, "monitor is paused")
		return
	}

	q := r.URL.Query()
	status := strings.ToLower(strings.TrimSpace(q.Get("status")))
	if status == "" {
		status = "up"
	}
	success := status == "up" || status == "ok"
	if status != "up" && status != "ok" && status != "down" {
		writeErr(w, http.StatusBadRequest, "status must be up or down")
		return
	}

	now := time.Now().UTC()
	if err := s.store.RecordPush(r.Context(), m.ID, now, success); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to record push")
		return
	}
	msg := strings.TrimSpace(q.Get("msg"))
	if msg == "" {
		msg = "reported down"
	}
	outcome := probe.CheckOutcome{
		Evidence:  store.CheckEvidence{Kind: "push", At: now, Success: success},
		NoLatency: true,
	}
	if ping := strings.TrimSpace(q.Get("ping")); ping != "" {
		if v, err := strconv.ParseFloat(ping, 64); err == nil {
			outcome.LatencyMs, outcome.NoLatency = v, false
		}
	}
	var pushErr error
	if !success {
		pushErr = errors.New(msg)
		outcome.Evidence.Error = msg
	}
	// The same path as a probe's check, so a job reporting down pages
	// someone and a recovery says so.
	s.applier.Apply(r.Context(), *m, store.LocalProbeNodeID, outcome, pushErr)
	current := "up"
	if st, err := s.store.MonitorStatus(r.Context(), m.ID); err == nil {
		current = st
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": current})
}
