package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"argus/internal/store"
)

// RDAPBaseURL is the bootstrap service that redirects to the registry holding
// a domain. It speaks JSON, unlike WHOIS, which is free text per registrar.
var RDAPBaseURL = "https://rdap.org"

const maxRDAPBody = 1 << 20

// rdapResponse is the slice of RDAP we use: the registration events and the
// registrar's status codes.
type rdapResponse struct {
	LDHName string `json:"ldhName"`
	Status  []string
	Events  []struct {
		Action string `json:"eventAction"`
		Date   string `json:"eventDate"`
	} `json:"events"`
}

// DomainName strips a scheme, port, path and trailing dot, so an operator can
// paste a URL and still get a domain check.
func DomainName(target string) string {
	t := strings.TrimSpace(target)
	if i := strings.Index(t, "://"); i >= 0 {
		t = t[i+3:]
	}
	t = strings.TrimSuffix(strings.Split(strings.Split(t, "/")[0], ":")[0], ".")
	return strings.ToLower(t)
}

// checkDomain looks the domain up over RDAP and reports when it expires. The
// check fails only when the lookup fails or the domain has already lapsed;
// the approaching-expiry warning is a separate one-shot alert.
func checkDomain(ctx context.Context, m store.Monitor) (CheckOutcome, error) {
	timeout := time.Duration(timeoutSeconds(m, 15)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name := DomainName(m.Target)
	if name == "" {
		return CheckOutcome{}, fmt.Errorf("target must be a domain name")
	}
	endpoint := strings.TrimRight(RDAPBaseURL, "/") + "/domain/" + name

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return CheckOutcome{}, err
	}
	req.Header.Set("Accept", "application/rdap+json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient(false, timeout).Do(req)
	out := CheckOutcome{LatencyMs: float64(time.Since(start).Microseconds()) / 1000}
	if err != nil {
		return out, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxRDAPBody))
	out.Evidence.StatusCode = resp.StatusCode
	if resp.StatusCode == http.StatusNotFound {
		return out, fmt.Errorf("domain %s is not registered", name)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.Evidence.BodySnippet = snippetBody(body, 300)
		return out, fmt.Errorf("rdap lookup failed: http %d", resp.StatusCode)
	}

	var doc rdapResponse
	if err := json.Unmarshal(body, &doc); err != nil {
		return out, fmt.Errorf("rdap response was not valid JSON")
	}
	expires := rdapEvent(doc, "expiration")
	if expires == nil {
		return out, fmt.Errorf("rdap has no expiration date for %s", name)
	}
	out.ExpiresAt = expires
	if len(doc.Status) > 0 {
		out.Evidence.DNSAnswers = doc.Status
	}
	if expires.Before(time.Now()) {
		return out, fmt.Errorf("domain expired %s", expires.UTC().Format("2006-01-02"))
	}
	return out, nil
}

func rdapEvent(doc rdapResponse, action string) *time.Time {
	for _, ev := range doc.Events {
		if !strings.EqualFold(ev.Action, action) {
			continue
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, strings.TrimSpace(ev.Date)); err == nil {
				utc := t.UTC()
				return &utc
			}
		}
	}
	return nil
}
