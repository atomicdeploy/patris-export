package recordsink

import (
	"archive/zip"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestCanonicalXLSXRoundTripPreservesTypesLayoutAndMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "canonical.xlsx")
	rows := []map[string]interface{}{
		{
			"product_code":              "00113007045",
			"name":                      "ماژول آزمون",
			"foreign_price":             json.Number("24.5"),
			"weight_grams":              json.Number("240"),
			"shipping_price_per_kg_cny": json.Number("120"),
			"markup_percent":            json.Number("30"),
			"irt_per_cny":               json.Number("29000"),
			"final_price":               int64(2009410),
			"warnings":                  []string{"price_verified"},
		},
	}
	options := XLSXOptions{
		RightToLeft: true,
		Metadata: XLSXMetadata{
			Schema:          "digitalogic.product-sync",
			SchemaVersion:   "1.0",
			FormulaID:       "landed_price_v1",
			FormulaRevision: "1.0.0",
			FormulaVersion:  "landed_price_v1",
			LocalCurrency:   "IRT",
			SourceID:        "patris-office",
			SourceDataset:   `C:\Patris\data4\kala.db`,
			SourceRevision:  "sha256:fixture-revision",
			GeneratedAt:     "2026-07-16T12:00:00Z",
			Warnings:        []string{"weight_inferred", "weight_inferred", "foreign_price_inferred"},
		},
	}
	if err := WriteXLSX(path, rows, "product_code", options); err != nil {
		t.Fatal(err)
	}

	book, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	if got := book.GetSheetList(); len(got) != 2 || got[0] != "Records" || got[1] != "Metadata" {
		t.Fatalf("sheet order = %v", got)
	}
	recordRows, err := book.GetRows("Records")
	if err != nil || len(recordRows) != 2 {
		t.Fatalf("records rows = %#v, err=%v", recordRows, err)
	}
	wantHeaders := []string{"product_code", "name", "foreign_price", "weight_grams", "shipping_price_per_kg_cny", "markup_percent", "irt_per_cny", "final_price", "warnings"}
	if strings.Join(recordRows[0], "|") != strings.Join(wantHeaders, "|") {
		t.Fatalf("canonical column order = %v, want %v", recordRows[0], wantHeaders)
	}
	columns := headerColumns(recordRows[0])
	assertXLSXCell(t, book, "Records", cellAt(columns, "product_code", 2), "00113007045", excelize.CellTypeSharedString)
	assertXLSXCell(t, book, "Records", cellAt(columns, "name", 2), "ماژول آزمون", excelize.CellTypeSharedString)
	assertXLSXCell(t, book, "Records", cellAt(columns, "foreign_price", 2), "24.5", excelize.CellTypeNumber)
	assertXLSXCell(t, book, "Records", cellAt(columns, "final_price", 2), "2009410", excelize.CellTypeNumber)

	codeStyle := styleForCell(t, book, "Records", cellAt(columns, "product_code", 2))
	if codeStyle.CustomNumFmt == nil || *codeStyle.CustomNumFmt != "@" {
		t.Fatalf("Code style = %+v, want text format", codeStyle)
	}
	priceStyle := styleForCell(t, book, "Records", cellAt(columns, "final_price", 2))
	if priceStyle.CustomNumFmt == nil || *priceStyle.CustomNumFmt != "#,##0" {
		t.Fatalf("final_price style = %+v", priceStyle)
	}
	decimalStyle := styleForCell(t, book, "Records", cellAt(columns, "foreign_price", 2))
	if decimalStyle.CustomNumFmt == nil || !strings.Contains(*decimalStyle.CustomNumFmt, "#") {
		t.Fatalf("foreign_price style = %+v", decimalStyle)
	}
	panes, err := book.GetPanes("Records")
	if err != nil || !panes.Freeze || panes.YSplit != 1 {
		t.Fatalf("records panes = %+v, err=%v", panes, err)
	}
	view, err := book.GetSheetView("Records", 0)
	if err != nil || view.RightToLeft == nil || !*view.RightToLeft {
		t.Fatalf("records RTL view = %+v, err=%v", view.RightToLeft, err)
	}
	width, err := book.GetColWidth("Records", "A")
	if err != nil || width < 18 {
		t.Fatalf("Code width = %v, err=%v", width, err)
	}

	metadataRows, err := book.GetRows("Metadata")
	if err != nil {
		t.Fatal(err)
	}
	metadata := metadataValues(metadataRows)
	for key, want := range map[string]string{
		"schema":           "digitalogic.product-sync",
		"schema_version":   "1.0",
		"formula_id":       "landed_price_v1",
		"formula_revision": "1.0.0",
		"source_id":        "patris-office",
		"source_dataset":   "kala.db",
		"source_revision":  "sha256:fixture-revision",
		"generated_at":     "2026-07-16T12:00:00Z",
	} {
		if metadata[key] != want {
			t.Errorf("metadata[%s] = %q, want %q", key, metadata[key], want)
		}
	}
	if metadata["warnings"] != "foreign_price_inferred\nweight_inferred" {
		t.Fatalf("metadata warnings = %q", metadata["warnings"])
	}
	metadataJSON, _ := json.Marshal(metadata)
	for _, forbidden := range []string{"password", "secret", "dsn", "Sharh1", `C:\\Patris`} {
		if strings.Contains(strings.ToLower(string(metadataJSON)), strings.ToLower(forbidden)) {
			t.Fatalf("metadata leaked %q: %s", forbidden, metadataJSON)
		}
	}

	worksheetXML := zipEntry(t, path, "xl/worksheets/sheet1.xml")
	if !strings.Contains(worksheetXML, `<autoFilter ref="$A$1:$I$2"`) {
		t.Fatalf("autofilter missing from Records sheet: %s", worksheetXML)
	}
}

func headerColumns(headers []string) map[string]int {
	result := make(map[string]int, len(headers))
	for index, header := range headers {
		result[header] = index + 1
	}
	return result
}

func cellAt(columns map[string]int, field string, row int) string {
	cell, _ := excelize.CoordinatesToCellName(columns[field], row)
	return cell
}

func assertXLSXCell(t *testing.T, book *excelize.File, sheet, cellName, want string, wantType excelize.CellType) {
	t.Helper()
	value, err := book.GetCellValue(sheet, cellName, excelize.Options{RawCellValue: true})
	if err != nil || value != want {
		t.Fatalf("%s!%s = %q, want %q, err=%v", sheet, cellName, value, want, err)
	}
	cellType, err := book.GetCellType(sheet, cellName)
	typeMatches := cellType == wantType
	// OOXML's canonical numeric representation omits the t attribute, which
	// Excelize reports as CellTypeUnset rather than CellTypeNumber.
	if wantType == excelize.CellTypeNumber && cellType == excelize.CellTypeUnset {
		typeMatches = true
	}
	if err != nil || !typeMatches {
		t.Fatalf("%s!%s type = %v, want %v, err=%v", sheet, cellName, cellType, wantType, err)
	}
}

func styleForCell(t *testing.T, book *excelize.File, sheet, cellName string) *excelize.Style {
	t.Helper()
	styleID, err := book.GetCellStyle(sheet, cellName)
	if err != nil {
		t.Fatal(err)
	}
	style, err := book.GetStyle(styleID)
	if err != nil {
		t.Fatal(err)
	}
	return style
}

func metadataValues(rows [][]string) map[string]string {
	result := map[string]string{}
	for _, row := range rows[1:] {
		if len(row) >= 2 {
			result[row[0]] = row[1]
		}
	}
	return result
}

func zipEntry(t *testing.T, path, name string) string {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name != name {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	t.Fatalf("zip entry %q not found", name)
	return ""
}
