package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func Load(ctx context.Context, db *sql.DB, studentID int64) (History, error) {
	result := History{
		Accounts: []Account{},
		Messages: []Message{},
		Records:  []SheetRecord{},
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `
		SELECT student_id, COALESCE(cohort_code, '') FROM integration.students WHERE student_id = $1
	`, studentID).Scan(&result.Student.ID, &result.Student.Cohort)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("student %d does not exist", studentID)
	}
	if err != nil {
		return result, err
	}

	result.Accounts, err = loadAccounts(ctx, tx, studentID)
	if err != nil {
		return result, err
	}
	result.Messages, err = loadMessages(ctx, tx, studentID)
	if err != nil {
		return result, err
	}
	result.Records, err = loadRecords(ctx, tx, studentID)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func loadAccounts(ctx context.Context, tx *sql.Tx, studentID int64) ([]Account, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT u.user_id, u.display_name FROM integration.zulip_student_links AS z
		JOIN zulip.users AS u ON u.user_id = z.zulip_user_id
		WHERE z.student_id = $1 ORDER BY u.user_id
	`, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	accounts := []Account{}
	for rows.Next() {
		var account Account
		if err := rows.Scan(&account.ID, &account.Name); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func loadMessages(ctx context.Context, tx *sql.Tx, studentID int64) ([]Message, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT m.message_id, u.user_id, u.display_name, c.name, m.topic,
		       m.content, m.source_url, m.sent_at, m.edited_at
		FROM integration.zulip_student_links AS z
		JOIN zulip.users AS u ON u.user_id = z.zulip_user_id
		JOIN zulip.messages AS m ON m.author_id = z.zulip_user_id
		JOIN zulip.channels AS c ON c.channel_id = m.channel_id
		WHERE z.student_id = $1 ORDER BY m.sent_at, m.message_id
	`, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := []Message{}
	for rows.Next() {
		var message Message
		var edited sql.NullTime
		if err := rows.Scan(&message.ID, &message.AuthorID, &message.Author, &message.Channel, &message.Topic,
			&message.Text, &message.URL, &message.SentAt, &edited); err != nil {
			return nil, err
		}
		message.SentAt = message.SentAt.UTC()
		if edited.Valid {
			timestamp := edited.Time.UTC()
			message.EditedAt = &timestamp
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func loadRecords(ctx context.Context, tx *sql.Tx, studentID int64) ([]SheetRecord, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT s.spreadsheet_id, s.spreadsheet_title, s.sheet_id, s.sheet_title,
		       COALESCE(r.source_row, 0), COALESCE(r.description, ''),
		       r.attributes - 'student_identity', r.attributes->'student_identity',
		       CASE WHEN r.attributes ? 'student_identity' THEN s.cells END
		FROM integration.student_sheet_links AS l
		JOIN integration.sheet_records AS r ON r.record_id = l.record_id
		JOIN public.sheet_snapshots AS s USING (spreadsheet_id, sheet_id)
		WHERE l.student_id = $1 AND l.match_status = 'confirmed'
		ORDER BY s.spreadsheet_title, s.spreadsheet_id, s.sheet_index, r.source_row, r.record_id
	`, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []SheetRecord{}
	for rows.Next() {
		var record SheetRecord
		var attributes, identity, cells []byte
		if err := rows.Scan(&record.BookID, &record.BookTitle, &record.SheetID, &record.SheetTitle,
			&record.Row, &record.Description, &attributes, &identity, &cells); err != nil {
			return nil, err
		}
		if string(attributes) != "{}" {
			record.Attributes = attributes
		}
		record.Status = "confirmed"
		if len(identity) > 0 {
			if err := record.readCells(identity, cells); err != nil {
				return nil, fmt.Errorf("read sheet %s/%d: %w", record.BookID, record.SheetID, err)
			}
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (record *SheetRecord) readCells(identity, raw []byte) error {
	match, err := decodeSheetMatch(identity)
	if err != nil {
		return err
	}
	row, cells, err := locateSheetRow(raw, match)
	switch {
	case errors.Is(err, errRowMissing):
		record.Row = 0
		record.Status = "identity_not_found"
	case errors.Is(err, errRowAmbiguous):
		record.Row = 0
		record.Status = "identity_ambiguous"
	case err != nil:
		return err
	default:
		record.Row = row
		for _, cell := range cells {
			switch cell.Row {
			case match.HeaderRow:
				record.Headers = append(record.Headers, cell)
			case row:
				record.Cells = append(record.Cells, cell)
			}
		}
	}
	return nil
}
