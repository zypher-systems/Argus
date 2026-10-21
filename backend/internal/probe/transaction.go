package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"argus/internal/store"
)

// MaxTransactionSteps keeps a transaction a check rather than a load test.
const MaxTransactionSteps = 10

// TransactionMethods are the methods a step may use.
var TransactionMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"}

// templateRef matches {{name}} and {{secret.name}}, with optional spaces.
var templateRef = regexp.MustCompile(`\{\{\s*(secret\.)?([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// validName is what an extracted variable or a secret may be called.
var validName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidVariableName reports whether name can be used as {{name}}.
func ValidVariableName(name string) bool { return validName.MatchString(name) }

// TemplateRefs lists the variables and secrets a string refers to.
func TemplateRefs(s string) (vars, secrets []string) {
	for _, m := range templateRef.FindAllStringSubmatch(s, -1) {
		if m[1] != "" {
			secrets = append(secrets, m[2])
		} else {
			vars = append(vars, m[2])
		}
	}
	return vars, secrets
}

// BlankRefs replaces every reference with a plain token, so the text around
// them can be validated; {{secret.host}} is still a hostname at run time.
func BlankRefs(s string) string { return templateRef.ReplaceAllString(s, "ref") }

// StepRefs lists every variable and secret a step refers to.
func StepRefs(step store.TransactionStep) (vars, secrets []string) {
	parts := []string{step.URL, step.Body}
	for _, v := range step.Headers {
		parts = append(parts, v)
	}
	for _, p := range parts {
		v, s := TemplateRefs(p)
		vars = append(vars, v...)
		secrets = append(secrets, s...)
	}
	return vars, secrets
}

// render substitutes references. An unknown one is an error rather than
// being sent literally: a request carrying "{{token}}" would fail in a way
// that points at the server instead of at the monitor.
func render(s string, vars, secrets map[string]string) (string, error) {
	var missing error
	out := templateRef.ReplaceAllStringFunc(s, func(ref string) string {
		m := templateRef.FindStringSubmatch(ref)
		if m[1] != "" {
			if v, ok := secrets[m[2]]; ok {
				return v
			}
			if missing == nil {
				missing = fmt.Errorf("secret %q is not set", m[2])
			}
			return ref
		}
		if v, ok := vars[m[2]]; ok {
			return v
		}
		if missing == nil {
			missing = fmt.Errorf("{{%s}} has not been extracted by an earlier step", m[2])
		}
		return ref
	})
	return out, missing
}

// redactor hides secret and extracted values in anything kept as evidence.
// Extracted values are usually session tokens, which are as sensitive as the
// password that obtained them.
type redactor struct{ values []string }

func (r *redactor) add(v string) {
	// Very short values would blank out ordinary text for no benefit.
	if len(v) < 4 {
		return
	}
	r.values = append(r.values, v, url.QueryEscape(v), url.PathEscape(v))
	// Longest first, so a value containing another is hidden whole.
	sort.Slice(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
}

func (r *redactor) apply(s string) string {
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, "[redacted]")
	}
	return s
}

func stepLabel(i int, step store.TransactionStep) string {
	if name := strings.TrimSpace(step.Name); name != "" {
		return fmt.Sprintf("step %d (%s)", i+1, name)
	}
	return fmt.Sprintf("step %d", i+1)
}

// defaultContentType guesses for a body the operator did not label.
func defaultContentType(body string) string {
	trimmed := strings.TrimSpace(body)
	switch {
	case json.Valid([]byte(trimmed)):
		return "application/json"
	case strings.Contains(trimmed, "=") && !strings.ContainsAny(trimmed, " \n"):
		return "application/x-www-form-urlencoded"
	default:
		return "text/plain; charset=utf-8"
	}
}

func hasHeader(h map[string]string, name string) bool {
	for k := range h {
		if strings.EqualFold(strings.TrimSpace(k), name) {
			return true
		}
	}
	return false
}

// checkTransaction runs each step in order with one cookie jar, so a login
// step's session carries into the steps after it. The first failing step
// ends the run and names itself in the error.
func checkTransaction(ctx context.Context, m store.Monitor) (CheckOutcome, error) {
	timeout := time.Duration(timeoutSeconds(m, 30)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var out CheckOutcome
	steps := m.Config.Steps
	if len(steps) == 0 {
		return out, errors.New("transaction has no steps")
	}
	if len(steps) > MaxTransactionSteps {
		return out, fmt.Errorf("transaction has more than %d steps", MaxTransactionSteps)
	}

	jar, _ := cookiejar.New(nil)
	client := httpClientFor(m, timeout)
	client.Jar = jar

	red := &redactor{}
	for _, v := range m.Config.Secrets {
		red.add(v)
	}
	vars := map[string]string{}

	start := time.Now()
	err := func() error {
		for i, step := range steps {
			if err := runStep(ctx, client, i, step, vars, m.Config.Secrets, red, &out); err != nil {
				return err
			}
		}
		return nil
	}()
	out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000

	out.Evidence.Curl = red.apply(out.Evidence.Curl)
	out.Evidence.BodySnippet = red.apply(out.Evidence.BodySnippet)
	for i := range out.Evidence.Steps {
		out.Evidence.Steps[i].Error = red.apply(out.Evidence.Steps[i].Error)
	}
	if err != nil {
		return out, errors.New(red.apply(err.Error()))
	}
	return out, nil
}

func runStep(ctx context.Context, client *http.Client, i int, step store.TransactionStep,
	vars, secrets map[string]string, red *redactor, out *CheckOutcome) (err error) {
	label := stepLabel(i, step)
	ev := store.StepEvidence{Name: strings.TrimSpace(step.Name)}
	if ev.Name == "" {
		ev.Name = fmt.Sprintf("Step %d", i+1)
	}
	stepStart := time.Now()
	defer func() {
		ev.LatencyMs = float64(time.Since(stepStart).Microseconds()) / 1000
		if err != nil {
			ev.Error = err.Error()
			err = fmt.Errorf("%s: %w", label, err)
		}
		out.Evidence.Steps = append(out.Evidence.Steps, ev)
	}()

	method := strings.ToUpper(strings.TrimSpace(step.Method))
	if method == "" {
		method = http.MethodGet
	}
	target, err := render(step.URL, vars, secrets)
	if err != nil {
		return err
	}
	body, err := render(step.Body, vars, secrets)
	if err != nil {
		return err
	}
	var reqBody io.Reader
	if body != "" && method != http.MethodGet && method != http.MethodHead {
		reqBody = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	for k, v := range step.Headers {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		val, err := render(v, vars, secrets)
		if err != nil {
			return err
		}
		req.Header.Set(k, val)
	}
	if reqBody != nil && !hasHeader(step.Headers, "Content-Type") {
		req.Header.Set("Content-Type", defaultContentType(body))
	}
	out.Evidence.Curl = httpCurl(req)

	resp, err := client.Do(req)
	if err != nil {
		out.Evidence.TLSError = classifyTLSError(err)
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if out.TLSExpiresAt == nil {
		out.TLSExpiresAt = leafExpiry(resp)
	}
	ev.StatusCode = resp.StatusCode
	out.Evidence.StatusCode = resp.StatusCode
	out.Evidence.ContentType = resp.Header.Get("Content-Type")

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	out.Evidence.BodySnippet = snippetBody(respBody, maxEvidenceBody)

	if !statusOK(resp.StatusCode, step.ExpectStatus) {
		if step.ExpectStatus != 0 {
			return fmt.Errorf("status %d, expected %d", resp.StatusCode, step.ExpectStatus)
		}
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if kw := strings.TrimSpace(step.Keyword); kw != "" && method != http.MethodHead {
		if !strings.Contains(string(respBody), kw) {
			return fmt.Errorf("keyword %q not found", kw)
		}
	}
	for _, x := range step.Extract {
		var val string
		switch strings.ToLower(strings.TrimSpace(x.From)) {
		case "header":
			val = resp.Header.Get(strings.TrimSpace(x.Path))
			if val == "" {
				return fmt.Errorf("header %q missing, so {{%s}} could not be extracted", x.Path, x.Name)
			}
		default:
			v, err := lookupJSONPath(respBody, strings.TrimSpace(x.Path))
			if err != nil {
				return fmt.Errorf("extracting {{%s}}: %w", x.Name, err)
			}
			val = v
		}
		if val == "" {
			return fmt.Errorf("{{%s}} was extracted empty", x.Name)
		}
		vars[x.Name] = val
		red.add(val)
	}
	return nil
}
