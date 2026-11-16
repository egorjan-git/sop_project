# SOP Project

Эта ветка содержит первый этап интеграции. Для запуска и разработки используй [актуальную версию в `main`](https://github.com/egorjan-git/sop_project/tree/main).

- [Сборщик Zulip](https://github.com/egorjan-git/sop_project/blob/main/zulip-collector/README.md) получает сообщения через Zulip API, сохраняет их в PostgreSQL и выгружает в JSON и CSV. Повторный сбор обновляет записи по ID, а после прерывания можно продолжить с сохранённой позиции.
- [Импорт ведомостей](https://github.com/egorjan-git/sop_project/blob/main/sop_project/README.md) загружает Excel и Google Таблицы в ту же PostgreSQL.
- [Учебная история студента](https://github.com/egorjan-git/sop_project/blob/main/sop_project/README.md#учебная-история-студента) объединяет сообщения привязанного аккаунта Zulip и подтверждённые строки ведомостей.

Порядок запуска PostgreSQL через Docker Compose и интеграционных тестов описан в [README ветки `main`](https://github.com/egorjan-git/sop_project/blob/main/README.md).
