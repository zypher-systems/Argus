package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"argus/internal/store"
)

// signIn logs in as email from the given client address (behind the bundled
// proxy) and returns the response and session cookie.
func signIn(t *testing.T, s *Server, email, password, addr string) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "172.18.0.5:40000"
	req.Header.Set("X-Forwarded-For", addr)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var cookie *http.Cookie
	if cs := rec.Result().Cookies(); len(cs) > 0 {
		cookie = cs[0]
	}
	return rec, cookie
}

func call(s *Server, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "172.18.0.5:40000"
	req.Header.Set("X-Forwarded-For", "198.51.100.10")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestExportIsAdminOnlyAndAudited(t *testing.T) {
	s := testAPI(t)
	s.cfg.TrustedProxies = 1
	ctx := context.Background()
	if _, err := s.store.CreateUser(ctx, "viewer@example.com", "Viewer", store.RoleViewer, "viewer-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.CreateUser(ctx, "operator@example.com", "Operator", store.RoleOperator, "operator-password"); err != nil {
		t.Fatal(err)
	}
	_, admin := signIn(t, s, "admin@example.com", "change-me-too", "198.51.100.10")
	_, viewer := signIn(t, s, "viewer@example.com", "viewer-password", "198.51.100.11")
	_, operator := signIn(t, s, "operator@example.com", "operator-password", "198.51.100.12")

	body := `{"name":"db","type":"postgres","target":"db:5432","interval_seconds":60,"config":{"auth_user":"u","auth_password":"db-secret-771"}}`
	if rec := call(s, admin, http.MethodPost, "/api/monitors", body); rec.Code != 201 {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	hook := `{"name":"slack","type":"slack","dest":"https://hooks.slack.com/services/T0/B0/SLACKSECRET","events":["down"]}`
	if rec := call(s, admin, http.MethodPost, "/api/channels", hook); rec.Code != 201 {
		t.Fatalf("channel %d %s", rec.Code, rec.Body.String())
	}

	for name, c := range map[string]*http.Cookie{"viewer": viewer, "operator": operator} {
		if rec := call(s, c, http.MethodGet, "/api/export", ""); rec.Code != http.StatusForbidden {
			t.Fatalf("%s export: want 403, got %d", name, rec.Code)
		}
		if rec := call(s, c, http.MethodPost, "/api/export", `{"passphrase":"long enough passphrase"}`); rec.Code != http.StatusForbidden {
			t.Fatalf("%s encrypted export: want 403, got %d", name, rec.Code)
		}
	}

	rec := call(s, admin, http.MethodGet, "/api/export", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "db-secret-771") {
		t.Fatalf("admin export should be a full backup: %d", rec.Code)
	}
	entries, err := s.store.ListAudit(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Action == "GET /api/export" && e.Email == "admin@example.com" {
			found = true
		}
	}
	if !found {
		t.Fatal("a plain export should be in the audit log")
	}

	// Viewers see where alerts go, not the webhook that sends them.
	rec = call(s, viewer, http.MethodGet, "/api/channels", "")
	if strings.Contains(rec.Body.String(), "SLACKSECRET") || !strings.Contains(rec.Body.String(), "hooks.slack.com") {
		t.Fatalf("viewer channel list: %s", rec.Body.String())
	}
	if rec := call(s, operator, http.MethodGet, "/api/channels", ""); !strings.Contains(rec.Body.String(), "SLACKSECRET") {
		t.Fatal("operators edit channels, so they see the full destination")
	}
}

func TestEncryptedExportRoundTrip(t *testing.T) {
	s := testAPI(t)
	_, admin := signIn(t, s, "admin@example.com", "change-me-too", "198.51.100.10")
	body := `{"name":"db","type":"postgres","target":"db:5432","interval_seconds":60,"config":{"auth_user":"u","auth_password":"db-secret-771"}}`
	if rec := call(s, admin, http.MethodPost, "/api/monitors", body); rec.Code != 201 {
		t.Fatalf("create %d", rec.Code)
	}
	if rec := call(s, admin, http.MethodPost, "/api/export", `{"passphrase":"too short"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("short passphrase: want 400, got %d", rec.Code)
	}
	rec := call(s, admin, http.MethodPost, "/api/export", `{"passphrase":"correct horse battery"}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "db-secret-771") {
		t.Fatalf("encrypted export %d: plaintext must not appear", rec.Code)
	}
	sealed := rec.Body.String()

	imp := func(pass string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]any{"format": "argus", "data": json.RawMessage(sealed), "passphrase": pass})
		return call(s, admin, http.MethodPost, "/api/import", string(b))
	}
	if rec := imp(""); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "passphrase") {
		t.Fatalf("no passphrase: %d %s", rec.Code, rec.Body.String())
	}
	if rec := imp("wrong horse battery"); rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong passphrase: %d", rec.Code)
	}
	rec = imp("correct horse battery")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"imported":1`) {
		t.Fatalf("import %d %s", rec.Code, rec.Body.String())
	}
	list, _ := s.store.ListMonitors(context.Background())
	restored := 0
	for _, m := range list {
		if m.Config.AuthPassword == "db-secret-771" {
			restored++
		}
	}
	if restored != 2 {
		t.Fatalf("the credential should survive the round trip, found it on %d monitors", restored)
	}
}

// TestLoginLimitPerAccount is guessing from many addresses at once, which
// the per-address limit cannot see.
func TestLoginLimitPerAccount(t *testing.T) {
	s := testAPI(t)
	s.cfg.TrustedProxies = 1
	// The owner has signed in from home before.
	if rec, _ := signIn(t, s, "admin@example.com", "change-me-too", "198.51.100.10"); rec.Code != 200 {
		t.Fatalf("owner login %d", rec.Code)
	}
	for i := 0; i < 20; i++ {
		rec, _ := signIn(t, s, "admin@example.com", "guess", fmt.Sprintf("203.0.113.%d", i+1))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: want 401, got %d", i+1, rec.Code)
		}
	}
	// A new address with the right password is still refused: the attacker
	// could have guessed it.
	if rec, _ := signIn(t, s, "admin@example.com", "change-me-too", "203.0.113.200"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("new address after a distributed attack: want 429, got %d", rec.Code)
	}
	// The owner's usual address still works, so the attack is not a lockout.
	if rec, _ := signIn(t, s, "admin@example.com", "change-me-too", "198.51.100.10"); rec.Code != 200 {
		t.Fatalf("owner during the attack: want 200, got %d", rec.Code)
	}
	// An account that does not exist answers the same way as one that does.
	for i := 0; i < 20; i++ {
		signIn(t, s, "nobody@example.com", "guess", fmt.Sprintf("192.0.2.%d", i+1))
	}
	if rec, _ := signIn(t, s, "nobody@example.com", "guess", "192.0.2.200"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("unknown accounts must be limited the same way, got %d", rec.Code)
	}
}

func TestOperatorCannotChangeSettingsOrUsers(t *testing.T) {
	cases := []struct {
		method, path string
		blocked      bool
	}{
		{http.MethodPost, "/api/users", true},
		{http.MethodPost, "/api/monitors", false},
		{http.MethodPut, "/api/channels/1", false},
		{http.MethodGet, "/api/settings", false},
		{http.MethodPut, "/api/settings", true},
		{http.MethodPost, "/api/settings/smtp-test", true},
		{http.MethodGet, "/api/probe-nodes", false},
		{http.MethodPost, "/api/probe-nodes", true},
		{http.MethodDelete, "/api/probe-nodes/3", true},
		{http.MethodPost, "/api/status-pages", false},
	}
	for _, c := range cases {
		if got := operatorBlocked(c.method, c.path); got != c.blocked {
			t.Errorf("%s %s: blocked=%v, want %v", c.method, c.path, got, c.blocked)
		}
	}
}
