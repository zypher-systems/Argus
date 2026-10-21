package config

import (
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL       string
	ListenAddr        string
	ProbeListen       string
	AdminEmail        string
	AdminPassword     string
	CookieSecure      bool
	APIURL            string
	ProbeToken        string
	Hosted            bool
	HostedDefaultPlan string
	// SecretKey (ARGUS_SECRET_KEY, 64 hex chars) encrypts monitor credentials
	// at rest. Empty stores them as they are.
	SecretKey string
	// TrustedProxies (ARGUS_TRUSTED_PROXIES) is how many reverse proxies sit in
	// front of the API. Each one appends to X-Forwarded-For, so only that many
	// entries, counted from the right, can be believed. The default of 1 is the
	// bundled web container; add one for each proxy in front of it.
	TrustedProxies int
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	return envBoolDefault(key, false)
}

func envBoolDefault(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	default:
		return def
	}
}

// envProxies reads ARGUS_TRUSTED_PROXIES. Anything unreadable falls back to
// the bundled layout rather than to trusting the header outright.
func envProxies() int {
	v := strings.TrimSpace(os.Getenv("ARGUS_TRUSTED_PROXIES"))
	if v == "" {
		return 1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 10 {
		return 1
	}
	return n
}

func Load() *Config {
	hosted := HostedBuild && envBool("ARGUS_HOSTED")
	if !HostedBuild && envBool("ARGUS_HOSTED") {
		slog.Warn("ARGUS_HOSTED is ignored: this is the self-hosted build")
	}
	plan := strings.ToLower(strings.TrimSpace(env("ARGUS_HOSTED_PLAN", "community")))
	if plan != "pro" {
		plan = "community"
	}
	cookie := envBool("ARGUS_COOKIE_SECURE")
	if hosted && os.Getenv("ARGUS_COOKIE_SECURE") == "" {
		cookie = true
	}
	return &Config{
		DatabaseURL:       databaseURL(),
		ListenAddr:        env("ARGUS_LISTEN", ":8080"),
		ProbeListen:       env("ARGUS_PROBE_LISTEN", ":8081"),
		AdminEmail:        os.Getenv("ARGUS_ADMIN_EMAIL"),
		AdminPassword:     os.Getenv("ARGUS_ADMIN_PASSWORD"),
		CookieSecure:      cookie,
		APIURL:            env("ARGUS_API_URL", ""),
		ProbeToken:        os.Getenv("ARGUS_PROBE_TOKEN"),
		Hosted:            hosted,
		HostedDefaultPlan: plan,
		SecretKey:         strings.TrimSpace(os.Getenv("ARGUS_SECRET_KEY")),
		TrustedProxies:    envProxies(),
	}
}

// exampleAdminPasswords are the values the example env file has shipped
// with. An install started with one would have an administrator anyone who
// has read the README can sign in as.
var exampleAdminPasswords = []string{"change-me-too", "change-me"}

// BootstrapAdminProblem says why the first administrator cannot be created
// from ARGUS_ADMIN_EMAIL and ARGUS_ADMIN_PASSWORD, or "" when it can. It only
// matters on a first start: once an account exists both are ignored.
func (c *Config) BootstrapAdminProblem() string {
	email, password := strings.TrimSpace(c.AdminEmail), c.AdminPassword
	switch {
	case email == "" || !strings.Contains(email, "@"):
		return "set ARGUS_ADMIN_EMAIL to the address the first administrator signs in with"
	case password == "":
		return "set ARGUS_ADMIN_PASSWORD to the first administrator's password"
	case len(password) < 8:
		return "ARGUS_ADMIN_PASSWORD must be at least 8 characters"
	}
	for _, example := range exampleAdminPasswords {
		if password == example {
			return "ARGUS_ADMIN_PASSWORD is still the example value; choose your own"
		}
	}
	return ""
}

// databaseURL is ARGUS_DB_URL, or one built from ARGUS_DB_HOST, ARGUS_DB_USER,
// ARGUS_DB_PASSWORD and ARGUS_DB_NAME. The separate form is what the bundled
// compose files use: a password written into a URL breaks it as soon as it
// holds a /, @, :, # or ?, which generated passwords do.
func databaseURL() string {
	if v := os.Getenv("ARGUS_DB_URL"); v != "" {
		return v
	}
	host, name := strings.TrimSpace(os.Getenv("ARGUS_DB_HOST")), strings.TrimSpace(os.Getenv("ARGUS_DB_NAME"))
	if host == "" || name == "" {
		return ""
	}
	if !strings.Contains(host, ":") {
		host += ":5432"
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("ARGUS_DB_USER"), os.Getenv("ARGUS_DB_PASSWORD")),
		Host:     host,
		Path:     "/" + name,
		RawQuery: "sslmode=" + env("ARGUS_DB_SSLMODE", "disable"),
	}
	return u.String()
}
