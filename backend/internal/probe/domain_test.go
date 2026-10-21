package probe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"argus/internal/store"
)

func TestDomainName(t *testing.T) {
	cases := map[string]string{
		"example.com":                  "example.com",
		"  Example.COM. ":              "example.com",
		"https://example.com/path?a=b": "example.com",
		"http://example.com:8443/":     "example.com",
		"sub.example.co.uk":            "sub.example.co.uk",
		"":                             "",
	}
	for in, want := range cases {
		if got := DomainName(in); got != want {
			t.Errorf("DomainName(%q) = %q, want %q", in, got, want)
		}
	}
}

func rdapServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/domain/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/rdap+json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	prev := RDAPBaseURL
	RDAPBaseURL = srv.URL
	t.Cleanup(func() {
		RDAPBaseURL = prev
		srv.Close()
	})
	return srv
}

func domainMonitor(target string) store.Monitor {
	return store.Monitor{Type: "domain", Target: target, Config: store.MonitorConfig{TimeoutSeconds: 5}}
}

func TestCheckDomainReadsExpiry(t *testing.T) {
	expiry := time.Now().AddDate(0, 4, 0).UTC().Truncate(time.Second)
	body := fmt.Sprintf(`{"ldhName":"example.com","status":["client transfer prohibited"],
		"events":[{"eventAction":"registration","eventDate":"2001-01-01T00:00:00Z"},
		          {"eventAction":"expiration","eventDate":%q}]}`, expiry.Format(time.RFC3339))
	rdapServer(t, 200, body)

	out, err := checkDomain(context.Background(), domainMonitor("https://example.com/ignored"))
	if err != nil {
		t.Fatal(err)
	}
	if out.ExpiresAt == nil || !out.ExpiresAt.Equal(expiry) {
		t.Fatalf("expiry %v, want %v", out.ExpiresAt, expiry)
	}
	if len(out.Evidence.DNSAnswers) != 1 {
		t.Fatalf("registrar status should be kept as evidence: %+v", out.Evidence)
	}
}

func TestCheckDomainFailures(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		past := time.Now().AddDate(0, 0, -3).UTC()
		rdapServer(t, 200, fmt.Sprintf(`{"events":[{"eventAction":"expiration","eventDate":%q}]}`, past.Format(time.RFC3339)))
		out, err := checkDomain(context.Background(), domainMonitor("example.com"))
		if err == nil || !strings.Contains(err.Error(), "expired") {
			t.Fatalf("an expired domain must fail the check, got %v", err)
		}
		if out.ExpiresAt == nil {
			t.Fatal("the expiry should still be reported")
		}
	})
	t.Run("not registered", func(t *testing.T) {
		rdapServer(t, 404, `{"errorCode":404}`)
		if _, err := checkDomain(context.Background(), domainMonitor("nope.example")); err == nil || !strings.Contains(err.Error(), "not registered") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("no expiration event", func(t *testing.T) {
		rdapServer(t, 200, `{"events":[{"eventAction":"registration","eventDate":"2001-01-01T00:00:00Z"}]}`)
		if _, err := checkDomain(context.Background(), domainMonitor("example.com")); err == nil || !strings.Contains(err.Error(), "no expiration") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("garbage", func(t *testing.T) {
		rdapServer(t, 200, `not json`)
		if _, err := checkDomain(context.Background(), domainMonitor("example.com")); err == nil || !strings.Contains(err.Error(), "valid JSON") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("registry error", func(t *testing.T) {
		rdapServer(t, 503, `busy`)
		if _, err := checkDomain(context.Background(), domainMonitor("example.com")); err == nil || !strings.Contains(err.Error(), "503") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestRDAPDateFormats(t *testing.T) {
	for _, date := range []string{"2027-03-01T00:00:00Z", "2027-03-01T00:00:00-0500", "2027-03-01"} {
		doc := rdapResponse{Events: []struct {
			Action string `json:"eventAction"`
			Date   string `json:"eventDate"`
		}{{Action: "expiration", Date: date}}}
		if got := rdapEvent(doc, "expiration"); got == nil {
			t.Errorf("registries write dates as %q; that should parse", date)
		}
	}
}

// TestCheckDomainLive queries the real RDAP bootstrap service.
func TestCheckDomainLive(t *testing.T) {
	if os.Getenv("ARGUS_NET_TESTS") == "" {
		t.Skip("set ARGUS_NET_TESTS=1 to query real RDAP")
	}
	out, err := checkDomain(context.Background(), domainMonitor("example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if out.ExpiresAt == nil || out.ExpiresAt.Before(time.Now()) {
		t.Fatalf("example.com should have a future expiry, got %v", out.ExpiresAt)
	}
	t.Logf("example.com expires %s", out.ExpiresAt.Format("2006-01-02"))
}
