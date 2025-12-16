package converter

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
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
	// Convert string fields if converter is set
	if e.converter != nil {
		records = e.convertRecords(records)
	}

	// Transform records to use Code as key and optimize structure
	transformed := e.TransformRecords(records)

	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	// Use custom JSON formatting to keep ANBAR inline
	data, err := json.MarshalIndent(transformed, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	// Post-process to make ANBAR arrays inline
	output := makeArraysInline(string(data), "ANBAR")

	if _, err := file.WriteString(output); err != nil {
		return fmt.Errorf("failed to write JSON: %w", err)
	}

	return nil
}

// ExportToCSV exports records to CSV format
func (e *Exporter) ExportToCSV(records []paradox.Record, fields []paradox.Field, outputPath string) error {
	// Convert string fields if converter is set
	if e.converter != nil {
		records = e.convertRecords(records)
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header
	header := make([]string, len(fields))
	for i, field := range fields {
		header[i] = field.Name
	}
	if err := writer.Write(header); err != nil {
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
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	return nil
}

// convertRecords converts string fields in records using the converter function
func (e *Exporter) convertRecords(records []paradox.Record) []paradox.Record {
	converted := make([]paradox.Record, len(records))
	
	for i, record := range records {
		convertedRecord := make(paradox.Record)
		for key, value := range record {
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
	
	return converted
}

// ExportRecordsToString exports records to a JSON string
func (e *Exporter) ExportRecordsToString(records []paradox.Record) (string, error) {
	// Convert string fields if converter is set
	if e.converter != nil {
		records = e.convertRecords(records)
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
		records = e.convertRecords(records)
	}
	
	// Transform records to use Code as key and optimize structure
	return e.TransformRecords(records)
}

// TransformRecords transforms records for Patris81-specific output format:
// - Use Code field as the key
// - Ignore fields starting with "Sort"
// - Combine ANBAR fields into an array
// This method is used by both the file exporter and the web server to ensure consistent output.
func (e *Exporter) TransformRecords(records []paradox.Record) map[string]interface{} {
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
		
		// Build optimized record
		optimized := make(map[string]interface{})
		anbarFields := make(map[int]interface{})
		
		for key, value := range record {
			// Skip Sort fields
			if strings.HasPrefix(key, "Sort") {
				continue
			}
			
			// Keep ALLANBAR as-is (check first to avoid confusion with ANBAR pattern)
			if key == "ALLANBAR" {
				optimized[key] = value
				continue
			}
			
			// Collect numbered ANBAR fields into map (ANBAR1, ANBAR2, etc.)
			if anbarFieldRegex.MatchString(key) {
				// Extract the number from ANBAR field name (e.g., "ANBAR1" -> 1)
				var num int
				if n, _ := fmt.Sscanf(key, "ANBAR%d", &num); n == 1 && num > 0 {
					anbarFields[num] = value
				}
				continue
			}
			
			// Add all other fields
			optimized[key] = value
		}
		
		// Add ANBAR array if we collected any, sorted by field number
		if len(anbarFields) > 0 {
			// Find the maximum ANBAR number to determine array size
			maxNum := 0
			for num := range anbarFields {
				if num > maxNum {
					maxNum = num
				}
			}
			
			// Build array with correct ordering (1-indexed fields -> 0-indexed array)
			anbarValues := make([]interface{}, maxNum)
			for i := 1; i <= maxNum; i++ {
				if val, ok := anbarFields[i]; ok {
					anbarValues[i-1] = val
				} else {
					anbarValues[i-1] = 0
				}
			}
			optimized["ANBAR"] = anbarValues
		}
		
		result[codeKey] = optimized
	}
	
	return result
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
