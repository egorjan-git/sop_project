package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"time"

	"zulip-collector/internal/collector"
	"zulip-collector/internal/config"
	"zulip-collector/internal/source"
	"zulip-collector/internal/storage/postgres"
)

type selection struct {
	channels string
	fromDate string
	toDate   string
}

func (s *selection) flags(flags *flag.FlagSet) {
	flags.StringVar(&s.channels, "channels", "", "ID каналов через запятую")
	flags.StringVar(&s.fromDate, "from", "", "первый день, YYYY-MM-DD")
	flags.StringVar(&s.toDate, "to", "", "последний день включительно, YYYY-MM-DD")
}

func (s selection) parse() ([]int64, time.Time, time.Time, error) {
	channels, err := parseIDs(s.channels)
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("-channels: %w", err)
	}
	moscow := time.FixedZone("MSK", 3*60*60)
	from, err := time.ParseInLocation(time.DateOnly, s.fromDate, moscow)
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("-from must be YYYY-MM-DD")
	}
	to, err := time.ParseInLocation(time.DateOnly, s.toDate, moscow)
	if err != nil || to.Before(from) {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("-to must be YYYY-MM-DD and equal to or later than -from")
	}
	ids := make([]int64, 0, len(channels))
	for id := range channels {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, from, to.AddDate(0, 0, 1), nil
}

func runLive(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("collector live", flag.ContinueOnError)
	var target selection
	target.flags(flags)
	pageSize := flags.Int("page-size", 100, "сообщений на запрос (от 1 до 1000)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *pageSize < 1 || *pageSize > 1000 || flags.NArg() > 0 {
		return fmt.Errorf("expected -page-size from 1 to 1000 and no positional arguments")
	}
	channels, from, until, err := target.parse()
	if err != nil {
		return err
	}
	api, err := zulipFromEnv()
	if err != nil {
		return err
	}
	db, err := config.OpenPostgres(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	store := postgres.NewPostgresStore(db)

	var failures []error
	for _, id := range channels {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		err := store.WithLiveChannel(ctx, id, func() error {
			channel, err := api.CheckChannel(ctx, id)
			if err != nil {
				return err
			}
			scope := fmt.Sprintf("live:%d:%s:%s", id, target.fromDate, target.toDate)
			if err := store.PrepareLiveScan(ctx, scope, id, channel.Name); err != nil {
				return err
			}
			live := &source.ZulipSource{API: api, Channel: channel, From: from, Until: until, PageSize: *pageSize}
			job := collector.Collector{Source: live, Store: store}
			stats, err := job.Sync(ctx, scope, nil)
			fmt.Printf("Канал %d (%s), %s — %s. Прочитано в периоде %d, добавлено %d, обновлено %d.\n",
				id, channel.Name, target.fromDate, target.toDate, stats.Selected, stats.Inserted, stats.Updated)
			if live.HistoryLimited {
				fmt.Printf("Канал %d. Zulip сообщил об ограничении доступной истории.\n", id)
			}
			return err
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("канал %d: %w", id, err))
		}
	}
	return errors.Join(failures...)
}

func zulipFromEnv() (*source.Zulip, error) {
	address := os.Getenv("ZULIP_URL")
	if address == "" {
		address = "https://chat.miem.hse.ru"
	}
	return source.NewZulip(address, os.Getenv("ZULIP_EMAIL"), os.Getenv("ZULIP_API_KEY"), nil)
}
