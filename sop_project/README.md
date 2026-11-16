# Импорт ведомостей

Выполни [настройку PostgreSQL](../README.md#postgresql-через-docker-compose), затем из корня `main`:

```sh
cd sop_project
```

## Импорт

Google Таблицы, из терминала zsh:

```zsh
read -rs 'GOOGLE_SHEETS_API_KEY?Ключ Sheets API: '
echo
export GOOGLE_SHEETS_API_KEY
go run . import-snapshots -sheet 'URL_ИЛИ_ID_КНИГИ'
```

Для закрытой книги с доступом у сервисного аккаунта:

```sh
unset GOOGLE_SHEETS_API_KEY
export GOOGLE_APPLICATION_CREDENTIALS='/путь/к/ключу.json'
go run . import-snapshots -sheet 'URL_ИЛИ_ID_КНИГИ'
```

Excel и локальные снимки:

```sh
go run . import-snapshots -xlsx 'book.xlsx' -id 'stable-book-id'
go run . import-snapshots -dir 'snapshots'
```

При повторном импорте Excel используй прежний `-id`.

## Выгрузка

```sh
go run . export-snapshots -format json -out sheets.json
go run . export-snapshots -format csv -out sheets.csv
```

Для повторной выгрузки укажи новые имена файлов.

## Учебная история студента

Найди ID аккаунта, книги и листа:

```sh
psql "$DATABASE_URL" -c 'SELECT user_id, display_name FROM zulip.users ORDER BY display_name;'
psql "$DATABASE_URL" -c 'SELECT spreadsheet_id, spreadsheet_title, sheet_id, sheet_title FROM public.sheet_snapshots ORDER BY spreadsheet_title, sheet_index;'
```

После сверки личности подставь подтверждённые ID и значение из выбранного столбца:

```sh
student_id=$(go run . student-history create -cohort 'БИВ24')
go run . student-history link-zulip -student "$student_id" -user 12345
go run . student-history link-sheet -student "$student_id" \
  -book 'BOOK_ID' -sheet 0 -column 3 -value 'student@example.org' -header-row 1
go run . student-history show -student "$student_id" -out student-history.json
```

Нумерация столбцов начинается с 1. `-header-row` задаёт последнюю строку заголовка или `0` для листа без заголовка. Повтори `link-sheet` для каждого нужного листа. Для повторной выгрузки используй тот же `student_id` и новое имя файла.
