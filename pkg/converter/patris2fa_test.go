package converter

import (
	"strings"
	"testing"
)

func TestPatris2Fa(t *testing.T) {
	originalMapping := DefaultCharMapping()
	defer SetDefaultMapping(originalMapping)

	// Create a simple mapping - using [zwnj] markers like the embedded map
	mapping := CharMapping{
		0xa1: "ا",
		0xa2: "آ",
		0xa4: "ب[zwnj]",
		0xa5: "ب",
		0xb4: "د",
		0xb6: "ر",
		0xb8: "ژ",
		0xd0: "ک",
		0xd2: "گ",
		0xd3: "ل[zwnj]",
		0xd4: "ل",
		0xd5: "م[zwnj]",
		0xd6: "م",
		0xd7: "ن[zwnj]",
		0xd9: "و",
		0xdb: "ه[zwnj]",
		0xdc: "ه",
		0xb9: "س[zwnj]",
		0xba: "س",
		0xbc: "ش",
		0xc4: "ع[zwnj]",
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
			input:    "\xa1\xa5", // Persian bytes in visual order: ا + ب (reversed from reading order)
			expected: "با",       // After byte reversal and mapping: ب + ا = با
		},
		{
			name:     "dash fix",
			input:    "test\x99string",
			expected: "test-string", // Dash replaced, English NOT reversed
		},
		{
			name:     "mixed content",
			input:    "ARDUINO \xa1\xa5",
			expected: "ARDUINO با", // English not reversed, Persian reversed and mapped
		},
		{
			name:     "User test case: BLUE PILL STM32F103C8T6 ماژول",
			input:    "BLUE PILL STM\xf6\xf5F\xf4\xf3\xf6C\xfbT\xf9 \xd3\xd9\xb8\xa1\xd6",
			expected: "BLUE PILL STM32F103C8T6 ماژول",
		},
		{
			name:     "Pure Farsi - ماژول",
			input:    "\xd3\xd9\xb8\xa1\xd6", // ل[zwnj] و ژ ا م (reversed in input) → ماژول after reversal
			expected: "ماژول",
		},
		{
			name:     "LAN8720 ماژول شبکه - User's actual data",
			input:    "\x4c\x41\x4e\xfb\xfa\xf5\xf3\x20\xdb\xd0\xa5\xbc\x20\xd3\xd9\xb8\xa1\xd6",
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

func TestPatris2FaPreservesLineBoundariesAndHorizontalWhitespace(t *testing.T) {
	mapping := CharMapping{
		0xa1: "A",
		0xa2: "B",
		0xa3: "C",
		0xa4: "D",
	}

	input := "\xa1\xa2\r\n\xa3\xa4  \tvalue\rplain\n\nlast"
	want := "BA\nDC  \tvalue\nplain\n\nlast"
	if got := Patris2FaWithMapping(input, mapping); got != want {
		t.Fatalf("Patris2FaWithMapping(%#v) = %q, want %q", []byte(input), got, want)
	}
}

func TestLoadCharMapping(t *testing.T) {
	mapping, err := LoadCharMapping("farsi_chars.txt")
	if err != nil {
		t.Fatalf("LoadCharMapping failed: %v", err)
	}

	assertPatrisMapping(t, mapping)
}

func TestDefaultCharMappingUsesEmbeddedFile(t *testing.T) {
	assertPatrisMapping(t, DefaultCharMapping())
}

func assertPatrisMapping(t *testing.T, mapping CharMapping) {
	t.Helper()

	if len(mapping) < 60 {
		t.Fatalf("Expected full Patris81 mapping, got %d entries", len(mapping))
	}

	if _, ok := mapping[0xa1]; !ok {
		t.Error("Expected mapping for Patris byte 0xa1")
	}
	if _, ok := mapping[0xfc]; !ok {
		t.Error("Expected mapping for Patris digit byte 0xfc")
	}
}

func TestSetDashFix(t *testing.T) {
	originalMapping := DefaultCharMapping()
	defer SetDefaultMapping(originalMapping)
	defer SetDashFix(true)

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
