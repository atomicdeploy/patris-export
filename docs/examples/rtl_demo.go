// Example demonstrating RTL text conversion
// This file can be run independently to verify the RTL conversion feature
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

// ConvertLTRVisualToRTL converts text from LTR-visual order to RTL-logical order
func ConvertLTRVisualToRTL(text string) string {
if text == "" {
return text
}

runes := []rune(text)

// Segment by whitespace into words
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

// Check if we have mixed content
hasRTL := false
hasLTR := false
for _, word := range words {
if len(word) > 0 && !unicode.IsSpace(word[0]) {
if isPersianOrArabic(word[0]) {
hasRTL = true
} else {
hasLTR = true
}
}
}

// Only reverse if we have both RTL and LTR content
if !hasRTL || !hasLTR {
return text
}

// Reverse the word order
var result []rune
for i := len(words) - 1; i >= 0; i-- {
result = append(result, words[i]...)
}

return string(result)
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
