package probe

import (
	"context"
	"os"
	"strings"
	"testing"

	"argus/internal/store"
)

func TestDatabaseTypeHelpers(t *testing.T) {
	if !IsDatabaseType("postgres") || !IsDatabaseType(" REDIS ") || IsDatabaseType("http") {
		t.Fatal("type detection")
	}
	for kind, want := range map[string]string{"postgres": "5432", "mysql": "3306", "redis": "6379"} {
		if got := DatabaseDefaultPort(kind); got != want {
			t.Errorf("%s default port %s, want %s", kind, got, want)
		}
	}
	host, port := hostPort("postgres", "db.internal")
	if host != "db.internal" || port != "5432" {
		t.Fatalf("bare host should get the default port, got %s:%s", host, port)
	}
	if h, p := hostPort("redis", "10.0.0.5:6380"); h != "10.0.0.5" || p != "6380" {
		t.Fatalf("explicit port should win, got %s:%s", h, p)
	}
}

func TestCleanDBErrorHidesPassword(t *testing.T) {
	err := cleanDBError(errorString(`failed to connect to postgres://argus:hunter2@db:5432/app: timeout`))
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("a connection error must not echo the password: %v", err)
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("the useful part should survive: %v", err)
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestMatchExpected(t *testing.T) {
	m := store.Monitor{Config: store.MonitorConfig{ExpectValue: "ok"}}
	if err := matchExpected(m, " OK "); err != nil {
		t.Fatalf("comparison should ignore case and spacing: %v", err)
	}
	if err := matchExpected(m, "degraded"); err == nil {
		t.Fatal("a mismatch must fail")
	}
	if err := matchExpected(store.Monitor{}, "anything"); err != nil {
		t.Fatal("no expectation means anything passes")
	}
}

// TestDatabaseChecksLive runs against real servers. Set the URLs to enable it:
//
//	ARGUS_TEST_PG=host:port ARGUS_TEST_MYSQL=host:port ARGUS_TEST_REDIS=host:port
func TestDatabaseChecksLive(t *testing.T) {
	cases := []struct {
		env, kind, user, pass, db string
	}{
		{"ARGUS_TEST_PG", "postgres", "t", "t", "t"},
		{"ARGUS_TEST_MYSQL", "mysql", "root", "t", "t"},
		{"ARGUS_TEST_REDIS", "redis", "", "", ""},
	}
	ran := 0
	for _, c := range cases {
		addr := os.Getenv(c.env)
		if addr == "" {
			continue
		}
		ran++
		m := store.Monitor{Type: c.kind, Target: addr, Config: store.MonitorConfig{
			TimeoutSeconds: 10, AuthUser: c.user, AuthPassword: c.pass, Database: c.db,
			InsecureSkipVerify: true,
		}}
		out, err := checkDatabase(context.Background(), m)
		if err != nil {
			t.Errorf("%s: %v", c.kind, err)
			continue
		}
		if out.LatencyMs <= 0 {
			t.Errorf("%s: no latency recorded", c.kind)
		}
		t.Logf("%s ok in %.1fms (%s)", c.kind, out.LatencyMs, out.Evidence.BodySnippet)

		// A wrong password must fail, and must not leak into the message.
		if c.pass != "" {
			bad := m
			bad.Config.AuthPassword = "definitely-wrong"
			if _, err := checkDatabase(context.Background(), bad); err == nil {
				t.Errorf("%s: bad credentials should fail", c.kind)
			} else if strings.Contains(err.Error(), "definitely-wrong") {
				t.Errorf("%s: error leaked the password: %v", c.kind, err)
			}
		}

		// An expectation that does not hold must fail.
		exp := m
		exp.Config.Query = queryFor(c.kind)
		exp.Config.ExpectValue = "not-this"
		if _, err := checkDatabase(context.Background(), exp); err == nil {
			t.Errorf("%s: a wrong expected value should fail", c.kind)
		}
	}
	if ran == 0 {
		t.Skip("set ARGUS_TEST_PG / ARGUS_TEST_MYSQL / ARGUS_TEST_REDIS to run these")
	}
}

func queryFor(kind string) string {
	if kind == "redis" {
		return ""
	}
	return "SELECT 'hello'"
}
