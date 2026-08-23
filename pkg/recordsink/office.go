package recordsink

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

const (
	contentTypeXLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"
	contentTypeXLSM = "application/vnd.ms-excel.sheet.macroEnabled.main+xml"
	contentTypeXLTM = "application/vnd.ms-excel.template.macroEnabled.main+xml"
	contentTypeVBA  = "application/vnd.ms-office.vbaProject"
)

// TrustedOfficeContract is server-owned configuration. TemplatePath is never
// accepted from an HTTP request. Target must explicitly identify the one table
// or defined name that owns the export header and data rows:
// "table:ExportProducts" or "name:ExportProducts".
type TrustedOfficeContract struct {
	TemplatePath string
	Target       string
}

// OfficeArtifactReport is safe provenance for logs and HTTP response headers.
// It deliberately excludes the trusted filesystem path.
type OfficeArtifactReport struct {
	Format           string
	Target           string
	RecordCount      int
	DataEmpty        bool
	SourceSHA256     string
	OutputSHA256     string
	VBAProjectSHA256 string
	SheetCount       int
}

type officeTarget struct {
	Kind      string
	Name      string
	Sheet     string
	Range     string
	StartCol  int
	StartRow  int
	EndCol    int
	EndRow    int
	TablePart string
}

type officePackageSnapshot struct {
	format         string
	hash           string
	vbaHash        string
	contentTypes   map[string]string
	workbookRels   map[string]string
	richParts      map[string]string
	sheets         []string
	definedNames   map[string]string
	formulas       map[string]string
	packageEntries map[string]struct{}
}

type packageContentTypes struct {
	Defaults  []packageContentTypeDefault  `xml:"Default"`
	Overrides []packageContentTypeOverride `xml:"Override"`
}

type packageContentTypeDefault struct {
	Extension   string `xml:"Extension,attr"`
	ContentType string `xml:"ContentType,attr"`
}

type packageContentTypeOverride struct {
	PartName    string `xml:"PartName,attr"`
	ContentType string `xml:"ContentType,attr"`
}

type packageRelationships struct {
	Relationships []packageRelationship `xml:"Relationship"`
}

type packageFormula struct {
	Attributes []xml.Attr `xml:",any,attr"`
	Text       string     `xml:",chardata"`
}

type packageRelationship struct {
	ID         string `xml:"Id,attr"`
	Type       string `xml:"Type,attr"`
	Target     string `xml:"Target,attr"`
	TargetMode string `xml:"TargetMode,attr"`
}

// PopulateTrustedXLSX reuses a configured local macro-free workbook only when
// it exposes the explicit table or named-range population contract.
func PopulateTrustedXLSX(outputPath string, rows []map[string]interface{}, keyField string, xlsxOptions XLSXOptions, contract TrustedOfficeContract) (OfficeArtifactReport, error) {
	return populateTrustedOffice(outputPath, rows, keyField, xlsxOptions, contract, ".xlsx")
}

// PopulateTrustedXLSM copies a configured local XLSM to a private temporary
// package, writes the current rows into its explicit target, closes it, reopens
// and verifies it, then atomically publishes the already-verified temporary
// package at outputPath. outputPath must not already exist.
func PopulateTrustedXLSM(outputPath string, rows []map[string]interface{}, keyField string, xlsxOptions XLSXOptions, contract TrustedOfficeContract) (OfficeArtifactReport, error) {
	return populateTrustedOffice(outputPath, rows, keyField, xlsxOptions, contract, ".xlsm")
}

func populateTrustedOffice(outputPath string, rows []map[string]interface{}, keyField string, xlsxOptions XLSXOptions, contract TrustedOfficeContract, extension string) (OfficeArtifactReport, error) {
	var report OfficeArtifactReport
	format := strings.TrimPrefix(extension, ".")
	formatLabel := strings.ToUpper(format)
	templatePath, err := validateTrustedOfficePath(contract.TemplatePath, extension)
	if err != nil {
		return report, err
	}
	if err := validateFreshOfficeOutput(outputPath, extension); err != nil {
		return report, err
	}
	targetKind, targetName, err := parseOfficeTargetSpec(contract.Target)
	if err != nil {
		return report, err
	}
	source, err := inspectOfficePackage(templatePath, extension)
	if err != nil {
		return report, fmt.Errorf("trusted %s package rejected: %w", formatLabel, err)
	}
	if _, err := uniqueExpectedProductCodes(rows, keyField); err != nil {
		return report, err
	}

	workPath, err := freshSiblingOfficePath(outputPath, extension)
	if err != nil {
		return report, err
	}
	defer os.Remove(workPath)
	if err := copyLocalFile(templatePath, workPath); err != nil {
		return report, fmt.Errorf("copy trusted %s to temporary package: %w", formatLabel, err)
	}

	book, err := excelize.OpenFile(workPath)
	if err != nil {
		return report, fmt.Errorf("open temporary %s package: %w", formatLabel, err)
	}
	target, err := resolveOfficeTarget(book, targetKind, targetName)
	if err == nil {
		err = populateOfficeTarget(book, target, rows, keyField, normalizeXLSXOptions([]XLSXOptions{xlsxOptions}))
	}
	if err == nil && target.Kind == "name" {
		err = resizeDefinedName(book, target, len(rows))
	}
	if err == nil {
		err = book.Save()
	}
	closeErr := book.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return report, fmt.Errorf("populate configured %s target: %w", formatLabel, err)
	}
	if target.Kind == "table" {
		if err := resizePackageTable(workPath, target.Name, populatedTargetRange(target, len(rows))); err != nil {
			return report, fmt.Errorf("resize configured %s table: %w", formatLabel, err)
		}
	}

	outputSnapshot, err := inspectOfficePackage(workPath, extension)
	if err != nil {
		return report, fmt.Errorf("reopen populated %s package: %w", formatLabel, err)
	}
	if err := verifyPreservedOfficeStructure(source, outputSnapshot, target); err != nil {
		return report, fmt.Errorf("verify populated %s structure: %w", formatLabel, err)
	}
	if err := verifyPopulatedOfficeTarget(workPath, contract.Target, rows, keyField, normalizeXLSXOptions([]XLSXOptions{xlsxOptions})); err != nil {
		return report, fmt.Errorf("verify populated %s records: %w", formatLabel, err)
	}
	if source.hash == outputSnapshot.hash {
		return report, fmt.Errorf("populated %s output hash unexpectedly equals its source hash", formatLabel)
	}
	if err := os.Rename(workPath, outputPath); err != nil {
		return report, fmt.Errorf("finalize verified %s package: %w", formatLabel, err)
	}
	report = OfficeArtifactReport{
		Format:           format,
		Target:           contract.Target,
		RecordCount:      len(rows),
		SourceSHA256:     source.hash,
		OutputSHA256:     outputSnapshot.hash,
		VBAProjectSHA256: outputSnapshot.vbaHash,
		SheetCount:       len(outputSnapshot.sheets),
	}
	return report, nil
}

// CopyVerifiedBlankXLTM permits only an already-empty, configured macro
// template. The target must contain headers and zero nonblank product rows.
// No API in this package writes records to XLTM.
func CopyVerifiedBlankXLTM(outputPath string, contract TrustedOfficeContract) (OfficeArtifactReport, error) {
	var report OfficeArtifactReport
	templatePath, err := validateTrustedOfficePath(contract.TemplatePath, ".xltm")
	if err != nil {
		return report, err
	}
	if err := validateFreshOfficeOutput(outputPath, ".xltm"); err != nil {
		return report, err
	}
	targetKind, targetName, err := parseOfficeTargetSpec(contract.Target)
	if err != nil {
		return report, err
	}
	source, err := inspectOfficePackage(templatePath, ".xltm")
	if err != nil {
		return report, fmt.Errorf("trusted XLTM package rejected: %w", err)
	}
	if err := verifyBlankOfficeTarget(templatePath, targetKind, targetName); err != nil {
		return report, fmt.Errorf("trusted XLTM must remain data-empty: %w", err)
	}
	workPath, err := freshSiblingOfficePath(outputPath, ".xltm")
	if err != nil {
		return report, err
	}
	defer os.Remove(workPath)
	if err := copyLocalFile(templatePath, workPath); err != nil {
		return report, fmt.Errorf("copy trusted XLTM to temporary package: %w", err)
	}
	outputSnapshot, err := inspectOfficePackage(workPath, ".xltm")
	if err != nil {
		return report, fmt.Errorf("reopen copied XLTM package: %w", err)
	}
	if err := verifyBlankOfficeTarget(workPath, targetKind, targetName); err != nil {
		return report, fmt.Errorf("copied XLTM is not data-empty: %w", err)
	}
	if source.hash != outputSnapshot.hash {
		return report, errors.New("blank XLTM copy changed the trusted source package")
	}
	if err := os.Rename(workPath, outputPath); err != nil {
		return report, fmt.Errorf("finalize verified blank XLTM package: %w", err)
	}
	report = OfficeArtifactReport{
		Format:           "xltm",
		Target:           contract.Target,
		RecordCount:      0,
		DataEmpty:        true,
		SourceSHA256:     source.hash,
		OutputSHA256:     outputSnapshot.hash,
		VBAProjectSHA256: outputSnapshot.vbaHash,
		SheetCount:       len(outputSnapshot.sheets),
	}
	return report, nil
}

func validateTrustedOfficePath(value, extension string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("trusted Office template is not configured")
	}
	if strings.Contains(value, "://") || strings.HasPrefix(strings.ToLower(value), "file:") || strings.HasPrefix(value, `\\`) {
		return "", errors.New("trusted Office template must be an allowlisted local file")
	}
	if !filepath.IsAbs(value) || !strings.EqualFold(filepath.Ext(value), extension) {
		return "", fmt.Errorf("trusted Office template must be an absolute local %s file", strings.ToUpper(extension))
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(value))
	if err != nil {
		return "", errors.New("trusted Office template is unavailable")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("trusted Office template is unavailable")
	}
	return resolved, nil
}

func validateFreshOfficeOutput(outputPath, extension string) error {
	if !filepath.IsAbs(outputPath) || !strings.EqualFold(filepath.Ext(outputPath), extension) {
		return fmt.Errorf("Office output must be a fresh absolute %s path", strings.ToUpper(extension))
	}
	if _, err := os.Stat(outputPath); err == nil {
		return errors.New("Office output already exists; refusing a non-atomic replacement")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Office output: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("create Office output directory: %w", err)
	}
	return nil
}

func freshSiblingOfficePath(outputPath, extension string) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(outputPath), ".patris-office-*.partial"+extension)
	if err != nil {
		return "", fmt.Errorf("create temporary Office package: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func copyLocalFile(sourcePath, outputPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, source)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func parseOfficeTargetSpec(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	kind, name, found := strings.Cut(value, ":")
	kind = strings.ToLower(strings.TrimSpace(kind))
	name = strings.TrimSpace(name)
	if !found || (kind != "table" && kind != "name") || name == "" {
		return "", "", errors.New(`Office target must be explicit: "table:Name" or "name:Name"`)
	}
	return kind, name, nil
}

func resolveOfficeTarget(book *excelize.File, kind, name string) (officeTarget, error) {
	if kind == "table" {
		for _, sheet := range book.GetSheetList() {
			tables, err := book.GetTables(sheet)
			if err != nil {
				return officeTarget{}, err
			}
			for _, table := range tables {
				if table.Name == name {
					return newOfficeTarget(kind, name, sheet, table.Range)
				}
			}
		}
		return officeTarget{}, fmt.Errorf("configured Office table %q does not exist", name)
	}
	for _, definedName := range book.GetDefinedName() {
		if definedName.Name != name || !isWorkbookDefinedNameScope(definedName.Scope) {
			continue
		}
		sheet, reference, err := parseDefinedNameReference(definedName.RefersTo)
		if err != nil {
			return officeTarget{}, fmt.Errorf("configured defined name %q is not a local rectangular range", name)
		}
		return newOfficeTarget(kind, name, sheet, reference)
	}
	return officeTarget{}, fmt.Errorf("configured Office defined name %q does not exist", name)
}

func newOfficeTarget(kind, name, sheet, reference string) (officeTarget, error) {
	parts := strings.Split(strings.ReplaceAll(reference, "$", ""), ":")
	if len(parts) != 2 {
		return officeTarget{}, errors.New("configured Office target must include one header row and one data row")
	}
	startCol, startRow, err := excelize.CellNameToCoordinates(parts[0])
	if err != nil {
		return officeTarget{}, err
	}
	endCol, endRow, err := excelize.CellNameToCoordinates(parts[1])
	if err != nil {
		return officeTarget{}, err
	}
	if startCol > endCol || startRow >= endRow {
		return officeTarget{}, errors.New("configured Office target must include one header row and one data row")
	}
	return officeTarget{Kind: kind, Name: name, Sheet: sheet, Range: reference, StartCol: startCol, StartRow: startRow, EndCol: endCol, EndRow: endRow}, nil
}

func parseDefinedNameReference(value string) (string, string, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "="))
	separator := strings.LastIndex(value, "!")
	if separator <= 0 || separator == len(value)-1 || strings.Contains(value, "[") {
		return "", "", errors.New("invalid local defined name")
	}
	sheet := strings.TrimSpace(value[:separator])
	if strings.HasPrefix(sheet, "'") && strings.HasSuffix(sheet, "'") {
		sheet = strings.ReplaceAll(sheet[1:len(sheet)-1], "''", "'")
	}
	reference := strings.TrimSpace(value[separator+1:])
	if sheet == "" || reference == "" {
		return "", "", errors.New("invalid local defined name")
	}
	return sheet, reference, nil
}

func populateOfficeTarget(book *excelize.File, target officeTarget, rows []map[string]interface{}, keyField string, options XLSXOptions) error {
	headers, fields, keyColumn, err := resolveOfficeColumns(book, target, rows, keyField, options)
	if err != nil {
		return err
	}
	_ = headers
	maxDataRows := max(target.EndRow-target.StartRow, len(rows))
	emptyRow := make([]interface{}, len(fields))
	for rowOffset := 1; rowOffset <= maxDataRows; rowOffset++ {
		startCell, _ := excelize.CoordinatesToCellName(target.StartCol, target.StartRow+rowOffset)
		if err := book.SetSheetRow(target.Sheet, startCell, &emptyRow); err != nil {
			return err
		}
	}
	for rowIndex, row := range rows {
		output := make([]interface{}, len(fields))
		for columnIndex, field := range fields {
			value := row[field]
			if isHumanProductNameField(field) {
				if text, ok := value.(string); ok {
					value = normalizeHumanProductName(text)
				}
			}
			output[columnIndex] = officeBatchValue(value)
		}
		startCell, _ := excelize.CoordinatesToCellName(target.StartCol, target.StartRow+rowIndex+1)
		if err := book.SetSheetRow(target.Sheet, startCell, &output); err != nil {
			return err
		}
		keyCell, _ := excelize.CoordinatesToCellName(target.StartCol+keyColumn, target.StartRow+rowIndex+1)
		if err := book.SetCellStr(target.Sheet, keyCell, cell(row[keyField])); err != nil {
			return err
		}
	}
	if len(rows) > 0 {
		for column := target.StartCol; column <= target.EndCol; column++ {
			seed, _ := excelize.CoordinatesToCellName(column, target.StartRow+1)
			styleID, err := book.GetCellStyle(target.Sheet, seed)
			if err != nil {
				return err
			}
			start, _ := excelize.CoordinatesToCellName(column, target.StartRow+1)
			end, _ := excelize.CoordinatesToCellName(column, target.StartRow+len(rows))
			if err := book.SetCellStyle(target.Sheet, start, end, styleID); err != nil {
				return err
			}
		}
	}
	return nil
}

func officeBatchValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case nil, string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return typed
	case json.Number:
		if number, exact := exactExcelFloat(typed.String()); exact {
			return number
		}
		return typed.String()
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	case []byte:
		return string(typed)
	default:
		return cell(value)
	}
}

func expectedOfficeRawValue(value interface{}) string {
	switch typed := officeBatchValue(value).(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "1"
		}
		return "0"
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return fmt.Sprint(typed)
	}
}

func resolveOfficeColumns(book *excelize.File, target officeTarget, rows []map[string]interface{}, keyField string, options XLSXOptions) ([]string, []string, int, error) {
	headers := make([]string, 0, target.EndCol-target.StartCol+1)
	fields := make([]string, 0, cap(headers))
	seenHeaders := make(map[string]struct{}, cap(headers))
	availableFields := make(map[string]string)
	for _, row := range rows {
		for field := range row {
			availableFields[strings.ToLower(strings.TrimSpace(field))] = field
		}
	}
	if _, exists := availableFields[strings.ToLower(strings.TrimSpace(keyField))]; !exists && len(rows) > 0 {
		return nil, nil, -1, fmt.Errorf("current records do not contain key field %q", keyField)
	}
	keyColumn := -1
	for column := target.StartCol; column <= target.EndCol; column++ {
		cellName, _ := excelize.CoordinatesToCellName(column, target.StartRow)
		header, err := book.GetCellValue(target.Sheet, cellName, excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, nil, -1, err
		}
		header = strings.TrimSpace(header)
		folded := strings.ToLower(header)
		if header == "" {
			return nil, nil, -1, fmt.Errorf("configured Office target has a blank header at %s", cellName)
		}
		if _, exists := seenHeaders[folded]; exists {
			return nil, nil, -1, fmt.Errorf("configured Office target has duplicate header %q", header)
		}
		seenHeaders[folded] = struct{}{}
		field := officeFieldForHeader(header, availableFields, keyField, options)
		if field == "" && len(rows) == 0 {
			// With an empty current snapshot there are no row keys from which to
			// discover arbitrary fields. Preserve the configured header as its
			// inert mapping so the old target rows can still be cleared exactly.
			field = header
		}
		if field == "" {
			return nil, nil, -1, fmt.Errorf("configured Office header %q has no current-record field mapping", header)
		}
		if strings.EqualFold(field, keyField) {
			keyColumn = column - target.StartCol
		}
		headers = append(headers, header)
		fields = append(fields, field)
	}
	if keyColumn < 0 {
		return nil, nil, -1, fmt.Errorf("configured Office target does not contain key field %q", keyField)
	}
	return headers, fields, keyColumn, nil
}

func officeFieldForHeader(header string, available map[string]string, keyField string, options XLSXOptions) string {
	if field := available[strings.ToLower(strings.TrimSpace(header))]; field != "" {
		return field
	}
	for _, field := range available {
		if strings.EqualFold(header, xlsxHeaderLabel(field, "", "en", options.ColumnLabels)) ||
			strings.EqualFold(header, xlsxHeaderLabel(field, "", "fa", options.ColumnLabels)) {
			return field
		}
	}
	switch strings.ToLower(strings.TrimSpace(header)) {
	case "code", "product code", "product_code", strings.ToLower("کد کالا"):
		if len(available) == 0 {
			return keyField
		}
		return available[strings.ToLower(keyField)]
	case "name", "product name", "product_name", strings.ToLower("نام کالا"):
		if len(available) == 0 {
			return "name"
		}
		return available["name"]
	}
	return ""
}

func resizeDefinedName(book *excelize.File, target officeTarget, rows int) error {
	endRow := target.StartRow + max(rows, 1)
	start, _ := excelize.CoordinatesToCellName(target.StartCol, target.StartRow, true)
	end, _ := excelize.CoordinatesToCellName(target.EndCol, endRow, true)
	sheet := strings.ReplaceAll(target.Sheet, "'", "''")
	if err := book.DeleteDefinedName(&excelize.DefinedName{Name: target.Name}); err != nil {
		return err
	}
	return book.SetDefinedName(&excelize.DefinedName{Name: target.Name, RefersTo: fmt.Sprintf("'%s'!%s:%s", sheet, start, end)})
}

func populatedTargetRange(target officeTarget, rows int) string {
	endRow := target.StartRow + max(rows, 1)
	start, _ := excelize.CoordinatesToCellName(target.StartCol, target.StartRow)
	end, _ := excelize.CoordinatesToCellName(target.EndCol, endRow)
	return start + ":" + end
}

func uniqueExpectedProductCodes(rows []map[string]interface{}, keyField string) ([]string, error) {
	codes := make([]string, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for index, row := range rows {
		code := cell(row[keyField])
		if code == "" {
			return nil, fmt.Errorf("current record %d has a blank product code", index+1)
		}
		if _, exists := seen[code]; exists {
			return nil, fmt.Errorf("current records contain duplicate product code %q", code)
		}
		seen[code] = struct{}{}
		codes[index] = code
	}
	return codes, nil
}

func verifyPopulatedOfficeTarget(path, targetSpec string, rows []map[string]interface{}, keyField string, options XLSXOptions) error {
	want, err := uniqueExpectedProductCodes(rows, keyField)
	if err != nil {
		return err
	}
	kind, name, err := parseOfficeTargetSpec(targetSpec)
	if err != nil {
		return err
	}
	book, err := excelize.OpenFile(path)
	if err != nil {
		return err
	}
	defer book.Close()
	target, err := resolveOfficeTarget(book, kind, name)
	if err != nil {
		return err
	}
	_, fields, keyColumn, err := resolveOfficeColumns(book, target, rows, keyField, options)
	if err != nil {
		return err
	}
	_ = fields
	for rowIndex, row := range rows {
		for columnIndex, field := range fields {
			cellName, _ := excelize.CoordinatesToCellName(target.StartCol+columnIndex, target.StartRow+rowIndex+1)
			got, err := book.GetCellValue(target.Sheet, cellName, excelize.Options{RawCellValue: true})
			if err != nil {
				return err
			}
			value := row[field]
			if isHumanProductNameField(field) {
				if displayName, ok := value.(string); ok {
					value = normalizeHumanProductName(displayName)
				}
			}
			want := expectedOfficeRawValue(value)
			if got != want {
				return fmt.Errorf("populated XLSM value at %s!%s is %q, want %q for field %q", target.Sheet, cellName, got, want, field)
			}
		}
	}
	got := make([]string, 0, len(want))
	seen := make(map[string]struct{}, len(want))
	for row := target.StartRow + 1; row <= target.EndRow; row++ {
		cellName, _ := excelize.CoordinatesToCellName(target.StartCol+keyColumn, row)
		code, err := book.GetCellValue(target.Sheet, cellName, excelize.Options{RawCellValue: true})
		if err != nil {
			return err
		}
		if code == "" {
			continue
		}
		if _, exists := seen[code]; exists {
			return fmt.Errorf("populated XLSM contains duplicate product code %q", code)
		}
		seen[code] = struct{}{}
		got = append(got, code)
	}
	if len(got) != len(want) {
		return fmt.Errorf("populated XLSM record count is %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			return fmt.Errorf("populated XLSM product code %d is %q, want %q", index+1, got[index], want[index])
		}
	}
	return nil
}

func verifyBlankOfficeTarget(path, kind, name string) error {
	book, err := excelize.OpenFile(path)
	if err != nil {
		return err
	}
	defer book.Close()
	target, err := resolveOfficeTarget(book, kind, name)
	if err != nil {
		return err
	}
	for row := target.StartRow + 1; row <= target.EndRow; row++ {
		for column := target.StartCol; column <= target.EndCol; column++ {
			cellName, _ := excelize.CoordinatesToCellName(column, row)
			value, err := book.GetCellValue(target.Sheet, cellName, excelize.Options{RawCellValue: true})
			if err != nil {
				return err
			}
			formula, err := book.GetCellFormula(target.Sheet, cellName)
			if err != nil {
				return err
			}
			if value != "" || formula != "" {
				return fmt.Errorf("configured XLTM target contains initial product data at %s!%s", target.Sheet, cellName)
			}
		}
	}
	return nil
}

func inspectOfficePackage(path, extension string) (officePackageSnapshot, error) {
	var snapshot officePackageSnapshot
	archive, err := zip.OpenReader(path)
	if err != nil {
		return snapshot, errors.New("file is not a valid Office ZIP package")
	}
	defer archive.Close()
	parts := make(map[string][]byte, len(archive.File))
	snapshot.packageEntries = make(map[string]struct{}, len(archive.File))
	for _, entry := range archive.File {
		if _, duplicate := parts[entry.Name]; duplicate {
			return snapshot, fmt.Errorf("Office package contains duplicate part %q", entry.Name)
		}
		reader, err := entry.Open()
		if err != nil {
			return snapshot, err
		}
		content, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			return snapshot, readErr
		}
		if closeErr != nil {
			return snapshot, closeErr
		}
		parts[entry.Name] = content
		snapshot.packageEntries[entry.Name] = struct{}{}
	}
	contentTypeData, exists := parts["[Content_Types].xml"]
	if !exists {
		return snapshot, errors.New("Office package is missing [Content_Types].xml")
	}
	var contentTypes packageContentTypes
	if err := xml.Unmarshal(contentTypeData, &contentTypes); err != nil {
		return snapshot, fmt.Errorf("parse Office content types: %w", err)
	}
	snapshot.contentTypes = make(map[string]string, len(contentTypes.Defaults)+len(contentTypes.Overrides))
	for _, item := range contentTypes.Defaults {
		snapshot.contentTypes["default:"+strings.ToLower(item.Extension)] = item.ContentType
	}
	for _, item := range contentTypes.Overrides {
		snapshot.contentTypes["override:"+item.PartName] = item.ContentType
	}
	wantMain := contentTypeXLSM
	switch strings.ToLower(extension) {
	case ".xlsx":
		wantMain = contentTypeXLSX
	case ".xltm":
		wantMain = contentTypeXLTM
	}
	if snapshot.contentTypes["override:/xl/workbook.xml"] != wantMain {
		return snapshot, fmt.Errorf("Office package main content type does not match %s", strings.ToUpper(extension))
	}
	macroEnabled := !strings.EqualFold(extension, ".xlsx")
	vbaProject, hasVBAProject := parts["xl/vbaProject.bin"]
	hasVBAOverride := snapshot.contentTypes["override:/xl/vbaProject.bin"] == contentTypeVBA
	hasVBAContentType := hasVBAOverride || snapshot.contentTypes["default:bin"] == contentTypeVBA
	if macroEnabled {
		if !hasVBAContentType {
			return snapshot, errors.New("macro-enabled Office package is missing the VBA content type")
		}
		if !hasVBAProject || len(vbaProject) == 0 {
			return snapshot, errors.New("macro-enabled Office package is missing vbaProject.bin")
		}
		snapshot.vbaHash = bytesSHA256(vbaProject)
	} else if hasVBAOverride || hasVBAProject {
		return snapshot, errors.New("macro-free XLSX package contains forbidden VBA content")
	}

	relsData, exists := parts["xl/_rels/workbook.xml.rels"]
	if !exists {
		return snapshot, errors.New("Office package is missing workbook relationships")
	}
	var relationships packageRelationships
	if err := xml.Unmarshal(relsData, &relationships); err != nil {
		return snapshot, fmt.Errorf("parse workbook relationships: %w", err)
	}
	snapshot.workbookRels = make(map[string]string, len(relationships.Relationships))
	hasVBARelationship := false
	for _, relationship := range relationships.Relationships {
		snapshot.workbookRels[relationship.ID] = relationship.Type + "|" + relationship.Target + "|" + relationship.TargetMode
		target := strings.ReplaceAll(strings.TrimSpace(relationship.Target), "\\", "/")
		if strings.HasSuffix(relationship.Type, "/vbaProject") && strings.EqualFold(filepath.Base(target), "vbaProject.bin") {
			hasVBARelationship = true
		}
	}
	if macroEnabled && !hasVBARelationship {
		return snapshot, errors.New("macro-enabled Office package is missing the VBA workbook relationship")
	}
	if !macroEnabled && hasVBARelationship {
		return snapshot, errors.New("macro-free XLSX package contains a forbidden VBA workbook relationship")
	}
	for partName, content := range parts {
		if partName == "xl/connections.xml" || strings.HasPrefix(partName, "xl/externalLinks/") {
			return snapshot, fmt.Errorf("Office package contains forbidden external connection part %q", partName)
		}
		if strings.HasSuffix(partName, ".rels") {
			var rels packageRelationships
			if err := xml.Unmarshal(content, &rels); err != nil {
				return snapshot, fmt.Errorf("parse relationships part %q: %w", partName, err)
			}
			for _, relationship := range rels.Relationships {
				if strings.EqualFold(relationship.TargetMode, "External") {
					return snapshot, fmt.Errorf("Office package contains forbidden external relationship in %q", partName)
				}
			}
		}
	}
	snapshot.richParts = make(map[string]string)
	for partName, content := range parts {
		if isPreservedRichOfficePart(partName) {
			snapshot.richParts[partName] = bytesSHA256(content)
		}
	}

	book, err := excelize.OpenFile(path)
	if err != nil {
		return snapshot, fmt.Errorf("open Office workbook: %w", err)
	}
	defer book.Close()
	snapshot.sheets = append([]string(nil), book.GetSheetList()...)
	snapshot.definedNames = make(map[string]string)
	for _, name := range book.GetDefinedName() {
		scope := strings.TrimSpace(name.Scope)
		if isWorkbookDefinedNameScope(scope) {
			scope = ""
		}
		snapshot.definedNames[scope+"|"+name.Name] = name.RefersTo
	}
	snapshot.formulas, err = packageFormulaSnapshot(parts)
	if err != nil {
		return snapshot, err
	}
	snapshot.hash, err = fileSHA256(path)
	if err != nil {
		return snapshot, err
	}
	snapshot.format = strings.TrimPrefix(strings.ToLower(extension), ".")
	return snapshot, nil
}

func isWorkbookDefinedNameScope(scope string) bool {
	scope = strings.TrimSpace(scope)
	return scope == "" || strings.EqualFold(scope, "Workbook")
}

func packageFormulaSnapshot(parts map[string][]byte) (map[string]string, error) {
	formulas := make(map[string]string)
	for partName, content := range parts {
		if !strings.HasPrefix(partName, "xl/worksheets/") || !strings.HasSuffix(partName, ".xml") {
			continue
		}
		decoder := xml.NewDecoder(bytes.NewReader(content))
		cellReference := ""
		for {
			token, err := decoder.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("parse worksheet formulas in %q: %w", partName, err)
			}
			switch typed := token.(type) {
			case xml.StartElement:
				switch typed.Name.Local {
				case "c":
					cellReference = ""
					for _, attribute := range typed.Attr {
						if attribute.Name.Local == "r" {
							cellReference = attribute.Value
						}
					}
				case "f":
					if cellReference == "" {
						return nil, fmt.Errorf("worksheet formula in %q has no cell reference", partName)
					}
					var formula packageFormula
					if err := decoder.DecodeElement(&formula, &typed); err != nil {
						return nil, fmt.Errorf("parse worksheet formula in %q: %w", partName, err)
					}
					attributes := make([]string, 0, len(formula.Attributes))
					for _, attribute := range formula.Attributes {
						attributes = append(attributes, attribute.Name.Space+":"+attribute.Name.Local+"="+attribute.Value)
					}
					sort.Strings(attributes)
					formulas[partName+"!"+cellReference] = strings.Join(attributes, "|") + "|" + formula.Text
				}
			case xml.EndElement:
				if typed.Name.Local == "c" {
					cellReference = ""
				}
			}
		}
	}
	return formulas, nil
}

func isPreservedRichOfficePart(name string) bool {
	for _, prefix := range []string{
		"xl/drawings/", "xl/charts/", "xl/media/", "xl/ctrlProps/",
		"xl/activeX/", "xl/comments", "xl/threadedComments/", "xl/persons/",
		"customXml/",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func verifyPreservedOfficeStructure(source, output officePackageSnapshot, target officeTarget) error {
	if source.format != output.format || source.vbaHash != output.vbaHash {
		return errors.New("Office package type or VBA project changed")
	}
	if !stringMapEqual(source.contentTypes, output.contentTypes) {
		return errors.New("Office content types changed during population")
	}
	if !stringMapEqual(source.workbookRels, output.workbookRels) {
		return errors.New("workbook relationships changed during population")
	}
	if !stringMapEqual(source.richParts, output.richParts) {
		return errors.New("drawings, charts, media, controls, or custom XML changed during population")
	}
	if !stringSetEqual(source.packageEntries, output.packageEntries) {
		return errors.New("Office package part inventory changed during population")
	}
	if !stringSliceEqual(source.sheets, output.sheets) {
		return errors.New("workbook sheet structure changed during population")
	}
	if !stringMapEqual(source.formulas, output.formulas) {
		return errors.New("workbook formulas changed during population")
	}
	if target.Kind == "table" {
		if !stringMapEqual(source.definedNames, output.definedNames) {
			return errors.New("defined names changed during table-based population")
		}
	} else {
		for key, value := range source.definedNames {
			if key == "|"+target.Name {
				continue
			}
			if output.definedNames[key] != value {
				return fmt.Errorf("defined name %q changed during population", key)
			}
		}
	}
	return nil
}

func resizePackageTable(path, tableName, newRange string) error {
	replacements, matched, err := func() (map[string][]byte, int, error) {
		archive, err := zip.OpenReader(path)
		if err != nil {
			return nil, 0, err
		}
		defer archive.Close()
		replacements := make(map[string][]byte)
		matched := 0
		for _, entry := range archive.File {
			if !strings.HasPrefix(entry.Name, "xl/tables/") || !strings.HasSuffix(entry.Name, ".xml") {
				continue
			}
			reader, err := entry.Open()
			if err != nil {
				return nil, 0, err
			}
			content, readErr := io.ReadAll(reader)
			_ = reader.Close()
			if readErr != nil {
				return nil, 0, readErr
			}
			name, ok := officeTableAttribute(content, "name")
			if !ok || name != tableName {
				continue
			}
			updated, err := replaceOfficeTableRange(content, newRange)
			if err != nil {
				return nil, 0, err
			}
			replacements[entry.Name] = updated
			matched++
		}
		return replacements, matched, nil
	}()
	if err != nil {
		return err
	}
	if matched != 1 {
		return fmt.Errorf("configured Office table %q resolved to %d package parts", tableName, matched)
	}
	return rewriteZipParts(path, replacements)
}

var officeTableStartPattern = regexp.MustCompile(`(?s)<(?:[A-Za-z0-9_]+:)?table\b[^>]*>`)

func officeTableAttribute(content []byte, attribute string) (string, bool) {
	start := officeTableStartPattern.Find(content)
	if len(start) == 0 {
		return "", false
	}
	pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(attribute) + `="([^"]*)"`)
	match := pattern.FindSubmatch(start)
	if len(match) != 2 {
		return "", false
	}
	return string(match[1]), true
}

func replaceOfficeTableRange(content []byte, newRange string) ([]byte, error) {
	if strings.ContainsAny(newRange, `"<>`) {
		return nil, errors.New("invalid Office table range")
	}
	result := append([]byte(nil), content...)
	for _, element := range []string{"table", "autoFilter"} {
		startPattern := regexp.MustCompile(`(?s)<(?:[A-Za-z0-9_]+:)?` + element + `\b[^>]*>`)
		location := startPattern.FindIndex(result)
		if location == nil {
			if element == "table" {
				return nil, errors.New("Office table XML is missing its table element")
			}
			continue
		}
		start := result[location[0]:location[1]]
		refPattern := regexp.MustCompile(`\bref="[^"]*"`)
		refLocation := refPattern.FindIndex(start)
		if refLocation == nil {
			return nil, fmt.Errorf("Office %s XML is missing its range", element)
		}
		replacement := []byte(`ref="` + newRange + `"`)
		updatedStart := make([]byte, 0, len(start)-refLocation[1]+refLocation[0]+len(replacement))
		updatedStart = append(updatedStart, start[:refLocation[0]]...)
		updatedStart = append(updatedStart, replacement...)
		updatedStart = append(updatedStart, start[refLocation[1]:]...)
		updated := make([]byte, 0, len(result)-len(start)+len(updatedStart))
		updated = append(updated, result[:location[0]]...)
		updated = append(updated, updatedStart...)
		updated = append(updated, result[location[1]:]...)
		result = updated
	}
	return result, nil
}

func rewriteZipParts(path string, replacements map[string][]byte) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".patris-zip-*.tmp")
	if err != nil {
		_ = archive.Close()
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	writer := zip.NewWriter(temporary)
	for _, entry := range archive.File {
		header := entry.FileHeader
		partWriter, err := writer.CreateHeader(&header)
		if err != nil {
			_ = writer.Close()
			_ = temporary.Close()
			_ = archive.Close()
			return err
		}
		if replacement, exists := replacements[entry.Name]; exists {
			_, err = partWriter.Write(replacement)
		} else {
			reader, openErr := entry.Open()
			if openErr != nil {
				err = openErr
			} else {
				_, err = io.Copy(partWriter, reader)
				_ = reader.Close()
			}
		}
		if err != nil {
			_ = writer.Close()
			_ = temporary.Close()
			_ = archive.Close()
			return err
		}
	}
	if err := writer.Close(); err != nil {
		_ = temporary.Close()
		_ = archive.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = archive.Close()
		return err
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func bytesSHA256(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func stringMapEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func stringSliceEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func stringSetEqual(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if _, exists := right[key]; !exists {
			return false
		}
	}
	return true
}
