CREATE TABLE IF NOT EXISTS sheet_snapshots (
    spreadsheet_id    TEXT NOT NULL,
    sheet_id          INTEGER NOT NULL CHECK (sheet_id >= 0),
    spreadsheet_title TEXT NOT NULL,
    sheet_title       TEXT NOT NULL,
    sheet_index       INTEGER NOT NULL CHECK (sheet_index >= 0),
    -- Dimensions of the source grid, including unpopulated cells.
    row_count         INTEGER NOT NULL CHECK (row_count >= 0),
    column_count      INTEGER NOT NULL CHECK (column_count >= 0),
    hidden            BOOLEAN NOT NULL,
    cells             JSONB NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (spreadsheet_id, sheet_id)
);
