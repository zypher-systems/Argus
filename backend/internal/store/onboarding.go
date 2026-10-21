package store

import (
	"context"
	"time"
)

// Onboarding is the getting-started checklist for one organization. Each
// step is done because the thing exists, not because a box was ticked.
type Onboarding struct {
	Monitors    int        `json:"monitors"`
	Channels    int        `json:"channels"`
	StatusPages int        `json:"status_pages"`
	TestAlertAt *time.Time `json:"test_alert_at"`
	DismissedAt *time.Time `json:"dismissed_at"`
	// SMTPReady says whether an email channel would deliver, so the
	// checklist can offer to email the person directly.
	SMTPReady bool `json:"smtp_ready"`
}

// Complete reports whether every step is done.
func (o Onboarding) Complete() bool {
	return o.Monitors > 0 && o.Channels > 0 && o.TestAlertAt != nil && o.StatusPages > 0
}

func (s *Store) GetOnboarding(ctx context.Context) (Onboarding, error) {
	var o Onboarding
	org := OrgID(ctx)
	err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM monitors WHERE type <> 'group' AND ($1 = 0 OR org_id = $1)),
		(SELECT count(*) FROM notification_channels WHERE ($1 = 0 OR org_id = $1)),
		(SELECT count(*) FROM status_pages WHERE ($1 = 0 OR org_id = $1)),
		(SELECT test_alert_at FROM onboarding WHERE org_id = $2),
		(SELECT dismissed_at FROM onboarding WHERE org_id = $2)`,
		org, writeOrg(ctx)).Scan(&o.Monitors, &o.Channels, &o.StatusPages, &o.TestAlertAt, &o.DismissedAt)
	if err != nil {
		return o, err
	}
	smtp, err := s.LoadSettings(ctx)
	if err == nil {
		o.SMTPReady = smtp.SMTP.Host != "" && smtp.SMTP.From != ""
	}
	return o, nil
}

// MarkTestAlert records that a channel test was delivered.
func (s *Store) MarkTestAlert(ctx context.Context, at time.Time) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO onboarding (org_id, test_alert_at) VALUES ($1, $2)
		ON CONFLICT (org_id) DO UPDATE SET test_alert_at = $2`, writeOrg(ctx), at)
	return err
}

// SetOnboardingDismissed hides or shows the checklist.
func (s *Store) SetOnboardingDismissed(ctx context.Context, dismissed bool, at time.Time) error {
	var v any
	if dismissed {
		v = at
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO onboarding (org_id, dismissed_at) VALUES ($1, $2)
		ON CONFLICT (org_id) DO UPDATE SET dismissed_at = $2`, writeOrg(ctx), v)
	return err
}
