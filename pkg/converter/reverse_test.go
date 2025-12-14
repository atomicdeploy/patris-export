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
