CREATE TABLE IF NOT EXISTS resource_catalog (
    source_id    TEXT NOT NULL,
    source_key   TEXT NOT NULL,
    name         TEXT NOT NULL,
    url          TEXT NOT NULL,
    discipline   TEXT NOT NULL,
    cohort       TEXT NOT NULL,
    availability TEXT NOT NULL,
    questions    TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (source_id, source_key)
);
