package converter

import (
	"testing"
)

func TestConvertLTRVisualToRTL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "pure English",
			input:    "LAN8720",
			expected: "LAN8720",
		},
		{
			name:     "pure Persian",
			input:    "ماژول",
			expected: "ماژول",
		},
		{
			name:     "pure Persian multi-word",
			input:    "ماژول شبکه",
			expected: "ماژول شبکه",
		},
		{
			name:     "English then Persian - issue example",
			input:    "LAN8720 ماژول شبکه",
			expected: "ماژول شبکه LAN8720",
		},
		{
			name:     "English then Persian - simple",
			input:    "ARDUINO با",
			expected: "با ARDUINO",
		},
		{
			name:     "Persian then English",
			input:    "ماژول LAN8720",
			expected: "LAN8720 ماژول",
		},
		{
			name:     "Multiple segments",
			input:    "STM32 ماژول BLUE PILL",
			expected: "PILL BLUE ماژول STM32",
		},
		{
			name:     "Three words mixed",
			input:    "BLUE PILL ماژول",
			expected: "ماژول PILL BLUE",
		},
		{
			name:     "Multiple spaces",
			input:    "LAN8720  ماژول شبکه",
			expected: "ماژول شبکه  LAN8720",
		},
		{
			name:     "Complex example from issue",
			input:    "BLUE PILL STM32F103C8T6 ماژول",
			expected: "ماژول STM32F103C8T6 PILL BLUE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertLTRVisualToRTL(tt.input)
			if result != tt.expected {
				t.Errorf("ConvertLTRVisualToRTL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSetRTLConversion(t *testing.T) {
	// Create a simple mapping for testing
	mapping := CharMapping{
		0xa1: "ا",
		0xa5: "ب",
		0xb8: "ژ",
		0xd0: "ک",
		0xd3: "ل",
		0xd6: "م",
		0xd9: "و",
		0xdb: "ه",
		0xbc: "ش",
		// Persian digits
		0xf3: "0",
		0xf4: "1",
		0xf5: "2",
		0xf6: "3",
		0xf7: "4",
		0xf8: "5",
		0xf9: "6",
		0xfa: "7",
		0xfb: "8",
		0xfc: "9",
	}

	SetDefaultMapping(mapping)

	tests := []struct {
		name              string
		input             string
		rtlEnabled        bool
		expectedWithRTL   string
		expectedWithoutRTL string
	}{
		{
			name:              "LAN8720 ماژول شبکه with RTL",
			input:             "\x4c\x41\x4e\xfb\xfa\xf5\xf3\x20\xdb\xd0\xa5\xbc\x20\xd3\xd9\xb8\xa1\xd6",
			rtlEnabled:        true,
			expectedWithRTL:   "ماژول شبکه LAN8720",
			expectedWithoutRTL: "LAN8720 ماژول شبکه",
		},
		{
			name:              "Mixed content",
			input:             "ARDUINO \xa1\xa5",
			rtlEnabled:        true,
			expectedWithRTL:   "با ARDUINO",
			expectedWithoutRTL: "ARDUINO با",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test with RTL disabled
			SetRTLConversion(false)
			result := Patris2FaWithMapping(tt.input, mapping)
			if result != tt.expectedWithoutRTL {
				t.Errorf("With RTL disabled: Patris2Fa(%#v) = %q, want %q", []byte(tt.input), result, tt.expectedWithoutRTL)
			}

			// Test with RTL enabled
			SetRTLConversion(true)
			result = Patris2FaWithMapping(tt.input, mapping)
			if result != tt.expectedWithRTL {
				t.Errorf("With RTL enabled: Patris2Fa(%#v) = %q, want %q", []byte(tt.input), result, tt.expectedWithRTL)
			}
		})
	}

	// Reset to default
	SetRTLConversion(false)
}

func TestIsPersianOrArabic(t *testing.T) {
	tests := []struct {
		char     rune
		expected bool
	}{
		// Persian/Arabic characters
		{'م', true},
		{'ا', true},
		{'ژ', true},
		{'و', true},
		{'ل', true},
		{'ش', true},
		{'ب', true},
		{'ک', true},
		{'ه', true},
		// Latin characters
		{'A', false},
		{'Z', false},
		{'a', false},
		{'z', false},
		// Numbers
		{'0', false},
		{'9', false},
		// Special characters
		{' ', false},
		{'-', false},
		{'_', false},
	}

	for _, tt := range tests {
		t.Run(string(tt.char), func(t *testing.T) {
			result := isPersianOrArabic(tt.char)
			if result != tt.expected {
				t.Errorf("isPersianOrArabic(%q) = %v, want %v", tt.char, result, tt.expected)
			}
		})
	}
}
