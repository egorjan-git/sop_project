package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2/google"
)

const (
	sheetsReadOnly = "https://www.googleapis.com/auth/spreadsheets.readonly"
	sheetsHost     = "sheets.googleapis.com"
	sheetsBaseURL  = "https://" + sheetsHost
)

func newSheetsClient(ctx context.Context) (*http.Client, error) {
	if key := strings.TrimSpace(os.Getenv("GOOGLE_SHEETS_API_KEY")); key != "" {
		return &http.Client{Transport: sheetsKeyTransport{key: key, next: http.DefaultTransport}}, nil
	}
	client, err := google.DefaultClient(ctx, sheetsReadOnly)
	if err != nil {
		return nil, fmt.Errorf("Google credentials: set GOOGLE_SHEETS_API_KEY for shared-by-link sheets or configure Application Default Credentials: %w", err)
	}
	return client, nil
}

type sheetsKeyTransport struct {
	key  string
	next http.RoundTripper
}

func (t sheetsKeyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || request.URL.Host != sheetsHost {
		return nil, fmt.Errorf("Google Sheets API key can only be sent to %s over HTTPS", sheetsHost)
	}
	req := request.Clone(request.Context())
	req.Header.Set("X-Goog-Api-Key", t.key)
	return t.next.RoundTrip(req)
}

// ReadGoogleSheet reads a header and rows using a Sheets API key or read-only ADC.
func ReadGoogleSheet(ctx context.Context, spreadsheetID, a1Range string) ([]Resource, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	client, err := newSheetsClient(ctx)
	if err != nil {
		return nil, err
	}
	return readGoogleSheet(ctx, client, sheetsBaseURL, spreadsheetID, a1Range)
}

func readGoogleSheet(ctx context.Context, client *http.Client, baseURL, spreadsheetID, a1Range string) ([]Resource, error) {
	params := url.Values{"majorDimension": {"ROWS"}, "valueRenderOption": {"FORMATTED_VALUE"}}
	endpoint := baseURL + "/v4/spreadsheets/" + url.PathEscape(spreadsheetID) +
		"/values/" + url.PathEscape(a1Range) + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read Google Sheets: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		var body struct {
			Error struct{ Message string }
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&body)
		if body.Error.Message != "" {
			return nil, fmt.Errorf("Google Sheets HTTP %d: %s", res.StatusCode, body.Error.Message)
		}
		return nil, fmt.Errorf("Google Sheets HTTP %d", res.StatusCode)
	}
	var result struct {
		Range  string
		Values [][]any
	}
	decoder := json.NewDecoder(res.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode Google Sheets response: %w", err)
	}
	if len(result.Values) == 0 {
		return nil, fmt.Errorf("Google Sheets range %q is empty; expected a header", a1Range)
	}
	start := firstSheetRow(result.Range)
	if result.Range == "" {
		start = firstSheetRow(a1Range)
	}
	var parser rowParser
	var records []Resource
	for index, cells := range result.Values {
		line := start + index
		row := make([]string, len(cells))
		empty := true
		for col, cell := range cells {
			switch value := cell.(type) {
			case nil:
			case string:
				row[col] = value
			case json.Number:
				row[col] = value.String()
			case bool:
				row[col] = strconv.FormatBool(value)
			default:
				return nil, fmt.Errorf("line %d, column %d: unexpected Google Sheets value type %T", line, col+1, value)
			}
			empty = empty && strings.TrimSpace(row[col]) == ""
		}
		if index == 0 {
			parser, err = newRowParser(row)
			if err != nil {
				return nil, fmt.Errorf("Google Sheets header on line %d: %w", line, err)
			}
			continue
		}
		if empty {
			continue
		}
		record, err := parser.parse(row, line)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

var a1Start = regexp.MustCompile(`^\$?[a-zA-Z]*\$?([1-9][0-9]*)(?::|$)`)

func firstSheetRow(a1Range string) int {
	cell := a1Range[strings.LastIndex(a1Range, "!")+1:]
	if match := a1Start.FindStringSubmatch(cell); len(match) > 1 {
		if n, err := strconv.Atoi(match[1]); err == nil {
			return n
		}
	}
	return 1
}
