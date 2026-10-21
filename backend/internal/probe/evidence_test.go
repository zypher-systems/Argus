package probe

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestClassifyTLSError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{fmt.Errorf("status 500"), ""},
		{fmt.Errorf("heartbeat overdue (2m0s)"), ""},
		{fmt.Errorf("tls: handshake failure"), "handshake"},
		{fmt.Errorf("x509: certificate has expired or is not yet valid"), "expired"},
		{fmt.Errorf("certificate expired 2020-01-02"), "expired"},
		{fmt.Errorf("x509: certificate is valid for foo, not bar"), "hostname mismatch"},
		{fmt.Errorf("x509: certificate signed by unknown authority"), "unknown authority"},
	}
	for _, c := range cases {
		if got := classifyTLSError(c.err); got != c.want {
			t.Errorf("classifyTLSError(%v) = %q want %q", c.err, got, c.want)
		}
	}
}

func TestSnippetBody(t *testing.T) {
	got := snippetBody([]byte("hello\x00world"), 2048)
	if !strings.Contains(got, "hello") || strings.Contains(got, "\x00") {
		t.Fatalf("snippet %q", got)
	}
	long := strings.Repeat("a", 3000)
	if len(snippetBody([]byte(long), 2048)) != 2048 {
		t.Fatal("expected cap at 2048")
	}
}

func TestHTTPCurlRedactsAuthorization(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://app.example/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Request-Id", "abc")
	got := httpCurl(req)
	if strings.Contains(got, "secret") {
		t.Fatalf("curl leaked secret: %s", got)
	}
	if !strings.Contains(got, "Authorization: <redacted>") {
		t.Fatalf("curl missing redaction: %s", got)
	}
	if !strings.Contains(got, "X-Request-Id: abc") {
		t.Fatalf("curl missing header: %s", got)
	}
	if _, err := url.Parse("https://app.example/health"); err != nil {
		t.Fatal(err)
	}
}
