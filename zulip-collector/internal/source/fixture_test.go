package source

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFixtureSource(t *testing.T) {
	path := filepath.Join(
		"..", "..", "testdata", "messages.json",
	)

	source, err := NewFixtureSource(
		path,
		2,
		map[int64]bool{
			501: true,
			502: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// первая порция
	first, err := source.FetchPage(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(first.Messages) != 2 {
		t.Fatalf(
			"expected 2 messages, got %d",
			len(first.Messages),
		)
	}

	if first.Done || first.NextCursor != "10002" {
		t.Fatal("unexpected first page state")
	}

	//продолжение с сохранённого курсора
	second, err := source.FetchPage(
		ctx, first.NextCursor,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(second.Messages) != 2 {
		t.Fatalf(
			"expected 2 messages, got %d",
			len(second.Messages),
		)
	}

	if !second.Done {
		t.Fatal("expected end of messages")
	}

	if second.Messages[0].ID != 10003 ||
		second.Messages[1].ID != 10005 {
		t.Fatal("unexpected message IDs")
	}

	// запрещённый тестовый канал не попал в выдачу
	for _, page := range []struct {
		messages []int64
	}{
		{[]int64{first.Messages[0].ID, first.Messages[1].ID}},
		{[]int64{second.Messages[0].ID, second.Messages[1].ID}},
	} {
		for _, id := range page.messages {
			if id == 10004 {
				t.Fatal("excluded message was returned")
			}
		}
	}
}
