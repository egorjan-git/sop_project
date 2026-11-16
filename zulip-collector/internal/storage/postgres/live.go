package postgres

import (
	"context"
	"database/sql/driver"
	"fmt"
	"time"
)

func (s *PostgresStore) WithLiveChannel(ctx context.Context, channelID int64, run func() error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	var locked bool
	err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", -channelID).Scan(&locked)
	if err != nil {
		conn.Raw(func(any) error { return driver.ErrBadConn })
		return err
	}
	if !locked {
		return fmt.Errorf("another collection is running for channel %d", channelID)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", -channelID); err != nil {
			conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()

	return run()
}

func (s *PostgresStore) PrepareLiveScan(ctx context.Context, scope string, channelID int64, name string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var approved bool
	err = tx.QueryRowContext(ctx, `
		INSERT INTO zulip.channels (channel_id, name, approved_public, checked_at)
		VALUES ($1, $2, true, now())
		ON CONFLICT (channel_id) DO UPDATE
		SET name = CASE WHEN zulip.channels.approved_public THEN EXCLUDED.name ELSE zulip.channels.name END,
		    updated_at = CASE WHEN zulip.channels.name IS DISTINCT FROM EXCLUDED.name
		                      AND zulip.channels.approved_public THEN now() ELSE zulip.channels.updated_at END,
		    checked_at = now()
		RETURNING approved_public
	`, channelID, name).Scan(&approved)
	if err != nil {
		return err
	}
	if !approved {
		return fmt.Errorf("channel %d is disabled in zulip.channels", channelID)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE zulip.sync_state
		SET cursor = '', is_complete = false, updated_at = now()
		WHERE scope_id = $1 AND is_complete AND last_run_id IS NULL
	`, scope)
	if err != nil {
		return err
	}
	return tx.Commit()
}
