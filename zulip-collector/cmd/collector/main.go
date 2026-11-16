package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"zulip-collector/internal/collector"
	"zulip-collector/internal/config"
	"zulip-collector/internal/source"
	"zulip-collector/internal/storage"
	"zulip-collector/internal/storage/postgres"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	if err := run(ctx); err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "channels":
			return listChannels(ctx, os.Args[2:])
		case "live":
			return runLive(ctx, os.Args[2:])
		case "export":
			return runExport(ctx, os.Args[2:])
		}
	}
	return runFixture(ctx)
}

func runFixture(ctx context.Context) error {
	storageType := flag.String(
		"storage", "memory", "memory или postgres",
	)
	scope := flag.String(
		"scope", "", "идентификатор обхода",
	)
	fixturePath := flag.String(
		"fixture", "testdata/messages.json", "путь к сообщениям",
	)
	membershipsPath := flag.String(
		"memberships", "testdata/memberships.json",
		"путь к данным о группах",
	)
	groupIDs := flag.String(
		"groups", "22022,22023,22024", "целевые группы",
	)
	channelIDs := flag.String(
		"channels", "501,502", "тестовые разрешённые каналы",
	)
	pageSize := flag.Int(
		"page-size", 2, "размер одной порции",
	)

	flag.Parse()

	if strings.TrimSpace(*scope) == "" {
		return fmt.Errorf("scope must not be empty")
	}

	targetGroups, err := parseIDs(*groupIDs)
	if err != nil {
		return fmt.Errorf("invalid groups: %w", err)
	}

	allowedChannels, err := parseIDs(*channelIDs)
	if err != nil {
		return fmt.Errorf("invalid channels: %w", err)
	}

	// читаем состав групп из тестового JSON
	data, err := os.ReadFile(*membershipsPath)
	if err != nil {
		return fmt.Errorf("read memberships: %w", err)
	}

	var memberships []collector.Membership
	if err := json.Unmarshal(data, &memberships); err != nil {
		return fmt.Errorf("decode memberships: %w", err)
	}

	authors := collector.TargetAuthors(
		memberships, targetGroups,
	)

	if len(authors) == 0 {
		return fmt.Errorf("no target authors found")
	}

	// пока используем только локальный источник
	fixture, err := source.NewFixtureSource(
		*fixturePath,
		*pageSize,
		allowedChannels,
	)
	if err != nil {
		return fmt.Errorf("create fixture source: %w", err)
	}

	// выбираем реализацию хранилища
	var store collector.MessageStore

	switch *storageType {
	case "memory":
		store = storage.NewMemoryStore()

	case "postgres":
		db, err := config.OpenPostgres(ctx)
		if err != nil {
			return err
		}
		defer db.Close()

		store = postgres.NewPostgresStore(db)

	default:
		return fmt.Errorf(
			"unknown storage: %s", *storageType,
		)
	}

	job := collector.Collector{
		Source: fixture,
		Store:  store,
	}

	fmt.Println("Запуск коллектора")
	fmt.Println("Хранилище:", *storageType)
	fmt.Println("Scope:", *scope)
	fmt.Println("Целевых пользователей:", len(authors))

	stats, err := job.Sync(ctx, *scope, authors)
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	progress, err := store.LoadProgress(ctx, *scope)
	if err != nil {
		return err
	}

	fmt.Println("\nРезультат синхронизации:")
	fmt.Printf("Страниц: %d\n", stats.Pages)
	fmt.Printf("Отобрано: %d\n", stats.Selected)
	fmt.Printf("Добавлено: %d\n", stats.Inserted)
	fmt.Printf("Обновлено: %d\n", stats.Updated)
	fmt.Printf("Курсор: %s\n", progress.Cursor)
	fmt.Printf("Завершено: %t\n", progress.Done)

	if stats.Pages == 0 && progress.Done {
		fmt.Println("Этот обход уже был завершён")
	}

	return nil
}

// преобразуем список ID из строки в множество
func parseIDs(raw string) (map[int64]bool, error) {
	result := make(map[int64]bool)

	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)

		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid ID: %q", part)
		}

		result[id] = true
	}

	return result, nil
}
