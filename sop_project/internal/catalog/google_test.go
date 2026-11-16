package catalog

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type sheetsTestTransport http.HandlerFunc

func (transport sheetsTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response := httptest.NewRecorder()
	transport(response, request)
	return response.Result(), nil
}

func TestReadGoogleSheet(t *testing.T) {
	const (
		spreadsheetID = "sheet-id_123"
		a1Range       = "'БИВ22/23'!A5:G50"
	)
	client := &http.Client{Transport: sheetsTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("request method = %q, want GET", r.Method)
		}
		wantPath := "/v4/spreadsheets/" + spreadsheetID + "/values/" + a1Range
		if r.URL.Path != wantPath || !strings.Contains(r.URL.EscapedPath(), "22%2F23") {
			t.Errorf("request path = %q, want %q with escaped range separator", r.URL.EscapedPath(), wantPath)
		}
		if got := r.URL.Query().Get("valueRenderOption"); got != "FORMATTED_VALUE" {
			t.Errorf("valueRenderOption = %q, want FORMATTED_VALUE", got)
		}
		if got := r.URL.Query().Get("majorDimension"); got != "ROWS" {
			t.Errorf("majorDimension = %q, want ROWS", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"range": "'БИВ22/23'!A5:G50",
			"majorDimension": "ROWS",
			"values": [
				["url", "name", "id", "cohort", "availability", "discipline", "questions"],
				["https://example.com/a", "Лекции", "0007", "БИВ22", "restricted", "", "Кому выдают доступ?"],
				[],
				["https://example.com/b", "Практика", 9007199254740991]
			]
		}`))
	})}

	got, err := readGoogleSheet(t.Context(), client, "https://sheets.test", spreadsheetID, a1Range)
	if err != nil {
		t.Fatalf("read Google Sheet: %v", err)
	}
	want := []Resource{
		{
			SourceKey: "0007", Name: "Лекции", URL: "https://example.com/a",
			Cohort: "БИВ22", Availability: "restricted", Questions: "Кому выдают доступ?", RowNumber: 6,
		},
		{
			SourceKey: "9007199254740991", Name: "Практика", URL: "https://example.com/b",
			Availability: "unknown", RowNumber: 8,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resources = %#v, want %#v", got, want)
	}
}

func TestReadGoogleSheetAccessDenied(t *testing.T) {
	client := &http.Client{Transport: sheetsTestTransport(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"Permission denied","status":"PERMISSION_DENIED"}}`))
	})}

	rows, err := readGoogleSheet(t.Context(), client, "https://sheets.test", "sheet-id", "Catalog!A1:G")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %v, want Sheets API status 403", err)
	}
	if len(rows) != 0 {
		t.Errorf("received %d records when Sheets API denied access", len(rows))
	}
}

func TestSheetsAPIKeyStaysOnGoogleHTTPS(t *testing.T) {
	t.Setenv("GOOGLE_SHEETS_API_KEY", " test-key ")
	client, err := newSheetsClient(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.Transport.(sheetsKeyTransport)
	if !ok {
		t.Fatal("expected API key transport")
	}
	calls := 0
	transport.next = sheetsTestTransport(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Goog-Api-Key") != "test-key" || r.URL.Query().Has("key") {
			t.Error("key must be sent only in the API key header")
		}
		w.WriteHeader(http.StatusOK)
	})
	client.Transport = transport
	for _, tc := range []struct {
		endpoint string
		allow    bool
	}{
		{sheetsBaseURL + "/v4/spreadsheets/test", true},
		{"http://sheets.googleapis.com/v4/spreadsheets/test", false},
		{"https://sheets.googleapis.com.example/v4/spreadsheets/test", false},
	} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tc.endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if (err == nil) != tc.allow {
			t.Errorf("request to %q: got %v, want allow=%t", tc.endpoint, err, tc.allow)
		}
		if resp != nil {
			resp.Body.Close()
		}
		if req.Header.Get("X-Goog-Api-Key") != "" {
			t.Error("transport modified the original request")
		}
	}
	if calls != 1 {
		t.Errorf("transport forwarded %d requests, want 1", calls)
	}
}

func TestReadGoogleSnapshotsAcrossRanges(t *testing.T) {
	const title = "Курс 'Go'"
	var ranges []string
	client := &http.Client{Transport: sheetsTestTransport(func(w http.ResponseWriter, r *http.Request) {
		cellRange := r.URL.Query().Get("ranges")
		ranges = append(ranges, cellRange)
		var body string
		switch cellRange {
		case "":
			body = `{"spreadsheetId":"book","properties":{"title":"Ведомость"},"sheets":[{"properties":{"sheetId":7,"title":"Курс 'Go'","hidden":true,"sheetType":"GRID","gridProperties":{"rowCount":3,"columnCount":12000}}}]}`
		case "'Курс ''Go'''!A1:QSN2":
			body = `{"sheets":[{"properties":{"sheetId":7,"title":"Курс 'Go'"},"data":[{"rowData":[{"values":[{},{"userEnteredFormat":{"textFormat":{}}},{"formattedValue":"Первая"}]}]}]}]}`
		case "'Курс ''Go'''!A3:QSN3":
			body = `{"sheets":[{"properties":{"sheetId":7,"title":"Курс 'Go'"},"data":[{"startRow":2,"rowData":[{"values":[{"formattedValue":"Третья","hyperlink":"https://example.com"}]}]}]}]}`
		default:
			t.Errorf("unexpected range %q", cellRange)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Error(err)
		}
	})}

	sheets, err := readGoogleSnapshots(t.Context(), client, "https://sheets.test", "book")
	if err != nil {
		t.Fatal(err)
	}
	if len(sheets) != 1 || sheets[0].SheetTitle != title || !sheets[0].Hidden {
		t.Fatalf("unexpected snapshots: %+v", sheets)
	}
	var cells []SheetCell
	if err := json.Unmarshal(sheets[0].Cells, &cells); err != nil {
		t.Fatal(err)
	}
	if len(cells) != 2 || cells[0].Row != 1 || cells[0].Column != 3 ||
		cells[1].Row != 3 || cells[1].Column != 1 || !strings.Contains(string(cells[1].Data), "hyperlink") {
		t.Fatalf("unexpected cells: %+v", cells)
	}
	if got := len(ranges); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
}
