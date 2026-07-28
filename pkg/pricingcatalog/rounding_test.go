package pricingcatalog

import (
	"context"
	"encoding/json"
	"testing"
)

func TestStaticRoundingDigitsDistinguishOmittedZeroNullAndInvalid(t *testing.T) {
	tests := []struct {
		name             string
		payload          string
		wantDigits       *int
		wantExplicitNull bool
		wantWarning      string
	}{
		{
			name:       "omitted defaults to whole IRT",
			payload:    `{"mode":"static","static":{"default_assignment":{"profit_percent":30}}}`,
			wantDigits: integerPointer(0),
		},
		{
			name:       "explicit zero remains zero",
			payload:    `{"mode":"static","static":{"rounding_digits":0,"default_assignment":{"profit_percent":30}}}`,
			wantDigits: integerPointer(0),
		},
		{
			name:             "explicit null is preserved and fails closed",
			payload:          `{"mode":"static","static":{"rounding_digits":null,"default_assignment":{"profit_percent":30}}}`,
			wantExplicitNull: true,
			wantWarning:      "price_rounding_digits_explicit_null",
		},
		{
			name:        "negative is invalid",
			payload:     `{"mode":"static","static":{"rounding_digits":-1,"default_assignment":{"profit_percent":30}}}`,
			wantWarning: "price_rounding_digits_invalid",
		},
		{
			name:        "above maximum is invalid",
			payload:     `{"mode":"static","static":{"rounding_digits":10,"default_assignment":{"profit_percent":30}}}`,
			wantWarning: "price_rounding_digits_invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var config Config
			if err := json.Unmarshal([]byte(test.payload), &config); err != nil {
				t.Fatal(err)
			}
			resolution := NewProvider(config).Resolve(context.Background(), "A")
			if test.wantDigits == nil {
				if resolution.RoundingDigits != nil {
					t.Fatalf("rounding digits = %d, want unavailable", *resolution.RoundingDigits)
				}
			} else if resolution.RoundingDigits == nil || *resolution.RoundingDigits != *test.wantDigits {
				t.Fatalf("rounding digits = %v, want %d", resolution.RoundingDigits, *test.wantDigits)
			}
			if resolution.ExplicitNulls["price_rounding_digits"] != test.wantExplicitNull {
				t.Fatalf("explicit-null state = %t, want %t", resolution.ExplicitNulls["price_rounding_digits"], test.wantExplicitNull)
			}
			if test.wantWarning != "" && !contains(resolution.Warnings, test.wantWarning) {
				t.Fatalf("warnings %v do not contain %s", resolution.Warnings, test.wantWarning)
			}
		})
	}
}

func TestStaticRoundingNullSurvivesJSONRoundTrip(t *testing.T) {
	var config Config
	if err := json.Unmarshal([]byte(`{"mode":"static","static":{"rounding_digits":null}}`), &config); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"mode":"static","static":{"rounding_digits":null},"digitalogic":{}}` {
		t.Fatalf("explicit null collapsed during round trip: %s", encoded)
	}
	if !Configured(config) {
		t.Fatal("an explicit null rounding reference must remain distinguishable from standalone omission")
	}
}

func integerPointer(value int) *int { return &value }
