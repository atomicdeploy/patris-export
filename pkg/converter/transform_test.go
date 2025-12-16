package converter

import (
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/paradox"
)

func TestTransformRecords(t *testing.T) {
	tests := []struct {
		name     string
		input    []paradox.Record
		expected map[string]interface{}
	}{
		{
			name: "Combines ANBAR fields into array",
			input: []paradox.Record{
				{
					"Code":   "12345",
					"Name":   "Test Product",
					"ANBAR1": 10,
					"ANBAR2": 20,
					"ANBAR3": 30,
					"ANBAR4": 0,
					"ANBAR5": 0,
				},
			},
			expected: map[string]interface{}{
				"12345": map[string]interface{}{
					"Code":  "12345",
					"Name":  "Test Product",
					"ANBAR": []interface{}{10, 20, 30, 0, 0},
				},
			},
		},
		{
			name: "Removes Sort fields",
			input: []paradox.Record{
				{
					"Code":  "67890",
					"Name":  "Another Product",
					"Sort1": "ShouldBeRemoved",
					"Sort2": "AlsoRemoved",
					"Value": 100,
				},
			},
			expected: map[string]interface{}{
				"67890": map[string]interface{}{
					"Code":  "67890",
					"Name":  "Another Product",
					"Value": 100,
				},
			},
		},
		{
			name: "Keeps ALLANBAR field",
			input: []paradox.Record{
				{
					"Code":     "99999",
					"ALLANBAR": 500,
					"ANBAR1":   10,
					"ANBAR2":   20,
				},
			},
			expected: map[string]interface{}{
				"99999": map[string]interface{}{
					"Code":     "99999",
					"ALLANBAR": 500,
					"ANBAR":    []interface{}{10, 20},
				},
			},
		},
		{
			name: "Uses Code as key",
			input: []paradox.Record{
				{
					"Code":  "100",
					"Name":  "First",
					"Value": 1,
				},
				{
					"Code":  "200",
					"Name":  "Second",
					"Value": 2,
				},
			},
			expected: map[string]interface{}{
				"100": map[string]interface{}{
					"Code":  "100",
					"Name":  "First",
					"Value": 1,
				},
				"200": map[string]interface{}{
					"Code":  "200",
					"Name":  "Second",
					"Value": 2,
				},
			},
		},
		{
			name: "Skips records without Code",
			input: []paradox.Record{
				{
					"Name":  "No Code",
					"Value": 999,
				},
				{
					"Code":  "111",
					"Name":  "Has Code",
					"Value": 111,
				},
			},
			expected: map[string]interface{}{
				"111": map[string]interface{}{
					"Code":  "111",
					"Name":  "Has Code",
					"Value": 111,
				},
			},
		},
		{
			name: "Complete transformation with all features",
			input: []paradox.Record{
				{
					"Code":     "12345",
					"Name":     "Complete Test",
					"ANBAR1":   5,
					"ANBAR2":   10,
					"ANBAR3":   0,
					"ANBAR4":   0,
					"ANBAR5":   0,
					"ANBAR6":   0,
					"ANBAR7":   0,
					"ANBAR8":   0,
					"ANBAR9":   0,
					"ANBAR10":  0,
					"ALLANBAR": 15,
					"Sort1":    "Remove",
					"Sort2":    "Remove",
					"SortCode": "Remove",
					"Price":    1000,
				},
			},
			expected: map[string]interface{}{
				"12345": map[string]interface{}{
					"Code":     "12345",
					"Name":     "Complete Test",
					"ANBAR":    []interface{}{5, 10, 0, 0, 0, 0, 0, 0, 0, 0},
					"ALLANBAR": 15,
					"Price":    1000,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := NewExporter(nil)
			result := exp.TransformRecords(tt.input)

			// Check that we got the expected number of records
			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d records, got %d", len(tt.expected), len(result))
				return
			}

			// Check each expected record
			for key, expectedRecord := range tt.expected {
				resultRecord, ok := result[key]
				if !ok {
					t.Errorf("Expected key %s not found in result", key)
					continue
				}

				// Compare the records
				expectedMap := expectedRecord.(map[string]interface{})
				resultMap := resultRecord.(map[string]interface{})

				// Check that all expected fields are present
				for field, expectedValue := range expectedMap {
					resultValue, ok := resultMap[field]
					if !ok {
						t.Errorf("Expected field %s not found in result for key %s", field, key)
						continue
					}

					// Special handling for arrays (ANBAR)
					if field == "ANBAR" {
						expectedArray := expectedValue.([]interface{})
						resultArray := resultValue.([]interface{})
						if len(expectedArray) != len(resultArray) {
							t.Errorf("ANBAR array length mismatch for key %s: expected %d, got %d", key, len(expectedArray), len(resultArray))
							continue
						}
						for i := range expectedArray {
							if expectedArray[i] != resultArray[i] {
								t.Errorf("ANBAR[%d] mismatch for key %s: expected %v, got %v", i, key, expectedArray[i], resultArray[i])
							}
						}
					} else if expectedValue != resultValue {
						t.Errorf("Field %s mismatch for key %s: expected %v, got %v", field, key, expectedValue, resultValue)
					}
				}

				// Check that no unexpected fields are present (like Sort fields)
				for field := range resultMap {
					if _, expected := expectedMap[field]; !expected {
						t.Errorf("Unexpected field %s found in result for key %s with value %v", field, key, resultMap[field])
					}
				}
			}
		})
	}
}
