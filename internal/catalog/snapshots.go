package catalog

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

type SheetSnapshot struct {
	SpreadsheetID    string          `json:"spreadsheet_id"`
	SpreadsheetTitle string          `json:"spreadsheet_title"`
	SheetTitle       string          `json:"sheet_title"`
	SheetID          int             `json:"sheet_id"`
	SheetIndex       int             `json:"sheet_index"`
	RowCount         int             `json:"row_count"`
	ColumnCount      int             `json:"column_count"`
	Hidden           bool            `json:"hidden"`
	Cells            json.RawMessage `json:"cells"`
}

type SheetCell struct {
	Row    int             `json:"row"`
	Column int             `json:"column"`
	Data   json.RawMessage `json:"data"`
}

func ReadSnapshotDirectory(dir string) ([]SheetSnapshot, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type tabKey struct {
		book string
		tab  int
	}
	seen := make(map[tabKey]bool)
	var snapshots []SheetSnapshot
	for _, file := range files {
		if !file.Type().IsRegular() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			return nil, err
		}
		var tab SheetSnapshot
		if err := json.Unmarshal(raw, &tab); err != nil {
			return nil, fmt.Errorf("read snapshot %s: %w", file.Name(), err)
		}
		if _, err := tab.parseCells(); err != nil {
			return nil, fmt.Errorf("invalid snapshot %s: %w", file.Name(), err)
		}
		key := tabKey{tab.SpreadsheetID, tab.SheetID}
		if seen[key] {
			return nil, fmt.Errorf("duplicate snapshot %s: spreadsheet %q tab %d", file.Name(), tab.SpreadsheetID, tab.SheetID)
		}
		seen[key] = true
		snapshots = append(snapshots, tab)
	}
	if len(snapshots) == 0 {
		return nil, fmt.Errorf("no JSON snapshots found in %s", dir)
	}
	return snapshots, nil
}

func (s SheetSnapshot) parseCells() ([]SheetCell, error) {
	if s.SpreadsheetID == "" || s.SpreadsheetTitle == "" || s.SheetTitle == "" ||
		s.SheetID < 0 || s.SheetIndex < 0 || s.RowCount < 0 || s.ColumnCount < 0 {
		return nil, fmt.Errorf("invalid spreadsheet or tab metadata")
	}
	raw := bytes.TrimSpace(s.Cells)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, fmt.Errorf("cells must be a JSON array")
	}
	var cells []SheetCell
	if err := json.Unmarshal(raw, &cells); err != nil {
		return nil, err
	}
	prev := SheetCell{}
	for _, c := range cells {
		if c.Row < 1 || c.Row > s.RowCount || c.Column < 1 || c.Column > s.ColumnCount {
			return nil, fmt.Errorf("cell R%dC%d outside the tab grid", c.Row, c.Column)
		}
		if c.Row < prev.Row || (c.Row == prev.Row && c.Column <= prev.Column) {
			return nil, fmt.Errorf("cell R%dC%d is repeated or out of order", c.Row, c.Column)
		}
		data := bytes.TrimSpace(c.Data)
		if len(data) == 0 || data[0] != '{' {
			return nil, fmt.Errorf("data at R%dC%d must be a JSON object", c.Row, c.Column)
		}
		prev = c
	}
	return cells, nil
}

func ExportSnapshots(out io.Writer, format string, snapshots []SheetSnapshot) error {
	if format == "json" {
		return json.NewEncoder(out).Encode(snapshots)
	}
	if format != "csv" {
		return fmt.Errorf("unsupported snapshot export format %q", format)
	}
	w := csv.NewWriter(out)
	if err := w.Write([]string{"spreadsheet_id", "spreadsheet_title", "sheet_id", "sheet_title", "hidden", "row", "column", "cell_data_json"}); err != nil {
		return err
	}
	for _, s := range snapshots {
		cells, err := s.parseCells()
		if err != nil {
			return fmt.Errorf("export %q / %q: %w", s.SpreadsheetTitle, s.SheetTitle, err)
		}
		for _, c := range cells {
			if err := w.Write([]string{s.SpreadsheetID, s.SpreadsheetTitle, strconv.Itoa(s.SheetID), s.SheetTitle,
				strconv.FormatBool(s.Hidden), strconv.Itoa(c.Row), strconv.Itoa(c.Column), string(c.Data)}); err != nil {
				return err
			}
		}
	}
	w.Flush()
	return w.Error()
}
