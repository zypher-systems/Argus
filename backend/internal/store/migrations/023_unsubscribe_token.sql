-- 2.11.0: a way out of status page emails. The confirm token is stored only
-- as a hash, so it cannot be put in a later email; this one can. It grants
-- nothing but removing that one subscription.
ALTER TABLE status_subscribers ADD COLUMN IF NOT EXISTS unsubscribe_token TEXT;
UPDATE status_subscribers
   SET unsubscribe_token = replace(gen_random_uuid()::text, '-', '') || replace(gen_random_uuid()::text, '-', '')
 WHERE unsubscribe_token IS NULL;
ALTER TABLE status_subscribers ALTER COLUMN unsubscribe_token SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS status_subscribers_unsubscribe ON status_subscribers (unsubscribe_token);
