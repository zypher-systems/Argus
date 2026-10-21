CREATE TABLE organizations (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    plan TEXT NOT NULL DEFAULT 'community',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO organizations (id, name, slug, plan)
VALUES (1, 'Default', 'default', 'community')
ON CONFLICT (id) DO NOTHING;

SELECT setval('organizations_id_seq', GREATEST((SELECT MAX(id) FROM organizations), 1));

ALTER TABLE users ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organizations(id);
ALTER TABLE monitors ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organizations(id);
ALTER TABLE notification_channels ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organizations(id);
ALTER TABLE maintenance_windows ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organizations(id);
ALTER TABLE status_pages ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organizations(id);
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organizations(id);
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organizations(id);
ALTER TABLE probe_nodes ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organizations(id);
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS org_id BIGINT REFERENCES organizations(id);

CREATE INDEX IF NOT EXISTS idx_users_org ON users (org_id);
CREATE INDEX IF NOT EXISTS idx_monitors_org ON monitors (org_id);
CREATE INDEX IF NOT EXISTS idx_channels_org ON notification_channels (org_id);
CREATE INDEX IF NOT EXISTS idx_status_pages_org ON status_pages (org_id);
CREATE INDEX IF NOT EXISTS idx_incidents_org ON incidents (org_id);
