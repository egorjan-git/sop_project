-- постоянные задания на сбор сообщений
CREATE TABLE IF NOT EXISTS zulip.sync_targets (
    target_id TEXT PRIMARY KEY,

    channel_id BIGINT NOT NULL
        REFERENCES zulip.channels(channel_id),

    period_start TIMESTAMPTZ NOT NULL,
    period_end   TIMESTAMPTZ NOT NULL,

    selection_version INTEGER NOT NULL DEFAULT 1,

    config JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- последний подтверждённый ID просмотренного сообщения
    high_watermark_id BIGINT,

    last_successful_at TIMESTAMPTZ,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (LENGTH(TRIM(target_id)) > 0),
    CHECK (period_end > period_start),
    CHECK (selection_version > 0),
    CHECK (
        high_watermark_id IS NULL
        OR high_watermark_id > 0
    ),
    CHECK (jsonb_typeof(config) = 'object')
);


-- связываем историю запусков с заданиями
ALTER TABLE zulip.sync_runs
    ADD COLUMN IF NOT EXISTS target_id TEXT
    REFERENCES zulip.sync_targets(target_id);


-- связываем контрольные точки с заданиями
ALTER TABLE zulip.sync_state
    ADD COLUMN IF NOT EXISTS target_id TEXT
    REFERENCES zulip.sync_targets(target_id);


-- запоминаем, какой запуск последним обновлял checkpoint
ALTER TABLE zulip.sync_state
    ADD COLUMN IF NOT EXISTS last_run_id BIGINT;


-- составной ключ для проверки принадлежности запуска
ALTER TABLE zulip.sync_runs
    ADD CONSTRAINT uq_zulip_run_target
    UNIQUE (run_id, target_id);


-- checkpoint не должен ссылаться на запуск другого target
ALTER TABLE zulip.sync_state
    ADD CONSTRAINT fk_zulip_checkpoint_run_target
    FOREIGN KEY (last_run_id, target_id)
    REFERENCES zulip.sync_runs(run_id, target_id);


ALTER TABLE zulip.sync_state
    ADD CONSTRAINT chk_zulip_checkpoint_run_target
    CHECK (
        last_run_id IS NULL
        OR target_id IS NOT NULL
    );


-- не больше одного running-запуска на target
CREATE UNIQUE INDEX IF NOT EXISTS idx_zulip_one_running_target
    ON zulip.sync_runs(target_id)
    WHERE target_id IS NOT NULL
      AND status = 'running';


-- поиск запусков конкретного задания
CREATE INDEX IF NOT EXISTS idx_zulip_runs_target_date
    ON zulip.sync_runs(target_id, started_at DESC);


-- поиск контрольных точек по заданию
CREATE INDEX IF NOT EXISTS idx_zulip_state_target
    ON zulip.sync_state(target_id);


-- поиск заданий конкретного канала
CREATE INDEX IF NOT EXISTS idx_zulip_targets_channel_period
    ON zulip.sync_targets(channel_id, period_start, period_end);
