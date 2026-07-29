package canonical

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/recorddiff"
	"gopkg.in/yaml.v3"
)

type fixedResolutionProvider struct {
	resolution pricingcatalog.Resolution
}

func (provider fixedResolutionProvider) Resolve(context.Context, string) pricingcatalog.Resolution {
	return provider.resolution
}

func canonicalTestConfig(code string) (Config, pricingcatalog.Provider) {
	fx := pricingcatalog.Decimal("29000")
	rate := pricingcatalog.Decimal("120")
	markup := pricingcatalog.Decimal("30")
	enabled := true
	cfg := DefaultConfig()
	cfg.Pricing = pricingcatalog.Config{
		Mode: pricingcatalog.ModeStatic,
		Static: pricingcatalog.StaticConfig{
			Revision:           "fixture-r1",
			CNYToIRT:           &fx,
			SelectedWarehouses: []string{"1", "2"},
			Methods: []pricingcatalog.Method{{
				ID: "air_express", Enabled: &enabled, PricePerKg: &rate, Currency: pricingcatalog.CurrencyCNY,
			}},
			Assignments: map[string]pricingcatalog.Assignment{
				code: {MethodID: "air_express", ProfitPercent: &markup},
			},
		},
	}
	return cfg, pricingcatalog.NewProvider(cfg.Pricing)
}

func TestSourceIdentityMatchesCanonicalCrossPlatformNaming(t *testing.T) {
	revision := "sha256:" + strings.Repeat("a", 64)
	for name, source := range map[string]string{
		"windows": `C:\Patris\data4\KALA.DB`,
		"url":     "https://example.invalid/export/KALA.DB",
	} {
		t.Run(name, func(t *testing.T) {
			got := SourceIdentity(source, "patris-office", revision)
			want := (Source{ID: "patris-office", Dataset: "kala.db", Revision: revision})
			if got != want {
				t.Fatalf("source identity=%#v, want %#v", got, want)
			}
		})
	}
	if got := SourceIdentity(`C:\Patris\data4\KALA.DB`, " ", revision); got.ID != "kala.db" {
		t.Fatalf("default source ID=%q, want kala.db", got.ID)
	}
}

func TestLandedPriceExactWorkbookFixture(t *testing.T) {
	got, err := LandedPrice("240", "120", pricingcatalog.CurrencyCNY, "24.5", "30", "29000", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2009410 {
		t.Fatalf("landed_price = %d, want 2009410", got)
	}
}

func TestLandedPriceTreatsEquivalentCNYAndIRRFreightEqually(t *testing.T) {
	cny, err := LandedPrice("1000", "100", pricingcatalog.CurrencyCNY, "10", "30", "30000", 0)
	if err != nil {
		t.Fatal(err)
	}
	irr, err := LandedPrice("1000", "30000000", pricingcatalog.CurrencyIRR, "10", "30", "30000", 0)
	if err != nil {
		t.Fatal(err)
	}
	if cny != 4290000 || irr != cny {
		t.Fatalf("equivalent freight diverged: CNY=%d IRR=%d, want 4290000 IRT", cny, irr)
	}
}

func TestTransformKeepsExactDecimalInputsThroughFinalRounding(t *testing.T) {
	fx := pricingcatalog.Decimal("1000000000000000000")
	freight := pricingcatalog.Decimal("0.000000000000000001")
	markup := pricingcatalog.Decimal("0")
	enabled := true
	cfg := DefaultConfig()
	cfg.Pricing = pricingcatalog.Config{Mode: pricingcatalog.ModeStatic, Static: pricingcatalog.StaticConfig{
		Revision: "exact-r1", CNYToIRT: &fx,
		Methods:     []pricingcatalog.Method{{ID: "air", Enabled: &enabled, PricePerKg: &freight, Currency: pricingcatalog.CurrencyCNY}},
		Assignments: map[string]pricingcatalog.Assignment{"A": {MethodID: "air", ProfitPercent: &markup}},
	}}
	rows, envelope := Transform(context.Background(), []map[string]interface{}{{
		"Code": "A", "foreign_price": "0.1000000000000000006", "weight_grams": "1", "ALLANBAR": 1,
	}}, "kala.db", cfg, pricingcatalog.NewProvider(cfg.Pricing), time.Unix(1, 0))
	if len(rows) != 1 || rows[0]["final_price"] != int64(100000000000000001) {
		t.Fatalf("exact decimal inputs were rounded before the final stage: %#v", rows)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"foreign_price":0.1000000000000000006`) {
		t.Fatalf("typed contract did not preserve exact decimal token: %s", encoded)
	}
}

func TestSharh1ExtraNumericSlotsAreAmbiguousAndNull(t *testing.T) {
	warnings := []string{}
	if got := foreignPriceFromDescription("0 0 0 24.5 99", &warnings); got != nil {
		t.Fatalf("extra Sharh1 slot produced a price: %v", got)
	}
	for _, expected := range []string{"foreign_price_extra_slots", "foreign_price_ambiguous"} {
		if !hasAny(warnings, expected) {
			t.Fatalf("missing %s warning: %v", expected, warnings)
		}
	}
}

func TestLandedPriceRoundsOnceHalfUpAndRejectsInvalidInput(t *testing.T) {
	got, err := LandedPrice("0", "0", pricingcatalog.CurrencyCNY, "0.005", "0", "100", 0)
	if err != nil || got != 1 {
		t.Fatalf("half-up result = %d, %v; want 1", got, err)
	}
	for _, invalid := range []string{"", "NaN", "Inf", "-1", "1e999999"} {
		if _, err := LandedPrice(invalid, "1", pricingcatalog.CurrencyCNY, "1", "1", "1", 0); err == nil {
			t.Fatalf("invalid decimal %q was accepted", invalid)
		}
	}
	if _, err := LandedPrice("1", "1", "USD", "1", "1", "1", 0); err == nil {
		t.Fatal("unsupported shipping currency was accepted")
	}
	for _, currency := range []string{" CNY", "CNY ", "\tIRR"} {
		if _, err := LandedPrice("1", "1", currency, "1", "1", "1", 0); err == nil {
			t.Fatalf("non-lexical shipping currency %q was accepted", currency)
		}
	}
}

func TestWhitespaceShippingCurrencyNeverProducesFinalPrice(t *testing.T) {
	staticConfig := pricingcatalog.Config{}
	if err := json.Unmarshal([]byte(`{"mode":"static","static":{"cny_to_irt":30000,"shipping_methods":[{"id":"air","price_per_kg":120,"currency":" CNY "}],"assignments":{"A":{"shipping_method_id":"air","profit_percent":30}}}}`), &staticConfig); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/integration/catalog" {
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","revision":"r1","currency":{"local":"IRT","cny_to_local":30000,"cny_to_irt":30000},"pricing":{"formula_id":"landed_price"},"shipping_methods":[{"id":"air","price_per_kg":120,"currency":"CNY "}]}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"shipping_method_id":"air","profit_percent":30,"profit_percent_source":"global_default","pricing_warnings":[]}}`)
	}))
	defer server.Close()
	httpConfig := pricingcatalog.Config{Mode: pricingcatalog.ModeDigitalogic, Digitalogic: pricingcatalog.DigitalogicConfig{BaseURL: server.URL}}

	for name, test := range map[string]struct {
		provider pricingcatalog.Provider
	}{
		"static": {provider: pricingcatalog.NewProvider(staticConfig)},
		"HTTP":   {provider: pricingcatalog.NewProvider(httpConfig)},
	} {
		t.Run(name, func(t *testing.T) {
			row := parseKalaProduct(context.Background(), map[string]interface{}{
				"Code": "A", "foreign_price": "10", "weight_grams": "1000", "ALLANBAR": 1,
			}, test.provider, true).Map()
			if _, exists := row["final_price"]; exists {
				t.Fatalf("invalid currency produced final_price: %#v", row)
			}
			warnings := row["warnings"].([]string)
			if !hasAny(warnings, "shipping_price_per_kg_currency_invalid") || !hasAny(warnings, "final_price_unavailable") {
				t.Fatalf("invalid currency warnings missing: %v", warnings)
			}
		})
	}
}

func TestStaticConfigMixedShippingNullsRemainExplicitInProducts(t *testing.T) {
	tests := []struct {
		name         string
		decode       func(*pricingcatalog.Config) error
		wantPrice    interface{}
		wantCurrency interface{}
	}{
		{
			name: "JSON amount and null currency",
			decode: func(cfg *pricingcatalog.Config) error {
				return json.Unmarshal([]byte(`{"mode":"static","static":{"cny_to_irt":30000,"shipping_methods":[{"id":"air","price_per_kg":120,"currency":null}],"assignments":{"A":{"shipping_method_id":"air","profit_percent":30}}}}`), cfg)
			},
			wantPrice: json.Number("120"), wantCurrency: nil,
		},
		{
			name: "YAML null amount and currency",
			decode: func(cfg *pricingcatalog.Config) error {
				return yaml.Unmarshal([]byte("mode: static\nstatic:\n  cny_to_irt: 30000\n  shipping_methods:\n    - id: air\n      price_per_kg: null\n      currency: CNY\n  assignments:\n    A:\n      shipping_method_id: air\n      profit_percent: 30\n"), cfg)
			},
			wantPrice: nil, wantCurrency: pricingcatalog.CurrencyCNY,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var pricing pricingcatalog.Config
			if err := test.decode(&pricing); err != nil {
				t.Fatal(err)
			}
			row := parseKalaProduct(context.Background(), map[string]interface{}{
				"Code": "A", "foreign_price": "10", "weight_grams": "1000", "ALLANBAR": 1,
			}, pricingcatalog.NewProvider(pricing), true).Map()
			if value, exists := row["shipping_price_per_kg"]; !exists || !reflect.DeepEqual(value, test.wantPrice) {
				t.Fatalf("shipping price presence/value = %#v, present %t; want %#v", value, exists, test.wantPrice)
			}
			if value, exists := row["shipping_price_per_kg_currency"]; !exists || !reflect.DeepEqual(value, test.wantCurrency) {
				t.Fatalf("shipping currency presence/value = %#v, present %t; want %#v", value, exists, test.wantCurrency)
			}
			if _, exists := row["final_price"]; exists {
				t.Fatalf("mixed null shipping pair produced final_price: %#v", row)
			}
		})
	}
}

func TestKalaProfileTransformsPersianFixtureWithoutRawBoundaryFields(t *testing.T) {
	code := "113007045"
	cfg, provider := canonicalTestConfig(code)
	rows := []map[string]interface{}{{
		"Code":     code,
		"Name":     "ماژول آزمون",
		"Serial":   "SER-1",
		"Vahed":    "عدد",
		"Sharh1":   "۰ ۰ ۰ ۲۴٫۵",
		"Sharh2":   "قفسه ۱۲ - گرم ۲۴۰ ۰ ۰ ۰",
		"FOROSH":   1000,
		"KHARYD":   900,
		"ALLANBAR": 99,
		"ANBAR":    []interface{}{2, 3, 0},
		"Sefaresh": 1,
	}}
	generated := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	products, envelope := Transform(context.Background(), rows, `C:\Patris\kala.db`, cfg, provider, generated)
	if len(products) != 1 {
		t.Fatalf("products = %d, want 1", len(products))
	}
	product := products[0]
	if product["product_code"] != code {
		t.Fatalf("Code was not preserved as immutable string: %#v", product["product_code"])
	}
	if fmt.Sprint(product["foreign_price"]) != "24.5" || fmt.Sprint(product["weight_grams"]) != "240" || product["final_price"] != int64(2009410) {
		t.Fatalf("unexpected canonical pricing: %#v", product)
	}
	if product["total_stock"] != 5.0 {
		t.Fatalf("selected warehouse stock = %#v, want 5", product["total_stock"])
	}
	if !strings.Contains(product["location"].(string), "قفسه 12") {
		t.Fatalf("location was not retained and digit-normalized: %q", product["location"])
	}
	if warnings, exists := product["warnings"]; !exists || len(warnings.([]string)) != 0 {
		t.Fatalf("evaluated empty warnings field was not preserved: %v", warnings)
	}
	for _, forbidden := range []string{"Sharh1", "Sharh2", "FOROSH", "KHARYD", "ALLANBAR", "ANBAR", "Code"} {
		if _, exists := product[forbidden]; exists {
			t.Fatalf("raw field %q crossed product-sync boundary: %#v", forbidden, product)
		}
	}
	if envelope.Schema != ContractName || envelope.Source.Dataset != "kala.db" || envelope.Source.ID != "kala.db" {
		t.Fatalf("unexpected contract/source: %+v", envelope)
	}
	if envelope.LocalCurrency != LocalCurrency || envelope.FormulaID != FormulaID {
		t.Fatalf("currency/formula contract is incomplete: %+v", envelope)
	}
	if !strings.HasPrefix(product["record_hash"].(string), "sha256:") || !strings.HasPrefix(envelope.Source.Revision, "sha256:") || !strings.HasPrefix(envelope.EventID, "sha256:") {
		t.Fatalf("hash identities were not generated: product=%v source=%q event=%q", product["record_hash"], envelope.Source.Revision, envelope.EventID)
	}
}

func TestKalaProfileOmitsUnavailableStandalonePricingAndIntegrationFields(t *testing.T) {
	cfg := DefaultConfig()
	provider := pricingcatalog.NewProvider(pricingcatalog.Config{Mode: pricingcatalog.ModeNone})
	rows := []map[string]interface{}{{
		"Code":     "BAD-1",
		"Sharh1":   "yuan unknown",
		"Sharh2":   "240 گرم / 300 گرم",
		"ALLANBAR": 0,
	}}
	products, _ := Transform(context.Background(), rows, "kala.db", cfg, provider, time.Time{})
	product := products[0]
	for _, absent := range []string{"foreign_price", "weight_grams", "shipping_method_id", "shipping_price_per_kg", "shipping_price_per_kg_currency", "markup_percent", "irt_per_cny", "final_price", "pricing_catalog_status"} {
		if _, exists := product[absent]; exists {
			t.Fatalf("standalone output included unavailable field %q: %#v", absent, product)
		}
	}
	warnings := strings.Join(product["warnings"].([]string), ",")
	for _, expected := range []string{"foreign_price_missing", "weight_ambiguous"} {
		if !strings.Contains(warnings, expected) {
			t.Fatalf("missing warning %q in %s", expected, warnings)
		}
	}
	for _, unrelated := range []string{"shipping_method", "shipping_price", "fx_rate", "final_price"} {
		if strings.Contains(warnings, unrelated) {
			t.Fatalf("standalone output included integration warning %q in %s", unrelated, warnings)
		}
	}
}

func TestKalaProfilePreservesExplicitNullButOmitsNeverReceivedFields(t *testing.T) {
	cfg := DefaultConfig()
	rows, envelope := Transform(context.Background(), []map[string]interface{}{{
		"Code": "NULL-1", "Name": nil, "Serial": "", "FOROSH": nil, "warehouse_stock": map[string]interface{}{"1": nil}, "ALLANBAR": 0,
	}}, "kala.db", cfg, nil, time.Unix(1, 0))
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	for _, field := range []string{"name", "sale_price_source"} {
		if value, exists := row[field]; !exists || value != nil {
			t.Fatalf("explicit null %q was not preserved: %#v", field, row)
		}
	}
	stock, ok := row["warehouse_stock"].(map[string]interface{})
	if !ok || stock["1"] != nil {
		t.Fatalf("explicit warehouse null was not preserved: %#v", row["warehouse_stock"])
	}
	for _, field := range []string{"unit", "minimum_stock", "shipping_method_id", "final_price"} {
		if _, exists := row[field]; exists {
			t.Fatalf("never-received field %q was emitted: %#v", field, row)
		}
	}
	if value, exists := row["serial"]; !exists || value != "" {
		t.Fatalf("explicit empty serial was not preserved: %#v", row)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"name":null`) || strings.Contains(text, `"shipping_method_id"`) || strings.Contains(text, `"formula_id"`) {
		t.Fatalf("sparse JSON contract is wrong: %s", text)
	}
}

func TestIntegratedProfileOmitsDerivedUnavailableFinalPrice(t *testing.T) {
	fx := pricingcatalog.Decimal("30000")
	cfg := DefaultConfig()
	cfg.Pricing = pricingcatalog.Config{
		Mode: pricingcatalog.ModeStatic,
		Static: pricingcatalog.StaticConfig{
			CNYToIRT: &fx,
		},
	}
	rows, envelope := Transform(context.Background(), []map[string]interface{}{{
		"Code": "NO-PRICE", "warehouse_stock": map[string]interface{}{},
	}}, "kala.db", cfg, nil, time.Unix(1, 0))
	if _, exists := rows[0]["final_price"]; exists {
		t.Fatalf("derived unavailable final_price must be absent, not null: %#v", rows[0])
	}
	stock, exists := rows[0]["warehouse_stock"]
	if !exists || !reflect.DeepEqual(stock, map[string]interface{}{}) {
		t.Fatalf("explicit empty warehouse object was not preserved: %#v", rows[0])
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"final_price":null`) {
		t.Fatalf("derived missing value leaked as explicit null: %s", encoded)
	}
}

func TestIntegratedProfilePreservesExplicitReferenceNulls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/integration/catalog" {
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","revision":"r1","currency":{"local":"IRT","cny_to_local":null,"cny_to_irt":null,"effective_date":null,"warnings":[]},"pricing":{"formula_id":"landed_price"},"selected_warehouses":[],"shipping_methods":[{"id":"air","price_per_kg":null,"currency":null}]}}`)
			return
		}
		if r.URL.Path == "/integration/pricing-assignments/batch" {
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.pricing-assignment-batch","requested_count":1,"resolved_count":1,"error_count":0,"maximum_codes":500,"default_percentage_markup":{"schema":"digitalogic.default-percentage-markup","configured":false,"type":"percentage","profit_percent":null,"source":"unset","revision":"r1"},"results":[{"code":"NULL-REFERENCE","status":"ok","assignment":{"code":"NULL-REFERENCE","shipping_method_id":"air","profit_percent":null,"profit_percent_source":"unset","pricing_warnings":["markup_percent_missing"]}}]}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"shipping_method_id":"air","profit_percent":null,"profit_percent_source":"unset","pricing_warnings":["markup_percent_missing"]}}`)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.Pricing = pricingcatalog.Config{Mode: pricingcatalog.ModeDigitalogic, Digitalogic: pricingcatalog.DigitalogicConfig{BaseURL: server.URL}}
	rows, _ := Transform(context.Background(), []map[string]interface{}{{
		"Code": "NULL-REFERENCE", "foreign_price": "2", "weight_grams": "3",
	}}, "kala.db", cfg, pricingcatalog.NewProvider(cfg.Pricing), time.Unix(1, 0))
	for _, field := range []string{"irt_per_cny", "currency_effective_date", "shipping_price_per_kg", "shipping_price_per_kg_currency", "markup_percent"} {
		if value, exists := rows[0][field]; !exists || value != nil {
			t.Fatalf("explicit reference null %q was not preserved: %#v", field, rows[0])
		}
	}
	if _, exists := rows[0]["final_price"]; exists {
		t.Fatalf("derived unavailable final_price must remain absent: %#v", rows[0])
	}
}

func TestProductSyncDecoderPreservesUnknownExtensionFields(t *testing.T) {
	var envelope Envelope
	payload := []byte(`{
		"schema":"patris.product-sync",
		"future_envelope":{"mode":"flexible"},
		"source":{"id":"A","dataset":"kala.db","future_source":7},
		"products":[{"product_code":"A","future_product":["x",2]}],
		"categories":[{"category_code":"10","future_category":false}],
		"deleted_codes":[{"product_code":"B","deleted":true,"future_tombstone":"kept"}]
	}`)
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode flexible envelope: %v", err)
	}
	if envelope.Extensions == nil ||
		envelope.Source.Extensions == nil ||
		len(envelope.Products) != 1 || envelope.Products[0].Extensions == nil ||
		len(envelope.Categories) != 1 || envelope.Categories[0].Extensions == nil ||
		len(envelope.DeletedCodes) != 1 || envelope.DeletedCodes[0].Extensions == nil {
		t.Fatalf("unknown extensions were not retained: %#v", envelope)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("re-encode flexible envelope: %v", err)
	}
	for _, member := range []string{
		`"future_envelope":{"mode":"flexible"}`,
		`"future_source":7`,
		`"future_product":["x",2]`,
		`"future_category":false`,
		`"future_tombstone":"kept"`,
	} {
		if !strings.Contains(string(encoded), member) {
			t.Fatalf("extension %s was lost during round trip: %s", member, encoded)
		}
	}
	rows := ProductsToRows(envelope.Products)
	if got := rows[0]["future_product"]; !reflect.DeepEqual(got, []interface{}{"x", json.Number("2")}) {
		t.Fatalf("product row did not expose preserved extension: %#v", got)
	}
}

func TestCanonicalHashesCanBeHiddenOrDisabled(t *testing.T) {
	rows := []map[string]interface{}{{
		"Code": "123456789", "Name": "Hash fixture",
	}}
	cfg := DefaultConfig()

	products, envelope := Transform(
		context.Background(),
		rows,
		"kala.db",
		cfg,
		pricingcatalog.NewProvider(pricingcatalog.Config{Mode: pricingcatalog.ModeNone}),
		time.Unix(1, 0),
	)
	if len(products) != 1 || products[0]["record_hash"] == "" ||
		envelope.Source.Revision == "" || envelope.EventID == "" {
		t.Fatalf("default hash identities missing: rows=%#v envelope=%+v", products, envelope)
	}

	expose := false
	cfg.Hashes.Expose = &expose
	hidden := OutputEnvelope(envelope, cfg)
	if hidden.Products[0].RecordHash != "" || hidden.Source.Revision == "" || hidden.EventID == "" {
		t.Fatalf("hide mode removed internal identities or exposed record hash: %+v", hidden)
	}

	enabled := false
	cfg.Hashes.Enabled = &enabled
	cfg = NormalizeConfig(cfg)
	products, disabled := Transform(
		context.Background(),
		rows,
		"kala.db",
		cfg,
		pricingcatalog.NewProvider(pricingcatalog.Config{Mode: pricingcatalog.ModeNone}),
		time.Unix(1, 0),
	)
	if _, exists := products[0]["record_hash"]; exists {
		t.Fatalf("disabled hashes still appeared in product rows: %#v", products[0])
	}
	if disabled.Products[0].RecordHash != "" || disabled.Source.Revision != "" || disabled.EventID != "" {
		t.Fatalf("disabled hashes were still materialized: %+v", disabled)
	}
}

func TestProductSyncDecoderRequiresCanonicalShippingPair(t *testing.T) {
	for name, payload := range map[string]string{
		"price only":          `{"product_code":"A","shipping_price_per_kg":120}`,
		"currency only":       `{"product_code":"A","shipping_price_per_kg_currency":"CNY"}`,
		"lowercase currency":  `{"product_code":"A","shipping_price_per_kg":120,"shipping_price_per_kg_currency":"cny"}`,
		"other currency":      `{"product_code":"A","shipping_price_per_kg":120,"shipping_price_per_kg_currency":"USD"}`,
		"whitespace currency": `{"product_code":"A","shipping_price_per_kg":120,"shipping_price_per_kg_currency":" CNY "}`,
		"quoted price":        `{"product_code":"A","shipping_price_per_kg":"120","shipping_price_per_kg_currency":"CNY"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var product Product
			if err := json.Unmarshal([]byte(payload), &product); err == nil {
				t.Fatalf("invalid shipping pair was accepted: %s", payload)
			}
		})
	}
	for _, currency := range []string{pricingcatalog.CurrencyCNY, pricingcatalog.CurrencyIRR} {
		var product Product
		payload := fmt.Sprintf(`{"product_code":"A","shipping_price_per_kg":120,"shipping_price_per_kg_currency":%q}`, currency)
		if err := json.Unmarshal([]byte(payload), &product); err != nil {
			t.Fatalf("%s shipping pair rejected: %v", currency, err)
		}
	}
}

func TestLegacyURLEncodedPatrisBytesPreserveDecimalPriceAndPersianWeight(t *testing.T) {
	cfg, provider := canonicalTestConfig("113007045")
	products, _ := Transform(context.Background(), []map[string]interface{}{{
		"Code":       "113007045",
		"priceinfo":  "0%0D0%0D0%0D24.5",
		"short_desc": "%D5%B6%D2240%0D0%0D0%0D0",
		"ALLANBAR":   1,
		"ANBAR":      []interface{}{1, 0},
	}}, "kala.db", cfg, provider, time.Time{})
	if len(products) != 1 {
		t.Fatalf("products = %d", len(products))
	}
	product := products[0]
	if fmt.Sprint(product["foreign_price"]) != "24.5" || fmt.Sprint(product["weight_grams"]) != "240" || product["final_price"] != int64(2009410) {
		t.Fatalf("legacy URL-encoded fields were not parsed: %#v", product)
	}
}

func TestDuplicateCodesAreQuarantinedDeterministically(t *testing.T) {
	cfg, provider := canonicalTestConfig("DUP")
	rows := []map[string]interface{}{
		{"Code": "DUP", "Sharh1": "0 0 0 1", "Sharh2": "1 گرم"},
		{"Code": "DUP", "Sharh1": "0 0 0 2", "Sharh2": "2 گرم"},
	}
	products, envelope := Transform(context.Background(), rows, "kala.db", cfg, provider, time.Time{})
	if len(products) != 0 || !reflect.DeepEqual(envelope.QuarantinedCodes, []string{"DUP"}) {
		t.Fatalf("duplicate Code was not quarantined: products=%#v envelope=%+v", products, envelope)
	}
	if !reflect.DeepEqual(envelope.Warnings, []string{"duplicate_product_code:DUP"}) {
		t.Fatalf("missing duplicate warning: %v", envelope.Warnings)
	}
}

func TestSourceIdentityIsStableAndEventIdentityIncludesGenerationTime(t *testing.T) {
	cfg, provider := canonicalTestConfig("A")
	rows := []map[string]interface{}{{"Code": "A", "Sharh1": "0 0 0 1", "Sharh2": "1 گرم", "ALLANBAR": 1}}
	_, first := Transform(context.Background(), rows, "https://office.example/files/kala.db?revision=2", cfg, provider, time.Unix(1, 0))
	_, second := Transform(context.Background(), rows, "https://office.example/files/kala.db?revision=2", cfg, provider, time.Unix(2, 0))
	if first.Source.ID != "kala.db" || first.Source.Dataset != "kala.db" {
		t.Fatalf("URL source identity is wrong: %+v", first.Source)
	}
	if first.Source.Revision != second.Source.Revision {
		t.Fatalf("generated_at changed the source revision: first=%+v second=%+v", first, second)
	}
	if first.GeneratedAt == second.GeneratedAt || first.EventID == second.EventID {
		t.Fatal("event occurrence identity did not include generated_at")
	}
}

func TestCatalogFetchTimeDoesNotChangeRecordOrEventIdentity(t *testing.T) {
	freight := pricingcatalog.Decimal("120")
	markup := pricingcatalog.Decimal("30")
	fx := pricingcatalog.Decimal("29000")
	base := pricingcatalog.Resolution{
		CatalogRevision: "r1", CatalogStatus: "fresh", MethodID: "air",
		ShippingPricePerKg: &freight, ShippingPricePerKgCurrency: pricingcatalog.CurrencyCNY, MarkupPercent: &markup, IRTPerCNY: &fx,
	}
	cfg := DefaultConfig()
	row := []map[string]interface{}{{"Code": "A", "Sharh1": "0 0 0 24.5", "Sharh2": "240 g", "ALLANBAR": 1}}
	firstResolution := base
	firstResolution.CatalogFetchedAt = time.Unix(1, 0)
	secondResolution := base
	secondResolution.CatalogFetchedAt = time.Unix(2, 0)
	generatedAt := time.Unix(10, 0)
	firstRows, first := Transform(context.Background(), row, "kala.db", cfg, fixedResolutionProvider{firstResolution}, generatedAt)
	secondRows, second := Transform(context.Background(), row, "kala.db", cfg, fixedResolutionProvider{secondResolution}, generatedAt)
	if firstRows[0]["record_hash"] != secondRows[0]["record_hash"] || first.Source.Revision != second.Source.Revision || first.EventID != second.EventID {
		t.Fatalf("volatile catalog fetch time changed identities: first=%+v second=%+v", first, second)
	}
}

func TestChangeEnvelopeCarriesDeterministicDeletedCodeTombstone(t *testing.T) {
	cfg, provider := canonicalTestConfig("B")
	rows := []map[string]interface{}{{"Code": "B", "Sharh1": "0 0 0 1", "Sharh2": "1 گرم", "ALLANBAR": 1}}
	products, snapshot := Transform(context.Background(), rows, "kala.db", cfg, provider, time.Unix(1, 0))
	previous := []map[string]interface{}{{"product_code": "A", "record_hash": "sha256:old"}}
	changes := recorddiff.Between(previous, products, "product_code", time.Unix(1, 0))
	first := ChangeEnvelope(snapshot, &changes)
	second := ChangeEnvelope(snapshot, &changes)
	if len(first.DeletedCodes) != 1 || first.DeletedCodes[0].ProductCode != "A" || !first.DeletedCodes[0].Deleted {
		t.Fatalf("deleted Code tombstone missing: %+v", first)
	}
	if first.EventID != second.EventID {
		t.Fatalf("change event IDs are not deterministic: %q != %q", first.EventID, second.EventID)
	}
}

func TestDigitalogicProfilePrefetches1002CodesInThreeBatchRequests(t *testing.T) {
	var catalogRequests int32
	var batchRequests int32
	var singleRequests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/integration/catalog":
			atomic.AddInt32(&catalogRequests, 1)
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","revision":"r1","currency":{"local":"IRT","cny_to_local":29000,"cny_to_irt":29000},"pricing":{"formula_id":"landed_price"},"shipping_methods":[{"id":"air","price_per_kg":120,"currency":"CNY"}]}}`)
		case "/integration/pricing-assignments/batch":
			atomic.AddInt32(&batchRequests, 1)
			var request struct {
				Codes []string `json:"codes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode batch request: %v", err)
			}
			results := make([]map[string]interface{}, 0, len(request.Codes))
			for _, code := range request.Codes {
				results = append(results, map[string]interface{}{
					"code": code, "status": "ok", "assignment": map[string]interface{}{
						"code": code, "shipping_method_id": "air", "profit_percent": "30", "profit_percent_source": "global_default", "pricing_warnings": []string{},
					},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{
				"schema":          "digitalogic.pricing-assignment-batch",
				"requested_count": len(request.Codes), "resolved_count": len(request.Codes), "error_count": 0, "maximum_codes": 500,
				"default_percentage_markup": map[string]interface{}{
					"schema": "digitalogic.default-percentage-markup", "configured": true, "type": "percentage", "profit_percent": "30", "source": "global_default", "revision": "rev-30",
				},
				"results": results,
			}})
		default:
			atomic.AddInt32(&singleRequests, 1)
			fmt.Fprint(w, `{"data":{"shipping_method_id":"air","profit_percent":30}}`)
		}
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.Pricing = pricingcatalog.Config{
		Mode: pricingcatalog.ModeDigitalogic,
		Digitalogic: pricingcatalog.DigitalogicConfig{
			BaseURL: server.URL, FreshFor: "1m", MaxStale: "1h", MaxEntries: 1,
		},
	}
	rows := make([]map[string]interface{}, 1002)
	for index := range rows {
		rows[index] = map[string]interface{}{
			"Code": fmt.Sprintf("P-%04d", index), "Sharh1": "0 0 0 24.5", "Sharh2": "240 Ú¯Ø±Ù…", "ALLANBAR": 1,
		}
	}
	products, _ := Transform(context.Background(), rows, "kala.db", cfg, pricingcatalog.NewProvider(cfg.Pricing), time.Now())
	if len(products) != len(rows) {
		t.Fatalf("products = %d, want %d", len(products), len(rows))
	}
	for index, product := range products {
		if got := product["shipping_method_id"]; got != "air" {
			t.Fatalf("product %d shipping_method_id = %v, want air; scoped prefetch result was lost", index, got)
		}
	}
	if got := atomic.LoadInt32(&catalogRequests); got != 1 {
		t.Fatalf("catalog requests = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&batchRequests); got != 3 {
		t.Fatalf("batch requests = %d, want exactly 3 for 1,002 Codes at 500/request", got)
	}
	if got := atomic.LoadInt32(&singleRequests); got != 0 {
		t.Fatalf("single-Code requests = %d, want 0", got)
	}
}
