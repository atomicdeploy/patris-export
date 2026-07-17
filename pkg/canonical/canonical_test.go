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
				ID: "air_express", Enabled: &enabled, PricePerKgCNY: &rate,
			}},
			Assignments: map[string]pricingcatalog.Assignment{
				code: {MethodID: "air_express", ProfitPercent: &markup},
			},
		},
	}
	return cfg, pricingcatalog.NewProvider(cfg.Pricing)
}

func TestLandedPriceV1ExactWorkbookFixture(t *testing.T) {
	got, err := LandedPriceV1("240", "120", "24.5", "30", "29000")
	if err != nil {
		t.Fatal(err)
	}
	if got != 2009410 {
		t.Fatalf("landed_price_v1 = %d, want 2009410", got)
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
		Methods:     []pricingcatalog.Method{{ID: "air", Enabled: &enabled, PricePerKgCNY: &freight}},
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

func TestLandedPriceV1RoundsOnceHalfUpAndRejectsInvalidInput(t *testing.T) {
	got, err := LandedPriceV1("0", "0", "0.005", "0", "100")
	if err != nil || got != 1 {
		t.Fatalf("half-up result = %d, %v; want 1", got, err)
	}
	for _, invalid := range []string{"", "NaN", "Inf", "-1", "1e999999"} {
		if _, err := LandedPriceV1(invalid, "1", "1", "1", "1"); err == nil {
			t.Fatalf("invalid decimal %q was accepted", invalid)
		}
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
	if warnings := product["warnings"].([]string); len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	for _, forbidden := range []string{"Sharh1", "Sharh2", "FOROSH", "KHARYD", "ALLANBAR", "ANBAR", "Code"} {
		if _, exists := product[forbidden]; exists {
			t.Fatalf("raw field %q crossed product-sync boundary: %#v", forbidden, product)
		}
	}
	if envelope.Schema != ContractName || envelope.SchemaVersion != ContractVersion || envelope.Source.Dataset != "kala.db" || envelope.Source.ID != "kala.db" {
		t.Fatalf("unexpected contract/source: %+v", envelope)
	}
	if envelope.LocalCurrency != LocalCurrency || envelope.FormulaID != FormulaVersion || envelope.FormulaRevision != FormulaRevision {
		t.Fatalf("currency/formula contract is incomplete: %+v", envelope)
	}
	if !strings.HasPrefix(product["record_hash"].(string), "sha256:") || !strings.HasPrefix(envelope.Source.Revision, "sha256:") || !strings.HasPrefix(envelope.EventID, "sha256:") {
		t.Fatalf("hash identities were not generated: product=%v source=%q event=%q", product["record_hash"], envelope.Source.Revision, envelope.EventID)
	}
}

func TestKalaProfileWithholdsMissingOrAmbiguousPricingInsteadOfZero(t *testing.T) {
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
	if product["foreign_price"] != nil || product["weight_grams"] != nil || product["final_price"] != nil {
		t.Fatalf("missing/ambiguous inputs became destructive values: %#v", product)
	}
	warnings := strings.Join(product["warnings"].([]string), ",")
	for _, expected := range []string{"foreign_price_missing", "weight_ambiguous", "import_freight_method_missing", "freight_rate_missing", "fx_rate_missing", "final_price_unavailable"} {
		if !strings.Contains(warnings, expected) {
			t.Fatalf("missing warning %q in %s", expected, warnings)
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
		FreightCNYPerKg: &freight, MarkupPercent: &markup, IRTPerCNY: &fx,
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

func TestDigitalogicProfileBoundsConcurrentAssignmentReads(t *testing.T) {
	var catalogRequests int32
	var active int32
	var maximum int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/integration/catalog" {
			atomic.AddInt32(&catalogRequests, 1)
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":29000,"cny_to_irt":29000},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air","price_per_kg_cny":120}]}}`)
			return
		}
		if r.URL.Path == "/pricing-assignments/batch" {
			http.NotFound(w, r)
			return
		}
		current := atomic.AddInt32(&active, 1)
		for {
			seen := atomic.LoadInt32(&maximum)
			if current <= seen || atomic.CompareAndSwapInt32(&maximum, seen, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		fmt.Fprint(w, `{"data":{"import_freight_method_id":"air","profit_percent":30}}`)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.Pricing = pricingcatalog.Config{
		Mode: pricingcatalog.ModeDigitalogic,
		Digitalogic: pricingcatalog.DigitalogicConfig{
			BaseURL: server.URL, MaxConcurrency: 3, FreshFor: "1m", MaxStale: "1h",
		},
	}
	rows := make([]map[string]interface{}, 10)
	for index := range rows {
		rows[index] = map[string]interface{}{
			"Code": fmt.Sprintf("P-%02d", index), "Sharh1": "0 0 0 24.5", "Sharh2": "240 گرم", "ALLANBAR": 1,
		}
	}
	products, _ := Transform(context.Background(), rows, "kala.db", cfg, pricingcatalog.NewProvider(cfg.Pricing), time.Now())
	if len(products) != len(rows) {
		t.Fatalf("products = %d, want %d", len(products), len(rows))
	}
	if got := atomic.LoadInt32(&catalogRequests); got != 1 {
		t.Fatalf("concurrent resolvers fetched catalog %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&maximum); got < 2 || got > 3 {
		t.Fatalf("assignment concurrency = %d, want 2..3", got)
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
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":29000,"cny_to_irt":29000},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air","price_per_kg_cny":120}]}}`)
		case "/pricing-assignments/batch":
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
						"code": code, "import_freight_method_id": "air", "profit_percent": "30", "profit_percent_source": "global_default", "pricing_warnings": []string{},
					},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{
				"schema": "digitalogic.pricing-assignment-batch", "schema_version": "1.0.0",
				"requested_count": len(request.Codes), "resolved_count": len(request.Codes), "error_count": 0, "maximum_codes": 500,
				"default_percentage_markup": map[string]interface{}{
					"schema": "digitalogic.default-percentage-markup", "schema_version": "1.0.0", "configured": true, "type": "percentage", "profit_percent": "30", "source": "global_default", "revision": "rev-30",
				},
				"results": results,
			}})
		default:
			atomic.AddInt32(&singleRequests, 1)
			fmt.Fprint(w, `{"data":{"import_freight_method_id":"air","profit_percent":30}}`)
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
		if got := product["import_freight_method_id"]; got != "air" {
			t.Fatalf("product %d import_freight_method_id = %v, want air; scoped prefetch result was lost", index, got)
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
