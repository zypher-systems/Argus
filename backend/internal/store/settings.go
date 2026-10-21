package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type SMTPSettings struct {
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	From             string `json:"from"`
	TLSMode          string `json:"tls_mode"`
	GlobalRecipients string `json:"global_recipients"`
}

type GeneralSettings struct {
	BaseURL          string `json:"base_url"`
	NotifyMaxPerHour int    `json:"notify_max_per_hour"`
}

type RetentionSettings struct {
	RawDays    int `json:"raw_days"`
	HourlyDays int `json:"hourly_days"`
	DailyDays  int `json:"daily_days"`
}

// LegacyDefaultAccent was the accent before the 2.5.0 design, stored by any
// install that saved branding settings without picking a colour.
const LegacyDefaultAccent = "#6366f1"

type BrandingSettings struct {
	ProductName  string `json:"product_name"`
	LogoURL      string `json:"logo_url"`
	AccentColor  string `json:"accent_color"`
	LoginMessage string `json:"login_message"`
}

type ChannelSettings struct {
	WebhookURL string `json:"webhook_url"`
	SlackURL   string `json:"slack_url"`
	DiscordURL string `json:"discord_url"`
}

type OIDCSettings struct {
	Enabled      bool   `json:"enabled"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	DefaultRole  string `json:"default_role"`
}

// ReportSettings drives the monthly SLA email.
type ReportSettings struct {
	MonthlyEnabled bool   `json:"monthly_enabled"`
	Recipients     string `json:"recipients"`
	// LastSentMonth is the "2006-01" of the month already reported on, so a
	// restart or a second api replica does not send it twice.
	LastSentMonth string `json:"last_sent_month"`
}

type AllSettings struct {
	SMTP      SMTPSettings      `json:"smtp"`
	Channels  ChannelSettings   `json:"channels"`
	General   GeneralSettings   `json:"general"`
	Retention RetentionSettings `json:"retention"`
	Branding  BrandingSettings  `json:"branding"`
	OIDC      OIDCSettings      `json:"oidc"`
	Reports   ReportSettings    `json:"reports"`
}

func DefaultSettings() AllSettings {
	return AllSettings{
		SMTP:      SMTPSettings{Port: 587, TLSMode: "starttls"},
		General:   GeneralSettings{},
		Retention: RetentionSettings{RawDays: 7, HourlyDays: 90, DailyDays: 365},
		Branding:  BrandingSettings{ProductName: "Argus"},
	}
}

func (s *Store) getSetting(ctx context.Context, key string, dest any) error {
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return json.Unmarshal(raw, dest)
}

func (s *Store) setSetting(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ($1,$2,now())
		ON CONFLICT (key) DO UPDATE SET value=$2, updated_at=now()`, key, raw)
	if err != nil {
		return err
	}
	return s.NotifyConfig(ctx)
}

func (s *Store) LoadSettings(ctx context.Context) (AllSettings, error) {
	all := DefaultSettings()
	_ = s.getSetting(ctx, "smtp", &all.SMTP)
	_ = s.getSetting(ctx, "channels", &all.Channels)
	_ = s.getSetting(ctx, "general", &all.General)
	_ = s.getSetting(ctx, "retention", &all.Retention)
	_ = s.getSetting(ctx, "branding", &all.Branding)
	_ = s.getSetting(ctx, "oidc", &all.OIDC)
	_ = s.getSetting(ctx, "reports", &all.Reports)
	if all.OIDC.DefaultRole == "" {
		all.OIDC.DefaultRole = RoleViewer
	}
	if all.Branding.ProductName == "" {
		all.Branding.ProductName = "Argus"
	}
	// Empty means the theme's own accent, which differs between light and
	// dark. Installs that saved branding before 2.5.0 stored the old indigo
	// default without choosing it, so that value reads as unset too.
	if strings.EqualFold(strings.TrimSpace(all.Branding.AccentColor), LegacyDefaultAccent) {
		all.Branding.AccentColor = ""
	}
	return all, nil
}

func (s *Store) GetReportSettings(ctx context.Context) (ReportSettings, error) {
	var rs ReportSettings
	if err := s.getSetting(ctx, "reports", &rs); err != nil && !errors.Is(err, ErrNotFound) {
		return ReportSettings{}, err
	}
	return rs, nil
}

func (s *Store) SetReportSettings(ctx context.Context, rs ReportSettings) error {
	return s.setSetting(ctx, "reports", rs)
}

// MarkMonthlyReportSent claims a month for one sender. It returns false when
// another api instance already claimed it.
func (s *Store) MarkMonthlyReportSent(ctx context.Context, month string) (bool, error) {
	current, err := s.GetReportSettings(ctx)
	if err != nil {
		return false, err
	}
	if current.LastSentMonth == month {
		return false, nil
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE settings
		SET value = jsonb_set(value, '{last_sent_month}', to_jsonb($1::text)), updated_at = now()
		WHERE key = 'reports' AND COALESCE(value->>'last_sent_month', '') <> $1`, month)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		// No row yet: write the whole object, which is safe because the
		// settings row is created once.
		current.LastSentMonth = month
		if err := s.setSetting(ctx, "reports", current); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *Store) GetSMTPSettings(ctx context.Context) (SMTPSettings, error) {
	var smtp SMTPSettings
	if err := s.getSetting(ctx, "smtp", &smtp); err != nil {
		if errors.Is(err, ErrNotFound) {
			return DefaultSettings().SMTP, nil
		}
		return SMTPSettings{}, err
	}
	return smtp, nil
}

func (s *Store) SetSMTPSettings(ctx context.Context, smtp SMTPSettings, keepPasswordIfEmpty bool) error {
	if keepPasswordIfEmpty && smtp.Password == "" {
		current, err := s.GetSMTPSettings(ctx)
		if err == nil {
			smtp.Password = current.Password
		}
	}
	return s.setSetting(ctx, "smtp", smtp)
}

func (s *Store) GetChannelSettings(ctx context.Context) (ChannelSettings, error) {
	var ch ChannelSettings
	if err := s.getSetting(ctx, "channels", &ch); err != nil {
		if errors.Is(err, ErrNotFound) {
			return ChannelSettings{}, nil
		}
		return ChannelSettings{}, err
	}
	return ch, nil
}

func (s *Store) SetChannelSettings(ctx context.Context, ch ChannelSettings) error {
	return s.setSetting(ctx, "channels", ch)
}

func (s *Store) SetGeneralSettings(ctx context.Context, g GeneralSettings) error {
	return s.setSetting(ctx, "general", g)
}

func (s *Store) SetRetentionSettings(ctx context.Context, r RetentionSettings) error {
	return s.setSetting(ctx, "retention", r)
}

func (s *Store) SetBrandingSettings(ctx context.Context, b BrandingSettings) error {
	return s.setSetting(ctx, "branding", b)
}

func (s *Store) SetOIDCSettings(ctx context.Context, o OIDCSettings, keepSecretIfEmpty bool) error {
	if keepSecretIfEmpty && o.ClientSecret == "" {
		var current OIDCSettings
		if err := s.getSetting(ctx, "oidc", &current); err == nil {
			o.ClientSecret = current.ClientSecret
		}
	}
	if o.DefaultRole == "" {
		o.DefaultRole = RoleViewer
	}
	return s.setSetting(ctx, "oidc", o)
}

func (s *Store) RefreshUptime24h(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `UPDATE monitor_state st SET uptime_24h = sub.uptime
		FROM (
			SELECT monitor_id, 100.0 * count(*) FILTER (WHERE success) / count(*) AS uptime
			FROM monitor_results
			WHERE ts > now() - interval '24 hours'
			GROUP BY monitor_id
		) sub
		WHERE st.monitor_id = sub.monitor_id`)
	return err
}
