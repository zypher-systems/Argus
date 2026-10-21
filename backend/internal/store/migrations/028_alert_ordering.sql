-- What a monitor's recipients were last told, kept as a fact instead of as
-- "an alert is being held". An alert is owed whenever this disagrees with
-- the monitor's status and nothing is suppressing it: down and not told, or
-- up again and still told it is down. The probe looks for that every 30
-- seconds, so the order outages and suppression arrive in cannot leave one
-- unreported, and an alert lost between recording an outage and queuing its
-- alert is sent on the next look.
ALTER TABLE monitor_state ADD COLUMN IF NOT EXISTS told_down BOOLEAN NOT NULL DEFAULT FALSE;

-- A monitor that went down while its incident could not be opened
-- (maintenance, mute) and is owed one when that ends.
ALTER TABLE monitor_state ADD COLUMN IF NOT EXISTS incident_owed BOOLEAN NOT NULL DEFAULT FALSE;

-- A monitor that is down today has been alerted as down, unless that alert
-- was the one being held.
UPDATE monitor_state SET told_down = CASE
        WHEN held_alert = 'down' THEN FALSE
        WHEN held_alert = 'recovery' THEN TRUE
        ELSE status = 'down'
    END;
ALTER TABLE monitor_state DROP COLUMN IF EXISTS held_alert;

-- Every certificate expiry already warned about, so that each is warned
-- once whichever is seen first. Remembering only the latest never warned
-- for a certificate that expires sooner than one seen before it.
ALTER TABLE monitor_state ADD COLUMN IF NOT EXISTS tls_warned_expiries TIMESTAMPTZ[] NOT NULL DEFAULT '{}';
UPDATE monitor_state SET tls_warned_expiries = ARRAY[tls_warned_expiry]
 WHERE tls_warned_expiry IS NOT NULL AND tls_warned_expiries = '{}';

-- A queued alert belongs to a stream: one problem as a pager sees it, the
-- key PagerDuty and Opsgenie match on. Within a stream, to one destination,
-- alerts are delivered in the order they were queued, and a newer alert that
-- says the opposite replaces an older one still waiting, in both directions.
ALTER TABLE alert_outbox ADD COLUMN IF NOT EXISTS stream TEXT NOT NULL DEFAULT '';
ALTER TABLE alert_outbox ADD COLUMN IF NOT EXISTS resolves BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX IF NOT EXISTS idx_alert_outbox_stream ON alert_outbox (stream, id) WHERE stream <> '';
