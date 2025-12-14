// Example demonstrating RTL text conversion
// This file can be run independently to verify the RTL conversion feature
//
// NOTE: This is a standalone demo with the algorithm copied inline.
// The actual implementation is in github.com/atomicdeploy/patris-export/pkg/converter
package main

import (
"fmt"
"unicode"
)

// isPersianOrArabic returns true if the rune is a Persian or Arabic character
func isPersianOrArabic(r rune) bool {
return (r >= 0x0600 && r <= 0x06FF) || 
(r >= 0xFB50 && r <= 0xFDFF) || 
(r >= 0xFE70 && r <= 0xFEFF)
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

// reversePersianNumberSequences handles pure Persian text with embedded numbers
func reversePersianNumberSequences(words [][]rune) string {
var result [][]rune
var currentSeq [][]rune

for _, word := range words {
if len(word) > 0 && unicode.IsSpace(word[0]) {
continue
}

if isNumericWord(word) {
if len(currentSeq) > 0 {
reversedSeq := [][]rune{word}
for i := len(currentSeq) - 1; i >= 0; i-- {
reversedSeq = append(reversedSeq, currentSeq[i])
}
result = append(result, reversedSeq...)
currentSeq = nil
} else {
result = append(result, word)
}
} else {
currentSeq = append(currentSeq, word)
}
}

result = append(result, currentSeq...)

var output []rune
for i, word := range result {
if i > 0 {
output = append(output, ' ')
}
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
if len(word) > 0 && unicode.IsSpace(word[0]) {
continue
}

wordIsRTL := len(word) > 0 && isPersianOrArabic(word[0])
wordIsNumeric := isNumericWord(word)

if !inGroup {
currentGroup = wordGroup{words: [][]rune{word}, isRTL: wordIsRTL || wordIsNumeric}
inGroup = true
} else if wordIsNumeric {
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

var result []rune
for i := len(groups) - 1; i >= 0; i-- {
for j, word := range groups[i].words {
if j > 0 {
result = append(result, ' ')
}
result = append(result, word...)
}
if i > 0 {
result = append(result, ' ')
}
}

return string(result)
}

// ConvertLTRVisualToRTL converts text from LTR-visual order to RTL-logical order
func ConvertLTRVisualToRTL(text string) string {
if text == "" {
return text
}

runes := []rune(text)

var words [][]rune
var current []rune

for _, r := range runes {
if unicode.IsSpace(r) {
if len(current) > 0 {
words = append(words, current)
current = nil
}
words = append(words, []rune{r})
} else {
current = append(current, r)
}
}

if len(current) > 0 {
words = append(words, current)
}

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

if hasPersian && !hasLatin {
return reversePersianNumberSequences(words)
}

if hasPersian && hasLatin {
return reverseScriptGroups(words)
}

return text
}

func main() {
fmt.Println("╔═══════════════════════════════════════════════════════════╗")
fmt.Println("║           RTL Text Conversion Demonstration              ║")
fmt.Println("╚═══════════════════════════════════════════════════════════╝")
fmt.Println()

examples := []struct {
desc     string
input    string
expected string
}{
{
desc:     "Issue Example - LAN8720 ماژول شبکه",
input:    "LAN8720 ماژول شبکه",
expected: "ماژول شبکه LAN8720",
},
{
desc:     "Issue 104001001 - Persian with numbers",
input:    "لیزر میلی وات ولت 5 قرمز 5 نقطه",
expected: "لیزر 5 میلی وات 5 ولت قرمز نقطه",
},
{
desc:     "Simple Mixed - ARDUINO با",
input:    "ARDUINO با",
expected: "با ARDUINO",
},
{
desc:     "Complex Example - BLUE PILL STM32F103C8T6 ماژول",
input:    "BLUE PILL STM32F103C8T6 ماژول",
expected: "ماژول STM32F103C8T6 PILL BLUE",
},
{
desc:     "Pure Persian - ماژول شبکه",
input:    "ماژول شبکه",
expected: "ماژول شبکه",
},
{
desc:     "Pure English - LAN8720",
input:    "LAN8720",
expected: "LAN8720",
},
}

allPassed := true
for i, ex := range examples {
result := ConvertLTRVisualToRTL(ex.input)
passed := result == ex.expected

status := "✓ PASS"
if !passed {
status = "✗ FAIL"
allPassed = false
}

fmt.Printf("%d. %s %s\n", i+1, status, ex.desc)
fmt.Printf("   Input (LTR Visual):   %s\n", ex.input)
fmt.Printf("   Output (RTL Logical): %s\n", result)
fmt.Printf("   Expected:             %s\n", ex.expected)

if !passed {
fmt.Printf("   ❌ MISMATCH!\n")
}
fmt.Println()
}

fmt.Println("═══════════════════════════════════════════════════════════")
if allPassed {
fmt.Println("✓ All tests PASSED!")
} else {
fmt.Println("✗ Some tests FAILED!")
}
fmt.Println("═══════════════════════════════════════════════════════════")
}
