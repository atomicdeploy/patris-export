package converter

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"
)

// CharMapping holds the Patris to Farsi character mappings
type CharMapping map[byte]string

var (
	defaultMapping CharMapping
	dashFixEnabled = true
	rtlConversionEnabled = false
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

	// Step 6: Apply RTL conversion if enabled
	// Converts from LTR-visual order to RTL-logical order for mixed content
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
		bytes []byte
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
				bytes: data[start:i],
				isPers: true,
			})
		} else {
			start := i
			i++
			segments = append(segments, segment{
				bytes: data[start:i],
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

// SetRTLConversion enables or disables RTL conversion
// When enabled, text is converted from LTR-visual order to RTL-logical order
func SetRTLConversion(enabled bool) {
	rtlConversionEnabled = enabled
}

// ConvertLTRVisualToRTL converts text from LTR-visual order to RTL-logical order
// 
// This function handles mixed Persian/English text that is stored in visual LTR order
// and converts it to logical RTL order for proper display in RTL contexts.
//
// Example:
//   Input:  "LAN8720 ماژول شبکه" (displays correctly in LTR)
//   Output: "ماژول شبکه LAN8720" (displays correctly in RTL)
//
// For Persian text with numbers:
//   Input:  "لیزر میلی وات ولت 5 قرمز 5 نقطه"
//   Output: "لیزر 5 میلی وات 5 ولت قرمز نقطه"
//
// The algorithm:
// 1. Splits text into words
// 2. Groups consecutive same-script words
// 3. Within Persian groups, reverses sequences ending with numbers
// 4. For mixed Persian/English, reverses the order of script groups
func ConvertLTRVisualToRTL(text string) string {
	if text == "" {
		return text
	}
	
	runes := []rune(text)
	
	// First, split into words
	var words [][]rune
	var current []rune
	
	for _, r := range runes {
		if unicode.IsSpace(r) {
			if len(current) > 0 {
				words = append(words, current)
				current = nil
			}
			words = append(words, []rune{r}) // Preserve spaces as separate "words"
		} else {
			current = append(current, r)
		}
	}
	
	if len(current) > 0 {
		words = append(words, current)
	}
	
	// Detect content types
	hasPersian := false
	hasLatin := false
	for _, word := range words {
		if len(word) > 0 && !unicode.IsSpace(word[0]) {
			if isPersianOrArabic(word[0]) {
				hasPersian = true
			} else if !isNumericWord(word) {
				hasLatin = true
			}
		}
	}
	
	// If pure Persian (possibly with numbers), reverse number-ending sequences
	if hasPersian && !hasLatin {
		return reversePersianNumberSequences(words)
	}
	
	// If mixed Persian and Latin, group and reverse
	if hasPersian && hasLatin {
		return reverseScriptGroups(words)
	}
	
	// Pure Latin or empty - return as is
	return text
}

// reversePersianNumberSequences handles pure Persian text with embedded numbers
// It reverses sequences of Persian words that end with a number
func reversePersianNumberSequences(words [][]rune) string {
	var result [][]rune
	var currentSeq [][]rune
	
	for _, word := range words {
		if len(word) > 0 && unicode.IsSpace(word[0]) {
			// Space - add to current sequence
			if len(currentSeq) > 0 {
				currentSeq = append(currentSeq, word)
			} else {
				result = append(result, word)
			}
		} else if isNumericWord(word) {
			// Number - reverse the accumulated sequence with this number
			if len(currentSeq) > 0 {
				// Put number first with a space, then Persian words in reverse with their spaces
				result = append(result, word)
				if len(currentSeq) > 0 && len(currentSeq[len(currentSeq)-1]) > 0 && unicode.IsSpace(currentSeq[len(currentSeq)-1][0]) {
					// Add trailing space after number
					result = append(result, currentSeq[len(currentSeq)-1])
					currentSeq = currentSeq[:len(currentSeq)-1]
				}
				// Reverse Persian words with their preceding spaces
				for i := len(currentSeq) - 1; i >= 0; i-- {
					result = append(result, currentSeq[i])
				}
				currentSeq = nil
			} else {
				result = append(result, word)
			}
		} else {
			// Persian word - add to current sequence
			currentSeq = append(currentSeq, word)
		}
	}
	
	// Flush remaining sequence
	result = append(result, currentSeq...)
	
	// Reconstruct string
	var output []rune
	for _, word := range result {
		output = append(output, word...)
	}
	return string(output)
}

// reverseScriptGroups handles mixed Persian and Latin text
func reverseScriptGroups(words [][]rune) string {
	type wordGroup struct {
		words [][]rune
		isRTL bool
	}
	
	var groups []wordGroup
	var currentGroup wordGroup
	var inGroup bool
	
	for _, word := range words {
		// Skip space-only words for grouping logic
		if len(word) > 0 && unicode.IsSpace(word[0]) {
			if inGroup {
				currentGroup.words = append(currentGroup.words, word)
			} else {
				groups = append(groups, wordGroup{words: [][]rune{word}, isRTL: false})
			}
			continue
		}
		
		// Determine if this word is RTL (Persian/Arabic)
		// Numbers are neutral and join the current group if one exists
		wordIsRTL := len(word) > 0 && isPersianOrArabic(word[0])
		wordIsNumeric := isNumericWord(word)
		
		if !inGroup {
			// Start new group - numbers default to RTL if starting a group
			currentGroup = wordGroup{words: [][]rune{word}, isRTL: wordIsRTL || wordIsNumeric}
			inGroup = true
		} else if wordIsNumeric {
			// Numbers join the current group (neutral behavior)
			currentGroup.words = append(currentGroup.words, word)
		} else if currentGroup.isRTL == wordIsRTL {
			currentGroup.words = append(currentGroup.words, word)
		} else {
			groups = append(groups, currentGroup)
			currentGroup = wordGroup{words: [][]rune{word}, isRTL: wordIsRTL}
		}
	}
	
	if inGroup {
		groups = append(groups, currentGroup)
	}
	
	// Reverse the order of groups
	var result []rune
	for i := len(groups) - 1; i >= 0; i-- {
		for _, word := range groups[i].words {
			result = append(result, word...)
		}
	}
	
	return string(result)
}

// isNumericWord returns true if the word consists only of digits
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

// isPersianOrArabic returns true if the rune is a Persian or Arabic character
func isPersianOrArabic(r rune) bool {
	// Persian/Arabic Unicode blocks
	return (r >= 0x0600 && r <= 0x06FF) || // Arabic
		(r >= 0xFB50 && r <= 0xFDFF) || // Arabic Presentation Forms-A
		(r >= 0xFE70 && r <= 0xFEFF)    // Arabic Presentation Forms-B
}
