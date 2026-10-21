CREATE TABLE status_pages (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    headline TEXT NOT NULL DEFAULT '',
    branded BOOLEAN NOT NULL DEFAULT FALSE,
    published BOOLEAN NOT NULL DEFAULT TRUE,
    monitor_ids BIGINT[],
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
