package collector

import "context"

// служебная информация об обработанной странице
type PageMetadata struct {
	PreviousCursor string

	// количество сообщений до фильтрации студентов
	FetchedMessages int

	// максимальный ID среди всех сообщений страницы
	// ноль означает, что страница не содержала сообщений
	MaxSeenMessageID int64
}

// дополнительный интерфейс для хранилищ
// которые поддерживают статистику страниц
type PageMetadataStore interface {
	SavePageWithMetadata(
		ctx context.Context,
		scope string,
		messages []Message,
		progress Progress,
		metadata PageMetadata,
	) (SaveResult, error)
}

func buildPageMetadata(
	page Page,
	previousCursor string,
) PageMetadata {
	var maxID int64

	// учитываем все сообщения страницы до фильтрации авторов
	for _, msg := range page.Messages {
		if msg.ID > maxID {
			maxID = msg.ID
		}
	}

	return PageMetadata{
		PreviousCursor:   previousCursor,
		FetchedMessages:  len(page.Messages),
		MaxSeenMessageID: maxID,
	}
}
