package catalog

import (
	"context"
	"database/sql"
	"fmt"
)

const upsertSheetSnapshot = `
INSERT INTO sheet_snapshots AS stored
    (spreadsheet_id, sheet_id, spreadsheet_title, sheet_title, sheet_index, row_count, column_count, hidden, cells)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (spreadsheet_id, sheet_id) DO UPDATE SET
    spreadsheet_title = EXCLUDED.spreadsheet_title,
    sheet_title = EXCLUDED.sheet_title,
    sheet_index = EXCLUDED.sheet_index,
    row_count = EXCLUDED.row_count,
    column_count = EXCLUDED.column_count,
    hidden = EXCLUDED.hidden,
    cells = EXCLUDED.cells,
    updated_at = CURRENT_TIMESTAMP
WHERE
    (stored.spreadsheet_title, stored.sheet_title, stored.sheet_index, stored.row_count,
     stored.column_count, stored.hidden, stored.cells)
    IS DISTINCT FROM
    (EXCLUDED.spreadsheet_title, EXCLUDED.sheet_title, EXCLUDED.sheet_index, EXCLUDED.row_count,
     EXCLUDED.column_count, EXCLUDED.hidden, EXCLUDED.cells)
`

func SaveSheetSnapshots(ctx context.Context, db *sql.DB, snapshots []SheetSnapshot) (int64, error) {
	if len(snapshots) == 0 {
		return 0, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin sheet import: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, upsertSheetSnapshot)
	if err != nil {
		return 0, fmt.Errorf("prepare sheet import: %w", err)
	}
	defer stmt.Close()

	var changed int64
	for _, snapshot := range snapshots {
		result, err := stmt.ExecContext(ctx, snapshot.SpreadsheetID, snapshot.SheetID,
			snapshot.SpreadsheetTitle, snapshot.SheetTitle, snapshot.SheetIndex,
			snapshot.RowCount, snapshot.ColumnCount, snapshot.Hidden, snapshot.Cells)
		if err != nil {
			return 0, fmt.Errorf("save spreadsheet %q, tab %d: %w", snapshot.SpreadsheetID, snapshot.SheetID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("count changes for spreadsheet %q, tab %d: %w", snapshot.SpreadsheetID, snapshot.SheetID, err)
		}
		changed += affected
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit sheet import: %w", err)
	}
	return changed, nil
}

func ListSheetSnapshots(ctx context.Context, db *sql.DB) ([]SheetSnapshot, error) {
	rows, err := db.QueryContext(ctx, `
        SELECT spreadsheet_id, sheet_id, spreadsheet_title, sheet_title, sheet_index,
               row_count, column_count, hidden, cells
        FROM sheet_snapshots
        ORDER BY spreadsheet_id, sheet_index, sheet_id
    `)
	if err != nil {
		return nil, fmt.Errorf("list sheet snapshots: %w", err)
	}
	defer rows.Close()

	snapshots := make([]SheetSnapshot, 0)
	for rows.Next() {
		var snapshot SheetSnapshot
		var cells []byte
		if err := rows.Scan(&snapshot.SpreadsheetID, &snapshot.SheetID, &snapshot.SpreadsheetTitle,
			&snapshot.SheetTitle, &snapshot.SheetIndex, &snapshot.RowCount, &snapshot.ColumnCount,
			&snapshot.Hidden, &cells); err != nil {
			return nil, fmt.Errorf("read sheet snapshot: %w", err)
		}
		snapshot.Cells = cells
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read sheet snapshots: %w", err)
	}
	return snapshots, nil
}
