package catalog

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type Entry struct {
	SourceID     string    `json:"source_id"`
	SourceKey    string    `json:"source_key"`
	Name         string    `json:"name"`
	URL          string    `json:"url"`
	Discipline   string    `json:"discipline"`
	Cohort       string    `json:"cohort"`
	Availability string    `json:"availability"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func List(ctx context.Context, db *sql.DB) ([]Entry, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT source_id, source_key, name, url, discipline, cohort,
		       availability, created_at, updated_at
		FROM resource_catalog
		ORDER BY source_id, source_key
	`)
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	defer rows.Close()
	entries := make([]Entry, 0)
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.SourceID, &e.SourceKey, &e.Name, &e.URL, &e.Discipline,
			&e.Cohort, &e.Availability, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("read catalog row: %w", err)
		}
		e.CreatedAt, e.UpdatedAt = e.CreatedAt.UTC(), e.UpdatedAt.UTC()
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func Export(out io.Writer, format string, entries []Entry) error {
	switch format {
	case "json":
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(entries)
	case "csv":
		writer := csv.NewWriter(out)
		if err := writer.Write([]string{"source_id", "source_key", "name", "url", "discipline", "cohort", "availability", "created_at", "updated_at"}); err != nil {
			return err
		}
		for _, e := range entries {
			if err := writer.Write([]string{e.SourceID, e.SourceKey, e.Name, e.URL, e.Discipline,
				e.Cohort, e.Availability, e.CreatedAt.Format(time.RFC3339Nano), e.UpdatedAt.Format(time.RFC3339Nano)}); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()
	default:
		return fmt.Errorf("unsupported export format %q (use csv or json)", format)
	}
}
