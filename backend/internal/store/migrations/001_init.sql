CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE monitors (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT 'icmp',
    target TEXT NOT NULL,
    interval_seconds INT NOT NULL DEFAULT 60,
    retries INT NOT NULL DEFAULT 3,
    escalate_minutes INT NOT NULL DEFAULT 0,
    escalate_recipients TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    recipients TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE monitor_state (
    monitor_id BIGINT PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending',
    last_latency_ms DOUBLE PRECISION,
    last_check_at TIMESTAMPTZ,
    last_change_at TIMESTAMPTZ,
    consecutive_failures INT NOT NULL DEFAULT 0,
    down_since TIMESTAMPTZ,
    escalated_at TIMESTAMPTZ,
    uptime_24h DOUBLE PRECISION
);

CREATE TABLE monitor_results (
    id BIGSERIAL PRIMARY KEY,
    monitor_id BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    ts TIMESTAMPTZ NOT NULL,
    latency_ms DOUBLE PRECISION,
    success BOOLEAN NOT NULL
);

CREATE INDEX idx_monitor_results_monitor_ts ON monitor_results (monitor_id, ts DESC);

CREATE TABLE monitor_events (
    id BIGSERIAL PRIMARY KEY,
    monitor_id BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    ts TIMESTAMPTZ NOT NULL DEFAULT now(),
    type TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_monitor_events_monitor_ts ON monitor_events (monitor_id, ts DESC);

CREATE TABLE rollup_hourly (
    monitor_id BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    bucket TIMESTAMPTZ NOT NULL,
    total INT NOT NULL,
    failures INT NOT NULL,
    avg_latency DOUBLE PRECISION,
    max_latency DOUBLE PRECISION,
    PRIMARY KEY (monitor_id, bucket)
);

CREATE TABLE rollup_daily (
    monitor_id BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    bucket TIMESTAMPTZ NOT NULL,
    total INT NOT NULL,
    failures INT NOT NULL,
    avg_latency DOUBLE PRECISION,
    max_latency DOUBLE PRECISION,
    PRIMARY KEY (monitor_id, bucket)
);

CREATE TABLE alerts (
    id BIGSERIAL PRIMARY KEY,
    monitor_id BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    recipients TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_alerts_monitor_ts ON alerts (monitor_id, created_at DESC);

CREATE TABLE maintenance_windows (
    id BIGSERIAL PRIMARY KEY,
    monitor_id BIGINT REFERENCES monitors(id) ON DELETE CASCADE,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
