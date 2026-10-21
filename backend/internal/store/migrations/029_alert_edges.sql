-- When a monitor was last switched back on. Its status is whatever it was
-- when it was paused until it has been checked again, so nothing is owed on
-- the strength of it before then.
ALTER TABLE monitor_state ADD COLUMN IF NOT EXISTS resumed_at TIMESTAMPTZ;

-- Finding the next alert to send looks at alerts that are waiting and at
-- alerts being sent whose sender may have died. The index from migration 026
-- covers only the waiting ones, so every look read the whole table.
DROP INDEX IF EXISTS idx_alert_outbox_due;
CREATE INDEX IF NOT EXISTS idx_alert_outbox_claim ON alert_outbox (next_attempt_at, id)
    WHERE status IN ('pending', 'sending');
-- For counting what is in flight to one destination.
CREATE INDEX IF NOT EXISTS idx_alert_outbox_sending ON alert_outbox (channel_id)
    WHERE status = 'sending';

-- Alerts queued before streams existed are given theirs, so they are
-- ordered and replaced like any other.
UPDATE alert_outbox
   SET resolves = type IN ('recovery', 'probe_up'),
       stream = CASE
           WHEN type = 'test' THEN ''
           WHEN COALESCE(payload->>'key', '') <> '' THEN payload->>'key'
           WHEN monitor_id = 0 THEN 'argus-probe'
           WHEN type = 'slow' OR (type = 'recovery' AND payload->>'of' = 'slow')
               THEN 'argus-monitor-' || monitor_id || '-slow'
           WHEN type IN ('cert', 'domain') THEN 'argus-monitor-' || monitor_id || '-' || type
           ELSE 'argus-monitor-' || monitor_id
       END
 WHERE stream = '' AND status IN ('pending', 'sending');
