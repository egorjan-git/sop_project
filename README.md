# SOP Project

Два парсера собирают сообщения Zulip и учебные ведомости в одну PostgreSQL. Требуется Go 1.26.2+.

## Что сделано

- Сборщик получает сообщения через Zulip API, сохраняет автора, дату, канал, тему, текст и ссылку. Повторный запуск обновляет сообщения по ID, а после сбоя сбор продолжается с сохранённой позиции. [Команды сборщика Zulip](https://github.com/egorjan-git/sop_project/blob/zulip-integration/README.md)
- Импортёр сохраняет снимки листов из Excel и Google Таблиц. Оба парсера выгружают данные в JSON и CSV. [Команды импортёра ведомостей](sop_project/README.md)
- Учебная история объединяет сообщения привязанного аккаунта Zulip и подтверждённые строки ведомостей. [Команды для учебной истории](sop_project/README.md#учебная-история-студента)
- Запуск PostgreSQL через Docker Compose проверен на чистом томе. Схема создаётся автоматически, а записи сохраняются при повторном создании контейнера с тем же томом.

## PostgreSQL через Docker Compose

Из корня репозитория:

```sh
if [ ! -f database/.env ]; then
  (umask 077; printf 'POSTGRES_PASSWORD=%s\n' "$(openssl rand -hex 24)" > database/.env)
fi
docker compose --env-file database/.env -f database/compose.yaml up -d --wait
set -a
. database/.env
set +a
export DATABASE_URL="postgresql://sop_app:${POSTGRES_PASSWORD}@127.0.0.1:${POSTGRES_PORT:-55432}/sop_db?sslmode=disable"
```

При первом запуске Compose создаёт схему обоих парсеров. Чтобы изменить порт `55432`, задай `POSTGRES_PORT` в `database/.env`. Для автоматического подключения из GoLand задай `DATABASE_URL` в конфигурациях обоих запусков.

## PostgreSQL на сервере

Передай обоим парсерам один `DATABASE_URL` с TLS. В пустой базе администратор один раз применяет миграции:

```sh
psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -f database/bootstrap.sql
```

## Проверка

```sh
go run ./zulip-collector/cmd/dbcheck
go test ./sop_project/... ./zulip-collector/...
```

Для интеграционных тестов создай отдельную пустую БД `sop_collector_test` и задай `POSTGRES_TEST_URL` для неё. Из корня проекта:

```sh
psql "$POSTGRES_TEST_URL" -X -v ON_ERROR_STOP=1 -f database/bootstrap.sql
go test -race ./sop_project/... ./zulip-collector/...
```

Миграции для этой базы применяются один раз.

Серверное развёртывание пока не выполнено. Рабочая PostgreSQL сейчас запущена локально.
