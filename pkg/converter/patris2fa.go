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
func Patris2FaWithMapping(value string, mapping CharMapping) string {
	if mapping == nil {
		mapping = defaultMapping
	}

	// Replace dash character if dashfix is enabled
	if dashFixEnabled {
		value = strings.ReplaceAll(value, "\x99", "-")
	}

	// Reverse specific character sequences (numbers, spaces, special chars, and Farsi chars)
	re := regexp.MustCompile(`([\x9f-\xe0\s\.\(\)\#\:\d\x99]+)`)
	value = re.ReplaceAllStringFunc(value, func(match string) string {
		return reverseString(match)
	})

	// Reverse numbers specifically
	reNum := regexp.MustCompile(`([\d]+)`)
	value = reNum.ReplaceAllStringFunc(value, func(match string) string {
		return reverseString(match)
	})

	// Convert characters using mapping
	var output strings.Builder
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if mapped, ok := mapping[ch]; ok {
			output.WriteString(mapped)
		} else {
			output.WriteByte(ch)
		}
	}

	// Clean up zero-width non-joiners and spaces
	result := output.String()
	result = regexp.MustCompile(`\[zwnj\]\s*`).ReplaceAllString(result, " ")
	result = regexp.MustCompile(`\s+`).ReplaceAllString(result, " ")

	return strings.TrimSpace(result)
}

// reverseString reverses a string
func reverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

// SetDashFix enables or disables dash fix
func SetDashFix(enabled bool) {
	dashFixEnabled = enabled
}
