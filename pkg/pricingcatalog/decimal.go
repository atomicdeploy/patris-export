package pricingcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Decimal preserves the exact base-10 token supplied by configuration or the
// Digitalogic API. It deliberately does not pass pricing inputs through an
// IEEE-754 float before landed_price_v1 evaluates them.
type Decimal string

func NewDecimal(value string) (*Decimal, error) {
	value = strings.TrimSpace(value)
	if !validDecimalText(value) {
		return nil, fmt.Errorf("invalid finite decimal %q", value)
	}
	value = normalizeDecimalText(value)
	decimal := Decimal(value)
	return &decimal, nil
}

func DecimalFromFloat(value float64) *Decimal {
	decimal := Decimal(strconv.FormatFloat(value, 'f', -1, 64))
	return &decimal
}

func (d Decimal) String() string {
	if validDecimalText(string(d)) {
		return normalizeDecimalText(string(d))
	}
	return string(d)
}

func (d Decimal) Float64() (float64, bool) {
	value, err := strconv.ParseFloat(string(d), 64)
	return value, err == nil
}

func (d Decimal) Rat() (*big.Rat, bool) {
	value, ok := new(big.Rat).SetString(strings.TrimSpace(string(d)))
	return value, ok
}

func (d Decimal) MarshalJSON() ([]byte, error) {
	if !validDecimalText(string(d)) {
		return nil, fmt.Errorf("invalid finite decimal %q", d)
	}
	return []byte(normalizeDecimalText(string(d))), nil
}

func (d *Decimal) UnmarshalJSON(data []byte) error {
	if d == nil {
		return fmt.Errorf("nil Decimal receiver")
	}
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) {
		return nil
	}
	var value string
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
	} else {
		value = string(data)
	}
	parsed, err := NewDecimal(value)
	if err != nil {
		return err
	}
	*d = *parsed
	return nil
}

func (d Decimal) MarshalText() ([]byte, error) {
	if !validDecimalText(string(d)) {
		return nil, fmt.Errorf("invalid finite decimal %q", d)
	}
	return []byte(normalizeDecimalText(string(d))), nil
}

func (d *Decimal) UnmarshalText(text []byte) error {
	parsed, err := NewDecimal(string(text))
	if err != nil {
		return err
	}
	*d = *parsed
	return nil
}

func validDecimalText(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "eE/") {
		return false
	}
	_, ok := new(big.Rat).SetString(value)
	return ok
}

func normalizeDecimalText(value string) string {
	value = strings.TrimSpace(value)
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(strings.TrimPrefix(value, "+"), "-")
	parts := strings.SplitN(value, ".", 2)
	integer := strings.TrimLeft(parts[0], "0")
	if integer == "" {
		integer = "0"
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = strings.TrimRight(parts[1], "0")
	}
	if fraction != "" {
		integer += "." + fraction
	}
	if negative && integer != "0" {
		integer = "-" + integer
	}
	return integer
}

func cloneDecimal(value *Decimal) *Decimal {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
