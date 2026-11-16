package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"

	"zulip-collector/internal/collector"
)

// FixtureSource имитирует источник сообщений зулип
type FixtureSource struct {
	messages []collector.Message
	pageSize int
}

// NewFixtureSource загружает сообщения из JSON
// allowedChannels содержит id разрешённых публичных каналов
func NewFixtureSource(
	path string,
	pageSize int,
	allowedChannels map[int64]bool,
) (*FixtureSource, error) {
	if pageSize <= 0 {
		return nil, fmt.Errorf("pageSize must be positive")
	}

	if len(allowedChannels) == 0 {
		return nil, fmt.Errorf("no allowed public channels")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixture: %w", err)
	}

	var allMessages []collector.Message
	if err := json.Unmarshal(data, &allMessages); err != nil {
		return nil, fmt.Errorf("decode fixture: %w", err)
	}

	messages := make([]collector.Message, 0)
	seen := make(map[int64]bool)

	for _, msg := range allMessages {
		// канал не разрешён поэтому сообщение пропускаем
		if !allowedChannels[msg.ChannelID] {
			continue
		}

		if msg.ID <= 0 || msg.AuthorID <= 0 {
			return nil, fmt.Errorf("invalid message ID or author ID")
		}

		if seen[msg.ID] {
			return nil, fmt.Errorf("duplicate message ID: %d", msg.ID)
		}

		seen[msg.ID] = true
		messages = append(messages, msg)
	}

	// порядок сообщений должен быть стабильным
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].ID < messages[j].ID
	})

	return &FixtureSource{
		messages: messages,
		pageSize: pageSize,
	}, nil
}

// FetchPage возвращает следующую порцию сообщений
// cursor это id последнего обработанного сообщения
func (s *FixtureSource) FetchPage(
	ctx context.Context,
	cursor string,
) (collector.Page, error) {
	if err := ctx.Err(); err != nil {
		return collector.Page{}, err
	}

	var lastID int64

	if cursor != "" {
		id, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || id < 0 {
			return collector.Page{}, fmt.Errorf(
				"invalid cursor: %q", cursor,
			)
		}
		lastID = id
	}

	// ищем первое сообщение с id больше курсора
	start := sort.Search(len(s.messages), func(i int) bool {
		return s.messages[i].ID > lastID
	})

	if start >= len(s.messages) {
		return collector.Page{
			NextCursor: cursor,
			Done:       true,
		}, nil
	}

	end := start + s.pageSize
	if end > len(s.messages) {
		end = len(s.messages)
	}

	batch := append(
		[]collector.Message(nil),
		s.messages[start:end]...,
	)

	nextCursor := strconv.FormatInt(
		batch[len(batch)-1].ID, 10,
	)

	return collector.Page{
		Messages:   batch,
		NextCursor: nextCursor,
		Done:       end == len(s.messages),
	}, nil
}

var _ collector.MessageSource = (*FixtureSource)(nil)
