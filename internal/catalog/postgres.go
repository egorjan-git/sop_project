package catalog

import (
	"context"
	"database/sql"
	"fmt"
)

const upsertResource = `
INSERT INTO resource_catalog AS stored
    (source_id, source_key, name, url, discipline, cohort, availability, questions)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (source_id, source_key) DO UPDATE SET
    name = EXCLUDED.name,
    url = EXCLUDED.url,
    discipline = EXCLUDED.discipline,
    cohort = EXCLUDED.cohort,
    availability = EXCLUDED.availability,
    questions = EXCLUDED.questions,
    updated_at = CURRENT_TIMESTAMP
WHERE
    (stored.name, stored.url, stored.discipline, stored.cohort, stored.availability, stored.questions)
    IS DISTINCT FROM
    (EXCLUDED.name, EXCLUDED.url, EXCLUDED.discipline, EXCLUDED.cohort, EXCLUDED.availability, EXCLUDED.questions)
`

// Save writes the full batch in one transaction and returns the number of inserted or updated rows.
func Save(ctx context.Context, db *sql.DB, sourceID string, records []Resource) (int64, error) {
	if len(records) == 0 {
		return 0, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin catalog import: %w", err)
	}
	defer tx.Rollback()
	changed, err := saveResources(ctx, tx, sourceID, records)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit catalog import: %w", err)
	}
	return changed, nil
}

// ReplaceReviewedLinks updates the reviewed entries and removes stale candidates from this source.
func ReplaceReviewedLinks(ctx context.Context, db *sql.DB, sourceID string, records []Resource) (changed, removed int64, err error) {
	if len(records) == 0 {
		return 0, 0, fmt.Errorf("cannot replace reviewed links with an empty list")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin reviewed catalog update: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, sourceID); err != nil {
		return 0, 0, fmt.Errorf("lock reviewed catalog source: %w", err)
	}
	changed, err = saveResources(ctx, tx, sourceID, records)
	if err != nil {
		return 0, 0, err
	}
	keys := make([]string, 0, len(records))
	for _, record := range records {
		keys = append(keys, record.SourceKey)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM resource_catalog WHERE source_id = $1 AND NOT (source_key = ANY($2::text[]))`, sourceID, keys)
	if err != nil {
		return 0, 0, fmt.Errorf("remove unreviewed links: %w", err)
	}
	removed, err = result.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("count removed links: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit reviewed catalog update: %w", err)
	}
	return changed, removed, nil
}

func saveResources(ctx context.Context, tx *sql.Tx, sourceID string, records []Resource) (int64, error) {
	stmt, err := tx.PrepareContext(ctx, upsertResource)
	if err != nil {
		return 0, fmt.Errorf("prepare catalog import: %w", err)
	}
	defer stmt.Close()

	var changed int64
	for _, resource := range records {
		result, err := stmt.ExecContext(ctx, sourceID, resource.SourceKey,
			resource.Name, resource.URL, resource.Discipline, resource.Cohort,
			resource.Availability, resource.Questions)
		if err != nil {
			return 0, fmt.Errorf("save source row %d: %w", resource.RowNumber, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("count changes for source row %d: %w", resource.RowNumber, err)
		}
		changed += affected
	}

	return changed, nil
}
