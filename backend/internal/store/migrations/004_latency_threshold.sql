ALTER TABLE monitors
    ADD COLUMN latency_threshold_ms INT NOT NULL DEFAULT 0;

ALTER TABLE monitor_state
    ADD COLUMN consecutive_slow INT NOT NULL DEFAULT 0,
    ADD COLUMN slow_since TIMESTAMPTZ;
