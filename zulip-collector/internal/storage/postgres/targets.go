package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"zulip-collector/internal/syncmanager"
)

// создаём новое задание синхронизации
func (s *PostgresStore) CreateTarget(
	ctx context.Context,
	target syncmanager.Target,
) (syncmanager.Target, error) {
	if err := validateTarget(target); err != nil {
		return syncmanager.Target{}, err
	}

	if len(target.Config) == 0 {
		target.Config = json.RawMessage(`{}`)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return syncmanager.Target{}, fmt.Errorf(
			"begin target transaction: %w", err,
		)
	}
	defer tx.Rollback()

	// нельзя создавать задание для неодобренного канала
	var approved bool

	err = tx.QueryRowContext(ctx, `
		SELECT approved_public
		FROM zulip.channels
		WHERE channel_id = $1
		FOR SHARE
	`, target.ChannelID).Scan(&approved)

	if errors.Is(err, sql.ErrNoRows) {
		return syncmanager.Target{}, fmt.Errorf(
			"channel %d is not registered",
			target.ChannelID,
		)
	}
	if err != nil {
		return syncmanager.Target{}, fmt.Errorf(
			"check channel: %w", err,
		)
	}
	if !approved {
		return syncmanager.Target{}, fmt.Errorf(
			"channel %d is not approved",
			target.ChannelID,
		)
	}

	// создаём задание, но пока не устанавливаем watermark
	row := tx.QueryRowContext(ctx, `
		INSERT INTO zulip.sync_targets (
			target_id,
			channel_id,
			period_start,
			period_end,
			selection_version,
			config
		)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
		RETURNING
			target_id,
			channel_id,
			period_start,
			period_end,
			selection_version,
			config,
			high_watermark_id,
			last_successful_at,
			enabled,
			created_at,
			updated_at
	`,
		target.ID,
		target.ChannelID,
		target.PeriodStart.UTC(),
		target.PeriodEnd.UTC(),
		target.SelectionVersion,
		string(target.Config),
	)

	created, err := scanTarget(row)
	if err != nil {
		return syncmanager.Target{}, fmt.Errorf(
			"create target %s: %w", target.ID, err,
		)
	}

	if err := tx.Commit(); err != nil {
		return syncmanager.Target{}, fmt.Errorf(
			"commit target %s: %w", target.ID, err,
		)
	}

	return created, nil
}

// получаем задание из базы по его ID
func (s *PostgresStore) GetTarget(
	ctx context.Context,
	targetID string,
) (syncmanager.Target, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			target_id,
			channel_id,
			period_start,
			period_end,
			selection_version,
			config,
			high_watermark_id,
			last_successful_at,
			enabled,
			created_at,
			updated_at
		FROM zulip.sync_targets
		WHERE target_id = $1
	`, targetID)

	target, err := scanTarget(row)

	if errors.Is(err, sql.ErrNoRows) {
		return syncmanager.Target{}, fmt.Errorf(
			"%w: %s",
			syncmanager.ErrTargetNotFound,
			targetID,
		)
	}
	if err != nil {
		return syncmanager.Target{}, fmt.Errorf(
			"get target %s: %w", targetID, err,
		)
	}

	return target, nil
}

// проверяем параметры перед записью в базу
func validateTarget(target syncmanager.Target) error {
	if strings.TrimSpace(target.ID) == "" ||
		target.ID != strings.TrimSpace(target.ID) {
		return fmt.Errorf("invalid target ID")
	}

	if target.ChannelID <= 0 {
		return fmt.Errorf("invalid channel ID")
	}

	if target.PeriodStart.IsZero() ||
		target.PeriodEnd.IsZero() ||
		!target.PeriodEnd.After(target.PeriodStart) {
		return fmt.Errorf("invalid target period")
	}

	if target.SelectionVersion <= 0 {
		return fmt.Errorf("invalid selection version")
	}

	if len(target.Config) > 0 {
		var config map[string]json.RawMessage

		if err := json.Unmarshal(target.Config, &config); err != nil {
			return fmt.Errorf("invalid target config: %w", err)
		}

		if config == nil {
			return fmt.Errorf("target config must be a JSON object")
		}
	}

	return nil
}

// преобразуем одну SQL-строку в модель задания
func scanTarget(
	row interface{ Scan(...any) error },
) (syncmanager.Target, error) {
	var target syncmanager.Target

	var config []byte
	var watermark sql.NullInt64
	var lastSuccessful sql.NullTime

	err := row.Scan(
		&target.ID,
		&target.ChannelID,
		&target.PeriodStart,
		&target.PeriodEnd,
		&target.SelectionVersion,
		&config,
		&watermark,
		&lastSuccessful,
		&target.Enabled,
		&target.CreatedAt,
		&target.UpdatedAt,
	)
	if err != nil {
		return syncmanager.Target{}, err
	}

	target.Config = json.RawMessage(config)

	if watermark.Valid {
		value := watermark.Int64
		target.HighWatermarkID = &value
	}

	if lastSuccessful.Valid {
		value := lastSuccessful.Time
		target.LastSuccessfulAt = &value
	}

	return target, nil
}

// проверяем, что postgres реализует интерфейс заданий
var _ syncmanager.TargetRepository = (*PostgresStore)(nil)
