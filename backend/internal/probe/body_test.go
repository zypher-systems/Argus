package probe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"argus/internal/store"
)

// cutOffServer promises more body than it sends, then drops the connection.
func cutOffServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)+100))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stalledServer sends its headers and part of a body, then goes quiet until
// the client gives up.
func stalledServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	t.Cleanup(func() {
		close(done)
		srv.Close()
	})
	return srv
}

func TestCheckHTTPFailsOnIncompleteBody(t *testing.T) {
	const page = "<html>welcome to the shop"
	cases := []struct {
		name string
		srv  *httptest.Server
		cfg  store.MonitorConfig
		want string
	}{
		{name: "cut off", srv: cutOffServer(t, page), want: "reading response body"},
		{
			name: "cut off after the keyword",
			srv:  cutOffServer(t, page),
			cfg:  store.MonitorConfig{Keyword: "welcome"},
			want: "reading response body",
		},
		{
			name: "cut off before a forbidden keyword could appear",
			srv:  cutOffServer(t, page),
			cfg:  store.MonitorConfig{Keyword: "maintenance", KeywordInvert: true},
			want: "reading response body",
		},
		{name: "stalled until the timeout", srv: stalledServer(t, page), want: "reading response body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.cfg.TimeoutSeconds = 1
			m := store.Monitor{Type: "http", Target: c.srv.URL, Config: c.cfg}
			out, err := checkHTTP(context.Background(), m)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
			// What did arrive is still the evidence for why it failed.
			if out.Evidence.StatusCode != http.StatusOK || !strings.Contains(out.Evidence.BodySnippet, "welcome") {
				t.Fatalf("evidence lost: %+v", out.Evidence)
			}
		})
	}
}

func TestCheckHTTPReportsAWrongStatusBeforeABrokenBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "bad")
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}))
	defer srv.Close()
	_, err := checkHTTP(context.Background(), store.Monitor{Type: "http", Target: srv.URL})
	if err == nil || err.Error() != "status 502" {
		t.Fatalf("err = %v", err)
	}
}

// The cap on what Argus reads is not an incomplete body, and neither is a
// response that has none.
func TestCheckHTTPCompleteBodiesStayUp(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", maxBody*2))
	}))
	defer big.Close()
	if _, err := checkHTTP(context.Background(), store.Monitor{Type: "http", Target: big.URL}); err != nil {
		t.Fatalf("body over the cap: %v", err)
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer empty.Close()
	if _, err := checkHTTP(context.Background(), store.Monitor{Type: "http", Target: empty.URL}); err != nil {
		t.Fatalf("204: %v", err)
	}

	head := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
	}))
	defer head.Close()
	m := store.Monitor{Type: "http", Target: head.URL, Config: store.MonitorConfig{Method: http.MethodHead}}
	if _, err := checkHTTP(context.Background(), m); err != nil {
		t.Fatalf("HEAD: %v", err)
	}
}

// A response still sending at the cap counts as up: Argus has read what it
// reads, and following it to the end would fail any large file at the timeout.
func TestCheckHTTPStreamPastTheCapStaysUp(t *testing.T) {
	srv := stalledServer(t, strings.Repeat("x", maxBody*2))
	m := store.Monitor{Type: "http", Target: srv.URL, Config: store.MonitorConfig{TimeoutSeconds: 1}}
	if _, err := checkHTTP(context.Background(), m); err != nil {
		t.Fatalf("stream past the cap: %v", err)
	}
}

func TestTransactionFailsOnIncompleteBody(t *testing.T) {
	const page = `{"token":"tok-123456`
	for name, srv := range map[string]*httptest.Server{
		"cut off": cutOffServer(t, page),
		"stalled": stalledServer(t, page),
	} {
		t.Run(name, func(t *testing.T) {
			m := store.Monitor{Type: "transaction", Target: srv.URL, Config: store.MonitorConfig{
				TimeoutSeconds: 1,
				Steps: []store.TransactionStep{
					{Name: "login", Method: "GET", URL: srv.URL, Keyword: "token"},
					{Name: "never reached", Method: "GET", URL: srv.URL},
				},
			}}
			out, err := checkTransaction(context.Background(), m)
			if err == nil || !strings.Contains(err.Error(), `step 1 (login): reading response body`) {
				t.Fatalf("err = %v", err)
			}
			if len(out.Evidence.Steps) != 1 {
				t.Fatalf("ran %d steps, want the failing one only", len(out.Evidence.Steps))
			}
		})
	}
}
