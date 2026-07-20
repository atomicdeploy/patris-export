package recordsink

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"

	"github.com/atomicdeploy/patris-export/pkg/recordmap"
)

const (
	xlsxRecordsSheet  = "Records"
	xlsxMetadataSheet = "Metadata"
)

var canonicalXLSXFields = []string{
	"product_code",
	"name",
	"serial",
	"unit",
	"sale_price_source",
	"purchase_price_source",
	"warehouse_stock",
	"total_stock",
	"minimum_stock",
	"foreign_currency",
	"foreign_price",
	"weight_grams",
	"location",
	"shipping_method_id",
	"shipping_price_per_kg_cny",
	"markup_percent",
	"irt_per_cny",
	"pricing_catalog_revision",
	"pricing_catalog_status",
	"currency_effective_date",
	"final_price",
	"source_updated_at",
	"warnings",
	"record_hash",
}

// XLSXMetadata is the allowlisted, non-secret provenance written to the
// workbook Metadata sheet. It deliberately cannot carry arbitrary config,
// credentials, source paths, or raw Patris fields.
type XLSXMetadata struct {
	Schema         string
	FormulaID      string
	LocalCurrency  string
	SourceID       string
	SourceDataset  string
	SourceRevision string
	GeneratedAt    string
	Warnings       []string
}

type XLSXOptions struct {
	RightToLeft bool
	Metadata    XLSXMetadata
}

// WriteXLSX writes the same transformed rows consumed by the other record
// sinks. Options are variadic to preserve the existing public API.
func WriteXLSX(path string, rows []map[string]interface{}, keyField string, values ...XLSXOptions) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil && dir != "." {
		return err
	}
	options := normalizeXLSXOptions(values)
	book := excelize.NewFile()
	defer book.Close()
	defaultSheet := book.GetSheetName(0)
	if err := book.SetSheetName(defaultSheet, xlsxRecordsSheet); err != nil {
		return err
	}
	if err := writeRecordsWorksheet(book, rows, keyField, options); err != nil {
		return err
	}
	if err := writeMetadataWorksheet(book, options); err != nil {
		return err
	}
	book.SetActiveSheet(0)
	return book.SaveAs(path)
}

func normalizeXLSXOptions(values []XLSXOptions) XLSXOptions {
	options := XLSXOptions{}
	if len(values) > 0 {
		options = values[len(values)-1]
	}
	if strings.TrimSpace(options.Metadata.Schema) == "" {
		options.Metadata.Schema = "patris-export.records"
	}
	if strings.TrimSpace(options.Metadata.GeneratedAt) == "" {
		options.Metadata.GeneratedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	options.Metadata.Warnings = normalizedXLSXWarnings(options.Metadata.Warnings)
	return options
}

func normalizedXLSXWarnings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

type xlsxStyles struct {
	header  int
	text    int
	integer int
	decimal int
	wrapped int
}

func newXLSXStyles(book *excelize.File) (xlsxStyles, error) {
	header, err := book.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"1F4E78"}, Pattern: 1},
		Alignment: &excelize.Alignment{Vertical: "center"},
	})
	if err != nil {
		return xlsxStyles{}, err
	}
	textFormat := "@"
	text, err := book.NewStyle(&excelize.Style{CustomNumFmt: &textFormat})
	if err != nil {
		return xlsxStyles{}, err
	}
	integerFormat := "#,##0"
	integer, err := book.NewStyle(&excelize.Style{CustomNumFmt: &integerFormat})
	if err != nil {
		return xlsxStyles{}, err
	}
	decimalFormat := "#,##0.##############################"
	decimal, err := book.NewStyle(&excelize.Style{CustomNumFmt: &decimalFormat})
	if err != nil {
		return xlsxStyles{}, err
	}
	wrapped, err := book.NewStyle(&excelize.Style{CustomNumFmt: &textFormat, Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
	if err != nil {
		return xlsxStyles{}, err
	}
	return xlsxStyles{header: header, text: text, integer: integer, decimal: decimal, wrapped: wrapped}, nil
}

func writeRecordsWorksheet(book *excelize.File, rows []map[string]interface{}, keyField string, options XLSXOptions) error {
	styles, err := newXLSXStyles(book)
	if err != nil {
		return err
	}
	fields := xlsxFields(rows, keyField)
	widths := make([]float64, len(fields))
	for index, field := range fields {
		cellName, err := excelize.CoordinatesToCellName(index+1, 1)
		if err != nil {
			return err
		}
		if err := book.SetCellStr(xlsxRecordsSheet, cellName, field); err != nil {
			return err
		}
		widths[index] = readableCellWidth(field)
	}
	for rowIndex, row := range rows {
		for columnIndex, field := range fields {
			cellName, err := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+2)
			if err != nil {
				return err
			}
			value := row[field]
			style, err := writeXLSXCell(book, xlsxRecordsSheet, cellName, field, keyField, value, styles)
			if err != nil {
				return fmt.Errorf("write %s row %d: %w", field, rowIndex+2, err)
			}
			if style != 0 {
				if err := book.SetCellStyle(xlsxRecordsSheet, cellName, cellName, style); err != nil {
					return err
				}
			}
			if width := readableCellWidth(cell(value)); width > widths[columnIndex] {
				widths[columnIndex] = width
			}
		}
	}
	if len(fields) == 0 {
		return nil
	}
	lastColumn, err := excelize.ColumnNumberToName(len(fields))
	if err != nil {
		return err
	}
	if err := book.SetCellStyle(xlsxRecordsSheet, "A1", lastColumn+"1", styles.header); err != nil {
		return err
	}
	if err := book.SetRowHeight(xlsxRecordsSheet, 1, 24); err != nil {
		return err
	}
	for index, width := range widths {
		column, err := excelize.ColumnNumberToName(index + 1)
		if err != nil {
			return err
		}
		if isCodeColumn(fields[index], keyField) && width < 18 {
			width = 18
		}
		if err := book.SetColWidth(xlsxRecordsSheet, column, column, width); err != nil {
			return err
		}
	}
	lastRow := len(rows) + 1
	if err := book.AutoFilter(xlsxRecordsSheet, fmt.Sprintf("A1:%s%d", lastColumn, lastRow), nil); err != nil {
		return err
	}
	if err := book.SetPanes(xlsxRecordsSheet, &excelize.Panes{
		Freeze:      true,
		YSplit:      1,
		TopLeftCell: "A2",
		ActivePane:  "bottomLeft",
		Selection:   []excelize.Selection{{SQRef: "A2", ActiveCell: "A2", Pane: "bottomLeft"}},
	}); err != nil {
		return err
	}
	rtl := options.RightToLeft
	return book.SetSheetView(xlsxRecordsSheet, 0, &excelize.ViewOptions{RightToLeft: &rtl})
}

func xlsxFields(rows []map[string]interface{}, keyField string) []string {
	fields := recordmap.Fields(rows, keyField)
	if !strings.EqualFold(strings.TrimSpace(keyField), "product_code") {
		return fields
	}
	present := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		present[field] = struct{}{}
	}
	ordered := make([]string, 0, len(fields))
	used := make(map[string]struct{}, len(fields))
	for _, field := range canonicalXLSXFields {
		if _, exists := present[field]; exists {
			ordered = append(ordered, field)
			used[field] = struct{}{}
		}
	}
	for _, field := range fields {
		if _, exists := used[field]; !exists {
			ordered = append(ordered, field)
		}
	}
	return ordered
}

func writeXLSXCell(book *excelize.File, sheet, cellName, field, keyField string, value interface{}, styles xlsxStyles) (int, error) {
	if value == nil {
		return 0, nil
	}
	if isCodeColumn(field, keyField) {
		return styles.text, book.SetCellStr(sheet, cellName, cell(value))
	}
	switch typed := value.(type) {
	case bool:
		return 0, book.SetCellBool(sheet, cellName, typed)
	case int:
		value := int64(typed)
		if excelIntegerIsSafe(value) {
			return styles.integer, book.SetCellInt(sheet, cellName, value)
		}
		return styles.text, book.SetCellStr(sheet, cellName, strconv.FormatInt(value, 10))
	case int8:
		return styles.integer, book.SetCellInt(sheet, cellName, int64(typed))
	case int16:
		return styles.integer, book.SetCellInt(sheet, cellName, int64(typed))
	case int32:
		return styles.integer, book.SetCellInt(sheet, cellName, int64(typed))
	case int64:
		if excelIntegerIsSafe(typed) {
			return styles.integer, book.SetCellInt(sheet, cellName, typed)
		}
		return styles.text, book.SetCellStr(sheet, cellName, strconv.FormatInt(typed, 10))
	case uint:
		return writeXLSXUint(book, sheet, cellName, uint64(typed), styles)
	case uint8:
		return writeXLSXUint(book, sheet, cellName, uint64(typed), styles)
	case uint16:
		return writeXLSXUint(book, sheet, cellName, uint64(typed), styles)
	case uint32:
		return writeXLSXUint(book, sheet, cellName, uint64(typed), styles)
	case uint64:
		return writeXLSXUint(book, sheet, cellName, typed, styles)
	case float32:
		return styles.decimal, book.SetCellFloat(sheet, cellName, float64(typed), -1, 32)
	case float64:
		return styles.decimal, book.SetCellFloat(sheet, cellName, typed, -1, 64)
	case json.Number:
		return writeXLSXDecimal(book, sheet, cellName, typed.String(), styles)
	case string:
		return styles.text, book.SetCellStr(sheet, cellName, typed)
	case time.Time:
		return styles.text, book.SetCellStr(sheet, cellName, typed.UTC().Format(time.RFC3339Nano))
	default:
		return styles.wrapped, book.SetCellStr(sheet, cellName, cell(value))
	}
}

func writeXLSXUint(book *excelize.File, sheet, cellName string, value uint64, styles xlsxStyles) (int, error) {
	if value <= 999999999999999 {
		return styles.integer, book.SetCellUint(sheet, cellName, value)
	}
	return styles.text, book.SetCellStr(sheet, cellName, strconv.FormatUint(value, 10))
}

func writeXLSXDecimal(book *excelize.File, sheet, cellName, value string, styles xlsxStyles) (int, error) {
	if number, ok := exactExcelFloat(value); ok {
		return styles.decimal, book.SetCellFloat(sheet, cellName, number, -1, 64)
	}
	// OOXML numeric cells use IEEE-754/Excel precision. Preserve longer exact
	// decimal tokens as text instead of silently changing their value.
	return styles.text, book.SetCellStr(sheet, cellName, strings.TrimSpace(value))
}

func exactExcelFloat(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if decimalSignificantDigits(value) > 15 {
		return 0, false
	}
	want, ok := new(big.Rat).SetString(value)
	if !ok {
		return 0, false
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	stored := strconv.FormatFloat(number, 'f', -1, 64)
	got, ok := new(big.Rat).SetString(stored)
	return number, ok && want.Cmp(got) == 0
}

func decimalSignificantDigits(value string) int {
	digits := strings.NewReplacer("+", "", "-", "", ".", "").Replace(strings.TrimSpace(value))
	digits = strings.TrimLeft(digits, "0")
	digits = strings.TrimRight(digits, "0")
	return len(digits)
}

func excelIntegerIsSafe(value int64) bool {
	return value >= -999999999999999 && value <= 999999999999999
}

func isCodeColumn(field, keyField string) bool {
	field = strings.ToLower(strings.TrimSpace(field))
	keyField = strings.ToLower(strings.TrimSpace(keyField))
	return field == keyField || field == "code" || field == "product_code"
}

func readableCellWidth(value string) float64 {
	width := float64(utf8.RuneCountInString(value) + 2)
	if width < 10 {
		return 10
	}
	if width > 48 {
		return 48
	}
	return width
}

func writeMetadataWorksheet(book *excelize.File, options XLSXOptions) error {
	if _, err := book.NewSheet(xlsxMetadataSheet); err != nil {
		return err
	}
	styles, err := newXLSXStyles(book)
	if err != nil {
		return err
	}
	rows := [][2]string{
		{"Property", "Value"},
		{"schema", options.Metadata.Schema},
		{"formula_id", options.Metadata.FormulaID},
		{"local_currency", options.Metadata.LocalCurrency},
		{"source_id", options.Metadata.SourceID},
		{"source_dataset", safeDatasetName(options.Metadata.SourceDataset)},
		{"source_revision", options.Metadata.SourceRevision},
		{"generated_at", options.Metadata.GeneratedAt},
		{"warnings", strings.Join(options.Metadata.Warnings, "\n")},
	}
	for rowIndex, row := range rows {
		for columnIndex, value := range row {
			cellName, err := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+1)
			if err != nil {
				return err
			}
			if err := book.SetCellStr(xlsxMetadataSheet, cellName, value); err != nil {
				return err
			}
			style := styles.text
			if rowIndex == 0 {
				style = styles.header
			} else if columnIndex == 1 && row[0] == "warnings" {
				style = styles.wrapped
			}
			if err := book.SetCellStyle(xlsxMetadataSheet, cellName, cellName, style); err != nil {
				return err
			}
		}
	}
	if err := book.SetColWidth(xlsxMetadataSheet, "A", "A", 24); err != nil {
		return err
	}
	if err := book.SetColWidth(xlsxMetadataSheet, "B", "B", 72); err != nil {
		return err
	}
	if err := book.SetRowHeight(xlsxMetadataSheet, 1, 24); err != nil {
		return err
	}
	if len(options.Metadata.Warnings) > 1 {
		if err := book.SetRowHeight(xlsxMetadataSheet, len(rows), math.Min(120, 15*float64(len(options.Metadata.Warnings)))); err != nil {
			return err
		}
	}
	if err := book.SetPanes(xlsxMetadataSheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return err
	}
	rtl := options.RightToLeft
	return book.SetSheetView(xlsxMetadataSheet, 0, &excelize.ViewOptions{RightToLeft: &rtl})
}

func safeDatasetName(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	base := path.Base(value)
	if base == "." || base == "/" {
		return ""
	}
	return base
}
