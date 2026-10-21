-- Alerts waiting to be delivered, one row per alert per destination.
--
-- An alert used to exist only in the memory of the process sending it: a
-- restart mid-send, or a mail server or pager unreachable for more than a
-- minute or so, and it was gone, with the monitor already recorded as down
-- so nothing would raise it again. A row here survives both. Whichever
-- process is running picks it up, and it is retried until it is delivered or
-- a day has passed.
CREATE TABLE IF NOT EXISTS alert_outbox (
    id              BIGSERIAL PRIMARY KEY,
    -- No foreign key: probe alerts have no monitor, and an alert about a
    -- monitor that is deleted a moment later should still arrive.
    monitor_id      BIGINT NOT NULL DEFAULT 0,
    type            TEXT NOT NULL,
    -- The channel to send through, read again at send time so a corrected
    -- address takes effect. NULL sends email straight to the addresses in dest.
    channel_id      BIGINT,
    dest            TEXT NOT NULL DEFAULT '',
    payload         JSONB NOT NULL,
    -- pending | sent | failed | skipped | superseded
    status          TEXT NOT NULL DEFAULT 'pending',
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_alert_outbox_due ON alert_outbox (next_attempt_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_alert_outbox_monitor ON alert_outbox (monitor_id) WHERE status = 'pending';
