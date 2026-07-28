package canonical

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

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

func TestPartnerPriceFallbackUsesSharh1SlotOneAsIRRWithoutFreightOrFX(t *testing.T) {
	digits := 2
	markup := pricingcatalog.Decimal("30")
	config := pricingcatalog.Config{Mode: pricingcatalog.ModeStatic, Static: pricingcatalog.StaticConfig{
		RoundingDigits:    &digits,
		DefaultAssignment: &pricingcatalog.Assignment{ProfitPercent: &markup},
	}}
	product := parseKalaProduct(context.Background(), map[string]interface{}{
		"Code":   "PARTNER-1",
		"FOROSH": json.Number("9999990"),
		"Sharh1": "1234500\r0\r0\r0",
	}, pricingcatalog.NewProvider(config), true)
	row := product.Map()

	for field, want := range map[string]interface{}{
		"foreign_price":                  json.Number("0"),
		"sale_price_source":              float64(9999990),
		"partner_price_source":           json.Number("1234500"),
		"price_source_amount":            json.Number("1234500"),
		"price_source_currency":          pricingcatalog.CurrencyIRR,
		"price_source_kind":              PriceSourceKindPartner,
		"shipping_method_id":             pricingcatalog.MethodDomestic,
		"shipping_price_per_kg":          json.Number("0"),
		"shipping_price_per_kg_currency": pricingcatalog.CurrencyIRR,
		"price_rounding_digits":          2,
		"price_rounding_mode":            pricingcatalog.RoundingModeHalfUp,
		"final_price":                    int64(160500),
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
		"Code": "103133", "partner_price_source": float32(5500000), "FOROSH": float32(9000000), "foreign_price": float32(0),
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
		"Code": "CNY-1", "foreign_price": "10", "weight_grams": "1000",
		"partner_price_source": 888888888, "FOROSH": 999999999,
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

func TestPricingFallbackOrderRequiresCompleteForeignRouteThenPartnerThenOptInSale(t *testing.T) {
	digits := 2
	fx := pricingcatalog.Decimal("30000")
	freight := pricingcatalog.Decimal("22000000")
	markup := pricingcatalog.Decimal("30")
	enabled := true
	base := pricingcatalog.Config{Mode: pricingcatalog.ModeStatic, Static: pricingcatalog.StaticConfig{
		CNYToIRT:       &fx,
		RoundingDigits: &digits,
		Methods: []pricingcatalog.Method{{
			ID: "air_express", Enabled: &enabled, PricePerKg: &freight, Currency: pricingcatalog.CurrencyIRR,
		}},
		DefaultAssignment: &pricingcatalog.Assignment{MethodID: "air_express", ProfitPercent: &markup},
	}}

	partner := parseKalaProduct(context.Background(), map[string]interface{}{
		"Code": "PARTNER-FALLBACK", "foreign_price": 10, "partner_price_source": 7000, "FOROSH": 12000,
	}, pricingcatalog.NewProvider(base), true).Map()
	for field, want := range map[string]interface{}{
		"price_source_amount":            json.Number("7000"),
		"price_source_kind":              PriceSourceKindPartner,
		"final_price":                    int64(900),
		"shipping_method_id":             pricingcatalog.MethodDomestic,
		"shipping_price_per_kg":          json.Number("0"),
		"shipping_price_per_kg_currency": pricingcatalog.CurrencyIRR,
	} {
		if !reflect.DeepEqual(partner[field], want) {
			t.Fatalf("partner fallback %s = %#v, want %#v; row=%#v", field, partner[field], want, partner)
		}
	}
	if !hasAny(partner["warnings"].([]string), "foreign_price_path_unavailable") {
		t.Fatalf("missing foreign-route diagnostic: %v", partner["warnings"])
	}
	zeroWeight := parseKalaProduct(context.Background(), map[string]interface{}{
		"Code": "ZERO-WEIGHT", "foreign_price": 10, "weight_grams": 0, "partner_price_source": 7000,
	}, pricingcatalog.NewProvider(base), true).Map()
	if zeroWeight["weight_grams"] != json.Number("0") || zeroWeight["price_source_kind"] != PriceSourceKindPartner {
		t.Fatalf("explicit zero weight was not preserved while falling through: %#v", zeroWeight)
	}
	if !hasAny(zeroWeight["warnings"].([]string), "weight_non_positive_for_foreign_price") {
		t.Fatalf("zero-weight foreign fallback lacked a diagnostic: %v", zeroWeight["warnings"])
	}

	directConfig := base
	directConfig.UseSalePriceDirectFallback = true
	direct := parseKalaProduct(context.Background(), map[string]interface{}{
		"Code": "SALE-FALLBACK", "foreign_price": 10, "FOROSH": 12000,
	}, pricingcatalog.NewProvider(directConfig), true, true).Map()
	for field, want := range map[string]interface{}{
		"price_source_amount":            json.Number("12000"),
		"price_source_kind":              PriceSourceKindSaleDirect,
		"final_price":                    int64(1200),
		"shipping_method_id":             pricingcatalog.MethodDomestic,
		"shipping_price_per_kg":          json.Number("0"),
		"shipping_price_per_kg_currency": pricingcatalog.CurrencyIRR,
	} {
		if !reflect.DeepEqual(direct[field], want) {
			t.Fatalf("direct fallback %s = %#v, want %#v; row=%#v", field, direct[field], want, direct)
		}
	}
	for _, omitted := range []string{"markup_percent", "irt_per_cny", "price_rounding_digits", "price_rounding_mode"} {
		if _, exists := direct[omitted]; exists {
			t.Fatalf("direct fallback emitted unused %s: %#v", omitted, direct)
		}
	}

	noMarkup := directConfig
	noMarkup.Static.DefaultAssignment = &pricingcatalog.Assignment{MethodID: "air_express"}
	directAfterUnusablePartner := parseKalaProduct(context.Background(), map[string]interface{}{
		"Code": "PARTNER-INCOMPLETE", "partner_price_source": 7000, "FOROSH": 12000,
	}, pricingcatalog.NewProvider(noMarkup), true, true).Map()
	if directAfterUnusablePartner["price_source_kind"] != PriceSourceKindSaleDirect ||
		directAfterUnusablePartner["final_price"] != int64(1200) {
		t.Fatalf("incomplete partner path did not continue to direct fallback: %#v", directAfterUnusablePartner)
	}
	if !hasAny(directAfterUnusablePartner["warnings"].([]string), "markup_percent_missing") {
		t.Fatalf("direct fallback lost partner-path diagnostic: %v", directAfterUnusablePartner["warnings"])
	}

	disabled := parseKalaProduct(context.Background(), map[string]interface{}{
		"Code": "SALE-DISABLED", "FOROSH": 12000,
	}, pricingcatalog.NewProvider(base), true).Map()
	for _, absent := range []string{"price_source_amount", "price_source_currency", "price_source_kind", "final_price"} {
		if _, exists := disabled[absent]; exists {
			t.Fatalf("disabled direct fallback emitted %s: %#v", absent, disabled)
		}
	}
}

func TestDirectSalePriceUsesExactIRRToIRTConversionWithoutRounding(t *testing.T) {
	if got, err := DirectSalePrice("123450"); err != nil || got != 12345 {
		t.Fatalf("DirectSalePrice = %d, %v; want 12345", got, err)
	}
	if _, err := DirectSalePrice("123456"); err == nil {
		t.Fatal("fractional IRT direct sale value was silently rounded")
	}
}

func TestTransformPassesConfiguredDirectSaleFallback(t *testing.T) {
	config := DefaultConfig()
	config.SourceID = "direct-test"
	config.Pricing = pricingcatalog.Config{
		Mode:                       pricingcatalog.ModeStatic,
		UseSalePriceDirectFallback: true,
	}
	rows, envelope := Transform(context.Background(), []map[string]interface{}{{
		"Code": "123456", "Name": "Direct product", "Serial": "DIRECT-1", "FOROSH": 12000,
	}}, "kala.db", config, nil, time.Unix(1, 0).UTC())
	if len(rows) != 1 || len(envelope.Products) != 1 {
		t.Fatalf("direct transform product count = %d/%d", len(rows), len(envelope.Products))
	}
	if rows[0]["price_source_kind"] != PriceSourceKindSaleDirect || rows[0]["final_price"] != int64(1200) {
		t.Fatalf("configured direct fallback did not reach transform: %#v", rows[0])
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifySnapshotJSON(encoded); err != nil {
		t.Fatalf("direct fallback snapshot did not verify: %v", err)
	}
}

func TestZeroAndNullSourceFactsRemainDistinctButAreNotSelected(t *testing.T) {
	markup := pricingcatalog.Decimal("30")
	config := pricingcatalog.Config{Mode: pricingcatalog.ModeStatic, Static: pricingcatalog.StaticConfig{
		DefaultAssignment: &pricingcatalog.Assignment{ProfitPercent: &markup},
	}}
	provider := pricingcatalog.NewProvider(config)

	zero := parseKalaProduct(context.Background(), map[string]interface{}{
		"Code": "ZERO", "foreign_price": 0, "partner_price_source": 0, "sale_price_source": 0,
	}, provider, true).Map()
	for _, field := range []string{"foreign_price", "partner_price_source", "sale_price_source"} {
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
		"Code": "NULL", "foreign_price": nil, "partner_price_source": nil, "sale_price_source": nil,
	}, provider, true).Map()
	for _, field := range []string{"foreign_price", "partner_price_source", "sale_price_source"} {
		if value, exists := explicitNull[field]; !exists || value != nil {
			t.Fatalf("explicit null %s was not preserved: %#v", field, explicitNull)
		}
	}
	if !hasAny(explicitNull["warnings"].([]string), "partner_price_explicit_null") {
		t.Fatalf("explicit-null partner diagnostic missing: %v", explicitNull["warnings"])
	}

	omitted := parseKalaProduct(context.Background(), map[string]interface{}{"Code": "OMITTED"}, provider, true).Map()
	for _, field := range []string{"foreign_price", "partner_price_source", "sale_price_source"} {
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
		"Code": "ROUND-NULL", "partner_price_source": 100000,
	}, pricingcatalog.NewProvider(config), true).Map()
	if value, exists := row["price_rounding_digits"]; !exists || value != nil {
		t.Fatalf("explicit null rounding reference was not preserved: %#v", row)
	}
	for _, absent := range []string{"price_source_amount", "price_source_currency", "price_source_kind", "price_rounding_mode", "final_price"} {
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
