package api

import (
	"net/url"
	"strings"

	"argus/internal/store"
)

func redactMonitorConfig(cfg *store.MonitorConfig) {
	cfg.AuthPassword = ""
	cfg.AuthToken = ""
	// Names stay so the editor can list them; values never leave.
	if len(cfg.Secrets) > 0 {
		names := make(map[string]string, len(cfg.Secrets))
		for name := range cfg.Secrets {
			names[name] = ""
		}
		cfg.Secrets = names
	}
}

func redactMonitor(m *store.Monitor) {
	redactMonitorConfig(&m.Config)
}

func redactMonitorWithState(m *store.MonitorWithState) {
	redactMonitorConfig(&m.Config)
}

func mergeAuthSecrets(dst *store.MonitorConfig, existing store.MonitorConfig) {
	if dst.Auth == "basic" && dst.AuthPassword == "" {
		dst.AuthPassword = existing.AuthPassword
	}
	// A database monitor has no Auth mode; an empty password on update means
	// "keep the one you have", since the API never sends it back.
	if dst.Auth == "" && dst.AuthPassword == "" && dst.AuthUser != "" {
		dst.AuthPassword = existing.AuthPassword
	}
	if dst.Auth == "bearer" && dst.AuthToken == "" {
		dst.AuthToken = existing.AuthToken
	}
	if dst.PushToken == "" {
		dst.PushToken = existing.PushToken
	}
	mergeTransactionSecrets(dst, existing)
}

var errStoredSecretMoved = errString("This monitor's saved credentials are only reused for the host they were saved for. Enter them again to use them with a different host, proxy or monitor type.")

// reuseStoredSecrets fills the credentials a request left blank from the
// saved monitor, as an edit that does not touch them needs. It refuses when
// the request would send them somewhere they were not saved for: the API
// never returns a credential, and pointing a monitor at another host with
// the password field left blank would otherwise deliver it there.
func reuseStoredSecrets(kind, target string, cfg *store.MonitorConfig, existing store.Monitor) error {
	if leansOnStored(*cfg, existing.Config) && !sameDestination(kind, target, *cfg, existing) {
		return errStoredSecretMoved
	}
	mergeAuthSecrets(cfg, existing.Config)
	return nil
}

// leansOnStored says whether mergeAuthSecrets would copy a saved credential
// into cfg.
func leansOnStored(cfg, saved store.MonitorConfig) bool {
	if cfg.AuthPassword == "" && saved.AuthPassword != "" && (cfg.Auth == "basic" || (cfg.Auth == "" && cfg.AuthUser != "")) {
		return true
	}
	if cfg.Auth == "bearer" && cfg.AuthToken == "" && saved.AuthToken != "" {
		return true
	}
	for name, v := range cfg.Secrets {
		if v == "" && saved.Secrets[name] != "" {
			return true
		}
	}
	return false
}

// sameDestination says whether a monitor saved as existing would still talk
// only to the hosts it talked to before, through the same proxy.
func sameDestination(kind, target string, cfg store.MonitorConfig, existing store.Monitor) bool {
	if !strings.EqualFold(kind, existing.Type) {
		return false
	}
	if strings.TrimSpace(cfg.ProxyURL) != strings.TrimSpace(existing.Config.ProxyURL) {
		return false
	}
	if strings.EqualFold(kind, "transaction") {
		had := map[string]bool{}
		for _, step := range existing.Config.Steps {
			had[origin(step.URL)] = true
		}
		for _, step := range cfg.Steps {
			if !had[origin(step.URL)] {
				return false
			}
		}
		return true
	}
	return origin(target) == origin(existing.Target)
}

// origin is the part of a target that says where a connection goes: scheme,
// host and port of a URL, or host and port of anything else. The path, the
// query and a database name can change without the credentials going
// anywhere new.
func origin(target string) string {
	t := strings.ToLower(strings.TrimSpace(target))
	if strings.Contains(t, "://") {
		if u, err := url.Parse(t); err == nil && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
		return t
	}
	if i := strings.IndexAny(t, "/?"); i >= 0 {
		t = t[:i]
	}
	return t
}
