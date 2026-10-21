package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"argus/internal/store"
)

func TestDueMonth(t *testing.T) {
	cases := []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC), "2026-09"},
		{time.Date(2026, 10, 1, 23, 0, 0, 0, time.UTC), "2026-09"},
		{time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), "2025-12"},
		{time.Date(2026, 10, 1, 7, 59, 0, 0, time.UTC), ""},
		{time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC), ""},
		{time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC), ""},
	}
	for _, c := range cases {
		if got := dueMonth(c.at); got != c.want {
			t.Errorf("dueMonth(%s) = %q, want %q", c.at.Format(time.RFC3339), got, c.want)
		}
	}
}

func TestMonthWindowIsAClosedMonth(t *testing.T) {
	from, to, err := monthWindow("2026-02")
	if err != nil {
		t.Fatal(err)
	}
	if from.Format("2006-01-02") != "2026-02-01" || to.Format("2006-01-02") != "2026-03-01" {
		t.Fatalf("window %s → %s", from, to)
	}
	if _, _, err := monthWindow("nonsense"); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestMonthlyReportEmail(t *testing.T) {
	up := func(v float64) *float64 { return &v }
	rows := []store.SLARow{
		{Name: "Checkout API", SLOTarget: 99.9, UptimePct: up(99.95), TotalChecks: 43200, FailedChecks: 21, WithinSLO: true},
		{Name: "Marketing site", SLOTarget: 99.9, UptimePct: up(98.20), TotalChecks: 43200, FailedChecks: 777, WithinSLO: false},
		{Name: "Never checked", SLOTarget: 99.9},
	}
	email := MonthlyReportEmail("2026-09", rows, "https://argus.acme.test/")
	subject, body := email.Subject, email.HTML
	if !strings.Contains(subject, "September 2026") || !strings.Contains(subject, "1 below target") {
		t.Fatalf("subject %q", subject)
	}
	for _, want := range []string{"Checkout API", "99.95%", "Marketing site", "98.20%", "777 of 43200", "https://argus.acme.test/sla"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "Never checked") {
		t.Error("a monitor with no checks should not be listed with a fake number")
	}
	if !strings.Contains(body, "lowest Marketing site at 98.20%") {
		t.Error("summary should name the worst monitor")
	}
	for _, want := range []string{"Checkout API: 99.95%", "Marketing site: 98.20% (target 99.90%, 777 of 43200 failed)  below target", "https://argus.acme.test/sla"} {
		if !strings.Contains(email.Text, want) {
			t.Errorf("text part missing %q:\n%s", want, email.Text)
		}
	}

	clean := MonthlyReportEmail("2026-09", []store.SLARow{
		{Name: "OK", SLOTarget: 99, UptimePct: up(100), WithinSLO: true},
	}, "").Subject
	if strings.Contains(clean, "below target") {
		t.Errorf("a clean month should not warn: %q", clean)
	}
}

func TestMonthlyReportEscapesMonitorNames(t *testing.T) {
	up := 99.0
	body := MonthlyReportEmail("2026-09", []store.SLARow{
		{Name: `<script>alert(1)</script>`, SLOTarget: 99, UptimePct: &up, WithinSLO: true},
	}, "").HTML
	if strings.Contains(body, "<script>") {
		t.Fatal("monitor names must be escaped into the email")
	}
}

func TestMarkMonthlyReportSentClaimsOnce(t *testing.T) {
	s := testAPI(t)
	ctx := context.Background()
	if err := s.store.SetReportSettings(ctx, store.ReportSettings{MonthlyEnabled: true, Recipients: "ops@example.com"}); err != nil {
		t.Fatal(err)
	}
	first, err := s.store.MarkMonthlyReportSent(ctx, "2026-09")
	if err != nil || !first {
		t.Fatalf("first claim %v %v", first, err)
	}
	second, err := s.store.MarkMonthlyReportSent(ctx, "2026-09")
	if err != nil || second {
		t.Fatalf("the same month must not be claimed twice: %v %v", second, err)
	}
	next, err := s.store.MarkMonthlyReportSent(ctx, "2026-10")
	if err != nil || !next {
		t.Fatalf("a new month should claim: %v %v", next, err)
	}
	cfg, err := s.store.GetReportSettings(ctx)
	if err != nil || cfg.LastSentMonth != "2026-10" || !cfg.MonthlyEnabled || cfg.Recipients != "ops@example.com" {
		t.Fatalf("claiming must not disturb the operator's settings: %+v %v", cfg, err)
	}
}

func TestMonthlyReportClaimsWhenEnabled(t *testing.T) {
	s := testAPI(t)
	ctx := context.Background()
	if err := s.store.SetReportSettings(ctx, store.ReportSettings{MonthlyEnabled: true, Recipients: "ops@example.com"}); err != nil {
		t.Fatal(err)
	}
	s.runMonthlyReport(ctx, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	cfg, _ := s.store.GetReportSettings(ctx)
	if cfg.LastSentMonth != "2026-09" {
		t.Fatalf("an enabled report should claim September, got %q", cfg.LastSentMonth)
	}
}
