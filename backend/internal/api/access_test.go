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

// operatorHarness is an install with an administrator signed in on
// h and an operator signed in on the harness returned beside it.
func operatorHarness(t *testing.T) (admin, operator *apiHarness) {
	t.Helper()
	h := newAPIHarness(t)
	rec := h.do(http.MethodPost, "/api/users", `{"email":"op@example.com","name":"Op","role":"operator","password":"another-pass-1"}`)
	if rec.Code != http.StatusCreated && rec.Code != 200 {
		t.Fatalf("create operator %d %s", rec.Code, rec.Body.String())
	}
	lr, cookie := signIn(t, h.s, "op@example.com", "another-pass-1", "192.0.2.10")
	if lr.Code != 200 || cookie == nil {
		t.Fatalf("operator sign-in %d %s", lr.Code, lr.Body.String())
	}
	return h, &apiHarness{t: t, s: h.s, cookie: cookie}
}

// Whoever can point the mail server, the identity provider or the base URL
// somewhere else can read the mail password and sign in as an administrator.
// A probe node is handed monitor credentials. None of these is an
// operator's to change.
func TestOperatorCannotChangeSettingsOrProbeNodes(t *testing.T) {
	admin, op := operatorHarness(t)
	rec := admin.do(http.MethodPost, "/api/probe-nodes", `{"name":"Frankfurt","region":"eu"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin creates a node: %d %s", rec.Code, rec.Body.String())
	}
	var node store.ProbeNode
	_ = json.Unmarshal(rec.Body.Bytes(), &node)

	refused := []struct{ name, method, path, body string }{
		{"identity provider", http.MethodPut, "/api/settings", `{"oidc":{"enabled":true,"issuer":"https://idp.elsewhere.test","client_id":"x","client_secret":"y","default_role":"admin"}}`},
		{"mail server", http.MethodPut, "/api/settings", `{"smtp":{"host":"mail.elsewhere.test","port":25,"from":"a@b.test"}}`},
		{"base URL", http.MethodPut, "/api/settings", `{"general":{"base_url":"https://elsewhere.test"}}`},
		{"branding", http.MethodPut, "/api/settings", `{"branding":{"product_name":"Mine"}}`},
		{"retention", http.MethodPut, "/api/settings", `{"retention":{"raw_days":1,"hourly_days":7,"daily_days":30}}`},
		{"reports", http.MethodPut, "/api/settings", `{"reports":{"monthly_enabled":true,"recipients":"me@elsewhere.test"}}`},
		{"test email", http.MethodPost, "/api/settings/smtp-test", `{"to":"me@elsewhere.test"}`},
		{"new probe node", http.MethodPost, "/api/probe-nodes", `{"name":"Mine","region":"x"}`},
		{"delete probe node", http.MethodDelete, fmt.Sprintf("/api/probe-nodes/%d", node.ID), ``},
	}
	for _, c := range refused {
		if rec := op.do(c.method, c.path, c.body); rec.Code != http.StatusForbidden {
			t.Errorf("operator changing %s: got %d %s, want 403", c.name, rec.Code, rec.Body.String())
		}
	}
	settings, err := admin.s.store.LoadSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if settings.OIDC.Issuer != "" || settings.SMTP.Host != "" || settings.General.BaseURL != "" {
		t.Fatalf("an operator's request changed settings: %+v %+v %+v", settings.OIDC, settings.SMTP, settings.General)
	}

	// The work an operator is for still goes through, and settings stay readable.
	if rec := op.do(http.MethodGet, "/api/settings", ""); rec.Code != 200 {
		t.Errorf("operator reading settings: %d", rec.Code)
	}
	if rec := op.do(http.MethodGet, "/api/probe-nodes", ""); rec.Code != 200 {
		t.Errorf("operator listing probe nodes: %d", rec.Code)
	}
	if rec := op.do(http.MethodPost, "/api/monitors", `{"name":"shop","type":"http","target":"http://127.0.0.1:1","interval_seconds":60}`); rec.Code != http.StatusCreated {
		t.Errorf("operator creating a monitor: %d %s", rec.Code, rec.Body.String())
	}
	if rec := op.do(http.MethodPost, "/api/channels", `{"name":"hook","type":"webhook","dest":"https://hooks.example.com/x"}`); rec.Code != http.StatusCreated && rec.Code != 200 {
		t.Errorf("operator creating a channel: %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.do(http.MethodPut, "/api/settings", `{"general":{"base_url":"https://argus.example.com"}}`); rec.Code != 200 {
		t.Errorf("admin changing the base URL: %d %s", rec.Code, rec.Body.String())
	}
	// t.Context is already cancelled when cleanups run.
	t.Cleanup(func() { _ = admin.s.store.SetGeneralSettings(context.Background(), store.GeneralSettings{}) })
}

func TestEmailVerifiedClaim(t *testing.T) {
	cases := map[string]bool{
		`true`:    true,
		`"true"`:  true,
		`"TRUE"`:  true,
		`false`:   false,
		`"false"`: false,
		``:        false, // the provider did not say
		`null`:    false,
		`1`:       false,
	}
	for raw, want := range cases {
		if got := emailVerified(json.RawMessage(raw)); got != want {
			t.Errorf("email_verified %q: got %v, want %v", raw, got, want)
		}
	}
}

// The API never returns a saved credential, so a blank field on an edit
// means "keep it". That must not become a way to have it sent somewhere
// else: to another host, through another proxy, or as another kind of check.
func TestSavedCredentialsStayWithTheirHost(t *testing.T) {
	_, op := operatorHarness(t)
	var seen string
	theirs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen += r.Header.Get("Authorization") + r.URL.String()
	}))
	defer theirs.Close()

	body := func(target, password string) string {
		return fmt.Sprintf(`{"name":"api","type":"http","target":%q,"interval_seconds":60,"enabled":false,
			"config":{"auth":"basic","auth_user":"svc","auth_password":%q}}`, target, password)
	}
	rec := op.do(http.MethodPost, "/api/monitors", body("https://api.internal.test/health", "s3cret-pw"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var m store.Monitor
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	edit := fmt.Sprintf("/api/monitors/%d", m.ID)
	saved := func() string {
		got, err := op.s.store.GetMonitor(t.Context(), m.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Target + " " + got.Config.AuthPassword
	}

	if rec := op.do(http.MethodPut, edit, body(theirs.URL+"/collect", "")); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Enter them again") {
		t.Fatalf("another host with the password left blank: %d %s", rec.Code, rec.Body.String())
	}
	if got := saved(); got != "https://api.internal.test/health s3cret-pw" {
		t.Fatalf("the refused edit changed the monitor: %q", got)
	}
	if rec := op.do(http.MethodPut, edit, strings.Replace(body("https://api.internal.test/health", ""), `"auth":"basic"`, `"auth":"basic","proxy_url":"`+theirs.URL+`"`, 1)); rec.Code != http.StatusBadRequest {
		t.Fatalf("a new proxy with the password left blank: %d %s", rec.Code, rec.Body.String())
	}
	if rec := op.do(http.MethodPut, edit, body("https://api.internal.test/ready?deep=1", "")); rec.Code != 200 {
		t.Fatalf("another path on the same host keeps the password: %d %s", rec.Code, rec.Body.String())
	}
	if got := saved(); got != "https://api.internal.test/ready?deep=1 s3cret-pw" {
		t.Fatalf("after a same-host edit: %q", got)
	}
	if rec := op.do(http.MethodPut, edit, body("https://other.internal.test/health", "typed-again")); rec.Code != 200 {
		t.Fatalf("another host with the password entered: %d %s", rec.Code, rec.Body.String())
	}

	// A transaction's saved secrets, through an edit and through a test run.
	tx := func(url, secret string) string {
		return fmt.Sprintf(`{"name":"flow","type":"transaction","interval_seconds":60,"enabled":false,"config":{
			"steps":[{"name":"login","method":"GET","url":%q,"headers":{"X-Key":"{{secret.key}}"}}],
			"secrets":{"key":%q}}}`, url, secret)
	}
	rec = op.do(http.MethodPost, "/api/monitors", tx("https://app.internal.test/login", "tx-s3cret"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create transaction %d %s", rec.Code, rec.Body.String())
	}
	var flow store.Monitor
	_ = json.Unmarshal(rec.Body.Bytes(), &flow)
	if rec := op.do(http.MethodPut, fmt.Sprintf("/api/monitors/%d", flow.ID), tx(theirs.URL+"/collect", "")); rec.Code != http.StatusBadRequest {
		t.Fatalf("a transaction step to another host with the secret left blank: %d %s", rec.Code, rec.Body.String())
	}
	run := strings.Replace(tx(theirs.URL+"/collect", ""), `{"name":"flow"`, fmt.Sprintf(`{"monitor_id":%d,"name":"flow"`, flow.ID), 1)
	if rec := op.do(http.MethodPost, "/api/monitors/test-run", run); rec.Code != http.StatusBadRequest {
		t.Fatalf("a test run to another host with the secret left blank: %d %s", rec.Code, rec.Body.String())
	}
	if seen != "" {
		t.Fatalf("a saved credential reached another host: %q", seen)
	}
}

func TestOrigin(t *testing.T) {
	same := [][2]string{
		{"https://api.example.com/health", "HTTPS://API.example.com/ready?x=1"},
		{"db.internal:5432/app", "db.internal:5432/reports"},
		{"mail.example.com:587", " mail.example.com:587 "},
	}
	for _, c := range same {
		if origin(c[0]) != origin(c[1]) {
			t.Errorf("%q and %q are the same place: %q vs %q", c[0], c[1], origin(c[0]), origin(c[1]))
		}
	}
	different := [][2]string{
		{"https://api.example.com/health", "https://api.example.com.evil.test/health"},
		{"https://api.example.com/health", "http://api.example.com/health"},
		{"https://api.example.com/health", "https://api.example.com:8443/health"},
		{"https://api.example.com/health", "https://evil.test/api.example.com"},
		{"https://api.example.com/health", "https://api.example.com@evil.test/health"},
		{"db.internal:5432/app", "db.elsewhere:5432/app"},
	}
	for _, c := range different {
		if origin(c[0]) == origin(c[1]) {
			t.Errorf("%q and %q are different places, both %q", c[0], c[1], origin(c[0]))
		}
	}
}

// An export can be edited by hand. What it carries passes the same checks
// as a monitor typed into the form.
func TestImportAppliesTheSameChecksAsASave(t *testing.T) {
	_, op := operatorHarness(t)
	export := `{"format":"argus","data":{"monitors":[
		{"name":"flush","type":"redis","target":"cache.internal:6379","interval_seconds":60,"retries":1,"enabled":true,"config":{"query":"FLUSHALL"}},
		{"name":"tight","type":"http","target":"http://127.0.0.1:1","interval_seconds":1,"retries":1,"enabled":true},
		{"name":"unset","type":"http","target":"http://127.0.0.1:1","interval_seconds":0,"retries":0,"enabled":true},
		{"name":"elsewhere","type":"http","target":"http://127.0.0.1:1","interval_seconds":60,"retries":1,"enabled":true,"probe_node_ids":[4242],"confirm_locations":3},
		{"name":"fine","type":"redis","target":"cache.internal:6379","interval_seconds":60,"retries":1,"enabled":true,"config":{"query":"GET health"}}
	]}}`
	rec := op.do(http.MethodPost, "/api/import", export)
	if rec.Code != 200 {
		t.Fatalf("import %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Imported int `json:"imported"`
		Skipped  []struct{ Name, Reason string }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	skipped := map[string]string{}
	for _, s := range out.Skipped {
		skipped[s.Name] = s.Reason
	}
	if skipped["flush"] == "" || skipped["tight"] == "" || out.Imported != 3 {
		t.Fatalf("want flush and tight refused and three imported: %s", rec.Body.String())
	}
	list, err := op.s.store.ListEnabledMonitors(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range list {
		if m.IntervalSeconds < 10 || m.Retries < 1 {
			t.Errorf("%s was saved with interval %d and retries %d", m.Name, m.IntervalSeconds, m.Retries)
		}
		if len(m.ProbeNodeIDs) != 0 || m.ConfirmLocations != 1 {
			t.Errorf("%s kept another install's probe nodes: %v confirm %d", m.Name, m.ProbeNodeIDs, m.ConfirmLocations)
		}
	}
}

// The limiter remembers its keys, so a key must never be something the
// caller chose. A push to a token nobody issued is counted against the
// caller's address, whatever the token says and however long it is.
func TestPushLimiterKeysAreNotTheCallersToChoose(t *testing.T) {
	s := testAPI(t)
	push := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/push/"+token, nil)
		req.RemoteAddr = "172.18.0.5:40000"
		req.Header.Set("X-Forwarded-For", "192.0.2.77")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	before := len(s.statusLimit.attempts)
	last := 0
	for i := range 40 {
		last = push(fmt.Sprintf("%04d%s", i, strings.Repeat("a", 4000)))
	}
	if grew := len(s.statusLimit.attempts) - before; grew > 1 {
		t.Fatalf("40 made-up tokens from one address left %d limiter keys, want 1", grew)
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("guessing tokens from one address should be limited, got %d", last)
	}
	for key := range s.statusLimit.attempts {
		if len(key) > 200 {
			t.Fatalf("a limiter key holds caller-chosen text (%d bytes)", len(key))
		}
	}
}

// The app asks /auth/me first to decide whether someone is signed in. It
// answers as every other request would for that session, so a disabled
// account is told so there and is not shown an app that then refuses everything.
func TestAuthMeAgreesWithTheRestOfTheAPI(t *testing.T) {
	h := newAPIHarness(t)
	member := func(email string) (*apiHarness, int64) {
		rec := h.do(http.MethodPost, "/api/users", `{"email":"`+email+`","name":"M","role":"operator","password":"another-pass-1"}`)
		if rec.Code != http.StatusCreated && rec.Code != 200 {
			t.Fatalf("create %s: %d %s", email, rec.Code, rec.Body.String())
		}
		var u store.User
		_ = json.Unmarshal(rec.Body.Bytes(), &u)
		lr, cookie := signIn(t, h.s, email, "another-pass-1", "192.0.2.20")
		if lr.Code != 200 || cookie == nil {
			t.Fatalf("sign in %s: %d", email, lr.Code)
		}
		return &apiHarness{t: t, s: h.s, cookie: cookie}, u.ID
	}
	kept, _ := member("kept@example.com")
	off, offID := member("off@example.com")
	if rec := kept.do(http.MethodGet, "/api/auth/me", ""); rec.Code != 200 {
		t.Fatalf("a signed-in member: %d %s", rec.Code, rec.Body.String())
	}

	if rec := h.do(http.MethodPut, fmt.Sprintf("/api/users/%d", offID), `{"name":"M","role":"operator","disabled":true}`); rec.Code != 200 {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body.String())
	}
	if rec := off.do(http.MethodGet, "/api/auth/me", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a disabled account is not signed in: %d %s", rec.Code, rec.Body.String())
	}
	if rec := kept.do(http.MethodGet, "/api/auth/me", ""); rec.Code != 200 {
		t.Fatalf("another account stays signed in: %d %s", rec.Code, rec.Body.String())
	}
}

// 401 means "not signed in" and sends the app to the sign-in page. A wrong
// current password is not that.
func TestWrongCurrentPasswordIsNotASignOut(t *testing.T) {
	h := newAPIHarness(t)
	rec := h.do(http.MethodPost, "/api/auth/password", `{"current":"not-it","password":"a-new-passphrase"}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "current password") {
		t.Fatalf("wrong current password: %d %s", rec.Code, rec.Body.String())
	}
	if rec := h.do(http.MethodGet, "/api/monitors", ""); rec.Code != 200 {
		t.Fatalf("the session should be untouched: %d", rec.Code)
	}
}

// A status page's subscribers belong to the page's organization. Deleting
// one checks that, as listing and adding do; without it any signed-in
// account on the hosted build could delete any page's subscribers by id.
// And a viewer, who cannot read a channel's address, cannot read a webhook
// subscriber's either.
func TestSubscribersAreScopedAndMaskedLikeChannels(t *testing.T) {
	admin, _ := operatorHarness(t)
	ctx := context.Background()
	rec := admin.do(http.MethodPost, "/api/status-pages", `{"name":"Acme","slug":"acme","published":true,"monitor_ids":[]}`)
	if rec.Code != http.StatusCreated && rec.Code != 200 {
		t.Fatalf("create page %d %s", rec.Code, rec.Body.String())
	}
	var page store.StatusPage
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	sub, err := admin.s.store.CreateSubscriber(ctx, page.ID, "webhook", "https://hooks.example.com/services/T00/SECRET", true)
	if err != nil {
		t.Fatal(err)
	}
	list := fmt.Sprintf("/api/status-pages/%d/subscribers", page.ID)

	rec = admin.do(http.MethodPost, "/api/users", `{"email":"view@example.com","name":"V","role":"viewer","password":"another-pass-1"}`)
	if rec.Code != http.StatusCreated && rec.Code != 200 {
		t.Fatalf("create viewer %d %s", rec.Code, rec.Body.String())
	}
	lr, cookie := signIn(t, admin.s, "view@example.com", "another-pass-1", "192.0.2.30")
	if lr.Code != 200 || cookie == nil {
		t.Fatalf("viewer sign-in %d", lr.Code)
	}
	viewer := &apiHarness{t: t, s: admin.s, cookie: cookie}
	if rec := viewer.do(http.MethodGet, list, ""); rec.Code != 200 || strings.Contains(rec.Body.String(), "SECRET") {
		t.Fatalf("a viewer read a webhook subscriber's address: %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.do(http.MethodGet, list, ""); !strings.Contains(rec.Body.String(), "SECRET") {
		t.Fatalf("an admin should see it: %s", rec.Body.String())
	}

	// Another organization's administrator, on the hosted build.
	admin.s.cfg.Hosted = true
	admin.s.store.SetHosted(true)
	t.Cleanup(func() { admin.s.store.SetHosted(false) })
	other, err := admin.s.store.CreateOrg(ctx, "Other", "other", store.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.s.store.CreateUser(store.WithOrg(ctx, other.ID), "them@example.com", "T", store.RoleAdmin, "another-pass-1"); err != nil {
		t.Fatal(err)
	}
	lr, cookie = signIn(t, admin.s, "them@example.com", "another-pass-1", "192.0.2.31")
	if lr.Code != 200 || cookie == nil {
		t.Fatalf("other org sign-in %d %s", lr.Code, lr.Body.String())
	}
	them := &apiHarness{t: t, s: admin.s, cookie: cookie}
	if rec := them.do(http.MethodDelete, fmt.Sprintf("%s/%d", list, sub.ID), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("another organization deleted this page's subscriber: %d %s", rec.Code, rec.Body.String())
	}
	var left int
	if err := admin.s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM status_subscribers WHERE id=$1`, sub.ID).Scan(&left); err != nil || left != 1 {
		t.Fatalf("the subscriber should still be there: %d %v", left, err)
	}
}

// The plan is stored on the organization. Its administrator can rename the
// organization and cannot change the plan. The plan does not lock features.
func TestHostedOrgAdminCannotSetThePlan(t *testing.T) {
	h := newAPIHarness(t)
	h.s.cfg.Hosted = true
	h.s.store.SetHosted(true)
	t.Cleanup(func() { h.s.store.SetHosted(false) })

	rec := h.do(http.MethodPut, "/api/org", `{"name":"Renamed","plan":"pro"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"Renamed"`) {
		t.Fatalf("renaming the organization: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"plan":"community"`) {
		t.Fatalf("an administrator changed the organization's own plan: %s", rec.Body.String())
	}
	if rec := h.do(http.MethodGet, "/api/sla", ""); rec.Code != http.StatusOK {
		t.Fatalf("SLA is available without a plan upgrade: %d %s", rec.Code, rec.Body.String())
	}
}
