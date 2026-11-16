package history

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestHistoryAcrossBothParsers(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_URL")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_URL is not set")
	}
	ctx := t.Context()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var dbName string
	if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&dbName); err != nil || dbName != "sop_collector_test" {
		t.Fatalf("unexpected test database %q: %v", dbName, err)
	}

	seed := time.Now().UnixMicro()
	bookID := fmt.Sprintf("history-test-%d", seed)
	var studentID, otherID int64
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, operation := range []struct {
			query string
			args  []any
		}{
			{"DELETE FROM integration.student_sheet_links WHERE student_id IN ($1,$2)", []any{studentID, otherID}},
			{"DELETE FROM integration.sheet_records WHERE spreadsheet_id=$1", []any{bookID}},
			{"DELETE FROM public.sheet_snapshots WHERE spreadsheet_id=$1", []any{bookID}},
			{"DELETE FROM integration.zulip_student_links WHERE student_id IN ($1,$2)", []any{studentID, otherID}},
			{"DELETE FROM integration.students WHERE student_id IN ($1,$2)", []any{studentID, otherID}},
			{"DELETE FROM zulip.messages WHERE channel_id=$1", []any{seed}},
			{"DELETE FROM zulip.channels WHERE channel_id=$1", []any{seed}},
			{"DELETE FROM zulip.users WHERE user_id IN ($1,$2)", []any{seed, seed + 1}},
		} {
			if _, err := db.ExecContext(cleanup, operation.query, operation.args...); err != nil {
				t.Error(err)
			}
		}
	})
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO zulip.users (user_id, display_name) VALUES ($1,'Студент'),($2,'Другой')", seed, seed+1)
	exec("INSERT INTO zulip.channels (channel_id,name,approved_public) VALUES ($1,'Курс',true)", seed)
	exec(`INSERT INTO zulip.messages (message_id, author_id, channel_id, content, sent_at, source_url)
	      VALUES ($1,$1,$1,'Моё сообщение',now(),'https://example.org/1'),
	             ($2,$2,$1,'Чужое сообщение',now(),'https://example.org/2')`, seed, seed+1)
	const cells = `[
	  {"row":1,"column":1,"data":{"formattedValue":"Email"}},
	  {"row":1,"column":2,"data":{"formattedValue":"Имя"}},
	  {"row":2,"column":1,"data":{"formattedValue":"demo@example.org"}},
	  {"row":2,"column":2,"data":{"formattedValue":"Тестовый ученик"}},
	  {"row":3,"column":1,"data":{"formattedValue":"other@example.org"}},
	  {"row":3,"column":2,"data":{"formattedValue":"Другой ученик"}}
	]`
	exec(`INSERT INTO public.sheet_snapshots
	      (spreadsheet_id,sheet_id,spreadsheet_title,sheet_title,sheet_index,row_count,column_count,hidden,cells)
	      VALUES ($1,0,'Книга','Лист',0,3,2,false,$2)`, bookID, cells)

	studentID, err = CreateStudent(ctx, db, "БИВ24")
	if err != nil {
		t.Fatal(err)
	}
	otherID, err = CreateStudent(ctx, db, "БИВ24")
	if err != nil {
		t.Fatal(err)
	}
	if err := LinkZulip(ctx, db, studentID, seed); err != nil {
		t.Fatal(err)
	}
	if err := LinkZulip(ctx, db, otherID, seed); err == nil {
		t.Fatal("one Zulip user was linked to two students")
	}
	email := SheetMatch{Column: 1, Value: "demo@example.org", HeaderRow: 1}
	for range 2 {
		if row, err := LinkSheet(ctx, db, studentID, bookID, 0, email); err != nil || row != 2 {
			t.Fatalf("link row = %d, error = %v", row, err)
		}
	}
	exec(`UPDATE public.sheet_snapshots AS s SET cells = (
		SELECT jsonb_agg(CASE WHEN (cell->>'row')::int > 1
			THEN jsonb_set(cell, '{row}', to_jsonb(5 - (cell->>'row')::int))
			ELSE cell END)
		FROM jsonb_array_elements(s.cells) AS cell
	) WHERE spreadsheet_id=$1`, bookID)
	name := SheetMatch{Column: 2, Value: "Тестовый ученик", HeaderRow: 1}
	if _, err := LinkSheet(ctx, db, otherID, bookID, 0, name); err == nil || !strings.Contains(err.Error(), "already confirmed for another student") {
		t.Fatalf("expected conflicting owner after reorder, got %v", err)
	}
	if _, err := LinkSheet(ctx, db, studentID, bookID, 0, name); err == nil {
		t.Fatal("same row was linked twice using different columns")
	}
	exec(`WITH new_record AS (
		INSERT INTO integration.sheet_records (spreadsheet_id,sheet_id,source_key,source_row)
		VALUES ($1,0,'unconfirmed',2) RETURNING record_id
	) INSERT INTO integration.student_sheet_links (student_id, record_id)
	  SELECT $2, record_id FROM new_record`, bookID, studentID)

	read := func() SheetRecord {
		t.Helper()
		got, err := Load(ctx, db, studentID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Student.Cohort != "БИВ24" || len(got.Accounts) != 1 ||
			len(got.Messages) != 1 || got.Messages[0].ID != seed || len(got.Records) != 1 {
			t.Fatalf("unexpected history: %+v", got)
		}
		return got.Records[0]
	}
	if r := read(); r.Row != 3 || r.Status != "confirmed" || len(r.Cells) != 2 || len(r.Headers) != 2 {
		t.Fatalf("reordered record: %+v", r)
	}
	exec(`UPDATE public.sheet_snapshots AS s SET cells = (
		SELECT jsonb_agg(CASE WHEN cell->>'row'='2' AND cell->>'column'='1'
			THEN jsonb_set(cell,'{data,formattedValue}','"demo@example.org"') ELSE cell END)
		FROM jsonb_array_elements(s.cells) AS cell
	) WHERE spreadsheet_id=$1`, bookID)
	if r := read(); r.Row != 0 || r.Status != "identity_ambiguous" || len(r.Cells) != 0 {
		t.Fatalf("ambiguous record: %+v", r)
	}
}
