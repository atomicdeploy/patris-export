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
		0x99: "ـ",
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
			input:    "\xa5\xa1", // Persian bytes in visual order
			expected: "با",        // After byte reversal and mapping: با
		},
		{
			name:     "dash fix",
			input:    "test\x99string",
			expected: "tset-gnirts", // English stays as-is, only Persian sequences reversed
		},
		{
			name:     "mixed content",
			input:    "ARDUINO \xa1\xa5",
			expected: "ARDUINO با", // English not reversed, Persian correctly mapped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Patris2Fa(tt.input)
			if result != tt.expected {
				t.Errorf("Patris2Fa(%q) = %q, want %q", tt.input, result, tt.expected)
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
