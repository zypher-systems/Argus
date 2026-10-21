ALTER TABLE monitors
    ADD COLUMN config JSONB NOT NULL DEFAULT '{}'::jsonb;
