# SOP Project

- [Импорт Google Таблиц и Excel](sop_project/README.md) → `public.sheet_snapshots`.
- [Сбор сообщений Zulip](zulip-collector/README.md) → `zulip.messages`.
- [Учебная история студента](sop_project/README.md#учебная-история-студента) → сообщения и подтверждённые строки ведомостей.

Оба парсера используют один `DATABASE_URL`. Требуется Go 1.26.2+.

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
