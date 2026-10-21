package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"argus/internal/alerting"
	"argus/internal/probe"
	"argus/internal/store"
)

// optionalChannelIDs distinguishes omitted, JSON null (default set), and an explicit list.
type optionalChannelIDs struct {
	present bool
	ids     []int64
}

func (o *optionalChannelIDs) UnmarshalJSON(b []byte) error {
	o.present = true
	if string(b) == "null" {
		o.ids = nil
		return nil
	}
	return json.Unmarshal(b, &o.ids)
}

const maxHTTPBody = 8192

var jsonPathRe = regexp.MustCompile(`^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+)*$`)

func (s *Server) handleMonitorSeries(w http.ResponseWriter, r *http.Request) {
	points, err := s.store.SparkSeries(r.Context(), 80)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load series")
		return
	}
	if points == nil {
		points = []store.SeriesPoint{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"granularity": "raw", "points": points})
}

type monitorRequest struct {
	Name               string              `json:"name"`
	Type               string              `json:"type"`
	Target             string              `json:"target"`
	IntervalSeconds    int                 `json:"interval_seconds"`
	Retries            int                 `json:"retries"`
	EscalateMinutes    int                 `json:"escalate_minutes"`
	EscalateRecipients string              `json:"escalate_recipients"`
	Enabled            *bool               `json:"enabled"`
	Tags               string              `json:"tags"`
	LatencyThresholdMs int                 `json:"latency_threshold_ms"`
	ChannelIDs         optionalChannelIDs  `json:"channel_ids"`
	RealmIDs           optionalChannelIDs  `json:"realm_ids"`
	ResendMinutes      int                 `json:"resend_minutes"`
	ParentID           *int64              `json:"parent_id"`
	DependsOn          *int64              `json:"depends_on"`
	SLOTarget          float64             `json:"slo_target"`
	ProbeNodeIDs       []int64             `json:"probe_node_ids"`
	ConfirmLocations   int                 `json:"confirm_locations"`
	Config             store.MonitorConfig `json:"config"`
}

func (b *monitorRequest) validate() string {
	if strings.TrimSpace(b.Name) == "" {
		return "name is required"
	}
	if b.Type == "" {
		b.Type = "icmp"
	}
	if b.Type == "group" && strings.TrimSpace(b.Target) == "" {
		b.Target = strings.TrimSpace(b.Name)
	}
	if b.Type == "transaction" {
		// A transaction's target is its first step, never typed separately.
		b.Target = transactionTarget(b.Config)
		if b.Target == "" {
			return "a transaction needs at least one step"
		}
	}
	if strings.TrimSpace(b.Target) == "" {
		return "target is required"
	}
	switch b.Type {
	case "icmp", "http", "tcp", "dns", "cert", "domain", "smtp", "imap", "pop3",
		"postgres", "mysql", "redis", "transaction", "push", "group":
	default:
		return "unsupported monitor type"
	}
	if msg := validateTarget(b.Type, b.Target); msg != "" {
		return msg
	}
	if msg := normalizeConfig(b.Type, &b.Config); msg != "" {
		return msg
	}
	// A scheduled heartbeat is judged against its cron times; the interval is
	// only how often that is looked at.
	if b.Type == "push" && b.Config.Schedule != "" {
		b.IntervalSeconds = 60
	}
	if b.IntervalSeconds <= 0 {
		b.IntervalSeconds = 60
	}
	if b.IntervalSeconds < 10 {
		return "interval must be at least 10 seconds"
	}
	if b.IntervalSeconds > 86400 {
		return "interval must be at most 86400 seconds"
	}
	if b.Retries <= 0 {
		b.Retries = 3
	}
	if b.Retries > 10 {
		return "retries must be at most 10"
	}
	if b.EscalateMinutes < 0 {
		b.EscalateMinutes = 0
	}
	if b.EscalateMinutes > 10080 {
		return "escalation delay must be at most 10080 minutes"
	}
	if b.ResendMinutes < 0 {
		b.ResendMinutes = 0
	}
	if b.ResendMinutes > 10080 {
		return "resend interval must be at most 10080 minutes"
	}
	if b.LatencyThresholdMs < 0 {
		return "latency_threshold_ms must be 0 (off) or a positive number of milliseconds"
	}
	if b.LatencyThresholdMs > 600000 {
		return "latency_threshold_ms must be at most 600000"
	}
	if b.SLOTarget == 0 {
		b.SLOTarget = 99.9
	}
	if b.SLOTarget < 50 || b.SLOTarget > 100 {
		return "slo_target must be 50-100"
	}
	if b.ProbeNodeIDs == nil {
		b.ProbeNodeIDs = []int64{}
	}
	// A heartbeat arrives here and a group has no check, so neither runs
	// anywhere else.
	if b.Type == "push" || b.Type == "group" {
		b.ProbeNodeIDs = []int64{}
		b.ConfirmLocations = 1
	}
	if b.ConfirmLocations == 0 {
		b.ConfirmLocations = 1
	}
	locations := len(store.Locations(store.Monitor{Type: b.Type, ProbeNodeIDs: b.ProbeNodeIDs}))
	if b.ConfirmLocations < 1 || b.ConfirmLocations > locations {
		if locations == 1 {
			return "confirm_locations must be 1 for a monitor checked from one location"
		}
		return fmt.Sprintf("confirm_locations must be between 1 and %d, the number of locations this monitor is checked from", locations)
	}
	if b.Enabled == nil {
		t := true
		b.Enabled = &t
	}
	return ""
}

func validateTarget(kind, target string) string {
	t := strings.TrimSpace(target)
	switch kind {
	case "http":
		u, err := url.Parse(t)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "target must be an http or https URL"
		}
	case "cert":
		if strings.Contains(t, "://") {
			u, err := url.Parse(t)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return "cert target must be an https URL or host:port"
			}
			break
		}
		host, port, err := net.SplitHostPort(t)
		if err != nil || host == "" || port == "" {
			if strings.Contains(t, ":") {
				return "cert target must be an https URL or host:port"
			}
			break
		}
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return "target port must be 1-65535"
		}
	case "tcp":
		host, port, err := net.SplitHostPort(t)
		if err != nil || host == "" || port == "" {
			return "target must be host:port"
		}
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return "target port must be 1-65535"
		}
	case "dns":
		if strings.Contains(t, "://") || strings.Contains(t, " ") {
			return "target must be a hostname"
		}
	case "smtp", "imap", "pop3", "postgres", "mysql", "redis":
		host, port, err := net.SplitHostPort(t)
		if err != nil {
			// A bare host is fine; the protocol's default port is used.
			if strings.Contains(t, ":") || strings.Contains(t, "/") || strings.Contains(t, " ") {
				return "target must be a host or host:port"
			}
			break
		}
		if host == "" {
			return "target must be a host or host:port"
		}
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return "target port must be 1-65535"
		}
	case "domain":
		name := probe.DomainName(t)
		if name == "" || !strings.Contains(name, ".") || strings.Contains(name, " ") {
			return "target must be a domain name, for example example.com"
		}
	}
	return ""
}

func normalizeConfig(kind string, cfg *store.MonitorConfig) string {
	if cfg.TimeoutSeconds < 0 {
		return "timeout must be at least 0"
	}
	if cfg.TimeoutSeconds > 120 {
		return "timeout must be at most 120 seconds"
	}
	switch kind {
	case "http":
		return normalizeHTTPConfig(cfg)
	case "tcp":
		*cfg = store.MonitorConfig{TimeoutSeconds: cfg.TimeoutSeconds}
	case "dns":
		rec := strings.ToUpper(strings.TrimSpace(cfg.Record))
		if rec == "" {
			rec = "A"
		}
		if !probe.ValidDNSRecord(rec) {
			return "dns record must be one of " + strings.Join(probe.DNSRecordTypes, ", ")
		}
		expectIP := strings.TrimSpace(cfg.ExpectIP)
		expectValue := strings.TrimSpace(cfg.ExpectValue)
		if rec == "A" || rec == "AAAA" {
			if expectValue != "" && expectIP == "" {
				expectIP, expectValue = expectValue, ""
			}
			if expectIP != "" && net.ParseIP(expectIP) == nil {
				return "expect_ip must be a valid IP"
			}
		} else {
			if expectIP != "" && expectValue == "" {
				expectValue, expectIP = expectIP, ""
			}
			if len(expectValue) > 512 {
				return "expected value must be at most 512 characters"
			}
		}
		*cfg = store.MonitorConfig{
			Record: rec, ExpectIP: expectIP, ExpectValue: expectValue,
			TimeoutSeconds: cfg.TimeoutSeconds,
		}
	case "cert":
		if msg := checkWarnDays(cfg.WarnDays); msg != "" {
			return msg
		}
		*cfg = store.MonitorConfig{TimeoutSeconds: cfg.TimeoutSeconds, InsecureSkipVerify: cfg.InsecureSkipVerify, WarnDays: cfg.WarnDays}
	case "domain":
		if msg := checkWarnDays(cfg.WarnDays); msg != "" {
			return msg
		}
		*cfg = store.MonitorConfig{TimeoutSeconds: cfg.TimeoutSeconds, WarnDays: cfg.WarnDays}
	case "postgres", "mysql", "redis":
		if len(cfg.Query) > 500 {
			return "query must be at most 500 characters"
		}
		if len(cfg.Database) > 128 || len(cfg.AuthUser) > 128 {
			return "database and user must be at most 128 characters"
		}
		if len(cfg.ExpectValue) > 200 {
			return "expected value must be at most 200 characters"
		}
		mode := strings.ToLower(strings.TrimSpace(cfg.TLSMode))
		if kind == "redis" {
			if mode != "" && mode != probe.MailTLSNone && mode != probe.MailTLSImplicit {
				return "redis tls_mode must be none or tls"
			}
			if !probe.ValidRedisCommand(cfg.Query) {
				return "redis query must be a read command (PING, GET, EXISTS, INFO, DBSIZE, …)"
			}
		}
		*cfg = store.MonitorConfig{
			TimeoutSeconds: cfg.TimeoutSeconds,
			AuthUser:       strings.TrimSpace(cfg.AuthUser),
			AuthPassword:   cfg.AuthPassword,
			Database:       strings.TrimSpace(cfg.Database),
			Query:          strings.TrimSpace(cfg.Query),
			ExpectValue:    strings.TrimSpace(cfg.ExpectValue),
			TLSMode:        mode,
			// For databases this means "do not require TLS", which is the
			// common case on a private network.
			InsecureSkipVerify: cfg.InsecureSkipVerify,
		}
	case "smtp", "imap", "pop3":
		mode := strings.ToLower(strings.TrimSpace(cfg.TLSMode))
		if mode == "" {
			mode = probe.MailTLSNone
		}
		switch mode {
		case probe.MailTLSNone, probe.MailTLSStartTLS, probe.MailTLSImplicit:
		default:
			return "tls_mode must be none, starttls, or tls"
		}
		if len(cfg.Keyword) > 200 {
			return "keyword must be at most 200 characters"
		}
		*cfg = store.MonitorConfig{
			TimeoutSeconds: cfg.TimeoutSeconds, TLSMode: mode,
			Keyword: strings.TrimSpace(cfg.Keyword), KeywordInvert: cfg.KeywordInvert,
			InsecureSkipVerify: cfg.InsecureSkipVerify,
		}
	case "transaction":
		return normalizeTransactionConfig(cfg)
	case "push":
		if cfg.GraceSeconds < 0 {
			return "grace_seconds must be at least 0"
		}
		if cfg.GraceSeconds > 86400 {
			return "grace_seconds must be at most 86400"
		}
		sched := strings.Join(strings.Fields(cfg.Schedule), " ")
		tz := strings.TrimSpace(cfg.Timezone)
		if sched != "" {
			if len(sched) > 100 {
				return "schedule must be at most 100 characters"
			}
			if _, err := probe.ParseSchedule(sched, tz); err != nil {
				return err.Error()
			}
		} else {
			tz = ""
		}
		*cfg = store.MonitorConfig{PushToken: strings.TrimSpace(cfg.PushToken), GraceSeconds: cfg.GraceSeconds,
			Schedule: sched, Timezone: tz}
	case "group":
		*cfg = store.MonitorConfig{}
	default:
		*cfg = store.MonitorConfig{}
	}
	return ""
}

func checkWarnDays(days int) string {
	if days < 0 || days > 365 {
		return "warn_days must be between 1 and 365"
	}
	return ""
}

func normalizeHTTPConfig(cfg *store.MonitorConfig) string {
	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = "GET"
	}
	if method != "GET" && method != "HEAD" && method != "POST" {
		return "http method must be GET, HEAD, or POST"
	}
	cfg.Method = method
	if cfg.ExpectStatus < 0 || cfg.ExpectStatus > 599 {
		return "expect_status must be 0 (any 2xx/3xx) or 100-599"
	}
	if len(cfg.Headers) > 20 {
		return "at most 20 headers"
	}

	if method != "POST" {
		if strings.TrimSpace(cfg.Body) != "" {
			return "body is only allowed on POST"
		}
		cfg.Body = ""
	} else if len(cfg.Body) > maxHTTPBody {
		return "body must be at most 8192 bytes"
	}

	path := strings.TrimSpace(cfg.JSONPath)
	if method == "HEAD" && path != "" {
		return "json_path is not allowed on HEAD"
	}
	if path != "" {
		if !jsonPathRe.MatchString(path) {
			return "json_path must be dotted identifiers"
		}
		cfg.JSONPath = path
	} else {
		cfg.JSONPath = ""
		cfg.JSONExpect = ""
	}

	auth := strings.ToLower(strings.TrimSpace(cfg.Auth))
	switch auth {
	case "", "none":
		cfg.Auth = ""
		cfg.AuthUser = ""
		cfg.AuthPassword = ""
		cfg.AuthToken = ""
	case "basic":
		if strings.TrimSpace(cfg.AuthUser) == "" {
			return "auth_user is required for basic auth"
		}
		cfg.Auth = "basic"
		cfg.AuthToken = ""
	case "bearer":
		if strings.TrimSpace(cfg.AuthToken) == "" {
			return "auth_token is required for bearer auth"
		}
		cfg.Auth = "bearer"
		cfg.AuthUser = ""
		cfg.AuthPassword = ""
	default:
		return "auth must be basic or bearer"
	}
	if cfg.Auth != "" {
		for k := range cfg.Headers {
			if strings.EqualFold(strings.TrimSpace(k), "Authorization") {
				return "Authorization header cannot be set when auth is configured"
			}
		}
	}

	cfg.Record = ""
	cfg.ExpectIP = ""
	cfg.PushToken = ""
	cfg.GraceSeconds = 0

	proxy := strings.TrimSpace(cfg.ProxyURL)
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "proxy_url must be an http or https URL"
		}
		cfg.ProxyURL = proxy
	}
	return ""
}

func (s *Server) handleListMonitors(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.store.ListMonitors(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list monitors")
		return
	}
	if monitors == nil {
		monitors = []store.MonitorWithState{}
	}
	if err := s.store.AttachRealmIDs(r.Context(), monitors); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list monitors")
		return
	}
	for i := range monitors {
		redactMonitorWithState(&monitors[i])
		setNextPing(&monitors[i])
	}
	writeJSON(w, http.StatusOK, monitors)
}

func (s *Server) handleCreateMonitor(w http.ResponseWriter, r *http.Request) {
	var body monitorRequest
	if err := decodeJSON(r, &body, 32768); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if msg := body.validate(); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	m := store.Monitor{
		Name:               strings.TrimSpace(body.Name),
		Type:               body.Type,
		Target:             strings.TrimSpace(body.Target),
		IntervalSeconds:    body.IntervalSeconds,
		Retries:            body.Retries,
		EscalateMinutes:    body.EscalateMinutes,
		EscalateRecipients: strings.TrimSpace(body.EscalateRecipients),
		Enabled:            *body.Enabled,
		Tags:               strings.TrimSpace(body.Tags),
		LatencyThresholdMs: body.LatencyThresholdMs,
		ResendMinutes:      body.ResendMinutes,
		ParentID:           nilIfZero(body.ParentID),
		DependsOn:          nilIfZero(body.DependsOn),
		SLOTarget:          body.SLOTarget,
		ProbeNodeIDs:       body.ProbeNodeIDs,
		ConfirmLocations:   body.ConfirmLocations,
		Config:             body.Config,
	}
	if body.Type == "push" {
		if err := ensurePushToken(&m.Config); err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to generate push token")
			return
		}
	}
	if err := s.store.ValidateMonitorRefs(r.Context(), 0, m.ParentID, m.DependsOn); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.ChannelIDs.present {
		if err := s.store.ValidateChannelIDs(r.Context(), body.ChannelIDs.ids); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		m.ChannelIDs = body.ChannelIDs.ids
	}
	if err := s.applyProbeNodes(r, &m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.CreateMonitor(r.Context(), &m); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create monitor")
		return
	}
	if body.RealmIDs.present {
		if err := s.store.SetMonitorRealms(r.Context(), m.ID, body.RealmIDs.ids); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	ids, err := s.store.LoadRealmIDs(r.Context(), m.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create monitor")
		return
	}
	m.RealmIDs = ids
	redactMonitor(&m)
	s.enqueueCheckNow(r.Context(), m.ID, m.Type)
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleGetMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := monitorIDFromURL(w, r)
	if !ok {
		return
	}
	m, err := s.store.GetMonitor(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load monitor")
		return
	}
	redactMonitorWithState(m)
	setNextPing(m)
	ids, err := s.store.LoadRealmIDs(r.Context(), m.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load monitor")
		return
	}
	m.RealmIDs = ids
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleUpdateMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := monitorIDFromURL(w, r)
	if !ok {
		return
	}
	existing, err := s.store.GetMonitor(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load monitor")
		return
	}
	var body monitorRequest
	if err := decodeJSON(r, &body, 32768); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := reuseStoredSecrets(body.Type, body.Target, &body.Config, existing.Monitor); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if msg := body.validate(); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	m := store.Monitor{
		ID:                 existing.ID,
		CreatedAt:          existing.CreatedAt,
		Name:               strings.TrimSpace(body.Name),
		Type:               body.Type,
		Target:             strings.TrimSpace(body.Target),
		IntervalSeconds:    body.IntervalSeconds,
		Retries:            body.Retries,
		EscalateMinutes:    body.EscalateMinutes,
		EscalateRecipients: strings.TrimSpace(body.EscalateRecipients),
		Enabled:            *body.Enabled,
		Tags:               strings.TrimSpace(body.Tags),
		LatencyThresholdMs: body.LatencyThresholdMs,
		ResendMinutes:      body.ResendMinutes,
		ParentID:           nilIfZero(body.ParentID),
		DependsOn:          nilIfZero(body.DependsOn),
		ChannelIDs:         existing.ChannelIDs,
		SLOTarget:          body.SLOTarget,
		ProbeNodeIDs:       body.ProbeNodeIDs,
		ConfirmLocations:   body.ConfirmLocations,
		Config:             body.Config,
	}
	if body.Type == "push" {
		if err := ensurePushToken(&m.Config); err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to generate push token")
			return
		}
	}
	if err := s.store.ValidateMonitorRefs(r.Context(), existing.ID, m.ParentID, m.DependsOn); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.ChannelIDs.present {
		if err := s.store.ValidateChannelIDs(r.Context(), body.ChannelIDs.ids); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		m.ChannelIDs = body.ChannelIDs.ids
	}
	if err := s.applyProbeNodes(r, &m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.UpdateMonitor(r.Context(), &m); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to update monitor")
		return
	}
	if body.RealmIDs.present {
		if err := s.store.SetMonitorRealms(r.Context(), m.ID, body.RealmIDs.ids); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	ids, err := s.store.LoadRealmIDs(r.Context(), m.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to update monitor")
		return
	}
	m.RealmIDs = ids
	redactMonitor(&m)
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleDeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := monitorIDFromURL(w, r)
	if !ok {
		return
	}
	told, _ := s.store.ToldDownAmong(r.Context(), []int64{id})
	if err := s.store.DeleteMonitor(r.Context(), id); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to delete monitor")
		return
	}
	s.closeDeleted(told)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// closeDeleted sends a recovery for monitors that were deleted while their
// recipients had been told they were down. Nothing will ever report them
// back up, and the alert would stay open on a pager for good.
func (s *Server) closeDeleted(told []store.Monitor) {
	for _, m := range told {
		s.dispatch.DispatchAsync(alerting.Payload{
			Monitor: m, Type: "recovery", At: time.Now().UTC(),
			Detail: "the monitor was deleted while it was down; this closes its alert",
		})
	}
}

func (s *Server) handleBulkMonitors(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs       []int64 `json:"ids"`
		Action    string  `json:"action"`
		Tag       string  `json:"tag"`
		RealmID   int64   `json:"realm_id"`
		ChannelID int64   `json:"channel_id"`
	}
	if err := decodeJSON(r, &body, 65536); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	var (
		n   int64
		err error
	)
	switch strings.ToLower(strings.TrimSpace(body.Action)) {
	case "pause":
		n, err = s.store.BulkSetEnabled(r.Context(), body.IDs, false)
	case "resume":
		n, err = s.store.BulkSetEnabled(r.Context(), body.IDs, true)
	case "delete":
		told, _ := s.store.ToldDownAmong(r.Context(), body.IDs)
		if n, err = s.store.BulkDelete(r.Context(), body.IDs); err == nil {
			s.closeDeleted(told)
		}
	case "add_tag":
		n, err = s.store.BulkAddTag(r.Context(), body.IDs, body.Tag)
	case "add_realm":
		n, err = s.store.BulkAddRealm(r.Context(), body.IDs, body.RealmID)
	case "attach_channel":
		n, err = s.store.BulkAttachChannel(r.Context(), body.IDs, body.ChannelID)
	default:
		writeErr(w, http.StatusBadRequest, "action must be pause, resume, delete, add_tag, add_realm, or attach_channel")
		return
	}
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "required") || strings.Contains(msg, "at most") ||
			strings.Contains(msg, "cannot") || strings.Contains(msg, "unknown") ||
			strings.Contains(msg, "invalid") {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to apply bulk action")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": n})
}

func (s *Server) handlePauseMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := monitorIDFromURL(w, r)
	if !ok {
		return
	}
	m, err := s.store.GetMonitor(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load monitor")
		return
	}
	newEnabled := !m.Enabled
	if err := s.store.SetMonitorEnabled(r.Context(), id, newEnabled); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to toggle monitor")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": newEnabled})
}

func (s *Server) enqueueCheckNow(ctx context.Context, id int64, kind string) {
	if strings.EqualFold(kind, "group") {
		return
	}
	reqID, err := s.store.EnqueueCheckNow(ctx, id)
	if err != nil {
		return
	}
	_ = s.store.NotifyCheckNow(ctx, reqID)
}

func (s *Server) writeMonitor(w http.ResponseWriter, r *http.Request, id int64) {
	m, err := s.store.GetMonitor(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load monitor")
		return
	}
	ids, err := s.store.LoadRealmIDs(r.Context(), m.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load monitor")
		return
	}
	m.RealmIDs = ids
	redactMonitorWithState(m)
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleCheckNow(w http.ResponseWriter, r *http.Request) {
	id, ok := monitorIDFromURL(w, r)
	if !ok {
		return
	}
	m, err := s.store.GetMonitor(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load monitor")
		return
	}
	if strings.EqualFold(m.Type, "group") {
		writeErr(w, http.StatusBadRequest, "group is not probed")
		return
	}
	hb, _ := s.store.GetProbeHeartbeat(r.Context())
	if store.ProbeStale(hb, time.Now(), probeStaleAfter) {
		writeErr(w, http.StatusServiceUnavailable, "probe is not running")
		return
	}
	reqID, err := s.store.EnqueueCheckNow(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to queue check")
		return
	}
	_ = s.store.NotifyCheckNow(r.Context(), reqID)

	to := m.Config.TimeoutSeconds
	if to <= 0 {
		to = 10
	}
	wait := time.Duration(to+5) * time.Second
	if wait < 15*time.Second {
		wait = 15 * time.Second
	}
	if wait > 90*time.Second {
		wait = 90 * time.Second
	}
	deadline := time.Now().Add(wait)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		rec, err := s.store.GetCheckNow(r.Context(), reqID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to read check result")
			return
		}
		if rec != nil && (rec.Status == "done" || rec.Status == "failed") {
			success := rec.Success != nil && *rec.Success
			writeJSON(w, http.StatusOK, map[string]any{
				"success":    success,
				"latency_ms": rec.LatencyMs,
				"error":      rec.Error,
				"evidence":   rec.Evidence,
				"applied":    m.Enabled && store.AssignedToNode(m.Monitor, store.LocalProbeNodeID),
			})
			return
		}
		if time.Now().After(deadline) {
			writeErr(w, http.StatusGatewayTimeout, "probe did not run the check in time")
			return
		}
		select {
		case <-r.Context().Done():
			writeErr(w, http.StatusGatewayTimeout, "check cancelled")
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) handleMuteMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := monitorIDFromURL(w, r)
	if !ok {
		return
	}
	var body struct {
		Minutes int `json:"minutes"`
	}
	if err := decodeJSON(r, &body, 1024); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	until, err := store.MuteUntil(time.Now().UTC(), body.Minutes)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.MuteMonitor(r.Context(), id, until); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to mute monitor")
		return
	}
	detail := "unmuted"
	if until != nil {
		if body.Minutes == -1 {
			detail = "until unmute"
		} else {
			detail = fmt.Sprintf("%d minutes", body.Minutes)
		}
	}
	etype := "muted"
	if until == nil {
		etype = "unmuted"
	}
	_ = s.store.InsertEvent(r.Context(), id, etype, detail)
	s.writeMonitor(w, r, id)
}

func (s *Server) handleAckMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := monitorIDFromURL(w, r)
	if !ok {
		return
	}
	m, err := s.store.GetMonitor(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load monitor")
		return
	}
	if m.State.Status != "down" {
		writeErr(w, http.StatusBadRequest, "monitor is not down")
		return
	}
	var body struct {
		Minutes int `json:"minutes"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &body, 1024); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid request")
			return
		}
	}
	until, err := store.AckUntil(time.Now().UTC(), body.Minutes)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.AckMonitor(r.Context(), id, until); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusBadRequest, "monitor is not down")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to acknowledge monitor")
		return
	}
	detail := "until next down"
	if until != nil {
		detail = fmt.Sprintf("%d minutes", body.Minutes)
	}
	_ = s.store.InsertEvent(r.Context(), id, "acked", detail)
	s.writeMonitor(w, r, id)
}

func (s *Server) handleCloneMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := monitorIDFromURL(w, r)
	if !ok {
		return
	}
	src, err := s.store.GetMonitor(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load monitor")
		return
	}
	realmIDs, err := s.store.LoadRealmIDs(r.Context(), src.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to clone monitor")
		return
	}
	dst := src.Monitor
	dst.ID = 0
	dst.Name = strings.TrimSpace(src.Name) + " copy"
	dst.Enabled = true
	dst.CreatedAt = time.Time{}
	if dst.Type == "push" {
		dst.Config.PushToken = ""
		if err := ensurePushToken(&dst.Config); err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to generate push token")
			return
		}
	}
	if err := s.store.CreateMonitor(r.Context(), &dst); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to clone monitor")
		return
	}
	if err := s.store.SetMonitorRealms(r.Context(), dst.ID, realmIDs); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to clone monitor")
		return
	}
	s.enqueueCheckNow(r.Context(), dst.ID, dst.Type)
	created, err := s.store.GetMonitor(r.Context(), dst.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to clone monitor")
		return
	}
	created.RealmIDs = realmIDs
	redactMonitorWithState(created)
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	id, ok := monitorIDFromURL(w, r)
	if !ok {
		return
	}
	rangeParam := r.URL.Query().Get("range")
	now := time.Now().UTC()
	var from time.Time
	var granularity string
	var hours int

	switch rangeParam {
	case "1h":
		granularity, hours = "raw", 1
	case "6h":
		granularity, hours = "raw", 6
	case "24h", "":
		granularity, hours = "raw", 24
	case "7d":
		granularity, hours = "hour", 24*7
	case "30d":
		granularity, hours = "hour", 24*30
	case "90d":
		granularity, hours = "day", 24*90
	case "365d":
		granularity, hours = "day", 24*365
	default:
		writeErr(w, http.StatusBadRequest, "invalid range")
		return
	}
	from = now.Add(-time.Duration(hours) * time.Hour)

	if granularity == "raw" {
		points, err := s.store.ResultsRaw(r.Context(), id, from)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to load results")
			return
		}
		if points == nil {
			points = []store.RawPoint{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"granularity": granularity, "points": points})
		return
	}

	table := "rollup_hourly"
	if granularity == "day" {
		table = "rollup_daily"
	}
	points, err := s.store.Rollups(r.Context(), table, id, from)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load rollups")
		return
	}
	if points == nil {
		points = []store.RollupPoint{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"granularity": granularity, "points": points})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := monitorIDFromURL(w, r)
	if !ok {
		return
	}
	events, err := s.store.ListEvents(r.Context(), id, 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load events")
		return
	}
	if events == nil {
		events = []store.Event{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleMonitorAlerts(w http.ResponseWriter, r *http.Request) {
	id, ok := monitorIDFromURL(w, r)
	if !ok {
		return
	}
	alerts, err := s.store.ListAlerts(r.Context(), &id, 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load alerts")
		return
	}
	if alerts == nil {
		alerts = []store.Alert{}
	}
	writeJSON(w, http.StatusOK, alerts)
}

func (s *Server) handleMonitorPercentiles(w http.ResponseWriter, r *http.Request) {
	id, ok := monitorIDFromURL(w, r)
	if !ok {
		return
	}
	hours := 24
	if r.URL.Query().Get("range") == "7d" {
		hours = 24 * 7
	}
	from := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	p, err := s.store.LatencyPercentiles(r.Context(), id, from)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load percentiles")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func nilIfZero(v *int64) *int64 {
	if v == nil || *v <= 0 {
		return nil
	}
	return v
}

// applyProbeNodes checks the probe nodes a monitor is being saved with.
func (s *Server) applyProbeNodes(r *http.Request, m *store.Monitor) error {
	if len(m.ProbeNodeIDs) == 0 {
		return nil
	}
	return s.store.ValidateProbeNodeIDs(r.Context(), m.ProbeNodeIDs)
}

type errString string

func (e errString) Error() string { return string(e) }

// setNextPing says when a scheduled heartbeat is next due.
func setNextPing(m *store.MonitorWithState) {
	if m.Type != "push" || m.Config.Schedule == "" {
		return
	}
	if due, err := probe.NextExpectedPing(m.Monitor); err == nil {
		m.State.NextPingAt = &due
	}
}
