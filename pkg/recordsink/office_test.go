package recordsink

import (
	"archive/zip"
	"encoding/base64"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

const transparentPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func testVBAProject(t *testing.T) []byte {
	t.Helper()
	candidates, err := filepath.Glob(filepath.Join("..", "..", "docs", "examples", "*.xltm"))
	if err != nil || len(candidates) != 1 {
		t.Fatalf("locate tracked macro fixture: candidates=%v error=%v", candidates, err)
	}
	archive, err := zip.OpenReader(candidates[0])
	if err != nil {
		t.Fatalf("open tracked macro fixture: %v", err)
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if entry.Name != "xl/vbaProject.bin" {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		value, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if len(value) == 0 {
			t.Fatal("tracked vbaProject.bin is empty")
		}
		return value
	}
	t.Fatal("tracked macro fixture has no vbaProject.bin")
	return nil
}

func createRealMacroPackage(t *testing.T, extension string, populated bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trusted"+extension)
	book := excelize.NewFile()
	if err := book.SetSheetName("Sheet1", "Records"); err != nil {
		t.Fatal(err)
	}
	for cellName, value := range map[string]string{"A1": "product_code", "B1": "name"} {
		if err := book.SetCellStr("Records", cellName, value); err != nil {
			t.Fatal(err)
		}
	}
	if populated {
		if err := book.SetCellStr("Records", "A2", "REJECTED-INITIAL-ROW"); err != nil {
			t.Fatal(err)
		}
		if err := book.SetCellStr("Records", "B2", "Must fail closed"); err != nil {
			t.Fatal(err)
		}
	}
	if err := book.AddTable("Records", &excelize.Table{Range: "A1:B2", Name: "ExportProducts", StyleName: "TableStyleMedium2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := book.NewSheet("Formula Sheet"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellInt("Formula Sheet", "A1", 1); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellFormula("Formula Sheet", "A2", "A1+1"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetDefinedName(&excelize.DefinedName{Name: "KeepFormula", RefersTo: "'Formula Sheet'!$A$2"}); err != nil {
		t.Fatal(err)
	}
	picture, err := base64.StdEncoding.DecodeString(transparentPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	if err := book.AddPictureFromBytes("Formula Sheet", "C1", &excelize.Picture{Extension: ".png", File: picture}); err != nil {
		t.Fatal(err)
	}
	if err := book.AddVBAProject(testVBAProject(t)); err != nil {
		t.Fatal(err)
	}
	if err := book.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectOfficePackage(path, extension); err != nil {
		t.Fatalf("generated real Office fixture is invalid: %v", err)
	}
	return path
}

func createRealXLSXPackage(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trusted.xlsx")
	book := excelize.NewFile()
	if err := book.SetSheetName("Sheet1", "Records"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellStr("Records", "A1", "product_code"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellStr("Records", "B1", "name"); err != nil {
		t.Fatal(err)
	}
	if err := book.AddTable("Records", &excelize.Table{Range: "A1:B2", Name: "ExportProducts", StyleName: "TableStyleMedium2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := book.NewSheet("Workbook Structure"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellFormula("Workbook Structure", "A2", "1+1"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetDefinedName(&excelize.DefinedName{Name: "KeepFormula", RefersTo: "'Workbook Structure'!$A$2"}); err != nil {
		t.Fatal(err)
	}
	if err := book.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectOfficePackage(path, ".xlsx"); err != nil {
		t.Fatalf("generated real XLSX fixture is invalid: %v", err)
	}
	return path
}

func TestPopulateTrustedXLSXReusesConfiguredPackageAndWritesCurrentRows(t *testing.T) {
	source := createRealXLSXPackage(t)
	output := filepath.Join(t.TempDir(), "populated.xlsx")
	report, err := PopulateTrustedXLSX(output, []map[string]interface{}{
		{"product_code": "0012", "name": "Configured - WooID 45"},
		{"product_code": "12/3", "name": "Literal"},
	}, "product_code", XLSXOptions{}, TrustedOfficeContract{TemplatePath: source, Target: "table:ExportProducts"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Format != "xlsx" || report.RecordCount != 2 || report.VBAProjectSHA256 != "" || report.SourceSHA256 == report.OutputSHA256 {
		t.Fatalf("unexpected XLSX report: %+v", report)
	}
	book, err := excelize.OpenFile(output)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	for cellName, want := range map[string]string{"A2": "0012", "B2": "Configured", "A3": "12/3", "B3": "Literal"} {
		got, err := book.GetCellValue("Records", cellName, excelize.Options{RawCellValue: true})
		if err != nil || got != want {
			t.Fatalf("Records!%s = %q, want %q (error=%v)", cellName, got, want, err)
		}
	}
	if formula, err := book.GetCellFormula("Workbook Structure", "A2"); err != nil || formula != "1+1" {
		t.Fatalf("preserved formula = %q, want 1+1 (error=%v)", formula, err)
	}
}

func TestPopulateTrustedXLSMPreservesPackageAndWritesExactCurrentRows(t *testing.T) {
	source := createRealMacroPackage(t, ".xlsm", false)
	sourceHash, err := fileSHA256(source)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "populated.xlsm")
	rows := []map[string]interface{}{
		{"product_code": "0012", "name": "Regulator - WooID 1234"},
		{"product_code": "25.40", "name": "Literal code"},
	}
	report, err := PopulateTrustedXLSM(output, rows, "product_code", XLSXOptions{}, TrustedOfficeContract{
		TemplatePath: source,
		Target:       "table:ExportProducts",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.RecordCount != 2 || report.DataEmpty || report.SourceSHA256 != sourceHash || report.SourceSHA256 == report.OutputSHA256 || report.VBAProjectSHA256 == "" {
		t.Fatalf("unexpected XLSM report: %+v", report)
	}
	if after, err := fileSHA256(source); err != nil || after != sourceHash {
		t.Fatalf("trusted source was mutated: hash=%q error=%v", after, err)
	}
	book, err := excelize.OpenFile(output)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	for cellName, want := range map[string]string{"A2": "0012", "B2": "Regulator", "A3": "25.40", "B3": "Literal code"} {
		got, err := book.GetCellValue("Records", cellName, excelize.Options{RawCellValue: true})
		if err != nil || got != want {
			t.Fatalf("Records!%s = %q, want %q (error=%v)", cellName, got, want, err)
		}
	}
	if formula, err := book.GetCellFormula("Formula Sheet", "A2"); err != nil || formula != "A1+1" {
		t.Fatalf("formula = %q, want A1+1 (error=%v)", formula, err)
	}
	tables, err := book.GetTables("Records")
	if err != nil || len(tables) != 1 || tables[0].Range != "A1:B3" {
		t.Fatalf("populated table = %+v (error=%v), want A1:B3", tables, err)
	}
	snapshot, err := inspectOfficePackage(output, ".xlsm")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.richParts) == 0 || len(snapshot.sheets) != 2 || snapshot.definedNames["|KeepFormula"] == "" {
		t.Fatalf("rich workbook structure was not retained: rich=%v sheets=%v names=%v", snapshot.richParts, snapshot.sheets, snapshot.definedNames)
	}
}

func TestPopulateTrustedXLSMClearsInitialRowsForEmptyCurrentSnapshot(t *testing.T) {
	source := createRealMacroPackage(t, ".xlsm", true)
	output := filepath.Join(t.TempDir(), "empty-current.xlsm")
	report, err := PopulateTrustedXLSM(output, nil, "product_code", XLSXOptions{}, TrustedOfficeContract{
		TemplatePath: source,
		Target:       "table:ExportProducts",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.RecordCount != 0 || report.DataEmpty {
		t.Fatalf("empty current XLSM report = %+v", report)
	}
	book, err := excelize.OpenFile(output)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	for _, cellName := range []string{"A2", "B2"} {
		if value, err := book.GetCellValue("Records", cellName, excelize.Options{RawCellValue: true}); err != nil || value != "" {
			t.Fatalf("empty current snapshot left Records!%s=%q (error=%v)", cellName, value, err)
		}
	}
}

func TestPopulateTrustedXLSMSupportsExplicitWorkbookNamedRange(t *testing.T) {
	source := createRealMacroPackage(t, ".xlsm", false)
	book, err := excelize.OpenFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := book.SetDefinedName(&excelize.DefinedName{Name: "ExportRange", RefersTo: "Records!$A$1:$B$2"}); err != nil {
		t.Fatal(err)
	}
	if err := book.Save(); err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "named.xlsm")
	_, err = PopulateTrustedXLSM(output, []map[string]interface{}{
		{"product_code": "0007", "name": "Named target"},
		{"product_code": "12/3", "name": "Literal code"},
	}, "product_code", XLSXOptions{}, TrustedOfficeContract{TemplatePath: source, Target: "name:ExportRange"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := excelize.OpenFile(output)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	found := false
	for _, definedName := range result.GetDefinedName() {
		if definedName.Name == "ExportRange" {
			found = strings.Contains(strings.ReplaceAll(definedName.RefersTo, "$", ""), "A1:B3")
		}
	}
	if !found {
		t.Fatalf("ExportRange was not resized to the two current rows: %+v", result.GetDefinedName())
	}
}

func TestPopulateTrustedXLSMFailsClosedForInvalidSourcesTargetsAndRows(t *testing.T) {
	source := createRealMacroPackage(t, ".xlsm", false)
	tests := []struct {
		name     string
		contract TrustedOfficeContract
		rows     []map[string]interface{}
		want     string
	}{
		{name: "relative source", contract: TrustedOfficeContract{TemplatePath: "trusted.xlsm", Target: "table:ExportProducts"}, rows: []map[string]interface{}{{"product_code": "A", "name": "A"}}, want: "absolute local"},
		{name: "URL source", contract: TrustedOfficeContract{TemplatePath: "https://example.invalid/trusted.xlsm", Target: "table:ExportProducts"}, rows: []map[string]interface{}{{"product_code": "A", "name": "A"}}, want: "allowlisted local"},
		{name: "missing target kind", contract: TrustedOfficeContract{TemplatePath: source, Target: "ExportProducts"}, rows: []map[string]interface{}{{"product_code": "A", "name": "A"}}, want: "explicit"},
		{name: "unknown target", contract: TrustedOfficeContract{TemplatePath: source, Target: "table:Missing"}, rows: []map[string]interface{}{{"product_code": "A", "name": "A"}}, want: "does not exist"},
		{name: "duplicate code", contract: TrustedOfficeContract{TemplatePath: source, Target: "table:ExportProducts"}, rows: []map[string]interface{}{{"product_code": "A", "name": "A"}, {"product_code": "A", "name": "B"}}, want: "duplicate product code"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "rejected.xlsm")
			_, err := PopulateTrustedXLSM(output, test.rows, "product_code", XLSXOptions{}, test.contract)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
			if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
				t.Fatalf("failed population finalized an output: %v", statErr)
			}
		})
	}
	wrongFormatOutput := filepath.Join(t.TempDir(), "must-not-bake.xltm")
	if _, err := PopulateTrustedXLSM(wrongFormatOutput, []map[string]interface{}{{"product_code": "A", "name": "A"}}, "product_code", XLSXOptions{}, TrustedOfficeContract{TemplatePath: source, Target: "table:ExportProducts"}); err == nil || !strings.Contains(err.Error(), "XLSM path") {
		t.Fatalf("XLSM population accepted an XLTM output path: %v", err)
	}
	if _, err := os.Stat(wrongFormatOutput); !os.IsNotExist(err) {
		t.Fatalf("XLSM-to-XLTM population finalized an output: %v", err)
	}
}

func TestOfficePackageValidationRejectsRealExtensionContentMismatch(t *testing.T) {
	xlsxDir := t.TempDir()
	xlsxSource := filepath.Join(xlsxDir, "source.xlsx")
	xlsx := filepath.Join(xlsxDir, "renamed.xlsm")
	book := excelize.NewFile()
	if err := book.SaveAs(xlsxSource); err != nil {
		t.Fatal(err)
	}
	_ = book.Close()
	if err := os.Rename(xlsxSource, xlsx); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectOfficePackage(xlsx, ".xlsm"); err == nil || !strings.Contains(err.Error(), "content type") {
		t.Fatalf("macro/content-type mismatch error = %v", err)
	}
	xlsmSource := createRealMacroPackage(t, ".xlsm", false)
	if _, err := inspectOfficePackage(xlsmSource, ".xlsx"); err == nil || !strings.Contains(err.Error(), "content type") {
		t.Fatalf("XLSM-as-XLSX content mismatch error = %v", err)
	}
	xltmSource := createRealMacroPackage(t, ".xltm", false)
	if _, err := inspectOfficePackage(xltmSource, ".xlsm"); err == nil || !strings.Contains(err.Error(), "content type") {
		t.Fatalf("XLTM-as-XLSM content mismatch error = %v", err)
	}
}

func TestOfficePackageValidationRejectsExternalRelationships(t *testing.T) {
	path := createRealXLSXPackage(t)
	book, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellHyperLink("Records", "B2", "https://example.invalid/product", "External"); err != nil {
		t.Fatal(err)
	}
	if err := book.Save(); err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectOfficePackage(path, ".xlsx"); err == nil || !strings.Contains(err.Error(), "forbidden external relationship") {
		t.Fatalf("external relationship error = %v", err)
	}
}

func TestCopyVerifiedBlankXLTMRequiresAndPreservesDataEmptyTemplate(t *testing.T) {
	source := createRealMacroPackage(t, ".xltm", false)
	output := filepath.Join(t.TempDir(), "blank.xltm")
	report, err := CopyVerifiedBlankXLTM(output, TrustedOfficeContract{TemplatePath: source, Target: "table:ExportProducts"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.DataEmpty || report.RecordCount != 0 || report.SourceSHA256 == "" || report.SourceSHA256 != report.OutputSHA256 {
		t.Fatalf("unexpected blank XLTM report: %+v", report)
	}
	badSource := createRealMacroPackage(t, ".xltm", true)
	badOutput := filepath.Join(t.TempDir(), "must-not-exist.xltm")
	if _, err := CopyVerifiedBlankXLTM(badOutput, TrustedOfficeContract{TemplatePath: badSource, Target: "table:ExportProducts"}); err == nil || !strings.Contains(err.Error(), "data-empty") {
		t.Fatalf("populated XLTM error = %v, want fail-closed data-empty error", err)
	}
	if _, err := os.Stat(badOutput); !os.IsNotExist(err) {
		t.Fatalf("rejected XLTM was finalized: %v", err)
	}
}

func TestCanonicalDigitalogicXLTMShipsNoStaticCatalogRecords(t *testing.T) {
	templatePath, err := filepath.Abs(filepath.Join(
		"..", "..", "docs", "examples", "لیست قیمت دیجیتالاجیک.xltm",
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"table:Products", "table:SyncData"} {
		output := filepath.Join(t.TempDir(), strings.TrimPrefix(target, "table:")+".xltm")
		report, err := CopyVerifiedBlankXLTM(output, TrustedOfficeContract{
			TemplatePath: templatePath,
			Target:       target,
		})
		if err != nil {
			t.Fatalf("canonical %s data-free gate failed: %v", target, err)
		}
		if !report.DataEmpty || report.RecordCount != 0 || report.SourceSHA256 != report.OutputSHA256 {
			t.Fatalf("canonical %s unexpectedly contains persisted records: %+v", target, report)
		}
	}
}

func TestExcelTemplateReleaseGuardsAndRuntimeRefreshContract(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	checks := []struct {
		path  string
		parts []string
	}{
		{
			path: filepath.Join(repoRoot, "scripts", "windows", "Build-ExcelDashboard.ps1"),
			parts: []string{
				"Test-ExcelTemplateDataFree.ps1",
				"$templateDataAuditPath -Path $OutputPath",
				"$templateDataAuditPath -Path $DistributionCopyPath",
			},
		},
		{
			path: filepath.Join(repoRoot, "scripts", "windows", "Update-ExcelWorkbookModules.ps1"),
			parts: []string{
				"$templateDataAuditPath -Path $resolvedInput",
				"$templateDataAuditPath -Path $resolvedOutput",
			},
		},
		{
			path: filepath.Join(repoRoot, "scripts", "windows", "Test-ExcelSearchProgressDelivery.ps1"),
			parts: []string{
				"$templateDataAuditPath -Path $CandidatePath",
			},
		},
		{
			path: filepath.Join(repoRoot, "scripts", "windows", "Validate-ExcelPriceCalculator.cjs"),
			parts: []string{
				"saving live rows into the release .xltm template is forbidden",
				"assertReleaseTemplateIsDataFree(options.candidate)",
				"$candidateBook.SaveAs([IO.Path]::GetFullPath($saveSyncedTo), 52)",
			},
		},
		{
			path: filepath.Join(repoRoot, "docs", "examples", "vba", "ThisWorkbook.cls"),
			parts: []string{
				"Private Sub Workbook_Open()",
				"ProductCatalogSync.ScheduleRefreshOnOpen",
			},
		},
		{
			path: filepath.Join(repoRoot, "docs", "examples", "vba", "ProductCatalogSync.bas"),
			parts: []string{
				"Public Sub RefreshAllData",
				"Private Sub CommitRefreshSnapshot",
				"ScheduleEventDrivenRefresh",
			},
		},
	}
	for _, check := range checks {
		source, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, part := range check.parts {
			if !strings.Contains(string(source), part) {
				t.Errorf("%s is missing fail-closed release guard %q", check.path, part)
			}
		}
	}
}
