# Сбор сообщений Zulip

Задай `DATABASE_URL` общей PostgreSQL по [инструкции проекта](../README.md). Команды ниже выполняются из папки `zulip-collector`.

```zsh
export ZULIP_EMAIL='EMAIL_БОТА'
read -rs 'ZULIP_API_KEY?Ключ Zulip: '
echo
export ZULIP_API_KEY
go run ./cmd/collector channels
```

`channels` показывает доступные публичные каналы с БИВ22–24 в названии. Для канала с другим названием возьми числовой ID из ссылки в Zulip.

## Сбор и обновление

```sh
go run ./cmd/collector live -channels 2392,2428 -from 2024-09-01 -to 2026-11-16
```

Обе даты включены по московскому времени. После прерывания повтори команду с теми же датами и ботом. Повторный проход после завершения перечитывает доступную историю и обновляет сообщения по ID. Чтобы собрать новый день, увеличь `-to`.

## Выгрузка

```sh
mkdir -p exports
go run ./cmd/collector export -channels 2392,2428 -from 2024-09-01 -to 2026-11-16 -format json -out exports/biv24.json
go run ./cmd/collector export -channels 2392,2428 -from 2024-09-01 -to 2026-11-16 -format csv -out exports/biv24.csv
```

В выгрузке есть текст, автор, дата, канал, тема и ссылка на оригинал. Для проверки открой `url` в Zulip.
