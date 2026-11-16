package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"resource-importer/internal/catalog"
)

func importSnapshots(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("import-snapshots", flag.ContinueOnError)
	sheet := flags.String("sheet", "", "Google spreadsheet ID or URL (includes all tabs)")
	dir := flags.String("dir", "", "directory of local tab snapshots")
	xlsx := flags.String("xlsx", "", "path to an Excel workbook")
	id := flags.String("id", "", "stable workbook ID for -xlsx")
	title := flags.String("title", "", "workbook title for -xlsx (defaults to the file name)")
	dryRun := flags.Bool("dry-run", false, "read and validate without writing to PostgreSQL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	inputCount := 0
	for _, input := range []string{*sheet, *dir, *xlsx} {
		if input != "" {
			inputCount++
		}
	}
	if inputCount != 1 || flags.NArg() != 0 {
		return errors.New("provide exactly one of -sheet, -dir or -xlsx")
	}
	if *xlsx == "" && (*id != "" || *title != "") {
		return errors.New("-id and -title are only used with -xlsx")
	}
	var snapshots []catalog.SheetSnapshot
	var err error
	switch {
	case *dir != "":
		snapshots, err = catalog.ReadSnapshotDirectory(*dir)
	case *xlsx != "":
		bookID := strings.TrimSpace(*id)
		if bookID == "" {
			return errors.New("-xlsx requires a stable -id")
		}
		snapshots, err = catalog.ReadXLSXSnapshots(*xlsx, bookID, strings.TrimSpace(*title))
	default:
		id, idErr := spreadsheetID(*sheet)
		if idErr != nil {
			return idErr
		}
		snapshots, err = catalog.ReadGoogleSnapshots(ctx, id)
	}
	if err != nil {
		return err
	}
	books := make(map[string]bool)
	for _, s := range snapshots {
		books[s.SpreadsheetID] = true
	}
	if *dryRun {
		fmt.Printf("spreadsheets=%d tabs=%d (dry run)\n", len(books), len(snapshots))
		return nil
	}
	db, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	changed, err := catalog.SaveSheetSnapshots(ctx, db, snapshots)
	if err != nil {
		return err
	}
	fmt.Printf("spreadsheets=%d tabs=%d changed=%d unchanged=%d\n", len(books), len(snapshots), changed, int64(len(snapshots))-changed)
	return nil
}

func exportSnapshots(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("export-snapshots", flag.ContinueOnError)
	format := flags.String("format", "", "csv or json")
	output := flags.String("out", "", "new local output file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if (*format != "csv" && *format != "json") || *output == "" || flags.NArg() != 0 {
		return errors.New("use export-snapshots -format csv|json -out NEW_FILE")
	}
	db, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	snapshots, err := catalog.ListSheetSnapshots(ctx, db)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	writeErr := catalog.ExportSnapshots(file, *format, snapshots)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("write %s: %w", *output, err)
	}
	fmt.Printf("exported %d tabs to %s\n", len(snapshots), *output)
	return nil
}

func importCatalogLinks(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("catalog-links", flag.ContinueOnError)
	sourceFile := flags.String("sources", "", "catalog CSV with workbook metadata")
	source := flags.String("source", "", "stable identifier for the reviewed entries in PostgreSQL")
	review := flags.String("review", "", "CSV of reviewed educational resources")
	candidatesOut := flags.String("candidates-out", "", "new CSV file for candidates awaiting review")
	dryRun := flags.Bool("dry-run", false, "count links without writing to PostgreSQL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	sourceID := strings.TrimSpace(*source)
	if *sourceFile == "" || flags.NArg() != 0 || (*review != "" && (sourceID == "" || *candidatesOut != "")) {
		return errors.New("use catalog-links -sources FILE [-candidates-out FILE | -review FILE -source SOURCE [-dry-run]]")
	}
	sources, err := readCatalogCSV(*sourceFile)
	if err != nil {
		return err
	}
	db, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	snapshots, err := catalog.ListSheetSnapshots(ctx, db)
	if err != nil {
		return err
	}
	candidates, err := catalog.SheetLinks(snapshots, sources)
	if err != nil {
		return err
	}
	if *review == "" {
		if *candidatesOut != "" {
			file, err := os.OpenFile(*candidatesOut, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			writeErr := catalog.ExportLinkCandidates(file, candidates)
			if err := errors.Join(writeErr, file.Close()); err != nil {
				return fmt.Errorf("write %s: %w", *candidatesOut, err)
			}
		}
		fmt.Printf("candidates=%d (review before saving)\n", len(candidates))
		return nil
	}
	reviewed, err := readCatalogCSV(*review)
	if err != nil {
		return err
	}
	links, err := catalog.ReviewLinks(candidates, reviewed)
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Printf("source=%q candidates=%d reviewed=%d (dry run)\n", sourceID, len(candidates), len(links))
		return nil
	}
	changed, removed, err := catalog.ReplaceReviewedLinks(ctx, db, sourceID, links)
	if err != nil {
		return err
	}
	fmt.Printf("source=%q candidates=%d reviewed=%d changed=%d removed=%d unchanged=%d\n", sourceID, len(candidates), len(links), changed, removed, int64(len(links))-changed)
	return nil
}
