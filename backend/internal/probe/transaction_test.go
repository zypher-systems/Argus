package probe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"argus/internal/store"
)

// shopServer is a small app with the shape transactions exist for: log in,
// get a token and a session cookie, then use both.
func shopServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ User, Password string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.User != "probe" || body.Password != "hunter2-long-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"bad credentials"}`)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "sess-abc123", Path: "/"})
		w.Header().Set("X-Request-Id", "req-7781")
		_, _ = io.WriteString(w, `{"data":{"token":"tok-9f8e7d6c5b4a","user":{"id":42}},"items":[{"sku":"A-1"},{"sku":"B-2"}]}`)
	})
	mux.HandleFunc("GET /orders", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-9f8e7d6c5b4a" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if c, err := r.Cookie("session"); err != nil || c.Value != "sess-abc123" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "no session")
			return
		}
		_, _ = io.WriteString(w, `{"orders":[{"id":"ord-1","sku":"`+r.URL.Query().Get("sku")+`"}]}`)
	})
	mux.HandleFunc("GET /broken", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "upstream said tok-9f8e7d6c5b4a is invalid")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func transactionMonitor(steps ...store.TransactionStep) store.Monitor {
	return store.Monitor{
		Type: "transaction",
		Config: store.MonitorConfig{
			TimeoutSeconds: 5,
			Steps:          steps,
			Secrets:        map[string]string{"password": "hunter2-long-secret"},
		},
	}
}

func loginStep(base string) store.TransactionStep {
	return store.TransactionStep{
		Name:   "Log in",
		Method: "POST",
		URL:    base + "/login",
		Body:   `{"user":"probe","password":"{{secret.password}}"}`,
		Extract: []store.StepExtract{
			{Name: "token", From: "json", Path: "data.token"},
			{Name: "sku", From: "json", Path: "items.1.sku"},
			{Name: "request_id", From: "header", Path: "X-Request-Id"},
		},
	}
}

func TestTransactionCarriesTokensAndCookies(t *testing.T) {
	srv := shopServer(t)
	m := transactionMonitor(
		loginStep(srv.URL),
		store.TransactionStep{
			Name:    "List orders",
			URL:     srv.URL + "/orders?sku={{sku}}",
			Headers: map[string]string{"Authorization": "Bearer {{token}}", "X-Trace": "{{request_id}}"},
			Keyword: `"sku":"B-2"`,
		},
	)
	out, err := checkTransaction(context.Background(), m)
	if err != nil {
		t.Fatalf("transaction should pass: %v", err)
	}
	if len(out.Evidence.Steps) != 2 {
		t.Fatalf("want 2 step records, got %+v", out.Evidence.Steps)
	}
	for _, s := range out.Evidence.Steps {
		if s.StatusCode != 200 || s.Error != "" {
			t.Fatalf("step %+v should have passed", s)
		}
	}
	if out.LatencyMs <= 0 {
		t.Fatal("total latency should be recorded")
	}
}

func TestTransactionNamesTheFailingStep(t *testing.T) {
	srv := shopServer(t)
	m := transactionMonitor(
		loginStep(srv.URL),
		store.TransactionStep{Name: "Checkout", URL: srv.URL + "/broken"},
		store.TransactionStep{Name: "Never runs", URL: srv.URL + "/orders"},
	)
	out, err := checkTransaction(context.Background(), m)
	if err == nil {
		t.Fatal("a 500 must fail the transaction")
	}
	if !strings.Contains(err.Error(), `step 2 (Checkout): status 500`) {
		t.Fatalf("error should name the step: %v", err)
	}
	if len(out.Evidence.Steps) != 2 {
		t.Fatalf("steps after the failure must not run: %+v", out.Evidence.Steps)
	}
	if out.Evidence.StatusCode != 500 {
		t.Fatalf("evidence should describe the failing step, got %d", out.Evidence.StatusCode)
	}
}

func TestTransactionRedactsSecretsAndExtractedValues(t *testing.T) {
	srv := shopServer(t)
	m := transactionMonitor(
		loginStep(srv.URL),
		store.TransactionStep{
			Name:    "Checkout",
			URL:     srv.URL + "/broken?t={{token}}&p={{secret.password}}",
			Headers: map[string]string{"X-Token": "{{token}}"},
		},
	)
	out, err := checkTransaction(context.Background(), m)
	if err == nil {
		t.Fatal("expected failure")
	}
	for label, text := range map[string]string{
		"error": err.Error(), "curl": out.Evidence.Curl, "body": out.Evidence.BodySnippet,
	} {
		for _, leaked := range []string{"tok-9f8e7d6c5b4a", "hunter2-long-secret"} {
			if strings.Contains(text, leaked) {
				t.Errorf("%s leaks %q: %s", label, leaked, text)
			}
		}
	}
	if !strings.Contains(out.Evidence.BodySnippet, "[redacted]") {
		t.Fatalf("the token in the response body should be redacted: %s", out.Evidence.BodySnippet)
	}
}

func TestTransactionNamesTheExpectedStatus(t *testing.T) {
	srv := shopServer(t)
	step := loginStep(srv.URL)
	step.ExpectStatus = 201
	_, err := checkTransaction(context.Background(), transactionMonitor(step))
	if err == nil || !strings.Contains(err.Error(), "status 200, expected 201") {
		t.Fatalf("the error should say what was expected, got %v", err)
	}
}

func TestTransactionWrongSecretFailsAtLogin(t *testing.T) {
	srv := shopServer(t)
	m := transactionMonitor(loginStep(srv.URL))
	m.Config.Secrets["password"] = "not-the-password"
	_, err := checkTransaction(context.Background(), m)
	if err == nil || !strings.Contains(err.Error(), "step 1 (Log in): status 401") {
		t.Fatalf("want a login failure, got %v", err)
	}
}

func TestTransactionExtractionFailures(t *testing.T) {
	srv := shopServer(t)
	cases := map[string]store.StepExtract{
		"missing json path":  {Name: "x", From: "json", Path: "data.nope"},
		"array out of range": {Name: "x", From: "json", Path: "items.9.sku"},
		"missing header":     {Name: "x", From: "header", Path: "X-Absent"},
	}
	for name, x := range cases {
		step := loginStep(srv.URL)
		step.Extract = []store.StepExtract{x}
		_, err := checkTransaction(context.Background(), transactionMonitor(step))
		if err == nil || !strings.Contains(err.Error(), "{{x}}") {
			t.Errorf("%s: want an extraction error naming {{x}}, got %v", name, err)
		}
	}
}

func TestTransactionUnknownReferenceIsNotSentLiterally(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	t.Cleanup(srv.Close)
	for _, ref := range []string{"{{never_extracted}}", "{{secret.absent}}"} {
		m := transactionMonitor(store.TransactionStep{URL: srv.URL + "/x?v=" + ref})
		if _, err := checkTransaction(context.Background(), m); err == nil {
			t.Errorf("%s: an unresolved reference must fail", ref)
		}
	}
	if hits != 0 {
		t.Fatalf("no request should be sent with an unresolved reference, got %d", hits)
	}
}

func TestTransactionDefaultContentType(t *testing.T) {
	cases := map[string]string{
		`{"a":1}`:          "application/json",
		"user=a&pass=b":    "application/x-www-form-urlencoded",
		"plain words here": "text/plain; charset=utf-8",
	}
	for body, want := range cases {
		if got := defaultContentType(body); got != want {
			t.Errorf("%q: got %q, want %q", body, got, want)
		}
	}
}

func TestTemplateRefs(t *testing.T) {
	vars, secrets := TemplateRefs(`Bearer {{ token }} {{secret.api_key}} {{id}} {{not valid}}`)
	if strings.Join(vars, ",") != "token,id" || strings.Join(secrets, ",") != "api_key" {
		t.Fatalf("vars %v secrets %v", vars, secrets)
	}
}
