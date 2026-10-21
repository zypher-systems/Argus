ALTER TABLE users ADD COLUMN IF NOT EXISTS oidc_sub TEXT UNIQUE;

ALTER TABLE monitors
    ADD COLUMN IF NOT EXISTS slo_target DOUBLE PRECISION NOT NULL DEFAULT 99.9,
    ADD COLUMN IF NOT EXISTS probe_node_ids BIGINT[] NOT NULL DEFAULT '{}';

ALTER TABLE api_tokens
    ADD COLUMN IF NOT EXISTS user_id BIGINT REFERENCES users(id) ON DELETE CASCADE;

UPDATE api_tokens SET user_id = (SELECT id FROM users ORDER BY id ASC LIMIT 1)
WHERE user_id IS NULL;

CREATE TABLE IF NOT EXISTS probe_nodes (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    region TEXT NOT NULL DEFAULT '',
    token_hash TEXT UNIQUE,
    last_seen TIMESTAMPTZ,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS probe_node_state (
    monitor_id BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    node_id BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    last_latency_ms DOUBLE PRECISION,
    last_check_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (monitor_id, node_id)
);

CREATE TABLE IF NOT EXISTS audit_log (
    id BIGSERIAL PRIMARY KEY,
    ts TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id BIGINT,
    email TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    resource TEXT NOT NULL DEFAULT '',
    resource_id BIGINT,
    detail JSONB NOT NULL DEFAULT '{}',
    ip TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_audit_log_ts ON audit_log (ts DESC);
CREATE INDEX IF NOT EXISTS idx_probe_nodes_enabled ON probe_nodes (enabled);
