-- 2.9.0: a monitor checked from several locations goes down when enough of
-- them agree, not whenever one of them fails.

-- How many locations must see a monitor down before it is down. 1 keeps the
-- old behaviour: any location that confirms a failure takes it down.
ALTER TABLE monitors ADD COLUMN IF NOT EXISTS confirm_locations INT NOT NULL DEFAULT 1
    CHECK (confirm_locations BETWEEN 1 AND 20);

-- Each location counts its own consecutive failures against the monitor's
-- retries, so one flaky region cannot run up another's count.
ALTER TABLE probe_node_state ADD COLUMN IF NOT EXISTS consecutive_failures INT NOT NULL DEFAULT 0;

-- Until now a location's status was its last raw result, not a confirmed
-- one. Start each location from the monitor's confirmed state, so the first
-- check after the upgrade cannot turn one old failure into an outage.
UPDATE probe_node_state p
   SET status = s.status, consecutive_failures = s.consecutive_failures
  FROM monitor_state s
 WHERE s.monitor_id = p.monitor_id;
