package collector

import (
	"context"
	"fmt"
	"time"
)

type Message struct {
	ID int64 `json:"id"`

	AuthorID   int64  `json:"author_id"`
	AuthorName string `json:"author_name"`

	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`

	Topic string `json:"topic"`
	Text  string `json:"text"`
	URL   string `json:"url"`

	CreatedAt time.Time  `json:"created_at"`
	EditedAt  *time.Time `json:"edited_at,omitempty"`
}

type Membership struct {
	UserID  int64 `json:"user_id"`
	GroupID int64 `json:"group_id"`
}

// Page одна порция сообщений от источника
type Page struct {
	Messages   []Message
	NextCursor string
	Done       bool
}

// MessageSource независимо от Zulip и JSON
type MessageSource interface {
	FetchPage(
		ctx context.Context,
		cursor string,
	) (Page, error)
}

// Progress состояние загрузки
type Progress struct {
	Cursor string
	Done   bool
}

type SaveResult struct {
	Inserted int
	Updated  int
}

// MessageStore постоянное или тестовое хранилище
type MessageStore interface {
	LoadProgress(
		ctx context.Context,
		scope string,
	) (Progress, error)

	// сообщения и курсор должны сохраняться атомарно
	SavePage(
		ctx context.Context,
		scope string,
		messages []Message,
		progress Progress,
	) (SaveResult, error)
}

type Stats struct {
	Pages    int
	Selected int
	Inserted int
	Updated  int
}

type Collector struct {
	Source MessageSource
	Store  MessageStore
}

// TargetAuthors определяет целевых пользователей
func TargetAuthors(
	memberships []Membership,
	targetGroups map[int64]bool,
) map[int64]bool {
	authors := make(map[int64]bool)

	for _, membership := range memberships {
		if targetGroups[membership.GroupID] {
			authors[membership.UserID] = true
		}
	}

	return authors
}

// Sync обрабатывает доступные сообщения постранично
func (c *Collector) Sync(
	ctx context.Context,
	scope string,
	authors map[int64]bool,
) (Stats, error) {
	var stats Stats

	progress, err := c.Store.LoadProgress(ctx, scope)
	if err != nil {
		return stats, err
	}

	for !progress.Done {
		if err := ctx.Err(); err != nil {
			return stats, err
		}

		// запоминаем курсор, с которого запросили страницу
		previousCursor := progress.Cursor

		page, err := c.Source.FetchPage(ctx, previousCursor)
		if err != nil {
			return stats, fmt.Errorf("fetch page: %w", err)
		}

		// считаем метаданные до фильтрации студентов
		metadata := buildPageMetadata(page, previousCursor)

		if !page.Done &&
			page.NextCursor == progress.Cursor {
			return stats, fmt.Errorf(
				"source cursor did not advance",
			)
		}

		selected := make([]Message, 0)
		for _, msg := range page.Messages {
			if authors == nil || authors[msg.AuthorID] {
				selected = append(selected, msg)
			}
		}

		next := Progress{
			Cursor: page.NextCursor,
			Done:   page.Done,
		}

		var result SaveResult

		if store, ok := c.Store.(PageMetadataStore); ok {
			// postgres умеет сохранять статистику страницы
			result, err = store.SavePageWithMetadata(
				ctx,
				scope,
				selected,
				next,
				metadata,
			)
		} else {
			// обычное хранилище сохраняет только сообщения и курсор
			result, err = c.Store.SavePage(
				ctx,
				scope,
				selected,
				next,
			)
		}

		if err != nil {
			return stats, fmt.Errorf("save page: %w", err)
		}

		stats.Pages++
		stats.Selected += len(selected)
		stats.Inserted += result.Inserted
		stats.Updated += result.Updated

		progress = next
	}

	return stats, nil
}
