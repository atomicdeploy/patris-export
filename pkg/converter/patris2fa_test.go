package converter

import (
	"strings"
	"testing"
)

func TestPatris2Fa(t *testing.T) {
	// Create a simple mapping
	mapping := CharMapping{
		0xa1: "ا",
		0xa2: "آ",
		0xa4: "ب*",
		0xa5: "ب",
		0xb4: "د",
		0xb6: "ر",
		0xb8: "ژ",
		0xd0: "ک",
		0xd2: "گ",
		0xd3: "ل*",
		0xd4: "ل",
		0xd5: "م*",
		0xd6: "م",
		0xd9: "و",
		0xb9: "س*",
		0xba: "س",
		0xbc: "ش",
		0xc4: "ع*",
		0x99: "ـ",
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
			name:     "simple conversion",
			input:    "\xa5\xa1", // Persian bytes in visual order: ب + ا
			expected: "با",        // After byte reversal and mapping: ا + ب = با
		},
		{
			name:     "dash fix",
			input:    "test\x99string",
			expected: "test-string", // Dash replaced, English NOT reversed
		},
		{
			name:     "mixed content",
			input:    "ARDUINO \xa5\xa1",
			expected: "ARDUINO با", // English not reversed, Persian reversed and mapped
		},
		{
			name:     "User test case: BLUE PILL STM32F103C8T6 ماژول",
			input:    "BLUE PILL STM\xf6\xf5F\xf4\xf3\xf6C\xfbT\xf9 \xd4\xd9\xb8\xa1\xd6",
			expected: "BLUE PILL STM32F103C8T6 ماژول",
		},
		{
			name:     "Pure Farsi - ماژول",
			input:    "\xd4\xd9\xb8\xa1\xd6", // ل و ژ ا م (reversed in input) → ماژول after reversal
			expected: "ماژول",
		},
		{
			name:     "LAN8720 ماژول شبکه",
			input:    "LAN8720 \xd4\xd9\xb8\xa1\xd6 \xdc\xd0\xbc", // ماژول شبکه with correct bytes
			expected: "LAN8720 ماژول شبکه",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Patris2Fa(tt.input)
			if result != tt.expected {
				t.Errorf("Patris2Fa(%#v) = %q, want %q", []byte(tt.input), result, tt.expected)
			}
		})
	}
}

func TestLoadCharMapping(t *testing.T) {
	// Create a temporary mapping file
	tempFile := "../../testdata/farsi_chars.txt"

	mapping, err := LoadCharMapping(tempFile)
	if err != nil {
		t.Fatalf("LoadCharMapping failed: %v", err)
	}

	if len(mapping) == 0 {
		t.Error("Expected non-empty mapping")
	}

	// Check for a known mapping
	if val, ok := mapping[0xa1]; !ok || val != "ا" {
		t.Errorf("Expected mapping[0xa1] = 'ا', got %q", val)
	}
}

func TestSetDashFix(t *testing.T) {
	SetDashFix(false)
	SetDefaultMapping(CharMapping{0x99: "ـ"})
	
	result := Patris2Fa("test\x99string")
	if strings.Contains(result, "-") {
		t.Error("Dash fix should be disabled")
	}

	SetDashFix(true)
	result = Patris2Fa("test\x99string")
	if !strings.Contains(result, "-") {
		t.Error("Dash fix should be enabled")
	}
}
