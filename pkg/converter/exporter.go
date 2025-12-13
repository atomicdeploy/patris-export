package converter

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/atomicdeploy/patris-export/pkg/paradox"
)

// ExportFormat represents the export format type
type ExportFormat string

const (
	FormatJSON ExportFormat = "json"
	FormatCSV  ExportFormat = "csv"
)

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

// ExportToJSON exports records to JSON format
func (e *Exporter) ExportToJSON(records []paradox.Record, outputPath string) error {
	// Convert string fields if converter is set
	if e.converter != nil {
		records = e.convertRecords(records)
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(records); err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
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

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return string(data), nil
}
