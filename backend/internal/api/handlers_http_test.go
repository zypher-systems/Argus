package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"argus/internal/config"
	"argus/internal/store"
)

func testAPI(t *testing.T) *Server {
	t.Helper()
	if os.Getenv("ARGUS_TEST_DB_URL") == "" {
		t.Skip("ARGUS_TEST_DB_URL unset")
	}
	st := func() *store.Store {
		// reuse store tests' helper by opening the same way
		dsn := os.Getenv("ARGUS_TEST_DB_URL")
		ctx := context.Background()
		s, err := store.New(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		if _, err := s.Pool.Exec(ctx, `TRUNCATE monitors, probe_heartbeat, alerts, notification_channels,
			users, sessions, maintenance_windows, api_tokens, incidents, incident_updates,
			status_subscribers, status_pages, probe_nodes, probe_node_state, audit_log, alert_outbox,
			realms, organizations RESTART IDENTITY CASCADE`); err != nil {
			t.Fatal(err)
		}
		// Settings outlive the truncated tables. A test that failed partway may have left them set.
		if _, err := s.Pool.Exec(ctx, `DELETE FROM settings WHERE key IN ('general','smtp','oidc','branding','reports')`); err != nil {
			t.Fatal(err)
		}
		if err := s.EnsureAdmin(ctx, "admin@example.com", "change-me-too"); err != nil {
			t.Fatal(err)
		}
		return s
	}()
	return New(&config.Config{ListenAddr: ":0", AdminEmail: "admin@example.com", AdminPassword: "change-me-too"}, st)
}

func TestHealthAndMetrics(t *testing.T) {
	s := testAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("health %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("health body %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"probe"`) {
		t.Fatalf("health missing probe: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("metrics %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "argus_monitors") || !strings.Contains(body, "argus_probe_up") {
		t.Fatalf("metrics body %s", body)
	}
}

func TestLoginAndListMonitors(t *testing.T) {
	s := testAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"change-me-too"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()
	if len(cookie) == 0 {
		t.Fatal("expected session cookie")
	}
	req = httptest.NewRequest(http.MethodGet, "/api/monitors", nil)
	req.AddCookie(cookie[0])
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("monitors %d %s", rec.Code, rec.Body.String())
	}
}

func TestRealmsCRUD(t *testing.T) {
	s := testAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"change-me-too"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]

	req = httptest.NewRequest(http.MethodGet, "/api/realms", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "[") {
		t.Fatalf("list realms %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/realms", strings.NewReader(`{"name":"Websites"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"Websites"`) {
		t.Fatalf("create realm %d %s", rec.Code, rec.Body.String())
	}
	idStart := strings.Index(rec.Body.String(), `"id":`)
	if idStart < 0 {
		t.Fatalf("create realm missing id %s", rec.Body.String())
	}
	realmID := strings.TrimLeft(rec.Body.String()[idStart+5:], " ")
	realmID = strings.SplitN(realmID, ",", 2)[0]
	realmID = strings.TrimSpace(realmID)

	req = httptest.NewRequest(http.MethodPost, "/api/realms", strings.NewReader(`{"name":"websites"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("duplicate realm %d %s", rec.Code, rec.Body.String())
	}

	body := `{"name":"site","type":"icmp","target":"127.0.0.1","interval_seconds":60,"retries":3,"realm_ids":[` + realmID + `]}`
	req = httptest.NewRequest(http.MethodPost, "/api/monitors", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"realm_ids":[`+realmID+`]`) {
		t.Fatalf("create monitor with realm %d %s", rec.Code, rec.Body.String())
	}
}

func TestUsersSLAAuditAndProbeNodesAreAvailable(t *testing.T) {
	s := testAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"change-me-too"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}

	if rec := get("/api/users"); rec.Code != 200 {
		t.Fatalf("users list %d %s", rec.Code, rec.Body.String())
	}
	if rec := get("/api/auth/me"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"role":"admin"`) {
		t.Fatalf("me %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/users", `{"email":"ops@example.com","role":"operator","password":"abcdefgh"}`); rec.Code != 201 {
		t.Fatalf("extra user %d %s", rec.Code, rec.Body.String())
	}
	if rec := get("/api/sla"); rec.Code != 200 {
		t.Fatalf("sla %d %s", rec.Code, rec.Body.String())
	}
	if rec := get("/api/audit"); rec.Code != 200 {
		t.Fatalf("audit %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/probe-nodes", `{"name":"us-east","region":"us-east"}`); rec.Code != 201 {
		t.Fatalf("probe nodes %d %s", rec.Code, rec.Body.String())
	}
	if rec := get("/api/instance"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"instance_id"`) {
		t.Fatalf("instance %d %s", rec.Code, rec.Body.String())
	}
	if rec := get("/api/auth/oidc"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("oidc status %d %s", rec.Code, rec.Body.String())
	}
}

func TestOperatorVerbs(t *testing.T) {
	s := testAPI(t)
	ctx := context.Background()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"change-me-too"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}

	rec = post("/api/monitors", `{"name":"site","type":"http","target":"https://example.com","interval_seconds":60,"retries":1}`)
	if rec.Code != 201 {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	idStart := strings.Index(rec.Body.String(), `"id":`)
	id := strings.SplitN(strings.TrimLeft(rec.Body.String()[idStart+5:], " "), ",", 2)[0]
	id = strings.TrimSpace(id)

	if rec := post("/api/monitors/"+id+"/mute", `{"minutes":60}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"muted_until"`) {
		t.Fatalf("mute %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/monitors/"+id+"/mute", `{"minutes":0}`); rec.Code != 200 {
		t.Fatalf("unmute %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/monitors/"+id+"/ack", `{"minutes":0}`); rec.Code != 400 {
		t.Fatalf("ack while up %d %s", rec.Code, rec.Body.String())
	}

	rec = post("/api/monitors/"+id+"/clone", "")
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"site copy"`) {
		t.Fatalf("clone %d %s", rec.Code, rec.Body.String())
	}

	rec = post("/api/monitors", `{"name":"folder","type":"group","target":"folder","interval_seconds":60,"retries":1}`)
	if rec.Code != 201 {
		t.Fatalf("group %d %s", rec.Code, rec.Body.String())
	}
	gidStart := strings.Index(rec.Body.String(), `"id":`)
	gid := strings.SplitN(strings.TrimLeft(rec.Body.String()[gidStart+5:], " "), ",", 2)[0]
	gid = strings.TrimSpace(gid)
	if rec := post("/api/monitors/"+gid+"/check", ""); rec.Code != 400 {
		t.Fatalf("check group %d %s", rec.Code, rec.Body.String())
	}

	if rec := post("/api/monitors/"+id+"/check", ""); rec.Code != 503 {
		t.Fatalf("check without probe %d %s", rec.Code, rec.Body.String())
	}

	if err := s.store.TouchProbeHeartbeat(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/monitors/"+id+"/check", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		done <- rec
	}()
	// Play the probe: create and clone queued first checks of their own, so
	// complete every request until the handler answers, like the engine does.
	var got *httptest.ResponseRecorder
	claimedOurs := false
	lat := 9.5
	for deadline := time.Now().Add(10 * time.Second); got == nil; {
		select {
		case got = <-done:
			continue
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("check-now handler never answered")
		}
		req, err := s.store.ClaimPendingCheckNow(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if req == nil {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if fmt.Sprint(req.MonitorID) == id {
			claimedOurs = true
		}
		if err := s.store.CompleteCheckNow(ctx, req.ID, true, &lat, "", &store.CheckEvidence{Kind: "http", Success: true}); err != nil {
			t.Fatal(err)
		}
	}
	if !claimedOurs {
		t.Fatal("probe never saw a check-now request for the monitor")
	}
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"success":true`) {
		t.Fatalf("check now %d %s", got.Code, got.Body.String())
	}

	if rec := get("/api/monitors/" + id); rec.Code != 200 {
		t.Fatalf("get %d %s", rec.Code, rec.Body.String())
	}
}

func TestBulkMonitors(t *testing.T) {
	s := testAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"change-me-too"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}
	idOf := func(body string) string {
		start := strings.Index(body, `"id":`)
		if start < 0 {
			return ""
		}
		return strings.TrimSpace(strings.SplitN(strings.TrimLeft(body[start+5:], " "), ",", 2)[0])
	}

	a := post("/api/monitors", `{"name":"bulk-a","type":"icmp","target":"1.1.1.1","interval_seconds":60,"retries":3}`)
	b := post("/api/monitors", `{"name":"bulk-b","type":"icmp","target":"8.8.8.8","interval_seconds":60,"retries":3}`)
	if a.Code != 201 || b.Code != 201 {
		t.Fatalf("create %d %s / %d %s", a.Code, a.Body.String(), b.Code, b.Body.String())
	}
	idA, idB := idOf(a.Body.String()), idOf(b.Body.String())
	ids := `{"ids":[` + idA + `,` + idB + `]`

	if rec := post("/api/monitors/bulk", ids+`,"action":"pause"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"updated":2`) {
		t.Fatalf("pause %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/monitors/bulk", ids+`,"action":"add_tag","tag":"edge"}`); rec.Code != 200 {
		t.Fatalf("tag %d %s", rec.Code, rec.Body.String())
	}
	realm := post("/api/realms", `{"name":"Bulk"}`)
	if realm.Code != 201 {
		t.Fatalf("realm %d %s", realm.Code, realm.Body.String())
	}
	rid := idOf(realm.Body.String())
	if rec := post("/api/monitors/bulk", ids+`,"action":"add_realm","realm_id":`+rid+`}`); rec.Code != 200 {
		t.Fatalf("realm assign %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/monitors/bulk", ids+`,"action":"resume"}`); rec.Code != 200 {
		t.Fatalf("resume %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/monitors/bulk", `{"ids":[],"action":"pause"}`); rec.Code != 400 {
		t.Fatalf("empty ids %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/monitors/bulk", ids+`,"action":"nope"}`); rec.Code != 400 {
		t.Fatalf("bad action %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/monitors/bulk", ids+`,"action":"delete"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"updated":2`) {
		t.Fatalf("delete %d %s", rec.Code, rec.Body.String())
	}
}

func TestRegisterForbiddenWhenNotHosted(t *testing.T) {
	s := testAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"email":"a@example.com","password":"abcdefgh","org_name":"Acme"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("register should be 403 when not hosted %d %s", rec.Code, rec.Body.String())
	}
}

func TestHostedRegisterAndOrgIsolation(t *testing.T) {
	s := testAPI(t)
	s.cfg.Hosted = true
	if err := s.store.SetRegistration(context.Background(), true); err != nil {
		t.Fatal(err)
	}

	register := func(email, org string) *http.Cookie {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"email":"`+email+`","password":"abcdefgh","org_name":"`+org+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 201 {
			t.Fatalf("register %s %d %s", email, rec.Code, rec.Body.String())
		}
		cookies := rec.Result().Cookies()
		if len(cookies) == 0 {
			t.Fatal("expected session cookie")
		}
		return cookies[0]
	}

	cookieA := register("alpha@example.com", "Alpha")
	req := httptest.NewRequest(http.MethodPost, "/api/monitors", strings.NewReader(`{"name":"alpha-icmp","type":"icmp","target":"127.0.0.1","interval_seconds":60,"retries":3}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookieA)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("create monitor %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	idx := strings.Index(body, `"id":`)
	if idx < 0 {
		t.Fatalf("monitor id missing %s", body)
	}
	id := strings.TrimLeft(body[idx+5:], " ")
	id = strings.TrimRight(strings.Split(id, ",")[0], "}")
	id = strings.TrimSpace(id)

	cookieB := register("beta@example.com", "Beta")
	req = httptest.NewRequest(http.MethodGet, "/api/monitors/"+id, nil)
	req.AddCookie(cookieB)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("cross-org get should 404, got %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/monitors", nil)
	req.AddCookie(cookieB)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("list %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "alpha-icmp") {
		t.Fatalf("org B listed org A monitor: %s", rec.Body.String())
	}
}

// TestAnnouncedMaintenanceIsVisibleImmediately guards the status page cache:
// an operator who announces a window is telling customers about it now, so a
// page served a moment earlier must not keep being served.
func TestAnnouncedMaintenanceIsVisibleImmediately(t *testing.T) {
	s := testAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"change-me-too"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}
	public := func() string {
		req := httptest.NewRequest(http.MethodGet, "/api/status/ops", nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("public status %d %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	if rec := post("/api/status-pages", `{"name":"Ops","slug":"ops","published":true}`); rec.Code != 201 {
		t.Fatalf("status page %d %s", rec.Code, rec.Body.String())
	}
	// Warm the cache the way a visitor would.
	if body := public(); strings.Contains(body, "Storage migration") {
		t.Fatal("window announced before it was created")
	}

	now := time.Now().UTC()
	window := fmt.Sprintf(`{"starts_at":%q,"ends_at":%q,"note":"Storage migration","public":true}`,
		now.Add(-time.Minute).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339))
	rec = post("/api/maintenance-windows", window)
	if rec.Code != 201 {
		t.Fatalf("create window %d %s", rec.Code, rec.Body.String())
	}
	id := strings.TrimSpace(strings.SplitN(strings.TrimLeft(rec.Body.String()[strings.Index(rec.Body.String(), `"id":`)+5:], " "), ",", 2)[0])

	if body := public(); !strings.Contains(body, "Storage migration") {
		t.Fatalf("announced window not on the status page: %s", body)
	}

	// Unannouncing it has to take effect just as promptly.
	req = httptest.NewRequest(http.MethodDelete, "/api/maintenance-windows/"+id, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("delete window %d %s", rec.Code, rec.Body.String())
	}
	if body := public(); strings.Contains(body, "Storage migration") {
		t.Fatalf("deleted window still on the status page: %s", body)
	}

	// A window the operator kept private must never reach the page.
	private := fmt.Sprintf(`{"starts_at":%q,"ends_at":%q,"note":"Internal failover drill","public":false}`,
		now.Add(-time.Minute).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339))
	if rec := post("/api/maintenance-windows", private); rec.Code != 201 {
		t.Fatalf("create private window %d %s", rec.Code, rec.Body.String())
	}
	if body := public(); strings.Contains(body, "Internal failover drill") {
		t.Fatalf("private window leaked: %s", body)
	}
}

// TestLoginLimitIgnoresForgedForwardedFor is the brute-force case: a caller
// who writes a fresh X-Forwarded-For on every attempt must still be counted as
// one caller, because nginx appends the address it actually saw.
func TestLoginLimitIgnoresForgedForwardedFor(t *testing.T) {
	s := testAPI(t)
	s.cfg.TrustedProxies = 1
	h := s.Handler()
	limited := false
	for i := 0; i < 8; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"wrong-password"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "172.18.0.5:40000"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d, 203.0.113.7", i+1))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			if i < 5 {
				t.Fatalf("limited after only %d attempts", i)
			}
			break
		}
	}
	if !limited {
		t.Fatal("rotating a forged X-Forwarded-For bypassed the login limit")
	}
}
