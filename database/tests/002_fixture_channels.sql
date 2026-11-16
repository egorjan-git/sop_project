-- тестовые каналы для локального коллектора
-- в рабочей базе этот файл не используем

INSERT INTO zulip.channels (
    channel_id,
    name,
    approved_public,
    checked_at
)
VALUES
    (501, 'Комп графика', TRUE, NOW()),
    (502, 'ПС', TRUE, NOW())
ON CONFLICT (channel_id) DO UPDATE
SET
    name = EXCLUDED.name,
    approved_public = EXCLUDED.approved_public,
    checked_at = EXCLUDED.checked_at,
    updated_at = NOW();
