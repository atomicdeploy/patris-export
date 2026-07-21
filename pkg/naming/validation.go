package naming

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

const warningPrefix = "naming_"

// InternalWarningsField carries pre-conversion validation results through the
// canonical transformer. It is consumed before any public row is produced.
const InternalWarningsField = "_patris_naming_warnings"

var textFields = map[string]struct{}{
	"description": {},
	"name":        {},
	"part_number": {},
	"sharh":       {},
	"sharh1":      {},
	"sharh2":      {},
}

// Summary describes naming-convention violations without retaining source
// values. It is safe for CLI diagnostics and logs.
type Summary struct {
	Rows       int
	Violations int
}

// Warnings returns deterministic, field-scoped warning codes for user-entered
// naming and description fields. Values are never included in a warning.
func Warnings(row map[string]interface{}) []string {
	if len(row) == 0 {
		return []string{}
	}
	fields := make([]string, 0, len(row))
	for field := range row {
		if _, ok := textFields[strings.ToLower(strings.TrimSpace(field))]; ok {
			fields = append(fields, field)
		}
	}
	sort.Strings(fields)

	warnings := make([]string, 0)
	seen := map[string]struct{}{}
	for _, field := range fields {
		value, ok := row[field].(string)
		if !ok || value == "" {
			continue
		}
		fieldID := canonicalFieldID(field)
		for _, rule := range violatedRules(value) {
			warning := fmt.Sprintf("%s%s:%s", warningPrefix, rule, fieldID)
			if _, duplicate := seen[warning]; duplicate {
				continue
			}
			seen[warning] = struct{}{}
			warnings = append(warnings, warning)
		}
	}
	sort.Strings(warnings)
	return warnings
}

func canonicalFieldID(field string) string {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "name", "part_number":
		return "name"
	case "description", "sharh", "sharh1", "sharh2":
		return "description"
	default:
		return "text"
	}
}

// Attach adds naming warnings to row warning arrays while preserving existing
// diagnostics. It returns a secret-safe aggregate for CLI reporting.
func Attach(rows []map[string]interface{}) Summary {
	summary := Summary{}
	for _, row := range rows {
		warnings := Warnings(row)
		if len(warnings) == 0 {
			continue
		}
		row["warnings"] = Merge(row["warnings"], warnings)
		summary.Rows++
		summary.Violations += len(warnings)
	}
	return summary
}

// Merge combines existing diagnostics with naming warnings deterministically.
func Merge(existing interface{}, additions []string) []string {
	return mergeWarnings(existing, additions)
}

// Summarize counts attached naming warnings without exposing row contents.
func Summarize(rows []map[string]interface{}) Summary {
	summary := Summary{}
	for _, row := range rows {
		count := 0
		for _, warning := range warningStrings(row["warnings"]) {
			if strings.HasPrefix(warning, warningPrefix) {
				count++
			}
		}
		if count > 0 {
			summary.Rows++
			summary.Violations += count
		}
	}
	return summary
}

func violatedRules(value string) []string {
	rules := make([]string, 0, 4)
	runes := []rune(value)
	if len(runes) == 0 {
		return rules
	}
	if unicode.IsSpace(runes[0]) {
		rules = append(rules, "leading_space")
	}
	if unicode.IsSpace(runes[len(runes)-1]) {
		rules = append(rules, "trailing_space")
	}
	if strings.Contains(value, "  ") {
		rules = append(rules, "multiple_spaces")
	}
	if hasUnseparatedKindTransition(runes) {
		rules = append(rules, "mixed_kind_without_space")
	}
	return rules
}

func hasUnseparatedKindTransition(runes []rune) bool {
	previous := runeKind(0)
	for _, r := range runes {
		current := classifyRune(r)
		if previous != 0 && current != 0 && previous != current {
			return true
		}
		previous = current
	}
	return false
}

type runeKind uint8

const (
	kindPersian runeKind = iota + 1
	kindEnglish
	kindDigit
)

func classifyRune(r rune) runeKind {
	switch {
	case unicode.Is(unicode.Arabic, r) && unicode.IsLetter(r):
		return kindPersian
	case r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z':
		return kindEnglish
	case unicode.IsDigit(r):
		return kindDigit
	default:
		return 0
	}
}

func mergeWarnings(existing interface{}, additions []string) []string {
	seen := map[string]struct{}{}
	for _, warning := range append(warningStrings(existing), additions...) {
		warning = strings.TrimSpace(warning)
		if warning != "" {
			seen[warning] = struct{}{}
		}
	}
	merged := make([]string, 0, len(seen))
	for warning := range seen {
		merged = append(merged, warning)
	}
	sort.Strings(merged)
	return merged
}

func warningStrings(value interface{}) []string {
	switch warnings := value.(type) {
	case []string:
		return append([]string(nil), warnings...)
	case []interface{}:
		values := make([]string, 0, len(warnings))
		for _, warning := range warnings {
			if text, ok := warning.(string); ok {
				values = append(values, text)
			}
		}
		return values
	case string:
		if strings.TrimSpace(warnings) != "" {
			return []string{warnings}
		}
	}
	return nil
}
