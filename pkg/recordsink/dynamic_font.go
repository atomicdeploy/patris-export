package recordsink

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

const defaultDynamicPriceDigitFont = "Yekan Bakh FaNum"

// DynamicWorkbookFontPolicy is the fixed role-map policy used by the Persian
// calculator. Cell roles are verified through their saved OpenXML style IDs;
// DrawingML text runs are verified through their explicit latin/ea/cs slots.
type DynamicWorkbookFontPolicy struct {
	PersianFont          string
	LatinFont            string
	PriceDisplayFaNum    bool
	HasPriceDisplayFaNum bool
}

// DynamicWorkbookFontReport reports only counts and configured family names;
// it contains no workbook values or filesystem paths.
type DynamicWorkbookFontReport struct {
	PersianFont      string
	LatinFont        string
	PriceDisplayFont string
	MappedCells      int
	DrawingTextRuns  int
	DrawingFontSlots int
}

type openXMLStyles struct {
	Fonts struct {
		Fonts []openXMLFont `xml:"font"`
	} `xml:"fonts"`
	CellXfs struct {
		XFs []openXMLXF `xml:"xf"`
	} `xml:"cellXfs"`
}

type openXMLFont struct {
	Name   *openXMLVal `xml:"name"`
	Scheme *openXMLVal `xml:"scheme"`
}

type openXMLVal struct {
	Value string `xml:"val,attr"`
}

type openXMLXF struct {
	FontID int `xml:"fontId,attr"`
}

type drawingRunAudit struct {
	text  string
	lang  string
	slots map[string]string
}

// ReadDynamicWorkbookFontPolicy reads the configured font roles and optional
// selling-price FaNum toggle from workbook-scoped named cells. It does not
// infer any role from cell text.
func ReadDynamicWorkbookFontPolicy(path string) (DynamicWorkbookFontPolicy, error) {
	var policy DynamicWorkbookFontPolicy
	book, err := excelize.OpenFile(path)
	if err != nil {
		return policy, fmt.Errorf("open workbook font configuration: %w", err)
	}
	defer book.Close()
	values := make(map[string]string, 3)
	for _, definedName := range book.GetDefinedName() {
		if !isWorkbookDefinedNameScope(definedName.Scope) ||
			(definedName.Name != "PersianFont" && definedName.Name != "LatinFont" && definedName.Name != "PriceDisplayFaNum") {
			continue
		}
		sheet, cellRange, err := parseDefinedNameReference(definedName.RefersTo)
		if err != nil {
			return policy, fmt.Errorf("font configuration name %q is not a local cell", definedName.Name)
		}
		parts := strings.Split(strings.ReplaceAll(cellRange, "$", ""), ":")
		if len(parts) != 1 {
			return policy, fmt.Errorf("font configuration name %q must refer to one cell", definedName.Name)
		}
		value, err := book.GetCellValue(sheet, parts[0], excelize.Options{RawCellValue: true})
		if err != nil {
			return policy, fmt.Errorf("read font configuration name %q: %w", definedName.Name, err)
		}
		values[definedName.Name] = strings.TrimSpace(value)
	}
	policy.PersianFont = values["PersianFont"]
	policy.LatinFont = values["LatinFont"]
	if policy.PersianFont == "" || policy.LatinFont == "" {
		return policy, errors.New("workbook font configuration requires PersianFont and LatinFont named cells")
	}
	if value, ok := values["PriceDisplayFaNum"]; ok {
		enabled, err := parseWorkbookYesNo(value)
		if err != nil {
			return policy, fmt.Errorf("font configuration name %q: %w", "PriceDisplayFaNum", err)
		}
		policy.HasPriceDisplayFaNum = true
		policy.PriceDisplayFaNum = enabled
	}
	return policy, nil
}

func parseWorkbookYesNo(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "بله":
		return true, nil
	case "no", "خیر":
		return false, nil
	default:
		return false, fmt.Errorf("value %q must be localized Yes or No", value)
	}
}

// ValidateDynamicWorkbookFontPolicy is a hard package validator for the fixed
// calculator role map. It never guesses a role from cell contents.
func ValidateDynamicWorkbookFontPolicy(path string, policy DynamicWorkbookFontPolicy) (DynamicWorkbookFontReport, error) {
	report := DynamicWorkbookFontReport{
		PersianFont:      strings.TrimSpace(policy.PersianFont),
		LatinFont:        strings.TrimSpace(policy.LatinFont),
		PriceDisplayFont: strings.TrimSpace(policy.LatinFont),
	}
	if report.PersianFont == "" || report.LatinFont == "" {
		return report, errors.New("font policy requires nonblank PersianFont and LatinFont")
	}
	if policy.HasPriceDisplayFaNum && policy.PriceDisplayFaNum {
		report.PriceDisplayFont = defaultDynamicPriceDigitFont
	}
	book, err := excelize.OpenFile(path)
	if err != nil {
		return report, fmt.Errorf("open workbook font package: %w", err)
	}
	defer book.Close()
	if got := book.GetSheetList(); !stringSliceEqual(got, []string{"محصولات", "داشبورد", "تنظیمات", "داده‌های همگام‌سازی"}) {
		return report, fmt.Errorf("dynamic workbook sheet map is %v, want the fixed four-sheet role map", got)
	}
	styles, err := readOpenXMLStyles(path)
	if err != nil {
		return report, err
	}
	expected := make(map[string]string)
	addFontRoleRange(expected, "محصولات", "B1:K5", report.PersianFont)
	addFontRoleRange(expected, "داشبورد", "B1:I34", report.PersianFont)
	for _, value := range []string{
		"B5:C6", "D5:E6", "F5:G6", "H5:I6", "B9:C10", "D9:E10",
		"F9:G10", "H9:I10", "B13:E14", "F13:G14", "H13:I14",
		"C22:C24", "C27:C29", "C32:C34",
	} {
		addFontRoleRange(expected, "داشبورد", value, report.LatinFont)
	}
	addFontRoleRange(expected, "تنظیمات", "A1:F55", report.PersianFont)
	settingsLatinRanges := []string{
		"B3:F4", "B7:F7", "B10:F15", "B18:F22", "B24:F26", "B46:F55",
	}
	if policy.HasPriceDisplayFaNum {
		settingsLatinRanges = append(settingsLatinRanges, "B39:F40")
	} else {
		settingsLatinRanges = append(settingsLatinRanges, "B39:F43")
	}
	for _, value := range settingsLatinRanges {
		addFontRoleRange(expected, "تنظیمات", value, report.LatinFont)
	}
	if err := addTableFontRoles(book, expected, "محصولات", "Products", report.PersianFont, report.LatinFont, []int{3, 4, 8, 10}, []int{2, 5, 6, 7, 9}); err != nil {
		return report, err
	}
	if err := addTableColumnFontRole(book, expected, "محصولات", "Products", 1, report.PriceDisplayFont); err != nil {
		return report, err
	}
	if err := addTableFontRoles(book, expected, "داده‌های همگام‌سازی", "SyncData", report.PersianFont, report.LatinFont, []int{17, 18, 19, 20}, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 21, 22, 23, 24}); err != nil {
		return report, err
	}
	for key, fontName := range expected {
		sheet, cellName, _ := strings.Cut(key, "!")
		styleID, err := book.GetCellStyle(sheet, cellName)
		if err != nil {
			return report, fmt.Errorf("read mapped font style %s: %w", key, err)
		}
		if styleID < 0 || styleID >= len(styles.CellXfs.XFs) {
			return report, fmt.Errorf("mapped font style %s has invalid style ID %d", key, styleID)
		}
		fontID := styles.CellXfs.XFs[styleID].FontID
		if fontID < 0 || fontID >= len(styles.Fonts.Fonts) {
			return report, fmt.Errorf("mapped font style %s has invalid font ID %d", key, fontID)
		}
		font := styles.Fonts.Fonts[fontID]
		actual := ""
		if font.Name != nil {
			actual = strings.TrimSpace(font.Name.Value)
		}
		if actual == "" || !strings.EqualFold(actual, fontName) {
			return report, fmt.Errorf("mapped font style %s is %q, want %q", key, actual, fontName)
		}
		if isForbiddenWorkbookFont(actual) {
			return report, fmt.Errorf("mapped font style %s uses forbidden family %q", key, actual)
		}
		if font.Scheme != nil && strings.TrimSpace(font.Scheme.Value) != "" {
			return report, fmt.Errorf("mapped font style %s retains theme scheme %q", key, font.Scheme.Value)
		}
		report.MappedCells++
	}
	runs, slots, err := auditDrawingTextFonts(path, report.PersianFont)
	if err != nil {
		return report, err
	}
	if runs == 0 {
		return report, errors.New("dynamic workbook has no explicitly mapped DrawingML text runs")
	}
	report.DrawingTextRuns = runs
	report.DrawingFontSlots = slots
	return report, nil
}

func addFontRoleRange(target map[string]string, sheet, reference, fontName string) {
	parts := strings.Split(strings.ReplaceAll(reference, "$", ""), ":")
	if len(parts) != 2 {
		return
	}
	startColumn, startRow, startErr := excelize.CellNameToCoordinates(parts[0])
	endColumn, endRow, endErr := excelize.CellNameToCoordinates(parts[1])
	if startErr != nil || endErr != nil {
		return
	}
	for row := startRow; row <= endRow; row++ {
		for column := startColumn; column <= endColumn; column++ {
			cellName, _ := excelize.CoordinatesToCellName(column, row)
			target[sheet+"!"+cellName] = fontName
		}
	}
}

func addTableFontRoles(book *excelize.File, target map[string]string, sheet, tableName, persianFont, latinFont string, persianColumns, latinColumns []int) error {
	tables, err := book.GetTables(sheet)
	if err != nil {
		return err
	}
	for _, table := range tables {
		if table.Name != tableName {
			continue
		}
		parts := strings.Split(strings.ReplaceAll(table.Range, "$", ""), ":")
		if len(parts) != 2 {
			return fmt.Errorf("font role table %q has invalid range %q", tableName, table.Range)
		}
		startColumn, headerRow, err := excelize.CellNameToCoordinates(parts[0])
		if err != nil {
			return err
		}
		endColumn, endRow, err := excelize.CellNameToCoordinates(parts[1])
		if err != nil {
			return err
		}
		for column := startColumn; column <= endColumn; column++ {
			cellName, _ := excelize.CoordinatesToCellName(column, headerRow)
			target[sheet+"!"+cellName] = persianFont
		}
		for _, role := range []struct {
			columns []int
			font    string
		}{{persianColumns, persianFont}, {latinColumns, latinFont}} {
			for _, relativeColumn := range role.columns {
				column := startColumn + relativeColumn - 1
				if column > endColumn {
					return fmt.Errorf("font role column %d is outside table %q", relativeColumn, tableName)
				}
				for row := headerRow + 1; row <= endRow; row++ {
					cellName, _ := excelize.CoordinatesToCellName(column, row)
					target[sheet+"!"+cellName] = role.font
				}
			}
		}
		return nil
	}
	return fmt.Errorf("font role table %q does not exist", tableName)
}

func addTableColumnFontRole(book *excelize.File, target map[string]string, sheet, tableName string, relativeColumn int, fontName string) error {
	tables, err := book.GetTables(sheet)
	if err != nil {
		return err
	}
	for _, table := range tables {
		if table.Name != tableName {
			continue
		}
		parts := strings.Split(strings.ReplaceAll(table.Range, "$", ""), ":")
		if len(parts) != 2 {
			return fmt.Errorf("font role table %q has invalid range %q", tableName, table.Range)
		}
		startColumn, headerRow, err := excelize.CellNameToCoordinates(parts[0])
		if err != nil {
			return err
		}
		endColumn, endRow, err := excelize.CellNameToCoordinates(parts[1])
		if err != nil {
			return err
		}
		column := startColumn + relativeColumn - 1
		if relativeColumn < 1 || column > endColumn {
			return fmt.Errorf("font role column %d is outside table %q", relativeColumn, tableName)
		}
		for row := headerRow + 1; row <= endRow; row++ {
			cellName, _ := excelize.CoordinatesToCellName(column, row)
			target[sheet+"!"+cellName] = fontName
		}
		return nil
	}
	return fmt.Errorf("font role table %q does not exist", tableName)
}

func readOpenXMLStyles(path string) (openXMLStyles, error) {
	var styles openXMLStyles
	archive, err := zip.OpenReader(path)
	if err != nil {
		return styles, err
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if entry.Name != "xl/styles.xml" {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return styles, err
		}
		err = xml.NewDecoder(reader).Decode(&styles)
		_ = reader.Close()
		if err != nil {
			return styles, fmt.Errorf("parse xl/styles.xml: %w", err)
		}
		if len(styles.Fonts.Fonts) == 0 || len(styles.CellXfs.XFs) == 0 {
			return styles, errors.New("xl/styles.xml has no fonts or cell styles")
		}
		return styles, nil
	}
	return styles, errors.New("workbook package is missing xl/styles.xml")
}

func auditDrawingTextFonts(path, expected string) (int, int, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return 0, 0, err
	}
	defer archive.Close()
	runs := 0
	slots := 0
	for _, entry := range archive.File {
		if !(strings.HasPrefix(entry.Name, "xl/drawings/") || strings.HasPrefix(entry.Name, "xl/charts/")) || filepath.Ext(entry.Name) != ".xml" {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return runs, slots, err
		}
		partRuns, partSlots, auditErr := auditDrawingXML(reader, entry.Name, expected)
		_ = reader.Close()
		if auditErr != nil {
			return runs, slots, auditErr
		}
		runs += partRuns
		slots += partSlots
	}
	return runs, slots, nil
}

func auditDrawingXML(reader io.Reader, partName, expected string) (int, int, error) {
	decoder := xml.NewDecoder(reader)
	var run *drawingRunAudit
	var defaults *drawingRunAudit
	propertyGroup := ""
	runs := 0
	slots := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return runs, slots, fmt.Errorf("parse DrawingML part %q: %w", partName, err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			switch typed.Name.Local {
			case "r":
				run = &drawingRunAudit{slots: make(map[string]string)}
			case "rPr":
				if run != nil {
					propertyGroup = "run"
					for _, attribute := range typed.Attr {
						if attribute.Name.Local == "lang" {
							run.lang = attribute.Value
						}
					}
				}
			case "defRPr":
				defaults = &drawingRunAudit{slots: make(map[string]string)}
				propertyGroup = "default"
				for _, attribute := range typed.Attr {
					if attribute.Name.Local == "lang" {
						defaults.lang = attribute.Value
					}
				}
			case "latin", "ea", "cs":
				if propertyGroup == "run" && run != nil {
					for _, attribute := range typed.Attr {
						if attribute.Name.Local == "typeface" {
							run.slots[typed.Name.Local] = strings.TrimSpace(attribute.Value)
						}
					}
				} else if propertyGroup == "default" && defaults != nil {
					for _, attribute := range typed.Attr {
						if attribute.Name.Local == "typeface" {
							defaults.slots[typed.Name.Local] = strings.TrimSpace(attribute.Value)
						}
					}
				}
			case "t":
				if run != nil {
					var text string
					if err := decoder.DecodeElement(&text, &typed); err != nil {
						return runs, slots, err
					}
					run.text += text
				}
			}
		case xml.EndElement:
			switch typed.Name.Local {
			case "rPr":
				propertyGroup = ""
			case "defRPr":
				propertyGroup = ""
				if defaults != nil && len(defaults.slots) > 0 {
					for _, slotName := range []string{"latin", "ea", "cs"} {
						actual := defaults.slots[slotName]
						if actual == "" || !strings.EqualFold(actual, expected) {
							return runs, slots, fmt.Errorf("DrawingML default text in %q has %s=%q, want %q", partName, slotName, actual, expected)
						}
						if isForbiddenWorkbookFont(actual) {
							return runs, slots, fmt.Errorf("DrawingML default text in %q uses forbidden font %q", partName, actual)
						}
						slots++
					}
					runs++
					if !strings.EqualFold(defaults.lang, "fa-IR") {
						return runs, slots, fmt.Errorf("DrawingML default text in %q has language %q, want fa-IR", partName, defaults.lang)
					}
				}
				defaults = nil
			case "r":
				if run != nil && strings.TrimSpace(run.text) != "" {
					for _, slotName := range []string{"latin", "ea", "cs"} {
						actual := run.slots[slotName]
						if actual == "" || !strings.EqualFold(actual, expected) {
							return runs, slots, fmt.Errorf("DrawingML text run in %q has %s=%q, want %q", partName, slotName, actual, expected)
						}
						if isForbiddenWorkbookFont(actual) {
							return runs, slots, fmt.Errorf("DrawingML text run in %q uses forbidden font %q", partName, actual)
						}
						slots++
					}
					if !strings.EqualFold(run.lang, "fa-IR") {
						return runs, slots, fmt.Errorf("DrawingML text run in %q has language %q, want fa-IR", partName, run.lang)
					}
					runs++
				}
				run = nil
			}
		}
	}
	return runs, slots, nil
}

func isForbiddenWorkbookFont(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "aptos", "calibri", "arial":
		return true
	default:
		return false
	}
}
