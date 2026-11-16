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

## Учебная история студента

Нужна общая база со схемой `integration`, созданной при [настройке PostgreSQL](../README.md). Выполни запросы через `psql "$DATABASE_URL"` или в **Query Console** в GoLand:

```sql
SELECT user_id, display_name FROM zulip.users ORDER BY display_name;
SELECT spreadsheet_id, spreadsheet_title, sheet_id, sheet_title
FROM public.sheet_snapshots ORDER BY spreadsheet_title, sheet_index;
```

После сверки личности создай запись и привяжи аккаунт Zulip и нужные листы. Замени `12345`, `BOOK_ID`, номер листа и значение на проверенные данные:

```sh
student_id=$(go run . student-history create -cohort 'БИВ24')
go run . student-history link-zulip -student "$student_id" -user 12345
go run . student-history link-sheet -student "$student_id" \
  -book 'BOOK_ID' -sheet 0 -column 3 -value 'student@example.org' -header-row 1
go run . student-history show -student "$student_id" -out student-history.json
```

`-sheet` — ID листа из запроса, `-column` — номер столбца с 1. Укажи точное значение проверенной почты или логина. Повтори `link-sheet` для каждого нужного листа. `-header-row` задаёт последнюю строку заголовка, а `0` означает, что заголовков нет. Для повторной выгрузки используй прежний `student_id` и новое имя файла.

`show` выводит сообщения связанных аккаунтов и подтверждённые строки листов. При выгрузке строка заново ищется по сохранённому идентификатору. Если значение исчезло или встретилось несколько раз, в JSON появится статус `identity_not_found` или `identity_ambiguous`.
