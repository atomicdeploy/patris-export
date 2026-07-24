package converter

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/atomicdeploy/patris-export/pkg/paradox"
)

// ExportFormat represents the export format type
type ExportFormat string

const (
	FormatJSON ExportFormat = "json"
	FormatCSV  ExportFormat = "csv"
)

// Regular expression to match numbered ANBAR fields (ANBAR1, ANBAR2, etc.)
var anbarFieldRegex = regexp.MustCompile(`^ANBAR\d+$`)

// IsSortField reports whether a Paradox field is an internal sorting column.
// These fields are transport metadata and must never be converted, exported,
// or compared as business data.
func IsSortField(field string) bool {
	return strings.HasPrefix(field, "Sort")
}

// Exporter handles exporting Paradox database records
type Exporter struct {
	converter func(string) string
}

// NewExporter creates a new exporter with optional converter function
func NewExporter(converter func(string) string) *Exporter {
	return &Exporter{
		converter: converter,
	}
}

// ExportToJSON exports records to JSON format with Patris81-specific formatting
func (e *Exporter) ExportToJSON(records []paradox.Record, outputPath string) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	return e.ExportToJSONWriter(records, file)
}

// ExportToCSV exports records to CSV format
func (e *Exporter) ExportToCSV(records []paradox.Record, fields []paradox.Field, outputPath string) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	return e.ExportToCSVWriter(records, fields, file)
}

// ExportToJSONWriter exports records to JSON format writing to the provided io.Writer
func (e *Exporter) ExportToJSONWriter(records []paradox.Record, writer io.Writer) error {
	// Convert string fields if converter is set
	if e.converter != nil {
		records = e.ConvertRecords(records)
	}

	// Transform records to use Code as key and optimize structure
	transformed := e.TransformRecords(records)

	// Use custom JSON formatting to keep ANBAR inline
	data, err := json.MarshalIndent(transformed, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	// Post-process to make ANBAR arrays inline
	output := makeArraysInline(string(data), "ANBAR")

	// Add trailing newline for better Unix tool compatibility
	if _, err := writer.Write([]byte(output + "\n")); err != nil {
		return fmt.Errorf("failed to write JSON: %w", err)
	}

	return nil
}

// ExportToCSVWriter exports records to CSV format writing to the provided io.Writer
func (e *Exporter) ExportToCSVWriter(records []paradox.Record, fields []paradox.Field, writer io.Writer) error {
	fields = exportFields(fields)

	// Convert string fields if converter is set
	if e.converter != nil {
		records = e.ConvertRecords(records)
	}

	csvWriter := csv.NewWriter(writer)

	// Write header
	header := make([]string, len(fields))
	for i, field := range fields {
		header[i] = field.Name
	}
	if err := csvWriter.Write(header); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write records
	for _, record := range records {
		row := make([]string, len(fields))
		for i, field := range fields {
			if val, ok := record[field.Name]; ok {
				row[i] = fmt.Sprintf("%v", val)
			}
		}
		if err := csvWriter.Write(row); err != nil {
			return fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	// Explicitly flush and check for errors
	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		return fmt.Errorf("failed to write CSV: flush failed: %w", err)
	}

	return nil
}

// ConvertRecords converts string fields while preserving record cardinality
// and order. Dataset profiles use it before Code-keyed shaping so duplicate
// identifiers remain observable and can be quarantined safely.
func (e *Exporter) ConvertRecords(records []paradox.Record) []paradox.Record {
	converted, _ := e.ConvertRecordsContext(context.Background(), records)
	return converted
}

// ConvertRecordsContext converts records while cooperatively honoring caller
// cancellation between records and fields. The background wrapper above keeps
// the established exporter API unchanged for unbounded CLI/file workflows.
func (e *Exporter) ConvertRecordsContext(ctx context.Context, records []paradox.Record) ([]paradox.Record, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	converted := make([]paradox.Record, len(records))

	for i, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		convertedRecord := make(paradox.Record)
		for key, value := range record {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// Sort fields are internal Paradox indexes. Discard them before
			// even inspecting/converting values to avoid needless work.
			if IsSortField(key) {
				continue
			}
			if strVal, ok := value.(string); ok {
				// Only convert non-empty strings
				if strings.TrimSpace(strVal) != "" {
					convertedRecord[key] = e.converter(strVal)
				} else {
					convertedRecord[key] = strVal
				}
			} else {
				convertedRecord[key] = value
			}
		}
		converted[i] = convertedRecord
	}

	return converted, nil
}

// ExportRecordsToString exports records to a JSON string
func (e *Exporter) ExportRecordsToString(records []paradox.Record) (string, error) {
	// Convert string fields if converter is set
	if e.converter != nil {
		records = e.ConvertRecords(records)
	}

	// Transform records to use Code as key and optimize structure
	transformed := e.TransformRecords(records)

	data, err := json.MarshalIndent(transformed, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}

	// Post-process to make ANBAR arrays inline
	output := makeArraysInline(string(data), "ANBAR")

	return output, nil
}

// ConvertAndTransformRecords converts string fields and transforms records for Patris81-specific output.
// This combines the conversion and transformation steps into a single method for use by the web server.
func (e *Exporter) ConvertAndTransformRecords(records []paradox.Record) map[string]interface{} {
	// Convert string fields if converter is set
	if e.converter != nil {
		records = e.ConvertRecords(records)
	}

	// Transform records to use Code as key and optimize structure
	return e.TransformRecords(records)
}

// TransformRecordsMap transforms an array of map records to Code-keyed map format
// This is used when records are already in map[string]interface{} format (e.g., from datasource)
func (e *Exporter) TransformRecordsMap(records []map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	for _, record := range records {
		// Extract Code as the key
		codeKey := ""
		if code, ok := record["Code"]; ok {
			codeKey = fmt.Sprintf("%v", code)
		} else {
			// Skip records without Code
			continue
		}

		// Create a copy of the record without Code field (it becomes the key)
		transformedRecord := make(map[string]interface{})
		for key, value := range record {
			if key != "Code" && !IsSortField(key) {
				transformedRecord[key] = splitDescriptionLines(key, value)
			}
		}

		result[codeKey] = transformedRecord
	}

	return result
}

// TransformRecords transforms records for Patris81-specific output format:
// - Use Code field as the key
// - Ignore fields starting with "Sort"
// - Combine ANBAR fields into an array (sorted by number)
// This method is used by both the file exporter and the web server to ensure consistent output.
func (e *Exporter) TransformRecords(records []paradox.Record) map[string]interface{} {
	result, _ := e.TransformRecordsContext(context.Background(), records)
	return result
}

// TransformRecordsContext shapes records while cooperatively honoring caller
// cancellation between records, fields, and ANBAR materialization.
func (e *Exporter) TransformRecordsContext(ctx context.Context, records []paradox.Record) (map[string]interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make(map[string]interface{})

	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Extract Code as the key
		codeKey := ""
		if code, ok := record["Code"]; ok {
			codeKey = fmt.Sprintf("%v", code)
		} else {
			// Skip records without Code
			continue
		}

		// Build optimized record
		optimized := make(map[string]interface{})
		anbarFields := make(map[int]interface{})
		maxAnbarIndex := 0

		for key, value := range record {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// Skip Sort fields
			if IsSortField(key) {
				continue
			}

			// Keep ALLANBAR as-is (check first to avoid confusion with ANBAR pattern)
			if key == "ALLANBAR" {
				optimized[key] = value
				continue
			}

			// Collect numbered ANBAR fields into map (ANBAR1, ANBAR2, etc.)
			if anbarFieldRegex.MatchString(key) {
				// Extract the number from ANBARn field name (e.g., "ANBAR1" -> 1)
				var index int
				if n, _ := fmt.Sscanf(key, "ANBAR%d", &index); n == 1 && index > 0 {
					anbarFields[index] = value
					if index > maxAnbarIndex {
						maxAnbarIndex = index
					}
				}
				continue
			}

			// Add all other fields
			optimized[key] = splitDescriptionLines(key, value)
		}

		// Add ANBAR array if we collected any, in sorted order by field number
		if len(anbarFields) > 0 {
			// Build array with correct ordering (1-indexed fields -> 0-indexed array)
			anbarValues := make([]interface{}, maxAnbarIndex)
			for i := 1; i <= maxAnbarIndex; i++ {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if val, ok := anbarFields[i]; ok {
					anbarValues[i-1] = val
				} else {
					anbarValues[i-1] = 0 // Fill missing indices with 0
				}
			}
			optimized["ANBAR"] = anbarValues
		}

		result[codeKey] = optimized
	}

	return result, nil
}

func exportFields(fields []paradox.Field) []paradox.Field {
	filtered := make([]paradox.Field, 0, len(fields))
	for _, field := range fields {
		if !IsSortField(field.Name) {
			filtered = append(filtered, field)
		}
	}
	return filtered
}

func splitDescriptionLines(field string, value interface{}) interface{} {
	if field != "Sharh1" && field != "Sharh2" {
		return value
	}
	text, ok := value.(string)
	if !ok {
		return value
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if !strings.Contains(text, "\n") {
		return value
	}
	return strings.Split(text, "\n")
}

// makeArraysInline converts multi-line numeric arrays to single-line format
// Specifically optimized for ANBAR arrays but works for any numeric array
func makeArraysInline(jsonStr string, fieldNames ...string) string {
	// Build pattern to match specified field names
	fieldPattern := strings.Join(fieldNames, "|")
	if fieldPattern == "" {
		return jsonStr
	}

	// Pattern to match multi-line arrays with numeric values
	// Matches: "ANBAR": [\n      1,\n      2,\n    ]
	pattern := fmt.Sprintf(`("(?:%s)":\s*)\[\s*((?:\d+,?\s*)+)\]`, fieldPattern)
	re := regexp.MustCompile(pattern)

	return re.ReplaceAllStringFunc(jsonStr, func(match string) string {
		// Extract field name
		fieldRe := regexp.MustCompile(`"([^"]+)":`)
		fieldMatch := fieldRe.FindStringSubmatch(match)
		if len(fieldMatch) < 2 {
			return match
		}
		fieldName := fieldMatch[1]

		// Extract the numeric values
		valueRe := regexp.MustCompile(`\d+`)
		values := valueRe.FindAllString(match, -1)

		// Check if match ends with comma (not last property)
		hasComma := strings.HasSuffix(strings.TrimSpace(match), ",")

		// Rebuild as inline with proper spacing
		result := fmt.Sprintf(`"%s": [%s]`, fieldName, strings.Join(values, ", "))
		if hasComma {
			result += ","
		}

		return result
	})
}
