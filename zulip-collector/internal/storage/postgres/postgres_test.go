package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"zulip-collector/internal/collector"
	"zulip-collector/internal/storage/postgres"
)

func TestPostgresStore(t *testing.T) {
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

	// проверяем, что тест случайно не запустили на рабочей базе
	var databaseName string
	err = db.QueryRowContext(ctx,
		"SELECT current_database()",
	).Scan(&databaseName)

	if err != nil || databaseName != "sop_collector_test" {
		db.Close()
		t.Fatalf("unexpected test database: %q, %v", databaseName, err)
	}

	// отдельные ID для каждого запуска теста
	base := time.Now().UnixMicro()
	userID := base + 1
	channelID := base + 2
	blockedChannelID := base + 3
	messageID := base + 4
	nextMessageID := base + 5
	blockedMessageID := base + 6
	scope := fmt.Sprintf("postgres-test-%d", base)

	// после теста удаляем только созданные им записи
	t.Cleanup(func() {
		cleanupCtx := context.Background()

		_, _ = db.ExecContext(cleanupCtx,
			"DELETE FROM zulip.sync_state WHERE scope_id = $1", scope)
		_, _ = db.ExecContext(cleanupCtx,
			"DELETE FROM zulip.messages WHERE message_id IN ($1, $2, $3)",
			messageID, nextMessageID, blockedMessageID)
		_, _ = db.ExecContext(cleanupCtx,
			"DELETE FROM zulip.users WHERE user_id = $1", userID)
		_, _ = db.ExecContext(cleanupCtx,
			"DELETE FROM zulip.channels WHERE channel_id IN ($1, $2)",
			channelID, blockedChannelID)

		_ = db.Close()
	})

	// регистрируем разрешённый и запрещённый тестовые каналы
	_, err = db.ExecContext(ctx, `
		INSERT INTO zulip.channels
		    (channel_id, name, approved_public)
		VALUES
		    ($1, 'Тестовый публичный', TRUE),
		    ($2, 'Тестовый запрещённый', FALSE)
	`, channelID, blockedChannelID)
	if err != nil {
		t.Fatal(err)
	}

	store := postgres.NewPostgresStore(db)

	msg := collector.Message{
		ID:          messageID,
		AuthorID:    userID,
		AuthorName:  "Тестовый студент",
		ChannelID:   channelID,
		ChannelName: "Тестовый публичный",
		Topic:       "Go",
		Text:        "Первоначальный текст",
		URL:         fmt.Sprintf("https://example.org/%d", messageID),
		CreatedAt:   time.Date(2024, 10, 15, 12, 0, 0, 0, time.UTC),
	}

	progress := collector.Progress{
		Cursor: fmt.Sprint(messageID),
		Done:   false,
	}

	// сохраняем сообщение первый раз
	result, err := store.SavePage(
		ctx, scope, []collector.Message{msg}, progress,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 1 {
		t.Fatalf("expected one insert, got %+v", result)
	}

	// повторная запись не должна создавать дубликаты
	result, err = store.SavePage(
		ctx, scope, []collector.Message{msg}, progress,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 0 || result.Updated != 0 {
		t.Fatalf("unexpected duplicate: %+v", result)
	}

	// имитируем редактирование сообщения
	msg.Text = "Исправленный текст"
	msg.Topic = "Новая тема"
	msg.URL = fmt.Sprintf(
		"https://example.org/edited/%d", messageID,
	)
	edited := msg.CreatedAt.Add(time.Hour)
	msg.EditedAt = &edited

	result, err = store.SavePage(
		ctx, scope, []collector.Message{msg}, progress,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated != 1 {
		t.Fatalf("expected one update, got %+v", result)
	}

	var savedText, savedTopic, savedURL string
	err = db.QueryRowContext(ctx, `
		SELECT content, topic, source_url
		FROM zulip.messages
		WHERE message_id = $1
	`, messageID).Scan(&savedText, &savedTopic, &savedURL)

	if err != nil {
		t.Fatal(err)
	}
	if savedText != msg.Text ||
		savedTopic != msg.Topic ||
		savedURL != msg.URL {
		t.Fatal("message changes were not saved")
	}

	// первая запись допустима, вторая находится в запрещённом канале
	good := msg
	good.ID = nextMessageID

	bad := msg
	bad.ID = blockedMessageID
	bad.ChannelID = blockedChannelID
	bad.ChannelName = "Тестовый запрещённый"

	failedProgress := collector.Progress{
		Cursor: fmt.Sprint(blockedMessageID),
		Done:   true,
	}

	_, err = store.SavePage(
		ctx, scope,
		[]collector.Message{good, bad},
		failedProgress,
	)
	if err == nil {
		t.Fatal("expected transaction to fail")
	}

	// проверяем, что вся неудачная порция откатилась
	var count int
	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM zulip.messages
		WHERE message_id IN ($1, $2)
	`, nextMessageID, blockedMessageID).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed transaction left messages in database")
	}

	// создаём новый объект хранилища
	// курсор должен сохраниться после предыдущих операций
	reopened := postgres.NewPostgresStore(db)

	savedProgress, err := reopened.LoadProgress(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if savedProgress.Cursor != progress.Cursor ||
		savedProgress.Done {
		t.Fatalf("cursor changed after rollback: %+v", savedProgress)
	}

	// повторяем сохранение с исправленной порцией
	result, err = reopened.SavePage(
		ctx, scope, []collector.Message{good},
		collector.Progress{
			Cursor: fmt.Sprint(nextMessageID),
			Done:   true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 1 {
		t.Fatalf("resume failed: %+v", result)
	}

	finalProgress, err := reopened.LoadProgress(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if !finalProgress.Done ||
		finalProgress.Cursor != fmt.Sprint(nextMessageID) {
		t.Fatalf("unexpected final progress: %+v", finalProgress)
	}
}
