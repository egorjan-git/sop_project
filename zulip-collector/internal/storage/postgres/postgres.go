package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"zulip-collector/internal/collector"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// получаем состояние предыдущей загрузки
func (s *PostgresStore) LoadProgress(
	ctx context.Context,
	scope string,
) (collector.Progress, error) {
	if strings.TrimSpace(scope) == "" {
		return collector.Progress{}, fmt.Errorf("empty scope")
	}

	var progress collector.Progress

	err := s.db.QueryRowContext(
		ctx,
		`SELECT cursor, is_complete
		 FROM zulip.sync_state
		 WHERE scope_id = $1`,
		scope,
	).Scan(&progress.Cursor, &progress.Done)

	// ещё ни разу не запускали этот scope
	if errors.Is(err, sql.ErrNoRows) {
		return collector.Progress{}, nil
	}

	if err != nil {
		return collector.Progress{}, fmt.Errorf(
			"load progress: %w", err,
		)
	}

	return progress, nil
}

// прежний способ сохранения страниц
func (s *PostgresStore) SavePage(
	ctx context.Context,
	scope string,
	messages []collector.Message,
	progress collector.Progress,
) (collector.SaveResult, error) {
	return s.savePage(ctx, scope, messages, progress, nil)
}

// сохранение страницы вместе с её статистикой
func (s *PostgresStore) SavePageWithMetadata(
	ctx context.Context,
	scope string,
	messages []collector.Message,
	progress collector.Progress,
	metadata collector.PageMetadata,
) (collector.SaveResult, error) {
	return s.savePage(ctx, scope, messages, progress, &metadata)
}

// сохраняем сообщения и курсор в одной транзакции
func (s *PostgresStore) savePage(
	ctx context.Context,
	scope string,
	messages []collector.Message,
	progress collector.Progress,
	metadata *collector.PageMetadata,
) (collector.SaveResult, error) {
	var result collector.SaveResult

	if strings.TrimSpace(scope) == "" {
		return result, fmt.Errorf("empty scope")
	}

	// проверяем согласованность метаданных страницы
	if metadata != nil {
		if metadata.FetchedMessages < len(messages) {
			return result, fmt.Errorf(
				"selected messages exceed fetched messages",
			)
		}

		if metadata.FetchedMessages > 0 &&
			metadata.MaxSeenMessageID <= 0 {
			return result, fmt.Errorf(
				"missing maximum message ID",
			)
		}

		if metadata.FetchedMessages == 0 &&
			metadata.MaxSeenMessageID != 0 {
			return result, fmt.Errorf(
				"empty page has a message ID",
			)
		}
	}

	seen := make(map[int64]bool)

	// проверяем данные до начала записи
	for _, msg := range messages {
		if msg.ID <= 0 ||
			msg.AuthorID <= 0 ||
			msg.ChannelID <= 0 ||
			msg.CreatedAt.IsZero() ||
			strings.TrimSpace(msg.URL) == "" ||
			strings.TrimSpace(msg.AuthorName) == "" ||
			strings.TrimSpace(msg.ChannelName) == "" {
			return result, fmt.Errorf(
				"invalid message: %d", msg.ID,
			)
		}

		if seen[msg.ID] {
			return result, fmt.Errorf(
				"duplicate ID in page: %d", msg.ID,
			)
		}

		seen[msg.ID] = true
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// блокируем checkpoint на время обработки страницы
	var previousCursor string
	var wasComplete bool
	var runID sql.NullInt64
	var checkpointExists bool

	err = tx.QueryRowContext(ctx, `
		SELECT cursor, is_complete, last_run_id
		FROM zulip.sync_state
		WHERE scope_id = $1
		FOR UPDATE
	`, scope).Scan(
		&previousCursor,
		&wasComplete,
		&runID,
	)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		// первый пакет может ещё не иметь checkpoint
		if metadata != nil && metadata.PreviousCursor != "" {
			return result, fmt.Errorf(
				"checkpoint is missing for cursor %q",
				metadata.PreviousCursor,
			)
		}

	case err != nil:
		return result, fmt.Errorf(
			"lock checkpoint: %w", err,
		)

	default:
		checkpointExists = true
	}

	// управляемые запуски должны передавать статистику
	if runID.Valid && metadata == nil {
		return result, fmt.Errorf(
			"managed run requires page metadata",
		)
	}

	if metadata != nil {
		if checkpointExists {
			if wasComplete {
				return result, fmt.Errorf(
					"checkpoint is already complete",
				)
			}

			if previousCursor != metadata.PreviousCursor {
				return result, fmt.Errorf(
					"stale page: expected cursor %q, got %q",
					previousCursor,
					metadata.PreviousCursor,
				)
			}
		}

		// незавершённая страница должна продвигать курсор
		if !progress.Done &&
			progress.Cursor == metadata.PreviousCursor {
			return result, fmt.Errorf(
				"page did not advance cursor",
			)
		}

		// статус запуска проверяем под блокировкой
		if runID.Valid {
			var status string

			err = tx.QueryRowContext(ctx, `
				SELECT status
				FROM zulip.sync_runs
				WHERE run_id = $1
				AND scope_id = $2
				FOR UPDATE
			`, runID.Int64, scope).Scan(&status)

			if err != nil {
				return result, fmt.Errorf(
					"lock sync run: %w", err,
				)
			}

			if status != "running" {
				return result, fmt.Errorf(
					"run %d is not running", runID.Int64,
				)
			}
		}
	}

	for _, msg := range messages {
		// канал должен быть явно разрешён в базе
		var approved bool

		err := tx.QueryRowContext(
			ctx,
			`SELECT approved_public
			 FROM zulip.channels
			 WHERE channel_id = $1
			 FOR SHARE`,
			msg.ChannelID,
		).Scan(&approved)

		if errors.Is(err, sql.ErrNoRows) {
			return collector.SaveResult{}, fmt.Errorf(
				"channel %d is not registered",
				msg.ChannelID,
			)
		}
		if err != nil {
			return collector.SaveResult{}, err
		}
		if !approved {
			return collector.SaveResult{}, fmt.Errorf(
				"channel %d is not approved",
				msg.ChannelID,
			)
		}

		// добавляем автора или обновляем его имя
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO zulip.users (user_id, display_name)
			 VALUES ($1, $2)
			 ON CONFLICT (user_id) DO UPDATE
			 SET display_name = EXCLUDED.display_name,
			     updated_at = NOW()
			 WHERE zulip.users.display_name
			       IS DISTINCT FROM EXCLUDED.display_name`,
			msg.AuthorID,
			msg.AuthorName,
		)
		if err != nil {
			return collector.SaveResult{}, fmt.Errorf(
				"save author %d: %w", msg.AuthorID, err,
			)
		}

		// название канала может измениться
		_, err = tx.ExecContext(
			ctx,
			`UPDATE zulip.channels
			 SET name = $2, updated_at = NOW()
			 WHERE channel_id = $1
			   AND name IS DISTINCT FROM $2`,
			msg.ChannelID,
			msg.ChannelName,
		)
		if err != nil {
			return collector.SaveResult{}, err
		}

		// пробуем вставить новое сообщение
		inserted, err := tx.ExecContext(
			ctx,
			`INSERT INTO zulip.messages (
			    message_id, author_id, channel_id,
			    topic, content, sent_at, edited_at, source_url
			 )
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 ON CONFLICT (message_id) DO NOTHING`,
			msg.ID,
			msg.AuthorID,
			msg.ChannelID,
			msg.Topic,
			msg.Text,
			msg.CreatedAt,
			msg.EditedAt,
			msg.URL,
		)
		if err != nil {
			return collector.SaveResult{}, fmt.Errorf(
				"insert message %d: %w", msg.ID, err,
			)
		}

		count, err := inserted.RowsAffected()
		if err != nil {
			return collector.SaveResult{}, err
		}

		if count == 1 {
			result.Inserted++
			continue
		}

		// сообщение уже есть, обновляем только изменённые поля
		updated, err := tx.ExecContext(
			ctx,
			`UPDATE zulip.messages
			 SET author_id = $2,
			     channel_id = $3,
			     topic = $4,
			     content = $5,
			     sent_at = $6,
			     edited_at = $7,
			     source_url = $8,
			     updated_at = NOW()
			 WHERE message_id = $1
			   AND (
			       author_id, channel_id, topic, content,
			       sent_at, edited_at, source_url
			   ) IS DISTINCT FROM (
			       $2, $3, $4, $5, $6, $7, $8
			   )`,
			msg.ID,
			msg.AuthorID,
			msg.ChannelID,
			msg.Topic,
			msg.Text,
			msg.CreatedAt,
			msg.EditedAt,
			msg.URL,
		)
		if err != nil {
			return collector.SaveResult{}, fmt.Errorf(
				"update message %d: %w", msg.ID, err,
			)
		}

		count, err = updated.RowsAffected()
		if err != nil {
			return collector.SaveResult{}, err
		}

		if count == 1 {
			result.Updated++
		}
	}

	// сохраняем курсор в той же транзакции
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO zulip.sync_state (
		    scope_id, cursor, is_complete
		 )
		 VALUES ($1, $2, $3)
		 ON CONFLICT (scope_id) DO UPDATE
		 SET cursor = EXCLUDED.cursor,
		     is_complete = EXCLUDED.is_complete,
		     updated_at = NOW()`,
		scope,
		progress.Cursor,
		progress.Done,
	)
	if err != nil {
		return collector.SaveResult{}, fmt.Errorf(
			"save progress: %w", err,
		)
	}

	// счётчики обновляем только для управляемых запусков
	if metadata != nil && runID.Valid {
		_, err = tx.ExecContext(ctx, `
			UPDATE zulip.sync_runs
			SET processed_pages =
					COALESCE(processed_pages, 0) + 1,
				fetched_messages =
					COALESCE(fetched_messages, 0) + $2,
				selected_messages =
					COALESCE(selected_messages, 0) + $3,
				inserted_messages =
					COALESCE(inserted_messages, 0) + $4,
				updated_messages =
					COALESCE(updated_messages, 0) + $5
			WHERE run_id = $1
			AND status = 'running'
		`,
			runID.Int64,
			metadata.FetchedMessages,
			len(messages),
			result.Inserted,
			result.Updated,
		)

		if err != nil {
			return collector.SaveResult{}, fmt.Errorf(
				"update page statistics: %w", err,
			)
		}

		// храним ID отдельно от курсора пагинации
		if metadata.MaxSeenMessageID > 0 {
			_, err = tx.ExecContext(ctx, `
				UPDATE zulip.sync_state
				SET last_seen_message_id =
					GREATEST(
						COALESCE(last_seen_message_id, 0),
						$2
					)
				WHERE scope_id = $1
			`, scope, metadata.MaxSeenMessageID)

			if err != nil {
				return collector.SaveResult{}, fmt.Errorf(
					"save last seen message ID: %w", err,
				)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return collector.SaveResult{}, fmt.Errorf(
			"commit transaction: %w", err,
		)
	}

	return result, nil
}

// проверяем соответствие интерфейсу коллектора
var _ collector.PageMetadataStore = (*PostgresStore)(nil)
