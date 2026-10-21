-- 2.7.0: domain monitors watch registration expiry, which is not a TLS cert.
ALTER TABLE monitor_state
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS expiry_warned TIMESTAMPTZ;
