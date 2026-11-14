package catalog

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

func ReadCSV(input io.Reader) ([]Resource, error) {
	data, err := io.ReadAll(input)
	if err != nil {
		return nil, fmt.Errorf("read CSV: %w", err)
	}
	data = bytes.TrimPrefix(data, []byte("\ufeff"))
	if !utf8.Valid(data) || bytes.ContainsRune(data, '\x00') {
		return nil, errors.New("CSV must be UTF-8 without NUL characters")
	}
	r := csv.NewReader(bytes.NewReader(data))
	firstLine, _, _ := bytes.Cut(bytes.TrimLeft(data, "\r\n"), []byte{'\n'})
	if bytes.Count(firstLine, []byte{';'}) > bytes.Count(firstLine, []byte{','}) {
		r.Comma = ';'
	}
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	parser, err := newRowParser(header)
	if err != nil {
		return nil, err
	}
	var records []Resource
	for {
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			return records, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read CSV: %w", err)
		}
		line, _ := r.FieldPos(0)
		record, err := parser.parse(row, line)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
}
