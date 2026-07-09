package converter

import (
	"encoding/hex"
	"fmt"
	"testing"
)

func TestPatris2FaDebug(t *testing.T) {
	// Load mapping
	mapping, err := LoadCharMapping("../../testdata/farsi_chars.txt")
	if err != nil {
		t.Fatal(err)
	}

	// Test strings that should convert
	testCases := []struct {
		input    string
		inputHex string
	}{
		// Add actual hex values from database
		{input: "test1", inputHex: ""},
	}

	for _, tc := range testCases {
		result := Patris2FaWithMapping(tc.input, mapping)
		fmt.Printf("Input: %q\nHex: %s\nOutput: %q\n\n", tc.input, hex.EncodeToString([]byte(tc.input)), result)
	}
}
