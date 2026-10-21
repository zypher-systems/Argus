CREATE INDEX IF NOT EXISTS idx_monitor_results_ts ON monitor_results (ts);
CREATE INDEX IF NOT EXISTS idx_monitor_events_ts ON monitor_events (ts);
CREATE INDEX IF NOT EXISTS idx_alerts_created_at ON alerts (created_at);

CREATE TABLE IF NOT EXISTS probe_heartbeat (
    id TEXT PRIMARY KEY,
    last_seen TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    alerted_down BOOLEAN NOT NULL DEFAULT FALSE
);

ALTER TABLE alerts ALTER COLUMN monitor_id DROP NOT NULL;

UPDATE notification_channels
SET events = array_append(events, 'probe_down')
WHERE NOT ('probe_down' = ANY (events));

UPDATE notification_channels
SET events = array_append(events, 'probe_up')
WHERE NOT ('probe_up' = ANY (events));
