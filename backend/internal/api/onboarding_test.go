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

func TestQuickMonitor(t *testing.T) {
	cases := []struct{ in, kind, name string }{
		{"https://shop.example.com", "http", "shop.example.com"},
		{"https://shop.example.com/", "http", "shop.example.com"},
		{"https://shop.example.com/api/health", "http", "shop.example.com/api/health"},
		{"db.internal:5432", "tcp", "db.internal:5432"},
		{"10.0.0.1", "icmp", "10.0.0.1"},
		{"router.lan", "icmp", "router.lan"},
		{"[2001:db8::1]:443", "tcp", "[2001:db8::1]:443"},
	}
	for _, c := range cases {
		got := quickMonitor(c.in)
		if got.Type != c.kind || got.Name != c.name || got.Target != c.in {
			t.Errorf("%q: got %s %q, want %s %q", c.in, got.Type, got.Name, c.kind, c.name)
		}
	}
}

func TestQuickAdd(t *testing.T) {
	s := testAPI(t)
	_, admin := signIn(t, s, "admin@example.com", "change-me-too", "198.51.100.10")
	if rec := call(s, admin, http.MethodPost, "/api/monitors", `{"name":"shop","type":"http","target":"https://shop.example.com","interval_seconds":60}`); rec.Code != 201 {
		t.Fatalf("seed %d", rec.Code)
	}
	text := strings.Join([]string{
		"# production",
		"https://shop.example.com",   // already watched
		"https://api.example.com/v1", // http
		"db.internal:5432",           // tcp
		"",
		"10.0.0.1",                   // icmp
		"ftp://files.example.com",    // not http(s)
		"db.internal:99999",          // bad port
		"https://api.example.com/v1", // duplicate within the paste
	}, "\n")
	b, _ := json.Marshal(map[string]string{"text": text})
	rec := call(s, admin, http.MethodPost, "/api/monitors/quick", string(b))
	if rec.Code != 200 {
		t.Fatalf("quick add %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Created []store.Monitor `json:"created"`
		Skipped []quickSkip     `json:"skipped"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Created) != 3 {
		t.Fatalf("want 3 created, got %d: %s", len(out.Created), rec.Body.String())
	}
	reasons := map[int]string{}
	for _, sk := range out.Skipped {
		reasons[sk.Line] = sk.Reason
	}
	if reasons[2] != "already watched" || reasons[9] != "already watched" {
		t.Fatalf("existing and repeated lines should be skipped as already watched: %v", reasons)
	}
	if reasons[7] == "" || reasons[8] == "" {
		t.Fatalf("invalid lines should say why, by line number: %v", reasons)
	}
	if _, ok := reasons[1]; ok {
		t.Fatal("comment lines are ignored, not skipped")
	}

	// Limits.
	var many []string
	for i := 0; i < maxQuickAdd+1; i++ {
		many = append(many, fmt.Sprintf("10.0.1.%d", i))
	}
	b, _ = json.Marshal(map[string]string{"text": strings.Join(many, "\n")})
	if rec := call(s, admin, http.MethodPost, "/api/monitors/quick", string(b)); rec.Code != http.StatusBadRequest {
		t.Fatalf("more than %d lines: want 400, got %d", maxQuickAdd, rec.Code)
	}
	if rec := call(s, admin, http.MethodPost, "/api/monitors/quick", `{"text":"  \n# nothing\n"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("nothing to add: want 400, got %d", rec.Code)
	}
}

func TestOnboardingFollowsTheData(t *testing.T) {
	s := testAPI(t)
	ctx := context.Background()
	if _, err := s.store.CreateUser(ctx, "viewer@example.com", "Viewer", store.RoleViewer, "viewer-password"); err != nil {
		t.Fatal(err)
	}
	_, admin := signIn(t, s, "admin@example.com", "change-me-too", "198.51.100.10")
	_, viewer := signIn(t, s, "viewer@example.com", "viewer-password", "198.51.100.11")
	state := func() store.Onboarding {
		t.Helper()
		rec := call(s, admin, http.MethodGet, "/api/onboarding", "")
		if rec.Code != 200 {
			t.Fatalf("onboarding %d", rec.Code)
		}
		var o store.Onboarding
		_ = json.Unmarshal(rec.Body.Bytes(), &o)
		return o
	}
	if o := state(); o.Monitors != 0 || o.Channels != 0 || o.StatusPages != 0 || o.TestAlertAt != nil || o.Complete() {
		t.Fatalf("fresh install: %+v", o)
	}

	call(s, admin, http.MethodPost, "/api/monitors/quick", `{"text":"10.0.0.1"}`)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(hook.Close)
	rec := call(s, admin, http.MethodPost, "/api/channels", fmt.Sprintf(`{"name":"hook","type":"webhook","dest":%q}`, hook.URL))
	var ch store.Channel
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)
	if o := state(); o.Monitors != 1 || o.Channels != 1 || o.TestAlertAt != nil {
		t.Fatalf("after a monitor and a channel: %+v", o)
	}

	// A failed test does not count; a delivered one does.
	broken := call(s, admin, http.MethodPost, "/api/channels", `{"name":"dead","type":"webhook","dest":"http://127.0.0.1:9/hook"}`)
	var dead store.Channel
	_ = json.Unmarshal(broken.Body.Bytes(), &dead)
	if rec := call(s, admin, http.MethodPost, fmt.Sprintf("/api/channels/%d/test", dead.ID), ""); rec.Code == 200 {
		t.Fatal("setup: the dead channel should fail")
	}
	if o := state(); o.TestAlertAt != nil {
		t.Fatal("a failed test must not tick the step")
	}
	if rec := call(s, admin, http.MethodPost, fmt.Sprintf("/api/channels/%d/test", ch.ID), ""); rec.Code != 200 {
		t.Fatalf("test %d %s", rec.Code, rec.Body.String())
	}
	rec = call(s, admin, http.MethodPost, "/api/onboarding/status-page", "")
	if rec.Code != 201 {
		t.Fatalf("status page %d %s", rec.Code, rec.Body.String())
	}
	var page store.StatusPage
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if page.Slug == "" || !page.Published || len(page.MonitorIDs) != 1 {
		t.Fatalf("the first page should be published with the monitors on it: %+v", page)
	}
	// A second one picks another free address rather than failing.
	rec = call(s, admin, http.MethodPost, "/api/onboarding/status-page", "")
	var second store.StatusPage
	_ = json.Unmarshal(rec.Body.Bytes(), &second)
	if rec.Code != 201 || second.Slug == page.Slug {
		t.Fatalf("second page should get a new slug: %d %+v", rec.Code, second)
	}
	if o := state(); !o.Complete() {
		t.Fatalf("every step done: %+v", o)
	}

	// Hiding it is a write, so viewers cannot.
	if rec := call(s, viewer, http.MethodPut, "/api/onboarding", `{"dismissed":true}`); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer dismiss: want 403, got %d", rec.Code)
	}
	if rec := call(s, admin, http.MethodPut, "/api/onboarding", `{"dismissed":true}`); rec.Code != 200 {
		t.Fatalf("dismiss %d", rec.Code)
	}
	if o := state(); o.DismissedAt == nil {
		t.Fatal("dismissal should stick")
	}
	call(s, admin, http.MethodPut, "/api/onboarding", `{"dismissed":false}`)
	if o := state(); o.DismissedAt != nil {
		t.Fatal("it can be brought back")
	}
}
