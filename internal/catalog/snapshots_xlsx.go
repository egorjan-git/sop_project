package catalog

import (
	"archive/zip"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

type xlsxSheet struct {
	ID    int    `xml:"sheetId,attr"`
	Title string `xml:"name,attr"`
	State string `xml:"state,attr"`
	RelID string `xml:"id,attr"`
}

type xlsxFormula struct {
	Text  string `xml:",chardata"`
	Type  string `xml:"t,attr"`
	Range string `xml:"ref,attr"`
}

type xlsxCell struct {
	Address string       `xml:"r,attr"`
	Type    string       `xml:"t,attr"`
	Style   int          `xml:"s,attr"`
	Value   *string      `xml:"v"`
	Formula *xlsxFormula `xml:"f"`
}

type xlsxPosition struct{ row, column int }

// ReadXLSXSnapshots reads workbook cells with their original coordinates and tab IDs.
// Formula results are taken from the workbook cache.
func ReadXLSXSnapshots(filePath, workbookID, workbookTitle string) (snapshots []SheetSnapshot, err error) {
	if strings.TrimSpace(workbookID) == "" || strings.TrimSpace(workbookTitle) == "" {
		return nil, fmt.Errorf("XLSX workbook ID and title are required")
	}
	book, err := excelize.OpenFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("open XLSX: %w", err)
	}
	defer func() { err = errors.Join(err, book.Close()) }()
	archive, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, fmt.Errorf("open XLSX package: %w", err)
	}
	defer func() { err = errors.Join(err, archive.Close()) }()
	files := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		files[file.Name] = file
	}

	var rootRelations xlsxRelations
	if err := decodeXLSXPart(files, "_rels/.rels", &rootRelations); err != nil {
		return nil, err
	}
	var workbookPart string
	for _, relation := range rootRelations.Relations {
		if strings.HasSuffix(relation.Type, "/officeDocument") {
			workbookPart = xlsxPartPath("", relation.Target)
			break
		}
	}
	if workbookPart == "" {
		return nil, fmt.Errorf("XLSX workbook part not found")
	}
	var workbook struct {
		Sheets []xlsxSheet `xml:"sheets>sheet"`
	}
	if err := decodeXLSXPart(files, workbookPart, &workbook); err != nil {
		return nil, err
	}
	var relations xlsxRelations
	relsPath := path.Join(path.Dir(workbookPart), "_rels", path.Base(workbookPart)+".rels")
	if err := decodeXLSXPart(files, relsPath, &relations); err != nil {
		return nil, err
	}
	byID := make(map[string]xlsxRelation, len(relations.Relations))
	for _, relation := range relations.Relations {
		byID[relation.ID] = relation
	}

	snapshots = make([]SheetSnapshot, 0, len(workbook.Sheets))
	for index, sheet := range workbook.Sheets {
		snapshot := SheetSnapshot{
			SpreadsheetID:    workbookID,
			SpreadsheetTitle: workbookTitle,
			SheetID:          sheet.ID,
			SheetTitle:       sheet.Title,
			SheetIndex:       index,
			Hidden:           sheet.State == "hidden" || sheet.State == "veryHidden",
			Cells:            json.RawMessage("[]"),
		}
		relation, ok := byID[sheet.RelID]
		if !ok {
			return nil, fmt.Errorf("XLSX tab %q has no package relationship", sheet.Title)
		}
		if strings.HasSuffix(relation.Type, "/worksheet") {
			part := xlsxPartPath(path.Dir(workbookPart), relation.Target)
			if err := readXLSXTab(book, files, part, &snapshot); err != nil {
				return nil, fmt.Errorf("read XLSX tab %q: %w", sheet.Title, err)
			}
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

type xlsxRelation struct {
	ID     string `xml:"Id,attr"`
	Type   string `xml:"Type,attr"`
	Target string `xml:"Target,attr"`
}

type xlsxRelations struct {
	Relations []xlsxRelation `xml:"Relationship"`
}

func xlsxPartPath(dir, target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(path.Clean(target), "/")
	}
	return path.Join(dir, target)
}

func decodeXLSXPart(files map[string]*zip.File, name string, value any) error {
	part := files[name]
	if part == nil {
		return fmt.Errorf("missing XLSX part %q", name)
	}
	stream, err := part.Open()
	if err != nil {
		return fmt.Errorf("open XLSX part %q: %w", name, err)
	}
	defer stream.Close()
	if err := xml.NewDecoder(stream).Decode(value); err != nil {
		return fmt.Errorf("decode XLSX part %q: %w", name, err)
	}
	return nil
}

func readXLSXTab(book *excelize.File, files map[string]*zip.File, part string, snapshot *SheetSnapshot) error {
	var sheet struct {
		Dimension struct {
			Range string `xml:"ref,attr"`
		} `xml:"dimension"`
		Rows []struct {
			Number int        `xml:"r,attr"`
			Cells  []xlsxCell `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := decodeXLSXPart(files, part, &sheet); err != nil {
		return err
	}
	if _, end, rangeFound := strings.Cut(sheet.Dimension.Range, ":"); rangeFound {
		if col, row, err := excelize.CellNameToCoordinates(end); err == nil {
			snapshot.RowCount, snapshot.ColumnCount = row, col
		}
	}

	stored := make(map[xlsxPosition]xlsxCell)
	rowNumber := 0
	for _, row := range sheet.Rows {
		rowNumber++
		if row.Number > 0 {
			rowNumber = row.Number
		}
		column := 0
		for _, cell := range row.Cells {
			column++
			cellRow := rowNumber
			if cell.Address != "" {
				var err error
				column, cellRow, err = excelize.CellNameToCoordinates(cell.Address)
				if err != nil {
					return fmt.Errorf("invalid cell coordinate %q: %w", cell.Address, err)
				}
			}
			stored[xlsxPosition{cellRow, column}] = cell
		}
	}

	links, err := book.GetHyperLinkCells(snapshot.SheetTitle, "")
	if err != nil {
		return fmt.Errorf("read hyperlinks: %w", err)
	}
	type hyperlink struct{ target, sourceRange string }
	byPosition := make(map[xlsxPosition]hyperlink, len(links))
	for _, cell := range links {
		address, _, isRange := strings.Cut(cell, ":")
		column, row, err := excelize.CellNameToCoordinates(address)
		if err != nil {
			return fmt.Errorf("read hyperlink address %q: %w", address, err)
		}
		_, link, err := book.GetCellHyperLink(snapshot.SheetTitle, address)
		if err != nil {
			return fmt.Errorf("read hyperlink at %s: %w", address, err)
		}
		info := hyperlink{target: link}
		if isRange {
			info.sourceRange = cell
		}
		byPosition[xlsxPosition{row, column}] = info
	}

	notes, err := book.GetComments(snapshot.SheetTitle)
	if err != nil {
		return fmt.Errorf("read notes: %w", err)
	}
	notesByPosition := make(map[xlsxPosition]excelize.Comment, len(notes))
	for _, note := range notes {
		column, row, err := excelize.CellNameToCoordinates(note.Cell)
		if err != nil {
			return fmt.Errorf("read note address %q: %w", note.Cell, err)
		}
		notesByPosition[xlsxPosition{row, column}] = note
	}

	for position := range byPosition {
		if _, ok := stored[position]; !ok {
			stored[position] = xlsxCell{}
		}
	}
	for position := range notesByPosition {
		if _, ok := stored[position]; !ok {
			stored[position] = xlsxCell{}
		}
	}
	positions := make([]xlsxPosition, 0, len(stored))
	for position := range stored {
		positions = append(positions, position)
	}
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].row != positions[j].row {
			return positions[i].row < positions[j].row
		}
		return positions[i].column < positions[j].column
	})

	cells := make([]SheetCell, 0, len(positions))
	for _, position := range positions {
		snapshot.RowCount = max(snapshot.RowCount, position.row)
		snapshot.ColumnCount = max(snapshot.ColumnCount, position.column)
		address, err := excelize.CoordinatesToCellName(position.column, position.row)
		if err != nil {
			return err
		}
		data, err := xlsxCellData(book, snapshot.SheetTitle, address, stored[position])
		if err != nil {
			return fmt.Errorf("read XLSX cell %s: %w", address, err)
		}
		if link := byPosition[position]; link.target != "" {
			data["hyperlink"] = link.target
			if link.sourceRange != "" {
				data["xlsxHyperlinkRange"] = link.sourceRange
			}
		}
		if note, ok := notesByPosition[position]; ok {
			var text strings.Builder
			text.WriteString(note.Text)
			for _, paragraph := range note.Paragraph {
				text.WriteString(paragraph.Text)
			}
			data["note"] = text.String()
			data["xlsxNoteAuthor"] = note.Author
		}
		encoded, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("encode XLSX cell %s: %w", address, err)
		}
		cells = append(cells, SheetCell{Row: position.row, Column: position.column, Data: encoded})
	}
	snapshot.Cells, err = json.Marshal(cells)
	return err
}

func xlsxCellData(book *excelize.File, sheet, address string, cell xlsxCell) (map[string]any, error) {
	data := make(map[string]any)
	if cell.Style > 0 {
		data["xlsxStyleId"] = cell.Style
	}
	if cell.Value != nil || cell.Type == "inlineStr" {
		formatted, err := book.GetCellValue(sheet, address)
		if err != nil {
			return nil, err
		}
		raw, err := book.GetCellValue(sheet, address, excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, err
		}
		data["formattedValue"] = formatted
		value := xlsxValue(cell.Type, raw)
		data["effectiveValue"] = value
		if cell.Formula == nil {
			data["userEnteredValue"] = value
		}
	}
	if cell.Formula != nil {
		formula, err := book.GetCellFormula(sheet, address)
		if err != nil {
			return nil, err
		}
		if formula == "" {
			formula = cell.Formula.Text
		}
		if formula != "" {
			data["userEnteredValue"] = map[string]string{"formulaValue": "=" + strings.TrimPrefix(formula, "=")}
		}
		if cell.Formula.Type != "" {
			data["xlsxFormulaType"] = cell.Formula.Type
		}
		if cell.Formula.Range != "" {
			data["xlsxFormulaRange"] = cell.Formula.Range
		}
	}
	return data, nil
}

func xlsxValue(cellType, raw string) map[string]any {
	switch cellType {
	case "b":
		return map[string]any{"boolValue": raw == "1" || strings.EqualFold(raw, "true")}
	case "e":
		return map[string]any{"errorValue": map[string]string{"type": "ERROR", "message": raw}}
	case "n", "":
		// Preserve the original numeric precision when Excel supplies a JSON number.
		if _, err := strconv.ParseFloat(raw, 64); err == nil && json.Valid([]byte(raw)) {
			return map[string]any{"numberValue": json.Number(raw)}
		}
	}
	return map[string]any{"stringValue": raw}
}
