# Импорт таблиц и каталога ресурсов

Запускай команды из корня `resource-importer`. Нужны Go 1.26+ и PostgreSQL. Для миграций нужен `psql`.

## Подключение к PostgreSQL

Задай `DATABASE_URL` общей или локальной базы. Подставь свои параметры подключения. Если таблицы уже созданы, пропусти миграции.

```sh
export DATABASE_URL='postgresql://USER@HOST:5432/DBNAME?sslmode=require'
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f migrations/001_resource_catalog.sql \
  -f migrations/002_sheet_snapshots.sql
```

`sheet_snapshots` хранит вкладки и ячейки. `resource_catalog` хранит источники и проверенные ссылки.

## Загрузка материалов

Получи у команды рабочие файлы `sources.csv` и `reviewed-resources.csv` и положи их в `data/`. При подключении к уже заполненной базе укажи прежние значения `-source` в переменных `BOOKS_SOURCE_ID` и `LINKS_SOURCE_ID`, чтобы обновить существующие записи.

```sh
go run . import -file data/sources.csv -source "${BOOKS_SOURCE_ID:-sources}"
```

Для Google Таблиц введи ключ Sheets API в терминале zsh. Запусти команду `import-snapshots` для каждой ссылки Google из `data/sources.csv`.

```zsh
read -rs 'GOOGLE_SHEETS_API_KEY?Ключ Sheets API: '
printf '\n'
export GOOGLE_SHEETS_API_KEY
go run . import-snapshots -sheet 'URL_ИЛИ_ID_КНИГИ'
```

Для закрытых книг можно использовать сервисный аккаунт с доступом на чтение. Сними `GOOGLE_SHEETS_API_KEY` через `unset` и укажи путь к JSON-ключу в `GOOGLE_APPLICATION_CREDENTIALS`. В проверке пяти книг использовался API-ключ.

Скачай книгу Excel по ссылке из реестра. В команде укажи путь к файлу и `id` книги из реестра. При обновлении передавай тот же `-id`.

```sh
go run . import-snapshots -xlsx '/путь/к/книге.xlsx' -id 'ID_ИЗ_РЕЕСТРА'
```

После импорта книг загрузи готовый список проверенных ссылок. Команда с `-review` заменяет список для указанного `-source`.

```sh
go run . catalog-links -sources data/sources.csv \
  -review data/reviewed-resources.csv -source "${LINKS_SOURCE_ID:-sheet-links}"
```

Для поиска новых ссылок выполни `go run . catalog-links -sources data/sources.csv -candidates-out candidates.csv`. Используй новый путь для файла кандидатов. При проверке сохраняй `id` и `url` выбранных записей.

Входные CSV используют поля `id,name,url,discipline,cohort,availability`. Обязательны `id`, `name` и `url`. Чтобы проверить импорт CSV без подключения к базе, выполни `go run . import -file examples/resources.csv -source example -dry-run`. Ожидается `read=3`.

## Выгрузка JSON и CSV

Для выгрузки достаточно доступа на чтение к обеим таблицам. Команды создают новую папку и выводят её путь.

```sh
export_dir=$(mktemp -d "${TMPDIR:-/tmp}/resource-importer.XXXXXXXX") &&
  go run . export-snapshots -format json -out "$export_dir/sheets.json" &&
  go run . export-snapshots -format csv -out "$export_dir/sheets.csv" &&
  go run . export -format json -out "$export_dir/resource-catalog.json" &&
  go run . export -format csv -out "$export_dir/resource-catalog.csv" &&
  printf '%s\n' "$export_dir"
```

`sheets.json` содержит снимки вкладок. В `sheets.csv` одна строка соответствует одной сохранённой ячейке. Файлы `resource-catalog.*` содержат записи каталога.

При повторном импорте с теми же идентификаторами дублей не будет. Если данные совпадают, команда вернёт `changed=0`.
