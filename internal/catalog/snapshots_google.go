package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	snapshotProperties = "sheetId,title,index,hidden,sheetType,gridProperties(rowCount,columnCount)"
	snapshotCellFields = "userEnteredValue,effectiveValue,formattedValue,hyperlink,note,userEnteredFormat(textFormat(link)),textFormatRuns(startIndex,format(link)),chipRuns"
	snapshotChunkCells = 24000
)

type googleSnapshotResponse struct {
	SpreadsheetID string `json:"spreadsheetId"`
	Properties    struct {
		Title string `json:"title"`
	} `json:"properties"`
	Sheets []struct {
		Properties struct {
			ID     int    `json:"sheetId"`
			Title  string `json:"title"`
			Index  int    `json:"index"`
			Hidden bool   `json:"hidden"`
			Type   string `json:"sheetType"`
			Grid   struct {
				Rows    int `json:"rowCount"`
				Columns int `json:"columnCount"`
			} `json:"gridProperties"`
		} `json:"properties"`
		Data []struct {
			StartRow    int `json:"startRow"`
			StartColumn int `json:"startColumn"`
			Rows        []struct {
				Values []json.RawMessage `json:"values"`
			} `json:"rowData"`
		} `json:"data"`
	} `json:"sheets"`
}

// ReadGoogleSnapshots reads every tab, including hidden tabs, with a Sheets API key or read-only ADC.
func ReadGoogleSnapshots(ctx context.Context, spreadsheetID string) ([]SheetSnapshot, error) {
	client, err := newSheetsClient(ctx)
	if err != nil {
		return nil, err
	}
	client.Timeout = 90 * time.Second
	endpoint := sheetsBaseURL + "/v4/spreadsheets/" + url.PathEscape(spreadsheetID)
	return readGoogleSnapshots(ctx, client, endpoint, spreadsheetID)
}

func readGoogleSnapshots(ctx context.Context, client *http.Client, endpoint, spreadsheetID string) ([]SheetSnapshot, error) {
	metadata, err := requestSnapshot(ctx, client, endpoint, url.Values{
		"fields": {"spreadsheetId,properties(title),sheets(properties(" + snapshotProperties + "))"},
	})
	if err != nil {
		return nil, fmt.Errorf("read spreadsheet %s metadata: %w", spreadsheetID, err)
	}
	if metadata.SpreadsheetID != spreadsheetID || metadata.Properties.Title == "" || len(metadata.Sheets) == 0 {
		return nil, fmt.Errorf("Google Sheets returned incomplete metadata for spreadsheet %s", spreadsheetID)
	}
	snapshots := make([]SheetSnapshot, 0, len(metadata.Sheets))
	for _, sheet := range metadata.Sheets {
		p := sheet.Properties
		snapshot := SheetSnapshot{
			SpreadsheetID:    spreadsheetID,
			SpreadsheetTitle: metadata.Properties.Title,
			SheetID:          p.ID,
			SheetTitle:       p.Title,
			SheetIndex:       p.Index,
			Hidden:           p.Hidden,
			RowCount:         p.Grid.Rows,
			ColumnCount:      p.Grid.Columns,
		}
		cells := make([]SheetCell, 0)
		if p.Grid.Rows > 0 && p.Grid.Columns > 0 {
			cells, err = readGoogleSnapshotCells(ctx, client, endpoint, snapshot, p.Type)
			if err != nil {
				return nil, fmt.Errorf("read sheet %q: %w", p.Title, err)
			}
		}
		snapshot.Cells, err = json.Marshal(cells)
		if err != nil {
			return nil, fmt.Errorf("encode sheet %q: %w", p.Title, err)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func readGoogleSnapshotCells(ctx context.Context, client *http.Client, endpoint string, sheet SheetSnapshot, sheetType string) ([]SheetCell, error) {
	rowsPerChunk := max(1, snapshotChunkCells/sheet.ColumnCount)
	quotedTitle := "'" + strings.ReplaceAll(sheet.SheetTitle, "'", "''") + "'"
	lastColumn := snapshotColumnName(sheet.ColumnCount)
	cells := make([]SheetCell, 0)
	for start := 0; start < sheet.RowCount; start += rowsPerChunk {
		end := min(start+rowsPerChunk, sheet.RowCount)
		cellRange := fmt.Sprintf("%s!A%d:%s%d", quotedTitle, start+1, lastColumn, end)
		if sheetType == "DATA_SOURCE" {
			// Sheets only permits requesting a DATA_SOURCE tab as a whole.
			end = sheet.RowCount
			cellRange = quotedTitle
		}
		response, err := requestSnapshot(ctx, client, endpoint, url.Values{
			"fields": {"sheets(properties(sheetId,title),data(startRow,startColumn,rowData(values(" + snapshotCellFields + "))))"},
			"ranges": {cellRange},
		})
		if err != nil {
			return nil, fmt.Errorf("range %s: %w", cellRange, err)
		}
		if len(response.Sheets) != 1 || response.Sheets[0].Properties.ID != sheet.SheetID || response.Sheets[0].Properties.Title != sheet.SheetTitle {
			return nil, fmt.Errorf("range %s returned an unexpected tab", cellRange)
		}
		for _, data := range response.Sheets[0].Data {
			if len(data.Rows) > 0 && (data.StartRow != start || data.StartColumn != 0 || len(data.Rows) > end-start) {
				return nil, fmt.Errorf("range %s returned unexpected row or column coordinates", cellRange)
			}
			for rowIndex, row := range data.Rows {
				if len(row.Values) > sheet.ColumnCount {
					return nil, fmt.Errorf("range %s returned an unexpected column count", cellRange)
				}
				for columnIndex, value := range row.Values {
					value = bytes.TrimSpace(value)
					if !hasSnapshotCellData(value) {
						continue
					}
					cells = append(cells, SheetCell{Row: data.StartRow + rowIndex + 1, Column: data.StartColumn + columnIndex + 1, Data: value})
				}
			}
		}
		if sheetType == "DATA_SOURCE" {
			break
		}
	}
	return cells, nil
}

func hasSnapshotCellData(data json.RawMessage) bool {
	if len(data) == 0 || bytes.Equal(data, []byte("{}")) || bytes.Equal(data, []byte("null")) {
		return false
	}
	if !bytes.Contains(data, []byte(`"userEnteredFormat"`)) {
		return true
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) > 1 {
		return true
	}
	format, exists := fields["userEnteredFormat"]
	if !exists {
		return len(fields) != 0
	}
	var parsed struct {
		TextFormat struct {
			Link struct {
				URI string
			}
		}
	}
	return json.Unmarshal(format, &parsed) == nil && parsed.TextFormat.Link.URI != ""
}

func snapshotColumnName(column int) string {
	name := ""
	for column > 0 {
		column--
		name = string(rune('A'+column%26)) + name
		column /= 26
	}
	return name
}

func requestSnapshot(ctx context.Context, client *http.Client, endpoint string, params url.Values) (googleSnapshotResponse, error) {
	for attempt := 0; ; attempt++ {
		result, code, retryAfter, err := getSnapshot(ctx, client, endpoint+"?"+params.Encode())
		retry := code == http.StatusTooManyRequests || code == http.StatusInternalServerError ||
			code == http.StatusBadGateway || code == http.StatusServiceUnavailable || code == http.StatusGatewayTimeout
		if err == nil || !retry || attempt >= 4 {
			return result, err
		}
		delay := (2 << attempt) * time.Second
		if code == http.StatusTooManyRequests {
			delay *= 4
		}
		if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 && seconds <= 120 {
			delay = max(delay, time.Duration(seconds)*time.Second)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
		}
	}
}

func getSnapshot(ctx context.Context, client *http.Client, endpoint string) (googleSnapshotResponse, int, string, error) {
	var result googleSnapshotResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return result, 0, "", err
	}
	res, err := client.Do(req)
	if err != nil {
		return result, 0, "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		var body struct {
			Error struct{ Message string }
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&body)
		message := body.Error.Message
		if message == "" {
			message = http.StatusText(res.StatusCode)
		}
		return result, res.StatusCode, res.Header.Get("Retry-After"), fmt.Errorf("Google Sheets HTTP %d: %s", res.StatusCode, message)
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return result, res.StatusCode, "", fmt.Errorf("decode Google Sheets snapshot: %w", err)
	}
	return result, res.StatusCode, "", nil
}
