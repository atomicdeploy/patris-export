package pricingcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestStaticAndDisabledProvidersDoNotExposeRemotePrefetch(t *testing.T) {
	for name, provider := range map[string]Provider{
		"static":   NewProvider(Config{Mode: ModeStatic}),
		"disabled": NewProvider(Config{Mode: ModeNone}),
	} {
		if _, ok := provider.(Prefetcher); ok {
			t.Fatalf("%s provider unexpectedly exposes a remote prefetch capability", name)
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

func TestHTTPProviderBacksOffCatalogFailureAcrossPrefetchAndResolve(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL, FreshFor: "1m"}, server.Client(), func() time.Time { return now })
	provider.Prefetch(context.Background(), []string{"A", "B"})
	for _, code := range []string{"A", "B"} {
		resolved := provider.Resolve(context.Background(), code)
		if !contains(resolved.Warnings, "pricing_catalog_fetch_failed") || !contains(resolved.Warnings, "pricing_catalog_unavailable") {
			t.Fatalf("catalog failure diagnostics were lost for %s: %+v", code, resolved)
		}
	}
	if requests != 1 {
		t.Fatalf("one catalog failure caused %d requests inside the freshness backoff", requests)
	}
}

func TestHTTPProviderBoundsPrefetchDiagnosticCache(t *testing.T) {
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	provider := newHTTPProvider(DigitalogicConfig{BaseURL: "http://localhost", MaxEntries: 2}, &http.Client{}, func() time.Time { return now })
	provider.storePrefetchDiagnostic([]string{"A", "B", "C"}, []string{"test_diagnostic"}, now, false)

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.diagnostics) != 2 || provider.diagnosticLRU.Len() != 2 || provider.diagnostics["A"] != nil || provider.diagnostics["B"] == nil || provider.diagnostics["C"] == nil {
		t.Fatalf("diagnostic cache exceeded/did not honor its LRU bound: map=%d lru=%d keys=%v", len(provider.diagnostics), provider.diagnosticLRU.Len(), provider.diagnostics)
	}
}

func TestHTTPProviderRunBarrierSurvivesPersistentCacheEviction(t *testing.T) {
	var batchRequests, singleRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/integration/catalog":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":1,"cny_to_irt":1},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air","price_per_kg_cny":1}]}}`)
		case "/pricing-assignments/batch":
			batchRequests++
			var request struct {
				Codes []string `json:"codes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			results := make([]map[string]interface{}, 0, len(request.Codes))
			resolvedCount := 0
			for _, code := range request.Codes {
				if code == "B" {
					results = append(results, map[string]interface{}{
						"code": code, "status": "error", "error": map[string]interface{}{
							"code": "digitalogic_product_temporarily_invalid", "http_status": 422, "retryable": false,
						},
					})
					continue
				}
				resolvedCount++
				results = append(results, map[string]interface{}{
					"code": code, "status": "ok", "assignment": map[string]interface{}{
						"code": code, "import_freight_method_id": "air", "profit_percent": "30", "profit_percent_source": "global_default", "pricing_warnings": []string{},
					},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{
				"schema": "digitalogic.pricing-assignment-batch", "schema_version": "1.0.0",
				"requested_count": len(request.Codes), "resolved_count": resolvedCount, "error_count": len(request.Codes) - resolvedCount, "maximum_codes": 500,
				"default_percentage_markup": map[string]interface{}{
					"schema": "digitalogic.default-percentage-markup", "schema_version": "1.0.0", "configured": true, "type": "percentage", "profit_percent": "30", "source": "global_default", "revision": "rev-30",
				},
				"results": results,
			}})
		default:
			singleRequests++
		}
	}))
	defer server.Close()

	provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL, MaxEntries: 1}, server.Client(), time.Now)
	scoped := provider.Prefetch(context.Background(), []string{"A", "B", "C"})
	scopedProvider, ok := scoped.(*prefetchedProvider)
	if !ok {
		t.Fatalf("Prefetch returned %T, want transform-scoped provider", scoped)
	}
	scopedProvider.run.mu.Lock()
	runEntries := len(scopedProvider.run.outcomes)
	scopedProvider.run.mu.Unlock()
	if runEntries != 3 {
		t.Fatalf("run barrier entries = %d, want exactly the 3 requested Codes", runEntries)
	}
	provider.mu.Lock()
	assignmentEntries := provider.lru.Len()
	diagnosticEntries := provider.diagnosticLRU.Len()
	provider.mu.Unlock()
	if assignmentEntries > 1 || diagnosticEntries > 1 {
		t.Fatalf("persistent bounds were exceeded: assignments=%d diagnostics=%d", assignmentEntries, diagnosticEntries)
	}

	for _, code := range []string{"A", "B", "C"} {
		resolved := scoped.Resolve(context.Background(), code)
		if code == "B" {
			if !contains(resolved.Warnings, "product_pricing_assignment_batch_result_failed") {
				t.Fatalf("run-scoped diagnostic for B was evicted: %+v", resolved)
			}
			continue
		}
		if resolved.MethodID != "air" || decimalText(resolved.MarkupPercent) != "30" {
			t.Fatalf("run-scoped success for %s was evicted: %+v", code, resolved)
		}
	}
	if batchRequests != 1 || singleRequests != 0 {
		t.Fatalf("run barrier allowed N+1 fallback: batch=%d single=%d", batchRequests, singleRequests)
	}
	scopedProvider.run.mu.Lock()
	runEntries = len(scopedProvider.run.outcomes)
	scopedProvider.run.mu.Unlock()
	if runEntries != 0 {
		t.Fatalf("run barrier retained %d consumed outcomes", runEntries)
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

func TestHTTPProviderBatchPrefetchUsesGoldenContractAndSharedAuth(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/digitalogic-pricing-assignment-batch-v1.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var batchRequests, singleRequests int
	var authHeaders []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/integration/catalog":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":29000,"cny_to_irt":29000},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air_express","price_per_kg_cny":120}]}}`)
		case "/pricing-assignments/batch":
			batchRequests++
			fmt.Fprintf(w, `{"success":true,"data":%s}`, fixture)
		default:
			singleRequests++
			fmt.Fprint(w, `{"data":{"code":"unexpected","import_freight_method_id":"air_express","profit_percent":"30"}}`)
		}
	}))
	defer server.Close()
	t.Setenv("PATRIS_BATCH_TEST_TOKEN", "read-only-token")

	provider := newHTTPProvider(DigitalogicConfig{
		BaseURL: server.URL, BearerTokenEnv: "PATRIS_BATCH_TEST_TOKEN", FreshFor: "1m", MaxStale: "1h",
	}, server.Client(), time.Now)
	provider.Prefetch(context.Background(), []string{"113007045", "MISSING"})
	resolved := provider.Resolve(context.Background(), "113007045")
	missing := provider.Resolve(context.Background(), "MISSING")

	if batchRequests != 1 || singleRequests != 0 {
		t.Fatalf("requests: batch=%d single=%d, want 1/0", batchRequests, singleRequests)
	}
	if resolved.MethodID != "air_express" || decimalText(resolved.MarkupPercent) != "30" || resolved.MarkupPercentSource != "global_default" || decimalText(resolved.FreightCNYPerKg) != "120" {
		t.Fatalf("golden assignment did not resolve exactly: %+v", resolved)
	}
	if !contains(missing.Warnings, "product_pricing_assignment_not_found") {
		t.Fatalf("typed not-found result was lost: %+v", missing)
	}
	if len(authHeaders) != 2 {
		t.Fatalf("shared token covered %d requests, want catalog GET plus batch POST", len(authHeaders))
	}
	for _, header := range authHeaders {
		if header != "Bearer read-only-token" {
			t.Fatalf("shared auth header = %q", header)
		}
	}
}

func TestHTTPProviderMissingConfiguredBearerMakesNoNetworkRequest(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL, BearerTokenEnv: "PATRIS_MISSING_PRICING_TOKEN"}, server.Client(), time.Now)
	resolved := provider.Resolve(context.Background(), "A")
	if requests != 0 || !contains(resolved.Warnings, "pricing_catalog_fetch_failed") || !contains(resolved.Warnings, "pricing_catalog_unavailable") {
		t.Fatalf("missing configured bearer was not rejected before transport: requests=%d resolution=%+v", requests, resolved)
	}
}

func TestHTTPProviderBatchPreservesExactProductOverrideSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/integration/catalog":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":29000,"cny_to_irt":29000},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air","price_per_kg_cny":120}]}}`)
		case "/pricing-assignments/batch":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.pricing-assignment-batch","schema_version":"1.0.0","requested_count":1,"resolved_count":1,"error_count":0,"maximum_codes":500,"default_percentage_markup":{"schema":"digitalogic.default-percentage-markup","schema_version":"1.0.0","configured":true,"type":"percentage","profit_percent":"30","source":"global_default","revision":"rev-30"},"results":[{"code":"EXACT","status":"ok","assignment":{"code":"EXACT","import_freight_method_id":"air","profit_percent":"12.500000000001","profit_percent_source":"product_override","pricing_warnings":[]}}]}}`)
		default:
			t.Fatalf("unexpected single-Code request: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL}, server.Client(), time.Now)
	provider.Prefetch(context.Background(), []string{"EXACT"})
	resolved := provider.Resolve(context.Background(), "EXACT")
	if decimalText(resolved.MarkupPercent) != "12.500000000001" || resolved.MarkupPercentSource != "product_override" {
		t.Fatalf("exact override/source changed: %+v", resolved)
	}
}

func TestHTTPProviderRetryableBatchResultFallsBackToSingleResolver(t *testing.T) {
	var singleRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/integration/catalog":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":1,"cny_to_irt":1},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air","price_per_kg_cny":1}]}}`)
		case "/pricing-assignments/batch":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.pricing-assignment-batch","schema_version":"1.0.0","requested_count":1,"resolved_count":0,"error_count":1,"maximum_codes":500,"default_percentage_markup":{"schema":"digitalogic.default-percentage-markup","schema_version":"1.0.0","configured":false,"type":"percentage","source":"unset","revision":"rev-unset"},"results":[{"code":"A","status":"error","error":{"code":"digitalogic_pricing_temporarily_unavailable","http_status":503,"retryable":true}}]}}`)
		default:
			singleRequests++
			fmt.Fprint(w, `{"data":{"code":"A","import_freight_method_id":"air","profit_percent":"7.25","profit_percent_source":"product_override","pricing_warnings":[]}}`)
		}
	}))
	defer server.Close()

	provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL}, server.Client(), time.Now)
	provider.Prefetch(context.Background(), []string{"A"})
	resolved := provider.Resolve(context.Background(), "A")
	if singleRequests != 1 || decimalText(resolved.MarkupPercent) != "7.25" || resolved.MarkupPercentSource != "product_override" || !contains(resolved.Warnings, "product_pricing_assignment_batch_result_retryable") {
		t.Fatalf("retryable result did not use the bounded single resolver: requests=%d resolution=%+v", singleRequests, resolved)
	}
}

func TestHTTPProviderRejectsInconsistentBatchMarkupSource(t *testing.T) {
	var singleRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/integration/catalog":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":1,"cny_to_irt":1},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air","price_per_kg_cny":1}]}}`)
		case "/pricing-assignments/batch":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.pricing-assignment-batch","schema_version":"1.0.0","requested_count":1,"resolved_count":1,"error_count":0,"maximum_codes":500,"default_percentage_markup":{"schema":"digitalogic.default-percentage-markup","schema_version":"1.0.0","configured":true,"type":"percentage","profit_percent":"30","source":"global_default","revision":"rev-30"},"results":[{"code":"A","status":"ok","assignment":{"code":"A","import_freight_method_id":"air","profit_percent":"31","profit_percent_source":"global_default","pricing_warnings":[]}}]}}`)
		default:
			singleRequests++
		}
	}))
	defer server.Close()

	provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL}, server.Client(), time.Now)
	provider.Prefetch(context.Background(), []string{"A"})
	resolved := provider.Resolve(context.Background(), "A")
	if singleRequests != 0 || !contains(resolved.Warnings, "pricing_assignment_batch_contract_invalid") {
		t.Fatalf("inconsistent source/default semantics did not fail closed: requests=%d resolution=%+v", singleRequests, resolved)
	}
}

func TestHTTPProviderRejectsInvalidBatchDecimalsAndMissingDefaultRevision(t *testing.T) {
	tests := []struct {
		name             string
		defaultProfit    string
		assignmentProfit string
		assignmentSource string
		revision         string
	}{
		{name: "unquoted default", defaultProfit: `30`, assignmentProfit: `"30"`, assignmentSource: "global_default", revision: "rev-30"},
		{name: "unquoted assignment", defaultProfit: `"30"`, assignmentProfit: `30`, assignmentSource: "global_default", revision: "rev-30"},
		{name: "non-canonical default", defaultProfit: `"30.0"`, assignmentProfit: `"30"`, assignmentSource: "global_default", revision: "rev-30"},
		{name: "non-canonical assignment", defaultProfit: `"30"`, assignmentProfit: `"30.0"`, assignmentSource: "product_override", revision: "rev-30"},
		{name: "thirteen fractional digits in default", defaultProfit: `"0.1234567890123"`, assignmentProfit: `"0.1234567890123"`, assignmentSource: "global_default", revision: "rev-30"},
		{name: "thirteen fractional digits", defaultProfit: `"30"`, assignmentProfit: `"0.1234567890123"`, assignmentSource: "product_override", revision: "rev-30"},
		{name: "missing default revision", defaultProfit: `"30"`, assignmentProfit: `"30"`, assignmentSource: "global_default", revision: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var batchRequests, singleRequests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/integration/catalog":
					fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":1,"cny_to_irt":1},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[]}}`)
				case "/pricing-assignments/batch":
					batchRequests++
					fmt.Fprintf(w, `{"data":{"schema":"digitalogic.pricing-assignment-batch","schema_version":"1.0.0","requested_count":1,"resolved_count":1,"error_count":0,"maximum_codes":500,"default_percentage_markup":{"schema":"digitalogic.default-percentage-markup","schema_version":"1.0.0","configured":true,"type":"percentage","profit_percent":%s,"source":"global_default","revision":%q},"results":[{"code":"A","status":"ok","assignment":{"code":"A","import_freight_method_id":"air","profit_percent":%s,"profit_percent_source":"%s","pricing_warnings":[]}}]}}`, test.defaultProfit, test.revision, test.assignmentProfit, test.assignmentSource)
				default:
					singleRequests++
				}
			}))
			defer server.Close()

			provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL}, server.Client(), time.Now)
			scoped := provider.Prefetch(context.Background(), []string{"A"})
			resolved := scoped.Resolve(context.Background(), "A")
			if batchRequests != 1 || singleRequests != 0 || !contains(resolved.Warnings, "pricing_assignment_batch_contract_invalid") {
				t.Fatalf("invalid exact decimal did not fail closed: batch=%d single=%d resolution=%+v", batchRequests, singleRequests, resolved)
			}
		})
	}
}

func TestHTTPProviderCommitsChunksAtomicallyAfterDefaultRevisionAgreement(t *testing.T) {
	var batchRequests, singleRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/integration/catalog":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":1,"cny_to_irt":1},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air","price_per_kg_cny":1}]}}`)
		case "/pricing-assignments/batch":
			batchRequests++
			var request struct {
				Codes []string `json:"codes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			code := request.Codes[0]
			profit := "30"
			revision := "rev-30"
			if code == "B" {
				revision = "rev-31"
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{
				"schema": "digitalogic.pricing-assignment-batch", "schema_version": "1.0.0",
				"requested_count": 1, "resolved_count": 1, "error_count": 0, "maximum_codes": 500,
				"default_percentage_markup": map[string]interface{}{
					"schema": "digitalogic.default-percentage-markup", "schema_version": "1.0.0", "configured": true, "type": "percentage", "profit_percent": profit, "source": "global_default", "revision": revision,
				},
				"results": []map[string]interface{}{{
					"code": code, "status": "ok", "assignment": map[string]interface{}{
						"code": code, "import_freight_method_id": "air", "profit_percent": profit, "profit_percent_source": "global_default", "pricing_warnings": []string{},
					},
				}},
			}})
		default:
			singleRequests++
		}
	}))
	defer server.Close()

	provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL, BatchSize: 1, MaxEntries: 10}, server.Client(), time.Now)
	scoped := provider.Prefetch(context.Background(), []string{"A", "B"})
	provider.mu.Lock()
	committedAssignments := len(provider.assignments)
	provider.mu.Unlock()
	if committedAssignments != 0 {
		t.Fatalf("first chunk became visible before default agreement: assignments=%d", committedAssignments)
	}
	for _, code := range []string{"A", "B"} {
		resolved := scoped.Resolve(context.Background(), code)
		if resolved.MethodID != "" || !contains(resolved.Warnings, "pricing_assignment_batch_contract_invalid") {
			t.Fatalf("mixed default revisions did not fail atomically for %s: %+v", code, resolved)
		}
	}
	if batchRequests != 2 || singleRequests != 0 {
		t.Fatalf("mixed revision handling made unsafe requests: batch=%d single=%d", batchRequests, singleRequests)
	}
}

func TestHTTPProviderBatchUnsupportedFallsBackWithDiagnostic(t *testing.T) {
	var singleRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/integration/catalog":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":1,"cny_to_irt":1},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air","price_per_kg_cny":1}]}}`)
		case "/pricing-assignments/batch":
			http.NotFound(w, r)
		default:
			singleRequests++
			fmt.Fprint(w, `{"data":{"import_freight_method_id":"air","profit_percent":"0"}}`)
		}
	}))
	defer server.Close()

	provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL}, server.Client(), time.Now)
	provider.Prefetch(context.Background(), []string{"A"})
	resolved := provider.Resolve(context.Background(), "A")
	if singleRequests != 1 || !contains(resolved.Warnings, "pricing_assignment_batch_unsupported") {
		t.Fatalf("unsupported batch fallback was not explicit and safe: requests=%d resolution=%+v", singleRequests, resolved)
	}
}

func TestHTTPProviderMalformedBatchFailsClosedWithoutSingleRequests(t *testing.T) {
	var singleRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/integration/catalog":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":1,"cny_to_irt":1},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air","price_per_kg_cny":1}]}}`)
		case "/pricing-assignments/batch":
			fmt.Fprint(w, `{"data":{"schema":"wrong","schema_version":"1.0.0","results":[]}}`)
		default:
			singleRequests++
			fmt.Fprint(w, `{"data":{"import_freight_method_id":"air","profit_percent":"0"}}`)
		}
	}))
	defer server.Close()

	provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL}, server.Client(), time.Now)
	provider.Prefetch(context.Background(), []string{"A"})
	resolved := provider.Resolve(context.Background(), "A")
	if singleRequests != 0 || !contains(resolved.Warnings, "pricing_assignment_batch_contract_invalid") {
		t.Fatalf("malformed batch did not fail closed: requests=%d resolution=%+v", singleRequests, resolved)
	}
}

func TestHTTPProviderFreshFailClosedDiagnosticBacksOffAcrossPrefetchRuns(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		warning string
	}{
		{name: "authentication", mode: "auth", warning: "pricing_assignment_batch_auth_failed"},
		{name: "transport", mode: "transport", warning: "pricing_assignment_batch_transport_failed"},
		{name: "malformed contract", mode: "malformed", warning: "pricing_assignment_batch_contract_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var batchRequests, singleRequests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/integration/catalog":
					fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":1,"cny_to_irt":1},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[]}}`)
				case "/pricing-assignments/batch":
					batchRequests++
					switch test.mode {
					case "auth":
						http.Error(w, "forbidden", http.StatusForbidden)
					case "transport":
						connection, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Errorf("hijack batch connection: %v", err)
							return
						}
						_ = connection.Close()
					case "malformed":
						fmt.Fprint(w, `{"data":{"schema":"wrong","schema_version":"1.0.0","results":[]}}`)
					}
				default:
					singleRequests++
				}
			}))
			defer server.Close()

			provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL, FreshFor: "5m", MaxEntries: 1}, server.Client(), time.Now)
			for run := 0; run < 2; run++ {
				scoped := provider.Prefetch(context.Background(), []string{"A", "B", "C"})
				for _, code := range []string{"A", "B", "C"} {
					resolved := scoped.Resolve(context.Background(), code)
					if !contains(resolved.Warnings, test.warning) {
						t.Fatalf("run %d Code %s lost fail-closed diagnostic %q: %+v", run, code, test.warning, resolved)
					}
				}
			}
			if batchRequests != 1 || singleRequests != 0 {
				t.Fatalf("fresh fail-closed diagnostic did not back off: batch=%d single=%d", batchRequests, singleRequests)
			}
		})
	}
}

func TestHTTPProviderBatchAmbiguityIsAuthoritative(t *testing.T) {
	var singleRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/integration/catalog":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":1,"cny_to_irt":1},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[]}}`)
		case "/pricing-assignments/batch":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.pricing-assignment-batch","schema_version":"1.0.0","requested_count":1,"resolved_count":0,"error_count":1,"maximum_codes":500,"default_percentage_markup":{"schema":"digitalogic.default-percentage-markup","schema_version":"1.0.0","configured":false,"type":"percentage","source":"unset","revision":"rev-unset"},"results":[{"code":"COLLISION","status":"error","error":{"code":"digitalogic_product_code_ambiguous","http_status":409,"retryable":false}}]}}`)
		default:
			singleRequests++
		}
	}))
	defer server.Close()

	provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL}, server.Client(), time.Now)
	provider.Prefetch(context.Background(), []string{"COLLISION"})
	resolved := provider.Resolve(context.Background(), "COLLISION")
	if singleRequests != 0 || !contains(resolved.Warnings, "product_pricing_assignment_ambiguous") {
		t.Fatalf("ambiguous Code was retried or lost: requests=%d resolution=%+v", singleRequests, resolved)
	}
}

func TestHTTPProviderBatchFailureUsesOnlyBoundedStaleAssignment(t *testing.T) {
	var batchRequests, singleRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/integration/catalog":
			fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":1,"cny_to_irt":1},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[{"id":"air","price_per_kg_cny":1}]}}`)
		case "/pricing-assignments/batch":
			batchRequests++
			http.Error(w, "offline", http.StatusServiceUnavailable)
		default:
			singleRequests++
			fmt.Fprint(w, `{"data":{"code":"A","import_freight_method_id":"air","profit_percent":"5","profit_percent_source":"product_override","pricing_warnings":[]}}`)
		}
	}))
	defer server.Close()

	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL, FreshFor: "1m", MaxStale: "10m"}, server.Client(), func() time.Time { return now })
	if initial := provider.Resolve(context.Background(), "A"); decimalText(initial.MarkupPercent) != "5" {
		t.Fatalf("initial assignment did not seed the cache: %+v", initial)
	}

	now = now.Add(2 * time.Minute)
	provider.Prefetch(context.Background(), []string{"A"})
	stale := provider.Resolve(context.Background(), "A")
	if batchRequests != 1 || singleRequests != 1 || decimalText(stale.MarkupPercent) != "5" || !contains(stale.Warnings, "pricing_assignment_batch_http_failed") || !contains(stale.Warnings, "product_pricing_assignment_stale") {
		t.Fatalf("batch failure did not use one bounded stale value: batch=%d single=%d resolution=%+v", batchRequests, singleRequests, stale)
	}
}

func TestHTTPProviderBatchAuthAndBodyLimitsFailClosed(t *testing.T) {
	tests := []struct {
		name          string
		code          string
		batchResponse func(http.ResponseWriter)
		warning       string
		batchRequests int
	}{
		{
			name: "auth", code: "A", warning: "pricing_assignment_batch_auth_failed", batchRequests: 1,
			batchResponse: func(w http.ResponseWriter) { http.Error(w, "forbidden", http.StatusForbidden) },
		},
		{
			name: "response limit", code: "A", warning: "pricing_assignment_batch_response_too_large", batchRequests: 1,
			batchResponse: func(w http.ResponseWriter) { fmt.Fprint(w, strings.Repeat("x", 1024)) },
		},
		{
			name: "request limit", code: strings.Repeat("A", 600), warning: "pricing_assignment_batch_request_too_large", batchRequests: 0,
			batchResponse: func(w http.ResponseWriter) { t.Error("oversized request reached the server") },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var batchRequests, singleRequests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/integration/catalog":
					fmt.Fprint(w, `{"data":{"schema":"digitalogic.integration-catalog","schema_version":"1.0.0","revision":"r1","currency":{"local":"IRT","cny_to_local":1,"cny_to_irt":1},"pricing":{"formula_id":"landed_price_v1","formula_revision":"1.0.0"},"import_freight_methods":[]}}`)
				case "/pricing-assignments/batch":
					batchRequests++
					test.batchResponse(w)
				default:
					singleRequests++
				}
			}))
			defer server.Close()

			provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL, MaxResponseBytes: 512}, server.Client(), time.Now)
			provider.Prefetch(context.Background(), []string{test.code})
			resolved := provider.Resolve(context.Background(), test.code)
			if batchRequests != test.batchRequests || singleRequests != 0 || !contains(resolved.Warnings, test.warning) {
				t.Fatalf("batch guard mismatch: batch=%d single=%d resolution=%+v", batchRequests, singleRequests, resolved)
			}
		})
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
		"absolute batch":          {BaseURL: "https://example.test/wp-json/digitalogic/v1", BatchAssignmentPath: "https://evil.test/batch"},
	} {
		t.Run(name, func(t *testing.T) {
			resolution := newHTTPProvider(cfg, nil, time.Now).Resolve(context.Background(), "A")
			if !contains(resolution.Warnings, "pricing_catalog_path_invalid") && !contains(resolution.Warnings, "pricing_assignment_path_invalid") && !contains(resolution.Warnings, "pricing_assignment_batch_path_invalid") {
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
