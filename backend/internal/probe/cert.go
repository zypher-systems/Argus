package probe

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"argus/internal/store"
)

func certDialHost(target string) (string, error) {
	t := strings.TrimSpace(target)
	if strings.Contains(t, "://") {
		u, err := url.Parse(t)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("target must be an https URL or host:port")
		}
		host := u.Hostname()
		port := u.Port()
		if port == "" {
			port = "443"
		}
		return net.JoinHostPort(host, port), nil
	}
	host, port, err := net.SplitHostPort(t)
	if err != nil {
		if strings.Contains(t, ":") {
			return "", err
		}
		return net.JoinHostPort(t, "443"), nil
	}
	if host == "" || port == "" {
		return "", fmt.Errorf("target must be host:port")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", fmt.Errorf("target port must be 1-65535")
	}
	return net.JoinHostPort(host, port), nil
}

func checkCert(ctx context.Context, m store.Monitor) (CheckOutcome, error) {
	addr, err := certDialHost(m.Target)
	if err != nil {
		return CheckOutcome{}, err
	}
	timeout := time.Duration(timeoutSeconds(m, 10)) * time.Second
	dialer := &net.Dialer{Timeout: timeout}
	host, _, _ := net.SplitHostPort(addr)
	cfg := &tls.Config{ServerName: host, InsecureSkipVerify: m.Config.InsecureSkipVerify, MinVersion: tls.VersionTLS12}

	start := time.Now()
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, cfg)
	out := CheckOutcome{LatencyMs: float64(time.Since(start).Microseconds()) / 1000}
	if err != nil {
		return out, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return out, fmt.Errorf("no certificate presented")
	}
	expires := state.PeerCertificates[0].NotAfter.UTC()
	out.TLSExpiresAt = &expires
	if time.Now().UTC().After(expires) {
		return out, fmt.Errorf("certificate expired %s", expires.Format("2006-01-02"))
	}
	select {
	case <-ctx.Done():
		return out, ctx.Err()
	default:
	}
	return out, nil
}
