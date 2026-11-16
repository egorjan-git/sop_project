package collector

import (
	"context"
	"fmt"
	"testing"
)

// источник возвращает одну страницу из трёх сообщений
type metadataTestSource struct{}

func (metadataTestSource) FetchPage(
	ctx context.Context,
	cursor string,
) (Page, error) {
	if cursor != "" {
		return Page{}, fmt.Errorf(
			"unexpected cursor: %s", cursor,
		)
	}

	return Page{
		Messages: []Message{
			{ID: 10001, AuthorID: 101},
			{ID: 10002, AuthorID: 103},
			{ID: 10005, AuthorID: 104},
		},
		NextCursor: "10005",
		Done:       true,
	}, nil
}

// тестовое хранилище фиксирует вызов метода
type metadataTestStore struct {
	progress Progress
	messages []Message
	metadata PageMetadata
	calls    int
}

func (s *metadataTestStore) LoadProgress(
	ctx context.Context,
	scope string,
) (Progress, error) {
	return s.progress, nil
}

// этот метод не должен вызываться в данном тесте
func (s *metadataTestStore) SavePage(
	ctx context.Context,
	scope string,
	messages []Message,
	progress Progress,
) (SaveResult, error) {
	return SaveResult{}, fmt.Errorf(
		"legacy SavePage was called",
	)
}

func (s *metadataTestStore) SavePageWithMetadata(
	ctx context.Context,
	scope string,
	messages []Message,
	progress Progress,
	metadata PageMetadata,
) (SaveResult, error) {
	s.calls++
	s.messages = append([]Message(nil), messages...)
	s.metadata = metadata
	s.progress = progress

	return SaveResult{
		Inserted: len(messages),
	}, nil
}

func TestCollectorUsesPageMetadata(t *testing.T) {
	store := &metadataTestStore{}

	job := Collector{
		Source: metadataTestSource{},
		Store:  store,
	}

	// пользователь 103 намеренно не входит в выборку
	authors := map[int64]bool{
		101: true,
		104: true,
	}

	stats, err := job.Sync(
		context.Background(),
		"metadata-test",
		authors,
	)
	if err != nil {
		t.Fatal(err)
	}

	if store.calls != 1 {
		t.Fatalf(
			"expected one metadata save, got %d",
			store.calls,
		)
	}

	if len(store.messages) != 2 {
		t.Fatalf(
			"expected two selected messages, got %d",
			len(store.messages),
		)
	}

	if store.messages[0].ID != 10001 ||
		store.messages[1].ID != 10005 {
		t.Fatalf(
			"unexpected selected messages: %+v",
			store.messages,
		)
	}

	if store.metadata.PreviousCursor != "" ||
		store.metadata.FetchedMessages != 3 ||
		store.metadata.MaxSeenMessageID != 10005 {
		t.Fatalf(
			"incorrect page metadata: %+v",
			store.metadata,
		)
	}

	if !store.progress.Done ||
		store.progress.Cursor != "10005" {
		t.Fatalf(
			"incorrect final progress: %+v",
			store.progress,
		)
	}

	if stats.Pages != 1 ||
		stats.Selected != 2 ||
		stats.Inserted != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}
