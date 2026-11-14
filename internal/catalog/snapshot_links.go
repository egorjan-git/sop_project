package catalog

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type linkTextFormat struct {
	Link struct {
		URI string `json:"uri"`
	} `json:"link"`
}

type linkCell struct {
	Row    int `json:"row"`
	Column int `json:"column"`
	Data   struct {
		Hyperlink        string `json:"hyperlink"`
		UserEnteredValue struct {
			StringValue  string `json:"stringValue"`
			FormulaValue string `json:"formulaValue"`
		} `json:"userEnteredValue"`
		EffectiveValue struct {
			StringValue string `json:"stringValue"`
		} `json:"effectiveValue"`
		FormattedValue    string `json:"formattedValue"`
		UserEnteredFormat struct {
			TextFormat linkTextFormat `json:"textFormat"`
		} `json:"userEnteredFormat"`
		TextFormatRuns []struct {
			Format linkTextFormat `json:"format"`
		} `json:"textFormatRuns"`
		ChipRuns []struct {
			Chip struct {
				RichLinkProperties struct {
					URI string `json:"uri"`
				} `json:"richLinkProperties"`
			} `json:"chip"`
		} `json:"chipRuns"`
	} `json:"data"`
}

func SheetLinks(snapshots []SheetSnapshot, sources []Resource) ([]Resource, error) {
	ordered := append([]SheetSnapshot(nil), snapshots...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].SpreadsheetID != ordered[j].SpreadsheetID {
			return ordered[i].SpreadsheetID < ordered[j].SpreadsheetID
		}
		return ordered[i].SheetID < ordered[j].SheetID
	})
	seen := make(map[string]struct{})
	var links []Resource
	for _, sheet := range ordered {
		var cells []linkCell
		if err := json.Unmarshal(sheet.Cells, &cells); err != nil {
			return nil, fmt.Errorf("read links from tab %d of spreadsheet %q: %w", sheet.SheetID, sheet.SpreadsheetID, err)
		}
		sort.Slice(cells, func(i, j int) bool {
			if cells[i].Row != cells[j].Row {
				return cells[i].Row < cells[j].Row
			}
			return cells[i].Column < cells[j].Column
		})
		source := linkSource(sheet.SpreadsheetID, sources)
		for _, cell := range cells {
			for _, target := range cell.urls() {
				target = strings.TrimSpace(target)
				if !isCatalogCandidate(target) {
					continue
				}
				key := fmt.Sprintf("sheet-link-%x", sha256.Sum256([]byte(sheet.SpreadsheetID+"\x00"+target)))
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				address := snapshotColumnName(cell.Column) + strconv.Itoa(cell.Row)
				links = append(links, Resource{
					SourceKey:    key,
					Name:         fmt.Sprintf("%s / %s / %s", sheet.SpreadsheetTitle, sheet.SheetTitle, address),
					URL:          target,
					Discipline:   source.Discipline,
					Cohort:       source.Cohort,
					Availability: "unknown",
					Questions:    "Проверить содержание и доступ. " + sheetLinkOrigin(sheet, source, address),
					RowNumber:    cell.Row,
				})
			}
		}
	}
	return links, nil
}

func ReviewLinks(candidates, reviewed []Resource) ([]Resource, error) {
	if len(reviewed) == 0 {
		return nil, fmt.Errorf("review file contains no records")
	}
	byKey := make(map[string]Resource, len(candidates))
	for _, candidate := range candidates {
		byKey[candidate.SourceKey] = candidate
	}
	for _, record := range reviewed {
		candidate, found := byKey[record.SourceKey]
		if !found || candidate.URL != record.URL {
			return nil, fmt.Errorf("review line %d: ID and URL must match an extracted candidate", record.RowNumber)
		}
	}
	return reviewed, nil
}

func ExportLinkCandidates(out io.Writer, candidates []Resource) error {
	w := csv.NewWriter(out)
	if err := w.Write([]string{"id", "name", "url", "discipline", "cohort", "availability", "questions"}); err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err := w.Write([]string{candidate.SourceKey, candidate.Name, candidate.URL, candidate.Discipline,
			candidate.Cohort, candidate.Availability, candidate.Questions}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func linkSource(spreadsheetID string, sources []Resource) Resource {
	for _, source := range sources {
		if source.SourceKey == spreadsheetID || source.SourceKey == "gs-"+spreadsheetID || strings.Contains(source.URL, "/spreadsheets/d/"+spreadsheetID+"/") {
			return source
		}
	}
	return Resource{}
}

func sheetLinkOrigin(sheet SheetSnapshot, source Resource, address string) string {
	if source.SourceKey == "gs-"+sheet.SpreadsheetID || strings.HasPrefix(source.URL, "https://docs.google.com/spreadsheets/") {
		return fmt.Sprintf("Ссылка на ячейку https://docs.google.com/spreadsheets/d/%s/edit#gid=%d&range=%s", sheet.SpreadsheetID, sheet.SheetID, address)
	}
	location := fmt.Sprintf("Лист «%s», ячейка %s.", sheet.SheetTitle, address)
	if source.URL == "" {
		return location
	}
	return fmt.Sprintf("Исходная книга %s. %s", source.URL, location)
}

func (cell linkCell) urls() []string {
	data := cell.Data
	links := []string{data.Hyperlink, data.UserEnteredFormat.TextFormat.Link.URI}
	if data.Hyperlink == "" {
		if match := hyperlinkFormula.FindStringSubmatch(data.UserEnteredValue.FormulaValue); len(match) != 0 {
			links = append(links, strings.ReplaceAll(match[1], `""`, `"`))
		}
	}
	for _, run := range data.TextFormatRuns {
		links = append(links, run.Format.Link.URI)
	}
	for _, run := range data.ChipRuns {
		links = append(links, run.Chip.RichLinkProperties.URI)
	}
	for _, text := range []string{data.UserEnteredValue.StringValue, data.EffectiveValue.StringValue, data.FormattedValue} {
		links = append(links, literalWebLinks(text)...)
	}
	return links
}

var (
	webLinkPattern   = regexp.MustCompile(`(?i)https?://[^\s\p{Z}<>"'«»“”]+`)
	hyperlinkFormula = regexp.MustCompile(`(?i)^\s*=?\s*(?:_xlfn\.)?HYPERLINK\s*\(\s*"((?:[^"]|"")*)"\s*[,;)]`)
)

func literalWebLinks(text string) []string {
	trimmed := strings.TrimSpace(text)
	if isWebLink(trimmed) && !strings.ContainsAny(trimmed, " \t\r\n") {
		return []string{trimmed}
	}
	links := webLinkPattern.FindAllString(text, -1)
	for index, link := range links {
		link = strings.TrimRight(link, ".,;:!?…")
		for _, pair := range [][2]string{{"(", ")"}, {"[", "]"}, {"{", "}"}} {
			for strings.HasSuffix(link, pair[1]) && strings.Count(link, pair[1]) > strings.Count(link, pair[0]) {
				link = strings.TrimSuffix(link, pair[1])
			}
		}
		links[index] = link
	}
	return links
}

func isWebLink(target string) bool {
	parsed, err := url.Parse(target)
	return err == nil && parsed.Hostname() != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func isCatalogCandidate(target string) bool {
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "moss.stanford.edu":
		return false
	case "stats.auditory.ru":
		return !strings.HasPrefix(strings.ToLower(u.Path), "/plagiarism/")
	default:
		return true
	}
}
