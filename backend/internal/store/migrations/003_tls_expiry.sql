ALTER TABLE monitor_state
    ADD COLUMN tls_expires_at TIMESTAMPTZ,
    ADD COLUMN tls_warned_expiry TIMESTAMPTZ;
