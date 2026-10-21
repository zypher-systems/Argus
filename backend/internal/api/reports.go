package api

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"argus/internal/alerting"
	"argus/internal/store"
)

// reportHour is when the monthly email goes out, UTC, on the first of the month.
const reportHour = 8

// reportLoop sends the monthly SLA email. It checks hourly, which is often
// enough for a monthly job and cheap enough to ignore.
func (s *Server) reportLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	s.runMonthlyReport(ctx, time.Now().UTC())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.runMonthlyReport(ctx, now.UTC())
		}
	}
}

// dueMonth returns the month a report is owed for at this instant, or "" when
// none is. Reports cover the calendar month that just ended.
func dueMonth(now time.Time) string {
	if now.Day() != 1 || now.Hour() < reportHour {
		return ""
	}
	return now.AddDate(0, -1, 0).Format("2006-01")
}

func monthWindow(month string) (from, to time.Time, err error) {
	start, err := time.Parse("2006-01", month)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	start = start.UTC()
	return start, start.AddDate(0, 1, 0), nil
}

func (s *Server) runMonthlyReport(ctx context.Context, now time.Time) {
	month := dueMonth(now)
	if month == "" {
		return
	}
	cfg, err := s.store.GetReportSettings(ctx)
	if err != nil || !cfg.MonthlyEnabled || cfg.LastSentMonth == month {
		return
	}
	recipients := alerting.SplitRecipients(cfg.Recipients)
	if len(recipients) == 0 {
		return
	}
	// Claim the month before sending so two api replicas cannot both send it.
	claimed, err := s.store.MarkMonthlyReportSent(ctx, month)
	if err != nil || !claimed {
		return
	}
	if err := s.sendMonthlyReport(ctx, month, recipients); err != nil {
		slog.Error("monthly SLA report failed", "month", month, "error", err)
		return
	}
	slog.Info("monthly SLA report sent", "month", month, "recipients", len(recipients))
}

func (s *Server) sendMonthlyReport(ctx context.Context, month string, recipients []string) error {
	from, to, err := monthWindow(month)
	if err != nil {
		return err
	}
	rows, err := s.store.SLAReportRange(ctx, from, to)
	if err != nil {
		return err
	}
	settings, _ := s.store.LoadSettings(ctx)
	email := MonthlyReportEmail(month, rows, settings.General.BaseURL)
	sendCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return s.mailer.SendEmail(sendCtx, email, recipients)
}

// handleReportTest sends last month's report now, so an operator can see what
// lands in the inbox without waiting for the first of the month.
func (s *Server) handleReportTest(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.GetReportSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load report settings")
		return
	}
	recipients := alerting.SplitRecipients(cfg.Recipients)
	if len(recipients) == 0 {
		writeErr(w, http.StatusBadRequest, "add report recipients first")
		return
	}
	month := time.Now().UTC().AddDate(0, -1, 0).Format("2006-01")
	if err := s.sendMonthlyReport(r.Context(), month, recipients); err != nil {
		writeErr(w, http.StatusBadGateway, "failed to send: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "month": month, "recipients": len(recipients)})
}

// MonthlyReportEmail renders the SLA summary for one month.
func MonthlyReportEmail(month string, rows []store.SLARow, baseURL string) alerting.Email {
	label := month
	if t, err := time.Parse("2006-01", month); err == nil {
		label = t.Format("January 2006")
	}

	measured := 0
	breached := 0
	worst := ""
	worstPct := 101.0
	for _, r := range rows {
		if r.UptimePct == nil {
			continue
		}
		measured++
		if !r.WithinSLO {
			breached++
		}
		if *r.UptimePct < worstPct {
			worstPct = *r.UptimePct
			worst = r.Name
		}
	}

	subject := fmt.Sprintf("[Argus] %s uptime report", label)
	if breached > 0 {
		subject = fmt.Sprintf("[Argus] %s uptime report — %d below target", label, breached)
	}

	summary := fmt.Sprintf("%d monitor%s measured", measured, plural(measured))
	tone := "up"
	switch {
	case measured == 0:
		summary = "No monitors had checks recorded in this month."
		tone = "info"
	case breached == 0:
		summary += ", and all met their target."
	default:
		tone = "warn"
		summary += fmt.Sprintf(", %d below target", breached)
		if worst != "" {
			summary += fmt.Sprintf("; lowest %s at %.2f%%", worst, worstPct)
		}
		summary += "."
	}

	var table, text strings.Builder
	if measured > 0 {
		cell := "padding:8px 0;border-bottom:1px solid #eef1f5"
		head := "padding:8px 0;border-bottom:1px solid #dde2ea;font-weight:600"
		table.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="font-size:13px;border-collapse:collapse;color:#14171c">`)
		fmt.Fprintf(&table, `<tr style="color:#5a6472;text-align:left"><th style="%s">Monitor</th><th style="%s;text-align:right">Uptime</th><th style="%s;text-align:right">Target</th><th style="%s;text-align:right">Failed</th></tr>`, head, head, head, head)
		for _, r := range rows {
			if r.UptimePct == nil {
				continue
			}
			colour := "#146c43"
			if !r.WithinSLO {
				colour = "#b42318"
			}
			fmt.Fprintf(&table, `<tr><td style="%s">%s</td><td style="%s;text-align:right;color:%s;font-weight:600">%.2f%%</td><td style="%s;text-align:right;color:#5a6472">%.2f%%</td><td style="%s;text-align:right;color:#5a6472">%d of %d</td></tr>`,
				cell, html.EscapeString(r.Name), cell, colour, *r.UptimePct, cell, r.SLOTarget, cell, r.FailedChecks, r.TotalChecks)
			mark := ""
			if !r.WithinSLO {
				mark = "  below target"
			}
			fmt.Fprintf(&text, "%s: %.2f%% (target %.2f%%, %d of %d failed)%s\n", r.Name, *r.UptimePct, r.SLOTarget, r.FailedChecks, r.TotalChecks, mark)
		}
		table.WriteString(`</table>`)
	}

	layout := alerting.EmailLayout{
		Tone:    tone,
		Eyebrow: "Uptime report",
		Title:   label,
		Lead:    summary,
		RawHTML: table.String(),
		RawText: strings.TrimRight(text.String(), "\n"),
		Footer:  "Sent once a month. Turn this off in Settings → Reports.",
	}
	if base := strings.TrimRight(strings.TrimSpace(baseURL), "/"); base != "" {
		layout.Action = &alerting.EmailAction{Label: "Open the SLA report", URL: base + "/sla"}
	}
	htmlBody, textBody := alerting.RenderEmail(layout)
	return alerting.Email{Subject: subject, HTML: htmlBody, Text: textBody}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
