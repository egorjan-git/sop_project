# Сбор сообщений Zulip

Используется сборщик из `main`. Из корня репозитория:

```sh
git switch main
```

Выполни [настройку PostgreSQL](https://github.com/egorjan-git/sop_project/blob/main/README.md#postgresql-через-docker-compose) в том же терминале.

## Сбор

В терминале zsh:

```zsh
cd zulip-collector
export ZULIP_EMAIL='EMAIL_БОТА'
read -rs 'ZULIP_API_KEY?Ключ Zulip: '
echo
export ZULIP_API_KEY

go run ./cmd/collector channels
go run ./cmd/collector live -channels 2392,2428 -from 2024-09-01 -to 2026-11-16
```

Подставь нужные ID каналов и даты. Обе даты включены по московскому времени. После прерывания повтори `live` с тем же ботом, каналами и датами.

## Выгрузка

```sh
mkdir -p exports
go run ./cmd/collector export -channels 2392,2428 -from 2024-09-01 -to 2026-11-16 -format json -out exports/biv24.json
go run ./cmd/collector export -channels 2392,2428 -from 2024-09-01 -to 2026-11-16 -format csv -out exports/biv24.csv
```

[Общий README проекта](https://github.com/egorjan-git/sop_project/blob/main/README.md).
