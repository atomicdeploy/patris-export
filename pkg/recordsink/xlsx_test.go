package recordsink

import (
	"archive/zip"
	"encoding/json"
	"fmt"
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
				"B3": "http://127.0.0.1:18080/api/product-sync",
				"B4": "http://127.0.0.1:18080/api/excel/pricing-sync/state",
				"G8": "canonical",
			} {
				got, valueErr := book.GetCellValue(settingsSheet, cell, excelize.Options{RawCellValue: true})
				if valueErr != nil || got != want {
					t.Fatalf("%s!%s = %q, want %q, err=%v", settingsSheet, cell, got, want, valueErr)
				}
			}
			for _, cell := range []string{
				"B10", "B11", "B12", "B13", "B14", "B15",
				"B18", "B19", "B20", "B21", "B22", "B23",
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
				18, 19, 20, 21, 22, 23,
			} {
				wantMerge := fmt.Sprintf("B%d:F%d", row, row)
				if !settingsMerges[wantMerge] {
					t.Fatalf(
						"%s settings row %d does not preserve its independent merge %s; merges=%v",
						settingsSheet, row, wantMerge, settingsMerges,
					)
				}
			}
			if value, valueErr := book.GetCellValue(
				settingsSheet, "A27", excelize.Options{RawCellValue: true},
			); valueErr != nil || value != "" {
				t.Fatalf("%s!A27 retained unnecessary static text %q, err=%v", settingsSheet, value, valueErr)
			}

			syncTables, syncTableErr := book.GetTables(syncSheet)
			if syncTableErr != nil {
				t.Fatal(syncTableErr)
			}
			if len(syncTables) != 1 || syncTables[0].Name != "SyncData" ||
				strings.ReplaceAll(syncTables[0].Range, "$", "") != "A1:T2" {
				t.Fatalf("SyncData table = %+v, want empty A1:T2 table", syncTables)
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
		"columnSignature <> RECONCILED_COLUMN_KEYS",
		"If siteRows.Exists(identityKey) Then",
	} {
		position := strings.Index(source, requiredBeforeReplace)
		if position < 0 || replacePosition < 0 || position > replacePosition {
			t.Fatalf("VBA safeguard %q is missing or runs after product replacement", requiredBeforeReplace)
		}
	}
	readSourcePosition := strings.Index(source, "ReadSourceIdentity contract")
	refreshPosition := strings.Index(source, "reconciledRowsFetched = RefreshPricingState(reconciledRows)")
	importPosition := strings.Index(source, "productRows = ImportReconciledCatalog(reconciledRows)")
	if readSourcePosition < 0 || refreshPosition < 0 || importPosition < 0 ||
		readSourcePosition > refreshPosition || refreshPosition > importPosition {
		t.Fatalf("source identity and complete reconciled snapshot must be validated before product import")
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
		`linkCell.Font.Name = "Yekan Bakh"`,
	)
	wooLinkBoldAfterAddPosition := strings.Index(
		wooLinkRowSource[wooLinkAddPosition:],
		"linkCell.Font.Bold = True",
	)
	if wooLinkFontAfterAddPosition < 0 || wooLinkBoldAfterAddPosition < 0 ||
		wooLinkFontAfterAddPosition > wooLinkBoldAfterAddPosition {
		t.Fatal("WooCommerce hyperlink styling must restore Yekan Bakh before emphasis")
	}

	for _, required := range []string{
		`Attribute VB_Name = "ProductCatalogSync"`,
		`Private Const SYNC_TABLE As String = "SyncData"`,
		"Public Sub PreviewPricingChanges()",
		"Private Sub PreviewPricingChangesCore(ByVal showMessage As Boolean)",
		"Public Sub ApplyPricingChanges()",
		"Public Sub HandlePricingProposalChanged()",
		"mLastPreviewSettings = PricingSettingsCanonical()",
		"mLastPreviewStateRevision <> _",
		"mLastApplyRequestID = NewRequestID(\"apply\")",
		"RefreshAllData True",
		`ConfigSheet().Range("B10").Value2`,
		`ConfigSheet().Range("B13").Value2`,
		`settings.Range("B14").Value = remoteShipping`,
		`requestBody = BuildPricingRequest("preview", requestID, vbNullString, False)`,
		`JsonRuntime.JsonText(result, "preview_digest")`,
		"If Len(mLastPreviewDigest) = 0 Then",
		"If answer <> vbYes Then Exit Sub",
		`body = body & ",""preview_digest"":" & JsonString(previewDigest)`,
		`body = body & ",""confirmation"":""APPLY"""`,
		`"""idempotency_key"":" & JsonString(requestID)`,
		`"""product_changes"":[]`,
		`Private Const PRICING_CLIENT_HEADER As String = "X-Patris-Excel-Client"`,
		`Private Const PRICING_CSRF_HEADER As String = "X-Patris-Excel-CSRF-Token"`,
		`Private Const PRICING_REQUEST_SCHEMA As String = "patris.excel-pricing-companion-request/v1"`,
		`Private Const PRICING_SESSION_SCHEMA As String = "patris.excel-pricing-companion-session/v1"`,
		"Private Const STATE_PAGE_SIZE As Long = 250",
		"Private Const MAX_STATE_PAGES As Long = 8",
		"Private Const STATE_SNAPSHOT_RETRIES As Long = 3",
		`Private Const RECONCILED_COLUMN_KEYS As String = _`,
		"Private Const HTTP_TIMEOUT_MS As Long = 150000",
		"Private Const PRICING_HTTP_TIMEOUT_MS As Long = 600000",
		"Private Function StateRequestJson",
		"Private Function RefreshPricingStateOnce",
		"Private Sub RepairCanonicalDelivery()",
		"On Error GoTo RepairFailed",
		"For attempt = 1 To STATE_SNAPSHOT_RETRIES + 1",
		"Not sourceRepairAttempted Then Exit For",
		"csrfToken = PricingSessionToken()",
		`UniversalRefreshURL(), "{""delivery"":""wait""}", csrfToken, "", "")`,
		`JsonRuntime.JsonText(root, "source_revision")`,
		"mSourceRevision <> deliveredRevision",
		"Private Function UniversalRefreshURL() As String",
		`"/api/refresh"`,
		"Public Function RefreshAllDataForValidation() As Boolean",
		"RefreshAllDataForValidation = mLastRefreshSucceeded",
		"ValidateProjectionIntegrityGuard",
		"ProjectionIntegrityFixtureRejected",
		"pricingStateSnapshot = CapturePricingStateSnapshot(settings)",
		"RestorePricingStateSnapshot settings, pricingStateSnapshot",
		"RestorePricingStateSnapshot settings, retryStateSnapshot",
		`"product_type_cache_drift_term_changed"`,
		`"projection_integrity_product_type_readback_failed"`,
		`datasetRevision = SiteText(catalog, "dataset_revision")`,
		`currentRevision = SiteText(sourceValue, "current_revision")`,
		`submittedRevision = SiteText(sourceValue, "submitted_revision")`,
		`reconciledRevision = SiteText(reconciliationSource, "revision")`,
		"currentRevision <> mSourceRevision",
		"RejectProjectionIntegrityWarnings state",
		`Left$(warningCode, Len("product_type_cache_drift"))`,
		`Left$(warningCode, Len("projection_integrity"))`,
		`If LCase$(CStr(memberName)) <> "rows" Then`,
		"Private Function CatalogColumnSignature",
		"Private Function CatalogCountSignature",
		"paginationTotal <> firstPaginationTotal",
		"siteRows.Count <> firstPaginationTotal",
		"EnsureSourceIdentity",
		`JsonString(mSourceID)`,
		`JsonString(mSourceDataset)`,
		`JsonString(mSourceRevision)`,
		"Private Function PricingSessionToken() As String",
		`sessionText = HttpPostJsonRaw(`,
		`http.setRequestHeader "Idempotency-Key", idempotencyKey`,
		`"""expected_state_revision"":"`,
		`"""client_id"":" & JsonString(PRICING_CONTRACT_CLIENT_ID)`,
		`"""channel"":" & JsonString(PRICING_CONTRACT_CHANNEL)`,
		`"""request_id"":" & JsonString(requestID)`,
		`"""usd_effective_date"":" & JsonString(usdEffectiveDate)`,
		`"""cny_effective_date"":" & JsonString(cnyEffectiveDate)`,
		`"""profit_margin_percent"":" & JsonNumberOrNull(profitPercent)`,
		`"""air_express_price_per_kg"":" & JsonNumberOrNull(settings.Range("B22").Value2)`,
		`"""price_rounding_digits"":" & JsonNumberOrNull(settings.Range("B26").Value2)`,
		`"""price_rounding_mode"":" & JsonString(PRICE_ROUNDING_MODE)`,
		`"""air_express_currency"":" & JsonString(shippingCurrency)`,
		`"""shipping_catalog_revision"":" & JsonString(shippingRevision)`,
		`http.setRequestHeader "If-Match", _`,
		`Chr$(34) & expectedRevision & Chr$(34)`,
		"Private Function ResponseErrorMessage",
		`errorMessage = ResponseErrorMessage(CStr(http.responseText))`,
		"Private Function SafeStatusError",
		`InStr(lowered, "credential")`,
		"If Len(message) > 300 Then",
		"Private Function IsAllowedPricingBridgeUrl",
		"http.setProxy 1",
		`http.Open "POST", endpoint, False`,
		"Private Function SyncSheet() As Worksheet",
		"Private Function IsAllowedDigitalogicUrl",
		`"https://digitalogic.ir/"`,
		"reconciledRows.CompareMode = vbBinaryCompare",
		`patrisCodeValue = SiteText(reconciledRow, "patris_code")`,
		`syncKey <> "woo:" & wooIDValue`,
		`syncKey <> "patris:" & patrisCodeValue`,
		`codeValue = SiteText(reconciledRow, "sku")`,
		"ApplyWooLinkRow table, syncTable, rowIndex",
		"Private Sub ApplyWooLinkRow",
		"On Error GoTo RowFailed",
		"RowFailed:",
		`wooCell.NumberFormat = "@"`,
		"wooCell.Value2 = wooID",
		"linkCell.Value2 = linkText",
		"table.Parent.Hyperlinks.Add",
		"IsAllowedDigitalogicUrl(permalink)",
		"linkCell.Font.Bold = True",
		`Case "publish"`,
		`Case "draft", "pending", "private", "future"`,
		"table.ListColumns(1).DataBodyRange.FormulaR1C1 = priceFormula",
		`"IF(RC[8]<>"""",""woo:""&RC[8],""patris:""&RC[6])"`,
		`fallbackFormula = _`,
		`readyFormula = _`,
		"ROUND(",
		`U("062A0646063806CC06450627062A") & "'!R15C2`,
		",SyncData,20,FALSE)",
		",SyncData,10,FALSE)",
		`=""CNY""`,
		`=""USD""`,
		`=""IRR""`,
		`=""IRT""`,
		"RC[4]",
		`Private Declare PtrSafe Function MessageBoxW Lib "user32"`,
		"StrPtr(message)",
		"StrPtr(title)",
		"Private Function ShowUnicodeMessage",
		"ValidateUnicodeRuntime",
		"MB_RIGHT",
		"MB_RTLREADING",
		"Public Sub SearchProducts()",
		"Public Sub ClearProductSearch()",
		`Private Const SEARCH_BUTTON_SHAPE As String = "ProductSearchButton"`,
		"Set matches = ProductSearchMatchRows(table, query)",
		"Private Function ProductSearchMatchRows",
		"Private Function ProductRowMatchesQuery",
		"Private Function NextProductSearchMatchIndex",
		"mSearchCurrentRow = rowIndex",
		`SetSearchButtonCaption T("search_button") & " (" & _`,
		"ActiveWindow.ScrollColumn = ProductViewportColumn(table)",
		"ProductViewportColumn = Application.Max(1, table.Range.Column - 1)",
		`T = U("067E06CC062F0627002006A90631062F0646")`,
		"Public Sub HighlightSelectedProductRow",
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
		"X-Patris-Product-Sync-Secret",
		"PATRIS_PRODUCT_SYNC_SECRET",
		"MsgBox ",
		"MsgBox(",
		"VBA.MsgBox",
	} {
		if strings.Contains(strings.ToLower(source), strings.ToLower(forbidden)) {
			t.Fatalf("VBA source contains forbidden legacy or credential path: %s", forbidden)
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
		"Private Sub Workbook_SheetChange",
		`Union(Sh.Range("B18:B22"), _`,
		`Sh.Range("B26"))`,
		"ProductCatalogSync.HandlePricingProposalChanged",
		"Private Sub Workbook_SheetSelectionChange",
		"ProductCatalogSync.HighlightSelectedProductRow Target",
	} {
		if !strings.Contains(string(thisWorkbookContent), required) {
			t.Fatalf("ThisWorkbook source is missing pricing-change guard: %s", required)
		}
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

func TestDynamicCalculatorBuilderStylesPersianButtonsAndChartText(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "windows", "Build-ExcelDashboard.ps1")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	for _, required := range []string{
		"function Set-OfficeTextFont",
		"NameComplexScript = 'Yekan Bakh'",
		"NameFarEast = 'Yekan Bakh'",
		"$shape.TextFrame.Characters().Font.Name = 'Yekan Bakh'",
		"$searchButton.Name = 'ProductSearchButton'",
		"$searchButton.AlternativeText = ",
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
		`CanonicalDateText(settings.Range("B20").Value2)`,
		`CanonicalDateText(settings.Range("H16").Value2)`,
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
