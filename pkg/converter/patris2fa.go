package converter

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// CharMapping holds the Patris to Farsi character mappings
type CharMapping map[byte]string

type CharMappingEntry struct {
	Hex        string   `json:"hex"`
	Decimal    int      `json:"decimal"`
	Character  string   `json:"character"`
	Raw        string   `json:"raw,omitempty"`
	Codepoints []string `json:"codepoints"`
}

type CharMappingIssue struct {
	Line    int    `json:"line"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

var (
	defaultMapping       CharMapping
	dashFixEnabled       = true
	rtlConversionEnabled = false
)

// LoadCharMapping loads the character mapping from a file
func LoadCharMapping(filename string) (CharMapping, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open character mapping file: %w", err)
	}
	defer file.Close()

	return ParseCharMapping(file)
}

func parseCharMapping(reader io.Reader) (CharMapping, error) {
	mapping, _, err := ParseCharMappingReport(reader)
	return mapping, err
}

// ParseCharMapping loads a character mapping from a reader.
func ParseCharMapping(reader io.Reader) (CharMapping, error) {
	mapping, _, err := ParseCharMappingReport(reader)
	return mapping, err
}

// ParseCharMappingReport loads a character mapping and reports lines that were
// ignored because they do not match the Patris charmap text format.
func ParseCharMappingReport(reader io.Reader) (CharMapping, []CharMappingIssue, error) {
	mapping := make(CharMapping)
	issues := []CharMappingIssue{}
	scanner := bufio.NewScanner(reader)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			issues = append(issues, CharMappingIssue{Line: lineNumber, Content: line, Reason: "expected hex and character separated by a tab"})
			continue
		}

		hexVal := strings.TrimSpace(parts[0])
		charVal := strings.TrimSpace(parts[1])

		// Replace * with zero-width non-joiner marker
		charVal = strings.ReplaceAll(charVal, "*", "[zwnj]")

		// Decode hex value to byte
		bytes, err := hex.DecodeString(hexVal)
		if err != nil || len(bytes) != 1 {
			issues = append(issues, CharMappingIssue{Line: lineNumber, Content: line, Reason: "hex value must decode to exactly one byte"})
			continue
		}

		mapping[bytes[0]] = charVal
	}

	if err := scanner.Err(); err != nil {
		return nil, issues, fmt.Errorf("error reading character mapping file: %w", err)
	}

	return mapping, issues, nil
}

// DefaultCharMapping returns a copy of the embedded default Patris81 character mapping.
func DefaultCharMapping() CharMapping {
	copied := make(CharMapping, len(defaultMapping))
	for key, value := range defaultMapping {
		copied[key] = value
	}
	return copied
}

func CharMappingEntries(mapping CharMapping) []CharMappingEntry {
	entries := make([]CharMappingEntry, 0, len(mapping))
	for b, char := range mapping {
		entries = append(entries, CharMappingEntry{
			Hex:        fmt.Sprintf("%02X", b),
			Decimal:    int(b),
			Character:  char,
			Codepoints: codepoints(char),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Decimal < entries[j].Decimal
	})
	return entries
}

func codepoints(value string) []string {
	points := []string{}
	for _, r := range value {
		points = append(points, fmt.Sprintf("U+%04X", r))
	}
	return points
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

	if rtlConversionEnabled {
		result = ConvertLTRVisualToRTL(result)
	}

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

// SetRTLConversion enables or disables optional conversion from LTR visual order
// into RTL logical order for mixed Persian/Latin display contexts.
func SetRTLConversion(enabled bool) {
	rtlConversionEnabled = enabled
}

// RTLConversionEnabled reports whether optional RTL logical conversion is enabled.
func RTLConversionEnabled() bool {
	return rtlConversionEnabled
}

// ConvertLTRVisualToRTL converts already-decoded text from Patris-style LTR
// visual order into RTL logical order. It is intentionally opt-in because some
// current UIs still expect the legacy visual ordering.
func ConvertLTRVisualToRTL(text string) string {
	if text == "" {
		return text
	}

	words := splitWords(text)
	hasPersian := false
	hasLatin := false
	for _, word := range words {
		if isSpaceWord(word) {
			continue
		}
		if isPersianOrArabic(word[0]) {
			hasPersian = true
		} else if !isNumericWord(word) {
			hasLatin = true
		}
	}

	if hasPersian && hasLatin {
		return reverseScriptGroups(words)
	}
	return text
}

func splitWords(text string) [][]rune {
	var words [][]rune
	var current []rune
	for _, r := range []rune(text) {
		if unicode.IsSpace(r) {
			if len(current) > 0 {
				words = append(words, current)
				current = nil
			}
			words = append(words, []rune{r})
			continue
		}
		current = append(current, r)
	}
	if len(current) > 0 {
		words = append(words, current)
	}
	return words
}

func reverseScriptGroups(words [][]rune) string {
	type wordGroup struct {
		words [][]rune
		isRTL bool
	}

	var groups []wordGroup
	var current wordGroup
	inGroup := false

	for _, word := range words {
		if isSpaceWord(word) {
			continue
		}

		wordIsRTL := isPersianOrArabic(word[0])
		wordIsNumeric := isNumericWord(word)
		if !inGroup {
			current = wordGroup{words: [][]rune{word}, isRTL: wordIsRTL || wordIsNumeric}
			inGroup = true
			continue
		}
		if wordIsNumeric || current.isRTL == wordIsRTL {
			current.words = append(current.words, word)
			continue
		}
		groups = append(groups, current)
		current = wordGroup{words: [][]rune{word}, isRTL: wordIsRTL}
	}
	if inGroup {
		groups = append(groups, current)
	}

	var result [][]rune
	for i := len(groups) - 1; i >= 0; i-- {
		result = append(result, groups[i].words...)
	}
	return joinWords(result)
}

func joinWords(words [][]rune) string {
	var output []rune
	for i, word := range words {
		if i > 0 {
			output = append(output, ' ')
		}
		output = append(output, word...)
	}
	return string(output)
}

func isSpaceWord(word []rune) bool {
	return len(word) > 0 && unicode.IsSpace(word[0])
}

func isNumericWord(word []rune) bool {
	if len(word) == 0 {
		return false
	}
	for _, r := range word {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func isPersianOrArabic(r rune) bool {
	return (r >= 0x0600 && r <= 0x06FF) ||
		(r >= 0xFB50 && r <= 0xFDFF) ||
		(r >= 0xFE70 && r <= 0xFEFF)
}
