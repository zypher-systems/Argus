package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"argus/internal/store"
)

func parseID64(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

func chiURLParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}

func (s *Server) handleListMaintenance(w http.ResponseWriter, r *http.Request) {
	windows, err := s.store.ListMaintenanceWindows(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load maintenance windows")
		return
	}
	if windows == nil {
		windows = []store.MaintenanceWindow{}
	}
	writeJSON(w, http.StatusOK, windows)
}

func (s *Server) handleCreateMaintenance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MonitorID *int64    `json:"monitor_id"`
		StartsAt  time.Time `json:"starts_at"`
		EndsAt    time.Time `json:"ends_at"`
		Note      string    `json:"note"`
		Public    bool      `json:"public"`
	}
	if err := decodeJSON(r, &body, 8192); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if body.StartsAt.IsZero() || body.EndsAt.IsZero() || !body.EndsAt.After(body.StartsAt) {
		writeErr(w, http.StatusBadRequest, "ends_at must be after starts_at")
		return
	}
	win := store.MaintenanceWindow{
		MonitorID: body.MonitorID,
		StartsAt:  body.StartsAt.UTC(),
		EndsAt:    body.EndsAt.UTC(),
		Note:      body.Note,
		Public:    body.Public,
	}
	if err := s.store.CreateMaintenanceWindow(r.Context(), &win); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create maintenance window")
		return
	}
	// An announced window is meant to be read by customers now, not once the
	// status page's cache happens to expire.
	if win.Public {
		s.statusCache.invalidate()
	}
	writeJSON(w, http.StatusCreated, win)
}

func (s *Server) handleDeleteMaintenance(w http.ResponseWriter, r *http.Request) {
	id, err := parseID64(chiURLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.store.DeleteMaintenanceWindow(r.Context(), id); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "maintenance window not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to delete maintenance window")
		return
	}
	s.statusCache.invalidate()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
