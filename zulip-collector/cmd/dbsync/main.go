package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"zulip-collector/internal/collector"
	"zulip-collector/internal/config"
	"zulip-collector/internal/source"
	"zulip-collector/internal/storage/postgres"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: go run ./cmd/dbsync <scope>")
	}

	if err := run(context.Background(), os.Args[1]); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, scope string) error {
	// подключаемся к нашей базе
	db, err := config.OpenPostgres(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	// используем те же тестовые сообщения
	fixture, err := source.NewFixtureSource(
		"testdata/messages.json",
		2,
		map[int64]bool{
			501: true,
			502: true,
		},
	)
	if err != nil {
		return err
	}

	// пока работаем с тестовыми ID студентов
	authors := map[int64]bool{
		101: true,
		102: true,
		104: true,
	}

	store := postgres.NewPostgresStore(db)

	job := collector.Collector{
		Source: fixture,
		Store:  store,
	}

	stats, err := job.Sync(ctx, scope, authors)
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	progress, err := store.LoadProgress(ctx, scope)
	if err != nil {
		return err
	}

	fmt.Println("Синхронизация завершена")
	fmt.Printf("Страниц: %d\n", stats.Pages)
	fmt.Printf("Отобрано: %d\n", stats.Selected)
	fmt.Printf("Добавлено: %d\n", stats.Inserted)
	fmt.Printf("Обновлено: %d\n", stats.Updated)
	fmt.Printf("Курсор: %s\n", progress.Cursor)
	fmt.Printf("Завершено: %t\n", progress.Done)

	// проверяем, сколько сообщений сохранилось в postgres
	var count int

	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM zulip.messages
		WHERE message_id IN (10001, 10003, 10005)
	`).Scan(&count)

	if err != nil {
		return err
	}

	fmt.Printf("Тестовых сообщений в БД: %d\n", count)

	return nil
}
