package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"argus/internal/store"
)

func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	openOnly := r.URL.Query().Get("open") == "1" || r.URL.Query().Get("open") == "true"
	list, err := s.store.ListIncidents(r.Context(), openOnly)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list incidents")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func incidentIDFromURL(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid incident id")
		return 0, false
	}
	return id, true
}

func (s *Server) handleGetIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := incidentIDFromURL(w, r)
	if !ok {
		return
	}
	inc, err := s.store.GetIncident(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "incident not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load incident")
		return
	}
	writeJSON(w, http.StatusOK, inc)
}

func (s *Server) handleCreateIncident(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MonitorID *int64 `json:"monitor_id"`
		Title     string `json:"title"`
		Impact    string `json:"impact"`
		Status    string `json:"status"`
		Message   string `json:"message"`
		Public    *bool  `json:"public"`
	}
	if err := decodeJSON(r, &body, 8192); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeErr(w, http.StatusBadRequest, "title is required")
		return
	}
	if len(title) > 200 {
		writeErr(w, http.StatusBadRequest, "title must be at most 200 characters")
		return
	}
	impact := strings.ToLower(strings.TrimSpace(body.Impact))
	if impact == "" {
		impact = "major"
	}
	if !store.ValidIncidentImpact(impact) {
		writeErr(w, http.StatusBadRequest, "invalid impact")
		return
	}
	status := strings.ToLower(strings.TrimSpace(body.Status))
	if status == "" {
		status = "investigating"
	}
	if !store.ValidIncidentStatus(status) {
		writeErr(w, http.StatusBadRequest, "invalid status")
		return
	}
	if body.MonitorID != nil {
		if err := s.store.ValidateMonitorIDs(r.Context(), []int64{*body.MonitorID}); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	pub := true
	if body.Public != nil {
		pub = *body.Public
	}
	msg := strings.TrimSpace(body.Message)
	if msg == "" {
		msg = "Incident opened"
	}
	if len(msg) > 2000 {
		writeErr(w, http.StatusBadRequest, "message must be at most 2000 characters")
		return
	}
	inc := store.Incident{
		MonitorID:  body.MonitorID,
		Status:     status,
		Title:      title,
		Impact:     impact,
		Public:     pub,
		AutoOpened: false,
		OpenedAt:   time.Now().UTC(),
		Updates:    []store.IncidentUpdate{{Status: status, Message: msg}},
	}
	if err := s.store.CreateIncident(r.Context(), &inc); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create incident")
		return
	}
	s.statusCache.invalidate()
	s.dispatch.NotifyIncidentAsync(inc)
	writeJSON(w, http.StatusCreated, inc)
}

func (s *Server) handleIncidentUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := incidentIDFromURL(w, r)
	if !ok {
		return
	}
	var body struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := decodeJSON(r, &body, 8192); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	status := strings.ToLower(strings.TrimSpace(body.Status))
	if !store.ValidIncidentStatus(status) {
		writeErr(w, http.StatusBadRequest, "invalid status")
		return
	}
	msg := strings.TrimSpace(body.Message)
	if msg == "" {
		writeErr(w, http.StatusBadRequest, "message is required")
		return
	}
	if len(msg) > 2000 {
		writeErr(w, http.StatusBadRequest, "message must be at most 2000 characters")
		return
	}
	inc, err := s.store.AddIncidentUpdate(r.Context(), id, status, msg, time.Now().UTC())
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "incident not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.statusCache.invalidate()
	s.dispatch.NotifyIncidentAsync(*inc)
	writeJSON(w, http.StatusOK, inc)
}
