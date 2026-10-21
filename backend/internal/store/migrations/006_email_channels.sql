ALTER TABLE notification_channels RENAME COLUMN url TO dest;

INSERT INTO notification_channels (name, type, dest)
SELECT 'Email', 'email', trim(both from value->>'global_recipients')
FROM settings
WHERE key = 'smtp' AND coalesce(trim(both from value->>'global_recipients'), '') <> '';
