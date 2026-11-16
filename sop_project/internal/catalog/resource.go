package catalog

import (
	"fmt"
	"net/url"
	"strings"
)

type Resource struct {
	SourceKey    string
	Name         string
	URL          string
	Discipline   string
	Cohort       string
	Availability string
	Questions    string
	RowNumber    int
}

type rowParser struct {
	columns map[string]int
	seen    map[string]int
}

func newRowParser(header []string) (rowParser, error) {
	p := rowParser{columns: make(map[string]int, len(header)), seen: make(map[string]int)}
	for index, raw := range header {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			return p, fmt.Errorf("empty column name at column %d", index+1)
		}
		if _, exists := p.columns[name]; exists {
			return p, fmt.Errorf("duplicate column %q", name)
		}
		p.columns[name] = index
	}
	for _, name := range []string{"id", "name", "url"} {
		if _, exists := p.columns[name]; !exists {
			return p, fmt.Errorf("missing required column %q", name)
		}
	}
	return p, nil
}

func (p rowParser) parse(row []string, line int) (Resource, error) {
	value := func(name string) string {
		if index, exists := p.columns[name]; exists && index < len(row) {
			return strings.TrimSpace(row[index])
		}
		return ""
	}
	record := Resource{
		SourceKey:    value("id"),
		Name:         value("name"),
		URL:          value("url"),
		Discipline:   value("discipline"),
		Cohort:       value("cohort"),
		Availability: strings.ToLower(value("availability")),
		Questions:    value("questions"),
		RowNumber:    line,
	}
	if record.SourceKey == "" || record.Name == "" || record.URL == "" {
		return record, fmt.Errorf("line %d: id, name and url must be filled in", line)
	}
	if first, exists := p.seen[record.SourceKey]; exists {
		return record, fmt.Errorf("line %d: duplicate id %q (first seen on line %d)", line, record.SourceKey, first)
	}
	link, err := url.Parse(record.URL)
	if err != nil || link.Hostname() == "" || (link.Scheme != "https" && link.Scheme != "http") {
		return record, fmt.Errorf("line %d: url must be an absolute HTTP(S) link", line)
	}
	switch record.Availability {
	case "":
		record.Availability = "unknown"
	case "open", "restricted", "broken", "unknown":
	default:
		return record, fmt.Errorf("line %d: invalid availability %q (use open, restricted, broken or unknown)", line, record.Availability)
	}
	p.seen[record.SourceKey] = line
	return record, nil
}
