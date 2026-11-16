-- схема для связи данных из zulip и ведомостей
CREATE SCHEMA IF NOT EXISTS integration;


-- проверяем наличие исходных таблиц
DO $$
BEGIN
    IF to_regclass('zulip.users') IS NULL THEN
        RAISE EXCEPTION 'missing table zulip.users';
    END IF;

    IF to_regclass('public.sheet_snapshots') IS NULL THEN
        RAISE EXCEPTION 'missing table public.sheet_snapshots';
    END IF;
END
$$;


-- единый реестр студентов
CREATE TABLE IF NOT EXISTS integration.students (
    student_id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    cohort_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);


-- связываем аккаунт zulip с конкретным студентом
CREATE TABLE IF NOT EXISTS integration.zulip_student_links (
    zulip_user_id BIGINT PRIMARY KEY
        REFERENCES zulip.users(user_id),

    student_id BIGINT NOT NULL
        REFERENCES integration.students(student_id),

    match_method TEXT NOT NULL,
    verified_at TIMESTAMPTZ NOT NULL,

    CHECK (LENGTH(TRIM(match_method)) > 0)
);


-- структурированные записи, полученные из ведомостей
CREATE TABLE IF NOT EXISTS integration.sheet_records (
    record_id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    spreadsheet_id TEXT NOT NULL,
    sheet_id INTEGER NOT NULL,

    source_key TEXT NOT NULL,
    source_row INTEGER,
    source_column INTEGER,

    description TEXT,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    FOREIGN KEY (spreadsheet_id, sheet_id)
        REFERENCES public.sheet_snapshots(
            spreadsheet_id,
            sheet_id
        ),

    UNIQUE (spreadsheet_id, sheet_id, source_key),

    CHECK (LENGTH(TRIM(source_key)) > 0),
    CHECK (source_row IS NULL OR source_row > 0),
    CHECK (source_column IS NULL OR source_column > 0),
    CHECK (jsonb_typeof(attributes) = 'object')
);


-- связываем студентов с записями ведомостей
CREATE TABLE IF NOT EXISTS integration.student_sheet_links (
    student_id BIGINT NOT NULL
        REFERENCES integration.students(student_id),

    record_id BIGINT NOT NULL
        REFERENCES integration.sheet_records(record_id),

    match_status TEXT NOT NULL DEFAULT 'candidate'
        CHECK (match_status IN ('candidate', 'confirmed')),

    match_method TEXT,
    verified_at TIMESTAMPTZ,

    PRIMARY KEY (student_id, record_id),

    CHECK (
        match_status <> 'confirmed'
        OR (
            verified_at IS NOT NULL
            AND match_method IS NOT NULL
            AND LENGTH(TRIM(match_method)) > 0
        )
    )
);


-- индексы для поиска связей
CREATE INDEX IF NOT EXISTS idx_integration_zulip_student
    ON integration.zulip_student_links(student_id);

CREATE INDEX IF NOT EXISTS idx_integration_sheet_link_record
    ON integration.student_sheet_links(record_id);

CREATE INDEX IF NOT EXISTS idx_integration_sheet_link_status
    ON integration.student_sheet_links(student_id, match_status);
