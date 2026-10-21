CREATE TABLE IF NOT EXISTS incidents (
    id BIGSERIAL PRIMARY KEY,
    monitor_id BIGINT REFERENCES monitors(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'open',
    title TEXT NOT NULL,
    impact TEXT NOT NULL DEFAULT 'major',
    public BOOLEAN NOT NULL DEFAULT TRUE,
    auto_opened BOOLEAN NOT NULL DEFAULT TRUE,
    opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_incidents_opened_at ON incidents (opened_at DESC);
CREATE INDEX IF NOT EXISTS idx_incidents_status ON incidents (status);

CREATE UNIQUE INDEX IF NOT EXISTS idx_incidents_open_auto_monitor
    ON incidents (monitor_id)
    WHERE resolved_at IS NULL AND auto_opened = TRUE AND monitor_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS incident_updates (
    id BIGSERIAL PRIMARY KEY,
    incident_id BIGINT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    message TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_incident_updates_incident ON incident_updates (incident_id, created_at ASC);

CREATE TABLE IF NOT EXISTS status_subscribers (
    id BIGSERIAL PRIMARY KEY,
    status_page_id BIGINT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('email', 'webhook')),
    dest TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (status_page_id, kind, dest)
);

ALTER TABLE status_pages ADD COLUMN IF NOT EXISTS custom_domain TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_status_pages_custom_domain
    ON status_pages (lower(custom_domain))
    WHERE custom_domain IS NOT NULL AND custom_domain <> '';
