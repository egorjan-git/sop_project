package postgres_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"zulip-collector/internal/storage/postgres"
	"zulip-collector/internal/syncmanager"
)

func TestTargetRepository(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_URL")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_URL is not set")
	}

	ctx, cancel := context.WithTimeout(
		context.Background(), 20*time.Second,
	)
	defer cancel()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}

	// защищаемся от случайного запуска на основной базе
	var databaseName string
	err = db.QueryRowContext(ctx,
		"SELECT current_database()",
	).Scan(&databaseName)

	if err != nil || databaseName != "sop_collector_test" {
		db.Close()
		t.Fatalf("unexpected database: %q, %v", databaseName, err)
	}

	base := time.Now().UnixNano()
	channelID := base + 1
	blockedChannelID := base + 2
	targetID := fmt.Sprintf("test-target-%d", base)

	// удаляем тестовые записи после проверки
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		defer cleanupCancel()

		_, err := db.ExecContext(cleanupCtx,
			`DELETE FROM zulip.sync_targets
			 WHERE target_id = $1`,
			targetID,
		)
		if err != nil {
			t.Errorf("cleanup target: %v", err)
		}

		_, err = db.ExecContext(cleanupCtx,
			`DELETE FROM zulip.channels
			 WHERE channel_id IN ($1, $2)`,
			channelID, blockedChannelID,
		)
		if err != nil {
			t.Errorf("cleanup channels: %v", err)
		}

		_ = db.Close()
	})

	// регистрируем разрешённый и запрещённый каналы
	_, err = db.ExecContext(ctx, `
		INSERT INTO zulip.channels (
			channel_id, name, approved_public
		)
		VALUES
			($1, 'Тестовый публичный', TRUE),
			($2, 'Тестовый запрещённый', FALSE)
	`, channelID, blockedChannelID)
	if err != nil {
		t.Fatal(err)
	}

	store := postgres.NewPostgresStore(db)

	target := syncmanager.Target{
		ID:        targetID,
		ChannelID: channelID,

		PeriodStart: time.Date(
			2024, 1, 1, 0, 0, 0, 0, time.UTC,
		),
		PeriodEnd: time.Date(
			2025, 1, 1, 0, 0, 0, 0, time.UTC,
		),

		SelectionVersion: 1,
		Config: json.RawMessage(
			`{"target_group_ids":[22022,22023,22024]}`,
		),
	}

	// создаём задание
	created, err := store.CreateTarget(ctx, target)
	if err != nil {
		t.Fatal(err)
	}

	if created.ID != targetID || !created.Enabled {
		t.Fatalf("unexpected created target: %+v", created)
	}

	if created.HighWatermarkID != nil {
		t.Fatal("new target must not have a watermark")
	}

	if created.LastSuccessfulAt != nil {
		t.Fatal("new target must not have a successful run")
	}

	// читаем задание повторно из postgres
	loaded, err := store.GetTarget(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.ID != created.ID ||
		loaded.ChannelID != channelID ||
		loaded.SelectionVersion != 1 ||
		!loaded.PeriodStart.Equal(target.PeriodStart) ||
		!loaded.PeriodEnd.Equal(target.PeriodEnd) {
		t.Fatalf("loaded target differs: %+v", loaded)
	}

	// повторное создание с тем же ID запрещено
	_, err = store.CreateTarget(ctx, target)
	if err == nil {
		t.Fatal("duplicate target was accepted")
	}

	// запрещённый канал нельзя использовать
	blocked := target
	blocked.ID = targetID + "-blocked"
	blocked.ChannelID = blockedChannelID

	_, err = store.CreateTarget(ctx, blocked)
	if err == nil {
		t.Fatal("blocked channel was accepted")
	}

	// неправильный период должен быть отклонён
	invalid := target
	invalid.ID = targetID + "-invalid"
	invalid.PeriodEnd = invalid.PeriodStart

	_, err = store.CreateTarget(ctx, invalid)
	if err == nil {
		t.Fatal("invalid period was accepted")
	}

	// неизвестный target возвращает ожидаемую ошибку
	_, err = store.GetTarget(ctx, targetID+"-missing")
	if !errors.Is(err, syncmanager.ErrTargetNotFound) {
		t.Fatalf("unexpected error: %v", err)
	}
}
