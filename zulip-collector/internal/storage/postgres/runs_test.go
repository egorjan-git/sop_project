package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"zulip-collector/internal/collector"
	"zulip-collector/internal/storage/postgres"
	"zulip-collector/internal/syncmanager"
)

func TestRunRepository(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_URL")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_URL is not set")
	}

	ctx, cancel := context.WithTimeout(
		context.Background(), 20*time.Second,
	)
	defer cancel()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}

	// тест не должен изменять основную базу проекта
	var databaseName string
	err = db.QueryRowContext(
		ctx, "SELECT current_database()",
	).Scan(&databaseName)

	if err != nil || databaseName != "sop_collector_test" {
		db.Close()
		t.Fatalf("unexpected database: %q, %v", databaseName, err)
	}

	base := time.Now().UnixNano()
	channelID := base + 1
	targetID := fmt.Sprintf("test-run-target-%d", base)

	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		defer stop()
		defer db.Close()

		queries := []struct {
			query string
			arg   any
		}{
			{
				`DELETE FROM zulip.sync_errors
				 WHERE run_id IN (
				     SELECT run_id FROM zulip.sync_runs
				     WHERE target_id = $1
				 )`,
				targetID,
			},
			{
				`DELETE FROM zulip.sync_state
				 WHERE target_id = $1`,
				targetID,
			},
			{
				`DELETE FROM zulip.sync_runs
				 WHERE target_id = $1`,
				targetID,
			},
			{
				`DELETE FROM zulip.sync_targets
				 WHERE target_id = $1`,
				targetID,
			},
			{
				`DELETE FROM zulip.channels
				 WHERE channel_id = $1`,
				channelID,
			},
		}

		for _, item := range queries {
			if _, err := db.ExecContext(
				cleanupCtx, item.query, item.arg,
			); err != nil {
				t.Errorf("cleanup failed: %v", err)
			}
		}
	})

	// создаём разрешённый тестовый канал
	_, err = db.ExecContext(ctx, `
		INSERT INTO zulip.channels (
			channel_id, name, approved_public
		)
		VALUES ($1, 'Канал для теста запусков', TRUE)
	`, channelID)
	if err != nil {
		t.Fatal(err)
	}

	store := postgres.NewPostgresStore(db)

	// регистрируем постоянное задание
	target := syncmanager.Target{
		ID:               targetID,
		ChannelID:        channelID,
		PeriodStart:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:        time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		SelectionVersion: 1,
	}

	_, err = store.CreateTarget(ctx, target)
	if err != nil {
		t.Fatal(err)
	}

	// запускаем initial
	first, err := store.StartRun(
		ctx, targetID, syncmanager.ModeInitial,
	)
	if err != nil {
		t.Fatal(err)
	}

	// у нового запуска должен сразу существовать checkpoint
	checkpoint, err := store.GetRunCheckpoint(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}

	if checkpoint.RunID != first.ID ||
		checkpoint.ScopeID != first.ScopeID ||
		checkpoint.TargetID != targetID {
		t.Fatalf("checkpoint belongs to another run: %+v", checkpoint)
	}

	if checkpoint.Cursor != "" || checkpoint.Done {
		t.Fatalf("new checkpoint must be empty: %+v", checkpoint)
	}

	// имитируем страницу, на которой источник просмотрел одно сообщение, но после фильтрации не осталось ни одного сообщения целевого студента
	_, err = store.SavePageWithMetadata(
		ctx,
		first.ScopeID,
		nil,
		collector.Progress{
			Cursor: "10005",
			Done:   false,
		},
		collector.PageMetadata{
			PreviousCursor:   "",
			FetchedMessages:  1,
			MaxSeenMessageID: 10005,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// проверяем, что статистика сохранилась в postgres
	partial, err := store.GetRun(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}

	if partial.Stats.ProcessedPages != 1 ||
		partial.Stats.FetchedMessages != 1 ||
		partial.Stats.SelectedMessages != 0 ||
		partial.Stats.InsertedMessages != 0 {
		t.Fatalf(
			"incorrect page statistics: %+v",
			partial.Stats,
		)
	}

	// курсор уже продвинулся, повторять страницу нельзя
	_, err = store.SavePageWithMetadata(
		ctx,
		first.ScopeID,
		nil,
		collector.Progress{
			Cursor: "10005",
			Done:   false,
		},
		collector.PageMetadata{
			PreviousCursor:   "",
			FetchedMessages:  1,
			MaxSeenMessageID: 10005,
		},
	)
	if err == nil {
		t.Fatal("stale page was accepted")
	}

	// проверяем последний просмотренный ID
	var lastSeen sql.NullInt64

	err = db.QueryRowContext(ctx, `
	SELECT last_seen_message_id
	FROM zulip.sync_state
	WHERE scope_id = $1
`, first.ScopeID).Scan(&lastSeen)

	if err != nil {
		t.Fatal(err)
	}

	if !lastSeen.Valid || lastSeen.Int64 != 10005 {
		t.Fatalf("unexpected last seen ID: %+v", lastSeen)
	}

	// снова читаем checkpoint через RunRepository
	checkpoint, err = store.GetRunCheckpoint(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}

	if checkpoint.Cursor != "10005" ||
		checkpoint.Done ||
		checkpoint.RunID != first.ID {
		t.Fatalf("checkpoint was not updated correctly: %+v", checkpoint)
	}

	// новый объект хранилища должен видеть то же состояние
	reopened := postgres.NewPostgresStore(db)

	restored, err := reopened.GetRunCheckpoint(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}

	if restored.Cursor != checkpoint.Cursor ||
		restored.RunID != checkpoint.RunID {
		t.Fatalf("checkpoint was not restored: %+v", restored)
	}

	if first.ID <= 0 ||
		first.ScopeID == "" ||
		first.Status != syncmanager.RunRunning {
		t.Fatalf("invalid first run: %+v", first)
	}

	// второй запуск того же target пока запрещён
	_, err = store.StartRun(
		ctx, targetID, syncmanager.ModeIncremental,
	)
	if !errors.Is(err, syncmanager.ErrRunAlreadyActive) {
		t.Fatalf("expected active run error, got: %v", err)
	}

	// читаем созданный запуск
	loaded, err := store.GetRun(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ScopeID != first.ScopeID ||
		loaded.Mode != syncmanager.ModeInitial {
		t.Fatalf("unexpected run: %+v", loaded)
	}

	// имитируем ошибку первого запуска
	err = store.FailRun(
		ctx, first.ID, "synthetic source failure",
	)
	if err != nil {
		t.Fatal(err)
	}

	failed, err := store.GetRun(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != syncmanager.RunFailed ||
		failed.FinishedAt == nil ||
		failed.ErrorSummary == nil {
		t.Fatalf("failure was not recorded: %+v", failed)
	}

	// проверяем отдельную запись в sync_errors
	var errorCount int

	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM zulip.sync_errors
		WHERE run_id = $1
	`, first.ID).Scan(&errorCount)

	if err != nil || errorCount != 1 {
		t.Fatalf("expected one error, got %d: %v", errorCount, err)
	}

	// после неудачного запуска можно создать новый
	second, err := store.StartRun(
		ctx, targetID, syncmanager.ModeIncremental,
	)
	if err != nil {
		t.Fatal(err)
	}

	// второй запуск получает независимый checkpoint
	secondCheckpoint, err := store.GetRunCheckpoint(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}

	if secondCheckpoint.ScopeID == first.ScopeID ||
		secondCheckpoint.Cursor != "" ||
		secondCheckpoint.RunID != second.ID {
		t.Fatalf("second run reused old checkpoint: %+v", secondCheckpoint)
	}

	if second.ScopeID == first.ScopeID {
		t.Fatal("two runs must not share a checkpoint scope")
	}

	// незавершённый checkpoint не позволяет закрыть run
	err = store.CompleteRun(ctx, second.ID)

	if !errors.Is(err, syncmanager.ErrCheckpointIncomplete) {
		t.Fatalf(
			"expected incomplete checkpoint error, got: %v",
			err,
		)
	}

	// проверяем, что после отказа run остался работающим
	stillRunning, err := store.GetRun(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}

	if stillRunning.Status != syncmanager.RunRunning {
		t.Fatalf(
			"run status changed unexpectedly: %s",
			stillRunning.Status,
		)
	}

	// имитируем последнюю успешно обработанную страницу
	_, err = store.SavePageWithMetadata(
		ctx,
		second.ScopeID,
		nil,
		collector.Progress{
			Cursor: "10008",
			Done:   true,
		},
		collector.PageMetadata{
			PreviousCursor:   "",
			FetchedMessages:  2,
			MaxSeenMessageID: 10008,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// проверяем, что checkpoint действительно завершён
	finishedCheckpoint, err := store.GetRunCheckpoint(
		ctx, second.ID,
	)
	if err != nil {
		t.Fatal(err)
	}

	if !finishedCheckpoint.Done ||
		finishedCheckpoint.Cursor != "10008" {
		t.Fatalf(
			"unexpected checkpoint: %+v",
			finishedCheckpoint,
		)
	}

	// теперь завершаем запуск без передачи счётчиков
	err = store.CompleteRun(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}

	completed, err := store.GetRun(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}

	// ожидаем именно те счётчики, которые записала страница
	expectedStats := syncmanager.RunStats{
		ProcessedPages:  1,
		FetchedMessages: 2,
	}

	if completed.Status != syncmanager.RunCompleted ||
		completed.FinishedAt == nil ||
		completed.Stats != expectedStats {
		t.Fatalf(
			"unexpected completed run: %+v",
			completed,
		)
	}

	// повторно завершить тот же run нельзя
	err = store.CompleteRun(ctx, second.ID)

	if !errors.Is(err, syncmanager.ErrRunNotRunning) {
		t.Fatalf(
			"expected status error, got: %v",
			err,
		)
	}
}
