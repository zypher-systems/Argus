-- An alert that could not be sent when it happened: the monitor went down,
-- or came back, during maintenance, a mute, or its parent's outage. The probe
-- sends it once that is over, if it still applies. Without this an outage
-- that began inside a maintenance window was never alerted at all.
--   'down'      nobody has been told it is down
--   'recovery'  they were told it was down, and not yet that it is back
ALTER TABLE monitor_state ADD COLUMN IF NOT EXISTS held_alert TEXT NOT NULL DEFAULT '';

-- A heartbeat can say the job failed. That has to be remembered, or the
-- next freshness check sees a recent heartbeat and calls the monitor up.
ALTER TABLE monitor_state ADD COLUMN IF NOT EXISTS last_push_ok BOOLEAN NOT NULL DEFAULT TRUE;

-- A probe node that stops reporting is alerted once, like the local probe.
ALTER TABLE probe_nodes ADD COLUMN IF NOT EXISTS alerted_down BOOLEAN NOT NULL DEFAULT FALSE;

-- Deleting a channel left its id on the monitors attached to it. A monitor
-- attached only to deleted channels alerted nobody. Take the dead ids off,
-- and where none are left go back to the default of every channel: NULL
-- means that, while an empty list means the operator chose none.
UPDATE monitors m
   SET channel_ids = (SELECT array_agg(x.id ORDER BY x.ord)
                        FROM unnest(m.channel_ids) WITH ORDINALITY AS x(id, ord)
                       WHERE EXISTS (SELECT 1 FROM notification_channels c WHERE c.id = x.id))
 WHERE cardinality(m.channel_ids) > 0
   AND EXISTS (SELECT 1 FROM unnest(m.channel_ids) AS x(id)
                WHERE NOT EXISTS (SELECT 1 FROM notification_channels c WHERE c.id = x.id));

-- The same for probe nodes that were deleted: nobody was checking a monitor
-- assigned only to nodes that no longer exist. With none left it is checked
-- from this server. How many locations must agree is lowered only as far as
-- the locations that remain: a monitor that needed two of three and keeps
-- two still needs two.
WITH repaired AS (
    SELECT m.id,
           COALESCE((SELECT array_agg(x.id ORDER BY x.ord)
                       FROM unnest(m.probe_node_ids) WITH ORDINALITY AS x(id, ord)
                      WHERE x.id = 0 OR EXISTS (SELECT 1 FROM probe_nodes n WHERE n.id = x.id)), '{}') AS nodes
      FROM monitors m
     WHERE cardinality(m.probe_node_ids) > 0
       AND EXISTS (SELECT 1 FROM unnest(m.probe_node_ids) AS x(id)
                    WHERE x.id <> 0 AND NOT EXISTS (SELECT 1 FROM probe_nodes n WHERE n.id = x.id))
)
UPDATE monitors m
   SET probe_node_ids = r.nodes,
       confirm_locations = GREATEST(1, LEAST(m.confirm_locations, cardinality(r.nodes)))
  FROM repaired r
 WHERE m.id = r.id;

-- What a deleted node last reported is not a location any more. Left behind,
-- it showed as a second "local" row on the monitor's Locations table.
DELETE FROM probe_node_state s
 WHERE s.node_id <> 0
   AND NOT EXISTS (SELECT 1 FROM probe_nodes n WHERE n.id = s.node_id);
