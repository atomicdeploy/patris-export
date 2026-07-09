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

// Patris2FaWithMapping converts Patris81-encoded text to Persian/Farsi
//
// Patris81 Encoding Scheme:
// - Uses byte values 0x9F-0xE0 for Persian characters
// - Uses byte values 0xF3-0xFC for Persian digits 0-9
// - Stores text in visual (LTR) byte order, reversed from logical reading order
// - Uses 0x99 as a dash marker that can be converted to '-'
// - May include [zwnj] markers for zero-width non-joiners
//
// Conversion Process:
// 1. Replace dash markers (0x99) with '-' if enabled
// 2. Reverse Persian character and digit byte segments
// 3. Map Patris bytes to UTF-8 Persian characters
// 4. Re-reverse digit sequences to restore correct number order
// 5. Clean up spacing and zero-width non-joiners
func Patris2FaWithMapping(value string, mapping CharMapping) string {
	if mapping == nil {
		mapping = defaultMapping
	}

	valueBytes := []byte(value)

	// Step 1: Replace dash marker if enabled
	if dashFixEnabled {
		for i, b := range valueBytes {
			if b == 0x99 {
				valueBytes[i] = '-'
			}
		}
	}

	// Step 2: Reverse Patris-encoded segments
	// Persian characters (0x9F-0xE0) and whitespace/punctuation are stored reversed
	// English letters are NOT reversed, allowing mixed Persian/English text
	valueBytes = reversePatrisSegments(valueBytes)

	// Step 3: Map Patris bytes to UTF-8
	var output strings.Builder
	for _, b := range valueBytes {
		if mapped, ok := mapping[b]; ok {
			output.WriteString(mapped)
		} else {
			// Unmapped bytes are converted as ISO-8859-1 to Unicode
			output.WriteRune(rune(b))
		}
	}

	// Step 4: No digit re-reversal needed
	// Since Persian digit bytes (0xF3-0xFC) are not reversed in step 2,
	// they map directly to the correct digit order
	result := output.String()

	// Step 5: Clean up formatting
	// Replace [zwnj] markers with spaces for proper Persian word spacing
	result = regexp.MustCompile(`\[zwnj\]\s*`).ReplaceAllString(result, " ")
	// Normalize whitespace
	result = regexp.MustCompile(`\s+`).ReplaceAllString(result, " ")
	result = strings.TrimSpace(result)

	return result
}

// reversePatrisSegments reverses byte segments containing Patris-encoded characters
//
// The Patris81 encoding stores Persian text with segment AND byte reversal:
// 1. Persian word segments appear in reversed order
// 2. Bytes within each Persian segment are also reversed
//
// This function:
// 1. Identifies segments (Persian vs non-Persian)
// 2. Collects Persian segments and reverses their order AND bytes
// 3. Rebuilds string with reversed Persian segments
func reversePatrisSegments(data []byte) []byte {
	type segment struct {
		bytes  []byte
		isPers bool
	}

	// Step 1: Identify all segments
	var segments []segment
	i := 0

	for i < len(data) {
		if isPatrisByte(data[i]) {
			start := i
			for i < len(data) && isPatrisByte(data[i]) {
				i++
			}
			segments = append(segments, segment{
				bytes:  data[start:i],
				isPers: true,
			})
		} else {
			start := i
			i++
			segments = append(segments, segment{
				bytes:  data[start:i],
				isPers: false,
			})
		}
	}

	// Step 2: Collect Persian segments and reverse them
	var persSegments [][]byte
	for _, seg := range segments {
		if seg.isPers {
			// Reverse bytes within segment
			reversed := make([]byte, len(seg.bytes))
			for j := 0; j < len(seg.bytes); j++ {
				reversed[j] = seg.bytes[len(seg.bytes)-1-j]
			}
			persSegments = append(persSegments, reversed)
		}
	}

	// Reverse order of Persian segments
	for i, j := 0, len(persSegments)-1; i < j; i, j = i+1, j-1 {
		persSegments[i], persSegments[j] = persSegments[j], persSegments[i]
	}

	// Step 3: Rebuild string with reversed Persian segments
	var result []byte
	persIdx := 0
	for _, seg := range segments {
		if seg.isPers {
			result = append(result, persSegments[persIdx]...)
			persIdx++
		} else {
			result = append(result, seg.bytes...)
		}
	}

	return result
}

// isPatrisByte returns true if the byte should be part of a reversed Patris segment
func isPatrisByte(b byte) bool {
	// Only Persian characters (0x9F-0xE0) - NOT digits!
	// Persian digits (0xF3-0xFC) are already in correct visual order
	return b >= 0x9f && b <= 0xe0
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
