package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"argus/internal/alerting"
	"argus/internal/store"
)

type smtpRequest struct {
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	From             string `json:"from"`
	TLSMode          string `json:"tls_mode"`
	GlobalRecipients string `json:"global_recipients"`
}

type settingsResponse struct {
	store.AllSettings
	SMTPHasPassword bool `json:"smtp_has_password"`
	OIDCHasSecret   bool `json:"oidc_has_secret"`
	Hosted          bool `json:"hosted"`
	Registration    bool           `json:"registration"`
	ChannelCount    int            `json:"channel_count"`
}

func (s *Server) handlePublicBranding(w http.ResponseWriter, r *http.Request) {
	all, err := s.store.LoadSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	writeJSON(w, http.StatusOK, all.Branding)
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	all, err := s.store.LoadSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	resp := settingsResponse{AllSettings: all, Hosted: s.hosted(), Registration: s.registrationOpen(r)}
	if n, err := s.store.CountChannels(r.Context()); err == nil {
		resp.ChannelCount = n
	}
	resp.SMTPHasPassword = all.SMTP.Password != ""
	resp.SMTP.Password = ""
	resp.OIDCHasSecret = all.OIDC.ClientSecret != ""
	resp.OIDC.ClientSecret = ""
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SMTP         *smtpRequest             `json:"smtp"`
		Channels     *store.ChannelSettings   `json:"channels"`
		General      *store.GeneralSettings   `json:"general"`
		Retention    *store.RetentionSettings `json:"retention"`
		Branding     *store.BrandingSettings  `json:"branding"`
		OIDC         *store.OIDCSettings      `json:"oidc"`
		Reports      *store.ReportSettings    `json:"reports"`
		Registration *bool                    `json:"registration"`
	}
	if err := decodeJSON(r, &body, 65536); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if body.Registration != nil {
		if !s.hosted() {
			writeErr(w, http.StatusBadRequest, "registration is only available on hosted Argus")
			return
		}
		u := s.currentUser(r)
		if u == nil || u.Role != store.RoleAdmin {
			writeErr(w, http.StatusForbidden, "admin role required")
			return
		}
		if err := s.store.SetRegistration(r.Context(), *body.Registration); err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to save registration")
			return
		}
	}

	if body.SMTP != nil {
		smtp := body.SMTP
		switch smtp.TLSMode {
		case "none", "starttls", "ssl":
		default:
			smtp.TLSMode = "starttls"
		}
		if smtp.Port <= 0 || smtp.Port > 65535 {
			writeErr(w, http.StatusBadRequest, "invalid smtp port")
			return
		}
		if err := s.store.SetSMTPSettings(r.Context(), store.SMTPSettings{
			Host:             strings.TrimSpace(smtp.Host),
			Port:             smtp.Port,
			Username:         strings.TrimSpace(smtp.Username),
			Password:         smtp.Password,
			From:             strings.TrimSpace(smtp.From),
			TLSMode:          smtp.TLSMode,
			GlobalRecipients: strings.TrimSpace(smtp.GlobalRecipients),
		}, true); err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to save smtp settings")
			return
		}
	}

	if body.Channels != nil {
		writeErr(w, http.StatusBadRequest, "notification channels are managed at /api/channels")
		return
	}

	if body.General != nil {
		max := body.General.NotifyMaxPerHour
		if max < 0 || max > 10000 {
			writeErr(w, http.StatusBadRequest, "notify_max_per_hour must be 0-10000")
			return
		}
		if err := s.store.SetGeneralSettings(r.Context(), store.GeneralSettings{
			BaseURL:          strings.TrimSpace(body.General.BaseURL),
			NotifyMaxPerHour: max,
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to save general settings")
			return
		}
	}

	if body.Retention != nil {
		ret := *body.Retention
		if ret.RawDays < 1 || ret.RawDays > 30 {
			writeErr(w, http.StatusBadRequest, "raw retention must be 1-30 days")
			return
		}
		if ret.HourlyDays < 7 || ret.HourlyDays > 365 {
			writeErr(w, http.StatusBadRequest, "hourly retention must be 7-365 days")
			return
		}
		if ret.DailyDays < 30 || ret.DailyDays > 1825 {
			writeErr(w, http.StatusBadRequest, "daily retention must be 30-1825 days")
			return
		}
		if err := s.store.SetRetentionSettings(r.Context(), ret); err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to save retention settings")
			return
		}
	}

	if body.Branding != nil {
		b := *body.Branding
		if b.ProductName == "" {
			b.ProductName = "Argus"
		}
		if !validAccentColor(b.AccentColor) {
			writeErr(w, http.StatusBadRequest, "accent_color must be #RGB or #RRGGBB")
			return
		}
		b.AccentColor = strings.TrimSpace(b.AccentColor)
		if err := s.store.SetBrandingSettings(r.Context(), b); err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to save branding settings")
			return
		}
	}

	if body.OIDC != nil {
		o := *body.OIDC
		o.Issuer = strings.TrimSpace(o.Issuer)
		o.ClientID = strings.TrimSpace(o.ClientID)
		if o.DefaultRole == "" {
			o.DefaultRole = store.RoleViewer
		}
		if !store.ValidRole(o.DefaultRole) {
			writeErr(w, http.StatusBadRequest, "invalid default role")
			return
		}
		if err := s.store.SetOIDCSettings(r.Context(), o, true); err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to save oidc settings")
			return
		}
	}

	if body.Reports != nil {
		rs := *body.Reports
		recips, err := alerting.ValidEmailDest(rs.Recipients)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "report recipients must be comma-separated email addresses")
			return
		}
		rs.Recipients = recips
		if rs.MonthlyEnabled && recips == "" {
			writeErr(w, http.StatusBadRequest, "add at least one recipient for the monthly report")
			return
		}
		// Keep the bookkeeping field; it is not the operator's to set.
		current, _ := s.store.GetReportSettings(r.Context())
		rs.LastSentMonth = current.LastSentMonth
		if err := s.store.SetReportSettings(r.Context(), rs); err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to save report settings")
			return
		}
	}

	s.handleGetSettings(w, r)
}

func (s *Server) handleSMTPTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To string `json:"to"`
	}
	if err := decodeJSON(r, &body, 4096); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	to := strings.TrimSpace(body.To)
	if to == "" {
		writeErr(w, http.StatusBadRequest, "recipient is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	p := alerting.Payload{}
	s.dispatch.Decorate(ctx, &p)
	err := s.mailer.SendEmail(ctx, smtpTestEmail(p.Brand), []string{to})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "test email failed: "+err.Error())
		return
	}
	_ = s.store.InsertAlert(r.Context(), 0, "test", "sent", to, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// smtpTestEmail proves the mail server works, before any channel uses it.
func smtpTestEmail(brand string) alerting.Email {
	if brand == "" {
		brand = "Argus"
	}
	htmlBody, text := alerting.RenderEmail(alerting.EmailLayout{
		Brand:   brand,
		Tone:    "info",
		Eyebrow: "Mail server test",
		Title:   "Email is working",
		Lead:    "Alerts and reports sent by email will be delivered through this mail server.",
	})
	return alerting.Email{Subject: "[" + brand + "] Test email", HTML: htmlBody, Text: text}
}

func validAccentColor(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	if len(s) != 4 && len(s) != 7 {
		return false
	}
	if s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
