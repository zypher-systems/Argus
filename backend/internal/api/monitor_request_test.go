package api

import (
	"encoding/json"
	"strings"
	"testing"

	"argus/internal/store"
)

func TestOptionalChannelIDs(t *testing.T) {
	var omitted monitorRequest
	if err := json.Unmarshal([]byte(`{"name":"x","target":"1.1.1.1"}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if omitted.ChannelIDs.present {
		t.Fatal("omitted should not be present")
	}

	var asNull monitorRequest
	if err := json.Unmarshal([]byte(`{"channel_ids":null}`), &asNull); err != nil {
		t.Fatal(err)
	}
	if !asNull.ChannelIDs.present || asNull.ChannelIDs.ids != nil {
		t.Fatalf("null = present %#v", asNull.ChannelIDs)
	}

	var empty monitorRequest
	if err := json.Unmarshal([]byte(`{"channel_ids":[]}`), &empty); err != nil {
		t.Fatal(err)
	}
	if !empty.ChannelIDs.present || empty.ChannelIDs.ids == nil || len(empty.ChannelIDs.ids) != 0 {
		t.Fatalf("empty = %#v", empty.ChannelIDs)
	}

	var some monitorRequest
	if err := json.Unmarshal([]byte(`{"channel_ids":[3,8]}`), &some); err != nil {
		t.Fatal(err)
	}
	if !some.ChannelIDs.present || len(some.ChannelIDs.ids) != 2 || some.ChannelIDs.ids[0] != 3 {
		t.Fatalf("some = %#v", some.ChannelIDs)
	}
}

func TestMonitorRequestValidate(t *testing.T) {
	enabled := true
	ok := monitorRequest{
		Name:            "edge",
		Target:          "1.1.1.1",
		Type:            "icmp",
		IntervalSeconds: 60,
		Retries:         3,
		Enabled:         &enabled,
	}
	if msg := ok.validate(); msg != "" {
		t.Fatalf("valid request rejected: %s", msg)
	}

	missingName := ok
	missingName.Name = "  "
	if msg := missingName.validate(); msg == "" {
		t.Fatal("expected name required")
	}

	unknown := ok
	unknown.Type = "browser"
	if msg := unknown.validate(); msg != "unsupported monitor type" {
		t.Fatalf("type = %q, want unsupported monitor type", msg)
	}

	httpOK := ok
	httpOK.Type = "http"
	httpOK.Target = "https://app.internal/health"
	if msg := httpOK.validate(); msg != "" {
		t.Fatalf("valid http rejected: %s", msg)
	}

	certOK := ok
	certOK.Type = "cert"
	certOK.Target = "example.com:443"
	if msg := certOK.validate(); msg != "" {
		t.Fatalf("valid cert rejected: %s", msg)
	}

	pushOK := ok
	pushOK.Type = "push"
	pushOK.Target = "nightly backup"
	if msg := pushOK.validate(); msg != "" {
		t.Fatalf("valid push rejected: %s", msg)
	}

	groupOK := ok
	groupOK.Type = "group"
	groupOK.Target = ""
	if msg := groupOK.validate(); msg != "" {
		t.Fatalf("valid group rejected: %s", msg)
	}

	httpBad := ok
	httpBad.Type = "http"
	httpBad.Target = "app.internal"
	if msg := httpBad.validate(); msg == "" {
		t.Fatal("expected http URL error")
	}

	tcpOK := ok
	tcpOK.Type = "tcp"
	tcpOK.Target = "db.internal:5432"
	if msg := tcpOK.validate(); msg != "" {
		t.Fatalf("valid tcp rejected: %s", msg)
	}

	shortInterval := ok
	shortInterval.IntervalSeconds = 5
	if msg := shortInterval.validate(); msg == "" {
		t.Fatal("expected interval too short")
	}

	threshOK := ok
	threshOK.LatencyThresholdMs = 250
	if msg := threshOK.validate(); msg != "" {
		t.Fatalf("valid threshold rejected: %s", msg)
	}

	threshNeg := ok
	threshNeg.LatencyThresholdMs = -1
	if msg := threshNeg.validate(); msg == "" {
		t.Fatal("expected negative threshold rejected")
	}

	threshBig := ok
	threshBig.LatencyThresholdMs = 600001
	if msg := threshBig.validate(); msg == "" {
		t.Fatal("expected oversized threshold rejected")
	}
}

func TestNormalizeHTTPExtras(t *testing.T) {
	enabled := true
	base := monitorRequest{
		Name:            "api",
		Target:          "https://app.internal/health",
		Type:            "http",
		IntervalSeconds: 60,
		Retries:         3,
		Enabled:         &enabled,
		Config: store.MonitorConfig{
			Method:     "POST",
			Body:       `{"ping":true}`,
			JSONPath:   "status",
			JSONExpect: "ok",
			Auth:       "bearer",
			AuthToken:  "t",
		},
	}
	if msg := base.validate(); msg != "" {
		t.Fatalf("valid post rejected: %s", msg)
	}

	getBody := base
	getBody.Config.Method = "GET"
	if msg := getBody.validate(); msg == "" {
		t.Fatal("expected GET with body rejected")
	}

	headJSON := base
	headJSON.Config.Method = "HEAD"
	headJSON.Config.Body = ""
	headJSON.Config.Auth = ""
	headJSON.Config.AuthToken = ""
	if msg := headJSON.validate(); msg == "" {
		t.Fatal("expected HEAD with json_path rejected")
	}

	badPath := base
	badPath.Config.JSONPath = "status[0]"
	if msg := badPath.validate(); msg == "" {
		t.Fatal("expected bad json_path rejected")
	}

	authHeader := base
	authHeader.Config.Headers = map[string]string{"Authorization": "Bearer other"}
	if msg := authHeader.validate(); msg == "" {
		t.Fatal("expected auth + Authorization header rejected")
	}

	noUser := base
	noUser.Config.Auth = "basic"
	noUser.Config.AuthUser = ""
	noUser.Config.AuthToken = ""
	if msg := noUser.validate(); msg == "" {
		t.Fatal("expected basic without user rejected")
	}

	tooBig := base
	tooBig.Config.Body = strings.Repeat("x", 8193)
	if msg := tooBig.validate(); msg == "" {
		t.Fatal("expected oversized body rejected")
	}

	put := base
	put.Config.Method = "PUT"
	put.Config.Body = ""
	put.Config.JSONPath = ""
	put.Config.Auth = ""
	put.Config.AuthToken = ""
	if msg := put.validate(); msg == "" {
		t.Fatal("expected PUT rejected")
	}
}

func TestValidateNewMonitorTypes(t *testing.T) {
	cases := []struct {
		name string
		body monitorRequest
		ok   bool
	}{
		{"domain ok", monitorRequest{Name: "d", Type: "domain", Target: "example.com"}, true},
		{"domain from url", monitorRequest{Name: "d", Type: "domain", Target: "https://example.com/x"}, true},
		{"domain needs a dot", monitorRequest{Name: "d", Type: "domain", Target: "localhost"}, false},
		{"dns mx", monitorRequest{Name: "d", Type: "dns", Target: "example.com", Config: store.MonitorConfig{Record: "MX", ExpectValue: "10 mail.example.com"}}, true},
		{"dns txt", monitorRequest{Name: "d", Type: "dns", Target: "example.com", Config: store.MonitorConfig{Record: "txt", ExpectValue: "v=spf1"}}, true},
		{"dns bad record", monitorRequest{Name: "d", Type: "dns", Target: "example.com", Config: store.MonitorConfig{Record: "SRV"}}, false},
		{"dns bad ip", monitorRequest{Name: "d", Type: "dns", Target: "example.com", Config: store.MonitorConfig{Record: "A", ExpectIP: "not-an-ip"}}, false},
		{"smtp host only", monitorRequest{Name: "m", Type: "smtp", Target: "mail.example.com"}, true},
		{"smtp host port", monitorRequest{Name: "m", Type: "smtp", Target: "mail.example.com:587", Config: store.MonitorConfig{TLSMode: "starttls"}}, true},
		{"smtp bad tls", monitorRequest{Name: "m", Type: "imap", Target: "mail.example.com", Config: store.MonitorConfig{TLSMode: "ssl"}}, false},
		{"postgres", monitorRequest{Name: "db", Type: "postgres", Target: "db:5432", Config: store.MonitorConfig{AuthUser: "argus", AuthPassword: "x", Database: "app"}}, true},
		{"redis read command", monitorRequest{Name: "c", Type: "redis", Target: "cache", Config: store.MonitorConfig{Query: "GET health"}}, true},
		{"redis write command", monitorRequest{Name: "c", Type: "redis", Target: "cache", Config: store.MonitorConfig{Query: "FLUSHALL"}}, false},
		{"redis del", monitorRequest{Name: "c", Type: "redis", Target: "cache", Config: store.MonitorConfig{Query: "del key"}}, false},
		{"query too long", monitorRequest{Name: "db", Type: "mysql", Target: "db", Config: store.MonitorConfig{Query: strings.Repeat("x", 501)}}, false},
		{"bad port", monitorRequest{Name: "db", Type: "postgres", Target: "db:99999"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := c.body
			msg := body.validate()
			if c.ok && msg != "" {
				t.Fatalf("expected valid, got %q", msg)
			}
			if !c.ok && msg == "" {
				t.Fatal("expected a rejection")
			}
		})
	}
}

func TestDatabaseConfigKeepsCredentialsAndDropsNoise(t *testing.T) {
	body := monitorRequest{
		Name: "db", Type: "postgres", Target: "db:5432",
		Config: store.MonitorConfig{
			AuthUser: " argus ", AuthPassword: "hunter2", Database: " app ",
			Query: " SELECT 1 ", ExpectValue: " 1 ",
			// Fields from other monitor types must not survive.
			Keyword: "nope", Method: "POST", PushToken: "leak",
		},
	}
	if msg := body.validate(); msg != "" {
		t.Fatal(msg)
	}
	cfg := body.Config
	if cfg.AuthUser != "argus" || cfg.AuthPassword != "hunter2" || cfg.Database != "app" {
		t.Fatalf("credentials mangled: %+v", cfg)
	}
	if cfg.Query != "SELECT 1" || cfg.ExpectValue != "1" {
		t.Fatalf("query/expectation mangled: %+v", cfg)
	}
	if cfg.Keyword != "" || cfg.Method != "" || cfg.PushToken != "" {
		t.Fatalf("config from another type leaked through: %+v", cfg)
	}
}
