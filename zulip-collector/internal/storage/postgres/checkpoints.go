package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"zulip-collector/internal/syncmanager"
)

// получаем checkpoint, который принадлежит конкретному запуску
func (s *PostgresStore) GetRunCheckpoint(
	ctx context.Context,
	runID int64,
) (syncmanager.Checkpoint, error) {
	if runID <= 0 {
		return syncmanager.Checkpoint{},
			fmt.Errorf("invalid run ID")
	}

	var checkpoint syncmanager.Checkpoint

	err := s.db.QueryRowContext(ctx, `
		SELECT
			st.scope_id,
			st.target_id,
			st.last_run_id,
			st.cursor,
			st.is_complete,
			st.updated_at
		FROM zulip.sync_state AS st
		JOIN zulip.sync_runs AS r
			ON r.scope_id = st.scope_id
			AND r.target_id = st.target_id
			AND r.run_id = st.last_run_id
		WHERE r.run_id = $1
	`, runID).Scan(
		&checkpoint.ScopeID,
		&checkpoint.TargetID,
		&checkpoint.RunID,
		&checkpoint.Cursor,
		&checkpoint.Done,
		&checkpoint.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return syncmanager.Checkpoint{},
			fmt.Errorf(
				"%w: run %d",
				syncmanager.ErrCheckpointNotFound,
				runID,
			)
	}

	if err != nil {
		return syncmanager.Checkpoint{},
			fmt.Errorf(
				"get checkpoint for run %d: %w",
				runID,
				err,
			)
	}

	return checkpoint, nil
}

// postgres должен реализовывать интерфейс чтения checkpoint
var _ syncmanager.CheckpointRepository = (*PostgresStore)(nil)
