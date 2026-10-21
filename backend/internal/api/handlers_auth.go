package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"argus/internal/store"
)

type ctxKey int

const userKey ctxKey = 0

func (s *Server) cookieSecure(r *http.Request) bool {
	if s.cfg.CookieSecure {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure(r),
		MaxAge:   30 * 24 * 3600,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := s.rateKey(r)
	if !s.loginLimit.allow(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body, 4096); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	// Same answer as the address limit, and applied to unknown emails too,
	// so it reveals nothing about which accounts exist.
	account := accountKey(body.Email)
	if s.accountGuard.blocked(account, ip) {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}
	userID, err := s.store.VerifyLogin(r.Context(), body.Email, body.Password)
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			s.accountGuard.fail(account)
			writeErr(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeErr(w, http.StatusInternalServerError, "login failed")
		return
	}
	if err := s.store.DeleteSessionsForUser(r.Context(), userID); err != nil {
		writeErr(w, http.StatusInternalServerError, "session creation failed")
		return
	}
	token, err := s.store.CreateSession(r.Context(), userID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "session creation failed")
		return
	}
	s.loginLimit.reset(ip)
	s.accountGuard.success(account, ip)
	s.setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(r.Context(), c.Value)
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	userID := u.ID
	var body struct {
		Current string `json:"current"`
		Next    string `json:"password"`
	}
	if err := decodeJSON(r, &body, 4096); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if len(body.Next) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if err := s.store.UpdatePassword(r.Context(), userID, body.Current, body.Next); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			// Not 401: that means "not signed in", and the app treats it so.
			writeErr(w, http.StatusForbidden, "current password is incorrect")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to change password")
		return
	}
	token, err := s.store.CreateSession(r.Context(), userID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "session creation failed")
		return
	}
	s.setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	userID, ok, err := s.store.UserIDBySession(r.Context(), c.Value)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	u, err := s.store.GetUser(r.Context(), userID)
	if err != nil || u.Disabled {
		writeErr(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exists, err := s.store.UserExists(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "database error")
			return
		}
		if !exists {
			writeErr(w, http.StatusUnauthorized, "setup required")
			return
		}
		userID, ok, err := s.authenticate(r)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "database error")
			return
		}
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		user, err := s.store.GetUser(r.Context(), userID)
		if err != nil || user.Disabled {
			writeErr(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		if user.Role == store.RoleViewer && viewerBlocked(r.Method, r.URL.Path) {
			writeErr(w, http.StatusForbidden, "viewer role is read-only")
			return
		}
		if user.Role == store.RoleOperator && operatorBlocked(r.Method, r.URL.Path) {
			writeErr(w, http.StatusForbidden, "admin role required")
			return
		}
		ctx := context.WithValue(r.Context(), userKey, user)
		ctx = store.WithOrg(ctx, user.OrgID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) authenticate(r *http.Request) (int64, bool, error) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		return s.store.UserIDBySession(r.Context(), c.Value)
	}
	if tok := bearerToken(r); tok != "" {
		return s.store.UserIDByAPIToken(r.Context(), tok)
	}
	return 0, false, nil
}

func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return strings.TrimSpace(r.Header.Get("X-Api-Token"))
}

func decodeJSON(r *http.Request, dest any, maxBytes int64) error {
	if maxBytes > 0 {
		r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	}
	return json.NewDecoder(r.Body).Decode(dest)
}

func monitorIDFromURL(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid monitor id")
		return 0, false
	}
	return id, true
}
