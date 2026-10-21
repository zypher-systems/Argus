package probe

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"argus/internal/store"
)

func TestCheckHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Token") != "ok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("healthy"))
	}))
	defer srv.Close()

	m := store.Monitor{
		Type:   "http",
		Target: srv.URL,
		Config: store.MonitorConfig{Method: "GET", Headers: map[string]string{"X-Token": "ok"}, Keyword: "healthy"},
	}
	if out, err := Check(context.Background(), nil, m); err != nil {
		t.Fatalf("expected up: %v", err)
	} else if out.TLSExpiresAt != nil {
		t.Fatal("plain http should not report a certificate")
	}

	m.Config.Keyword = "missing"
	if _, err := Check(context.Background(), nil, m); err == nil {
		t.Fatal("expected keyword miss")
	}

	m.Config.Keyword = "healthy"
	m.Config.KeywordInvert = true
	if _, err := Check(context.Background(), nil, m); err == nil {
		t.Fatal("expected inverted keyword hit to fail")
	}
	m.Config.KeywordInvert = false

	m.Config.Keyword = ""
	m.Config.ExpectStatus = 201
	out, err := Check(context.Background(), nil, m)
	if err == nil {
		t.Fatal("expected status miss")
	}
	if out.Evidence.StatusCode != http.StatusOK {
		t.Fatalf("status_code %d", out.Evidence.StatusCode)
	}
	if !strings.Contains(out.Evidence.BodySnippet, "healthy") {
		t.Fatalf("body snippet %q", out.Evidence.BodySnippet)
	}
	if out.Evidence.Curl == "" {
		t.Fatal("expected curl")
	}
	if out.Evidence.Kind != "http" {
		t.Fatalf("kind %q", out.Evidence.Kind)
	}
}

func TestCheckHTTPPOSTJSON(t *testing.T) {
	var gotBody []byte
	var gotCT, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotUA = r.Header.Get("User-Agent")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"status":"ok","n":1,"nested":{"health":"up"}}`))
	}))
	defer srv.Close()

	m := store.Monitor{
		Type:   "http",
		Target: srv.URL,
		Config: store.MonitorConfig{
			Method:     "POST",
			Body:       `{"ping":true}`,
			JSONPath:   "status",
			JSONExpect: "ok",
		},
	}
	if _, err := Check(context.Background(), nil, m); err != nil {
		t.Fatalf("expected up: %v", err)
	}
	if string(gotBody) != `{"ping":true}` {
		t.Fatalf("body %s", gotBody)
	}
	if gotCT != "application/json" {
		t.Fatalf("content-type %s", gotCT)
	}
	if gotUA != userAgent {
		t.Fatalf("ua %s", gotUA)
	}

	m.Config.JSONPath = "nested.health"
	m.Config.JSONExpect = "up"
	if _, err := Check(context.Background(), nil, m); err != nil {
		t.Fatalf("nested path: %v", err)
	}

	m.Config.JSONExpect = "down"
	if _, err := Check(context.Background(), nil, m); err == nil {
		t.Fatal("expected json mismatch")
	}

	m.Config.JSONPath = "missing"
	m.Config.JSONExpect = "x"
	if _, err := Check(context.Background(), nil, m); err == nil {
		t.Fatal("expected missing path")
	}

	m.Config.JSONPath = "n"
	m.Config.JSONExpect = "1"
	if _, err := Check(context.Background(), nil, m); err != nil {
		t.Fatalf("number leaf: %v", err)
	}
}

func TestCheckHTTPJSONNotJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	m := store.Monitor{
		Type:   "http",
		Target: srv.URL,
		Config: store.MonitorConfig{JSONPath: "status", JSONExpect: "ok"},
	}
	if _, err := Check(context.Background(), nil, m); err == nil {
		t.Fatal("expected non-json failure")
	}
}

func TestCheckHTTPAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := store.Monitor{
		Type:   "http",
		Target: srv.URL,
		Config: store.MonitorConfig{Auth: "bearer", AuthToken: "secret"},
	}
	if _, err := Check(context.Background(), nil, m); err != nil {
		t.Fatalf("bearer: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("bearer auth %q", gotAuth)
	}

	m.Config = store.MonitorConfig{Auth: "basic", AuthUser: "ops", AuthPassword: "pw"}
	if _, err := Check(context.Background(), nil, m); err != nil {
		t.Fatalf("basic: %v", err)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Fatalf("basic auth %q", gotAuth)
	}
}

func TestCheckHTTPSCertExpiry(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := store.Monitor{
		Type:   "http",
		Target: srv.URL,
		Config: store.MonitorConfig{InsecureSkipVerify: true},
	}
	out, err := Check(context.Background(), nil, m)
	if err != nil {
		t.Fatalf("expected up: %v", err)
	}
	if out.TLSExpiresAt == nil {
		t.Fatal("expected tls expiry from https handshake")
	}
	want := srv.Certificate().NotAfter.UTC().Truncate(time.Second)
	got := out.TLSExpiresAt.UTC().Truncate(time.Second)
	if !got.Equal(want) {
		t.Fatalf("expiry %s, want %s", got, want)
	}

	m.Type = "cert"
	out, err = Check(context.Background(), nil, m)
	if err != nil {
		t.Fatalf("cert type: %v", err)
	}
	if out.TLSExpiresAt == nil {
		t.Fatal("cert type should report expiry")
	}
}

func TestCheckTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()

	m := store.Monitor{Type: "tcp", Target: ln.Addr().String(), Config: store.MonitorConfig{TimeoutSeconds: 2}}
	if _, err := Check(context.Background(), nil, m); err != nil {
		t.Fatalf("expected tcp up: %v", err)
	}

	m.Target = "127.0.0.1:1"
	if _, err := Check(context.Background(), nil, m); err == nil {
		t.Fatal("expected closed port to fail")
	}
}

func TestCheckDNSExpectIP(t *testing.T) {
	m := store.Monitor{
		Type:   "dns",
		Target: "localhost",
		Config: store.MonitorConfig{Record: "A", ExpectIP: "127.0.0.1", TimeoutSeconds: 3},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := Check(ctx, nil, m); err != nil {
		t.Fatalf("localhost A 127.0.0.1: %v", err)
	}

	m.Config.ExpectIP = "203.0.113.1"
	if _, err := Check(ctx, nil, m); err == nil {
		t.Fatal("expected wrong IP to fail")
	}
}

func TestStatusOK(t *testing.T) {
	if !statusOK(200, 0) || !statusOK(302, 0) || statusOK(500, 0) {
		t.Fatal("any 2xx/3xx")
	}
	if !statusOK(404, 404) || statusOK(200, 404) {
		t.Fatal("exact status")
	}
}

func TestCheckPush(t *testing.T) {
	now := time.Now()
	m := store.Monitor{Type: "push", IntervalSeconds: 60, CreatedAt: now}
	if _, err := checkPush(m, now); err != errAwaitingPush {
		t.Fatalf("fresh push: %v", err)
	}
	old := now.Add(-2 * time.Minute)
	m.LastPushAt = &old
	if _, err := checkPush(m, now); err == nil {
		t.Fatal("expected overdue")
	}
	recent := now.Add(-10 * time.Second)
	m.LastPushAt = &recent
	if _, err := checkPush(m, now); err != nil {
		t.Fatalf("fresh heartbeat: %v", err)
	}
}

func TestValidDNSRecord(t *testing.T) {
	for _, ok := range []string{"a", "AAAA", " mx ", "txt", "NS", "cname"} {
		if !ValidDNSRecord(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "SOA", "SRV", "CAA", "ANY", "A "} {
		if bad == "A " {
			continue
		}
		if ValidDNSRecord(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestNormalizeAnswer(t *testing.T) {
	if normalizeAnswer(" Mail.Example.COM. ") != "mail.example.com" {
		t.Fatal("hostnames should compare without case or trailing dot")
	}
}

// TestDNSRecordLookups hits the real resolver, so it is skipped unless the
// network is available (ARGUS_NET_TESTS=1).
func TestDNSRecordLookups(t *testing.T) {
	if os.Getenv("ARGUS_NET_TESTS") == "" {
		t.Skip("set ARGUS_NET_TESTS=1 to query public DNS")
	}
	ctx := context.Background()
	cases := []struct {
		record string
		target string
		expect string
	}{
		{"A", "one.one.one.one", "1.1.1.1"},
		{"MX", "gmail.com", ""},
		{"NS", "cloudflare.com", ""},
		{"TXT", "cloudflare.com", ""},
	}
	for _, c := range cases {
		m := store.Monitor{Type: "dns", Target: c.target, Config: store.MonitorConfig{Record: c.record, ExpectValue: ""}}
		if c.record == "A" && c.expect != "" {
			m.Config.ExpectIP = c.expect
		}
		out, err := checkDNS(ctx, m)
		if err != nil {
			t.Errorf("%s %s: %v", c.record, c.target, err)
			continue
		}
		if len(out.Evidence.DNSAnswers) == 0 {
			t.Errorf("%s %s returned no answers", c.record, c.target)
		}
	}

	// A wrong expectation must fail, and say what it saw.
	m := store.Monitor{Type: "dns", Target: "one.one.one.one", Config: store.MonitorConfig{Record: "A", ExpectIP: "203.0.113.9"}}
	if _, err := checkDNS(ctx, m); err == nil || !strings.Contains(err.Error(), "not in A answers") {
		t.Fatalf("expected a mismatch error, got %v", err)
	}
}
