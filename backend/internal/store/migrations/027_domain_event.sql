-- Domain expiry warnings arrived in 2.7.0 (2026-09-22) as an event of their
-- own. Earlier events were added to existing channels when they were
-- introduced; this one was not, so a channel made before then never received
-- a domain warning, and the one warning per expiry date was spent on nobody.
-- A channel that takes certificate warnings is given domain warnings too.
-- Channels made since then chose their events with "domain" on offer, and
-- are left as they were set.
UPDATE notification_channels
   SET events = array_append(events, 'domain')
 WHERE created_at < TIMESTAMPTZ '2026-09-23 00:00:00+00'
   AND 'cert' = ANY(events)
   AND NOT ('domain' = ANY(events));
