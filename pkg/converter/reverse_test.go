package converter

import (
	"testing"
)

// TestPatris2FaReversal tests the actual Patris2Fa behavior
// NOTE: The reverseString() function is an internal implementation detail
// It's only called on matched Farsi patterns, NOT on the entire input
func TestPatris2FaReversal(t *testing.T) {
	// Create a simple mapping for testing
	mapping := CharMapping{
		0xd1: "س", // sin
		0xd3: "ن", // nun  
		0xd5: "س", // sin
		0xd6: "و", // waw
		0xa5: "ب", // beh
		0xa1: "ا", // alef
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "English text not reversed",
			input:    "hello",
			expected: "hello",
		},
		{
			name:     "Persian bytes reversed in pattern",
			input:    "\xd1\xd6\xd3\xd5\xd1", // Patris-encoded bytes
			expected: "سسنوس",                // After reversal and mapping
		},
		{
			name:     "Mixed content",
			input:    "ARDUINO \xa5\xa1",
			expected: "ARDUINO با", // English unchanged, Persian reversed and mapped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Patris2FaWithMapping(tt.input, mapping)
			if result != tt.expected {
				t.Errorf("Patris2FaWithMapping(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
