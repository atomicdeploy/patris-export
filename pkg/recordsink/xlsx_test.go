package recordsink

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

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
	wantHeaders := []string{"Product Code", "Name", "Foreign Price", "Weight (g)", "Shipping Price/kg", "Shipping Currency", "Profit Margin (%)", "IRT per CNY", "Final Price (IRT)", "Warnings"}
	if strings.Join(recordRows[0], "|") != strings.Join(wantHeaders, "|") {
		t.Fatalf("canonical column order = %v, want %v", recordRows[0], wantHeaders)
	}
	columns := headerColumns(recordRows[0])
	assertXLSXCell(t, book, "Records", cellAt(columns, "Product Code", 2), "00113007045", excelize.CellTypeSharedString)
	assertXLSXCell(t, book, "Records", cellAt(columns, "Name", 2), "ماژول آزمون", excelize.CellTypeSharedString)
	assertXLSXCell(t, book, "Records", cellAt(columns, "Foreign Price", 2), "24.5", excelize.CellTypeNumber)
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
	if !strings.Contains(worksheetXML, `<autoFilter ref="$A$1:$J$2"`) {
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
		"هزینه حمل/کیلوگرم", "ارز هزینه حمل", "حاشیه سود (%)", "نرخ ریال به یوان", "قیمت نهایی (ریال)",
	}
	if strings.Join(recordRows[0], "|") != strings.Join(wantHeaders, "|") {
		t.Fatalf("localized columns = %v, want %v", recordRows[0], wantHeaders)
	}
	columns := headerColumns(recordRows[0])
	assertXLSXCell(t, book, "Records", cellAt(columns, "کد کالا", 2), "001", excelize.CellTypeSharedString)
	assertXLSXCell(t, book, "Records", cellAt(columns, "موجودی انبار ۲", 2), "1", excelize.CellTypeNumber)
	assertXLSXCell(t, book, "Records", cellAt(columns, "موجودی انبار ۱۰", 2), "3", excelize.CellTypeNumber)

	finalCell := cellAt(columns, "قیمت نهایی (ریال)", 2)
	wantFormula := `=IF(AND(COUNT(E2,F2,G2,I2,J2)=5,OR(H2="CNY",H2="IRR")),ROUND((E2*J2+F2/1000*IF(H2="CNY",G2*J2,G2/10))*(1+I2/100),0),"")`
	if formula, err := book.GetCellFormula("Records", finalCell); err != nil || formula != wantFormula {
		t.Fatalf("formula = %q, want %q, err=%v", formula, wantFormula, err)
	}
	if calculated, err := book.CalcCellValue("Records", finalCell, excelize.Options{RawCellValue: true}); err != nil || calculated != "2009410" {
		t.Fatalf("calculated final price = %q, want 2009410, err=%v", calculated, err)
	}
	missingCell := cellAt(columns, "قیمت نهایی (ریال)", 3)
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
			"final_price":                    nil,
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
	for row, want := range map[int]string{2: "4290000", 3: "4290000", 4: ""} {
		cell, _ := excelize.CoordinatesToCellName(finalColumn, row)
		got, calcErr := book.CalcCellValue("Records", cell, excelize.Options{RawCellValue: true})
		if calcErr != nil || got != want {
			t.Fatalf("formula result at %s = %q, want %q, err=%v", cell, got, want, calcErr)
		}
	}
	formulaCell, _ := excelize.CoordinatesToCellName(finalColumn, 2)
	formula, err := book.GetCellFormula("Records", formulaCell)
	if err != nil || !strings.Contains(formula, `="CNY"`) || !strings.Contains(formula, `="IRR"`) || !strings.Contains(formula, "/10") {
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

func TestDashboardExamplePackageHasNeutralMetadataAndNoExternalConnections(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "examples", "Patris-Digitalogic-Dashboard.xlsm")
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()

	var coreProperties string
	for _, entry := range archive.File {
		if strings.HasPrefix(entry.Name, "xl/externalLinks/") || entry.Name == "xl/connections.xml" {
			t.Fatalf("dashboard contains external Office connection %q", entry.Name)
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
				t.Fatalf("dashboard package entry %q leaked %q", entry.Name, forbidden)
			}
		}
		if entry.Name == "docProps/core.xml" {
			coreProperties = string(content)
		}
	}
	if !strings.Contains(coreProperties, "<dc:creator>AtomicDeploy</dc:creator>") ||
		!strings.Contains(coreProperties, "<cp:lastModifiedBy>AtomicDeploy</cp:lastModifiedBy>") {
		t.Fatalf("dashboard core properties are not project-owned: %s", coreProperties)
	}
	if strings.Contains(coreProperties, "dcterms:created") || strings.Contains(coreProperties, "dcterms:modified") {
		t.Fatalf("dashboard core properties contain volatile build timestamps: %s", coreProperties)
	}
}

func TestDashboardVBASourceValidatesCodesAndDigitalogicJSONBeforeMutation(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "examples", "vba", "PatrisDashboard.bas")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	deletePosition := strings.Index(source, "table.DataBodyRange.Delete")
	for _, requiredBeforeDelete := range []string{
		"If Not HasAnyHeader(headers, \"product_code\", \"Code\", \"code\")",
		"If Len(codeValue) = 0 Then",
		"If seenCodes.Exists(codeValue) Then",
	} {
		position := strings.Index(source, requiredBeforeDelete)
		if position < 0 || deletePosition < 0 || position > deletePosition {
			t.Fatalf("VBA safeguard %q is missing or runs after table deletion", requiredBeforeDelete)
		}
	}
	for _, required := range []string{
		"The Digitalogic endpoint returned an empty response.",
		"The Digitalogic endpoint did not return a JSON object or array.",
		"If Not IsValidJsonResponse(responseText) Or _",
		`Left$(responseText, 1) <> "{"`,
		"Private Function ParseJsonObject",
		"Private Function ParseJsonArray",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("VBA source is missing validation: %s", required)
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
