BEGIN;

-- тестовый пользователь zulip
INSERT INTO zulip.users (user_id, display_name)
VALUES (9900000101, 'Тестовый студент');


-- два разрешённых публичных канала
INSERT INTO zulip.channels (
    channel_id, name, approved_public
)
VALUES
    (9900000501, 'Комп графика', TRUE),
    (9900000502, 'ПС', TRUE);


-- сообщения одного студента из разных каналов
INSERT INTO zulip.messages (
    message_id,
    author_id,
    channel_id,
    topic,
    content,
    sent_at,
    source_url
)
VALUES
(
    99000001001,
    9900000101,
    9900000501,
    'Практическая работа',
    'Как реализовать интерфейс в Go?',
    '2024-10-15 14:35:00+00',
    'https://example.org/messages/99000001001'
),
(
    99000001002,
    9900000101,
    9900000502,
    'Контрольная работа',
    'Разобрал задачу по математике',
    '2024-10-16 11:20:00+00',
    'https://example.org/messages/99000001002'
);


-- создаём студента и связываем с аккаунтом zulip
WITH new_student AS (
    INSERT INTO integration.students (cohort_code)
    VALUES ('БИВ22')
    RETURNING student_id
)
INSERT INTO integration.zulip_student_links (
    zulip_user_id,
    student_id,
    match_method,
    verified_at
)
SELECT
    9900000101,
    student_id,
    'synthetic_manual_match',
    NOW()
FROM new_student;


-- имитируем снимок ведомости от парсера коллеги
INSERT INTO public.sheet_snapshots (
    spreadsheet_id,
    sheet_id,
    spreadsheet_title,
    sheet_title,
    sheet_index,
    row_count,
    column_count,
    hidden,
    cells
)
VALUES (
    '__test_sop_link__',
    1,
    'Тестовая ведомость',
    'БИВ22',
    0,
    3,
    2,
    FALSE,
    '[
      {"row":1,"column":1,"data":{"formattedValue":"Студент"}},
      {"row":1,"column":2,"data":{"formattedValue":"Описание"}},
      {"row":2,"column":1,"data":{"formattedValue":"Тестовый студент"}},
      {"row":2,"column":2,"data":{"formattedValue":"Практикум по Go"}},
      {"row":3,"column":1,"data":{"formattedValue":"Тестовый студент"}},
      {"row":3,"column":2,"data":{"formattedValue":"Задание по SQL"}}
    ]'::jsonb
);


-- извлекаем две условные записи из ведомости
INSERT INTO integration.sheet_records (
    spreadsheet_id,
    sheet_id,
    source_key,
    source_row,
    description,
    attributes
)
VALUES
(
    '__test_sop_link__',
    1,
    'row:2',
    2,
    'Практикум по Go',
    '{"discipline":"Go","result":"зачёт"}'::jsonb
),
(
    '__test_sop_link__',
    1,
    'row:3',
    3,
    'Задание по SQL',
    '{"discipline":"SQL","result":"не проверено"}'::jsonb
);


-- связываем студента с записями ведомости
-- первую связь подтверждаем, вторую оставляем кандидатом
INSERT INTO integration.student_sheet_links (
    student_id,
    record_id,
    match_status,
    match_method,
    verified_at
)
SELECT
    z.student_id,
    r.record_id,
    CASE
        WHEN r.source_key = 'row:2'
        THEN 'confirmed'
        ELSE 'candidate'
    END,
    CASE
        WHEN r.source_key = 'row:2'
        THEN 'synthetic_manual_match'
        ELSE NULL
    END,
    CASE
        WHEN r.source_key = 'row:2'
        THEN NOW()
        ELSE NULL
    END
FROM integration.zulip_student_links AS z
CROSS JOIN integration.sheet_records AS r
WHERE z.zulip_user_id = 9900000101
  AND r.spreadsheet_id = '__test_sop_link__';


-- проверка 1: получаем сообщения студента
SELECT
    z.student_id,
    m.message_id,
    u.display_name,
    c.name AS channel_name,
    m.topic,
    m.content,
    m.sent_at,
    m.source_url
FROM integration.zulip_student_links AS z
JOIN zulip.users AS u
    ON u.user_id = z.zulip_user_id
JOIN zulip.messages AS m
    ON m.author_id = u.user_id
JOIN zulip.channels AS c
    ON c.channel_id = m.channel_id
WHERE z.zulip_user_id = 9900000101
ORDER BY m.sent_at;


-- проверка 2: получаем подтверждённые записи ведомости
SELECT
    z.student_id,
    r.record_id,
    ss.sheet_title,
    r.description,
    r.attributes->>'discipline' AS discipline,
    r.attributes->>'result' AS result,
    sl.match_status
FROM integration.zulip_student_links AS z
JOIN integration.student_sheet_links AS sl
    ON sl.student_id = z.student_id
JOIN integration.sheet_records AS r
    ON r.record_id = sl.record_id
JOIN public.sheet_snapshots AS ss
    ON ss.spreadsheet_id = r.spreadsheet_id
   AND ss.sheet_id = r.sheet_id
WHERE z.zulip_user_id = 9900000101
  AND sl.match_status = 'confirmed';


-- проверка 3: общая статистика студента
-- считаем источники отдельно, без размножения строк
SELECT
    z.student_id,
    (
        SELECT COUNT(*)
        FROM zulip.messages AS m
        WHERE m.author_id = z.zulip_user_id
    ) AS message_count,
    (
        SELECT COUNT(*)
        FROM integration.student_sheet_links AS sl
        WHERE sl.student_id = z.student_id
          AND sl.match_status = 'confirmed'
    ) AS confirmed_records
FROM integration.zulip_student_links AS z
WHERE z.zulip_user_id = 9900000101;


-- отменяем все тестовые вставки
ROLLBACK;
