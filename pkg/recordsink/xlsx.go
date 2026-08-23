package recordsink

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"

	"github.com/atomicdeploy/patris-export/pkg/recordmap"
)

var terminalWooIDMarker = regexp.MustCompile(`(?i)[\p{Zs}\t]*[-\x{2013}\x{2014}][\p{Zs}\t]*WooID[\p{Zs}\t]+[0-9]+[\p{Zs}\t]*$`)

const (
	xlsxRecordsSheet  = "Records"
	xlsxMetadataSheet = "Metadata"
	XLSXModeValues    = "precalculated"
	XLSXModeFormula   = "formula"
)

var canonicalXLSXFields = []string{
	"product_code",
	"name",
	"category_code",
	"serial",
	"unit",
	"sale_price_source",
	"partner_price_source",
	"purchase_price_source",
	"warehouse_stock",
	"total_stock",
	"minimum_stock",
	"foreign_currency",
	"foreign_price",
	"weight_grams",
	"location",
	"shipping_method_id",
	"shipping_price_per_kg",
	"shipping_price_per_kg_currency",
	"markup_percent",
	"irt_per_cny",
	"pricing_catalog_revision",
	"pricing_catalog_status",
	"currency_effective_date",
	"price_source_amount",
	"price_source_currency",
	"price_source_kind",
	"price_rounding_digits",
	"price_rounding_mode",
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

// XLSXPreferences contains caller-selectable presentation behavior. It does
// not carry transformed values: every workbook still consumes the shared
// record-pipeline rows.
type XLSXPreferences struct {
	Language     string
	Mode         string
	ZebraRows    bool
	ColumnLabels map[string]string
}

type XLSXOptions struct {
	RightToLeft  bool
	Language     string
	Mode         string
	ZebraRows    *bool
	ColumnLabels map[string]string
	Metadata     XLSXMetadata
}

// ResolveXLSXLanguage normalizes a requested workbook language and lets
// "auto" follow the active UI/TUI language.
func ResolveXLSXLanguage(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "fa" || value == "en" {
		return value
	}
	fallback = strings.ToLower(strings.TrimSpace(fallback))
	if fallback == "fa" {
		return "fa"
	}
	return "en"
}

// WriteXLSX writes the same transformed rows consumed by the other record
// sinks. Options are variadic to preserve the existing public API.
func WriteXLSX(path string, rows []map[string]interface{}, keyField string, values ...XLSXOptions) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil && dir != "." {
		return err
	}
	options := normalizeXLSXOptions(values)
	if !strings.EqualFold(filepath.Ext(path), ".xlsx") {
		return fmt.Errorf("generated record export requires an .xlsx output; use the trusted XLSM or blank XLTM workflow for macro-enabled packages")
	}
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
	if options.Mode == XLSXModeFormula {
		mode := "auto"
		enabled := true
		if err := book.SetCalcProps(&excelize.CalcPropsOptions{
			CalcMode:       &mode,
			FullCalcOnLoad: &enabled,
			CalcOnSave:     &enabled,
			ForceFullCalc:  &enabled,
		}); err != nil {
			return err
		}
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
	options.Language = ResolveXLSXLanguage(options.Language, "en")
	options.Mode = normalizeXLSXMode(options.Mode)
	if options.ZebraRows == nil {
		enabled := true
		options.ZebraRows = &enabled
	}
	options.RightToLeft = options.RightToLeft || options.Language == "fa"
	options.Metadata.Warnings = normalizedXLSXWarnings(options.Metadata.Warnings)
	return options
}

func normalizeXLSXMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "formula", "formulas":
		return XLSXModeFormula
	default:
		return XLSXModeValues
	}
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
	header       int
	text         int
	integer      int
	decimal      int
	wrapped      int
	textZebra    int
	integerZebra int
	decimalZebra int
	wrappedZebra int
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
	text, err := newXLSXDataStyle(book, "@", false, false)
	if err != nil {
		return xlsxStyles{}, err
	}
	integer, err := newXLSXDataStyle(book, "#,##0", false, false)
	if err != nil {
		return xlsxStyles{}, err
	}
	decimal, err := newXLSXDataStyle(book, "#,##0.##############################", false, false)
	if err != nil {
		return xlsxStyles{}, err
	}
	wrapped, err := newXLSXDataStyle(book, "@", true, false)
	if err != nil {
		return xlsxStyles{}, err
	}
	textZebra, err := newXLSXDataStyle(book, "@", false, true)
	if err != nil {
		return xlsxStyles{}, err
	}
	integerZebra, err := newXLSXDataStyle(book, "#,##0", false, true)
	if err != nil {
		return xlsxStyles{}, err
	}
	decimalZebra, err := newXLSXDataStyle(book, "#,##0.##############################", false, true)
	if err != nil {
		return xlsxStyles{}, err
	}
	wrappedZebra, err := newXLSXDataStyle(book, "@", true, true)
	if err != nil {
		return xlsxStyles{}, err
	}
	return xlsxStyles{
		header: header, text: text, integer: integer, decimal: decimal, wrapped: wrapped,
		textZebra: textZebra, integerZebra: integerZebra, decimalZebra: decimalZebra, wrappedZebra: wrappedZebra,
	}, nil
}

func newXLSXDataStyle(book *excelize.File, numberFormat string, wrapped, zebra bool) (int, error) {
	style := &excelize.Style{CustomNumFmt: &numberFormat}
	if wrapped {
		style.Alignment = &excelize.Alignment{WrapText: true, Vertical: "top"}
	}
	if zebra {
		style.Fill = excelize.Fill{Type: "pattern", Color: []string{"EAF2F8"}, Pattern: 1}
	}
	return book.NewStyle(style)
}

func (styles xlsxStyles) withZebra(style int, enabled bool) int {
	if !enabled {
		return style
	}
	switch style {
	case styles.text:
		return styles.textZebra
	case styles.integer:
		return styles.integerZebra
	case styles.decimal:
		return styles.decimalZebra
	case styles.wrapped:
		return styles.wrappedZebra
	default:
		return style
	}
}

type xlsxColumn struct {
	Field       string
	WarehouseID string
	Header      string
}

func writeRecordsWorksheet(book *excelize.File, rows []map[string]interface{}, keyField string, options XLSXOptions) error {
	styles, err := newXLSXStyles(book)
	if err != nil {
		return err
	}
	columns := xlsxColumns(rows, keyField, options)
	widths := make([]float64, len(columns))
	fieldColumns := make(map[string]int, len(columns))
	for index, column := range columns {
		cellName, err := excelize.CoordinatesToCellName(index+1, 1)
		if err != nil {
			return err
		}
		if err := book.SetCellStr(xlsxRecordsSheet, cellName, column.Header); err != nil {
			return err
		}
		if column.WarehouseID == "" {
			fieldColumns[column.Field] = index + 1
		}
		widths[index] = readableCellWidth(column.Header)
	}
	for rowIndex, row := range rows {
		for columnIndex, column := range columns {
			cellName, err := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+2)
			if err != nil {
				return err
			}
			value := xlsxColumnValue(row, column)
			style := 0
			if options.Mode == XLSXModeFormula && column.Field == "final_price" {
				if formula, ok := xlsxPriceFormula(rowIndex+2, fieldColumns); ok {
					err = book.SetCellFormula(xlsxRecordsSheet, cellName, formula)
					style = styles.integer
				}
			} else {
				style, err = writeXLSXCell(book, xlsxRecordsSheet, cellName, column.Field, keyField, value, styles)
			}
			if err != nil {
				return fmt.Errorf("write %s row %d: %w", column.Field, rowIndex+2, err)
			}
			if style != 0 {
				zebra := options.ZebraRows != nil && *options.ZebraRows && rowIndex%2 == 1
				style = styles.withZebra(style, zebra)
				if err := book.SetCellStyle(xlsxRecordsSheet, cellName, cellName, style); err != nil {
					return err
				}
			}
			if width := readableCellWidth(cell(value)); width > widths[columnIndex] {
				widths[columnIndex] = width
			}
		}
	}
	if len(columns) == 0 {
		return nil
	}
	lastColumn, err := excelize.ColumnNumberToName(len(columns))
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
		if isCodeColumn(columns[index].Field, keyField) && width < 18 {
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

func xlsxColumns(rows []map[string]interface{}, keyField string, options XLSXOptions) []xlsxColumn {
	fields := xlsxFields(rows, keyField)
	columns := make([]xlsxColumn, 0, len(fields))
	warehouses := xlsxWarehouseIDs(rows)
	for _, field := range fields {
		if field == "warehouse_stock" && len(warehouses) > 0 {
			for _, warehouseID := range warehouses {
				columns = append(columns, xlsxColumn{
					Field:       field,
					WarehouseID: warehouseID,
					Header:      xlsxHeaderLabel(field, warehouseID, options.Language, options.ColumnLabels),
				})
			}
			continue
		}
		columns = append(columns, xlsxColumn{
			Field:  field,
			Header: xlsxHeaderLabel(field, "", options.Language, options.ColumnLabels),
		})
	}
	return columns
}

func xlsxWarehouseIDs(rows []map[string]interface{}) []string {
	seen := map[string]struct{}{}
	for _, row := range rows {
		value, exists := row["warehouse_stock"]
		if !exists || value == nil {
			continue
		}
		reflected := reflect.ValueOf(value)
		if reflected.Kind() != reflect.Map || reflected.Type().Key().Kind() != reflect.String {
			continue
		}
		iterator := reflected.MapRange()
		for iterator.Next() {
			warehouseID := strings.TrimSpace(iterator.Key().String())
			if warehouseID != "" {
				seen[warehouseID] = struct{}{}
			}
		}
	}
	ids := make([]string, 0, len(seen))
	for warehouseID := range seen {
		ids = append(ids, warehouseID)
	}
	sort.Slice(ids, func(left, right int) bool {
		leftNumber, leftErr := strconv.ParseUint(ids[left], 10, 64)
		rightNumber, rightErr := strconv.ParseUint(ids[right], 10, 64)
		if leftErr == nil && rightErr == nil && leftNumber != rightNumber {
			return leftNumber < rightNumber
		}
		return strings.ToLower(ids[left]) < strings.ToLower(ids[right])
	})
	return ids
}

func xlsxColumnValue(row map[string]interface{}, column xlsxColumn) interface{} {
	value := row[column.Field]
	if column.WarehouseID == "" || value == nil {
		return value
	}
	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Map || reflected.Type().Key().Kind() != reflect.String {
		return nil
	}
	entry := reflected.MapIndex(reflect.ValueOf(column.WarehouseID).Convert(reflected.Type().Key()))
	if !entry.IsValid() {
		return nil
	}
	return entry.Interface()
}

func xlsxPriceFormula(row int, fieldColumns map[string]int) (string, bool) {
	required := []string{
		"price_source_amount", "price_source_currency", "price_source_kind",
		"weight_grams", "shipping_price_per_kg", "shipping_price_per_kg_currency",
		"markup_percent", "irt_per_cny", "price_rounding_digits",
	}
	references := make(map[string]string, len(required))
	for _, field := range required {
		column, exists := fieldColumns[field]
		if !exists {
			return "", false
		}
		cellName, err := excelize.CoordinatesToCellName(column, row)
		if err != nil {
			return "", false
		}
		references[field] = cellName
	}
	return fmt.Sprintf(
		`=IF(AND(%s="foreign_price",%s="CNY",COUNT(%s,%s,%s,%s,%s,%s)=6,OR(%s="CNY",%s="IRR")),ROUND((%s*%s+%s/1000*IF(%s="CNY",%s*%s,%s/10))*(1+%s/100),-%s),IF(AND(%s="partner_price",%s="IRR",COUNT(%s,%s,%s)=3),ROUND((%s/10)*(1+%s/100),-%s),IF(AND(%s="sale_price_direct",%s="IRR",COUNT(%s)=1,MOD(%s,10)=0),%s/10,"")))`,
		references["price_source_kind"],
		references["price_source_currency"],
		references["price_source_amount"],
		references["weight_grams"],
		references["shipping_price_per_kg"],
		references["markup_percent"],
		references["irt_per_cny"],
		references["price_rounding_digits"],
		references["shipping_price_per_kg_currency"],
		references["shipping_price_per_kg_currency"],
		references["price_source_amount"],
		references["irt_per_cny"],
		references["weight_grams"],
		references["shipping_price_per_kg_currency"],
		references["shipping_price_per_kg"],
		references["irt_per_cny"],
		references["shipping_price_per_kg"],
		references["markup_percent"],
		references["price_rounding_digits"],
		references["price_source_kind"],
		references["price_source_currency"],
		references["price_source_amount"],
		references["markup_percent"],
		references["price_rounding_digits"],
		references["price_source_amount"],
		references["markup_percent"],
		references["price_rounding_digits"],
		references["price_source_kind"],
		references["price_source_currency"],
		references["price_source_amount"],
		references["price_source_amount"],
		references["price_source_amount"],
	), true
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
		if isHumanProductNameField(field) {
			typed = normalizeHumanProductName(typed)
		}
		return styles.text, book.SetCellStr(sheet, cellName, typed)
	case time.Time:
		return styles.text, book.SetCellStr(sheet, cellName, typed.UTC().Format(time.RFC3339Nano))
	default:
		return styles.wrapped, book.SetCellStr(sheet, cellName, cell(value))
	}
}

func isHumanProductNameField(field string) bool {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "name", "product_name":
		return true
	default:
		return strings.TrimSpace(field) == "نام کالا"
	}
}

// normalizeHumanProductName removes only a synthetic terminal WooID marker
// from a display name. It intentionally does not touch identifiers, SKUs, or
// embedded/non-terminal text.
func normalizeHumanProductName(value string) string {
	return terminalWooIDMarker.ReplaceAllString(value, "")
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
		{"xlsx_language", options.Language},
		{"xlsx_mode", options.Mode},
		{"zebra_rows", strconv.FormatBool(options.ZebraRows != nil && *options.ZebraRows)},
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
