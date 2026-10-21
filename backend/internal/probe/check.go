package probe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"argus/internal/store"
)

func timeoutSeconds(m store.Monitor, fallback int) int {
	if m.Config.TimeoutSeconds > 0 {
		return m.Config.TimeoutSeconds
	}
	if fallback <= 0 {
		return 10
	}
	return fallback
}

type CheckOutcome struct {
	LatencyMs    float64
	TLSExpiresAt *time.Time
	// ExpiresAt is when the thing being watched lapses — today that is a
	// domain registration, which is not a TLS certificate.
	ExpiresAt *time.Time
	Evidence  store.CheckEvidence
	// NoLatency marks a result with nothing to time, such as a heartbeat,
	// so charts do not show it as zero milliseconds.
	NoLatency bool
}

func Check(ctx context.Context, pinger *Pinger, m store.Monitor) (CheckOutcome, error) {
	kind := strings.ToLower(m.Type)
	if kind == "" {
		kind = "icmp"
	}
	var out CheckOutcome
	var err error
	switch kind {
	case "http":
		out, err = checkHTTP(ctx, m)
	case "tcp":
		out, err = checkTCP(ctx, m)
	case "dns":
		out, err = checkDNS(ctx, m)
	case "cert":
		out, err = checkCert(ctx, m)
	case "domain":
		out, err = checkDomain(ctx, m)
	case "smtp", "imap", "pop3":
		out, err = checkMail(ctx, m)
	case "postgres", "mysql", "redis":
		out, err = checkDatabase(ctx, m)
	case "transaction":
		out, err = checkTransaction(ctx, m)
	case "push":
		out, err = checkPush(m, time.Now())
	case "group":
		err = fmt.Errorf("group is not probed")
	case "icmp":
		out, err = checkICMP(ctx, pinger, m)
	default:
		err = fmt.Errorf("unsupported monitor type %q", m.Type)
	}
	finalizeEvidence(kind, &out.Evidence, err)
	return out, err
}
