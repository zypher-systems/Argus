ALTER TABLE monitor_state
    ADD COLUMN IF NOT EXISTS muted_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS acked_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS acked_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_failure_evidence JSONB,
    ADD COLUMN IF NOT EXISTS last_failure_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS check_now_requests (
    id BIGSERIAL PRIMARY KEY,
    monitor_id BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending',
    success BOOLEAN,
    latency_ms DOUBLE PRECISION,
    error TEXT NOT NULL DEFAULT '',
    evidence JSONB,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_check_now_pending
    ON check_now_requests (id)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_check_now_monitor_open
    ON check_now_requests (monitor_id)
    WHERE status IN ('pending', 'running');
