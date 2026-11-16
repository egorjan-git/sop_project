package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"resource-importer/internal/history"
)

const historyUsage = `student-history commands:
  student-history create [-cohort COHORT]
  student-history link-zulip -student ID -user ZULIP_USER_ID
  student-history link-sheet -student ID -book BOOK_ID -sheet TAB_ID -column N -value VALUE [-header-row N]
  student-history show -student ID [-out FILE]`

func studentHistory(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println(historyUsage)
		return nil
	}
	flags := flag.NewFlagSet("student-history "+args[0], flag.ContinueOnError)
	switch args[0] {
	case "create":
		cohort := flags.String("cohort", "", "student cohort")
		if err := parseHistoryFlags(flags, args[1:]); err != nil {
			return err
		}
		db, err := openDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		id, err := history.CreateStudent(ctx, db, *cohort)
		if err == nil {
			fmt.Println(id)
		}
		return err
	case "link-zulip":
		studentID := flags.Int64("student", 0, "student ID")
		userID := flags.Int64("user", 0, "verified numeric Zulip user ID")
		if err := parseHistoryFlags(flags, args[1:]); err != nil {
			return err
		}
		if *studentID <= 0 || *userID <= 0 {
			return errors.New("provide positive -student and -user IDs")
		}
		db, err := openDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := history.LinkZulip(ctx, db, *studentID, *userID); err != nil {
			return err
		}
		fmt.Printf("student=%d zulip_user=%d\n", *studentID, *userID)
		return nil
	case "link-sheet":
		studentID := flags.Int64("student", 0, "student ID")
		bookID := flags.String("book", "", "spreadsheet ID in PostgreSQL")
		sheetID := flags.Int("sheet", -1, "numeric sheet ID in PostgreSQL")
		column := flags.Int("column", 0, "1-based identity column")
		value := flags.String("value", "", "exact verified identity as displayed in this column")
		header := flags.Int("header-row", 1, "last header row to skip and include as labels; 0 for no header")
		if err := parseHistoryFlags(flags, args[1:]); err != nil {
			return err
		}
		if *studentID <= 0 || strings.TrimSpace(*bookID) == "" || *sheetID < 0 ||
			*column <= 0 || strings.TrimSpace(*value) == "" || *header < 0 {
			return errors.New("provide -student, -book, -sheet, -column and -value")
		}
		db, err := openDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		matcher := history.SheetMatch{Column: *column, Value: *value, HeaderRow: *header}
		row, err := history.LinkSheet(ctx, db, *studentID, strings.TrimSpace(*bookID), *sheetID, matcher)
		if err == nil {
			fmt.Printf("student=%d sheet=%d row=%d\n", *studentID, *sheetID, row)
		}
		return err
	case "show":
		studentID := flags.Int64("student", 0, "student ID")
		out := flags.String("out", "", "new JSON file (defaults to stdout)")
		if err := parseHistoryFlags(flags, args[1:]); err != nil {
			return err
		}
		if *studentID <= 0 {
			return errors.New("provide a positive -student ID")
		}
		db, err := openDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		result, err := history.Load(ctx, db, *studentID)
		if err != nil {
			return err
		}
		if *out == "" {
			return writeStudentHistory(os.Stdout, result)
		}
		file, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		if err := errors.Join(writeStudentHistory(file, result), file.Close()); err != nil {
			return errors.Join(err, os.Remove(*out))
		}
		return nil
	default:
		return fmt.Errorf("unknown student-history command %q; use student-history -h", args[0])
	}
}

func parseHistoryFlags(flags *flag.FlagSet, args []string) error {
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments; use %s -h", flags.Name())
	}
	return nil
}

func writeStudentHistory(w io.Writer, result history.History) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
