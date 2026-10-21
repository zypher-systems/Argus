package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// apiHarness is a test server with the bootstrap administrator signed in.
type apiHarness struct {
	t      *testing.T
	s      *Server
	cookie *http.Cookie
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	s := testAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"change-me-too"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	return &apiHarness{t: t, s: s, cookie: rec.Result().Cookies()[0]}
}

func (h *apiHarness) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(h.cookie)
	rec := httptest.NewRecorder()
	h.s.Handler().ServeHTTP(rec, req)
	return rec
}

func (h *apiHarness) public(path, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	h.s.Handler().ServeHTTP(rec, req)
	return rec
}
