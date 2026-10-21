package store

import (
	"strings"
	"testing"
)

func TestPublicEventDetail(t *testing.T) {
	raw := "dial tcp 10.0.0.5:5432: connection refused"
	got := PublicEventDetail(raw)
	if got != "Connection refused" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "10.0.0.5") || strings.Contains(got, "5432") {
		t.Fatalf("leaked target in %q", got)
	}

	if PublicEventDetail("i/o timeout") != "Check timed out" {
		t.Fatal("timeout")
	}
	if PublicEventDetail("tls: handshake failure") != "TLS handshake failed" {
		t.Fatal("tls")
	}
	if PublicEventDetail("latency 12.3ms") != "latency 12.3ms" {
		t.Fatal("latency")
	}
	cert := PublicEventDetail("certificate expires 2026-10-01 (14 days)")
	if !strings.Contains(cert, "certificate expires") {
		t.Fatalf("cert = %q", cert)
	}
	if PublicEventDetail("") != "" {
		t.Fatal("empty")
	}
	mystery := PublicEventDetail("dial tcp db.internal:5432: no route to host")
	if mystery != "Check failed" {
		t.Fatalf("got %q", mystery)
	}
	if strings.Contains(mystery, "db.internal") {
		t.Fatalf("leaked host in %q", mystery)
	}
}
