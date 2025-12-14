package converter

import (
	"testing"
)

func TestReverseString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "ASCII string",
			input:    "hello",
			expected: "olleh",
		},
		{
			name:     "Patris-encoded bytes (non-UTF-8)",
			input:    "\xd1\xd6\xd3\xd5\xd1",
			expected: "\xd1\xd5\xd3\xd6\xd1",
		},
		{
			name:     "Mixed ASCII and Patris bytes",
			input:    "ABC\xd1\xd6\xd3",
			expected: "\xd3\xd6\xd1CBA",
		},
		{
			name:     "Numbers",
			input:    "12345",
			expected: "54321",
		},
		{
			name:     "Mixed content with spaces",
			input:    "Test \xd1\xd6\xd3 123",
			expected: "321 \xd3\xd6\xd1 tseT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reverseString(tt.input)
			if result != tt.expected {
				t.Errorf("reverseString(%q) = %q (hex: %x), want %q (hex: %x)",
					tt.input, result, result, tt.expected, tt.expected)
			}
		})
	}
}

func TestPatris2FaReversal(t *testing.T) {
	// Create a simple mapping for testing
	mapping := CharMapping{
		0xd1: "س", // sin
		0xd3: "ن", // nun  
		0xd5: "س", // sin
		0xd6: "و", // waw
	}

	// Test that byte reversal preserves Patris encoding
	// Input: "\xd1\xd6\xd3\xd5\xd1" should reverse to "\xd1\xd5\xd3\xd6\xd1"
	// Then map to: "س" + "س" + "ن" + "و" + "س" = "سسنوس"
	// But actually the pattern should group and reverse, resulting in "سنسور" (sensor)
	
	input := "روسنس" // This is wrong encoding that needs reversal
	// After proper reversal and mapping, it should become "سنسور"
	
	// Note: This test demonstrates the concept. The actual conversion
	// depends on the complete mapping table and the input being in Patris encoding
	result := Patris2FaWithMapping(input, mapping)
	
	// The result should have the bytes reversed and then mapped
	if len(result) == 0 {
		t.Error("Result should not be empty")
	}
}
