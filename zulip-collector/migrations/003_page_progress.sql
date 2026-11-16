-- максимальный ID сообщения, просмотренного источником
-- не путать с курсором пагинации

ALTER TABLE zulip.sync_state
    ADD COLUMN IF NOT EXISTS last_seen_message_id BIGINT;

ALTER TABLE zulip.sync_state
    ADD CONSTRAINT chk_zulip_last_seen_message_id
    CHECK (
        last_seen_message_id IS NULL
        OR last_seen_message_id > 0
    );
