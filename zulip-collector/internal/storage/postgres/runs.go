package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"zulip-collector/internal/syncmanager"
)

// создаём новый запуск для существующего target
func (s *PostgresStore) StartRun(
	ctx context.Context,
	targetID string,
	mode syncmanager.Mode,
) (syncmanager.Run, error) {
	if strings.TrimSpace(targetID) == "" ||
		targetID != strings.TrimSpace(targetID) {
		return syncmanager.Run{}, fmt.Errorf("invalid target ID")
	}

	if !mode.Valid() {
		return syncmanager.Run{}, fmt.Errorf(
			"invalid sync mode: %q", mode,
		)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return syncmanager.Run{}, fmt.Errorf(
			"begin run transaction: %w", err,
		)
	}
	defer tx.Rollback()

	// фиксируем конфигурацию задания на момент старта
	var target syncmanager.Target
	var config []byte
	var approved bool

	err = tx.QueryRowContext(ctx, `
		SELECT
			t.channel_id,
			t.period_start,
			t.period_end,
			t.selection_version,
			t.config,
			t.enabled,
			c.approved_public
		FROM zulip.sync_targets AS t
		JOIN zulip.channels AS c
			ON c.channel_id = t.channel_id
		WHERE t.target_id = $1
		FOR UPDATE OF t
		FOR SHARE OF c
	`, targetID).Scan(
		&target.ChannelID,
		&target.PeriodStart,
		&target.PeriodEnd,
		&target.SelectionVersion,
		&config,
		&target.Enabled,
		&approved,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return syncmanager.Run{}, fmt.Errorf(
			"%w: %s",
			syncmanager.ErrTargetNotFound,
			targetID,
		)
	}
	if err != nil {
		return syncmanager.Run{}, fmt.Errorf(
			"load target: %w", err,
		)
	}

	if !target.Enabled {
		return syncmanager.Run{},
			syncmanager.ErrTargetDisabled
	}

	if !approved {
		return syncmanager.Run{},
			syncmanager.ErrChannelNotAllowed
	}

	// второй running для того же target запрещён
	var active bool

	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM zulip.sync_runs
			WHERE target_id = $1
			  AND status = 'running'
		)
	`, targetID).Scan(&active)
	if err != nil {
		return syncmanager.Run{}, err
	}

	if active {
		return syncmanager.Run{},
			syncmanager.ErrRunAlreadyActive
	}

	// сохраняем версию выборки вместе с конфигурацией
	snapshot, err := json.Marshal(struct {
		ChannelID        int64           `json:"channel_id"`
		SelectionVersion int             `json:"selection_version"`
		TargetConfig     json.RawMessage `json:"target_config"`
	}{
		ChannelID:        target.ChannelID,
		SelectionVersion: target.SelectionVersion,
		TargetConfig:     json.RawMessage(config),
	})
	if err != nil {
		return syncmanager.Run{}, fmt.Errorf(
			"encode run config: %w", err,
		)
	}

	// каждый run получает собственный scope для checkpoint
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return syncmanager.Run{}, fmt.Errorf(
			"generate run scope: %w", err,
		)
	}

	scopeID := fmt.Sprintf("run-%x", token)

	run := syncmanager.Run{
		TargetID:    targetID,
		ScopeID:     scopeID,
		Mode:        mode,
		Status:      syncmanager.RunRunning,
		PeriodStart: target.PeriodStart,
		PeriodEnd:   target.PeriodEnd,
		ScopeConfig: snapshot,
	}

	err = tx.QueryRowContext(ctx, `
		INSERT INTO zulip.sync_runs (
			target_id,
			scope_id,
			mode,
			status,
			period_start,
			period_end,
			scope_config,
			started_at
		)
		VALUES (
			$1, $2, $3, 'running',
			$4, $5, $6::jsonb, NOW()
		)
		RETURNING run_id, started_at
	`,
		run.TargetID,
		run.ScopeID,
		string(run.Mode),
		run.PeriodStart,
		run.PeriodEnd,
		string(run.ScopeConfig),
	).Scan(&run.ID, &run.StartedAt)

	if err != nil {
		return syncmanager.Run{}, fmt.Errorf(
			"create sync run: %w", err,
		)
	}

	// создаём checkpoint для только что зарегистрированного запуска
	// обе записи появятся в базе только после общего commit
	_, err = tx.ExecContext(ctx, `
	INSERT INTO zulip.sync_state (
		scope_id,
		cursor,
		is_complete,
		target_id,
		last_run_id
	)
	VALUES ($1, '', FALSE, $2, $3)
	`,
		run.ScopeID,
		run.TargetID,
		run.ID,
	)

	if err != nil {
		return syncmanager.Run{}, fmt.Errorf(
			"create checkpoint for run %d: %w",
			run.ID,
			err,
		)
	}

	if err := tx.Commit(); err != nil {
		return syncmanager.Run{}, fmt.Errorf(
			"commit sync run: %w", err,
		)
	}

	return run, nil
}

// читаем ранее зарегистрированный запуск
func (s *PostgresStore) GetRun(
	ctx context.Context,
	runID int64,
) (syncmanager.Run, error) {
	var run syncmanager.Run

	var mode, status string
	var config []byte
	var limited sql.NullBool
	var summary sql.NullString
	var finished sql.NullTime

	err := s.db.QueryRowContext(ctx, `
		SELECT
			run_id,
			target_id,
			scope_id,
			mode,
			status,
			period_start,
			period_end,
			scope_config,
			COALESCE(processed_pages, 0),
			COALESCE(fetched_messages, 0),
			COALESCE(selected_messages, 0),
			COALESCE(inserted_messages, 0),
			COALESCE(updated_messages, 0),
			history_limited,
			error_summary,
			started_at,
			finished_at
		FROM zulip.sync_runs
		WHERE run_id = $1
		  AND target_id IS NOT NULL
	`, runID).Scan(
		&run.ID,
		&run.TargetID,
		&run.ScopeID,
		&mode,
		&status,
		&run.PeriodStart,
		&run.PeriodEnd,
		&config,
		&run.Stats.ProcessedPages,
		&run.Stats.FetchedMessages,
		&run.Stats.SelectedMessages,
		&run.Stats.InsertedMessages,
		&run.Stats.UpdatedMessages,
		&limited,
		&summary,
		&run.StartedAt,
		&finished,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return syncmanager.Run{},
			syncmanager.ErrRunNotFound
	}
	if err != nil {
		return syncmanager.Run{}, fmt.Errorf(
			"get sync run: %w", err,
		)
	}

	run.Mode = syncmanager.Mode(mode)
	run.Status = syncmanager.RunStatus(status)
	run.ScopeConfig = json.RawMessage(config)

	if limited.Valid {
		value := limited.Bool
		run.HistoryLimited = &value
	}
	if summary.Valid {
		value := summary.String
		run.ErrorSummary = &value
	}
	if finished.Valid {
		value := finished.Time
		run.FinishedAt = &value
	}

	return run, nil
}

// завершаем успешный запуск и записываем статистику

func (s *PostgresStore) CompleteRun(
	ctx context.Context,
	runID int64,
) error {
	if runID <= 0 {
		return fmt.Errorf("invalid run ID")
	}

	// сначала узнаём, какой checkpoint принадлежит запуску
	// это чтение без блокировки, ниже всё перепроверим
	var scopeID string
	var targetID string

	err := s.db.QueryRowContext(ctx, `
		SELECT scope_id, target_id
		FROM zulip.sync_runs
		WHERE run_id = $1
		  AND target_id IS NOT NULL
	`, runID).Scan(&scopeID, &targetID)

	if errors.Is(err, sql.ErrNoRows) {
		return syncmanager.ErrRunNotFound
	}
	if err != nil {
		return fmt.Errorf("find sync run: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin completion transaction: %w", err)
	}
	defer tx.Rollback()

	// сначала блокируем checkpoint
	// SavePage использует такой же порядок блокировок
	var checkpointTarget string
	var checkpointRunID int64
	var checkpointDone bool

	err = tx.QueryRowContext(ctx, `
		SELECT target_id, last_run_id, is_complete
		FROM zulip.sync_state
		WHERE scope_id = $1
		FOR UPDATE
	`, scopeID).Scan(
		&checkpointTarget,
		&checkpointRunID,
		&checkpointDone,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf(
			"%w: run %d",
			syncmanager.ErrCheckpointNotFound,
			runID,
		)
	}
	if err != nil {
		return fmt.Errorf("lock checkpoint: %w", err)
	}

	// теперь блокируем строку самого запуска
	var storedScope string
	var storedTarget string
	var status string

	err = tx.QueryRowContext(ctx, `
		SELECT scope_id, target_id, status
		FROM zulip.sync_runs
		WHERE run_id = $1
		FOR UPDATE
	`, runID).Scan(
		&storedScope,
		&storedTarget,
		&status,
	)
	if err != nil {
		return fmt.Errorf("lock sync run: %w", err)
	}

	if status != string(syncmanager.RunRunning) {
		return syncmanager.ErrRunNotRunning
	}

	// убеждаемся, что связи не изменились
	if storedScope != scopeID ||
		storedTarget != targetID ||
		checkpointTarget != targetID ||
		checkpointRunID != runID {
		return fmt.Errorf(
			"checkpoint does not belong to run %d",
			runID,
		)
	}

	// нельзя завершить запуск, если обход ещё продолжается
	if !checkpointDone {
		return syncmanager.ErrCheckpointIncomplete
	}

	// счётчики не трогаем: они уже накоплены по страницам
	_, err = tx.ExecContext(ctx, `
		UPDATE zulip.sync_runs
		SET status = 'completed',
		    error_summary = NULL,
		    finished_at = NOW()
		WHERE run_id = $1
		  AND status = 'running'
	`, runID)

	if err != nil {
		return fmt.Errorf("complete sync run: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit completed run: %w", err)
	}

	return nil
}

// фиксируем неудачный запуск вместе с записью об ошибке
func (s *PostgresStore) FailRun(
	ctx context.Context,
	runID int64,
	summary string,
) error {
	if runID <= 0 {
		return fmt.Errorf("invalid run ID")
	}

	summary = strings.TrimSpace(summary)
	if summary == "" {
		return fmt.Errorf("error summary must not be empty")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin failure transaction: %w", err)
	}
	defer tx.Rollback()

	var failedID int64

	err = tx.QueryRowContext(ctx, `
		UPDATE zulip.sync_runs
		SET status = 'failed',
		    error_summary = $2,
		    finished_at = NOW()
		WHERE run_id = $1
		  AND target_id IS NOT NULL
		  AND status = 'running'
		RETURNING run_id
	`, runID, summary).Scan(&failedID)

	if errors.Is(err, sql.ErrNoRows) {
		return syncmanager.ErrRunNotRunning
	}
	if err != nil {
		return fmt.Errorf("fail sync run: %w", err)
	}

	// храним только безопасное описание ошибки
	_, err = tx.ExecContext(ctx, `
		INSERT INTO zulip.sync_errors (
			run_id,
			category,
			message,
			retryable
		)
		VALUES ($1, 'run_failure', $2, FALSE)
	`, failedID, summary)
	if err != nil {
		return fmt.Errorf("save sync error: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit failed run: %w", err)
	}

	return nil
}

// проверяем реализацию интерфейса на этапе компиляции
var _ syncmanager.RunRepository = (*PostgresStore)(nil)
