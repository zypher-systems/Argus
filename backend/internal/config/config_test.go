package config

import (
	"net/url"
	"testing"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("ARGUS_DB_URL", "")
	t.Setenv("ARGUS_LISTEN", "")
	t.Setenv("ARGUS_PROBE_LISTEN", "")
	t.Setenv("ARGUS_COOKIE_SECURE", "")
	cfg := Load()
	if cfg.DatabaseURL != "" {
		t.Fatalf("empty env should not invent a URL, got %q", cfg.DatabaseURL)
	}
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("listen = %q", cfg.ListenAddr)
	}
	if cfg.ProbeListen != ":8081" {
		t.Fatalf("probe listen = %q", cfg.ProbeListen)
	}
	if cfg.CookieSecure {
		t.Fatal("cookie secure should default off")
	}

	t.Setenv("ARGUS_DB_URL", "postgres://u:p@db:5432/argus")
	t.Setenv("ARGUS_COOKIE_SECURE", "true")
	cfg = Load()
	if cfg.DatabaseURL == "" {
		t.Fatal("expected URL")
	}
	if !cfg.CookieSecure {
		t.Fatal("expected cookie secure")
	}
}

func TestTrustedProxies(t *testing.T) {
	cases := map[string]int{
		"":     1, // the bundled web container
		"0":    0,
		"2":    2,
		" 3 ":  3,
		"-1":   1,
		"99":   1,
		"many": 1,
	}
	for in, want := range cases {
		t.Setenv("ARGUS_TRUSTED_PROXIES", in)
		if got := Load().TrustedProxies; got != want {
			t.Errorf("ARGUS_TRUSTED_PROXIES=%q: got %d, want %d", in, got, want)
		}
	}
}

func TestBootstrapAdminProblem(t *testing.T) {
	cases := []struct {
		email, password string
		ok              bool
	}{
		{"admin@example.com", "a-real-passphrase", true},
		{"admin@example.com", "12345678", true},
		{"", "a-real-passphrase", false},
		{"not-an-address", "a-real-passphrase", false},
		{"admin@example.com", "", false},
		{"admin@example.com", "short", false},
		{"admin@example.com", "change-me-too", false}, // what .env.example shipped with
		{"admin@example.com", "change-me", false},
	}
	for _, c := range cases {
		problem := (&Config{AdminEmail: c.email, AdminPassword: c.password}).BootstrapAdminProblem()
		if (problem == "") != c.ok {
			t.Errorf("email %q password %q: problem %q, want ok=%v", c.email, c.password, problem, c.ok)
		}
	}
}

// The bundled compose files hand over the database settings one by one. A
// password with URL punctuation in it must survive the trip.
func TestDatabaseURLFromSeparateSettings(t *testing.T) {
	t.Setenv("ARGUS_DB_URL", "")
	t.Setenv("ARGUS_DB_HOST", "db")
	t.Setenv("ARGUS_DB_USER", "argus")
	t.Setenv("ARGUS_DB_PASSWORD", "p@ss/w:rd#1?x%y z")
	t.Setenv("ARGUS_DB_NAME", "argus")
	got := Load().DatabaseURL
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("not a URL: %q: %v", got, err)
	}
	if pw, _ := u.User.Password(); pw != "p@ss/w:rd#1?x%y z" || u.User.Username() != "argus" {
		t.Fatalf("credentials did not survive: user %q password %q in %q", u.User.Username(), pw, got)
	}
	if u.Host != "db:5432" || u.Path != "/argus" || u.Query().Get("sslmode") != "disable" {
		t.Fatalf("host, database or sslmode wrong: %q", got)
	}

	// An explicit URL still wins, for installs that set one.
	t.Setenv("ARGUS_DB_URL", "postgres://u:p@elsewhere:5432/x")
	if got := Load().DatabaseURL; got != "postgres://u:p@elsewhere:5432/x" {
		t.Fatalf("ARGUS_DB_URL should take precedence: %q", got)
	}
	// Half a configuration is none.
	t.Setenv("ARGUS_DB_URL", "")
	t.Setenv("ARGUS_DB_NAME", "")
	if got := Load().DatabaseURL; got != "" {
		t.Fatalf("no database name, so no URL: %q", got)
	}
}
