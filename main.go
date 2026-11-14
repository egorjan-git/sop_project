package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"resource-importer/internal/catalog"
)

const usage = `Usage:
  go run . import -sheet ID_OR_URL -range "'Sheet'!A1:G" -source SOURCE [-dry-run]
  go run . import -file FILE -source SOURCE [-dry-run]
  go run . export -format csv|json [-out FILE]
  go run . import-snapshots (-sheet ID_OR_URL | -dir DIR) [-dry-run]
  go run . import-snapshots -xlsx FILE -id SOURCE_ID [-title TITLE] [-dry-run]
  go run . export-snapshots -format csv|json -out FILE
  go run . catalog-links -sources FILE [-candidates-out FILE | -review FILE -source SOURCE [-dry-run]]
Set DATABASE_URL to connect to PostgreSQL.`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Println(usage)
		return nil
	}
	var err error
	switch args[0] {
	case "import":
		err = importCatalog(ctx, args[1:])
	case "export":
		err = exportCatalog(ctx, args[1:])
	case "import-snapshots":
		err = importSnapshots(ctx, args[1:])
	case "export-snapshots":
		err = exportSnapshots(ctx, args[1:])
	case "catalog-links":
		err = importCatalogLinks(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command %q; use -h", args[0])
	}
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func importCatalog(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	sheet := flags.String("sheet", "", "Google spreadsheet ID or URL")
	a1Range := flags.String("range", "", "Google Sheets A1 range starting at the header row")
	file := flags.String("file", "", "local UTF-8 CSV fixture instead of Google Sheets")
	source := flags.String("source", "", "stable identifier of the source")
	dryRun := flags.Bool("dry-run", false, "validate the source without connecting to PostgreSQL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	sourceID := strings.TrimSpace(*source)
	if (*file == "") == (*sheet == "") || sourceID == "" || flags.NArg() != 0 {
		return errors.New("provide -source and exactly one of -sheet or -file; use import -h for help")
	}
	var records []catalog.Resource
	if *sheet != "" {
		id, err := spreadsheetID(*sheet)
		if err != nil {
			return err
		}
		sheetRange := strings.TrimSpace(*a1Range)
		if sheetRange == "" {
			return errors.New("provide -range for the Google Sheet, for example 'Ресурсы!A1:G'")
		}
		records, err = catalog.ReadGoogleSheet(ctx, id, sheetRange)
		if err != nil {
			return err
		}
	} else {
		if *a1Range != "" {
			return errors.New("-range is only used with -sheet")
		}
		var err error
		records, err = readCatalogCSV(*file)
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if *dryRun {
		fmt.Printf("source=%q read=%d (dry run)\n", sourceID, len(records))
		return nil
	}
	db, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	changed, err := catalog.Save(ctx, db, sourceID, records)
	if err != nil {
		return err
	}
	fmt.Printf("source=%q read=%d changed=%d unchanged=%d\n", sourceID, len(records), changed, int64(len(records))-changed)
	return nil
}

func readCatalogCSV(path string) ([]catalog.Resource, error) {
	input, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	records, err := catalog.ReadCSV(input)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return records, nil
}

var sheetIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func spreadsheetID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if u, err := url.Parse(id); err == nil && u.Scheme == "https" && u.Host == "docs.google.com" {
		if path, ok := strings.CutPrefix(u.Path, "/spreadsheets/d/"); ok {
			id, _, _ = strings.Cut(path, "/")
		}
	}
	if !sheetIDPattern.MatchString(id) {
		return "", errors.New("-sheet must be a spreadsheet ID or a Google Sheets URL")
	}
	return id, nil
}

func exportCatalog(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	format := flags.String("format", "", "csv or json")
	output := flags.String("out", "", "output file (defaults to stdout)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if (*format != "csv" && *format != "json") || flags.NArg() != 0 {
		return errors.New("use export -format csv|json [-out FILE]")
	}
	db, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	entries, err := catalog.List(ctx, db)
	if err != nil {
		return err
	}
	if *output == "" {
		return catalog.Export(os.Stdout, *format, entries)
	}
	file, err := os.Create(*output)
	if err != nil {
		return err
	}
	writeErr := catalog.Export(file, *format, entries)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("write %s: %w", *output, err)
	}
	return nil
}

func openDB(ctx context.Context) (*sql.DB, error) {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return nil, errors.New("set DATABASE_URL to connect to PostgreSQL")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return db, nil
}
