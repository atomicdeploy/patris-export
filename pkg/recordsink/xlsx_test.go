package recordsink

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestNormalizeHumanProductNameStripsOnlyTerminalWooIDMarker(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "hyphen", in: "LM2576 - WooID 1234", want: "LM2576"},
		{name: "en dash", in: "LM2576 \u2013 WooID 1234", want: "LM2576"},
		{name: "em dash", in: "LM2576\u00a0\u2014\u00a0WooID\u00a01234  ", want: "LM2576"},
		{name: "mixed whitespace", in: "LM2576\t-   wooid\t1234", want: "LM2576"},
		{name: "no separator", in: "LM2576 WooID 1234", want: "LM2576 WooID 1234"},
		{name: "nonterminal", in: "LM2576 - WooID 1234 rev2", want: "LM2576 - WooID 1234 rev2"},
		{name: "nonnumeric", in: "LM2576 - WooID ABC", want: "LM2576 - WooID ABC"},
		{name: "sku-like", in: "SKU-WooID-1234", want: "SKU-WooID-1234"},
		{name: "embedded", in: "WooID 1234 - LM2576", want: "WooID 1234 - LM2576"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeHumanProductName(test.in); got != test.want {
				t.Fatalf("normalizeHumanProductName(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestWriteXLSXNormalizesNameWithoutChangingSKUOrCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.xlsx")
	marker := "Part - WooID 1234"
	rows := []map[string]interface{}{{
		"name":         marker,
		"product_code": marker,
		"sku":          marker,
	}}
	if err := WriteXLSX(path, rows, "product_code", XLSXOptions{ColumnLabels: map[string]string{
		"name":         "test_name",
		"product_code": "test_product_code",
		"sku":          "test_sku",
	}}); err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	values, err := book.GetRows(xlsxRecordsSheet, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatal(err)
	}
	columns := map[string]int{}
	for index, header := range values[0] {
		columns[header] = index
	}
	if got := values[1][columns["test_name"]]; got != "Part" {
		t.Fatalf("display name = %q, want Part", got)
	}
	for _, field := range []string{"test_product_code", "test_sku"} {
		if got := values[1][columns[field]]; got != marker {
			t.Fatalf("%s = %q, want unchanged %q", field, got, marker)
		}
	}
}

func TestWriteXLSXRejectsMacroEnabledOutputExtensions(t *testing.T) {
	for _, extension := range []string{".xlsm", ".xltm"} {
		err := WriteXLSX(filepath.Join(t.TempDir(), "records"+extension), []map[string]interface{}{{"product_code": "A"}}, "product_code")
		if err == nil || !strings.Contains(err.Error(), "generated record export") {
			t.Fatalf("WriteXLSX(%s) error = %v, want explicit generated-XLSX contract error", extension, err)
		}
	}
}

func TestCanonicalXLSXRoundTripPreservesTypesLayoutAndMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "canonical.xlsx")
	rows := []map[string]interface{}{
		{
			"product_code":                   "00113007045",
			"name":                           "ماژول آزمون",
			"foreign_price":                  json.Number("24.5"),
			"weight_grams":                   json.Number("240"),
			"shipping_price_per_kg":          json.Number("120"),
			"shipping_price_per_kg_currency": "CNY",
			"markup_percent":                 json.Number("30"),
			"irt_per_cny":                    json.Number("29000"),
			"price_source_amount":            json.Number("24.5"),
			"price_source_currency":          "CNY",
			"price_source_kind":              "foreign_price",
			"price_rounding_digits":          0,
			"price_rounding_mode":            "nearest_half_up",
			"final_price":                    int64(2009410),
			"warnings":                       []string{"price_verified"},
		},
	}
	options := XLSXOptions{
		RightToLeft: true,
		Language:    "en",
		Metadata: XLSXMetadata{
			Schema:         "patris.product-sync",
			FormulaID:      "landed_price",
			LocalCurrency:  "IRT",
			SourceID:       "patris-office",
			SourceDataset:  `C:\Patris\data4\kala.db`,
			SourceRevision: "sha256:fixture-revision",
			GeneratedAt:    "2026-07-16T12:00:00Z",
			Warnings:       []string{"weight_inferred", "weight_inferred", "foreign_price_inferred"},
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
	wantHeaders := []string{
		"Product Code", "Name", "Foreign Price", "Weight (g)", "Shipping Price/kg",
		"Shipping Currency", "Profit Margin (%)", "IRT per CNY", "Selected Price Amount",
		"Selected Price Currency", "Selected Price Source", "Price Rounding Digits",
		"Price Rounding Mode", "Final Price (IRT)", "Warnings",
	}
	if strings.Join(recordRows[0], "|") != strings.Join(wantHeaders, "|") {
		t.Fatalf("canonical column order = %v, want %v", recordRows[0], wantHeaders)
	}
	columns := headerColumns(recordRows[0])
	assertXLSXCell(t, book, "Records", cellAt(columns, "Product Code", 2), "00113007045", excelize.CellTypeSharedString)
	assertXLSXCell(t, book, "Records", cellAt(columns, "Name", 2), "ماژول آزمون", excelize.CellTypeSharedString)
	assertXLSXCell(t, book, "Records", cellAt(columns, "Foreign Price", 2), "24.5", excelize.CellTypeNumber)
	assertXLSXCell(t, book, "Records", cellAt(columns, "Selected Price Amount", 2), "24.5", excelize.CellTypeNumber)
	assertXLSXCell(t, book, "Records", cellAt(columns, "Final Price (IRT)", 2), "2009410", excelize.CellTypeNumber)

	codeStyle := styleForCell(t, book, "Records", cellAt(columns, "Product Code", 2))
	if codeStyle.CustomNumFmt == nil || *codeStyle.CustomNumFmt != "@" {
		t.Fatalf("Code style = %+v, want text format", codeStyle)
	}
	priceStyle := styleForCell(t, book, "Records", cellAt(columns, "Final Price (IRT)", 2))
	if priceStyle.CustomNumFmt == nil || *priceStyle.CustomNumFmt != "#,##0" {
		t.Fatalf("final_price style = %+v", priceStyle)
	}
	decimalStyle := styleForCell(t, book, "Records", cellAt(columns, "Foreign Price", 2))
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
		"schema":          "patris.product-sync",
		"formula_id":      "landed_price",
		"source_id":       "patris-office",
		"source_dataset":  "kala.db",
		"source_revision": "sha256:fixture-revision",
		"generated_at":    "2026-07-16T12:00:00Z",
		"xlsx_language":   "en",
		"xlsx_mode":       "precalculated",
		"zebra_rows":      "true",
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
	if !strings.Contains(worksheetXML, `<autoFilter ref="$A$1:$O$2"`) {
		t.Fatalf("autofilter missing from Records sheet: %s", worksheetXML)
	}
}

func TestFormulaXLSXLocalizesHeadersSplitsWarehousesAndCalculates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "formula-fa.xlsx")
	zebra := true
	rows := []map[string]interface{}{
		{
			"product_code":                   "001",
			"name":                           "ماژول آزمون",
			"warehouse_stock":                map[string]interface{}{"10": json.Number("3"), "2": json.Number("1")},
			"foreign_price":                  json.Number("24.5"),
			"weight_grams":                   json.Number("240"),
			"shipping_price_per_kg":          json.Number("120"),
			"shipping_price_per_kg_currency": "CNY",
			"markup_percent":                 json.Number("30"),
			"irt_per_cny":                    json.Number("29000"),
			"price_source_amount":            json.Number("24.5"),
			"price_source_currency":          "CNY",
			"price_source_kind":              "foreign_price",
			"price_rounding_digits":          0,
			"price_rounding_mode":            "nearest_half_up",
			"final_price":                    json.Number("2009410"),
		},
		{
			"product_code":                   "002",
			"name":                           "محصول بدون قیمت",
			"warehouse_stock":                map[string]interface{}{"2": nil},
			"weight_grams":                   json.Number("100"),
			"shipping_price_per_kg":          json.Number("120"),
			"shipping_price_per_kg_currency": "CNY",
			"markup_percent":                 json.Number("30"),
			"irt_per_cny":                    json.Number("29000"),
			"price_rounding_digits":          0,
			"price_rounding_mode":            "nearest_half_up",
			"final_price":                    nil,
		},
	}
	if err := WriteXLSX(path, rows, "product_code", XLSXOptions{
		Language:  "fa",
		Mode:      "formula",
		ZebraRows: &zebra,
	}); err != nil {
		t.Fatal(err)
	}

	book, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	recordRows, err := book.GetRows("Records", excelize.Options{RawCellValue: true})
	if err != nil || len(recordRows) != 3 {
		t.Fatalf("records rows = %#v, err=%v", recordRows, err)
	}
	wantHeaders := []string{
		"کد کالا", "نام", "موجودی انبار ۲", "موجودی انبار ۱۰", "قیمت ارزی", "وزن (گرم)",
		"هزینه حمل/کیلوگرم", "ارز هزینه حمل", "حاشیه سود (%)", "نرخ یوان (تومان)",
		"مبلغ منبع قیمت", "ارز منبع قیمت", "نوع منبع قیمت", "تعداد رقم گردکردن قیمت",
		"روش گردکردن قیمت", "قیمت نهایی (تومان)",
	}
	if strings.Join(recordRows[0], "|") != strings.Join(wantHeaders, "|") {
		t.Fatalf("localized columns = %v, want %v", recordRows[0], wantHeaders)
	}
	columns := headerColumns(recordRows[0])
	assertXLSXCell(t, book, "Records", cellAt(columns, "کد کالا", 2), "001", excelize.CellTypeSharedString)
	assertXLSXCell(t, book, "Records", cellAt(columns, "موجودی انبار ۲", 2), "1", excelize.CellTypeNumber)
	assertXLSXCell(t, book, "Records", cellAt(columns, "موجودی انبار ۱۰", 2), "3", excelize.CellTypeNumber)

	finalCell := cellAt(columns, "قیمت نهایی (تومان)", 2)
	if formula, err := book.GetCellFormula("Records", finalCell); err != nil ||
		!strings.Contains(formula, `"foreign_price"`) ||
		!strings.Contains(formula, `"partner_price"`) ||
		!strings.Contains(formula, `"sale_price_direct"`) ||
		!strings.Contains(formula, `ROUND(`) {
		t.Fatalf("living source-aware formula = %q, err=%v", formula, err)
	}
	if calculated, err := book.CalcCellValue("Records", finalCell, excelize.Options{RawCellValue: true}); err != nil || calculated != "2009410" {
		t.Fatalf("calculated final price = %q, want 2009410, err=%v", calculated, err)
	}
	missingCell := cellAt(columns, "قیمت نهایی (تومان)", 3)
	if calculated, err := book.CalcCellValue("Records", missingCell, excelize.Options{RawCellValue: true}); err != nil || calculated != "" {
		t.Fatalf("missing-input formula result = %q, want blank, err=%v", calculated, err)
	}
	if value, err := book.GetCellValue("Records", cellAt(columns, "موجودی انبار ۲", 3)); err != nil || value != "" {
		t.Fatalf("explicit-null warehouse cell = %q, want blank, err=%v", value, err)
	}
	view, err := book.GetSheetView("Records", 0)
	if err != nil || view.RightToLeft == nil || !*view.RightToLeft {
		t.Fatalf("Persian workbook RTL view = %+v, err=%v", view.RightToLeft, err)
	}
	zebraStyle := styleForCell(t, book, "Records", "A3")
	if zebraStyle.Fill.Type != "pattern" || len(zebraStyle.Fill.Color) == 0 || zebraStyle.Fill.Color[0] != "EAF2F8" {
		t.Fatalf("second data row zebra style = %+v", zebraStyle.Fill)
	}
	metadata := metadataValues(mustXLSXRows(t, book, "Metadata"))
	if metadata["xlsx_language"] != "fa" || metadata["xlsx_mode"] != "formula" || metadata["zebra_rows"] != "true" {
		t.Fatalf("formula workbook metadata = %+v", metadata)
	}
}

func TestFormulaXLSXSupportsCNYAndIRRFreightAndRejectsInvalidCurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "formula-currencies.xlsx")
	rows := []map[string]interface{}{
		{
			"product_code":                   "CNY-1",
			"foreign_price":                  json.Number("10"),
			"weight_grams":                   json.Number("1000"),
			"shipping_price_per_kg":          json.Number("100"),
			"shipping_price_per_kg_currency": "CNY",
			"markup_percent":                 json.Number("30"),
			"irt_per_cny":                    json.Number("30000"),
			"price_source_amount":            json.Number("10"),
			"price_source_currency":          "CNY",
			"price_source_kind":              "foreign_price",
			"price_rounding_digits":          0,
			"price_rounding_mode":            "nearest_half_up",
			"final_price":                    json.Number("4290000"),
		},
		{
			"product_code":                   "IRR-1",
			"foreign_price":                  json.Number("10"),
			"weight_grams":                   json.Number("1000"),
			"shipping_price_per_kg":          json.Number("30000000"),
			"shipping_price_per_kg_currency": "IRR",
			"markup_percent":                 json.Number("30"),
			"irt_per_cny":                    json.Number("30000"),
			"price_source_amount":            json.Number("10"),
			"price_source_currency":          "CNY",
			"price_source_kind":              "foreign_price",
			"price_rounding_digits":          0,
			"price_rounding_mode":            "nearest_half_up",
			"final_price":                    json.Number("4290000"),
		},
		{
			"product_code":                   "INVALID-1",
			"foreign_price":                  json.Number("10"),
			"weight_grams":                   json.Number("1000"),
			"shipping_price_per_kg":          json.Number("100"),
			"shipping_price_per_kg_currency": "USD",
			"markup_percent":                 json.Number("30"),
			"irt_per_cny":                    json.Number("30000"),
			"price_source_amount":            json.Number("10"),
			"price_source_currency":          "CNY",
			"price_source_kind":              "foreign_price",
			"price_rounding_digits":          0,
			"price_rounding_mode":            "nearest_half_up",
			"final_price":                    nil,
		},
		{
			"product_code":          "PARTNER-1",
			"price_source_amount":   json.Number("1234500"),
			"price_source_currency": "IRR",
			"price_source_kind":     "partner_price",
			"markup_percent":        json.Number("0"),
			"price_rounding_digits": 2,
			"price_rounding_mode":   "nearest_half_up",
			"final_price":           json.Number("123500"),
		},
		{
			"product_code":          "DIRECT-1",
			"price_source_amount":   json.Number("12000"),
			"price_source_currency": "IRR",
			"price_source_kind":     "sale_price_direct",
			"final_price":           json.Number("1200"),
		},
	}
	if err := WriteXLSX(path, rows, "product_code", XLSXOptions{Language: "en", Mode: "formula"}); err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	columns := headerColumns(mustXLSXRows(t, book, "Records")[0])
	finalColumn := columns["Final Price (IRT)"]
	for row, want := range map[int]string{2: "4290000", 3: "4290000", 4: "", 5: "123500", 6: "1200"} {
		cell, _ := excelize.CoordinatesToCellName(finalColumn, row)
		got, calcErr := book.CalcCellValue("Records", cell, excelize.Options{RawCellValue: true})
		if calcErr != nil || got != want {
			t.Fatalf("formula result at %s = %q, want %q, err=%v", cell, got, want, calcErr)
		}
	}
	formulaCell, _ := excelize.CoordinatesToCellName(finalColumn, 2)
	formula, err := book.GetCellFormula("Records", formulaCell)
	if err != nil || !strings.Contains(formula, `="CNY"`) || !strings.Contains(formula, `="IRR"`) ||
		!strings.Contains(formula, `"partner_price"`) || !strings.Contains(formula, `"sale_price_direct"`) ||
		!strings.Contains(formula, "MOD(") || !strings.Contains(formula, "/10") {
		t.Fatalf("currency-aware formula = %q, err=%v", formula, err)
	}
}

func TestXLSXPreferencesSupportCustomLabelsLTRAndNoZebra(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.xlsx")
	zebra := false
	rows := []map[string]interface{}{
		{"product_code": "0001", "warehouse_stock": map[string]float64{"A": 2}},
		{"product_code": "0002", "warehouse_stock": map[string]float64{"A": 4}},
	}
	if err := WriteXLSX(path, rows, "product_code", XLSXOptions{
		Language:     "en",
		ZebraRows:    &zebra,
		ColumnLabels: map[string]string{"Code": "Item Code", "ANBAR": "Depot Stock"},
	}); err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	rowsOut := mustXLSXRows(t, book, "Records")
	if got := strings.Join(rowsOut[0], "|"); got != "Item Code|Depot Stock A" {
		t.Fatalf("custom headers = %q", got)
	}
	view, err := book.GetSheetView("Records", 0)
	if err != nil || view.RightToLeft == nil || *view.RightToLeft {
		t.Fatalf("English workbook should be LTR: %+v, err=%v", view.RightToLeft, err)
	}
	style := styleForCell(t, book, "Records", "A3")
	if style.Fill.Type == "pattern" && len(style.Fill.Color) > 0 && style.Fill.Color[0] == "EAF2F8" {
		t.Fatalf("zebra fill remained enabled: %+v", style.Fill)
	}
}

func TestConfiguredXLSXLabelUsesDeterministicCanonicalAliasPrecedence(t *testing.T) {
	configured := map[string]string{
		"product_code":    "Canonical Code",
		"Code":            "Legacy Code",
		"warehouse_stock": "Canonical Stock",
		"ANBAR":           "Legacy Depot",
		"Warehouse":       "Old Warehouse",
	}
	for iteration := 0; iteration < 100; iteration++ {
		if got := configuredXLSXLabel("product_code", "en", configured); got != "Canonical Code" {
			t.Fatalf("product_code label = %q, want canonical label", got)
		}
		if got := configuredXLSXLabel("warehouse_stock", "en", configured); got != "Canonical Stock" {
			t.Fatalf("warehouse_stock label = %q, want canonical label", got)
		}
	}

	// A canonical default English label intentionally defers to the built-in
	// Persian vocabulary; a stale legacy alias must not override it.
	configured["warehouse_stock"] = "Warehouse Stock"
	if got := configuredXLSXLabel("warehouse_stock", "fa", configured); got != "" {
		t.Fatalf("Persian warehouse override = %q, want built-in localization", got)
	}

	delete(configured, "product_code")
	configured[" product_code "] = "Trimmed Canonical Code"
	if got := configuredXLSXLabel("product_code", "en", configured); got != "Trimmed Canonical Code" {
		t.Fatalf("trimmed canonical label = %q", got)
	}
}

func TestConfiguredXLSXProductCodeLabelsMigrateToNeutralLocalizedOutput(t *testing.T) {
	for _, test := range []struct {
		name       string
		language   string
		configured map[string]string
		want       string
	}{
		{name: "English deprecated brand", language: "en", configured: map[string]string{"product_code": "Patris Code"}, want: "Product Code"},
		{name: "Persian deprecated brand", language: "fa", configured: map[string]string{"Code": "کد پاتریس"}, want: "کد کالا"},
		{name: "English Persian brand", language: "en", configured: map[string]string{"Code": "کد‌پاتریس"}, want: "Product Code"},
		{name: "Persian English brand", language: "fa", configured: map[string]string{"product_code": "  PATRIS   CODE  "}, want: "کد کالا"},
		{name: "English legacy default", language: "en", configured: map[string]string{"Code": "Code"}, want: "Product Code"},
		{name: "Persian legacy default", language: "fa", configured: map[string]string{"product_code": "کد"}, want: "کد کالا"},
		{name: "Custom label remains authoritative", language: "en", configured: map[string]string{"Code": "Customer SKU"}, want: "Customer SKU"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := xlsxHeaderLabel("product_code", "", test.language, test.configured); got != test.want {
				t.Fatalf("xlsxHeaderLabel() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveXLSXLanguage(t *testing.T) {
	for _, test := range []struct {
		value, fallback, want string
	}{
		{value: "fa", fallback: "en", want: "fa"},
		{value: "EN", fallback: "fa", want: "en"},
		{value: "auto", fallback: "fa", want: "fa"},
		{value: "", fallback: "en", want: "en"},
	} {
		if got := ResolveXLSXLanguage(test.value, test.fallback); got != test.want {
			t.Errorf("ResolveXLSXLanguage(%q, %q) = %q, want %q", test.value, test.fallback, got, test.want)
		}
	}
}

type calculatorCardContract struct {
	name         string
	tableRange   string
	valueCell    string
	settingsCell string
}

type calculatorTemplateContract struct {
	name         string
	fileName     string
	productRange string
	headers      []string
	cards        []calculatorCardContract
}

func dynamicCalculatorContracts() []calculatorTemplateContract {
	return []calculatorTemplateContract{
		{
			name:         "canonical",
			fileName:     "لیست قیمت دیجیتالاجیک.xltm",
			productRange: "B5:K6",
			headers: []string{
				"قیمت فروش (تومان)",
				"وزن کالا (گرم)",
				"سایر",
				"محل کالا",
				"قیمت خرید (یوآن)",
				"موجودی کل",
				"کد کالا",
				"نام کالا",
				"شناسه ووکامرس",
				"دسته‌بندی",
			},
			cards: []calculatorCardContract{
				{name: "Yuan_Price", tableRange: "M6:M7", valueCell: "M7", settingsCell: "B10"},
				{name: "Shipping", tableRange: "O6:O7", valueCell: "O7", settingsCell: "B14"},
				{name: "Profit", tableRange: "O9:O10", valueCell: "O10", settingsCell: "B13"},
			},
		},
	}
}

func TestDynamicCalculatorTemplatesHaveNeutralMetadataAndNoExternalConnections(t *testing.T) {
	for _, contract := range dynamicCalculatorContracts() {
		t.Run(contract.name, func(t *testing.T) {
			lowerName := strings.ToLower(contract.fileName)
			if strings.Contains(lowerName, "patris") || strings.Contains(lowerName, "پاتریس") {
				t.Fatalf("customer-facing filename leaked upstream branding: %q", contract.fileName)
			}

			path := filepath.Join("..", "..", "docs", "examples", contract.fileName)
			archive, err := zip.OpenReader(path)
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()

			var coreProperties string
			for _, entry := range archive.File {
				if strings.HasPrefix(entry.Name, "xl/externalLinks/") || entry.Name == "xl/connections.xml" {
					t.Fatalf("template contains external Office connection %q", entry.Name)
				}
				reader, openErr := entry.Open()
				if openErr != nil {
					t.Fatal(openErr)
				}
				content, readErr := io.ReadAll(reader)
				reader.Close()
				if readErr != nil {
					t.Fatal(readErr)
				}
				lower := strings.ToLower(string(content))
				for _, forbidden := range []string{"x15ac:abspath", `c:\users\`, "/users/", "mahdi shokri", "mahdielector@"} {
					if strings.Contains(lower, forbidden) {
						t.Fatalf("template package entry %q leaked %q", entry.Name, forbidden)
					}
				}
				if entry.Name == "xl/workbook.xml" ||
					entry.Name == "xl/sharedStrings.xml" ||
					strings.HasPrefix(entry.Name, "xl/worksheets/") ||
					strings.HasPrefix(entry.Name, "xl/tables/") ||
					strings.HasPrefix(entry.Name, "xl/drawings/") ||
					strings.HasPrefix(entry.Name, "xl/comments") {
					for _, forbidden := range []string{"patris", "پاتریس"} {
						if strings.Contains(lower, forbidden) {
							t.Fatalf("customer-visible workbook entry %q leaked upstream branding %q", entry.Name, forbidden)
						}
					}
				}
				if entry.Name == "docProps/core.xml" {
					coreProperties = string(content)
				}
			}
			if !strings.Contains(coreProperties, "<dc:creator>AtomicDeploy</dc:creator>") ||
				!strings.Contains(coreProperties, "<cp:lastModifiedBy>AtomicDeploy</cp:lastModifiedBy>") {
				t.Fatalf("template core properties are not project-owned: %s", coreProperties)
			}
			if strings.Contains(coreProperties, "dcterms:created") || strings.Contains(coreProperties, "dcterms:modified") {
				t.Fatalf("template core properties contain volatile build timestamps: %s", coreProperties)
			}
		})
	}
}

func TestDynamicCalculatorPackagesPassFixedOpenXMLFontPolicy(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "docs", "examples", "*.xltm"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("locate canonical calculator: paths=%v error=%v", paths, err)
	}
	policy, err := ReadDynamicWorkbookFontPolicy(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	report, err := ValidateDynamicWorkbookFontPolicy(paths[0], policy)
	if err != nil {
		t.Fatal(err)
	}
	if report.MappedCells < 500 || report.DrawingTextRuns < 1 || report.DrawingFontSlots != report.DrawingTextRuns*3 {
		t.Fatalf("incomplete hard font audit: %+v", report)
	}
	if _, err := ValidateDynamicWorkbookFontPolicy(paths[0], DynamicWorkbookFontPolicy{}); err == nil {
		t.Fatal("missing configured font names passed the hard package validator")
	}
}

func TestDynamicCalculatorTemplatesPreservePersianPriceListContracts(t *testing.T) {
	const (
		priceSheet     = "محصولات"
		dashboardSheet = "داشبورد"
		settingsSheet  = "تنظیمات"
		syncSheet      = "داده‌های همگام‌سازی"
	)
	wantSheets := []string{priceSheet, dashboardSheet, settingsSheet, syncSheet}
	wantSyncHeaders := []string{
		"کلید همگام‌سازی",
		"ارز کالا",
		"نرخ حمل هر کیلو",
		"ارز حمل",
		"حاشیه سود (درصد)",
		"بهای یوآن",
		"بهای دلار",
		"تاریخ نرخ",
		"شناسه ووکامرس",
		"قیمت مشتری ووکامرس",
		"آخرین تغییر ووکامرس",
		"بازبینی رکورد",
		"نشانی محصول",
		"حاشیه سود کالا",
		"قیمت محاسباتی کالا",
		"قیمت ویژه ووکامرس (ممیزی)",
		"دسته‌بندی",
		"وضعیت انتشار",
		"هشدار قیمت",
		"نوع ردیف",
		"مبلغ منبع قیمت",
		"ارز منبع قیمت",
		"نوع منبع قیمت",
		"نشانی تصویر",
	}

	for _, contract := range dynamicCalculatorContracts() {
		t.Run(contract.name, func(t *testing.T) {
			path := filepath.Join("..", "..", "docs", "examples", contract.fileName)
			book, err := excelize.OpenFile(path)
			if err != nil {
				t.Fatal(err)
			}
			defer book.Close()

			if got := book.GetSheetList(); strings.Join(got, "|") != strings.Join(wantSheets, "|") {
				t.Fatalf("template sheet order = %v, want %v", got, wantSheets)
			}
			for _, sheet := range []string{priceSheet, dashboardSheet, settingsSheet} {
				visible, visibleErr := book.GetSheetVisible(sheet)
				if visibleErr != nil || !visible {
					t.Fatalf("%s visibility = %v, want visible, err=%v", sheet, visible, visibleErr)
				}
				rowHeight, rowHeightErr := book.GetRowHeight(sheet, 1)
				if rowHeightErr != nil || rowHeight < 59.99 || rowHeight > 60.01 {
					t.Fatalf("%s row 1 height = %v, want 60pt, err=%v", sheet, rowHeight, rowHeightErr)
				}
			}
			syncVisible, visibleErr := book.GetSheetVisible(syncSheet)
			if visibleErr != nil || syncVisible {
				t.Fatalf("%s visibility = %v, want hidden, err=%v", syncSheet, syncVisible, visibleErr)
			}
			workbookXML := zipEntry(t, path, "xl/workbook.xml")
			syncSheetStart := strings.Index(workbookXML, `<sheet name="`+syncSheet+`"`)
			if syncSheetStart < 0 {
				t.Fatalf("workbook.xml does not declare %q", syncSheet)
			}
			syncSheetEnd := strings.Index(workbookXML[syncSheetStart:], "/>")
			if syncSheetEnd < 0 ||
				!strings.Contains(workbookXML[syncSheetStart:syncSheetStart+syncSheetEnd], `state="veryHidden"`) {
				t.Fatalf("%s is not xlSheetVeryHidden in workbook.xml", syncSheet)
			}

			gotHeaders := make([]string, 0, len(contract.headers))
			for offset := range contract.headers {
				cell, cellErr := excelize.CoordinatesToCellName(offset+2, 5)
				if cellErr != nil {
					t.Fatal(cellErr)
				}
				value, valueErr := book.GetCellValue(priceSheet, cell, excelize.Options{RawCellValue: true})
				if valueErr != nil {
					t.Fatal(valueErr)
				}
				gotHeaders = append(gotHeaders, value)
			}
			if strings.Join(gotHeaders, "|") != strings.Join(contract.headers, "|") {
				t.Fatalf("Products headers = %v, want %v", gotHeaders, contract.headers)
			}

			tables, tableErr := book.GetTables(priceSheet)
			if tableErr != nil {
				t.Fatal(tableErr)
			}
			tablesByName := make(map[string]excelize.Table, len(tables))
			for _, table := range tables {
				tablesByName[table.Name] = table
			}
			for _, name := range []string{"Products", "Yuan_Price", "Shipping", "Profit"} {
				if _, ok := tablesByName[name]; !ok {
					t.Fatalf("template table %q is missing; tables = %+v", name, tables)
				}
			}
			if got := strings.ReplaceAll(tablesByName["Products"].Range, "$", ""); got != contract.productRange {
				t.Fatalf("Products range = %q, want empty-at-rest range %q", got, contract.productRange)
			}

			rangeParts := strings.Split(contract.productRange, ":")
			firstColumn, firstRow, coordinateErr := excelize.CellNameToCoordinates(rangeParts[0])
			if coordinateErr != nil {
				t.Fatal(coordinateErr)
			}
			lastColumn, lastRow, coordinateErr := excelize.CellNameToCoordinates(rangeParts[1])
			if coordinateErr != nil {
				t.Fatal(coordinateErr)
			}
			for row := firstRow + 1; row <= lastRow; row++ {
				for column := firstColumn; column <= lastColumn; column++ {
					cell, cellErr := excelize.CoordinatesToCellName(column, row)
					if cellErr != nil {
						t.Fatal(cellErr)
					}
					value, valueErr := book.GetCellValue(priceSheet, cell, excelize.Options{RawCellValue: true})
					if valueErr != nil {
						t.Fatal(valueErr)
					}
					formula, formulaErr := book.GetCellFormula(priceSheet, cell)
					if formulaErr != nil {
						t.Fatal(formulaErr)
					}
					if value != "" || formula != "" {
						t.Fatalf("template persisted product data at %s!%s: value=%q formula=%q", priceSheet, cell, value, formula)
					}
				}
			}

			for _, card := range contract.cards {
				if got := strings.ReplaceAll(tablesByName[card.name].Range, "$", ""); got != card.tableRange {
					t.Errorf("%s range = %q, want %q", card.name, got, card.tableRange)
				}
				value, valueErr := book.GetCellValue(priceSheet, card.valueCell, excelize.Options{RawCellValue: true})
				if valueErr != nil {
					t.Fatal(valueErr)
				}
				if value != "" {
					t.Errorf("%s persisted a runtime value at %s!%s: %q", card.name, priceSheet, card.valueCell, value)
				}
				formula, formulaErr := book.GetCellFormula(priceSheet, card.valueCell)
				if formulaErr != nil {
					t.Fatal(formulaErr)
				}
				if !strings.Contains(strings.ReplaceAll(formula, "'", ""), settingsSheet+"!"+card.settingsCell) {
					t.Errorf("%s formula = %q, want dynamic reference to %s!%s", card.name, formula, settingsSheet, card.settingsCell)
				}
			}

			for cell, want := range map[string]string{
				"B3":  "http://127.0.0.1:18080/api/product-sync",
				"B4":  "http://127.0.0.1:18080/api/pricing-sync/state",
				"B31": "خیر",
				"G8":  "canonical",
			} {
				got, valueErr := book.GetCellValue(settingsSheet, cell, excelize.Options{RawCellValue: true})
				if valueErr != nil || got != want {
					t.Fatalf("%s!%s = %q, want %q, err=%v", settingsSheet, cell, got, want, valueErr)
				}
			}
			for _, cell := range []string{
				"B10", "B11", "B12", "B13", "B14", "B15",
				"B18", "B19", "B20", "E20", "B21", "B22", "B23",
				"B32", "B33", "B34", "B35", "B36",
				"B46", "B47", "B48", "B49", "B50", "B51", "B52", "B53", "B54", "B55",
			} {
				value, valueErr := book.GetCellValue(settingsSheet, cell, excelize.Options{RawCellValue: true})
				if valueErr != nil || value != "" {
					t.Fatalf("%s!%s persisted runtime pricing state %q, err=%v", settingsSheet, cell, value, valueErr)
				}
			}
			settingsMergeCells, mergeErr := book.GetMergeCells(settingsSheet, true)
			if mergeErr != nil {
				t.Fatal(mergeErr)
			}
			settingsMerges := make(map[string]bool, len(settingsMergeCells))
			for index := range settingsMergeCells {
				settingsMerges[settingsMergeCells[index].GetStartAxis()+":"+
					settingsMergeCells[index].GetEndAxis()] = true
			}
			for _, row := range []int{
				10, 11, 12, 13, 14, 15,
				18, 19, 21, 22, 23, 26, 31,
				39, 40, 41, 42, 43,
				46, 47, 48, 49, 50, 51, 52, 53, 54, 55,
			} {
				wantMerge := fmt.Sprintf("B%d:F%d", row, row)
				if !settingsMerges[wantMerge] {
					t.Fatalf(
						"%s settings row %d does not preserve its independent merge %s; merges=%v",
						settingsSheet, row, wantMerge, settingsMerges,
					)
				}
			}
			if !settingsMerges["A37:F37"] {
				t.Fatalf("%s image-preview note is missing independent merge A37:F37; merges=%v", settingsSheet, settingsMerges)
			}
			for _, wantMerge := range []string{"B20:C20", "E20:F20"} {
				if !settingsMerges[wantMerge] {
					t.Fatalf("%s settings USD/CNY date row is missing merge %s; merges=%v",
						settingsSheet, wantMerge, settingsMerges)
				}
			}
			if value, valueErr := book.GetCellValue(
				settingsSheet, "A27", excelize.Options{RawCellValue: true},
			); valueErr != nil || value != "" {
				t.Fatalf("%s!A27 retained unnecessary static text %q, err=%v", settingsSheet, value, valueErr)
			}
			searchStyleID, styleErr := book.GetCellStyle(priceSheet, "C3")
			if styleErr != nil {
				t.Fatal(styleErr)
			}
			searchStyle, styleErr := book.GetStyle(searchStyleID)
			if styleErr != nil {
				t.Fatal(styleErr)
			}
			if searchStyle.NumFmt != 49 &&
				(searchStyle.CustomNumFmt == nil || *searchStyle.CustomNumFmt != "@") {
				t.Fatalf("search input C3 number format = %+v, want literal text (@)", searchStyle)
			}

			syncTables, syncTableErr := book.GetTables(syncSheet)
			if syncTableErr != nil {
				t.Fatal(syncTableErr)
			}
			if len(syncTables) != 1 || syncTables[0].Name != "SyncData" ||
				strings.ReplaceAll(syncTables[0].Range, "$", "") != "A1:X2" {
				t.Fatalf("SyncData table = %+v, want empty A1:X2 table", syncTables)
			}
			gotSyncHeaders := make([]string, 0, len(wantSyncHeaders))
			for column := 1; column <= len(wantSyncHeaders); column++ {
				cell, cellErr := excelize.CoordinatesToCellName(column, 1)
				if cellErr != nil {
					t.Fatal(cellErr)
				}
				value, valueErr := book.GetCellValue(syncSheet, cell, excelize.Options{RawCellValue: true})
				if valueErr != nil {
					t.Fatal(valueErr)
				}
				gotSyncHeaders = append(gotSyncHeaders, value)

				dataCell, cellErr := excelize.CoordinatesToCellName(column, 2)
				if cellErr != nil {
					t.Fatal(cellErr)
				}
				dataValue, valueErr := book.GetCellValue(syncSheet, dataCell, excelize.Options{RawCellValue: true})
				if valueErr != nil {
					t.Fatal(valueErr)
				}
				dataFormula, formulaErr := book.GetCellFormula(syncSheet, dataCell)
				if formulaErr != nil {
					t.Fatal(formulaErr)
				}
				if dataValue != "" || dataFormula != "" {
					t.Fatalf("SyncData persisted runtime data at %s: value=%q formula=%q", dataCell, dataValue, dataFormula)
				}
			}
			if strings.Join(gotSyncHeaders, "|") != strings.Join(wantSyncHeaders, "|") {
				t.Fatalf("SyncData headers = %v, want %v", gotSyncHeaders, wantSyncHeaders)
			}

			rawVisible, columnErr := book.GetColVisible(priceSheet, "D")
			if columnErr != nil || rawVisible {
				t.Errorf("raw compatibility column D visibility = %v, want hidden, err=%v", rawVisible, columnErr)
			}
			wooVisible, columnErr := book.GetColVisible(priceSheet, "J")
			if columnErr != nil || !wooVisible {
				t.Errorf("WooID column J visibility = %v, want visible, err=%v", wooVisible, columnErr)
			}
			for column, minimum := range map[string]float64{"B": 20, "K": 34} {
				width, widthErr := book.GetColWidth(priceSheet, column)
				if widthErr != nil || width < minimum {
					t.Errorf("Products column %s width = %.2f, want >= %.2f, err=%v", column, width, minimum, widthErr)
				}
			}
			styleID, styleErr := book.GetCellStyle(priceSheet, "J6")
			if styleErr != nil {
				t.Fatal(styleErr)
			}
			wooIDStyle, styleErr := book.GetStyle(styleID)
			if styleErr != nil {
				t.Fatal(styleErr)
			}
			if wooIDStyle.Alignment == nil ||
				wooIDStyle.Alignment.Horizontal != "left" ||
				wooIDStyle.Alignment.ReadingOrder != 1 {
				t.Errorf("WooID cell alignment = %+v, want left-to-right", wooIDStyle.Alignment)
			}
		})
	}
}

func TestDynamicCalculatorVBASourceGuardsLivePricingBeforeMutation(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "examples", "vba", "ProductCatalogSync.bas")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for offset, value := range content {
		if value > 0x7f {
			t.Fatalf("VBA source is not ASCII-safe: byte 0x%02x at offset %d", value, offset)
		}
	}
	source := string(content)

	constantStart := strings.Index(
		source,
		`Private Const RECONCILED_COLUMN_KEYS As String = _`,
	)
	constantEnd := strings.Index(source, "Private Const MB_RIGHT")
	if constantStart < 0 || constantEnd <= constantStart {
		t.Fatal("reconciled catalog column signature constant is missing")
	}
	var reconciledColumns strings.Builder
	for _, line := range strings.Split(source[constantStart:constantEnd], "\n") {
		parts := strings.Split(line, `"`)
		for index := 1; index < len(parts); index += 2 {
			reconciledColumns.WriteString(parts[index])
		}
	}
	wantReconciledColumns := strings.Join([]string{
		"sync_key", "reconciliation_status", "patris_code",
		"woocommerce_id", "parent_id", "product_type", "publication_status",
		"name", "part_number", "sku", "categories", "category_ids", "currency",
		"regular_price", "sale_price", "effective_price", "patris_final_price",
		"price_status", "stock_quantity", "stock_status", "patris_total_stock",
		"patris_minimum_stock", "patris_location", "weight_grams",
		"woocommerce_weight", "woocommerce_weight_unit", "foreign_price",
		"foreign_currency", "partner_price_irr", "price_source_amount",
		"price_source_currency", "price_source_kind", "price_rounding_digits",
		"price_rounding_mode", "shipping_method_id", "shipping_method_name_en",
		"shipping_method_name_fa", "shipping_price_per_kg",
		"shipping_price_per_kg_currency", "profit_margin_percent", "permalink",
		"image_url", "updated_at", "sync_status", "sync_error",
		"record_revision",
	}, ",")
	if reconciledColumns.String() != wantReconciledColumns {
		t.Fatalf(
			"VBA reconciled catalog columns = %q, want %q",
			reconciledColumns.String(),
			wantReconciledColumns,
		)
	}

	replacePosition := strings.Index(source, "ReplaceTableData table, mainOutput, dataRows, PRODUCT_COLUMN_COUNT")
	for _, requiredBeforeReplace := range []string{
		`CStr(JsonRuntime.JsonText(root, "schema")) <> "patris.product-sync"`,
		`JsonRuntime.JsonText(sourceValue, "revision")`,
		`datasetRevision = SiteText(catalog, "dataset_revision")`,
		"CatalogColumnSignature(catalog) <> RECONCILED_COLUMN_KEYS",
		"siteRows.Exists(identityKey)",
	} {
		position := strings.Index(source, requiredBeforeReplace)
		if position < 0 || replacePosition < 0 || position > replacePosition {
			t.Fatalf("VBA safeguard %q is missing or runs after product replacement", requiredBeforeReplace)
		}
	}
	section := func(startMarker, endMarker string) string {
		start := strings.Index(source, startMarker)
		if start < 0 {
			t.Fatalf("VBA section is missing: %s", startMarker)
		}
		endOffset := strings.Index(source[start+len(startMarker):], endMarker)
		if endOffset < 0 {
			t.Fatalf("VBA section terminator is missing: %s", endMarker)
		}
		return source[start : start+len(startMarker)+endOffset]
	}
	contractHandler := section("Private Sub HandleContractResponse", "Private Sub HandleSessionResponse")
	if strings.Index(contractHandler, "ReadSourceIdentity root") < 0 ||
		strings.Index(contractHandler, "ReadSourceIdentity root") > strings.Index(contractHandler, "BeginSessionRequest") {
		t.Fatal("source identity must be validated before the callback pipeline creates a session")
	}
	startHandler := section("Private Sub HandleSnapshotStartResponse", "Private Sub HandleSnapshotWaitResponse")
	for _, required := range []string{"ParsePricingSnapshotJob root", "mSseJobID = mSnapshotJobID", "BeginSnapshotWaitRequest"} {
		if !strings.Contains(startHandler, required) {
			t.Fatalf("snapshot start callback is missing %s", required)
		}
	}
	if strings.Contains(startHandler, "EnsureSseListener") {
		t.Fatal("cold refresh must not subscribe to durable SSE before the first atomic commit")
	}
	waitHandler := section("Private Sub HandleSnapshotWaitResponse", "Private Sub HandleSnapshotPayloadResponse")
	if !strings.Contains(waitHandler, "BeginSnapshotPayloadRequest") || strings.Contains(waitHandler, "Do While") {
		t.Fatal("terminal wait callback must transition once to the payload without polling")
	}
	queueHandler := section("Public Sub QueueAsyncDispatch", "Private Sub ScheduleQueuedAsyncDispatch")
	if !strings.Contains(queueHandler, "ScheduleQueuedAsyncDispatch") ||
		!strings.Contains(queueHandler, "mSaveRenameAsyncPending = True") ||
		strings.Contains(queueHandler, "DispatchAsyncRequest") ||
		strings.Contains(queueHandler, "FailActiveOperation") {
		t.Fatal("WinHTTP callbacks must only queue a rename-safe deferred dispatcher and return")
	}
	scheduleHandler := section("Private Sub ScheduleQueuedAsyncDispatch", "Public Sub DispatchQueuedAsyncRequests")
	for _, required := range []string{
		"Application.OnTime",
		"mAsyncDispatchTime = Now + TimeSerial(0, 0, 1)",
		`QualifiedWorkbookMacro("DispatchQueuedAsyncRequests")`,
		"DispatchFailed:",
		"mAsyncDispatchScheduled = False",
	} {
		if !strings.Contains(scheduleHandler, required) {
			t.Fatalf("async callback dispatcher is not safely deferred: %s", required)
		}
	}
	if strings.Count(scheduleHandler, "Application.OnTime") != 1 ||
		!strings.Contains(scheduleHandler, "mSaveRenameSchedulesSuspended") ||
		strings.Contains(scheduleHandler, "DispatchAsyncRequest") ||
		strings.Contains(scheduleHandler, "FailActiveOperation") {
		t.Fatal("OnTime scheduling must be a single non-reentrant action with deferred failure handling")
	}
	kickHandler := section("Public Sub KickQueuedAsyncDispatch", "Public Sub DispatchQueuedAsyncRequests")
	for _, required := range []string{
		"mAsyncDispatchErrorNumber <> 0",
		"DispatchQueuedAsyncRequests",
		"ScheduleQueuedAsyncDispatch",
	} {
		if !strings.Contains(kickHandler, required) {
			t.Fatalf("safe workbook-event dispatch kick is missing %q", required)
		}
	}
	if strings.Contains(kickHandler, "DispatchAsyncRequest") {
		t.Fatal("safe dispatch kick must not run a WinHTTP state transition directly")
	}
	macroFreeExport := section("Private Sub ExportMacroFreeCopy", "Private Sub RemoveMacroOnlyUI")
	for _, required := range []string{
		"Dim sourceWasSaved As Boolean",
		"Set syncSheetValue = SyncSheet()",
		"originalSyncVisibility = syncSheetValue.Visible",
		"sourceWasSaved = ThisWorkbook.Saved",
		"syncSheetValue.Visible = xlSheetVisible",
		"ThisWorkbook.Worksheets.Copy",
		"copyBook.Worksheets(syncSheetName).Visible = originalSyncVisibility",
		"If syncVisibilityChanged And Not syncSheetValue Is Nothing Then",
	} {
		if !strings.Contains(macroFreeExport, required) {
			t.Fatalf("macro-free export can omit or expose SyncData: %s", required)
		}
	}
	showSync := strings.Index(macroFreeExport, "syncSheetValue.Visible = xlSheetVisible")
	copySheets := strings.Index(macroFreeExport, "ThisWorkbook.Worksheets.Copy")
	restoreSource := strings.Index(macroFreeExport, "syncSheetValue.Visible = originalSyncVisibility")
	verifySuccessRestore := strings.Index(macroFreeExport, "If syncSheetValue.Visible <> originalSyncVisibility Then")
	restoreCopy := strings.Index(macroFreeExport, "copyBook.Worksheets(syncSheetName).Visible = originalSyncVisibility")
	if showSync < 0 || copySheets <= showSync || restoreSource <= copySheets ||
		verifySuccessRestore <= restoreSource || restoreCopy <= verifySuccessRestore {
		t.Fatal("macro-free export must expose SyncData only for the collection copy and restore both workbooks afterward")
	}
	captureSaved := strings.Index(macroFreeExport, "sourceWasSaved = ThisWorkbook.Saved")
	cleanExit := strings.Index(macroFreeExport, "CleanExit:")
	failedHandler := strings.Index(macroFreeExport, "Failed:")
	savedRestores := strings.Count(macroFreeExport, "If sourceWasSaved And Not syncVisibilityChanged Then")
	savedRestore := strings.Index(macroFreeExport, "If sourceWasSaved And Not syncVisibilityChanged Then")
	lastVisibilityRestore := strings.LastIndex(macroFreeExport, "        syncSheetValue.Visible = originalSyncVisibility")
	verifiedVisibilityRestore := strings.LastIndex(macroFreeExport, "If syncSheetValue.Visible = originalSyncVisibility Then")
	resumeCleanExit := strings.LastIndex(macroFreeExport, "Resume CleanExit")
	if captureSaved < 0 || captureSaved >= showSync || cleanExit <= restoreCopy || failedHandler <= cleanExit ||
		savedRestores != 1 || savedRestore <= cleanExit || savedRestore >= failedHandler ||
		lastVisibilityRestore <= failedHandler || verifiedVisibilityRestore <= lastVisibilityRestore ||
		resumeCleanExit <= verifiedVisibilityRestore {
		t.Fatal("macro-free export must preserve the source workbook's original Saved state on success and failure")
	}
	saveResume := section("Private Sub ResumeQualifiedSchedulesAfterSaveAs", "Public Sub RefreshAllData")
	for _, required := range []string{
		"mAsyncDispatchPending.Count > 0",
		"If refreshPending Then ScheduleRefreshOnOpen",
		"If asyncPending Then ScheduleQueuedAsyncDispatch",
		"If previewPending Then SchedulePricingPreview",
		"If eventRefreshPending Then ScheduleEventDrivenRefresh",
		"If sseReconnectPending Then ScheduleSseReconnect sseRenewSession",
	} {
		if !strings.Contains(saveResume, required) {
			t.Fatalf("Save As schedule rebinding is missing %q", required)
		}
	}
	for _, scheduler := range []struct {
		start string
		end   string
		flag  string
		reset string
	}{
		{"Public Sub ScheduleRefreshOnOpen", "Public Sub RunScheduledRefresh", "mSaveRenameRefreshPending = True", "mRefreshScheduled = False"},
		{"Private Sub ScheduleSseReconnect", "Public Sub RunSseReconnect", "mSaveRenameSseReconnectPending = True", "mSseReconnectScheduled = False"},
		{"Private Sub ScheduleEventDrivenRefresh", "Public Sub RunEventDrivenRefresh", "mSaveRenameEventRefreshPending = True", "mEventRefreshScheduled = False"},
		{"Private Sub SchedulePricingPreview", "Public Sub RunScheduledPricingPreview", "mSaveRenamePreviewPending = True", "mPricingPreviewScheduled = False"},
	} {
		schedulerSource := section(scheduler.start, scheduler.end)
		if !strings.Contains(schedulerSource, scheduler.flag) {
			t.Fatalf("qualified scheduler %s is not suspended across Save As", scheduler.start)
		}
		if !strings.Contains(schedulerSource, "ScheduleFailed:") ||
			!strings.Contains(schedulerSource, scheduler.reset) {
			t.Fatalf("qualified scheduler %s can retain a false scheduled flag after OnTime failure", scheduler.start)
		}
	}
	dispatchHandler := section("Public Sub DispatchQueuedAsyncRequests", "Private Sub CancelQueuedAsyncDispatch")
	for _, required := range []string{
		"mAsyncDispatchErrorNumber",
		"mAsyncDispatchErrorNumber = 0",
		"mAsyncDispatchErrorDescription = vbNullString",
		"DispatchAsyncRequest pendingToken",
	} {
		if !strings.Contains(dispatchHandler, required) {
			t.Fatalf("deferred async dispatcher is missing post-callback handling: %s", required)
		}
	}
	scheduleFailureHandler := section("    If mAsyncDispatchErrorNumber <> 0 Then", "    End If")
	if strings.Contains(scheduleFailureHandler, "FailActiveOperation") ||
		strings.Contains(scheduleFailureHandler, "mAsyncDispatchPending.RemoveAll") {
		t.Fatal("a transient OnTime collision must preserve and drain completed requests")
	}
	payloadHandler := section("Private Sub HandleSnapshotPayloadResponse", "Private Sub HandlePreviewResponse")
	rawHashPosition := strings.Index(payloadHandler, "SHA256RevisionBytes(responseBody)")
	validatePosition := strings.Index(payloadHandler, "ImportPricingSnapshotPayload(")
	listenerValidatePosition := strings.Index(payloadHandler, "ValidateSseListenerPrerequisites")
	commitPosition := strings.Index(payloadHandler, "CommitRefreshSnapshot reconciledRows")
	completePosition := strings.Index(payloadHandler, "CompleteActiveOperation True")
	sseArmPosition := strings.Index(payloadHandler, "ArmSseListenerAfterCommit")
	if rawHashPosition < 0 || validatePosition < rawHashPosition ||
		listenerValidatePosition < validatePosition || commitPosition < listenerValidatePosition ||
		completePosition < commitPosition || sseArmPosition < completePosition {
		t.Fatal("payload/listener prerequisites must validate before commit, and listener arm must follow successful completion")
	}
	listenerArm := section("Private Sub ArmSseListenerAfterCommit", "Private Sub AdoptSseSessionToken")
	for _, required := range []string{
		"EnsureSseListener eventsURL, jobID, csrfToken",
		"ListenerFailed:",
		"ScheduleSseReconnect True",
	} {
		if !strings.Contains(listenerArm, required) {
			t.Fatalf("post-commit listener recovery is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"FailActiveOperation",
		"CommitRefreshSnapshot",
		"RestoreCatalogTableSnapshot",
	} {
		if strings.Contains(listenerArm, forbidden) {
			t.Fatalf("post-commit listener recovery must preserve the committed snapshot: %s", forbidden)
		}
	}
	applyHandler := section("Private Sub ApplyPricingChangesCore", "Private Sub InvalidatePricingPreview")
	idempotencyPosition := strings.Index(applyHandler, `mLastApplyRequestID = NewRequestID("apply")`)
	savedIDPosition := strings.Index(applyHandler, "mOperationSavedApplyRequestID = mLastApplyRequestID")
	if idempotencyPosition < 0 || savedIDPosition < idempotencyPosition {
		t.Fatal("apply must persist its idempotency key before the asynchronous request can become uncertain")
	}
	sseEventHandler := section("Private Sub HandlePricingSseEvent", "Private Function IsExpectedApplyMutationEvent")
	for _, required := range []string{
		`Case "snapshot_ready"`,
		`Case "pricing_state_changed"`,
		`Case "pricing_state_invalidated"`,
		"eventStateRevision = SiteText(change, \"state_revision\")",
		"IsExpectedApplyMutationEvent(eventStateRevision)",
		"PreserveExpectedApplyMutationEvent",
	} {
		if !strings.Contains(sseEventHandler, required) {
			t.Fatalf("SSE apply-race handling is missing %q", required)
		}
	}
	snapshotReadyStart := strings.Index(sseEventHandler, `Case "snapshot_ready"`)
	if snapshotReadyStart < 0 {
		t.Fatal("SSE snapshot-ready handling is missing")
	}
	snapshotReadyEnd := strings.Index(sseEventHandler[snapshotReadyStart:], `Case "source_changed", "catalog_changed"`)
	if snapshotReadyEnd < 0 {
		t.Fatal("SSE snapshot-ready handling has no semantic-change boundary")
	}
	snapshotReadyHandler := sseEventHandler[snapshotReadyStart : snapshotReadyStart+snapshotReadyEnd]
	if strings.Contains(snapshotReadyHandler, "MarkSseRefreshRequired") {
		t.Fatal("snapshot readiness must not schedule another refresh without a semantic change event")
	}
	pricingChangedStart := strings.Index(sseEventHandler, `Case "pricing_state_changed"`)
	pricingInvalidatedStart := strings.Index(sseEventHandler, `Case "pricing_state_invalidated"`)
	if pricingChangedStart < 0 || pricingInvalidatedStart <= pricingChangedStart {
		t.Fatal("pricing-state event handling boundaries are missing")
	}
	pricingChangedHandler := sseEventHandler[pricingChangedStart:pricingInvalidatedStart]
	for _, required := range []string{
		"mRefreshAfterSiteConfirmation = True",
		"QueueSiteConfirmationDiscovery",
	} {
		if !strings.Contains(pricingChangedHandler, required) {
			t.Fatalf("website confirmation must precede catalog refresh: %s", required)
		}
	}
	if strings.Contains(pricingChangedHandler, "MarkSseRefreshRequired") {
		t.Fatal("website pricing events must not start a catalog refresh before Excel applies and ACKs the committed setting")
	}
	completeWriteback := section("Private Sub CompletePricingWriteback", "Private Sub ApplyWebsiteCommittedWriteback")
	for _, required := range []string{
		`completedSiteConfirmation = _`,
		"mRefreshAfterSiteConfirmation = False",
	} {
		if !strings.Contains(completeWriteback, required) {
			t.Fatalf("post-ACK pricing completion is missing %q", required)
		}
	}
	if strings.Contains(completeWriteback, "MarkSseRefreshRequired") {
		t.Fatal("post-ACK pricing completion must not rebuild the independently event-driven catalog")
	}
	expectedApplyEvent := section("Private Function IsExpectedApplyMutationEvent", "Private Sub PreserveExpectedApplyMutationEvent")
	for _, required := range []string{
		`Case "apply"`,
		`Case "apply_refresh"`,
		"mRequiredSnapshotStateRevision",
		"vbBinaryCompare",
	} {
		if !strings.Contains(expectedApplyEvent, required) {
			t.Fatalf("expected apply mutation classifier is missing %q", required)
		}
	}
	preserveApplyEvent := section("Private Sub PreserveExpectedApplyMutationEvent", "Private Sub MarkSseRefreshRequired")
	for _, required := range []string{
		"mForceFreshSnapshot = True",
		"InvalidatePricingPreview",
		`If mOperationKind = "apply" Then`,
		"mSseRefreshRequired = True",
	} {
		if !strings.Contains(preserveApplyEvent, required) {
			t.Fatalf("expected apply mutation preservation is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"mOperationRequest.Abort",
		"FailActiveOperation",
		"ScheduleEventDrivenRefresh",
	} {
		if strings.Contains(preserveApplyEvent, forbidden) {
			t.Fatalf("apply's own SSE mutation event must not terminate or bypass its HTTP response: %s", forbidden)
		}
	}
	wooLinkRowPosition := strings.Index(source, "Private Sub ApplyWooLinkRow")
	if wooLinkRowPosition < 0 {
		t.Fatal("per-row WooCommerce link helper is missing")
	}
	wooLinkRowSource := source[wooLinkRowPosition:]
	wooLinkValuePosition := strings.Index(wooLinkRowSource, "linkCell.Value2 = linkText")
	wooLinkAddPosition := strings.Index(wooLinkRowSource, "table.Parent.Hyperlinks.Add")
	if wooLinkValuePosition < 0 || wooLinkAddPosition < 0 ||
		wooLinkValuePosition > wooLinkAddPosition {
		t.Fatal("WooCommerce link text must be written before the optional hyperlink is added")
	}
	wooLinkFontAfterAddPosition := strings.Index(
		wooLinkRowSource[wooLinkAddPosition:],
		"ApplyProductNameFont linkCell",
	)
	if wooLinkFontAfterAddPosition < 0 {
		t.Fatal("WooCommerce hyperlink styling must restore all Yekan Bakh font slots")
	}

	for _, required := range []string{
		`Attribute VB_Name = "ProductCatalogSync"`,
		`Private Const SYNC_TABLE As String = "SyncData"`,
		`Private Const SYNC_COLUMN_COUNT As Long = 24`,
		`Private Const PRICING_CLIENT_HEADER As String = "X-Patris-Excel-Client"`,
		`Private Const PRICING_CSRF_HEADER As String = "X-Patris-Excel-CSRF-Token"`,
		`Private Const PRICING_SESSION_SCHEMA As String = "patris.excel-pricing-companion-session/v1"`,
		`Private Const PRICING_SNAPSHOT_REQUEST_SCHEMA As String = "patris.pricing-snapshot-request/v1"`,
		`Private Const PRICING_SNAPSHOT_JOB_SCHEMA As String = "patris.pricing-snapshot-job/v1"`,
		`Private Const PRICING_SNAPSHOT_PAYLOAD_SCHEMA As String = "patris.pricing-snapshot/v1"`,
		`Private Const PRICING_SNAPSHOT_EVENT_SCHEMA As String = "patris.pricing-state-event/v1"`,
		`Private Const PRICING_SNAPSHOT_PROJECTION As String = "excel-v1"`,
		"DigitalogicMessage.ConfigureUnicodeHex",
		"DigitalogicMessage.ValidateUnicodeCaptions",
		`Private Const DEFAULT_FANUM_FONT As String = "Yekan Bakh FaNum"`,
		"Private mOperationRequest As AsyncWinHttpRequest",
		"Private mSseRequest As AsyncWinHttpRequest",
		"Private mSseSessionRequest As AsyncWinHttpRequest",
		"Private mSseParser As PricingSseParser",
		"Private mAsyncDispatchScheduled As Boolean",
		"Private mCatalogCommitInProgress As Boolean",
		"Public Sub DispatchQueuedAsyncRequests",
		"Private Sub CancelQueuedAsyncDispatch",
		"Private Sub BeginRefreshPipeline",
		"Private Sub BeginContractRequest",
		"Private Sub BeginSessionRequest",
		"Private Sub BeginSnapshotStartRequest",
		"Private Sub BeginSnapshotWaitRequest",
		"Private Sub BeginSnapshotPayloadRequest",
		"Private Sub StartFiniteRequest",
		"Private Function IsAllowedPricingAuthenticatedUrl",
		"If pricingRequest And Not IsAllowedPricingAuthenticatedUrl(endpoint) Then",
		`requestValue.OpenAsync UCase$(methodName), endpoint`,
		`requestValue.SetRequestHeader PRICING_CLIENT_HEADER, PRICING_CLIENT_ID`,
		`requestValue.SetRequestHeader PRICING_CSRF_HEADER`,
		`requestValue.SetRequestHeader "Idempotency-Key", idempotencyKey`,
		`requestValue.SetRequestHeader "If-Match", _`,
		`Chr$(34) & expectedRevision & Chr$(34)`,
		"Private Sub HandleOperationTerminal",
		`Case "snapshot_start"`,
		`Case "snapshot_wait"`,
		`Case "snapshot_payload"`,
		`waitURL <> expectedJobURL & "?wait=terminal"`,
		`eventsURL <> PricingBaseURL() & "/events"`,
		`jobEventsURL <> expectedJobURL`,
		`SiteText(root, "events_lifecycle") <> _`,
		`"session_scoped_durable"`,
		`SiteText(root, "job_events_lifecycle") <> _`,
		`"job_scoped_progress"`,
		`RequiredWholeNumber(root, "events_keepalive_seconds") <> 15`,
		`RequiredWholeNumber(root, "events_history_capacity") <> 256`,
		"Private Sub EnsureSseListener",
		"Private Sub BeginSseSessionRenewal",
		"Private Sub HandleSseSessionDispatch",
		`requestValue.SetRequestHeader "Accept", "text/event-stream"`,
		`requestValue.SetRequestHeader "Last-Event-ID", mSseLastEventID`,
		"Private Sub HandlePricingSseEvent",
		`Case "cursor_expired", "cursor_ahead", _`,
		`"initial_history_expired", "initial_state_unavailable"`,
		"mSseLastEventID = vbNullString",
		"Private Function RawUnsignedDecimalMember",
		`"18446744073709551615"`,
		"Private Sub ScheduleSseReconnect",
		"Public Sub RunSseReconnect",
		"Private Sub ScheduleEventDrivenRefresh",
		"Public Sub RunEventDrivenRefresh",
		"Set activeBook = Application.ActiveWorkbook",
		"If Not activeBook Is ThisWorkbook Then Exit Sub",
		"Public Sub CancelActivePricingOperations",
		"Public Sub ResumeAfterCancelledClose",
		"mResumeRefreshAfterCancelledClose",
		"If workbookIsClosing And Not mCancelRequest Is Nothing Then",
		"mCancelRequest.Abort",
		"If workbookIsClosing Then CancelSseReconnect",
		"If workbookIsClosing Then StopSseListener True",
		"Private Sub CommitRefreshSnapshot",
		"Private Sub CaptureCatalogTableSnapshot",
		"Private Sub RestoreCatalogTableSnapshot",
		"Private Sub RestoreTableFormulaSnapshot",
		"If catalogSnapshotCaptured And catalogMutationStarted Then",
		"ApplyGlobalState state",
		"ApplyReconciliationCounts catalog",
		"productRows = ImportReconciledCatalog(reconciledRows)",
		"Private Function SHA256RevisionBytes",
		"rawDigest = SHA256RevisionBytes(responseBody)",
		`StrComp(Trim$(responseETag), Trim$(mSnapshotExpectedETag)`,
		"Private Function ImportPricingSnapshotPayload",
		"rawStateDigest = SHA256Revision(rawStateText)",
		`If rawStateDigest <> SiteText(integrity, "state_digest") Then _`,
		"Private Function SnapshotRowFieldsMatch",
		`If rowValue.Kind <> "array" Then GoTo InvalidSnapshot`,
		`Case "matched", "patris_only", "woo_only"`,
		`matchedCount + patrisOnlyCount + wooOnlyCount <> unionCount`,
		`ambiguousCount <> 0 Or _`,
		"RejectProjectionIntegrityWarnings state",
		`Left$(warningCode, Len("product_type_cache_drift"))`,
		`Left$(warningCode, Len("projection_integrity"))`,
		"Private Sub BeginRepairRequest",
		`StartFiniteRequest "POST", UniversalRefreshURL()`,
		"Private Function RetrySnapshotAfterSourceDrift() As Boolean",
		"Private Function RetrySnapshotAfterTransientFailure(",
		"If mOperationSnapshotRetryCount >= 3 Or _",
		`errorCode = "canonical_source_mismatch" And _`,
		`"snapshot_source_revision_conflict" And _`,
		"RetrySnapshotAfterSourceDrift() Then",
		`If RetrySnapshotAfterTransientFailure(errorCode) Then Exit Sub`,
		`If RetrySnapshotAfterTransientFailure(mSnapshotJobCode) Then Exit Sub`,
		`Case "remote_unavailable", "canonical_source_unavailable"`,
		`BeginContractRequest "contract"`,
		`(StrComp(Trim$(address), UniversalRefreshURL(), vbTextCompare) = 0)`,
		`JsonRuntime.JsonText(root, "source_revision")`,
		"mSourceRevision <> mOperationDeliveredRevision",
		"Public Sub PreviewPricingChanges()",
		"Private Sub PreviewPricingChangesCore(ByVal showMessage As Boolean)",
		"Public Sub ApplyPricingChanges()",
		"Private Sub ApplyPricingChangesCore",
		"Public Sub HandlePricingProposalChanged()",
		"Public Sub QueuePricingInputWriteback(ByVal Target As Range)",
		"Private mWritebackRequest As AsyncWinHttpRequest",
		"Private Sub HandleWritebackTerminal",
		"Private Function BuildPricingWritebackRequest",
		`Private Const PRICING_WRITEBACK_REQUEST_SCHEMA As String = "patris.pricing-input-writeback-request/v1"`,
		`Case "yuan_price", "site_confirmation": addressText = "B18"`,
		`Private Const PRICING_CONFIRMATION_REQUEST_SCHEMA As String = "patris.pricing-confirmation-ack-request/v1"`,
		`Case "awaiting_excel"`,
		`"/writebacks/" & mWritebackJobID & "/ack"`,
		"Private Sub ApplyWebsiteCommittedWriteback",
		"Private Function BuildPricingConfirmationRequest",
		"ConfirmedCNYRate",
		`Case "dollar_price": addressText = "B19"`,
		`Case "profit_margin_percent": addressText = "B21"`,
		`Case "air_express_price_per_kg": addressText = "B22"`,
		"Public Function ValidatePricingWritebackUIForValidation() As Boolean",
		"Public Function ValidateOperationProgressUIForValidation() As Boolean",
		"Public Function ValidateProductImagePreviewUIForValidation() As Boolean",
		"Public Sub HandleProductImageSettingChanged()",
		"Private Sub RefreshProductImagePreview(ByVal relativeRow As Long)",
		"Private Sub HandleProductImageTerminal()",
		"Private Sub CancelProductImagePreview(ByVal clearPreview As Boolean)",
		"Private Function CachedProductImagePath(ByVal imageURL As String) As String",
		"Private Const PRODUCT_IMAGE_CACHE_LIMIT As Long = 16",
		"Private Const PRODUCT_IMAGE_MAX_BYTES As Long = 2097152",
		`requestValue.OpenAsync "GET", imageURL`,
		`requestValue.SetRequestHeader "Accept", _`,
		`If requestGeneration <> mImageGeneration Or _`,
		`requestRow <> SelectedProductRelativeRow() Or _`,
		`LCase$(Left$(imageURL, 8)) <> "https://"`,
		`InStr(1, authority, "@", vbBinaryCompare) > 0`,
		`Shapes.AddPicture(`,
		"Private Function OperationProgressStageMatchesForValidation(",
		`Case "pending", "sending": fillColor = RGB(252, 228, 178)`,
		`Case "confirmed": fillColor = RGB(226, 239, 218)`,
		`Case "failed": fillColor = RGB(244, 204, 204)`,
		`Case "warning": fillColor = RGB(255, 242, 204)`,
		"Private Sub MarkRefreshPricingConvergenceState()",
		`If completedKind = "refresh" Then MarkRefreshPricingConvergenceState`,
		`MarkWritebackState "site_confirmation", "confirmed", noteText`,
		`MarkWritebackState "site_confirmation", "warning", _`,
		`CStr(settings.Range("G14").Value2)`,
		`CStr(settings.Range("G56").Value2)`,
		"mPricingPreviewQueued = True",
		"Private Sub SchedulePricingPreview",
		"Public Sub RunScheduledPricingPreview",
		"PreviewPricingChangesCore False",
		"If answer <> vbYes Then Exit Sub",
		"mLastPreviewWarningCount = ValidatedWarningCount(result)",
		`JsonRuntime.JsonText(result, "preview_digest")`,
		"mRequiredSnapshotStateRevision = appliedStateRevision",
		`body = body & ",""preview_digest"":" & JsonString(previewDigest)`,
		`body = body & ",""confirmation"":""APPLY"""`,
		`"""idempotency_key"":" & JsonString(requestID)`,
		`"""product_changes"":[]`,
		`"""expected_state_revision"":"`,
		`"""client_id"":" & JsonString(PRICING_CONTRACT_CLIENT_ID)`,
		`"""channel"":" & JsonString(PRICING_CONTRACT_CHANNEL)`,
		`"""request_id"":" & JsonString(requestID)`,
		`"""usd_effective_date"":" & JsonString(usdEffectiveDate)`,
		`"""cny_effective_date"":" & JsonString(cnyEffectiveDate)`,
		`"""price_rounding_mode"":" & JsonString(PRICE_ROUNDING_MODE)`,
		"Public Function RefreshAllDataForValidation() As Boolean",
		"Public Function AsyncPricingIdleForValidation() As Boolean",
		"Public Function LastPricingOperationSucceededForValidation() As Boolean",
		"ValidateAsyncComponentsRuntime",
		"ValidateProjectionIntegrityGuard",
		"ProjectionIntegrityFixtureRejected",
		"Private Function SafeStatusError",
		`InStr(lowered, "credential")`,
		`SafeStatusError = T("sync_retry")`,
		"Public Function AuditMessageDialogForValidation",
		"Public Function ValidateFontPolicyFixturesForValidation",
		"Private Function IsAllowedPricingBridgeUrl",
		"Public Sub ScheduleRefreshOnOpen",
		"Public Sub CancelScheduledRefresh",
		"Private Sub CalculateRefreshedWorkbook",
		"Private Function SyncSheet() As Worksheet",
		"Private Function IsAllowedDigitalogicUrl",
		`"https://digitalogic.ir/"`,
		"ApplyWooLinkRow table, syncTable, rowIndex",
		"Private Function NormalizeHumanProductName",
		"Private Function FormatNonzeroStatusSummary",
		"Private Function AuditFixedFontMap",
		"EnforceConfiguredFontsAfterRefresh",
		"Public Function AuditFontsForValidation",
		"Private Function ReadSearchLiteral",
		`MergeArea.NumberFormat = "@"`,
		"Private Sub EnsureProductColumnWidths",
		"Public Function RepairFontDriftForValidation() As Boolean",
		"Public Function AuditFontsOnOpen() As Boolean",
		"Public Sub SearchProducts()",
		"Public Sub ClearProductSearch()",
		"Public Sub RefreshSearchEnterHotkey()",
		"Public Function UpdateSearchEnterHotkey(ByVal target As Range) As Boolean",
		"Public Sub ReleaseSearchEnterHotkey()",
		"Public Sub HandleProductSearchEnter()",
		"Private Function ProductSearchMatchRows",
		"Private Function ProductRowMatchesQuery",
		"Private Function NextProductSearchMatchIndex",
		`Application.OnKey "{ESC}",`,
		`Private Const SEARCH_BUTTON_SHAPE As String = "ProductSearchButton"`,
		"Public Sub HighlightSelectedProductRow",
		"Public Sub ApplyPriceDisplayFontSetting()",
		"Private Function PriceDisplayFontName",
		`NamedYesNo("PriceDisplayFaNum", valid)`,
		"table.ListColumns(1).DataBodyRange, priceDisplayFont, repair",
		"For Each columnIndex In Array(2, 5, 6, 7, 9)",
		`book.Worksheets(3).Range("A44:F44").ClearContents`,
		`book.Names("PriceDisplayFaNum").Delete`,
		"ValidateRoundingRuntime",
		"Application.WorksheetFunction.Round(123450#, -2) <> 123500#",
		"Public Sub HandleWorkbookBeforeSave",
		"Private Sub ExportMacroFreeCopy",
		"Private Sub RemoveMacroOnlyUI",
		"FileFormat:=xlOpenXMLWorkbook",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("VBA source is missing validation: %s", required)
		}
	}

	for key, wantMinimum := range map[string]int{
		`Application.OnKey "{F3}"`:  2,
		`Application.OnKey "{ESC}"`: 2,
	} {
		if count := strings.Count(source, key); count < wantMinimum {
			t.Fatalf("VBA source contains %d occurrences of %q, want at least %d for register/release", count, key, wantMinimum)
		}
	}
	if strings.Contains(source, `Application.OnKey "~",`) {
		t.Fatal("Enter must remain native; rebinding it can recurse through selection events and exhaust Excel's VBA stack")
	}
	for _, required := range []string{
		"Private Const SNAPSHOT_WAIT_TIMEOUT_MS As Long = 120000",
		"Private Const MAX_REFRESH_WALL_SECONDS As Double = 125#",
		"Private Const SEARCH_DELAY_SECONDS As Double = 0.55",
		"Private Sub RestoreExcelInteractivityAfterOperation()",
		"Application.EnableEvents = True",
		"Public Sub StartPricingEventListenerOnOpen()",
		"Public Function PricingEventListenerActiveForValidation() As Boolean",
		`If mWritebackSettingKey = "site_confirmation" Then`,
		`confirmedValue = CanonicalCellText(settings.Range("G18").Value2)`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Excel refresh cleanup is missing %q", required)
		}
	}
	if strings.Count(source, "RestoreExcelInteractivityAfterOperation") < 3 {
		t.Fatal("both successful and failed finite operations must restore Excel interactivity")
	}
	progressSurface := section("Public Sub SetOperationProgressSurface", "Private Sub UpdateOperationProgressShapes")
	if strings.Contains(progressSurface, `U("064606270645063906CC0646")`) {
		t.Fatal("indeterminate progress must not expose the internal Persian label نامعین")
	}
	if strings.Count(progressSurface, "displayText = messageText") < 2 {
		t.Fatal("neutral and indeterminate progress must display the useful stage message without an internal-state prefix")
	}
	progressValidation := section("Public Function ValidateOperationProgressUIForValidation", "Private Function WritebackCell")
	for _, required := range []string{
		`SetRefreshProgress "1/4"`,
		`SetRefreshProgress "2/4"`,
		`SetSnapshotProgress 0, 0, 0`,
		`SetRefreshProgress "3/4"`,
		`SetRefreshProgress "4/4"`,
		`"refresh_request", 10`,
		`"refresh_wait", -1`,
		`"refresh_download", -1`,
		`"refresh_validate", 70`,
		`"refresh_apply", 90`,
		`"completed", 100`,
		"CStr(Application.StatusBar)",
		"label.TextFrame2.TextRange.Text",
	} {
		if !strings.Contains(progressValidation, required) {
			t.Fatalf("progress UI validator does not cover panel and StatusBar lifecycle: %s", required)
		}
	}
	for _, forbidden := range []string{
		`Worksheets("`,
		"table.ListRows.Add",
		"Environ$(",
		`setRequestHeader "Authorization"`,
		"Bearer ",
		"client_secret",
		"consumer_key",
		"api_key",
		"password",
		"wp-json/wc/store",
		"digitalogic.excel-pricing-sync-request/v1",
		"default_profit_percent",
		`linkText = linkText & "WooID "`,
		"linkText = wooID",
		"X-Patris-Product-Sync-Secret",
		"PATRIS_PRODUCT_SYNC_SECRET",
		"MsgBox ",
		"MsgBox(",
		"VBA.MsgBox",
		`"catalog_materialization"`,
		"ApplyCatalogMaterializationState",
		`http.Open "GET", endpoint, False`,
		`http.Open "POST", endpoint, False`,
		"Application.CalculateFull",
		"Application.Interactive",
		"ShowUnicodeMessage",
		"DigitalogicMessage.Show",
		"DigitalogicMessage.DialogResult",
		"readyState",
		"DoEvents",
		"Sleep ",
		"MSXML2.ServerXMLHTTP",
		"SnapshotHttpRequest",
		"WaitForHttpResponse",
		"RefreshPricingStatePaged",
		"RefreshPricingStatePreferred",
		"StateRequestJson",
	} {
		if strings.Contains(strings.ToLower(source), strings.ToLower(forbidden)) {
			t.Fatalf("VBA source contains forbidden legacy or credential path: %s", forbidden)
		}
	}

	highlightStart := strings.Index(source, "Public Sub HighlightSelectedProductRow")
	proposalChangeStart := strings.Index(source, "Public Sub HandlePricingProposalChanged")
	previewStart := strings.Index(source, "Public Sub PreviewPricingChanges")
	if highlightStart < 0 || proposalChangeStart <= highlightStart || previewStart <= proposalChangeStart {
		t.Fatal("selection or proposal-change handlers are missing")
	}
	highlightSource := source[highlightStart:proposalChangeStart]
	if strings.Contains(highlightSource, "table.DataBodyRange.Calculate") ||
		strings.Count(highlightSource, "priceCell.Calculate") < 2 {
		t.Fatal("selection must calculate only the previous/current price cells")
	}
	proposalChangeSource := source[proposalChangeStart:previewStart]
	if strings.Contains(proposalChangeSource, "PreviewPricingChangesCore") ||
		strings.Contains(proposalChangeSource, "ApplyPricingChangesCore") {
		t.Fatal("editing a proposal must not start network preview/apply automatically")
	}

	refreshSource := section("Public Sub RefreshAllData", "Private Sub BeginRefreshPipeline")
	if !strings.Contains(refreshSource, "BeginRefreshPipeline silent, False") {
		t.Fatal("RefreshAllData must only start the callback-driven refresh pipeline")
	}
	beginRefreshSource := section("Private Sub BeginRefreshPipeline", "Private Sub CommitRefreshSnapshot")
	for _, forbidden := range []string{
		"Application.ScreenUpdating = False",
		"Application.EnableEvents = False",
		"Application.Calculation = xlCalculationManual",
	} {
		if strings.Contains(beginRefreshSource, forbidden) {
			t.Fatalf("network setup freezes Excel before atomic commit: %s", forbidden)
		}
	}
	if !strings.Contains(beginRefreshSource, `BeginContractRequest "contract"`) {
		t.Fatal("refresh pipeline must begin with the asynchronous product contract")
	}
	commitSource := section("Private Sub CommitRefreshSnapshot", "Public Sub RefreshOnOpen")
	for _, required := range []string{
		"mCatalogCommitInProgress = True",
		"mCatalogCommitInProgress = False",
		"Application.ScreenUpdating = False",
		"Application.EnableEvents = False",
		"Application.Calculation = xlCalculationManual",
		"ReleaseSearchEnterHotkey",
		`settings.Range("B6").Value = statusText`,
	} {
		if !strings.Contains(commitSource, required) {
			t.Fatalf("atomic refresh commit is missing %s", required)
		}
	}
	for _, required := range []string{
		"PRICING_REVISION_SCHEMA As String",
		"Private Sub BeginRevisionProbeRequest()",
		`PricingBaseURL() & "/revision"`,
		`Case "revision"`,
		"Private Sub HandleRevisionResponse",
		"Private Function TryCompleteUnchangedRevision",
		`settings.Range("G56").Value2`,
		`SiteText(root, "pricing_state_revision")`,
		`mOperationKind <> "refresh" Or mForceFreshSnapshot`,
		"HasCoherentLocalCatalog()",
		"StrictPriceParityMismatchCount() <> 0",
		`mStatePageTimingText = "revision_noop="`,
		"CompleteActiveOperation True",
		"mSnapshotDatasetRevision As String",
		"mSnapshotPricingStateRevision As String",
		`datasetRevision = SiteText(identity, "catalog_revision")`,
		"Private Function TryCompleteUnchangedSnapshot() As Boolean",
		`mOperationKind <> "refresh" Or mForceFreshSnapshot`,
		`settings.Range("G44").Value2`,
		`settings.Range("G14").Value2`,
		`settings.Range("G56").Value2`,
		`settings.Range("G45").Value2`,
		`settings.Range("G46").Value2`,
		"HasCoherentLocalCatalog()",
		"StrictPriceParityMismatchCount() <> 0",
		`settings.Range("B49").Value2 = mStatePageTimingText`,
		"CompleteActiveOperation True",
		"ArmSseListenerAfterCommit committedEventsURL",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("unchanged-revision fast path is missing %s", required)
		}
	}
	if strings.Contains(section("Private Function TryCompleteUnchangedSnapshot", "Private Function RetrySnapshotAfterSourceDrift"), "BeginSnapshotPayloadRequest") {
		t.Fatal("unchanged-revision fast path must finish before downloading the payload")
	}
	revisionFastPath := section("Private Function TryCompleteUnchangedRevision", "Private Sub HandleSnapshotStartResponse")
	if strings.Contains(revisionFastPath, `settings.Range("G44").Value2`) {
		t.Fatal("revision fast path must not compare the upstream composite catalog revision with G44's dataset revision")
	}
	for _, required := range []string{
		`StrongETagRevision(revisionETag) <> stateRevision`,
		`settings.Range("G56").Value2`,
		`settings.Range("G14").Value2`,
		`settings.Range("G45").Value2`,
		`settings.Range("G46").Value2`,
		"HasCoherentLocalCatalog()",
		"StrictPriceParityMismatchCount() <> 0",
	} {
		if !strings.Contains(revisionFastPath, required) {
			t.Fatalf("revision fast path lost fail-closed guard %s", required)
		}
	}
	transientRetry := section("Private Function RetrySnapshotAfterTransientFailure", "Private Function RefreshWallBudgetExhausted")
	for _, required := range []string{
		`Case "remote_unavailable", "canonical_source_unavailable"`,
		"mOperationSnapshotRetryCount >= 3",
		"RefreshWallBudgetExhausted()",
		`BeginContractRequest "contract"`,
	} {
		if !strings.Contains(transientRetry, required) {
			t.Fatalf("transient snapshot retry is missing %s", required)
		}
	}
	if strings.Contains(transientRetry, "remote_not_configured") ||
		strings.Contains(transientRetry, "snapshot_integrity_failed") {
		t.Fatal("configuration and integrity failures must remain fail-closed")
	}
	if !strings.Contains(section("Private Sub HandleSessionResponse", "Private Sub HandleRevisionResponse"), "BeginRevisionProbeRequest") {
		t.Fatal("ordinary refresh must probe the verified revision before starting a snapshot")
	}
	if strings.Contains(refreshSource+beginRefreshSource, "ConfirmUnicodeMessage") {
		t.Fatal("routine refresh status must remain inline and never open a modal dialog")
	}
	for _, uiSection := range []struct {
		start string
		end   string
	}{
		{"Public Sub FocusProductSearch", "Public Sub QueueProductSearch"},
		{"Public Sub SearchProducts", "Public Sub ClearProductSearch"},
		{"Public Sub ClearProductSearch", "Private Function ProductSearchMatchRows"},
		{"Public Sub HighlightSelectedProductRow", "Public Sub HandlePricingProposalChanged"},
	} {
		sectionSource := section(uiSection.start, uiSection.end)
		if strings.Contains(sectionSource, "mRefreshInProgress") ||
			(!strings.Contains(sectionSource, "mCatalogCommitInProgress") &&
				!strings.Contains(sectionSource, "SearchOperationBusy()")) {
			t.Fatalf("%s must remain usable throughout the network wait and pause only for atomic commit", uiSection.start)
		}
	}
	for _, required := range []string{
		"mOperationPreviousEnableCancelKey = Application.EnableCancelKey",
		"Application.EnableCancelKey = xlErrorHandler",
		"Application.EnableCancelKey = mOperationPreviousEnableCancelKey",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("callback refresh is missing cancellable inline handling: %s", required)
		}
	}

	searchStart := strings.Index(source, "Public Sub SearchProducts")
	clearSearchStart := strings.Index(source, "Public Sub ClearProductSearch")
	if searchStart < 0 || clearSearchStart <= searchStart {
		t.Fatal("product search handlers are missing")
	}
	searchSource := source[searchStart:clearSearchStart]
	if strings.Contains(searchSource, "ConfirmUnicodeMessage") {
		t.Fatal("routine no-result search status must remain inline and never open a modal dialog")
	}
	for _, required := range []string{
		"If SearchOperationBusy() Then",
		"requestedQuery As String",
		"requestGeneration As Long",
		"mSearchInProgress = True",
		"Application.EnableCancelKey = xlErrorHandler",
		"RememberSearchSelection anchor",
		`SetSearchButtonCaption T("search_button") & " (0)"`,
		"mSearchInProgress = False",
	} {
		if !strings.Contains(searchSource, required) {
			t.Fatalf("product search is missing its non-modal reentrancy guard: %s", required)
		}
	}
	if !strings.Contains(source, "SearchProductsForQuery queuedQuery, queuedGeneration") {
		t.Fatal("scheduled physical-Enter search must consume the captured query generation")
	}
	if !strings.Contains(source, "mScheduledSearchTime = Now + (SEARCH_DELAY_SECONDS / 86400#)") {
		t.Fatal("native Enter search must use a reliable sub-second top-level callback")
	}
	for _, forbidden := range []string{
		"Application.Goto", "DoEvents", "PumpExcelMessages",
		"KickQueuedAsyncDispatch", `Application.OnKey "~",`,
	} {
		if strings.Contains(searchSource, forbidden) {
			t.Fatalf("product search reintroduced synchronous event/dispatch recursion: %s", forbidden)
		}
	}
	for _, required := range []string{
		"IsNativeSearchEnterTransition(target)",
		"RememberSearchSelection target",
		"If queueNativeEnter Then",
		"UpdateSearchEnterHotkey = True",
		`Set searchInput = ThisWorkbook.Names("ProductSearchQuery"). _`,
		"If Not Intersect(target.Cells(1, 1), searchInput) Is Nothing Then _",
		"HighlightSelectedProductRow anchor, False",
		"Optional ByVal allowPreviewAndPump As Boolean = True",
		"Private Sub RunNativeEnterProductSearch()",
		"CancelScheduledProductSearch",
		"SearchProductsForQuery query, generation",
		"mPendingSearchGeneration = mSearchRequestGeneration",
		"NormalizeProductSearchText",
		"EnsureProductSearchIndex table",
		"table.DataBodyRange.Columns(7).Value2",
		"table.DataBodyRange.Columns(8).Value2",
		"table.DataBodyRange.Columns(9).Value2",
		"mSearchIndexProductCodes(rowIndex) = normalizedQuery",
		"mSearchIndexWooIDs(rowIndex) = normalizedQuery",
		"filteredView = table.Parent.FilterMode",
		"VisibleProductRowMap(table)",
		"vbBinaryCompare",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("native Enter or semantic search gate is missing: %s", required)
		}
	}
	if strings.Contains(source, "table.DataBodyRange.Rows(rowIndex).EntireRow.Hidden") {
		t.Fatal("local search must not make one Excel COM hidden-row call per product")
	}
	for _, required := range []string{
		"Public Sub PrepareLocalSearchOnOpen()",
		"Private Sub WarmProductSearchIndex()",
		"Private Sub InvalidateProductSearchIndex()",
		"If mSearchIndexReady And mSearchIndexRowCount = rowCount",
		"WarmProductSearchIndex",
		"InvalidateProductSearchIndex",
		"ClearQueuedPricingIntent settingKey",
		"mWritebackPendingValues.Remove settingKey",
		"mWritebackPendingGenerations.Remove settingKey",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("fast local search index lifecycle is missing: %s", required)
		}
	}

	writebackStart := strings.Index(source, "Public Sub RunScheduledPricingWriteback")
	writebackEnd := strings.Index(source, "Private Sub RunSynchronousWritebackStep")
	if writebackStart < 0 || writebackEnd <= writebackStart {
		t.Fatal("scheduled pricing writeback section is missing")
	}
	writebackSource := source[writebackStart:writebackEnd]
	if !strings.Contains(writebackSource, "RunSynchronousWritebackStep") {
		t.Fatal("scheduled pricing writeback must use the bounded loopback-only synchronous step")
	}
	for _, forbidden := range []string{"BeginWritebackPoll", "StartWritebackRequest"} {
		if strings.Contains(writebackSource, forbidden) {
			t.Fatalf("scheduled pricing writeback must not re-enter the stale asynchronous event path: %s", forbidden)
		}
	}
	postDequeueStart := strings.Index(writebackSource, `mWritebackRequestID = NewRequestID("writeback")`)
	if postDequeueStart < 0 {
		t.Fatal("scheduled writeback is missing its post-dequeue request boundary")
	}
	postDequeueSource := writebackSource[postDequeueStart:]
	for _, forbidden := range []string{
		"mActiveWritebackGeneration = 0",
		"mActiveWritebackDesiredValue = vbNullString",
	} {
		if strings.Contains(postDequeueSource, forbidden) {
			t.Fatalf("dequeue must preserve immutable writeback intent through send/ACK: %s", forbidden)
		}
	}
	terminalWriteback := section("Private Sub CompletePricingWriteback", "Private Sub ApplyWebsiteCommittedWriteback")
	for _, required := range []string{
		"mActiveWritebackGeneration = 0",
		"mActiveWritebackDesiredValue = vbNullString",
	} {
		if !strings.Contains(terminalWriteback, required) {
			t.Fatalf("terminal completion must clear immutable writeback intent: %s", required)
		}
	}
	restorePricing := section("Private Sub RestorePricingStateSnapshot", "Private Function PricingSettingsCanonical")
	for _, required := range []string{
		"previousEvents = Application.EnableEvents",
		"previousInternalRefresh = mInternalPricingRefresh",
		"Application.EnableEvents = False",
		"mInternalPricingRefresh = True",
		"mInternalPricingRefresh = previousInternalRefresh",
		"Application.EnableEvents = previousEvents",
	} {
		if !strings.Contains(restorePricing, required) {
			t.Fatalf("refresh rollback must not enqueue a server-originated pricing write: %s", required)
		}
	}
	for _, required := range []string{
		"If HasCoherentLocalCatalog() Then",
		`SetOperationProgressSurface "listener_wait", -1`,
		"Not RefreshWallBudgetExhausted()",
		"Private Function RefreshWallBudgetExhausted() As Boolean",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("event-first cold open or bounded snapshot fallback is missing: %s", required)
		}
	}
	for _, required := range []string{
		"QueueSiteConfirmationDiscovery",
		`mWritebackPending("site_confirmation") = "B18"`,
		`requestBody = "{}"`,
		`Case "yuan_price", "site_confirmation"`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("website-originated pricing confirmation is missing its fast Excel apply/ACK path: %s", required)
		}
	}
	confirmStart := strings.Index(source, "Private Function ConfirmUnicodeMessage")
	if confirmStart < 0 {
		t.Fatal("native Unicode confirmation helper is missing")
	}
	confirmationCallSites := source[:confirmStart]
	if count := strings.Count(confirmationCallSites, "ConfirmUnicodeMessage"); count != 2 {
		t.Fatalf("VBA contains %d modal confirmation call sites, want only overwrite and apply", count)
	}
	for _, required := range []string{
		`T("save_overwrite"), T("save_title")`,
		`T("apply_title"))`,
	} {
		if !strings.Contains(confirmationCallSites, required) {
			t.Fatalf("VBA is missing required explicit confirmation: %s", required)
		}
	}

	thisWorkbookPath := filepath.Join("..", "..", "docs", "examples", "vba", "ThisWorkbook.cls")
	thisWorkbookContent, err := os.ReadFile(thisWorkbookPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"Private Sub Workbook_BeforeSave",
		"ProductCatalogSync.HandleWorkbookBeforeSave SaveAsUI, Cancel",
		"Private Sub Workbook_AfterSave",
		"ProductCatalogSync.FinishWorkbookSaveTiming Success",
		"If Not Success Then ProductCatalogSync.ResumeAfterCancelledClose",
		"Private Sub Workbook_WindowActivate",
		"ProductCatalogSync.ResumeAfterCancelledClose",
		"ProductCatalogSync.KickQueuedAsyncDispatch",
		"If Success Then ProductCatalogSync.RegisterSearchHotkey",
		"Private Sub Workbook_SheetChange",
		`If Not Intersect(Target, Sh.Range("B31")) Is Nothing Then`,
		"ProductCatalogSync.HandleProductImageSettingChanged",
		`If Not Intersect(Target, Sh.Range("B44")) Is Nothing Then`,
		"ProductCatalogSync.ApplyPriceDisplayFontSetting",
		`Union(Sh.Range("B18:B22"), _`,
		`Sh.Range("B26"))`,
		"ProductCatalogSync.QueuePricingInputWriteback Target",
		"previousEvents = Application.EnableEvents",
		"Application.EnableEvents = previousEvents",
		`Sh.Range("E20"), _`,
		"Private Sub Workbook_SheetSelectionChange",
		"If ProductCatalogSync.UpdateSearchEnterHotkey(Target) Then _",
		"ProductCatalogSync.HighlightSelectedProductRow Target",
		"Call ProductCatalogSync.AuditFontsOnOpen",
		"ProductCatalogSync.PreserveSearchLiteral",
		"ProductCatalogSync.PrepareLocalSearchOnOpen",
		"ProductCatalogSync.StartPricingEventListenerOnOpen",
		"ProductCatalogSync.ScheduleRefreshOnOpen",
		"ProductCatalogSync.CancelActivePricingOperations True",
		"ProductCatalogSync.CancelScheduledRefresh",
	} {
		if !strings.Contains(string(thisWorkbookContent), required) {
			t.Fatalf("ThisWorkbook source is missing pricing-change guard: %s", required)
		}
	}
	if strings.Contains(string(thisWorkbookContent), "Application.EnableEvents = True") {
		t.Fatal("ThisWorkbook event handlers must restore the caller's prior event state")
	}
	if strings.Contains(string(thisWorkbookContent), "If Not ProductCatalogSync.AuditFontsOnOpen Then Exit Sub") {
		t.Fatal("font audit failure must not suppress scheduled population")
	}
	if strings.Contains(string(thisWorkbookContent), "Application.CalculateFull") ||
		strings.Contains(string(thisWorkbookContent), "ProductCatalogSync.RefreshOnOpen") {
		t.Fatal("Workbook_Open must paint the UI before scheduling live synchronization")
	}

	jsonPath := filepath.Join("..", "..", "docs", "examples", "vba", "JsonRuntime.bas")
	jsonContent, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"Private Function ParseObject", "Private Function ParseArray", "Duplicate JSON object member"} {
		if !strings.Contains(string(jsonContent), required) {
			t.Fatalf("JSON runtime is missing validation: %s", required)
		}
	}
}

func TestDynamicCalculatorVBAUsesWinHTTPEventsAndDurableSSE(t *testing.T) {
	asyncPath := filepath.Join("..", "..", "docs", "examples", "vba", "AsyncWinHttpRequest.cls")
	asyncContent, err := os.ReadFile(asyncPath)
	if err != nil {
		t.Fatal(err)
	}
	asyncSource := string(asyncContent)
	for _, required := range []string{
		`Attribute VB_Name = "AsyncWinHttpRequest"`,
		"Private WithEvents mHttp As WinHttp.WinHttpRequest",
		"Set mHttp = New WinHttp.WinHttpRequest",
		"Private Sub mHttp_OnResponseStart",
		"Private Sub mHttp_OnResponseDataAvailable(Data() As Byte)",
		"Private Sub mHttp_OnResponseFinished()",
		"Private Sub mHttp_OnError",
		"mResponseBody = bodyValue",
		"ProductCatalogSync.QueueAsyncDispatch mToken",
	} {
		if !strings.Contains(asyncSource, required) {
			t.Fatalf("WinHTTP event class is missing %s", required)
		}
	}
	for _, forbidden := range []string{
		"readyState", "DoEvents", "Sleep ", "WaitForResponse",
		"MSXML2.ServerXMLHTTP", `CreateObject("WinHttp`,
	} {
		if strings.Contains(strings.ToLower(asyncSource), strings.ToLower(forbidden)) {
			t.Fatalf("WinHTTP event class contains polling or late binding: %s", forbidden)
		}
	}

	parserPath := filepath.Join("..", "..", "docs", "examples", "vba", "PricingSseParser.cls")
	parserContent, err := os.ReadFile(parserPath)
	if err != nil {
		t.Fatal(err)
	}
	parserSource := string(parserContent)
	for _, required := range []string{
		`Attribute VB_Name = "PricingSseParser"`,
		"Public Function Feed(ByVal Chunk As Variant) As Collection",
		"Private Const MAX_BUFFER_BYTES As Long = 1048576",
		"Private Const MAX_EVENT_BYTES As Long = 262144",
		"MB_ERR_INVALID_CHARS",
		`Case "data"`,
		`Case "event"`,
		`Case "id"`,
		`Case "retry"`,
		`If Left$(currentLine, 1) <> ":" Then`,
	} {
		if !strings.Contains(parserSource, required) {
			t.Fatalf("incremental SSE parser is missing %s", required)
		}
	}
}

func TestDynamicCalculatorValidatorHandlesEmptyProductTable(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "windows", "Validate-ExcelPriceCalculator.cjs")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Git may materialize this CJS file with CRLF on Windows runners. Normalize
	// before order-sensitive source checks so checkout policy cannot change the
	// safety verdict.
	source := strings.ReplaceAll(string(content), "\r\n", "\n")
	for _, required := range []string{
		`$priceDataRange = $table.ListColumns.Item(1).DataBodyRange`,
		`if ($null -ne $priceDataRange) {`,
		`if ($null -eq $titleColumn) {`,
		`priceSlots = $priceSlots`,
		`technicalSlots = $technicalSlots`,
		`report.fontAudit.priceDisplayFaNum === 'بله'`,
		`report.fontAudit.faNumToggle.enabled === true`,
		`report.fontAudit.faNumToggle.disabled === true`,
		`slot.value === 'Yekan Bakh FaNum'`,
		`$settings.Range('B44').Value2 = 'بله'`,
		`$settings.Range('B44').Value2 = 'خیر'`,
		`if ($productRows.Count -eq 0 -or $tableFirstColumn -le 0 -or`,
		`found = $false`,
		`ProductCatalogSync.AsyncPricingIdleForValidation`,
		`ProductCatalogSync.LastPricingOperationSucceededForValidation`,
		`ProductCatalogSync.LastPricingOperationErrorForValidation`,
		`function Test-RetryableExcelComRejection`,
		`RPC_E_CALL_REJECTED`,
		`RPC_E_SERVERCALL_RETRYLATER`,
		`Invoke-ExcelBusyRetry`,
		`Start-Sleep -Milliseconds 100`,
		`closing the validator Excel process`,
		`public const uint JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`,
		`AssignProcessToJobObject`,
		`IsProcessInJob`,
		`MoveFileEx`,
		`MOVEFILE_REPLACE_EXISTING`,
		`function New-ValidatorKillOnCloseJob`,
		`function Add-ValidatorProcessToJob`,
		`function Get-ExcelProcessIdentity`,
		`function Get-ValidatorProcessIdentityById`,
		`$pathDeadline = [DateTime]::UtcNow.AddSeconds(5)`,
		`executable identity was not readable within 5 seconds`,
		`function Invoke-ComFinalizerBarrier`,
		`function Wait-ValidatorProcessExit`,
		`function New-ValidatorCombinedException`,
		`FinalReleaseComObject`,
		`[void]$process.Handle`,
		`$process.WaitForExit($timeoutMilliseconds)`,
		`$acceptedExitCodes -notcontains $exitCode`,
		`$excelProcessIdentity = Get-ExcelProcessIdentity $excel`,
		`$excelProcessAssignedToJob = Add-ValidatorProcessToJob $validatorJobHandle $excelProcessIdentity`,
		`Write-ValidatorProcessIdentity $excelProcessIdentity $env:PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH $false`,
		`Write-ValidatorProcessIdentity $excelProcessIdentity $env:PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH $excelProcessAssignedToJob`,
		`$exitResult = Wait-ValidatorProcessExit $excelProcessIdentity 15000 @(0)`,
		`PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH`,
		`function cleanupExactOwnedProcess`,
		`function resolveAbnormalValidatorCleanup`,
		`Preserve the same abnormal/unproven evidence invariant even for`,
		`function finalizeValidatorTempDirectory`,
		`preservedRecoveryDirectory = finalizeValidatorTempDirectory(`,
		`abnormalCleanupOutcome,`,
		`pre_identity_timeout_evidence_preserved`,
		`pre_identity_recovery_message_present`,
		`recovery_evidence_preserved_on_cleanup_failure`,
		`Recovery evidence preserved for exact-process remediation:`,
		`Scoped Excel cleanup is unproven:`,
		`--self-test-process-safety`,
		`--self-test-native-excel-timeout`,
		`gatedExitChild`,
		`Wait-SelfTestChildReady`,
		`PATRIS_SELFTEST_EXIT_SEVEN_READY`,
		`PATRIS_SELFTEST_EXIT_SEVEN_RELEASE`,
		`PATRIS_SELFTEST_EXIT_ZERO_READY`,
		`PATRIS_SELFTEST_EXIT_ZERO_RELEASE`,
		`missing_readiness_rejected`,
		`malformed_readiness_rejected`,
		`stale_readiness_rejected`,
		`validatorExcelProcessId`,
		`validatorExcelProcessAssignedToJob`,
		`validatorExcelProcessExited`,
		`validatorExcelProcessExitCode`,
		`validatorExcelProcessStartTimeUtc`,
		`validatorExcelExecutablePath`,
		`PATRIS_VALIDATOR_TIMEOUT_MS`,
		`VALIDATOR_HOST_STARTUP_CLEANUP_GRACE_MS`,
		`foreach ($fixture in @('2.4', '25.40', '12/3', '01.02', '001234'`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("native validator is missing the empty-table guard: %s", required)
		}
	}
	validatorMainStart := strings.LastIndex(source, `function main() {`)
	validatorResultErrorRelative := -1
	if validatorMainStart >= 0 {
		validatorResultErrorRelative = strings.Index(source[validatorMainStart:], `if (result.error) {`)
	}
	if validatorMainStart < 0 || validatorResultErrorRelative < 0 {
		t.Fatal("native validator is missing its Node host cleanup boundary")
	}
	validatorHostCleanup := source[validatorMainStart : validatorMainStart+validatorResultErrorRelative]
	finalizeCall := strings.Index(validatorHostCleanup, `preservedRecoveryDirectory = finalizeValidatorTempDirectory(`)
	tempDirectoryArgument := -1
	abnormalOutcomeArgument := -1
	if finalizeCall >= 0 {
		tempDirectoryArgument = strings.Index(validatorHostCleanup[finalizeCall:], `tempDirectory,`)
		abnormalOutcomeArgument = strings.Index(validatorHostCleanup[finalizeCall:], `abnormalCleanupOutcome,`)
	}
	if finalizeCall < 0 || tempDirectoryArgument < 0 || abnormalOutcomeArgument < 0 ||
		tempDirectoryArgument >= abnormalOutcomeArgument {
		t.Fatal("native validator must preserve exact-process recovery evidence when abnormal cleanup fails")
	}
	if strings.Contains(validatorHostCleanup, `fs.rmSync(tempDirectory`) {
		t.Fatal("native validator must not unconditionally delete exact-process recovery evidence")
	}
	nativeTimeoutStart := strings.Index(source, `function runNativeExcelTimeoutSelfTest() {`)
	nativeTimeoutEndRelative := -1
	if nativeTimeoutStart >= 0 {
		nativeTimeoutEndRelative = strings.Index(source[nativeTimeoutStart:], `function main() {`)
	}
	if nativeTimeoutStart < 0 || nativeTimeoutEndRelative < 0 {
		t.Fatal("native validator is missing its opt-in Excel timeout control boundary")
	}
	nativeTimeoutBody := source[nativeTimeoutStart : nativeTimeoutStart+nativeTimeoutEndRelative]
	if !strings.Contains(nativeTimeoutBody, `resolveAbnormalValidatorCleanup(`) ||
		!strings.Contains(nativeTimeoutBody, `finalizeValidatorTempDirectory(tempDirectory, abnormalCleanupOutcome)`) ||
		strings.Contains(nativeTimeoutBody, `fs.rmSync(tempDirectory`) {
		t.Fatal("native Excel timeout control must preserve evidence unless exact cleanup is proven")
	}

	powershellStart := strings.Index(source, "const powershell = String.raw`")
	if powershellStart < 0 {
		t.Fatal("native validator is missing its bounded main PowerShell body")
	}
	powershellEndRelative := strings.Index(source[powershellStart:], "const ownedProcessCleanupPowerShell = String.raw`")
	if powershellEndRelative < 0 {
		t.Fatal("native validator is missing its bounded main PowerShell body")
	}
	mainPowerShell := source[powershellStart : powershellStart+powershellEndRelative]
	closeReference := strings.LastIndex(mainPowerShell, `$referenceBook.Close($false)`)
	releaseReference := strings.LastIndex(mainPowerShell, `Release-ComObject $referenceBook`)
	closeCandidate := strings.LastIndex(mainPowerShell, `$candidateBook.Close($false)`)
	releaseCandidate := strings.LastIndex(mainPowerShell, `Release-ComObject $candidateBook`)
	quitExcel := strings.LastIndex(mainPowerShell, `$excel.Quit()`)
	releaseExcel := strings.LastIndex(mainPowerShell, `Release-ComObject $excel`)
	waitForProcess := strings.LastIndex(mainPowerShell, `$exitResult = Wait-ValidatorProcessExit $excelProcessIdentity 15000 @(0)`)
	writeReport := strings.LastIndex(mainPowerShell, `[Console]::Out.WriteLine(($report | ConvertTo-Json`)
	getExcelIdentity := strings.LastIndex(mainPowerShell, `$excelProcessIdentity = Get-ExcelProcessIdentity $excel`)
	writeUnassignedIdentity := strings.LastIndex(mainPowerShell, `Write-ValidatorProcessIdentity $excelProcessIdentity $env:PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH $false`)
	assignExcelJob := strings.LastIndex(mainPowerShell, `$excelProcessAssignedToJob = Add-ValidatorProcessToJob $validatorJobHandle $excelProcessIdentity`)
	writeAssignedIdentity := strings.LastIndex(mainPowerShell, `Write-ValidatorProcessIdentity $excelProcessIdentity $env:PATRIS_VALIDATOR_PROCESS_IDENTITY_PATH $excelProcessAssignedToJob`)
	runtimeStart := strings.Index(mainPowerShell, "$excel = $null\n$workbooks = $null")
	firstWorkbooksUse := -1
	if runtimeStart >= 0 {
		firstWorkbooksUseRelative := strings.Index(mainPowerShell[runtimeStart:], `$excel.Workbooks`)
		if firstWorkbooksUseRelative >= 0 {
			firstWorkbooksUse = runtimeStart + firstWorkbooksUseRelative
		}
	}
	if closeReference < 0 || releaseReference < 0 || closeCandidate < 0 || releaseCandidate < 0 ||
		quitExcel < 0 || releaseExcel < 0 || waitForProcess < 0 || writeReport < 0 ||
		getExcelIdentity < 0 || writeUnassignedIdentity < 0 || assignExcelJob < 0 || writeAssignedIdentity < 0 ||
		runtimeStart < 0 || firstWorkbooksUse < 0 {
		t.Fatal("native validator is missing explicit Excel teardown markers")
	}
	if !(getExcelIdentity < writeUnassignedIdentity && writeUnassignedIdentity < assignExcelJob &&
		assignExcelJob < writeAssignedIdentity && writeAssignedIdentity < firstWorkbooksUse &&
		firstWorkbooksUse < closeReference) {
		t.Fatal("native validator must persist exact Excel identity, explicitly job-fence it, confirm the fence, then use the workbook")
	}
	if !(closeReference < releaseReference && releaseReference < closeCandidate &&
		closeCandidate < releaseCandidate && releaseCandidate < quitExcel &&
		quitExcel < releaseExcel && releaseExcel < waitForProcess && waitForProcess < writeReport) {
		t.Fatal("native validator must close/release workbooks before Quit, release Excel, wait for exact PID, then report")
	}

	preQuitBarrier := strings.LastIndex(mainPowerShell[:quitExcel], `Invoke-ComFinalizerBarrier`)
	postQuitBarrierRelative := strings.Index(mainPowerShell[releaseExcel:], `Invoke-ComFinalizerBarrier`)
	if preQuitBarrier < releaseCandidate || postQuitBarrierRelative < 0 || releaseExcel+postQuitBarrierRelative > waitForProcess {
		t.Fatal("native validator must run finalizer barriers after workbook release and after Excel release")
	}

	barrierStart := strings.Index(source, `function Invoke-ComFinalizerBarrier`)
	barrierEndRelative := strings.Index(source[barrierStart:], `function Test-RetryableExcelComRejection`)
	if barrierStart < 0 || barrierEndRelative < 0 {
		t.Fatal("native validator is missing the finalizer barrier function body")
	}
	barrierBody := source[barrierStart : barrierStart+barrierEndRelative]
	if strings.Count(barrierBody, `[GC]::Collect()`) != 2 ||
		strings.Count(barrierBody, `[GC]::WaitForPendingFinalizers()`) != 2 {
		t.Fatal("native validator finalizer barrier must use two collect/finalizer passes")
	}
}

func TestDynamicCalculatorValidatorProcessSafetyBehavior(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("validator process-safety behavior requires Windows job objects and PowerShell")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node is unavailable: %v", err)
	}
	path := filepath.Join("..", "..", "scripts", "windows", "Validate-ExcelPriceCalculator.cjs")
	command := exec.Command(node, path, "--self-test-process-safety")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("validator process-safety self-test failed: %v\n%s", err, output)
	}
	var report struct {
		Passed                                    bool `json:"passed"`
		PreIdentityTimeoutEvidencePreserved       bool `json:"pre_identity_timeout_evidence_preserved"`
		PreIdentityRecoveryMessagePresent         bool `json:"pre_identity_recovery_message_present"`
		RecoveryEvidencePreservedOnCleanupFailure bool `json:"recovery_evidence_preserved_on_cleanup_failure"`
		Behavior                                  struct {
			Passed                        bool `json:"passed"`
			CrashExitRejected             bool `json:"crash_exit_rejected"`
			ExactProcessHandleUsed        bool `json:"exact_process_handle_used"`
			DualFailurePreserved          bool `json:"dual_failure_preserved"`
			ExplicitJobAssignmentVerified bool `json:"explicit_job_assignment_verified"`
			MissingReadinessRejected      bool `json:"missing_readiness_rejected"`
			MalformedReadinessRejected    bool `json:"malformed_readiness_rejected"`
			StaleReadinessRejected        bool `json:"stale_readiness_rejected"`
		} `json:"behavior"`
		Timeout struct {
			SpawnErrorCode     string `json:"spawn_error_code"`
			AssignedToJob      bool   `json:"assigned_to_job"`
			FirstCleanupStatus string `json:"first_cleanup_status"`
			GoneReadbackStatus string `json:"gone_readback_status"`
		} `json:"timeout"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode validator process-safety report: %v\n%s", err, output)
	}
	if !report.Passed || !report.PreIdentityTimeoutEvidencePreserved ||
		!report.PreIdentityRecoveryMessagePresent ||
		!report.RecoveryEvidencePreservedOnCleanupFailure ||
		!report.Behavior.Passed || !report.Behavior.CrashExitRejected ||
		!report.Behavior.ExactProcessHandleUsed || !report.Behavior.DualFailurePreserved ||
		!report.Behavior.ExplicitJobAssignmentVerified || !report.Behavior.MissingReadinessRejected ||
		!report.Behavior.MalformedReadinessRejected || !report.Behavior.StaleReadinessRejected ||
		!report.Timeout.AssignedToJob ||
		report.Timeout.SpawnErrorCode != "ETIMEDOUT" ||
		report.Timeout.FirstCleanupStatus != "already_exited" ||
		report.Timeout.GoneReadbackStatus != "already_exited" {
		t.Fatalf("validator process-safety report failed: %+v", report)
	}
}

func TestDynamicCalculatorValidatorNativeExcelTimeoutBehavior(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Excel timeout behavior requires Windows and Microsoft Excel")
	}
	if os.Getenv("PATRIS_RUN_NATIVE_EXCEL_TIMEOUT_TEST") != "1" {
		t.Skip("set PATRIS_RUN_NATIVE_EXCEL_TIMEOUT_TEST=1 on an Excel host to run the destructive-timeout control")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node is unavailable: %v", err)
	}
	validator := filepath.Join("..", "..", "scripts", "windows", "Validate-ExcelPriceCalculator.cjs")
	command := exec.Command(node, validator, "--self-test-native-excel-timeout")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("validator native Excel timeout self-test failed: %v\n%s", err, output)
	}
	var report struct {
		Passed  bool `json:"passed"`
		Control struct {
			Passed        bool   `json:"passed"`
			AssignedToJob bool   `json:"assigned_to_job"`
			ExitCode      int    `json:"exit_code"`
			Executable    string `json:"executable_path"`
		} `json:"control"`
		Timeout struct {
			SpawnErrorCode     string `json:"spawn_error_code"`
			AssignedToJob      bool   `json:"assigned_to_job"`
			Executable         string `json:"executable_path"`
			FirstCleanupStatus string `json:"first_cleanup_status"`
			GoneReadbackStatus string `json:"gone_readback_status"`
		} `json:"timeout"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode native Excel timeout report: %v\n%s", err, output)
	}
	if !report.Passed || !report.Control.Passed || !report.Control.AssignedToJob ||
		report.Control.ExitCode != 0 || !strings.EqualFold(filepath.Base(report.Control.Executable), "EXCEL.EXE") ||
		report.Timeout.SpawnErrorCode != "ETIMEDOUT" || !report.Timeout.AssignedToJob ||
		!strings.EqualFold(filepath.Base(report.Timeout.Executable), "EXCEL.EXE") ||
		report.Timeout.FirstCleanupStatus != "already_exited" ||
		report.Timeout.GoneReadbackStatus != "already_exited" {
		t.Fatalf("native Excel timeout report failed: %+v", report)
	}
}

func TestDynamicCalculatorBuilderStylesPersianButtonsAndChartText(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "windows", "Build-ExcelDashboard.ps1")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	if strings.Contains(source, "Me.Hide") {
		t.Fatal("generated modal forms must unload instead of leaving a hidden ThunderDFrame")
	}
	for _, required := range []string{
		"function Set-OfficeTextFont",
		"function Set-RangeFontSlots",
		"function Assert-RangeFontSlots",
		"function Assert-ShapeFontSlots",
		"function Assert-FontFamilyAvailable",
		"Assert-FontFamilyAvailable 'Yekan Bakh FaNum' 'Persian price digits'",
		"function Add-DigitalogicMessageForm",
		"$Workbook.VBProject.VBComponents.Add(3)",
		`$workbook.VBProject.References.AddFromGuid(`,
		`'{662901FC-6951-4854-9EB2-D9A2570F2B2E}'`,
		"$asyncWinHttpRequestPath",
		"$pricingSseParserPath",
		`Name = 'AsyncWinHttpRequest'`,
		`Name = 'PricingSseParser'`,
		`[int]$component.Type -ne 2`,
		"Public Function ValidateFonts",
		"Private Const ARABIC_CHARSET As Long = 178",
		"Public Sub ConfigureUnicodeHex",
		"Private Function DecodeUtf16Hex",
		"Public Function ValidateUnicodeCaptions",
		"Private Sub ApplyPersianControlFont",
		"ChrW$(codeUnit)",
		"target.Font.Charset = ARABIC_CHARSET",
		`Me.Caption = "DIGITALOGIC - Price Sync"`,
		"function Replace-ByteSequence",
		"$neutralProfile = 'C:\\ProgramData'",
		`$profileAppDataFragment = "Users\$profileLeaf\AppData"`,
		`$neutralAppDataFragment = "Users\$neutralUser\AppData"`,
		`$fontNode.SetAttribute('lang', 'fa-IR')`,
		"lblMessage.Font.Name = persianFont",
		"cmdPrimary.Font.Name = persianFont",
		"lblBrand.Font.Name = latinFont",
		"NameComplexScript = 'Yekan Bakh'",
		"NameFarEast = 'Yekan Bakh'",
		"$TextRange.LanguageID = 1065",
		"$shape.TextFrame.Characters().Font.Name = 'Yekan Bakh'",
		"$shape.Top = $Anchor.Top + (($Anchor.Height - $shape.Height) / 2)",
		"$priceList.Rows.Item(1).RowHeight = 60",
		"$dashboard.Rows.Item(1).RowHeight = 60",
		"$settings.Rows.Item(1).RowHeight = 60",
		"$settings.Range('B5').Value2 = 'بله'",
		"$priceList.Range('C3:E3').NumberFormat = '@'",
		"$searchButton.Name = 'ProductSearchButton'",
		"$searchButton.AlternativeText = ",
		"[void]$workbook.Names.Add($setting.Name, $settings.Range(\"B${row}\"))",
		"Name = 'PriceDisplayFaNum'",
		"Value = 'ترمیم و هشدار'",
		"Value = 'بله'",
		"Value = 'خیر'",
		"$settings.Range('B41').Validation.Add(3, 1, 1, 'خاموش,هشدار,ترمیم و هشدار,سختگیرانه')",
		"$settings.Range('B44').Validation.Add(3, 1, 1, 'بله,خیر')",
		"Set-RangeFontSlots $priceList.Range('B6') 'Yekan Bakh FaNum'",
		"Set-RangeFontSlots $priceList.Range('C6,F6:H6,J6') 'Segoe UI'",
		"Set-RangeFontSlots $priceList.Range('I6,K6') 'Yekan Bakh'",
		"Products column B is narrower than the required 20-character minimum",
		"Products column K is narrower than the required 34-character minimum",
		"Set-RangeFontSlots $settings.Range('A1:F55') 'Yekan Bakh'",
		"Set-RangeFontSlots $settings.Range('B3:F4,B7:F7,B10:F15,B18:F22,B24:F26,B39:F40,B46:F55') 'Segoe UI'",
		"Assert-RangeFontSlots $settings.Range('B41:F44') 'Yekan Bakh' 'localized font policy values'",
		"(46..55)",
		"[void]$workbook.Names.Add('ProjectedPricePreviewRow', $settings.Range('G48'))",
		"'مبلغ منبع قیمت'",
		"'ارز منبع قیمت'",
		"'نوع منبع قیمت'",
		"$syncData.Range('A1:X2')",
		"'نمایش تصاویر محصولات'",
		"$settings.Range('B31').Value2 = 'خیر'",
		"$settings.Range('B31').Validation.Add(3, 1, 1, 'خیر,بله')",
		"[void]$workbook.Names.Add('ShowProductImages', $settings.Range('B31'))",
		"[void]$workbook.Names.Add('ProductImagePreviewStatus', $priceList.Range('M22'))",
		"[void]$workbook.Names.Add('ProductImagePreviewArea', $imageAreaRange)",
		"Set-OfficeTextFont $chart.ChartTitle.Format.TextFrame2.TextRange",
		"Set-OfficeTextFont $chart.Legend.Format.TextFrame2.TextRange",
		"'تعداد رقم گردکردن قیمت'",
		"'تعداد رقم گردکردن قیمت پیشنهادی'",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("canonical builder is missing Persian font/rounding guard: %s", required)
		}
	}
}

func TestDynamicCalculatorVBANormalizesExcelDateSerials(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "examples", "vba", "ProductCatalogSync.bas")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)

	for _, required := range []string{
		"ValidateProposalDateNormalization",
		"CanonicalDateText(CDbl(sampleDate)) <> expectedDate",
		"CanonicalDateValuesEqual(CDbl(sampleDate), expectedDate)",
		`UpdateProposalDateCell settings.Range("B20"), settings.Range("G20"), _`,
		`UpdateProposalDateCell settings.Range("E20"), settings.Range("H16"), _`,
		`CanonicalDateText(settings.Range("B20").Value2)`,
		`CanonicalDateText(settings.Range("E20").Value2)`,
		"proposal.Value2 = remoteText",
		"baseline.Value2 = remoteText",
		"UpdateProposalDriftFlags settings, 29500#, 187891#, expectedDate, _",
		`If BooleanValue(settings.Range("G39").Value2) Or _`,
		`If Not BooleanValue(settings.Range("G39").Value2) Then`,
		"RestorePricingStateSnapshot settings, settingsSnapshot",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("VBA date normalization regression guard is missing: %s", required)
		}
	}

	for _, forbidden := range []string{
		`CanonicalCellText(settings.Range("B20").Value2) <>`,
		`cnyEffectiveDate = Trim$(CStr(settings.Range("B20").Value2))`,
		`dateText = Trim$(CStr(settings.Range("B20").Value2))`,
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("VBA retains serial-vs-ISO date comparison path: %s", forbidden)
		}
	}
}

func mustXLSXRows(t *testing.T, book *excelize.File, sheet string) [][]string {
	t.Helper()
	rows, err := book.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatal(err)
	}
	return rows
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
