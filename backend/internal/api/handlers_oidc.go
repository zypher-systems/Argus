package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"argus/internal/store"
)

const oidcCookie = "argus_oidc"

func (s *Server) oidcConfigured(r *http.Request) (store.OIDCSettings, bool) {
	all, err := s.store.LoadSettings(r.Context())
	if err != nil {
		return store.OIDCSettings{}, false
	}
	o := all.OIDC
	if !o.Enabled || strings.TrimSpace(o.Issuer) == "" || strings.TrimSpace(o.ClientID) == "" || o.ClientSecret == "" {
		return o, false
	}
	return o, true
}

func (s *Server) handleOIDCStatus(w http.ResponseWriter, r *http.Request) {
	_, ok := s.oidcConfigured(r)
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": ok})
}

func (s *Server) redirectBase(r *http.Request) string {
	all, err := s.store.LoadSettings(r.Context())
	if err == nil {
		if u := strings.TrimRight(strings.TrimSpace(all.General.BaseURL), "/"); u != "" {
			return u
		}
	}
	scheme := "http"
	if s.cookieSecure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	o, ok := s.oidcConfigured(r)
	if !ok {
		writeErr(w, http.StatusForbidden, "single sign-on is not enabled")
		return
	}
	provider, err := oidc.NewProvider(r.Context(), o.Issuer)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "oidc issuer is unreachable")
		return
	}
	state := randomHex(16)
	nonce := randomHex(16)
	http.SetCookie(w, &http.Cookie{
		Name:     oidcCookie,
		Value:    state + "." + nonce,
		Path:     "/api/auth/oidc",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure(r),
		MaxAge:   600,
	})
	cfg := oauth2.Config{
		ClientID:     o.ClientID,
		ClientSecret: o.ClientSecret,
		RedirectURL:  s.redirectBase(r) + "/api/auth/oidc/callback",
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
	http.Redirect(w, r, cfg.AuthCodeURL(state, oidc.Nonce(nonce)), http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	o, ok := s.oidcConfigured(r)
	if !ok {
		writeErr(w, http.StatusForbidden, "single sign-on is not enabled")
		return
	}
	c, err := r.Cookie(oidcCookie)
	if err != nil || c.Value == "" {
		writeErr(w, http.StatusBadRequest, "missing oidc state")
		return
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 || r.URL.Query().Get("state") != parts[0] {
		writeErr(w, http.StatusBadRequest, "invalid oidc state")
		return
	}
	nonce := parts[1]
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: "", Path: "/api/auth/oidc", MaxAge: -1})

	provider, err := oidc.NewProvider(r.Context(), o.Issuer)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "oidc issuer is unreachable")
		return
	}
	cfg := oauth2.Config{
		ClientID:     o.ClientID,
		ClientSecret: o.ClientSecret,
		RedirectURL:  s.redirectBase(r) + "/api/auth/oidc/callback",
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
	tok, err := cfg.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "oidc token exchange failed")
		return
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		writeErr(w, http.StatusBadGateway, "oidc response missing id_token")
		return
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: o.ClientID})
	idToken, err := verifier.Verify(r.Context(), rawID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "oidc token failed verification")
		return
	}
	if idToken.Nonce != nonce {
		writeErr(w, http.StatusBadRequest, "oidc nonce mismatch")
		return
	}
	var claims struct {
		Email string `json:"email"`
		// Most providers send a boolean, a few send the string "true", and
		// some leave it out.
		EmailVerified     json.RawMessage `json:"email_verified"`
		Name              string          `json:"name"`
		PreferredUsername string          `json:"preferred_username"`
	}
	if err := idToken.Claims(&claims); err != nil {
		writeErr(w, http.StatusBadGateway, "oidc claims missing")
		return
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" {
		writeErr(w, http.StatusBadRequest, "oidc token has no email claim")
		return
	}
	name := strings.TrimSpace(claims.Name)
	if name == "" {
		name = claims.PreferredUsername
	}
	role := o.DefaultRole
	if !store.ValidRole(role) {
		role = store.RoleViewer
	}
	user, err := s.store.FindOrCreateOIDCUser(r.Context(), idToken.Subject, email, name, role, emailVerified(claims.EmailVerified))
	if err != nil {
		if errors.Is(err, store.ErrEmailUnverified) {
			writeErr(w, http.StatusForbidden, "An Argus account already uses this email address, and your identity provider did not confirm the address is yours. Ask an Argus administrator.")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if user.Disabled {
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	_ = s.store.DeleteSessionsForUser(r.Context(), user.ID)
	session, err := s.store.CreateSession(r.Context(), user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "session creation failed")
		return
	}
	s.setSessionCookie(w, r, session)
	http.Redirect(w, r, "/", http.StatusFound)
}

// emailVerified reads the email_verified claim. Only an explicit true
// counts: a provider that leaves the claim out has not said the address was
// checked.
func emailVerified(raw json.RawMessage) bool {
	v := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	return strings.EqualFold(v, "true")
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
