package storage

import (
	"context"
	"testing"
	"time"

	"zulip-collector/internal/collector"
)

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	msg := collector.Message{
		ID:          10001,
		AuthorID:    101,
		AuthorName:  "Иван Петров",
		ChannelID:   501,
		ChannelName: "Программирование",
		Topic:       "Домашняя работа",
		Text:        "Первый вариант сообщения",
		URL:         "https://example.org/messages/10001",
		CreatedAt:   time.Date(2024, 10, 15, 14, 35, 0, 0, time.UTC),
	}

	progress := collector.Progress{
		Cursor: "10001",
		Done:   false,
	}

	// первая запись
	result, err := store.SavePage(
		ctx, "test-sync", []collector.Message{msg}, progress,
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.Inserted != 1 || result.Updated != 0 {
		t.Fatalf("unexpected first result: %+v", result)
	}

	// повторно сохраняем то же сообщение
	result, err = store.SavePage(
		ctx, "test-sync", []collector.Message{msg}, progress,
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.Inserted != 0 || result.Updated != 0 {
		t.Fatalf("duplicate detected: %+v", result)
	}

	// имитируем редактирование сообщения
	msg.Text = "Исправленный вариант сообщения"
	msg.Topic = "Исправления"
	edited := msg.CreatedAt.Add(time.Hour)
	msg.EditedAt = &edited

	result, err = store.SavePage(
		ctx, "test-sync", []collector.Message{msg}, progress,
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.Updated != 1 {
		t.Fatalf("expected one update, got %+v", result)
	}

	saved := store.Messages()

	if len(saved) != 1 {
		t.Fatalf("expected 1 message, got %d", len(saved))
	}

	if saved[0].Text != msg.Text || saved[0].Topic != msg.Topic {
		t.Fatal("message changes were not saved")
	}

	// проверяем сохранение курсора
	lastProgress, err := store.LoadProgress(ctx, "test-sync")
	if err != nil {
		t.Fatal(err)
	}

	if lastProgress.Cursor != "10001" || lastProgress.Done {
		t.Fatalf("unexpected progress: %+v", lastProgress)
	}
}
