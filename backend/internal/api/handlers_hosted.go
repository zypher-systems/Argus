package api

import (
	"net/http"
	"strings"

	"argus/internal/store"
)

func (s *Server) hosted() bool {
	return s.cfg != nil && s.cfg.Hosted
}

func (s *Server) registrationOpen(r *http.Request) bool {
	if !s.hosted() {
		return false
	}
	on, err := s.store.GetRegistration(r.Context())
	if err != nil {
		return false
	}
	return on
}

func (s *Server) handleHostedStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"hosted":       s.hosted(),
		"registration": s.registrationOpen(r),
	})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.hosted() {
		writeErr(w, http.StatusForbidden, "registration is only available on hosted Argus")
		return
	}
	if !s.registrationOpen(r) {
		writeErr(w, http.StatusForbidden, "registration is closed")
		return
	}
	ip := s.rateKey(r)
	if !s.loginLimit.allow(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
		OrgName  string `json:"org_name"`
	}
	if err := decodeJSON(r, &body, 8192); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	orgName := strings.TrimSpace(body.OrgName)
	if orgName == "" {
		orgName = strings.TrimSpace(body.Name)
	}
	if orgName == "" {
		orgName = strings.TrimSpace(body.Email)
	}
	if orgName == "" {
		writeErr(w, http.StatusBadRequest, "organization name is required")
		return
	}
	slug, err := s.store.UniqueOrgSlug(r.Context(), orgName)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create organization")
		return
	}
	plan := s.cfg.HostedDefaultPlan
	if plan == "" {
		plan = store.PlanCommunity
	}
	org, err := s.store.CreateOrg(r.Context(), orgName, slug, plan)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := store.WithOrg(r.Context(), org.ID)
	u, err := s.store.CreateUser(ctx, body.Email, body.Name, store.RoleAdmin, body.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	token, err := s.store.CreateSession(ctx, u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "session creation failed")
		return
	}
	s.loginLimit.reset(ip)
	s.setSessionCookie(w, r, token)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "org": org})
}

func (s *Server) handleGetOrg(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil || u.OrgID <= 0 {
		writeErr(w, http.StatusNotFound, "organization not found")
		return
	}
	org, err := s.store.GetOrg(r.Context(), u.OrgID)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "organization not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load organization")
		return
	}
	writeJSON(w, http.StatusOK, org)
}

func (s *Server) handleUpdateOrg(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil || u.Role != store.RoleAdmin {
		writeErr(w, http.StatusForbidden, "admin role required")
		return
	}
	org, err := s.store.GetOrg(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "organization not found")
		return
	}
	var body struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := decodeJSON(r, &body, 4096); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = org.Name
	}
	slug := strings.ToLower(strings.TrimSpace(body.Slug))
	if slug == "" {
		slug = org.Slug
	}
	// The plan is what the organization pays for, so its own administrator
	// does not get to set it.
	updated, err := s.store.UpdateOrg(r.Context(), org.ID, name, slug, org.Plan)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
