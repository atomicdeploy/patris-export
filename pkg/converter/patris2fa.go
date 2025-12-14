package converter

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// CharMapping holds the Patris to Farsi character mappings
type CharMapping map[byte]string

var (
	defaultMapping CharMapping
	dashFixEnabled = true
)

// LoadCharMapping loads the character mapping from a file
func LoadCharMapping(filename string) (CharMapping, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open character mapping file: %w", err)
	}
	defer file.Close()

	mapping := make(CharMapping)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}

		hexVal := strings.TrimSpace(parts[0])
		charVal := strings.TrimSpace(parts[1])

		// Replace * with zero-width non-joiner marker
		charVal = strings.ReplaceAll(charVal, "*", "[zwnj]")

		// Decode hex value to byte
		bytes, err := hex.DecodeString(hexVal)
		if err != nil || len(bytes) != 1 {
			continue
		}

		mapping[bytes[0]] = charVal
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading character mapping file: %w", err)
	}

	return mapping, nil
}

// SetDefaultMapping sets the default character mapping
func SetDefaultMapping(mapping CharMapping) {
	defaultMapping = mapping
}

// Patris2Fa converts Patris-encoded text to Farsi/Persian
func Patris2Fa(value string) string {
	return Patris2FaWithMapping(value, defaultMapping)
}

// Patris2FaWithMapping converts Patris-encoded text to Farsi using a specific mapping
// This EXACTLY matches the PHP implementation from the legacy code (paradox/patris2fa.php)
func Patris2FaWithMapping(value string, mapping CharMapping) string {
	if mapping == nil {
		mapping = defaultMapping
	}

	// Work with bytes throughout to handle Patris encoding correctly
	// Even though C.GoStringN creates a "string", the bytes are preserved
	valueBytes := []byte(value)

	// Replace dash character if dashfix is enabled (matches PHP line 40)
	if dashFixEnabled {
		for i, b := range valueBytes {
			if b == 0x99 {
				valueBytes[i] = '-'
			}
		}
	}

	// Reverse specific character sequences (matches PHP line 42)
	// Pattern: [\x9f-\xe0\xf3-\xfc\s\.\(\)\#\:\d\x99]+ 
	// We must work at byte level because UTF-8 interpretation breaks the pattern matching
	valueBytes = reversePatrisSegments(valueBytes)

	// Convert characters using mapping (matches PHP line 44-47)
	// For unmapped characters, PHP uses utf8_encode() which converts ISO-8859-1 to UTF-8
	var output strings.Builder
	for _, ch := range valueBytes {
		if mapped, ok := mapping[ch]; ok {
			output.WriteString(mapped)
		} else {
			// PHP's utf8_encode() converts ISO-8859-1 byte to Unicode code point
			// In Go, we convert the byte to a rune (Unicode code point) to get the same behavior
			output.WriteRune(rune(ch))
		}
	}

	// NOW reverse ASCII digit sequences (matches PHP line 43)
	// This happens AFTER mapping, creating the "double reversal" effect
	result := output.String()
	re := regexp.MustCompile(`(\d+)`)
	result = re.ReplaceAllStringFunc(result, func(match string) string {
		runes := []rune(match)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return string(runes)
	})

	// Clean up zero-width non-joiners and spaces (matches PHP line 47-48)
	// Replace [zwnj] followed by optional whitespace with a single space
	result = regexp.MustCompile(`\[zwnj\]\s*`).ReplaceAllString(result, " ")
	// Collapse multiple spaces into one
	result = regexp.MustCompile(`\s+`).ReplaceAllString(result, " ")
	result = strings.TrimSpace(result)

	return result
}

// reversePatrisSegments reverses byte segments that match the Patris pattern
// Pattern includes: Farsi bytes (0x9f-0xe0), Persian digits (0xf3-0xfc), whitespace, punctuation
func reversePatrisSegments(data []byte) []byte {
	result := make([]byte, 0, len(data))
	i := 0
	
	for i < len(data) {
		// Check if current byte matches Patris pattern
		if isPatrisByte(data[i]) {
			// Find the end of this Patris segment
			segmentStart := i
			for i < len(data) && isPatrisByte(data[i]) {
				i++
			}
			// Reverse this segment
			for j := i - 1; j >= segmentStart; j-- {
				result = append(result, data[j])
			}
		} else {
			// Non-Patris byte, copy as-is
			result = append(result, data[i])
			i++
		}
	}
	
	return result
}

// reverseDigitSegments reverses pure ASCII digit sequences
// This creates the "double reversal" effect for digits
func reverseDigitSegments(data []byte) []byte {
	result := make([]byte, 0, len(data))
	i := 0
	
	for i < len(data) {
		// Check if current byte is an ASCII digit
		if data[i] >= '0' && data[i] <= '9' {
			// Find the end of this digit sequence
			segmentStart := i
			for i < len(data) && data[i] >= '0' && data[i] <= '9' {
				i++
			}
			// Reverse this segment
			for j := i - 1; j >= segmentStart; j-- {
				result = append(result, data[j])
			}
		} else {
			// Non-digit byte, copy as-is
			result = append(result, data[i])
			i++
		}
	}
	
	return result
}

// isPatrisByte checks if a byte matches the Patris reversal pattern
func isPatrisByte(b byte) bool {
	return (b >= 0x9f && b <= 0xe0) || // Farsi characters
		b == ' ' || b == '.' || b == '(' || b == ')' || b == '#' || b == ':' ||
		(b >= '0' && b <= '9') || // ASCII digits
		b == 0x99 // Dash marker (before replacement)
}

// reverseString reverses a string byte-by-byte (matches PHP strrev behavior)
// This is critical for Patris encoding which uses non-UTF-8 byte sequences
func reverseString(s string) string {
	bytes := []byte(s)
	for i, j := 0, len(bytes)-1; i < j; i, j = i+1, j-1 {
		bytes[i], bytes[j] = bytes[j], bytes[i]
	}
	return string(bytes)
}

// SetDashFix enables or disables dash fix
func SetDashFix(enabled bool) {
	dashFixEnabled = enabled
}
