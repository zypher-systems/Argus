CREATE TABLE notification_channels (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    url TEXT NOT NULL,
    events TEXT[] NOT NULL DEFAULT ARRAY['down','recovery','slow','cert','escalation']::text[],
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE monitors
    ADD COLUMN channel_ids BIGINT[];

INSERT INTO notification_channels (name, type, url)
SELECT 'Webhook', 'webhook', trim(both from value->>'webhook_url')
FROM settings
WHERE key = 'channels' AND coalesce(trim(both from value->>'webhook_url'), '') <> '';

INSERT INTO notification_channels (name, type, url)
SELECT 'Slack', 'slack', trim(both from value->>'slack_url')
FROM settings
WHERE key = 'channels' AND coalesce(trim(both from value->>'slack_url'), '') <> '';

INSERT INTO notification_channels (name, type, url)
SELECT 'Discord', 'discord', trim(both from value->>'discord_url')
FROM settings
WHERE key = 'channels' AND coalesce(trim(both from value->>'discord_url'), '') <> '';
