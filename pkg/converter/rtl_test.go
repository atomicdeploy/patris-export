package converter

import "testing"

func TestConvertLTRVisualToRTL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "empty", input: "", expected: ""},
		{name: "pure English", input: "LAN8720", expected: "LAN8720"},
		{name: "pure Persian", input: "ماژول شبکه", expected: "ماژول شبکه"},
		{
			name:     "pure Persian numbers remain unchanged for now",
			input:    "لیزر میلی وات ولت 5 قرمز 5 نقطه",
			expected: "لیزر میلی وات ولت 5 قرمز 5 نقطه",
		},
		{
			name:     "mixed LAN module from PR comment",
			input:    "LAN8720 ماژول شبکه",
			expected: "ماژول شبکه LAN8720",
		},
		{
			name:     "multiple latin groups",
			input:    "BLUE PILL STM32F103C8T6 ماژول",
			expected: "ماژول BLUE PILL STM32F103C8T6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConvertLTRVisualToRTL(tt.input); got != tt.expected {
				t.Fatalf("ConvertLTRVisualToRTL(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRTLConversionOptIn(t *testing.T) {
	originalMapping := DefaultCharMapping()
	defer SetDefaultMapping(originalMapping)
	defer SetRTLConversion(false)

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
		0xf3: "0",
		0xf5: "2",
		0xfa: "7",
		0xfb: "8",
	}
	SetDefaultMapping(mapping)

	input := "LAN\xfb\xfa\xf5\xf3 \xdb\xd0\xa5\xbc \xd3\xd9\xb8\xa1\xd6"

	SetRTLConversion(false)
	if got := Patris2Fa(input); got != "LAN8720 ماژول شبکه" {
		t.Fatalf("RTL disabled = %q", got)
	}

	SetRTLConversion(true)
	if got := Patris2Fa(input); got != "ماژول شبکه LAN8720" {
		t.Fatalf("RTL enabled = %q", got)
	}
}
