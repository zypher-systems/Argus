ALTER TABLE monitors
    ADD COLUMN IF NOT EXISTS resend_minutes INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS parent_id BIGINT REFERENCES monitors(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS depends_on BIGINT REFERENCES monitors(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_monitors_parent_id ON monitors (parent_id);

ALTER TABLE monitor_state
    ADD COLUMN IF NOT EXISTS last_push_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_alert_at TIMESTAMPTZ;

CREATE UNIQUE INDEX IF NOT EXISTS idx_monitors_push_token
    ON monitors ((config->>'push_token'))
    WHERE type = 'push' AND config->>'push_token' IS NOT NULL AND config->>'push_token' <> '';

CREATE TABLE IF NOT EXISTS api_tokens (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);

UPDATE notification_channels
SET events = array_append(events, 'resend')
WHERE NOT ('resend' = ANY (events));
