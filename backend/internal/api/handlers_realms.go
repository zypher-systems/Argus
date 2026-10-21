package api

import (
	"net/http"
	"strings"

	"argus/internal/store"
)

func (s *Server) handleListRealms(w http.ResponseWriter, r *http.Request) {
	realms, err := s.store.ListRealms(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list realms")
		return
	}
	if realms == nil {
		realms = []store.Realm{}
	}
	writeJSON(w, http.StatusOK, realms)
}

func (s *Server) handleCreateRealm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &body, 4096); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	realm, err := s.store.CreateRealm(r.Context(), body.Name)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "already exists") || strings.Contains(msg, "required") || strings.Contains(msg, "at most") {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to create realm")
		return
	}
	writeJSON(w, http.StatusCreated, realm)
}

func (s *Server) handleUpdateRealm(w http.ResponseWriter, r *http.Request) {
	id, err := parseID64(chiURLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &body, 4096); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	realm, err := s.store.RenameRealm(r.Context(), id, body.Name)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "realm not found")
			return
		}
		msg := err.Error()
		if strings.Contains(msg, "already exists") || strings.Contains(msg, "required") || strings.Contains(msg, "at most") {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to rename realm")
		return
	}
	writeJSON(w, http.StatusOK, realm)
}

func (s *Server) handleDeleteRealm(w http.ResponseWriter, r *http.Request) {
	id, err := parseID64(chiURLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.store.DeleteRealm(r.Context(), id); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "realm not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to delete realm")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
