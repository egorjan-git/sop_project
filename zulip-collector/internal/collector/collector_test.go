package collector_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zulip-collector/internal/collector"
	"zulip-collector/internal/source"
	"zulip-collector/internal/storage"
)

// имитируем ошибку при получении второй страницы
type failingSource struct {
	original collector.MessageSource
	calls    int
}

func (s *failingSource) FetchPage(
	ctx context.Context,
	cursor string,
) (collector.Page, error) {
	s.calls++

	if s.calls == 2 {
		return collector.Page{}, errors.New("test connection lost")
	}

	return s.original.FetchPage(ctx, cursor)
}

func TestCollectorResumeAndReconcile(t *testing.T) {
	ctx := context.Background()

	path := filepath.Join(
		"..", "..", "testdata", "messages.json",
	)

	allowed := map[int64]bool{
		501: true,
		502: true,
	}

	fixture, err := source.NewFixtureSource(path, 2, allowed)
	if err != nil {
		t.Fatal(err)
	}

	authors := map[int64]bool{
		101: true,
		102: true,
		104: true,
	}

	store := storage.NewMemoryStore()

	job := collector.Collector{
		Source: &failingSource{original: fixture},
		Store:  store,
	}

	// первый запуск прерывается на второй странице
	stats, err := job.Sync(ctx, "initial", authors)

	if err == nil {
		t.Fatal("expected source error")
	}

	if stats.Pages != 1 || stats.Inserted != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	progress, err := store.LoadProgress(ctx, "initial")
	if err != nil {
		t.Fatal(err)
	}

	if progress.Cursor != "10002" || progress.Done {
		t.Fatalf("unexpected checkpoint: %+v", progress)
	}

	// теперь источник работает нормально
	// продолжаем с уже сохранённого курсора
	job.Source = fixture

	stats, err = job.Sync(ctx, "initial", authors)
	if err != nil {
		t.Fatal(err)
	}

	if stats.Inserted != 2 || stats.Pages != 1 {
		t.Fatalf("resume failed: %+v", stats)
	}

	saved := store.Messages()

	if len(saved) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(saved))
	}

	// проверяем что нужные сообщения на месте
	expected := []int64{10001, 10003, 10005}

	for i, id := range expected {
		if saved[i].ID != id {
			t.Fatalf(
				"expected ID %d, got %d",
				id, saved[i].ID,
			)
		}

		if saved[i].URL == "" {
			t.Fatalf("message %d has no URL", id)
		}
	}

	// тот же завершённый scope не должен загружаться снова
	stats, err = job.Sync(ctx, "initial", authors)
	if err != nil {
		t.Fatal(err)
	}

	if stats.Pages != 0 || stats.Inserted != 0 {
		t.Fatalf("finished job ran again: %+v", stats)
	}

	// теперь имитируем изменение старого сообщения
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var messages []collector.Message
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}

	for i := range messages {
		if messages[i].ID == 10001 {
			messages[i].Text = "Исправленный текст"
			messages[i].Topic = "Новая тема"

			edited := messages[i].CreatedAt.Add(time.Hour)
			messages[i].EditedAt = &edited
		}
	}

	// создаём отдельный временный JSON
	updatedJSON, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}

	updatedPath := filepath.Join(
		t.TempDir(), "updated_messages.json",
	)

	if err := os.WriteFile(
		updatedPath, updatedJSON, 0600,
	); err != nil {
		t.Fatal(err)
	}

	updatedSource, err := source.NewFixtureSource(
		updatedPath, 2, allowed,
	)
	if err != nil {
		t.Fatal(err)
	}

	// новый scope позволяет повторно проверить историю
	job.Source = updatedSource

	stats, err = job.Sync(ctx, "reconcile-1", authors)
	if err != nil {
		t.Fatal(err)
	}

	if stats.Inserted != 0 || stats.Updated != 1 {
		t.Fatalf("reconcile failed: %+v", stats)
	}

	saved = store.Messages()

	if saved[0].Text != "Исправленный текст" ||
		saved[0].Topic != "Новая тема" {
		t.Fatal("message was not updated")
	}

	if saved[0].URL == "" {
		t.Fatal("message URL was lost")
	}
}
