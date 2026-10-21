package store

import (
	"testing"
	"time"
)

func TestAdaptiveHistoryWindow(t *testing.T) {
	now := time.Date(2026, 9, 8, 15, 4, 0, 0, time.UTC)
	created := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	from, days := AdaptiveHistoryWindow(created, now)
	wantFrom := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	if !from.Equal(wantFrom) {
		t.Fatalf("from = %s", from)
	}
	if days != 3 {
		t.Fatalf("days = %d want 3", days)
	}

	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	from, days = AdaptiveHistoryWindow(old, now)
	if days != 90 {
		t.Fatalf("capped days = %d", days)
	}
	if from.After(now) {
		t.Fatal("from after now")
	}

	_, days = AdaptiveHistoryWindow(time.Time{}, now)
	if days != 1 {
		t.Fatalf("zero created days = %d", days)
	}
}

func TestNormalizeStatusHost(t *testing.T) {
	if got := NormalizeStatusHost("Status.Example.com:443"); got != "status.example.com" {
		t.Fatalf("got %q", got)
	}
	if !ValidCustomDomain("status.example.com") {
		t.Fatal("valid domain rejected")
	}
	if ValidCustomDomain("http://status.example.com") {
		t.Fatal("scheme should fail after normalize? wait - Normalize strips? no, :// might split oddly")
	}
	if ValidCustomDomain("localhost") {
		t.Fatal("localhost has no dot")
	}
	if ValidCustomDomain("") {
		t.Fatal("empty")
	}
}
