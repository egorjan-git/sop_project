package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"zulip-collector/internal/collector"
	"zulip-collector/internal/config"
	"zulip-collector/internal/storage/postgres"
)

func runExport(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("collector export", flag.ContinueOnError)
	var target selection
	target.flags(flags)
	format := flags.String("format", "json", "json или csv")
	out := flags.String("out", "", "путь к файлу")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if (*format != "json" && *format != "csv") || *out == "" || flags.NArg() != 0 {
		return fmt.Errorf("expected -format json or csv and -out FILE")
	}
	channels, from, until, err := target.parse()
	if err != nil {
		return err
	}
	db, err := config.OpenPostgres(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	store := postgres.NewPostgresStore(db)

	file, err := os.CreateTemp(filepath.Dir(*out), ".zulip-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()

	walk := func(write func(collector.Message) error) error {
		return store.WalkMessages(ctx, channels, from, until, write)
	}
	if *format == "csv" {
		err = writeCSV(file, walk)
	} else {
		err = writeJSON(file, walk)
	}
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), *out)
}

func writeCSV(out io.Writer, walk func(func(collector.Message) error) error) error {
	w := csv.NewWriter(out)
	if err := w.Write([]string{"id", "author_id", "author_name", "channel_id", "channel_name",
		"topic", "text", "url", "created_at", "edited_at"}); err != nil {
		return err
	}
	err := walk(func(m collector.Message) error {
		edited := ""
		if m.EditedAt != nil {
			edited = m.EditedAt.Format(time.RFC3339Nano)
		}
		return w.Write([]string{strconv.FormatInt(m.ID, 10), strconv.FormatInt(m.AuthorID, 10),
			m.AuthorName, strconv.FormatInt(m.ChannelID, 10), m.ChannelName, m.Topic, m.Text,
			m.URL, m.CreatedAt.Format(time.RFC3339Nano), edited})
	})
	if err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

func writeJSON(out io.Writer, walk func(func(collector.Message) error) error) error {
	if _, err := io.WriteString(out, "["); err != nil {
		return err
	}
	first := true
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	if err := walk(func(m collector.Message) error {
		if !first {
			if _, err := io.WriteString(out, ","); err != nil {
				return err
			}
		}
		first = false
		return enc.Encode(m)
	}); err != nil {
		return err
	}
	_, err := io.WriteString(out, "]\n")
	return err
}
