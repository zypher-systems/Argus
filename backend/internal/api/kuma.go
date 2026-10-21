package api

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"argus/internal/store"
)

type kumaBackup struct {
	MonitorList []kumaMonitor `json:"monitorList"`
}

type kumaMonitor struct {
	Name                string   `json:"name"`
	Type                string   `json:"type"`
	URL                 string   `json:"url"`
	Hostname            string   `json:"hostname"`
	Port                int      `json:"port"`
	Interval            int      `json:"interval"`
	MaxRetries          int      `json:"maxretries"`
	Keyword             string   `json:"keyword"`
	InvertKeyword       bool     `json:"invertKeyword"`
	Method              string   `json:"method"`
	Body                string   `json:"body"`
	Headers             string   `json:"headers"`
	AcceptedStatusCodes []string `json:"accepted_statuscodes"`
	Parent              any      `json:"parent"`
	PushToken           string   `json:"pushToken"`
	Timeout             int      `json:"timeout"`
	Active              *bool    `json:"active"`
	Description         string   `json:"description"`
	DNSResolveType      string   `json:"dns_resolve_type"`
	ID                  int64    `json:"id"`
}

type importSkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

func mapKumaMonitor(km kumaMonitor) (store.Monitor, int64, error) {
	name := strings.TrimSpace(km.Name)
	if name == "" {
		name = "imported"
	}
	interval := km.Interval
	if interval <= 0 {
		interval = 60
	}
	retries := km.MaxRetries
	if retries <= 0 {
		retries = 3
	}
	enabled := true
	if km.Active != nil {
		enabled = *km.Active
	}
	m := store.Monitor{
		Name:            name,
		IntervalSeconds: interval,
		Retries:         retries,
		Enabled:         enabled,
		Tags:            strings.TrimSpace(km.Description),
	}
	switch strings.ToLower(strings.TrimSpace(km.Type)) {
	case "http", "keyword", "json-query", "json_query":
		m.Type = "http"
		m.Target = strings.TrimSpace(km.URL)
		if m.Target == "" {
			return m, km.ID, fmt.Errorf("http monitor has no URL")
		}
		cfg := store.MonitorConfig{
			Method:         strings.ToUpper(strings.TrimSpace(km.Method)),
			Keyword:        km.Keyword,
			KeywordInvert:  km.InvertKeyword,
			Body:           km.Body,
			TimeoutSeconds: km.Timeout,
		}
		if cfg.Method == "" {
			cfg.Method = "GET"
		}
		if raw := strings.TrimSpace(km.Headers); raw != "" && raw != "{}" {
			var hdr map[string]string
			if json.Unmarshal([]byte(raw), &hdr) == nil {
				cfg.Headers = hdr
			}
		}
		m.Config = cfg
	case "ping", "tailscale-ping":
		m.Type = "icmp"
		m.Target = strings.TrimSpace(km.Hostname)
		if m.Target == "" {
			m.Target = strings.TrimSpace(km.URL)
		}
		if m.Target == "" {
			return m, km.ID, fmt.Errorf("ping monitor has no hostname")
		}
	case "port":
		m.Type = "tcp"
		host := strings.TrimSpace(km.Hostname)
		if host == "" {
			return m, km.ID, fmt.Errorf("port monitor has no hostname")
		}
		port := km.Port
		if port <= 0 {
			port = 80
		}
		m.Target = net.JoinHostPort(host, strconv.Itoa(port))
	case "dns":
		m.Type = "dns"
		m.Target = strings.TrimSpace(km.Hostname)
		if m.Target == "" {
			m.Target = strings.TrimSpace(km.URL)
		}
		if m.Target == "" {
			return m, km.ID, fmt.Errorf("dns monitor has no hostname")
		}
		rec := strings.ToUpper(strings.TrimSpace(km.DNSResolveType))
		if rec != "A" && rec != "AAAA" {
			rec = "A"
		}
		m.Config = store.MonitorConfig{Record: rec, TimeoutSeconds: km.Timeout}
	case "push":
		m.Type = "push"
		m.Target = name
		m.Config = store.MonitorConfig{PushToken: strings.TrimSpace(km.PushToken)}
	case "group":
		m.Type = "group"
		m.Target = name
	default:
		return m, km.ID, fmt.Errorf("unsupported Uptime Kuma type %q", km.Type)
	}
	return m, km.ID, nil
}

func kumaParentID(v any) *int64 {
	switch t := v.(type) {
	case float64:
		id := int64(t)
		if id > 0 {
			return &id
		}
	case json.Number:
		id, err := t.Int64()
		if err == nil && id > 0 {
			return &id
		}
	}
	return nil
}
