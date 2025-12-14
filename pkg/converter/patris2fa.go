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

	// Replace dash character if dashfix is enabled (matches PHP line 40)
	if dashFixEnabled {
		value = strings.ReplaceAll(value, "\x99", "-")
	}

	// Reverse specific character sequences (matches PHP line 42)
	// Pattern: [\x9f-\xe0\xf3-\xfc\s\.\(\)\#\:\d\x99]+ 
	// Includes: Farsi chars (0x9f-0xe0), Persian digits (0xf3-0xfc), whitespace, dot, parens, hash, colon, ASCII digits, and 0x99
	// NOTE: Does NOT include English letters A-Za-z
	re := regexp.MustCompile(`([\x9f-\xe0\xf3-\xfc\s\.\(\)\#\:\d\x99]+)`)
	value = re.ReplaceAllStringFunc(value, func(match string) string {
		return reverseString(match)
	})

	// Reverse ASCII digit sequences specifically (matches PHP line 43)
	reNum := regexp.MustCompile(`([\d]+)`)
	value = reNum.ReplaceAllStringFunc(value, func(match string) string {
		return reverseString(match)
	})

	// Convert characters using mapping (matches PHP line 44-47)
	// For unmapped characters, PHP uses utf8_encode() which in Go means keeping them as-is
	var output strings.Builder
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if mapped, ok := mapping[ch]; ok {
			output.WriteString(mapped)
		} else {
			// Keep unmapped characters as-is (equivalent to PHP's utf8_encode for ASCII)
			output.WriteByte(ch)
		}
	}

	// Clean up zero-width non-joiners and spaces (matches PHP line 47-48)
	result := output.String()
	// Replace [zwnj] followed by optional whitespace with a single space
	result = regexp.MustCompile(`\[zwnj\]\s*`).ReplaceAllString(result, " ")
	// Collapse multiple spaces into one
	result = regexp.MustCompile(`\s+`).ReplaceAllString(result, " ")
	result = strings.TrimSpace(result)

	return result
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
