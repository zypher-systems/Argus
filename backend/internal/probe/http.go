package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"argus/internal/store"
	"argus/internal/version"
)

// userAgent follows the build version so target logs show which Argus probed them.
var userAgent = "Argus/" + version.Version

const maxBody = 1 << 20

func redirectLimit(_ *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("stopped after 5 redirects")
	}
	return nil
}

func newHTTPClient(insecure bool) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			// A check opens its own connection. Reusing one kept a monitor
			// green while the server refused every new visitor, left connect
			// and TLS time out of the latency, and hid a changed certificate
			// until the connection happened to be replaced.
			DisableKeepAlives:     true,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: insecure},
		},
		CheckRedirect: redirectLimit,
	}
}

var (
	httpSecure   = newHTTPClient(false)
	httpInsecure = newHTTPClient(true)
)

func httpClient(insecure bool, timeout time.Duration) *http.Client {
	base := httpSecure
	if insecure {
		base = httpInsecure
	}
	c := *base
	if timeout > 0 {
		c.Timeout = timeout
	}
	return &c
}

func httpClientFor(m store.Monitor, timeout time.Duration) *http.Client {
	c := httpClient(m.Config.InsecureSkipVerify, timeout)
	proxyRaw := strings.TrimSpace(m.Config.ProxyURL)
	if proxyRaw == "" {
		return c
	}
	proxyURL, err := url.Parse(proxyRaw)
	if err != nil {
		return c
	}
	base := httpSecure.Transport.(*http.Transport).Clone()
	base.Proxy = http.ProxyURL(proxyURL)
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: m.Config.InsecureSkipVerify}
	out := *c
	out.Transport = base
	return &out
}

func leafExpiry(resp *http.Response) *time.Time {
	if resp == nil || resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return nil
	}
	t := resp.TLS.PeerCertificates[0].NotAfter.UTC()
	return &t
}

func checkHTTP(ctx context.Context, m store.Monitor) (CheckOutcome, error) {
	timeout := time.Duration(timeoutSeconds(m, 10)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	method := m.Config.Method
	if method == "" {
		method = http.MethodGet
	}
	var reqBody io.Reader
	if method == http.MethodPost && m.Config.Body != "" {
		reqBody = bytes.NewReader([]byte(m.Config.Body))
	}
	req, err := http.NewRequestWithContext(ctx, method, m.Target, reqBody)
	if err != nil {
		return CheckOutcome{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	for k, v := range m.Config.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		// Go sends the Host from the request, not from the header map, so
		// one set there was dropped without a word: checking a virtual host
		// by IP address tested the default site instead.
		if strings.EqualFold(strings.TrimSpace(k), "Host") {
			req.Host = strings.TrimSpace(v)
			continue
		}
		req.Header.Set(k, v)
	}
	applyHTTPAuth(req, m.Config)
	if method == http.MethodPost && m.Config.Body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	client := httpClientFor(m, timeout)
	// Expecting a redirect means the redirect is the answer. Following it
	// made an expected 301 or 302 impossible to match.
	if e := m.Config.ExpectStatus; e >= 300 && e < 400 {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}

	out := CheckOutcome{}
	out.Evidence.Curl = httpCurl(req)

	start := time.Now()
	resp, err := client.Do(req)
	out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		out.Evidence.TLSError = classifyTLSError(err)
		return out, err
	}
	defer func() { _ = resp.Body.Close() }()
	out.TLSExpiresAt = leafExpiry(resp)
	out.Evidence.StatusCode = resp.StatusCode
	out.Evidence.ContentType = resp.Header.Get("Content-Type")

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	out.Evidence.BodySnippet = snippetBody(body, maxEvidenceBody)
	if !statusOK(resp.StatusCode, m.Config.ExpectStatus) {
		return out, fmt.Errorf("status %d", resp.StatusCode)
	}
	if kw := strings.TrimSpace(m.Config.Keyword); kw != "" && method != http.MethodHead {
		found := strings.Contains(string(body), kw)
		if m.Config.KeywordInvert {
			if found {
				return out, fmt.Errorf("keyword %q found", kw)
			}
		} else if !found {
			return out, fmt.Errorf("keyword %q not found", kw)
		}
	}
	if path := strings.TrimSpace(m.Config.JSONPath); path != "" && method != http.MethodHead {
		if err := matchJSONPath(body, path, m.Config.JSONExpect); err != nil {
			return out, err
		}
	}
	return out, nil
}

func applyHTTPAuth(req *http.Request, cfg store.MonitorConfig) {
	switch strings.ToLower(strings.TrimSpace(cfg.Auth)) {
	case "basic":
		req.SetBasicAuth(cfg.AuthUser, cfg.AuthPassword)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}
}

func matchJSONPath(body []byte, path, expect string) error {
	got, err := lookupJSONPath(body, path)
	if err != nil {
		return err
	}
	if got != expect {
		return fmt.Errorf("json path %q: got %q, want %q", path, got, expect)
	}
	return nil
}

// lookupJSONPath follows a dotted path through a JSON body. A numeric segment
// indexes an array, so "items.0.id" reads the first item's id.
func lookupJSONPath(body []byte, path string) (string, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return "", fmt.Errorf("response is not JSON")
	}
	cur := v
	for _, key := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[key]
			if !ok {
				return "", fmt.Errorf("json path %q missing", path)
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(node) {
				return "", fmt.Errorf("json path %q missing", path)
			}
			cur = node[i]
		default:
			return "", fmt.Errorf("json path %q missing", path)
		}
	}
	return stringifyJSONLeaf(cur), nil
}

func stringifyJSONLeaf(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	case string:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func statusOK(got, expect int) bool {
	if expect == 0 {
		return got >= 200 && got < 400
	}
	return got == expect
}
