-- создаём отдельную схему для данных zulip
CREATE SCHEMA IF NOT EXISTS zulip;


-- пользователи zulip
CREATE TABLE IF NOT EXISTS zulip.users (
    user_id       BIGINT PRIMARY KEY,
    display_name  TEXT NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (user_id > 0)
);

-- каналы, доступные для сбора
CREATE TABLE IF NOT EXISTS zulip.channels (
    channel_id         BIGINT PRIMARY KEY,
    name               TEXT NOT NULL,
    approved_public    BOOLEAN NOT NULL DEFAULT FALSE,
    checked_at         TIMESTAMPTZ,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (channel_id > 0)
);


-- группы пользователей, например БИВ22
CREATE TABLE IF NOT EXISTS zulip.user_groups (
    group_id    BIGINT PRIMARY KEY,
    name        TEXT NOT NULL,

    CHECK (group_id > 0)
);


-- принадлежность пользователей к группам
CREATE TABLE IF NOT EXISTS zulip.group_memberships (
    group_id     BIGINT NOT NULL
        REFERENCES zulip.user_groups(group_id),

    user_id      BIGINT NOT NULL
        REFERENCES zulip.users(user_id),

    observed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (group_id, user_id)
);


-- сообщения из разрешённых публичных каналов
CREATE TABLE IF NOT EXISTS zulip.messages (
    message_id          BIGINT PRIMARY KEY,

    author_id           BIGINT NOT NULL
        REFERENCES zulip.users(user_id),

    channel_id          BIGINT NOT NULL
        REFERENCES zulip.channels(channel_id),

    topic               TEXT NOT NULL DEFAULT '',
    content             TEXT NOT NULL,
    sent_at             TIMESTAMPTZ NOT NULL,
    edited_at           TIMESTAMPTZ,

    source_url          TEXT NOT NULL,

    first_collected_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (message_id > 0),
    CHECK (LENGTH(TRIM(source_url)) > 0)
);


-- состояние загрузки для каждого scope
CREATE TABLE IF NOT EXISTS zulip.sync_state (
    scope_id     TEXT PRIMARY KEY,
    cursor       TEXT NOT NULL DEFAULT '',
    is_complete  BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (LENGTH(TRIM(scope_id)) > 0)
);


-- информация о каждом запуске коллектора
CREATE TABLE IF NOT EXISTS zulip.sync_runs (
    run_id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    scope_id           TEXT NOT NULL,
    mode               TEXT NOT NULL
        CHECK (mode IN ('initial', 'incremental', 'reconcile')),

    status             TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'completed', 'failed', 'cancelled')),

    period_start       TIMESTAMPTZ,
    period_end         TIMESTAMPTZ,

    scope_config       JSONB NOT NULL DEFAULT '{}'::jsonb,

    processed_pages    INTEGER NOT NULL DEFAULT 0,
    fetched_messages   INTEGER NOT NULL DEFAULT 0,
    selected_messages  INTEGER NOT NULL DEFAULT 0,
    inserted_messages  INTEGER NOT NULL DEFAULT 0,
    updated_messages   INTEGER NOT NULL DEFAULT 0,

    history_limited    BOOLEAN,
    error_summary      TEXT,

    started_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at        TIMESTAMPTZ,

    CHECK (processed_pages >= 0),
    CHECK (fetched_messages >= 0),
    CHECK (selected_messages >= 0),
    CHECK (inserted_messages >= 0),
    CHECK (updated_messages >= 0),

    CHECK (
        (period_start IS NULL AND period_end IS NULL)
        OR
        (period_start IS NOT NULL AND period_end > period_start)
    ),

    CHECK (
        finished_at IS NULL OR finished_at >= started_at
    ),

    CHECK (jsonb_typeof(scope_config) = 'object')
);


-- ошибки отдельных запусков
CREATE TABLE IF NOT EXISTS zulip.sync_errors (
    error_id      BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    run_id        BIGINT NOT NULL
        REFERENCES zulip.sync_runs(run_id),

    category      TEXT NOT NULL,
    message       TEXT NOT NULL,
    channel_id    BIGINT,
    retryable     BOOLEAN NOT NULL DEFAULT FALSE,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);


-- индексы для поиска и построения отчётов
CREATE INDEX IF NOT EXISTS idx_zulip_messages_author_date
    ON zulip.messages(author_id, sent_at);

CREATE INDEX IF NOT EXISTS idx_zulip_messages_channel_date
    ON zulip.messages(channel_id, sent_at);

CREATE INDEX IF NOT EXISTS idx_zulip_memberships_user
    ON zulip.group_memberships(user_id);

CREATE INDEX IF NOT EXISTS idx_zulip_sync_runs_scope
    ON zulip.sync_runs(scope_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_zulip_sync_errors_run
    ON zulip.sync_errors(run_id);
