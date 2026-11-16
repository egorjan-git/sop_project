# Импорт таблиц

Задай `DATABASE_URL` общей PostgreSQL по [инструкции проекта](../README.md). Команды ниже выполняются из папки импортёра `sop_project`.

## Загрузка

Для доступных по ссылке Google Таблиц в терминале zsh:

```zsh
read -rs 'GOOGLE_SHEETS_API_KEY?Ключ Sheets API: '
echo
export GOOGLE_SHEETS_API_KEY
go run . import-snapshots -sheet 'URL_ИЛИ_ID_КНИГИ'
```

Для закрытой книги задай `GOOGLE_APPLICATION_CREDENTIALS` с путём к ключу сервисного аккаунта, которому открыт доступ, и убери `GOOGLE_SHEETS_API_KEY`.

Импорт Excel и локальных снимков:

```sh
go run . import-snapshots -xlsx 'book.xlsx' -id 'stable-book-id'
go run . import-snapshots -dir 'snapshots'
```

При обновлении Excel используй прежний `-id`. Повторная загрузка тех же данных возвращает `changed=0`.

## Выгрузка

```sh
go run . export-snapshots -format json -out sheets.json
go run . export-snapshots -format csv -out sheets.csv
```

JSON содержит снимок на лист. В CSV одна строка соответствует одной сохранённой ячейке. Команды создают новые файлы, поэтому при повторной выгрузке укажи другие имена.
