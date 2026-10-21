package store

import (
	"regexp"
	"strings"
)

var (
	ipv4Port = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}:\d+\b`)
	ipv6Port = regexp.MustCompile(`\[[0-9a-fA-F:]+\]:\d+`)
	hostPort = regexp.MustCompile(`\b[A-Za-z0-9._-]+\.[A-Za-z0-9.-]+:\d+\b`)
)

// PublicEventDetail maps a stored probe error into a reason safe to show on
// an unauthenticated status page. Authenticated event logs keep the raw detail.
func PublicEventDetail(detail string) string {
	d := strings.TrimSpace(detail)
	if d == "" {
		return ""
	}
	lower := strings.ToLower(d)
	if strings.HasPrefix(lower, "latency ") || strings.Contains(lower, "certificate exp") {
		return stripAddrs(d)
	}
	switch {
	case strings.Contains(lower, "connection refused"):
		return "Connection refused"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		return "Check timed out"
	case strings.Contains(lower, "tls") || strings.Contains(lower, "certificate") || strings.Contains(lower, "handshake"):
		return "TLS handshake failed"
	default:
		return "Check failed"
	}
}

func stripAddrs(s string) string {
	s = ipv6Port.ReplaceAllString(s, "[redacted]")
	s = ipv4Port.ReplaceAllString(s, "[redacted]")
	s = hostPort.ReplaceAllString(s, "[redacted]")
	return s
}
