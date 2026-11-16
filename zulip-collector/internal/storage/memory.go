package storage

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"zulip-collector/internal/collector"
)

type MemoryStore struct {
	mu       sync.Mutex
	messages map[int64]collector.Message
	progress map[string]collector.Progress
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		messages: make(map[int64]collector.Message),
		progress: make(map[string]collector.Progress),
	}
}

// получаем последний сохранённый курсор
func (s *MemoryStore) LoadProgress(
	ctx context.Context,
	scope string,
) (collector.Progress, error) {
	if err := ctx.Err(); err != nil {
		return collector.Progress{}, err
	}

	if scope == "" {
		return collector.Progress{}, fmt.Errorf("empty scope")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.progress[scope], nil
}

// сохраняем порцию сообщений и позицию загрузки
func (s *MemoryStore) SavePage(
	ctx context.Context,
	scope string,
	messages []collector.Message,
	progress collector.Progress,
) (collector.SaveResult, error) {
	var result collector.SaveResult

	if scope == "" {
		return result, fmt.Errorf("empty scope")
	}

	if err := ctx.Err(); err != nil {
		return result, err
	}

	// сначала проверяем всю порцию, чтобы не сохранить её частично
	seen := make(map[int64]bool)

	for _, msg := range messages {
		if msg.ID <= 0 {
			return result, fmt.Errorf("invalid message ID: %d", msg.ID)
		}

		if seen[msg.ID] {
			return result, fmt.Errorf("duplicate ID in page: %d", msg.ID)
		}

		seen[msg.ID] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return result, err
	}

	for _, msg := range messages {
		old, exists := s.messages[msg.ID]

		if !exists {
			s.messages[msg.ID] = cloneMessage(msg)
			result.Inserted++
			continue
		}

		// уже есть такое сообщение значт проверяем изменения
		if !sameMessage(old, msg) {
			s.messages[msg.ID] = cloneMessage(msg)
			result.Updated++
		}
	}

	// курсор обновляем только после записи всей порции
	s.progress[scope] = progress

	return result, nil
}

// возвращаем все сообщения по возрастанию ID
func (s *MemoryStore) Messages() []collector.Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]collector.Message, 0, len(s.messages))

	for _, msg := range s.messages {
		result = append(result, cloneMessage(msg))
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	return result
}

// копируем и время редактирования, чтобы не хранить чужой указатель
func cloneMessage(msg collector.Message) collector.Message {
	if msg.EditedAt != nil {
		edited := *msg.EditedAt
		msg.EditedAt = &edited
	}

	return msg
}

// сравниваем содержимое, а не адреса указателей
func sameMessage(a, b collector.Message) bool {
	return a.ID == b.ID &&
		a.AuthorID == b.AuthorID &&
		a.AuthorName == b.AuthorName &&
		a.ChannelID == b.ChannelID &&
		a.ChannelName == b.ChannelName &&
		a.Topic == b.Topic &&
		a.Text == b.Text &&
		a.URL == b.URL &&
		a.CreatedAt.Equal(b.CreatedAt) &&
		sameTime(a.EditedAt, b.EditedAt)
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	return a.Equal(*b)
}

// проверяем, что MemoryStore подходит под наш интерфейс
var _ collector.MessageStore = (*MemoryStore)(nil)
