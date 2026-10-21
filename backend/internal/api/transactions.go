package api

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"argus/internal/probe"
	"argus/internal/store"
)

const (
	maxStepBody     = 16 << 10
	maxStepHeaders  = 20
	maxSecrets      = 20
	maxSecretLength = 4096
)

// transactionTarget is what a transaction shows where other monitors show a
// host: the first step's URL, references and all.
func transactionTarget(cfg store.MonitorConfig) string {
	if len(cfg.Steps) == 0 {
		return ""
	}
	return strings.TrimSpace(cfg.Steps[0].URL)
}

func normalizeTransactionConfig(cfg *store.MonitorConfig) string {
	steps := cfg.Steps
	if len(steps) == 0 {
		return "a transaction needs at least one step"
	}
	if len(steps) > probe.MaxTransactionSteps {
		return fmt.Sprintf("a transaction has at most %d steps", probe.MaxTransactionSteps)
	}

	if len(cfg.Secrets) > maxSecrets {
		return fmt.Sprintf("a transaction has at most %d secrets", maxSecrets)
	}
	secrets := map[string]string{}
	for name, v := range cfg.Secrets {
		name = strings.TrimSpace(name)
		if !probe.ValidVariableName(name) {
			return fmt.Sprintf("secret name %q must be letters, digits and underscores, not starting with a digit", name)
		}
		if v == "" {
			return fmt.Sprintf("secret %q has no value", name)
		}
		if len(v) > maxSecretLength {
			return fmt.Sprintf("secret %q is longer than %d characters", name, maxSecretLength)
		}
		secrets[name] = v
	}

	extracted := map[string]bool{}
	out := make([]store.TransactionStep, 0, len(steps))
	for i, st := range steps {
		label := fmt.Sprintf("step %d", i+1)
		st.Name = strings.TrimSpace(st.Name)
		if len(st.Name) > 80 {
			return label + ": name must be at most 80 characters"
		}
		st.Method = strings.ToUpper(strings.TrimSpace(st.Method))
		if st.Method == "" {
			st.Method = http.MethodGet
		}
		if !slices.Contains(probe.TransactionMethods, st.Method) {
			return label + ": method must be one of " + strings.Join(probe.TransactionMethods, ", ")
		}
		st.URL = strings.TrimSpace(st.URL)
		u, err := url.Parse(probe.BlankRefs(st.URL))
		if st.URL == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return label + ": url must be an http or https URL"
		}
		if st.Body != "" && (st.Method == http.MethodGet || st.Method == http.MethodHead) {
			return label + ": " + st.Method + " requests cannot have a body"
		}
		if len(st.Body) > maxStepBody {
			return fmt.Sprintf("%s: body must be at most %d bytes", label, maxStepBody)
		}
		if len(st.Headers) > maxStepHeaders {
			return fmt.Sprintf("%s: at most %d headers", label, maxStepHeaders)
		}
		headers := map[string]string{}
		for k, v := range st.Headers {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			if strings.ContainsAny(k, " :\r\n") || strings.ContainsAny(v, "\r\n") {
				return fmt.Sprintf("%s: header %q is not valid", label, k)
			}
			headers[k] = v
		}
		st.Headers = headers
		if len(headers) == 0 {
			st.Headers = nil
		}
		if st.ExpectStatus != 0 && (st.ExpectStatus < 100 || st.ExpectStatus > 599) {
			return label + ": expected status must be 100-599, or 0 for any 2xx or 3xx"
		}
		st.Keyword = strings.TrimSpace(st.Keyword)
		if len(st.Keyword) > 200 {
			return label + ": keyword must be at most 200 characters"
		}

		// References resolve against what exists when this step runs.
		vars, secretRefs := probe.StepRefs(st)
		for _, v := range vars {
			if !extracted[v] {
				return fmt.Sprintf("%s: {{%s}} is not extracted by an earlier step", label, v)
			}
		}
		for _, name := range secretRefs {
			if _, ok := secrets[name]; !ok {
				return fmt.Sprintf("%s: {{secret.%s}} is not a secret on this monitor", label, name)
			}
		}

		for j, x := range st.Extract {
			x.Name = strings.TrimSpace(x.Name)
			x.From = strings.ToLower(strings.TrimSpace(x.From))
			x.Path = strings.TrimSpace(x.Path)
			if x.From == "" {
				x.From = "json"
			}
			if x.From != "json" && x.From != "header" {
				return fmt.Sprintf("%s: extract from must be json or header", label)
			}
			if !probe.ValidVariableName(x.Name) || x.Name == "secret" {
				return fmt.Sprintf("%s: extracted name %q must be letters, digits and underscores", label, x.Name)
			}
			if extracted[x.Name] {
				return fmt.Sprintf("%s: {{%s}} is already extracted by an earlier step", label, x.Name)
			}
			if x.Path == "" || len(x.Path) > 200 {
				return fmt.Sprintf("%s: extract {{%s}} needs a path of at most 200 characters", label, x.Name)
			}
			st.Extract[j] = x
			extracted[x.Name] = true
		}
		out = append(out, st)
	}

	*cfg = store.MonitorConfig{
		TimeoutSeconds:     cfg.TimeoutSeconds,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		Steps:              out,
		Secrets:            secrets,
	}
	return ""
}

// mergeTransactionSecrets fills in values the editor sent back blank. The API
// returns secret names with empty values, so an unchanged secret arrives
// empty and means "keep it". A name that is gone was deleted.
func mergeTransactionSecrets(dst *store.MonitorConfig, existing store.MonitorConfig) {
	for name, v := range dst.Secrets {
		if v == "" {
			if old, ok := existing.Secrets[name]; ok {
				dst.Secrets[name] = old
			}
		}
	}
}

// maxTestRunSeconds bounds a test run, which holds an HTTP request open.
const maxTestRunSeconds = 60

// handleTestRun runs a transaction from the editor without saving it. It
// validates exactly as a save would, and when editing it uses the stored
// secrets for any left blank, as a save would. It runs from the API server,
// not a remote probe.
func (s *Server) handleTestRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		monitorRequest
		MonitorID int64 `json:"monitor_id"`
	}
	if err := decodeJSON(r, &body, 32768); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if body.Type != "transaction" {
		writeErr(w, http.StatusBadRequest, "test runs are for transaction monitors")
		return
	}
	if u := s.currentUser(r); u != nil && !s.testRunLimit.allow(fmt.Sprintf("user:%d", u.ID)) {
		writeErr(w, http.StatusTooManyRequests, "too many test runs, try again in a minute")
		return
	}
	if body.MonitorID > 0 {
		existing, err := s.store.GetMonitor(r.Context(), body.MonitorID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "monitor not found")
			return
		}
		if err := reuseStoredSecrets(body.Type, body.Target, &body.Config, existing.Monitor); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if strings.TrimSpace(body.Name) == "" {
		body.Name = "test run"
	}
	if msg := body.validate(); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	switch t := body.Config.TimeoutSeconds; {
	case t <= 0:
		body.Config.TimeoutSeconds = 30
	case t > maxTestRunSeconds:
		body.Config.TimeoutSeconds = maxTestRunSeconds
	}
	m := store.Monitor{Name: body.Name, Type: body.Type, Target: body.Target, Config: body.Config}
	out, err := probe.Check(r.Context(), nil, m)
	res := map[string]any{
		"success":    err == nil,
		"latency_ms": out.LatencyMs,
		"error":      "",
		"evidence":   out.Evidence,
	}
	if err != nil {
		res["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, res)
}
