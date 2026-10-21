package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"argus/internal/store"
)

func (d *Dispatcher) NotifyIncidentAsync(inc store.Incident) {
	go func() {
		defer recoverSend("incident update", inc.MonitorName)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		d.notifyIncident(ctx, inc)
	}()
}

func (d *Dispatcher) notifyIncident(ctx context.Context, inc store.Incident) {
	if inc.MonitorID == nil {
		return
	}
	subs, err := d.store.ConfirmedSubscribersForMonitor(ctx, *inc.MonitorID)
	if err != nil {
		slog.Error("load subscribers failed", "error", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	settings, err := d.store.LoadSettings(ctx)
	base := ""
	if err == nil {
		base = strings.TrimRight(strings.TrimSpace(settings.General.BaseURL), "/")
	}
	for _, sub := range subs {
		stored, err := d.store.GetStatusPage(ctx, sub.StatusPageID)
		if err != nil {
			continue
		}
		page := stored
		pageURL := pagePublicURL(base, page)
		switch sub.Kind {
		case "email":
			email := incidentEmail(inc, page.Name, pageURL, pageOrigin(base, page), sub.UnsubscribeToken)
			if err := d.mailer.SendEmail(ctx, email, []string{sub.Dest}); err != nil {
				slog.Error("subscriber email failed", "dest", sub.Dest, "error", err)
			}
		case "webhook":
			payload, err := json.Marshal(incidentHookBody(inc, page))
			if err != nil {
				continue
			}
			if err := PostJSON(ctx, sub.Dest, payload); err != nil {
				slog.Error("subscriber webhook failed", "dest", sub.Dest, "error", err)
			}
		}
	}
}

func pagePublicURL(base string, page *store.StatusPage) string {
	if page.CustomDomain != "" {
		return "https://" + page.CustomDomain
	}
	if base == "" {
		return "/status/" + page.Slug
	}
	return base + "/status/" + page.Slug
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// pageOrigin is where a status page is served from, for links that must be
// absolute; empty when Argus does not know its own address.
func pageOrigin(base string, page *store.StatusPage) string {
	if page.CustomDomain != "" {
		return "https://" + page.CustomDomain
	}
	return base
}

var incidentStatusWords = map[string]string{
	"investigating": "Investigating",
	"identified":    "Identified",
	"monitoring":    "Monitoring",
	"resolved":      "Resolved",
	"open":          "Investigating",
}

// incidentEmail is the update a status page subscriber receives. It carries
// a way out: a link, and the one-click headers mail providers now expect.
func incidentEmail(inc store.Incident, pageName, pageURL, origin, unsubToken string) Email {
	word := incidentStatusWords[inc.Status]
	if word == "" {
		word = capitalise(inc.Status)
	}
	tone := "down"
	switch {
	case inc.Status == "resolved":
		tone = "up"
	case inc.Impact == "minor" || inc.Impact == "none" || inc.Impact == "maintenance":
		tone = "warn"
	}
	msg := ""
	if len(inc.Updates) > 0 {
		msg = inc.Updates[len(inc.Updates)-1].Message
	}
	var rows []EmailRow
	if inc.MonitorName != "" {
		rows = append(rows, EmailRow{"Affects", inc.MonitorName})
	}
	if inc.Impact != "" && inc.Status != "resolved" {
		rows = append(rows, EmailRow{"Impact", capitalise(inc.Impact)})
	}
	layout := EmailLayout{
		Brand: pageName, Tone: tone, Eyebrow: word, Title: inc.Title, Lead: msg, Rows: rows,
		Footer: "You get these because you subscribed to " + pageName + ".",
	}
	if strings.HasPrefix(pageURL, "http") {
		layout.Action = &EmailAction{Label: "View status page", URL: pageURL}
	}
	e := Email{Subject: fmt.Sprintf("[%s] %s: %s", pageName, word, inc.Title)}
	if origin != "" && unsubToken != "" {
		unsubPage := origin + "/subscribe/unsubscribe?token=" + url.QueryEscape(unsubToken)
		oneClick := origin + "/api/subscribe/unsubscribe?token=" + url.QueryEscape(unsubToken)
		layout.FooterLink = &EmailAction{Label: "Unsubscribe", URL: unsubPage}
		e.Headers = map[string]string{
			"List-Unsubscribe":      "<" + oneClick + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		}
	}
	e.HTML, e.Text = RenderEmail(layout)
	return e
}

func incidentHookBody(inc store.Incident, page *store.StatusPage) map[string]any {
	event := "incident.updated"
	if inc.Status == "open" && len(inc.Updates) <= 1 {
		event = "incident.opened"
	}
	if inc.Status == "resolved" {
		event = "incident.resolved"
	}
	return map[string]any{
		"event": event,
		"page": map[string]any{
			"name": page.Name,
			"slug": page.Slug,
		},
		"incident": map[string]any{
			"id":          inc.ID,
			"title":       inc.Title,
			"status":      inc.Status,
			"impact":      inc.Impact,
			"monitor":     inc.MonitorName,
			"opened_at":   inc.OpenedAt,
			"resolved_at": inc.ResolvedAt,
		},
	}
}
