package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"argus/internal/secrets"
	"argus/internal/store"
)

func validTransaction() store.MonitorConfig {
	return store.MonitorConfig{
		Steps: []store.TransactionStep{
			{
				Name: "Log in", Method: "post", URL: "https://shop.example.com/login",
				Body:    `{"user":"probe","password":"{{secret.password}}"}`,
				Extract: []store.StepExtract{{Name: "token", Path: "data.token"}},
			},
			{
				Name: "Orders", URL: "https://shop.example.com/orders",
				Headers: map[string]string{"Authorization": "Bearer {{ token }}"},
			},
		},
		Secrets: map[string]string{"password": "hunter2-long-secret"},
	}
}

func TestNormalizeTransactionConfig(t *testing.T) {
	cfg := validTransaction()
	cfg.Headers = map[string]string{"stray": "http field"}
	if msg := normalizeTransactionConfig(&cfg); msg != "" {
		t.Fatalf("valid transaction rejected: %s", msg)
	}
	if cfg.Steps[0].Method != "POST" || cfg.Steps[1].Method != "GET" {
		t.Fatalf("methods not normalised: %+v", cfg.Steps)
	}
	if cfg.Steps[0].Extract[0].From != "json" {
		t.Fatal("extract should default to json")
	}
	if cfg.Headers != nil {
		t.Fatal("fields that do not belong to a transaction should be dropped")
	}

	cases := map[string]func(c *store.MonitorConfig){
		"no steps":             func(c *store.MonitorConfig) { c.Steps = nil },
		"too many steps":       func(c *store.MonitorConfig) { c.Steps = make([]store.TransactionStep, 11) },
		"bad method":           func(c *store.MonitorConfig) { c.Steps[1].Method = "TRACE" },
		"not a url":            func(c *store.MonitorConfig) { c.Steps[1].URL = "shop.example.com/orders" },
		"ftp url":              func(c *store.MonitorConfig) { c.Steps[1].URL = "ftp://shop.example.com" },
		"get with a body":      func(c *store.MonitorConfig) { c.Steps[1].Body = "x" },
		"used before extract":  func(c *store.MonitorConfig) { c.Steps[0].URL = "https://shop.example.com/{{token}}" },
		"never extracted":      func(c *store.MonitorConfig) { c.Steps[1].Headers["X"] = "{{session}}" },
		"unknown secret":       func(c *store.MonitorConfig) { c.Steps[1].Headers["X"] = "{{secret.api_key}}" },
		"extracted twice":      func(c *store.MonitorConfig) { c.Steps[1].Extract = []store.StepExtract{{Name: "token", Path: "t"}} },
		"extract without path": func(c *store.MonitorConfig) { c.Steps[0].Extract[0].Path = "" },
		"extract from body":    func(c *store.MonitorConfig) { c.Steps[0].Extract[0].From = "body" },
		"extract named secret": func(c *store.MonitorConfig) { c.Steps[0].Extract[0].Name = "secret" },
		"bad secret name":      func(c *store.MonitorConfig) { c.Secrets["api-key"] = "value" },
		"empty secret":         func(c *store.MonitorConfig) { c.Secrets["other"] = "" },
		"header injection":     func(c *store.MonitorConfig) { c.Steps[1].Headers["X"] = "a\r\nHost: evil" },
		"bad expect status":    func(c *store.MonitorConfig) { c.Steps[1].ExpectStatus = 42 },
	}
	for name, mutate := range cases {
		c := validTransaction()
		mutate(&c)
		if msg := normalizeTransactionConfig(&c); msg == "" {
			t.Errorf("%s: should be rejected", name)
		}
	}
}

func TestTransactionURLMayUseReferences(t *testing.T) {
	cfg := validTransaction()
	cfg.Secrets["host"] = "shop.example.com"
	cfg.Steps[1].URL = "https://{{secret.host}}/orders/{{token}}"
	if msg := normalizeTransactionConfig(&cfg); msg != "" {
		t.Fatalf("references in a URL should be allowed: %s", msg)
	}
}

func TestTransactionSecretsAreWriteOnly(t *testing.T) {
	cfg := validTransaction()
	redactMonitorConfig(&cfg)
	if v, ok := cfg.Secrets["password"]; !ok || v != "" {
		t.Fatalf("the name should survive and the value should not: %#v", cfg.Secrets)
	}

	existing := validTransaction()
	edited := validTransaction()
	edited.Secrets = map[string]string{"password": "", "added": "new-value"}
	mergeAuthSecrets(&edited, existing)
	if edited.Secrets["password"] != "hunter2-long-secret" || edited.Secrets["added"] != "new-value" {
		t.Fatalf("a blank secret keeps its value, a new one is taken: %#v", edited.Secrets)
	}
	removed := validTransaction()
	removed.Secrets = map[string]string{}
	mergeAuthSecrets(&removed, existing)
	if _, ok := removed.Secrets["password"]; ok {
		t.Fatal("a secret left out of the edit is deleted, not restored")
	}
}

// transactionBody is a create request for a transaction against target.
func transactionBody(name string) string {
	cfg := validTransaction()
	b, _ := json.Marshal(map[string]any{"name": name, "type": "transaction", "interval_seconds": 60, "config": cfg})
	return string(b)
}

func TestTransactionMonitorLifecycle(t *testing.T) {
	box, err := secrets.New(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	store.SetSecretBox(box)
	t.Cleanup(func() { store.SetSecretBox(nil) })

	h := newAPIHarness(t)

	rec := h.do(http.MethodPost, "/api/monitors", transactionBody("checkout"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "hunter2-long-secret") {
		t.Fatal("create echoed a secret")
	}
	var created store.Monitor
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Target != "https://shop.example.com/login" {
		t.Fatalf("target should be the first step, got %q", created.Target)
	}

	// At rest the secret is ciphertext.
	var raw string
	if err := h.s.store.Pool.QueryRow(context.Background(), `SELECT config->'secrets'->>'password' FROM monitors WHERE id=$1`, created.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !secrets.IsEncrypted(raw) {
		t.Fatalf("stored secret is not encrypted: %q", raw)
	}

	// Reading it back gives the name without the value.
	path := fmt.Sprintf("/api/monitors/%d", created.ID)
	rec = h.do(http.MethodGet, path, "")
	if strings.Contains(rec.Body.String(), "hunter2-long-secret") || !strings.Contains(rec.Body.String(), `"password":""`) {
		t.Fatalf("read should list the secret name only: %s", rec.Body.String())
	}

	// Saving the form unchanged keeps the secret.
	var got store.Monitor
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	edit, _ := json.Marshal(map[string]any{"name": "checkout", "type": "transaction", "interval_seconds": 60, "config": got.Config})
	unchanged := string(edit)
	if rec := h.do(http.MethodPut, path, unchanged); rec.Code != 200 {
		t.Fatalf("update %d %s", rec.Code, rec.Body.String())
	}
	full, err := h.s.store.GetMonitor(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if full.Config.Secrets["password"] != "hunter2-long-secret" {
		t.Fatalf("an unchanged secret was lost on save: %#v", full.Config.Secrets)
	}

	// Deleting a secret a step still uses is refused.
	got.Config.Secrets = map[string]string{}
	edit, _ = json.Marshal(map[string]any{"name": "checkout", "type": "transaction", "interval_seconds": 60, "config": got.Config})
	if rec := h.do(http.MethodPut, path, string(edit)); rec.Code != http.StatusBadRequest {
		t.Fatalf("removing a used secret: want 400, got %d", rec.Code)
	}

	if list, err := h.s.store.ListEnabledMonitorsForNode(context.Background(), store.LocalProbeNodeID); err != nil || !containsMonitor(list, created.ID) {
		t.Fatalf("the transaction should be scheduled: %v", err)
	}
	if rec := h.do(http.MethodPost, path+"/clone", "{}"); rec.Code != http.StatusCreated {
		t.Fatalf("clone: %d %s", rec.Code, rec.Body.String())
	}
}

func containsMonitor(list []store.Monitor, id int64) bool {
	for _, m := range list {
		if m.ID == id {
			return true
		}
	}
	return false
}

func TestTransactionTestRun(t *testing.T) {
	// The app under test: a login that checks the secret and hands back a token.
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			var b struct{ Password string }
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b.Password != "hunter2-long-secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"data":{"token":"tok-run-5566"}}`))
		case "/orders":
			if r.Header.Get("Authorization") != "Bearer tok-run-5566" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"orders":[]}`))
		}
	}))
	t.Cleanup(app.Close)
	cfg := store.MonitorConfig{
		Steps: []store.TransactionStep{
			{Name: "Log in", Method: "POST", URL: app.URL + "/login", Body: `{"password":"{{secret.password}}"}`,
				Extract: []store.StepExtract{{Name: "token", Path: "data.token"}}},
			{Name: "Orders", URL: app.URL + "/orders", Headers: map[string]string{"Authorization": "Bearer {{token}}"}},
		},
		Secrets: map[string]string{"password": "hunter2-long-secret"},
	}
	run := func(h *apiHarness, c store.MonitorConfig, extra map[string]any) (*httptest.ResponseRecorder, map[string]any) {
		body := map[string]any{"type": "transaction", "config": c}
		for k, v := range extra {
			body[k] = v
		}
		b, _ := json.Marshal(body)
		rec := h.do(http.MethodPost, "/api/monitors/test-run", string(b))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec, out
	}

	h := newAPIHarness(t)

	rec, out := run(h, cfg, nil)
	if rec.Code != 200 || out["success"] != true {
		t.Fatalf("test run should pass: %d %s", rec.Code, rec.Body.String())
	}
	if steps := out["evidence"].(map[string]any)["steps"].([]any); len(steps) != 2 {
		t.Fatalf("both steps should be reported: %v", steps)
	}
	if strings.Contains(rec.Body.String(), "hunter2-long-secret") || strings.Contains(rec.Body.String(), "tok-run-5566") {
		t.Fatal("a test run must not return secrets or extracted values")
	}

	// Nothing was saved.
	if list, _ := h.s.store.ListMonitors(context.Background()); len(list) != 0 {
		t.Fatalf("a test run must not create a monitor, found %d", len(list))
	}

	// A failing run reports the step, not an HTTP error.
	bad := cfg
	bad.Secrets = map[string]string{"password": "wrong-password-here"}
	if rec, out := run(h, bad, nil); rec.Code != 200 || out["success"] != false || !strings.Contains(out["error"].(string), "step 1 (Log in): status 401") {
		t.Fatalf("failing run: %d %s", rec.Code, rec.Body.String())
	}

	// Validation applies as it would on save.
	invalid := cfg
	invalid.Steps = []store.TransactionStep{{URL: app.URL + "/orders?t={{token}}"}}
	if rec, _ := run(h, invalid, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid config: want 400, got %d", rec.Code)
	}

	// Editing a saved monitor: a blank secret uses the stored value.
	create, _ := json.Marshal(map[string]any{"name": "shop", "type": "transaction", "interval_seconds": 60, "config": cfg})
	crec := h.do(http.MethodPost, "/api/monitors", string(create))
	if crec.Code != 201 {
		t.Fatalf("create %d %s", crec.Code, crec.Body.String())
	}
	var saved store.Monitor
	_ = json.Unmarshal(crec.Body.Bytes(), &saved)
	blank := cfg
	blank.Secrets = map[string]string{"password": ""}
	if rec, out := run(h, blank, map[string]any{"monitor_id": saved.ID}); out["success"] != true {
		t.Fatalf("a blank secret should use the stored one: %d %s", rec.Code, rec.Body.String())
	}
	if _, out := run(h, blank, nil); out["success"] == true {
		t.Fatal("without monitor_id a blank secret has nothing to fall back on")
	}

	// Other types are not test-run.
	if rec := h.do(http.MethodPost, "/api/monitors/test-run", `{"type":"http","target":"https://example.com"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("http test run: want 400, got %d", rec.Code)
	}
}
