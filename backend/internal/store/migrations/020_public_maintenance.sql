-- 2.7.0: a maintenance window can be announced on status pages, so customers
-- see planned work instead of an unexplained outage.
ALTER TABLE maintenance_windows
    ADD COLUMN IF NOT EXISTS public BOOLEAN NOT NULL DEFAULT false;
