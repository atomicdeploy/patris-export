package converter

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/paradox"
)

func TestExportToJSONWriter(t *testing.T) {
	tests := []struct {
		name     string
		records  []paradox.Record
		expected string
	}{
		{
			name: "Simple record",
			records: []paradox.Record{
				{
					"Code": "123",
					"Name": "Test",
				},
			},
			expected: `{
  "123": {
    "Code": "123",
    "Name": "Test"
  }
}`,
		},
		{
			name: "Record with ANBAR fields",
			records: []paradox.Record{
				{
					"Code":   "456",
					"Name":   "Product",
					"ANBAR1": 10,
					"ANBAR2": 20,
				},
			},
			expected: `{
  "456": {
    "ANBAR": [10, 20],
    "Code": "456",
    "Name": "Product"
  }
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := NewExporter(nil)
			var buf bytes.Buffer

			err := exp.ExportToJSONWriter(tt.records, &buf)
			if err != nil {
				t.Fatalf("ExportToJSONWriter failed: %v", err)
			}

			// Parse both expected and actual JSON to compare structure
			var expectedJSON, actualJSON map[string]interface{}
			if err := json.Unmarshal([]byte(tt.expected), &expectedJSON); err != nil {
				t.Fatalf("Failed to parse expected JSON: %v", err)
			}
			if err := json.Unmarshal(buf.Bytes(), &actualJSON); err != nil {
				t.Fatalf("Failed to parse actual JSON: %v", err)
			}

			// Compare the JSON structures
			if !jsonEqual(expectedJSON, actualJSON) {
				t.Errorf("JSON output mismatch:\nExpected:\n%s\nGot:\n%s", tt.expected, buf.String())
			}
		})
	}
}

func TestExportToCSVWriter(t *testing.T) {
	tests := []struct {
		name            string
		records         []paradox.Record
		fields          []paradox.Field
		expectedHeaders []string
		expectedRows    [][]string
	}{
		{
			name: "Simple CSV",
			records: []paradox.Record{
				{
					"Code": "123",
					"Name": "Test",
				},
				{
					"Code": "456",
					"Name": "Product",
				},
			},
			fields: []paradox.Field{
				{Name: "Code"},
				{Name: "Name"},
			},
			expectedHeaders: []string{"Code", "Name"},
			expectedRows: [][]string{
				{"123", "Test"},
				{"456", "Product"},
			},
		},
		{
			name: "CSV with missing field",
			records: []paradox.Record{
				{
					"Code": "789",
					"Name": "Item",
				},
				{
					"Code": "012",
				},
			},
			fields: []paradox.Field{
				{Name: "Code"},
				{Name: "Name"},
			},
			expectedHeaders: []string{"Code", "Name"},
			expectedRows: [][]string{
				{"789", "Item"},
				{"012", ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := NewExporter(nil)
			var buf bytes.Buffer

			err := exp.ExportToCSVWriter(tt.records, tt.fields, &buf)
			if err != nil {
				t.Fatalf("ExportToCSVWriter failed: %v", err)
			}

			// Parse CSV output
			reader := csv.NewReader(strings.NewReader(buf.String()))
			rows, err := reader.ReadAll()
			if err != nil {
				t.Fatalf("Failed to parse CSV: %v", err)
			}

			// Check headers
			if len(rows) < 1 {
				t.Fatal("No header row in CSV output")
			}
			headers := rows[0]
			if len(headers) != len(tt.expectedHeaders) {
				t.Errorf("Header count mismatch: expected %d, got %d", len(tt.expectedHeaders), len(headers))
			}
			for i, expected := range tt.expectedHeaders {
				if i >= len(headers) {
					t.Errorf("Missing header at index %d", i)
					continue
				}
				if headers[i] != expected {
					t.Errorf("Header[%d] mismatch: expected %s, got %s", i, expected, headers[i])
				}
			}

			// Check data rows
			dataRows := rows[1:]
			if len(dataRows) != len(tt.expectedRows) {
				t.Errorf("Row count mismatch: expected %d, got %d", len(tt.expectedRows), len(dataRows))
			}
			for i, expectedRow := range tt.expectedRows {
				if i >= len(dataRows) {
					t.Errorf("Missing row at index %d", i)
					continue
				}
				actualRow := dataRows[i]
				if len(actualRow) != len(expectedRow) {
					t.Errorf("Row[%d] column count mismatch: expected %d, got %d", i, len(expectedRow), len(actualRow))
				}
				for j, expected := range expectedRow {
					if j >= len(actualRow) {
						t.Errorf("Missing column at row %d, column %d", i, j)
						continue
					}
					if actualRow[j] != expected {
						t.Errorf("Row[%d][%d] mismatch: expected %s, got %s", i, j, expected, actualRow[j])
					}
				}
			}
		})
	}
}

// jsonEqual compares two JSON objects for equality
func jsonEqual(a, b interface{}) bool {
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)

	var aMap, bMap interface{}
	json.Unmarshal(aJSON, &aMap)
	json.Unmarshal(bJSON, &bMap)

	aStr, _ := json.Marshal(aMap)
	bStr, _ := json.Marshal(bMap)

	return string(aStr) == string(bStr)
}
