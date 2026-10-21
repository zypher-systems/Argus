-- 2.10.0: the getting-started checklist. Most of its steps are read from the
-- data itself (monitors, channels, status pages); this holds what is not.
CREATE TABLE IF NOT EXISTS onboarding (
    org_id BIGINT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    dismissed_at TIMESTAMPTZ,
    test_alert_at TIMESTAMPTZ
);
