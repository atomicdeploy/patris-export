package converter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMakeArraysInline(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		expected string
	}{
		{
			name: "ANBAR array gets inlined",
			input: map[string]interface{}{
				"Code":  102005001,
				"Name":  "Test",
				"ANBAR": []int{2, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			},
			expected: `"ANBAR": [2, 0, 0, 0, 0, 0, 0, 0, 0, 0]`,
		},
		{
			name: "All zeros ANBAR",
			input: map[string]interface{}{
				"Code":  102005002,
				"ANBAR": []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			},
			expected: `"ANBAR": [0, 0, 0, 0, 0, 0, 0, 0, 0, 0]`,
		},
		{
			name: "Mixed values ANBAR",
			input: map[string]interface{}{
				"Code":  102005003,
				"ANBAR": []int{10, 20, 30, 0, 0, 0, 0, 0, 0, 0},
			},
			expected: `"ANBAR": [10, 20, 30, 0, 0, 0, 0, 0, 0, 0]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal to indented JSON
			jsonBytes, err := json.MarshalIndent(tt.input, "", "  ")
			if err != nil {
				t.Fatalf("MarshalIndent failed: %v", err)
			}

			// Apply makeArraysInline
			result := makeArraysInline(string(jsonBytes), "ANBAR")

			// Check that result contains the expected inline format
			if !strings.Contains(result, tt.expected) {
				t.Errorf("Expected result to contain:\n%s\n\nGot:\n%s", tt.expected, result)
			}

			// Verify ANBAR is on a single line
			lines := strings.Split(result, "\n")
			anbarLineCount := 0
			for _, line := range lines {
				if strings.Contains(line, "ANBAR") {
					anbarLineCount++
					// Verify it's the complete array on one line
					if !strings.Contains(line, "[") || !strings.Contains(line, "]") {
						t.Errorf("ANBAR line should contain complete array: %s", line)
					}
				}
			}

			if anbarLineCount != 1 {
				t.Errorf("Expected exactly 1 line with ANBAR, got %d", anbarLineCount)
			}
		})
	}
}

func TestMakeArraysInlineNested(t *testing.T) {
	// Test with nested structure like actual output
	data := map[string]interface{}{
		"102005001": map[string]interface{}{
			"Code":  102005001,
			"Name":  "Test Product",
			"ANBAR": []int{2, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			"Value": 100,
		},
	}

	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent failed: %v", err)
	}

	result := makeArraysInline(string(jsonBytes), "ANBAR")

	// Verify ANBAR is inline
	expected := `"ANBAR": [2, 0, 0, 0, 0, 0, 0, 0, 0, 0]`
	if !strings.Contains(result, expected) {
		t.Errorf("Expected:\n%s\n\nGot:\n%s", expected, result)
	}

	// Count lines - should be fewer than original
	originalLines := strings.Count(string(jsonBytes), "\n")
	resultLines := strings.Count(result, "\n")

	if resultLines >= originalLines {
		t.Errorf("Expected fewer lines after compacting. Original: %d, Result: %d", originalLines, resultLines)
	}

	t.Logf("Compacted output:\n%s", result)
}

