package canonical

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

func TestPriceRoundingUsesNearestHalfUpAtConfiguredTrailingDigits(t *testing.T) {
	tests := []struct {
		name       string
		partnerIRR string
		digits     int
		want       int64
	}{
		{name: "below midpoint rounds down", partnerIRR: "1234490", digits: 2, want: 123400},
		{name: "midpoint rounds up", partnerIRR: "1234500", digits: 2, want: 123500},
		{name: "above midpoint rounds up", partnerIRR: "1234560", digits: 2, want: 123500},
		{name: "zero digits preserves whole IRT behavior", partnerIRR: "1234560", digits: 0, want: 123456},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := PartnerPrice(test.partnerIRR, "0", test.digits)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("PartnerPrice(%s, digits=%d) = %d, want %d", test.partnerIRR, test.digits, got, test.want)
			}
		})
	}

	for _, digits := range []int{-1, 10} {
		if _, err := PartnerPrice("1000", "0", digits); err == nil {
			t.Fatalf("invalid rounding digits %d were accepted", digits)
		}
		if _, err := LandedPrice("1", "1", pricingcatalog.CurrencyCNY, "1", "0", "1", digits); err == nil {
			t.Fatalf("CNY formula accepted invalid rounding digits %d", digits)
		}
	}
}

func TestPartnerPriceFallbackUsesFOROSHAsIRRWithoutFreightOrFX(t *testing.T) {
	digits := 2
	markup := pricingcatalog.Decimal("30")
	config := pricingcatalog.Config{Mode: pricingcatalog.ModeStatic, Static: pricingcatalog.StaticConfig{
		RoundingDigits:    &digits,
		DefaultAssignment: &pricingcatalog.Assignment{ProfitPercent: &markup},
	}}
	product := parseKalaProduct(context.Background(), map[string]interface{}{
		"Code":   "PARTNER-1",
		"FOROSH": json.Number("1234500"),
		"Sharh1": "0 0 0 0",
	}, pricingcatalog.NewProvider(config), true)
	row := product.Map()

	for field, want := range map[string]interface{}{
		"foreign_price":         json.Number("0"),
		"sale_price_source":     float64(1234500),
		"price_source_amount":   json.Number("1234500"),
		"price_source_currency": pricingcatalog.CurrencyIRR,
		"price_source_kind":     PriceSourceKindPartner,
		"price_rounding_digits": 2,
		"price_rounding_mode":   pricingcatalog.RoundingModeHalfUp,
		"final_price":           int64(160500),
	} {
		if !reflect.DeepEqual(row[field], want) {
			t.Fatalf("%s = %#v, want %#v; row=%#v", field, row[field], want, row)
		}
	}
	warnings := row["warnings"].([]string)
	for _, warning := range []string{
		"foreign_price_non_positive",
		"freight_not_applied_for_partner_price",
		"partner_price_fallback_used",
	} {
		if !hasAny(warnings, warning) {
			t.Fatalf("fallback warnings %v do not contain %s", warnings, warning)
		}
	}
	for _, irrelevant := range []string{"fx_rate_missing", "shipping_method_missing", "shipping_price_per_kg_missing"} {
		if hasAny(warnings, irrelevant) {
			t.Fatalf("partner-price calculation retained irrelevant warning %s: %v", irrelevant, warnings)
		}
	}
}

func TestPartnerPriceFallbackAcceptsLargeParadoxFloat32WithoutExponentAmbiguity(t *testing.T) {
	digits := 2
	markup := pricingcatalog.Decimal("30")
	config := pricingcatalog.Config{Mode: pricingcatalog.ModeStatic, Static: pricingcatalog.StaticConfig{
		RoundingDigits:    &digits,
		DefaultAssignment: &pricingcatalog.Assignment{ProfitPercent: &markup},
	}}
	row := parseKalaProduct(context.Background(), map[string]interface{}{
		"Code": "103133", "FOROSH": float32(5500000), "foreign_price": float32(0),
	}, pricingcatalog.NewProvider(config), true).Map()
	for field, want := range map[string]interface{}{
		"price_source_amount":   json.Number("5500000"),
		"price_source_currency": pricingcatalog.CurrencyIRR,
		"price_source_kind":     PriceSourceKindPartner,
		"final_price":           int64(715000),
	} {
		if !reflect.DeepEqual(row[field], want) {
			t.Fatalf("%s = %#v, want %#v; row=%#v", field, row[field], want, row)
		}
	}
	if hasAny(row["warnings"].([]string), "partner_price_invalid") {
		t.Fatalf("valid float32 FOROSH was rejected: %v", row["warnings"])
	}
}

func TestCNYPriceRemainsPreferredOverPositivePartnerPrice(t *testing.T) {
	digits := 2
	fx := pricingcatalog.Decimal("30000")
	freight := pricingcatalog.Decimal("100")
	markup := pricingcatalog.Decimal("30")
	enabled := true
	config := pricingcatalog.Config{Mode: pricingcatalog.ModeStatic, Static: pricingcatalog.StaticConfig{
		CNYToIRT:       &fx,
		RoundingDigits: &digits,
		Methods: []pricingcatalog.Method{{
			ID: "air", Enabled: &enabled, PricePerKg: &freight, Currency: pricingcatalog.CurrencyCNY,
		}},
		DefaultAssignment: &pricingcatalog.Assignment{MethodID: "air", ProfitPercent: &markup},
	}}
	product := parseKalaProduct(context.Background(), map[string]interface{}{
		"Code": "CNY-1", "foreign_price": "10", "weight_grams": "1000", "FOROSH": 999999999,
	}, pricingcatalog.NewProvider(config), true)
	row := product.Map()
	if row["price_source_kind"] != PriceSourceKindForeign || row["price_source_currency"] != pricingcatalog.CurrencyCNY || row["price_source_amount"] != json.Number("10") {
		t.Fatalf("CNY did not retain source precedence: %#v", row)
	}
	if row["final_price"] != int64(4290000) {
		t.Fatalf("CNY final price = %#v, want 4290000", row["final_price"])
	}
	if hasAny(row["warnings"].([]string), "partner_price_fallback_used") {
		t.Fatalf("positive partner price displaced CNY: %v", row["warnings"])
	}
}

func TestZeroAndNullSourceFactsRemainDistinctButAreNotSelected(t *testing.T) {
	markup := pricingcatalog.Decimal("30")
	config := pricingcatalog.Config{Mode: pricingcatalog.ModeStatic, Static: pricingcatalog.StaticConfig{
		DefaultAssignment: &pricingcatalog.Assignment{ProfitPercent: &markup},
	}}
	provider := pricingcatalog.NewProvider(config)

	zero := parseKalaProduct(context.Background(), map[string]interface{}{
		"Code": "ZERO", "foreign_price": 0, "sale_price_source": 0,
	}, provider, true).Map()
	for _, field := range []string{"foreign_price", "sale_price_source"} {
		if value, exists := zero[field]; !exists || value == nil {
			t.Fatalf("explicit zero %s was not preserved: %#v", field, zero)
		}
	}
	for _, field := range []string{"price_source_amount", "price_source_currency", "price_source_kind", "final_price"} {
		if _, exists := zero[field]; exists {
			t.Fatalf("non-positive source generated %s: %#v", field, zero)
		}
	}
	for _, warning := range []string{"foreign_price_non_positive", "partner_price_non_positive", "final_price_unavailable"} {
		if !hasAny(zero["warnings"].([]string), warning) {
			t.Fatalf("zero diagnostics %v do not contain %s", zero["warnings"], warning)
		}
	}

	explicitNull := parseKalaProduct(context.Background(), map[string]interface{}{
		"Code": "NULL", "foreign_price": nil, "sale_price_source": nil,
	}, provider, true).Map()
	for _, field := range []string{"foreign_price", "sale_price_source"} {
		if value, exists := explicitNull[field]; !exists || value != nil {
			t.Fatalf("explicit null %s was not preserved: %#v", field, explicitNull)
		}
	}
	if !hasAny(explicitNull["warnings"].([]string), "partner_price_explicit_null") {
		t.Fatalf("explicit-null partner diagnostic missing: %v", explicitNull["warnings"])
	}

	omitted := parseKalaProduct(context.Background(), map[string]interface{}{"Code": "OMITTED"}, provider, true).Map()
	for _, field := range []string{"foreign_price", "sale_price_source"} {
		if _, exists := omitted[field]; exists {
			t.Fatalf("omitted source became %s: %#v", field, omitted)
		}
	}
	if !hasAny(omitted["warnings"].([]string), "partner_price_missing") {
		t.Fatalf("omitted partner diagnostic missing: %v", omitted["warnings"])
	}
}

func TestExplicitNullRoundingReferenceIsPreservedWithoutGeneratedNulls(t *testing.T) {
	var config pricingcatalog.Config
	if err := json.Unmarshal([]byte(`{"mode":"static","static":{"rounding_digits":null,"default_assignment":{"profit_percent":30}}}`), &config); err != nil {
		t.Fatal(err)
	}
	row := parseKalaProduct(context.Background(), map[string]interface{}{
		"Code": "ROUND-NULL", "FOROSH": 100000,
	}, pricingcatalog.NewProvider(config), true).Map()
	if value, exists := row["price_rounding_digits"]; !exists || value != nil {
		t.Fatalf("explicit null rounding reference was not preserved: %#v", row)
	}
	for field, want := range map[string]interface{}{
		"price_source_amount":   json.Number("100000"),
		"price_source_currency": pricingcatalog.CurrencyIRR,
		"price_source_kind":     PriceSourceKindPartner,
	} {
		if !reflect.DeepEqual(row[field], want) {
			t.Fatalf("selected source fact %s = %#v, want %#v; row=%#v", field, row[field], want, row)
		}
	}
	for _, absent := range []string{"price_rounding_mode", "final_price"} {
		if _, exists := row[absent]; exists {
			t.Fatalf("unavailable derived field %s was generated: %#v", absent, row)
		}
	}
	if !hasAny(row["warnings"].([]string), "price_rounding_digits_explicit_null") {
		t.Fatalf("rounding null diagnostic missing: %v", row["warnings"])
	}
}

func TestSelectedPriceSourceContractRejectsPartialNullZeroAndInvalidCurrency(t *testing.T) {
	payloads := []string{
		`{"product_code":"A","price_source_amount":1,"warnings":[]}`,
		`{"product_code":"A","price_source_amount":null,"price_source_currency":"CNY","price_source_kind":"foreign_price","warnings":[]}`,
	}
	for _, payload := range payloads {
		var product Product
		if err := json.Unmarshal([]byte(payload), &product); err == nil {
			t.Fatalf("invalid selected source was accepted: %s", payload)
		}
	}

	for _, test := range []struct {
		currency string
		kind     string
	}{
		{currency: "USD", kind: PriceSourceKindForeign},
		{currency: pricingcatalog.CurrencyIRR, kind: PriceSourceKindForeign},
		{currency: pricingcatalog.CurrencyCNY, kind: PriceSourceKindPartner},
	} {
		amount := pricingcatalog.Decimal("1")
		digits := 0
		product := Product{
			ProductCode:         "A",
			PriceSourceAmount:   &amount,
			PriceSourceCurrency: test.currency,
			PriceSourceKind:     test.kind,
			PriceRoundingDigits: &digits,
			PriceRoundingMode:   pricingcatalog.RoundingModeHalfUp,
			Warnings:            []string{},
			fieldPresence: map[string]fieldPresence{
				"price_source_amount":   fieldValue,
				"price_source_currency": fieldValue,
				"price_source_kind":     fieldValue,
				"price_rounding_digits": fieldValue,
				"price_rounding_mode":   fieldValue,
			},
		}
		product.RecordHash = recordHash(product)
		if err := validateProductIdentity(product, 0); err == nil {
			t.Fatalf("invalid selected-source currency/kind pair was accepted: %+v", test)
		}
	}
}
