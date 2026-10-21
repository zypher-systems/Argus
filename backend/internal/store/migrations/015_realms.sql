CREATE TABLE realms (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organizations(id),
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX realms_org_lower_name ON realms (org_id, lower(name));
CREATE INDEX idx_realms_org ON realms (org_id);

CREATE TABLE realm_monitors (
    realm_id BIGINT NOT NULL REFERENCES realms(id) ON DELETE CASCADE,
    monitor_id BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    PRIMARY KEY (realm_id, monitor_id)
);

CREATE INDEX idx_realm_monitors_monitor ON realm_monitors (monitor_id);
