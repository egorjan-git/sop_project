package main

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestImportXLSXWithoutTitle(t *testing.T) {
	workbook := excelize.NewFile()
	t.Cleanup(func() { workbook.Close() })
	if err := workbook.SetCellValue("Sheet1", "A1", "Проверка"); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "Ведомость.xlsx")
	if err := workbook.SaveAs(filename); err != nil {
		t.Fatal(err)
	}

	if err := importSnapshots(t.Context(), []string{
		"-xlsx", filename, "-id", "audit-excel", "-dry-run",
	}); err != nil {
		t.Fatal(err)
	}
}
