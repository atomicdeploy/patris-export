package pricingcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func floatPointer(value float64) *Decimal { return DecimalFromFloat(value) }
func boolPointer(value bool) *bool        { return &value }

func decimalText(value *Decimal) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func TestStaticProviderResolvesExactCodeAndDefaults(t *testing.T) {
	provider := NewProvider(Config{
		Mode: ModeStatic,
		Static: StaticConfig{
			CNYToIRT:           floatPointer(29000),
			SelectedWarehouses: []string{"6", "1", "1"},
			Methods: []Method{{
				ID: "air_express", Enabled: boolPointer(true), PricePerKgCNY: floatPointer(120),
			}},
			Assignments: map[string]Assignment{
				"113007045": {MethodID: "air_express", ProfitPercent: floatPointer(30)},
			},
		},
	})

	resolution := provider.Resolve(context.Background(), "113007045")
	if resolution.MethodID != "air_express" || decimalText(resolution.FreightCNYPerKg) != "120" {
		t.Fatalf("unexpected freight resolution: %+v", resolution)
	}
	if decimalText(resolution.MarkupPercent) != "30" || decimalText(resolution.IRTPerCNY) != "29000" {
		t.Fatalf("unexpected pricing resolution: %+v", resolution)
	}
	if got := strings.Join(resolution.SelectedWarehouses, ","); got != "1,6" {
		t.Fatalf("warehouses were not normalized: %q", got)
	}
	if len(resolution.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", resolution.Warnings)
	}

	missing := provider.Resolve(context.Background(), "missing")
	warnings := strings.Join(missing.Warnings, ",")
	for _, expected := range []string{"freight_rate_missing", "import_freight_method_missing", "markup_percent_missing"} {
		if !strings.Contains(warnings, expected) {
			t.Fatalf("missing warning %q in %v", expected, missing.Warnings)
		}
	}
}

func TestStaticProviderRejectsNonFinitePricingInputs(t *testing.T) {
	nan := Decimal("NaN")
	provider := NewProvider(Config{Mode: ModeStatic, Static: StaticConfig{
		CNYToIRT:    &nan,
		Methods:     []Method{{ID: "air", PricePerKgCNY: &nan}},
		Assignments: map[string]Assignment{"A": {MethodID: "air", ProfitPercent: &nan}},
	}})
	resolution := provider.Resolve(context.Background(), "A")
	if resolution.IRTPerCNY != nil || resolution.FreightCNYPerKg != nil || resolution.MarkupPercent != nil {
		t.Fatalf("non-finite static inputs were retained: %+v", resolution)
	}
	for _, warning := range []string{"fx_rate_missing", "freight_rate_missing", "markup_percent_missing"} {
		if !contains(resolution.Warnings, warning) {
			t.Fatalf("missing %s in %v", warning, resolution.Warnings)
		}
	}
}

func TestHTTPProviderCachesCatalogAndAssignmentsWithinFreshnessLimit(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/integration/catalog":
			fmt.Fprint(w, `{"success":true,"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"sha256:test","currency":{"local":"IRT","cny_to_local":29000,"cny_to_irt":29000,"effective_date":"2026-07-16"},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"selected_warehouses":["1","6"],"import_freight_methods":[{"id":"air_express","enabled":true,"price_per_kg_cny":120}]}}`)
		case "/products/by-code/A/import-pricing":
			fmt.Fprint(w, `{"success":true,"data":{"import_freight_method_id":"air_express","markup":{"profit_percent":30,"warning":null},"pricing_warnings":[]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	provider := newHTTPProvider(DigitalogicConfig{
		BaseURL: server.URL, FreshFor: "5m", MaxStale: "1h", MaxEntries: 2,
	}, server.Client(), func() time.Time { return now })

	for i := 0; i < 2; i++ {
		resolution := provider.Resolve(context.Background(), "A")
		if len(resolution.Warnings) != 0 || resolution.CatalogRevision != "sha256:test" {
			t.Fatalf("unexpected resolution %d: %+v", i, resolution)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if requests["/integration/catalog"] != 1 || requests["/products/by-code/A/import-pricing"] != 1 {
		t.Fatalf("fresh values were not cached: %#v", requests)
	}
}

func TestHTTPProviderUsesCacheOnlyInsideExplicitMaxStale(t *testing.T) {
	var fail bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/integration/catalog" {
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"sha256:test","currency":{"local":"IRT","cny_to_local":29000,"cny_to_irt":29000},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air","enabled":true,"price_per_kg_cny":100}]}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"import_freight_method_id":"air","profit_percent":10}}`)
	}))
	defer server.Close()

	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	provider := newHTTPProvider(DigitalogicConfig{
		BaseURL: server.URL, FreshFor: "1m", MaxStale: "10m", MaxEntries: 2,
	}, server.Client(), func() time.Time { return now })
	if got := provider.Resolve(context.Background(), "A"); len(got.Warnings) != 0 {
		t.Fatalf("initial fetch failed: %+v", got)
	}

	fail = true
	now = now.Add(2 * time.Minute)
	stale := provider.Resolve(context.Background(), "A")
	if stale.FreightCNYPerKg == nil || !contains(stale.Warnings, "pricing_catalog_stale") || !contains(stale.Warnings, "product_pricing_assignment_stale") {
		t.Fatalf("bounded stale cache was not used: %+v", stale)
	}

	now = now.Add(20 * time.Minute)
	expired := provider.Resolve(context.Background(), "A")
	if expired.FreightCNYPerKg != nil || !contains(expired.Warnings, "pricing_catalog_unavailable") {
		t.Fatalf("expired cache was used outside max_stale: %+v", expired)
	}
}

func TestHTTPProviderBoundsAssignmentCache(t *testing.T) {
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counts[r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/integration/catalog" {
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":1,"cny_to_irt":1},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air","price_per_kg_cny":1}]}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"import_freight_method_id":"air","profit_percent":0}}`)
	}))
	defer server.Close()

	provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL, FreshFor: "1h", MaxEntries: 1}, server.Client(), time.Now)
	provider.Resolve(context.Background(), "A")
	provider.Resolve(context.Background(), "B")
	provider.Resolve(context.Background(), "A")
	if counts["/products/by-code/A/import-pricing"] != 2 {
		t.Fatalf("LRU did not evict the oldest assignment: %#v", counts)
	}
}

func TestJoinURLRejectsOriginChangesAndAbsolutePaths(t *testing.T) {
	base := "https://example.test/wp-json/digitalogic/v1"
	for _, path := range []string{
		"https://evil.test/catalog",
		"//evil.test/catalog",
		"/absolute/catalog",
		`\\evil.test\catalog`,
	} {
		if resolved, err := joinURL(base, path); err == nil {
			t.Fatalf("joinURL(%q) unexpectedly resolved to %q", path, resolved)
		}
	}
	resolved, err := joinURL(base, "products/by-code/A%2FB/import-pricing")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "https://example.test/wp-json/digitalogic/v1/products/by-code/A%2FB/import-pricing" {
		t.Fatalf("encoded Code path changed: %q", resolved)
	}
}

func TestHTTPProviderRejectsUntrustedConfiguredPathsBeforeRequest(t *testing.T) {
	for name, cfg := range map[string]DigitalogicConfig{
		"absolute catalog":        {BaseURL: "https://example.test/wp-json/digitalogic/v1", CatalogPath: "https://evil.test/catalog"},
		"scheme relative catalog": {BaseURL: "https://example.test/wp-json/digitalogic/v1", CatalogPath: "//evil.test/catalog"},
		"assignment without Code": {BaseURL: "https://example.test/wp-json/digitalogic/v1", AssignmentPath: "products/import-pricing"},
		"absolute assignment":     {BaseURL: "https://example.test/wp-json/digitalogic/v1", AssignmentPath: "https://evil.test/{code}"},
	} {
		t.Run(name, func(t *testing.T) {
			resolution := newHTTPProvider(cfg, nil, time.Now).Resolve(context.Background(), "A")
			if !contains(resolution.Warnings, "pricing_catalog_path_invalid") && !contains(resolution.Warnings, "pricing_assignment_path_invalid") {
				t.Fatalf("unsafe configured path was not rejected: %+v", resolution)
			}
		})
	}
}

func TestHTTPProviderEncodesProductCodeAsOnePathSegment(t *testing.T) {
	var assignmentEscapedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/integration/catalog" {
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":1,"cny_to_irt":1},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air","price_per_kg_cny":1}]}}`)
			return
		}
		assignmentEscapedPath = r.URL.EscapedPath()
		fmt.Fprint(w, `{"data":{"import_freight_method_id":"air","profit_percent":0}}`)
	}))
	defer server.Close()

	provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL}, server.Client(), time.Now)
	resolution := provider.Resolve(context.Background(), "A/B")
	if len(resolution.Warnings) != 0 {
		t.Fatalf("encoded assignment failed: %+v", resolution)
	}
	if !strings.Contains(strings.ToUpper(assignmentEscapedPath), "A%2FB") {
		t.Fatalf("Code was not kept in one encoded path segment: %q", assignmentEscapedPath)
	}
}

func TestHTTPProviderFailsClosedOnStatusAndResponseLimit(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"status": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not authorized and deliberately verbose", http.StatusUnauthorized)
		},
		"oversize": func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, strings.Repeat("x", 512))
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL, MaxResponseBytes: 64}, server.Client(), time.Now)
			resolution := provider.Resolve(context.Background(), "A")
			if !contains(resolution.Warnings, "pricing_catalog_fetch_failed") || !contains(resolution.Warnings, "pricing_catalog_unavailable") {
				t.Fatalf("provider did not fail closed: %+v", resolution)
			}
			for _, warning := range resolution.Warnings {
				if strings.Contains(warning, "authorized") || strings.Contains(warning, "http") {
					t.Fatalf("remote response details leaked into warning: %q", warning)
				}
			}
		})
	}
}

func TestHTTPProviderWithholdsFXForIncompatibleCatalogContracts(t *testing.T) {
	tests := []struct {
		name            string
		schemaVersion   string
		localCurrency   string
		formulaID       string
		formulaRevision string
		expectedWarning string
	}{
		{name: "schema major", schemaVersion: "2.0.0", localCurrency: "IRT", formulaID: "landed_price_v1", formulaRevision: "1.0.0", expectedWarning: "pricing_catalog_schema_incompatible"},
		{name: "local currency", schemaVersion: "1.0.0", localCurrency: "USD", formulaID: "landed_price_v1", formulaRevision: "1.0.0", expectedWarning: "pricing_local_currency_not_irt"},
		{name: "formula id", schemaVersion: "1.0.0", localCurrency: "IRT", formulaID: "landed_price_v2", formulaRevision: "1.0.0", expectedWarning: "pricing_formula_incompatible"},
		{name: "formula revision", schemaVersion: "1.0.0", localCurrency: "IRT", formulaID: "landed_price_v1", formulaRevision: "2.0.0", expectedWarning: "pricing_formula_incompatible"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/integration/catalog" {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{
						"schema": "digitalogic.integration-catalog", "schema_version": test.schemaVersion, "revision": "r1",
						"currency":               map[string]interface{}{"local": test.localCurrency, "cny_to_local": 29000, "cny_to_irt": 29000},
						"pricing":                map[string]interface{}{"formula_id": test.formulaID, "formula_revision": test.formulaRevision},
						"import_freight_methods": []map[string]interface{}{{"id": "air", "price_per_kg_cny": 120}},
					}})
					return
				}
				fmt.Fprint(w, `{"data":{"import_freight_method_id":"air","profit_percent":30}}`)
			}))
			defer server.Close()

			provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL}, server.Client(), time.Now)
			resolution := provider.Resolve(context.Background(), "A")
			if resolution.IRTPerCNY != nil || !contains(resolution.Warnings, test.expectedWarning) || !contains(resolution.Warnings, "fx_rate_missing") {
				t.Fatalf("incompatible catalog was allowed to price: %+v", resolution)
			}
			if resolution.FreightCNYPerKg == nil || resolution.MarkupPercent == nil {
				t.Fatalf("non-FX catalog data should remain observable: %+v", resolution)
			}
		})
	}
}

func TestHTTPProviderRequiresConsistentNonNullCNYToIRT(t *testing.T) {
	for name, currency := range map[string]map[string]interface{}{
		"missing":  {"local": "IRT", "cny_to_local": 29000, "cny_to_irt": nil},
		"conflict": {"local": "IRT", "cny_to_local": 29000, "cny_to_irt": 30000},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/integration/catalog" {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{
						"schema": "digitalogic.integration-catalog", "schema_version": "1.0.0", "revision": "r1",
						"currency":               currency,
						"pricing":                map[string]interface{}{"formula_id": "landed_price_v1", "formula_revision": "1.0.0"},
						"import_freight_methods": []map[string]interface{}{{"id": "air", "price_per_kg_cny": 120}},
					}})
					return
				}
				fmt.Fprint(w, `{"data":{"import_freight_method_id":"air","profit_percent":30}}`)
			}))
			defer server.Close()
			resolution := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL}, server.Client(), time.Now).Resolve(context.Background(), "A")
			if resolution.IRTPerCNY != nil || !contains(resolution.Warnings, "fx_rate_missing") {
				t.Fatalf("invalid FX contract was used: %+v", resolution)
			}
			if name == "missing" && !contains(resolution.Warnings, "pricing_cny_to_irt_missing_or_invalid") {
				t.Fatalf("missing CNY/IRT warning: %+v", resolution)
			}
			if name == "conflict" && !contains(resolution.Warnings, "pricing_fx_contract_conflict") {
				t.Fatalf("missing FX conflict warning: %+v", resolution)
			}
		})
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
