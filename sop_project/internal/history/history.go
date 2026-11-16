package history

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"resource-importer/internal/catalog"
)

type Student struct {
	ID     int64  `json:"id"`
	Cohort string `json:"cohort,omitempty"`
}

type Account struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Message struct {
	ID       int64      `json:"id"`
	AuthorID int64      `json:"author_id"`
	Author   string     `json:"author"`
	Channel  string     `json:"channel"`
	Topic    string     `json:"topic"`
	Text     string     `json:"text"`
	URL      string     `json:"url"`
	SentAt   time.Time  `json:"sent_at"`
	EditedAt *time.Time `json:"edited_at,omitempty"`
}

type SheetRecord struct {
	BookID      string              `json:"spreadsheet_id"`
	BookTitle   string              `json:"spreadsheet_title"`
	SheetID     int                 `json:"sheet_id"`
	SheetTitle  string              `json:"sheet_title"`
	Row         int                 `json:"row,omitempty"`
	Description string              `json:"description,omitempty"`
	Attributes  json.RawMessage     `json:"attributes,omitempty"`
	Headers     []catalog.SheetCell `json:"headers,omitempty"`
	Cells       []catalog.SheetCell `json:"cells,omitempty"`
	Status      string              `json:"status"`
}

type History struct {
	Student  Student       `json:"student"`
	Accounts []Account     `json:"zulip_accounts"`
	Messages []Message     `json:"messages"`
	Records  []SheetRecord `json:"sheet_records"`
}

type SheetMatch struct {
	Column    int    `json:"column"`
	Value     string `json:"value"`
	HeaderRow int    `json:"header_row"`
}

type sheetAttributes struct {
	Match *SheetMatch `json:"student_identity,omitempty"`
}

func CreateStudent(ctx context.Context, db *sql.DB, cohort string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO integration.students (cohort_code) VALUES (NULLIF($1, '')) RETURNING student_id
	`, strings.TrimSpace(cohort)).Scan(&id)
	return id, err
}

func LinkZulip(ctx context.Context, db *sql.DB, studentID, userID int64) error {
	result, err := db.ExecContext(ctx, `
		INSERT INTO integration.zulip_student_links (zulip_user_id, student_id, match_method, verified_at)
		VALUES ($1, $2, 'manual', CURRENT_TIMESTAMP)
		ON CONFLICT (zulip_user_id) DO UPDATE SET verified_at = CURRENT_TIMESTAMP
		WHERE zulip_student_links.student_id = EXCLUDED.student_id
	`, userID, studentID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("Zulip account %d already belongs to another student", userID)
	}
	return nil
}

func LinkSheet(ctx context.Context, db *sql.DB, studentID int64, bookID string, sheetID int, match SheetMatch) (int, error) {
	match.Value = strings.TrimSpace(match.Value)
	if match.Value == "" || match.Column <= 0 || match.HeaderRow < 0 {
		return 0, fmt.Errorf("invalid sheet identity")
	}
	attributes, err := json.Marshal(sheetAttributes{Match: &match})
	if err != nil {
		return 0, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var raw []byte
	var rowCount int
	err = tx.QueryRowContext(ctx, `
		SELECT cells, row_count FROM public.sheet_snapshots
		WHERE spreadsheet_id = $1 AND sheet_id = $2 FOR NO KEY UPDATE
	`, bookID, sheetID).Scan(&raw, &rowCount)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("sheet %s/%d was not imported", bookID, sheetID)
	}
	if err != nil {
		return 0, err
	}
	if match.HeaderRow > rowCount {
		return 0, fmt.Errorf("header row exceeds sheet height")
	}
	row, cells, err := locateSheetRow(raw, match)
	if err != nil {
		return 0, fmt.Errorf("sheet %s/%d: %w", bookID, sheetID, err)
	}

	key := fmt.Sprintf("identity:%d:%x", match.Column, sha256.Sum256([]byte(match.Value)))
	if err := checkSheetConfirmation(ctx, tx, bookID, sheetID, studentID, key, row, cells); err != nil {
		return 0, err
	}
	var recordID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO integration.sheet_records
			(spreadsheet_id, sheet_id, source_key, source_row, source_column, attributes)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (spreadsheet_id, sheet_id, source_key) DO UPDATE
		SET source_row = EXCLUDED.source_row, attributes = EXCLUDED.attributes, updated_at = CURRENT_TIMESTAMP
		RETURNING record_id
	`, bookID, sheetID, key, row, match.Column, attributes).Scan(&recordID)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO integration.student_sheet_links (student_id, record_id, match_status, match_method, verified_at)
		VALUES ($1, $2, 'confirmed', 'manual_identity', CURRENT_TIMESTAMP)
		ON CONFLICT (student_id, record_id) DO UPDATE
		SET match_status = 'confirmed', match_method = EXCLUDED.match_method, verified_at = EXCLUDED.verified_at
	`, studentID, recordID)
	if err != nil {
		return 0, err
	}
	return row, tx.Commit()
}

func checkSheetConfirmation(ctx context.Context, tx *sql.Tx, bookID string, sheetID int, studentID int64, key string, row int, cells []catalog.SheetCell) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT l.student_id, r.source_key, COALESCE(r.source_row, 0), r.attributes->'student_identity'
		FROM integration.student_sheet_links AS l
		JOIN integration.sheet_records AS r ON r.record_id = l.record_id
		WHERE r.spreadsheet_id = $1 AND r.sheet_id = $2 AND l.match_status = 'confirmed'
	`, bookID, sheetID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var owner int64
		var existingKey string
		var existingRow int
		var identity []byte
		if err := rows.Scan(&owner, &existingKey, &existingRow, &identity); err != nil {
			return err
		}
		if existingKey == key {
			if owner != studentID {
				return fmt.Errorf("sheet identity is already confirmed for another student")
			}
			continue
		}
		if len(identity) > 0 {
			match, err := decodeSheetMatch(identity)
			if err != nil {
				return err
			}
			existingRow, err = findSheetRow(cells, match)
			if errors.Is(err, errRowMissing) || errors.Is(err, errRowAmbiguous) {
				continue
			}
			if err != nil {
				return err
			}
		}
		if existingRow == row {
			if owner != studentID {
				return fmt.Errorf("sheet row %d is already confirmed for another student", row)
			}
			return fmt.Errorf("sheet row %d is already confirmed with a different identifier", row)
		}
	}
	return rows.Err()
}

func decodeSheetMatch(identity []byte) (SheetMatch, error) {
	var match SheetMatch
	if err := json.Unmarshal(identity, &match); err != nil {
		return match, fmt.Errorf("decode saved sheet identity: %w", err)
	}
	if match.Column <= 0 || match.Value == "" || match.HeaderRow < 0 {
		return match, fmt.Errorf("invalid saved sheet identity")
	}
	return match, nil
}

var (
	errRowMissing   = errors.New("identity not found in sheet")
	errRowAmbiguous = errors.New("identity matches multiple rows in sheet")
)

func locateSheetRow(raw []byte, match SheetMatch) (int, []catalog.SheetCell, error) {
	var cells []catalog.SheetCell
	if err := json.Unmarshal(raw, &cells); err != nil {
		return 0, nil, fmt.Errorf("decode sheet cells: %w", err)
	}
	row, err := findSheetRow(cells, match)
	return row, cells, err
}

func findSheetRow(cells []catalog.SheetCell, match SheetMatch) (int, error) {
	var row int
	for _, cell := range cells {
		if cell.Column != match.Column || cell.Row <= match.HeaderRow {
			continue
		}
		var data struct {
			Formatted *string `json:"formattedValue"`
			Effective struct {
				String *string     `json:"stringValue"`
				Number json.Number `json:"numberValue"`
			} `json:"effectiveValue"`
		}
		if err := json.Unmarshal(cell.Data, &data); err != nil {
			return 0, fmt.Errorf("decode identity cell: %w", err)
		}
		value := data.Effective.Number.String()
		if data.Effective.String != nil {
			value = *data.Effective.String
		}
		if data.Formatted != nil {
			value = *data.Formatted
		}
		if strings.TrimSpace(value) != match.Value {
			continue
		}
		if row != 0 && cell.Row != row {
			return 0, errRowAmbiguous
		}
		row = cell.Row
	}
	if row == 0 {
		return 0, errRowMissing
	}
	return row, nil
}
