package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
	"github.com/gorilla/mux"
)

const excelPricingTestSecret = "excel-pricing-test-secret-value"

func TestExcelPricingSessionRequiresDirectLoopbackAndNoProxyEvidence(t *testing.T) {
	server := newExcelPricingTestServer(t, "http://127.0.0.1:9/wp-json/digitalogic/patris/product-sync")
	request := newExcelPricingRequest(http.MethodPost, "/api/pricing-sync/session", `{}`)
	request.RemoteAddr = "203.0.113.10:4000"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("remote session status=%d, want 403: %s", response.Code, response.Body.String())
	}

	request = newExcelPricingRequest(http.MethodPost, "/api/pricing-sync/session", `{}`)
	request.Header.Set("X-Forwarded-For", "127.0.0.1")
	response = httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("proxy-marked session status=%d, want 403: %s", response.Code, response.Body.String())
	}

	request = newExcelPricingRequest(http.MethodPost, "/api/pricing-sync/session", `{}`)
	request.Header.Set("Origin", "https://evil.example")
	response = httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin session status=%d, want 403: %s", response.Code, response.Body.String())
	}

	token := openExcelPricingSession(t, server)
	if len(token) != 43 {
		t.Fatalf("session token length=%d, want 43", len(token))
	}
	if values := response.Header().Values("Access-Control-Allow-Origin"); len(values) != 0 {
		t.Fatalf("pricing surface emitted CORS allowance: %v", values)
	}
}

func TestPricingSyncUsesGeneralRouteAndDoesNotExposeExcelAlias(t *testing.T) {
	server := newExcelPricingTestServer(t, "http://127.0.0.1:9/wp-json/digitalogic/patris/product-sync")

	canonical := newExcelPricingRequest(http.MethodPost, "/api/pricing-sync/session", `{}`)
	canonicalResponse := httptest.NewRecorder()
	server.router.ServeHTTP(canonicalResponse, canonical)
	if canonicalResponse.Code != http.StatusOK {
		t.Fatalf("canonical route status=%d: %s", canonicalResponse.Code, canonicalResponse.Body.String())
	}

	for _, legacyPath := range []string{
		"/api/excel/pricing-sync/session",
		"/api/excel/pricing-sync/state",
		"/api/excel/pricing-sync/preview",
		"/api/excel/pricing-sync/apply",
		"/api/spreadsheet/pricing-sync/session",
		"/api/spreadsheet/pricing-sync/state",
		"/api/spreadsheet/pricing-sync/preview",
		"/api/spreadsheet/pricing-sync/apply",
		"/spreadsheet/pricing-sync/state",
	} {
		legacy := newExcelPricingRequest(http.MethodPost, legacyPath, `{}`)
		legacyResponse := httptest.NewRecorder()
		server.router.ServeHTTP(legacyResponse, legacy)
		if legacyResponse.Code != http.StatusNotFound {
			t.Fatalf("legacy client-named path %q status=%d, want 404: %s", legacyPath, legacyResponse.Code, legacyResponse.Body.String())
		}
	}
}

func TestExcelPricingStateInjectsProtectedCredentialAndCanonicalSource(t *testing.T) {
	var received map[string]interface{}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wp-json/digitalogic/pricing/sync/state" {
			t.Fatalf("remote path=%q", r.URL.Path)
		}
		if got := r.Header.Get(updateout.ProductSyncSecretHeader); got != excelPricingTestSecret {
			t.Fatalf("remote secret header=%q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "" {
			t.Fatalf("state forwarded idempotency header=%q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		writeRemotePricingResponse(t, w, excelPricingStateSchema, excelPricingRevisionForTest("settings-a"))
	}))
	defer remote.Close()

	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	source := excelPricingStateSourceForTest()
	var canonicalCalls atomic.Int32
	projection := canonicalProjectionSequence(source)
	server.excelPricing.canonical = func(ctx context.Context) (recordpipe.Result, error) {
		canonicalCalls.Add(1)
		return projection(ctx)
	}
	token := openExcelPricingSession(t, server)
	request := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/state",
		validExcelPricingStateBody(source, 1, 250),
		token,
	)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("state status=%d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), excelPricingTestSecret) {
		t.Fatal("state response exposed remote credential")
	}
	if received["schema"] != excelPricingRemoteRequestSchema || received["operation"] != "state" {
		t.Fatalf("remote identity=%#v", received)
	}
	if received["client_id"] != excelPricingContractClientID ||
		received["channel"] != excelPricingContractChannel ||
		received["request_id"] != "excel-state-test-0001" {
		t.Fatalf("remote client context=%#v", received)
	}
	if received["page"] != float64(1) || received["limit"] != float64(250) {
		t.Fatalf("remote pagination=%#v/%#v, want 1/250", received["page"], received["limit"])
	}
	remoteSource, ok := received["source"].(map[string]interface{})
	if !ok || remoteSource["id"] != source.ID || remoteSource["dataset"] != source.Dataset || remoteSource["revision"] != source.Revision {
		t.Fatalf("remote source=%#v, want %#v", received["source"], source)
	}
	if canonicalCalls.Load() != 1 {
		t.Fatalf("read-only state validated canonical source %d time(s), want 1", canonicalCalls.Load())
	}
}

func TestExcelPricingStateRejectsPageSizeAboveBound(t *testing.T) {
	server := newExcelPricingTestServer(t, "https://digitalogic.example/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	request := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/state",
		validExcelPricingStateBody(excelPricingStateSourceForTest(), 1, 251),
		token,
	)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("state status=%d, want 400: %s", response.Code, response.Body.String())
	}
}

func TestExcelPricingStateRequiresExpectedSourceIdentity(t *testing.T) {
	server := newExcelPricingTestServer(t, "https://digitalogic.example/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	mismatched := excelPricingStateSourceForTest()
	mismatched.ID = "unexpected-source"
	wrongDataset := excelPricingStateSourceForTest()
	wrongDataset.Dataset = "other.db"
	invalidRevision := excelPricingStateSourceForTest()
	invalidRevision.Revision = "SHA256:" + strings.Repeat("A", 64)
	for name, body := range map[string]string{
		"missing":          `{"schema":"patris.excel-pricing-companion-request/v1","schema_version":1,"operation":"state","page":1,"limit":1,"locale":"fa"}`,
		"wrong ID":         validExcelPricingStateBody(mismatched, 1, 1),
		"wrong dataset":    validExcelPricingStateBody(wrongDataset, 1, 1),
		"invalid revision": validExcelPricingStateBody(invalidRevision, 1, 1),
	} {
		t.Run(name, func(t *testing.T) {
			request := authenticatedExcelPricingRequest(
				http.MethodPost,
				"/api/pricing-sync/state",
				body,
				token,
			)
			response := httptest.NewRecorder()
			server.router.ServeHTTP(response, request)
			want := http.StatusBadRequest
			if name == "wrong ID" || name == "wrong dataset" {
				want = http.StatusConflict
			}
			if response.Code != want {
				t.Fatalf("state status=%d, want %d: %s", response.Code, want, response.Body.String())
			}
		})
	}
}

func TestExcelPricingMutationRejectsCallerSuppliedSource(t *testing.T) {
	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		remoteCalls.Add(1)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	var canonicalCalls atomic.Int32
	server.excelPricing.canonical = func(context.Context) (recordpipe.Result, error) {
		canonicalCalls.Add(1)
		return recordpipe.Result{}, nil
	}
	stateRevision := excelPricingRevisionForTest("settings-preview-source-rejected")
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(validExcelPricingMutationBody(
		"preview", "excel-preview-source-0001", stateRevision, "", "",
	)), &payload); err != nil {
		t.Fatal(err)
	}
	payload["source"] = excelPricingStateSourceForTest()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	token := openExcelPricingSession(t, server)
	request := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/preview", string(body), token,
	)
	request.Header.Set("Idempotency-Key", "excel-preview-source-0001")
	request.Header.Set("If-Match", `"`+stateRevision+`"`)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("preview status=%d, want 400: %s", response.Code, response.Body.String())
	}
	if canonicalCalls.Load() != 0 || remoteCalls.Load() != 0 {
		t.Fatalf("rejected source reached canonical=%d remote=%d", canonicalCalls.Load(), remoteCalls.Load())
	}
}

func TestExcelPricingPreviewPreservesOptimisticHeadersAndRejectsDrift(t *testing.T) {
	stateRevision := excelPricingRevisionForTest("settings-preview")
	source := canonical.Source{
		ID: "source", Dataset: "dataset", Revision: excelPricingRevisionForTest("source"),
	}
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("Idempotency-Key"); got != "excel-preview-0001" {
			t.Fatalf("idempotency header=%q", got)
		}
		if got := r.Header.Get("If-Match"); got != `"`+stateRevision+`"` {
			t.Fatalf("If-Match=%q", got)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		expected := map[string]interface{}{
			"schema":                  excelPricingRemoteRequestSchema,
			"schema_version":          float64(1),
			"operation":               "preview",
			"client_id":               excelPricingContractClientID,
			"channel":                 excelPricingContractChannel,
			"request_id":              "excel-preview-0001",
			"source":                  jsonMapForTest(t, source),
			"idempotency_key":         "excel-preview-0001",
			"expected_state_revision": stateRevision,
			"settings": map[string]interface{}{
				"dollar_price":              float64(187891),
				"yuan_price":                float64(29500),
				"effective_date":            "2026-07-27",
				"usd_effective_date":        "2026-07-26",
				"cny_effective_date":        "2026-07-27",
				"profit_margin_percent":     float64(30),
				"air_express_price_per_kg":  float64(120),
				"air_express_currency":      "CNY",
				"shipping_catalog_revision": excelPricingRevisionForTest("shipping-catalog"),
				"price_rounding_digits":     float64(2),
				"price_rounding_mode":       "nearest_half_up",
			},
			"product_changes": []interface{}{},
		}
		if !reflect.DeepEqual(payload, expected) {
			t.Fatalf("forwarded universal pricing JSON mismatch:\n got: %#v\nwant: %#v", payload, expected)
		}
		writeRemotePricingResponse(t, w, excelPricingPreviewSchema, stateRevision)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.excelPricing.canonical = canonicalProjectionSequence(source)
	token := openExcelPricingSession(t, server)
	body := validExcelPricingMutationBody("preview", "excel-preview-0001", stateRevision, "", "")
	request := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/preview", body, token)
	request.Header.Set("Idempotency-Key", "excel-preview-0001")
	request.Header.Set("If-Match", `"`+stateRevision+`"`)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("preview status=%d: %s", response.Code, response.Body.String())
	}

	drift := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/preview", body, token)
	drift.Header.Set("Idempotency-Key", "different-key")
	drift.Header.Set("If-Match", `"`+stateRevision+`"`)
	response = httptest.NewRecorder()
	server.router.ServeHTTP(response, drift)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("header drift status=%d, want 400: %s", response.Code, response.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("remote preview calls=%d, want 1", calls.Load())
	}
}

func TestExcelPricingMutationRejectsNullChangesAndUppercaseRevision(t *testing.T) {
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeRemotePricingResponse(t, w, excelPricingPreviewSchema, excelPricingRevisionForTest("settings"))
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.excelPricing.canonical = canonicalProjectionSequence(excelPricingStateSourceForTest())
	token := openExcelPricingSession(t, server)
	stateRevision := excelPricingRevisionForTest("settings")

	for name, body := range map[string]string{
		"null changes": strings.Replace(
			validExcelPricingMutationBody("preview", "excel-preview-0002", stateRevision, "", ""),
			`"product_changes":[]`,
			`"product_changes":null`,
			1,
		),
		"uppercase revision": strings.Replace(
			validExcelPricingMutationBody("preview", "excel-preview-0002", stateRevision, "", ""),
			stateRevision,
			strings.ToUpper(stateRevision),
			1,
		),
	} {
		t.Run(name, func(t *testing.T) {
			request := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/preview", body, token)
			request.Header.Set("Idempotency-Key", "excel-preview-0002")
			headerRevision := stateRevision
			if name == "uppercase revision" {
				headerRevision = strings.ToUpper(stateRevision)
			}
			request.Header.Set("If-Match", `"`+headerRevision+`"`)
			response := httptest.NewRecorder()
			server.router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400: %s", response.Code, response.Body.String())
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("remote preview calls=%d, want 0", calls.Load())
	}

	for _, leading := range []string{".", ":", "_", "-"} {
		idempotency := leading + "excel-preview-0003"
		body := validExcelPricingMutationBody("preview", idempotency, stateRevision, "", "")
		request := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/preview", body, token)
		request.Header.Set("Idempotency-Key", idempotency)
		request.Header.Set("If-Match", `"`+stateRevision+`"`)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("leading %q idempotency status=%d, want 400: %s", leading, response.Code, response.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid idempotency reached remote: calls=%d", calls.Load())
	}
}

func TestExcelPricingMutationRequiresCompleteAtomicSettingsAndClientContext(t *testing.T) {
	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		writeRemotePricingResponse(t, w, excelPricingPreviewSchema, excelPricingRevisionForTest("settings"))
	}))
	defer remote.Close()

	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	var canonicalCalls atomic.Int32
	server.excelPricing.canonical = func(context.Context) (recordpipe.Result, error) {
		canonicalCalls.Add(1)
		return recordpipe.Result{}, nil
	}
	token := openExcelPricingSession(t, server)
	stateRevision := excelPricingRevisionForTest("settings-complete")

	mutations := map[string]func(map[string]interface{}){
		"missing USD date": func(payload map[string]interface{}) {
			delete(payload["settings"].(map[string]interface{}), "usd_effective_date")
		},
		"CNY date differs from effective date": func(payload map[string]interface{}) {
			payload["settings"].(map[string]interface{})["cny_effective_date"] = "2026-07-28"
		},
		"legacy default profit field": func(payload map[string]interface{}) {
			settings := payload["settings"].(map[string]interface{})
			delete(settings, "profit_margin_percent")
			settings["default_profit_percent"] = 30
		},
		"missing shipping revision": func(payload map[string]interface{}) {
			delete(payload["settings"].(map[string]interface{}), "shipping_catalog_revision")
		},
		"missing price rounding digits": func(payload map[string]interface{}) {
			delete(payload["settings"].(map[string]interface{}), "price_rounding_digits")
		},
		"fractional price rounding digits": func(payload map[string]interface{}) {
			payload["settings"].(map[string]interface{})["price_rounding_digits"] = 2.5
		},
		"out of range price rounding digits": func(payload map[string]interface{}) {
			payload["settings"].(map[string]interface{})["price_rounding_digits"] = 10
		},
		"wrong price rounding mode": func(payload map[string]interface{}) {
			payload["settings"].(map[string]interface{})["price_rounding_mode"] = "bankers"
		},
		"invalid shipping currency": func(payload map[string]interface{}) {
			payload["settings"].(map[string]interface{})["air_express_currency"] = "USD"
		},
		"zero shipping amount": func(payload map[string]interface{}) {
			payload["settings"].(map[string]interface{})["air_express_price_per_kg"] = 0
		},
		"wrong client ID": func(payload map[string]interface{}) {
			payload["client_id"] = "another-client"
		},
		"request and idempotency mismatch": func(payload map[string]interface{}) {
			payload["request_id"] = "excel-preview-different"
		},
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(validExcelPricingMutationBody(
				"preview", "excel-preview-complete-0001", stateRevision, "", "",
			)), &payload); err != nil {
				t.Fatal(err)
			}
			mutate(payload)
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			request := authenticatedExcelPricingRequest(
				http.MethodPost,
				"/api/pricing-sync/preview",
				string(body),
				token,
			)
			request.Header.Set("Idempotency-Key", "excel-preview-complete-0001")
			request.Header.Set("If-Match", `"`+stateRevision+`"`)
			response := httptest.NewRecorder()
			server.router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400: %s", response.Code, response.Body.String())
			}
		})
	}
	if canonicalCalls.Load() != 0 || remoteCalls.Load() != 0 {
		t.Fatalf("invalid atomic settings reached canonical=%d remote=%d", canonicalCalls.Load(), remoteCalls.Load())
	}
}

func TestValidateExcelPricingSettingsUsesUniversalShippingRange(t *testing.T) {
	settings := excelPricingSettings{
		DollarPrice:             187891,
		YuanPrice:               29500,
		EffectiveDate:           "2026-07-27",
		USDEffectiveDate:        "2026-07-26",
		CNYEffectiveDate:        "2026-07-27",
		ProfitMarginPercent:     json.Number("30.25"),
		AirExpressPricePerKG:    json.Number("123456789012345678.123456789012"),
		AirExpressCurrency:      "CNY",
		ShippingCatalogRevision: excelPricingRevisionForTest("shipping-catalog"),
		PriceRoundingDigits:     json.Number("2"),
		PriceRoundingMode:       "nearest_half_up",
	}
	if err := validateExcelPricingSettings(settings); err != nil {
		t.Fatalf("universal shipping range was rejected: %v", err)
	}
	for _, invalid := range []string{
		"1234567890123456789",
		"1.1234567890123",
		"1e3",
		"0",
	} {
		settings.AirExpressPricePerKG = json.Number(invalid)
		if err := validateExcelPricingSettings(settings); err == nil {
			t.Errorf("invalid shipping amount %q was accepted", invalid)
		}
	}
}

func TestExcelPricingApplyRegeneratesDispatchesAndRefetchesState(t *testing.T) {
	oldSource := canonical.Source{ID: "source", Dataset: "dataset", Revision: excelPricingRevisionForTest("old-source")}
	newSource := canonical.Source{ID: "source", Dataset: "dataset", Revision: excelPricingRevisionForTest("new-source")}
	stateRevision := excelPricingRevisionForTest("new-settings")
	previewDigest := excelPricingRevisionForTest("preview")
	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		var payload struct {
			Operation string           `json:"operation"`
			Source    canonical.Source `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		switch payload.Operation {
		case "apply":
			if payload.Source != oldSource {
				t.Fatalf("apply source=%#v, want %#v", payload.Source, oldSource)
			}
			writeRemotePricingResponse(t, w, excelPricingApplySchema, stateRevision)
		case "state":
			if payload.Source != newSource {
				t.Fatalf("post-apply state source=%#v, want %#v", payload.Source, newSource)
			}
			writeRemotePricingResponse(t, w, excelPricingStateSchema, stateRevision)
		default:
			t.Fatalf("unexpected remote operation %q", payload.Operation)
		}
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.canonicalProjection = newCanonicalProjectionCache()
	seedCanonicalProjectionCacheForTest(server, "pre-apply")
	canonicalGeneration := canonicalProjectionCacheGenerationForTest(server)
	server.excelPricing.canonical = canonicalProjectionSequence(oldSource, newSource)
	var dispatched atomic.Int32
	server.excelPricing.dispatch = func(_ context.Context, cfg updateout.Config, event updateout.Event) (updateout.DeliveryResult, error) {
		dispatched.Add(1)
		if cfg.ProductSyncSecretEnv != "PATRIS_TEST_EXCEL_PRICING_SECRET" {
			t.Fatalf("dispatch secret env=%q", cfg.ProductSyncSecretEnv)
		}
		if cfg.RetryAttempts != 10 || cfg.RetryBackoff != "2s" {
			t.Fatalf("protected pricing dispatch retry policy=%d/%q, want 10/2s", cfg.RetryAttempts, cfg.RetryBackoff)
		}
		if event.Contract == nil || event.Contract.Source != newSource || event.SnapshotContract == nil {
			t.Fatalf("dispatch event did not carry regenerated contract: %#v", event)
		}
		return updateout.DeliveryResult{
			HTTPStatus: http.StatusOK,
			Status:     "accepted",
			EventID:    event.Contract.EventID,
			Attempts:   1,
		}, nil
	}
	token := openExcelPricingSession(t, server)
	body := validExcelPricingMutationBody("apply", "excel-apply-0001", stateRevision, previewDigest, "APPLY")
	bindExcelPricingPreviewForTest(t, server, body)
	server.excelPricing.snapshots.mu.Lock()
	server.excelPricing.snapshots.cache["pre-apply"] = &excelPricingSnapshot{
		stateRevision: excelPricingRevisionForTest("stale-settings"),
	}
	server.excelPricing.snapshots.mu.Unlock()
	request := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/apply", body, token)
	request.Header.Set("Idempotency-Key", "excel-apply-0001")
	request.Header.Set("If-Match", `"`+stateRevision+`"`)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("apply status=%d: %s", response.Code, response.Body.String())
	}
	if dispatched.Load() != 1 || remoteCalls.Load() != 2 {
		t.Fatalf("dispatch=%d remote_calls=%d, want 1/2", dispatched.Load(), remoteCalls.Load())
	}
	server.excelPricing.snapshots.mu.Lock()
	_, staleCacheRemained := server.excelPricing.snapshots.cache["pre-apply"]
	server.excelPricing.snapshots.mu.Unlock()
	if staleCacheRemained {
		t.Fatal("successful apply did not invalidate the pre-apply snapshot cache")
	}
	assertCanonicalProjectionInvalidatedForTest(t, server, canonicalGeneration, "apply")
	if !strings.Contains(response.Body.String(), excelPricingApplySchema) ||
		strings.Contains(response.Body.String(), excelPricingTestSecret) {
		t.Fatalf("unsafe or wrong apply response: %s", response.Body.String())
	}
	server.excelPricing.snapshots.mu.Lock()
	server.excelPricing.snapshots.cache["post-apply"] = &excelPricingSnapshot{
		stateRevision: stateRevision,
	}
	server.excelPricing.snapshots.mu.Unlock()
	seedCanonicalProjectionCacheForTest(server, "post-apply")
	replayCanonicalGeneration := canonicalProjectionCacheGenerationForTest(server)
	replayRequest := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/apply", body, token)
	replayRequest.Header.Set("Idempotency-Key", "excel-apply-0001")
	replayRequest.Header.Set("If-Match", `"`+stateRevision+`"`)
	replayResponse := httptest.NewRecorder()
	server.router.ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusOK || replayResponse.Body.String() != response.Body.String() {
		t.Fatalf("apply replay status=%d body=%s", replayResponse.Code, replayResponse.Body.String())
	}
	if dispatched.Load() != 1 || remoteCalls.Load() != 2 {
		t.Fatalf("apply replay repeated effects: dispatch=%d remote=%d", dispatched.Load(), remoteCalls.Load())
	}
	server.excelPricing.snapshots.mu.Lock()
	_, replayPreservedCache := server.excelPricing.snapshots.cache["post-apply"]
	server.excelPricing.snapshots.mu.Unlock()
	if !replayPreservedCache {
		t.Fatal("idempotent apply replay invalidated cache without a new mutation")
	}
	server.canonicalProjection.mu.Lock()
	replayPreservedCanonical := server.canonicalProjection.cached != nil &&
		server.canonicalProjection.generation == replayCanonicalGeneration
	server.canonicalProjection.mu.Unlock()
	if !replayPreservedCanonical {
		t.Fatal("idempotent apply replay invalidated the canonical projection cache")
	}

	var conflictingPayload map[string]interface{}
	if err := json.Unmarshal([]byte(body), &conflictingPayload); err != nil {
		t.Fatal(err)
	}
	conflictingPayload["settings"].(map[string]interface{})["dollar_price"] = float64(187892)
	conflictingBody, _ := json.Marshal(conflictingPayload)
	conflictRequest := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/apply", string(conflictingBody), token,
	)
	conflictRequest.Header.Set("Idempotency-Key", "excel-apply-0001")
	conflictRequest.Header.Set("If-Match", `"`+stateRevision+`"`)
	conflictResponse := httptest.NewRecorder()
	server.router.ServeHTTP(conflictResponse, conflictRequest)
	if conflictResponse.Code != http.StatusConflict ||
		!strings.Contains(conflictResponse.Body.String(), "idempotency_conflict") {
		t.Fatalf("apply idempotency conflict status=%d: %s", conflictResponse.Code, conflictResponse.Body.String())
	}
	if dispatched.Load() != 1 || remoteCalls.Load() != 2 {
		t.Fatalf("conflicting apply repeated effects: dispatch=%d remote=%d", dispatched.Load(), remoteCalls.Load())
	}
}

func TestExcelPricingApplyRejectsAmbiguousDeferredProductSync(t *testing.T) {
	oldSource := canonical.Source{ID: "source", Dataset: "dataset", Revision: excelPricingRevisionForTest("old-source")}
	newSource := canonical.Source{ID: "source", Dataset: "dataset", Revision: excelPricingRevisionForTest("new-source")}
	stateRevision := excelPricingRevisionForTest("new-settings")
	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		writeRemotePricingResponse(t, w, excelPricingApplySchema, stateRevision)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.excelPricing.canonical = canonicalProjectionSequence(oldSource, newSource)
	server.excelPricing.dispatch = func(_ context.Context, _ updateout.Config, event updateout.Event) (updateout.DeliveryResult, error) {
		return updateout.DeliveryResult{
			HTTPStatus:        http.StatusOK,
			Status:            "accepted",
			EventID:           event.Contract.EventID,
			Attempts:          1,
			DeferredProducts:  1,
			DeferredAmbiguous: 1,
		}, nil
	}
	token := openExcelPricingSession(t, server)
	body := validExcelPricingMutationBody(
		"apply",
		"excel-apply-0002",
		stateRevision,
		excelPricingRevisionForTest("preview"),
		"APPLY",
	)
	bindExcelPricingPreviewForTest(t, server, body)
	server.excelPricing.snapshots.mu.Lock()
	server.excelPricing.snapshots.cache["pre-failed-verification"] = &excelPricingSnapshot{
		stateRevision: excelPricingRevisionForTest("stale-before-failed-verification"),
	}
	server.excelPricing.snapshots.mu.Unlock()
	request := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/apply", body, token)
	request.Header.Set("Idempotency-Key", "excel-apply-0002")
	request.Header.Set("If-Match", `"`+stateRevision+`"`)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("apply status=%d, want 502: %s", response.Code, response.Body.String())
	}
	if remoteCalls.Load() != 1 {
		t.Fatalf("remote calls=%d, want apply only and no state readback", remoteCalls.Load())
	}
	server.excelPricing.snapshots.mu.Lock()
	_, staleCacheRemained := server.excelPricing.snapshots.cache["pre-failed-verification"]
	server.excelPricing.snapshots.mu.Unlock()
	if staleCacheRemained {
		t.Fatal("remote apply success retained stale cache after local verification failure")
	}
}

func TestExcelPricingApplyAcceptsMissingDeferredProductSync(t *testing.T) {
	oldSource := canonical.Source{ID: "source", Dataset: "dataset", Revision: excelPricingRevisionForTest("old-source")}
	newSource := canonical.Source{ID: "source", Dataset: "dataset", Revision: excelPricingRevisionForTest("new-source")}
	stateRevision := excelPricingRevisionForTest("new-settings")
	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		switch {
		case strings.HasSuffix(r.URL.Path, "/apply"):
			writeRemotePricingResponse(t, w, excelPricingApplySchema, stateRevision)
		case strings.HasSuffix(r.URL.Path, "/state"):
			writeRemotePricingResponse(t, w, excelPricingStateSchema, stateRevision)
		default:
			t.Fatalf("unexpected remote path %q", r.URL.Path)
		}
	}))
	defer remote.Close()

	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.excelPricing.canonical = canonicalProjectionSequence(oldSource, newSource)
	server.excelPricing.dispatch = func(_ context.Context, _ updateout.Config, event updateout.Event) (updateout.DeliveryResult, error) {
		return updateout.DeliveryResult{
			HTTPStatus:       http.StatusOK,
			Status:           "accepted",
			EventID:          event.Contract.EventID,
			Attempts:         1,
			DeferredProducts: 120,
			DeferredMissing:  120,
		}, nil
	}

	token := openExcelPricingSession(t, server)
	body := validExcelPricingMutationBody(
		"apply",
		"excel-apply-0003",
		stateRevision,
		excelPricingRevisionForTest("preview"),
		"APPLY",
	)
	bindExcelPricingPreviewForTest(t, server, body)
	request := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/apply", body, token)
	request.Header.Set("Idempotency-Key", "excel-apply-0003")
	request.Header.Set("If-Match", `"`+stateRevision+`"`)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("apply status=%d, want 200: %s", response.Code, response.Body.String())
	}
	if remoteCalls.Load() != 2 {
		t.Fatalf("remote calls=%d, want apply plus state readback", remoteCalls.Load())
	}
}

func TestExcelPricingRemoteErrorsAndRedirectsAreSecretSafe(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/state") {
			w.Header().Set("Location", "/credential-leak")
			w.WriteHeader(http.StatusFound)
			_, _ = io.WriteString(w, `{"code":"remote_conflict","message":"`+excelPricingTestSecret+`"}`)
			return
		}
		t.Fatal("redirect was followed")
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.excelPricing.canonical = canonicalProjectionSequence(excelPricingStateSourceForTest())
	token := openExcelPricingSession(t, server)
	request := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/state",
		validExcelPricingStateBody(excelPricingStateSourceForTest(), 1, 1),
		token,
	)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("redirect status=%d, want 502: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), excelPricingTestSecret) ||
		strings.Contains(response.Body.String(), "credential-leak") {
		t.Fatalf("remote error leaked protected material: %s", response.Body.String())
	}
}

func TestExcelPricingRejectsNonJSONRemoteSuccess(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `{"schema":"`+excelPricingStateSchema+`","state_revision":"`+
			excelPricingRevisionForTest("settings")+`"}`)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.excelPricing.canonical = canonicalProjectionSequence(excelPricingStateSourceForTest())
	token := openExcelPricingSession(t, server)
	request := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/state",
		validExcelPricingStateBody(excelPricingStateSourceForTest(), 1, 1),
		token,
	)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502: %s", response.Code, response.Body.String())
	}
}

func TestExcelPricingBoundsTimeoutsAndMessageSizes(t *testing.T) {
	if excelPricingOperationTimeout != 8*time.Minute {
		t.Fatalf(
			"pricing operation timeout=%s, want %s",
			excelPricingOperationTimeout,
			8*time.Minute,
		)
	}
	for value, want := range map[string]time.Duration{
		"invalid": 10 * time.Second,
		"1ms":     time.Second,
		"2m":      2 * time.Minute,
		"5m":      2 * time.Minute,
		"15s":     15 * time.Second,
	} {
		if got := excelPricingRemoteTimeout(value); got != want {
			t.Errorf("timeout %q=%s, want %s", value, got, want)
		}
	}

	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("x"), excelPricingMaxResponseBytes+1))
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.excelPricing.canonical = canonicalProjectionSequence(excelPricingStateSourceForTest())
	token := openExcelPricingSession(t, server)

	oversized := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/state",
		`{"schema":"`+excelPricingLocalRequestSchema+`","padding":"`+
			strings.Repeat("x", excelPricingMaxRequestBytes)+`"}`,
		token,
	)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, oversized)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized request status=%d, want 400: %s", response.Code, response.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("oversized request reached remote: calls=%d", calls.Load())
	}

	request := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/state",
		validExcelPricingStateBody(excelPricingStateSourceForTest(), 1, 1),
		token,
	)
	response = httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("oversized response status=%d, want 502: %s", response.Code, response.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("remote calls=%d, want 1", calls.Load())
	}
}

func TestExcelPricingRemoteURLIsSameOriginAndFixedPath(t *testing.T) {
	got, err := excelPricingRemoteURL(
		"https://digitalogic.ir/subdir/wp-json/digitalogic/patris/product-sync",
		"preview",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://digitalogic.ir/subdir/wp-json/digitalogic/pricing/sync/preview" {
		t.Fatalf("remote pricing URL=%q", got)
	}
	for _, candidate := range []string{
		"http://digitalogic.ir/wp-json/digitalogic/patris/product-sync",
		"https://user@digitalogic.ir/wp-json/digitalogic/patris/product-sync",
		"https://digitalogic.ir/wp-json/digitalogic/patris/product-sync?token=x",
		"https://digitalogic.ir/not-rest/product-sync",
	} {
		if _, err := excelPricingRemoteURL(candidate, "state"); err == nil {
			t.Errorf("unsafe URL accepted: %s", candidate)
		}
	}
}

func newExcelPricingTestServer(t *testing.T, productSyncURL string) *Server {
	t.Helper()
	t.Setenv("PATRIS_TEST_EXCEL_PRICING_SECRET", excelPricingTestSecret)
	manager, err := appconfig.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Update(func(cfg *appconfig.Config) {
		cfg.Canonical.SourceID = "source"
		cfg.SendUpdates = updateout.Config{
			Enabled:              true,
			URL:                  productSyncURL,
			Method:               http.MethodPost,
			Format:               "json",
			Mode:                 "full",
			Initial:              true,
			RequireContract:      true,
			Timeout:              "2s",
			RetryAttempts:        1,
			ProductSyncSecretEnv: "PATRIS_TEST_EXCEL_PRICING_SECRET",
		}
	}); err != nil {
		t.Fatal(err)
	}
	state := newExcelPricingState()
	state.canonical = canonicalProjectionSequence(excelPricingStateSourceForTest())
	server := &Server{
		router:       mux.NewRouter(),
		config:       manager,
		excelPricing: state,
		dbPath:       "dataset",
	}
	server.setupRoutes()
	return server
}

func openExcelPricingSession(t *testing.T, server *Server) string {
	t.Helper()
	request := newExcelPricingRequest(http.MethodPost, "/api/pricing-sync/session", `{}`)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("session status=%d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Schema    string `json:"schema"`
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Schema != excelPricingSessionSchema {
		t.Fatalf("session schema=%q", payload.Schema)
	}
	return payload.CSRFToken
}

func newExcelPricingRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, "http://127.0.0.1:18080"+path, bytes.NewBufferString(body))
	request.Host = "127.0.0.1:18080"
	request.RemoteAddr = "127.0.0.1:49152"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(excelPricingClientHeader, excelPricingClientID)
	return request
}

func authenticatedExcelPricingRequest(method, path, body, token string) *http.Request {
	request := newExcelPricingRequest(method, path, body)
	request.Header.Set(excelPricingCSRFHeader, token)
	return request
}

func validExcelPricingMutationBody(operation, idempotency, stateRevision, previewDigest, confirmation string) string {
	payload := map[string]interface{}{
		"schema":                  excelPricingLocalRequestSchema,
		"schema_version":          1,
		"operation":               operation,
		"client_id":               excelPricingContractClientID,
		"channel":                 excelPricingContractChannel,
		"request_id":              idempotency,
		"idempotency_key":         idempotency,
		"expected_state_revision": stateRevision,
		"settings": map[string]interface{}{
			"dollar_price":              187891,
			"yuan_price":                29500,
			"effective_date":            "2026-07-27",
			"usd_effective_date":        "2026-07-26",
			"cny_effective_date":        "2026-07-27",
			"profit_margin_percent":     30,
			"air_express_price_per_kg":  120,
			"air_express_currency":      "CNY",
			"shipping_catalog_revision": excelPricingRevisionForTest("shipping-catalog"),
			"price_rounding_digits":     2,
			"price_rounding_mode":       "nearest_half_up",
		},
		"product_changes": []interface{}{},
	}
	if previewDigest != "" {
		payload["preview_digest"] = previewDigest
	}
	if confirmation != "" {
		payload["confirmation"] = confirmation
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func bindExcelPricingPreviewForTest(t *testing.T, server *Server, applyBody string) {
	t.Helper()
	var request excelPricingLocalRequest
	if err := json.Unmarshal([]byte(applyBody), &request); err != nil {
		t.Fatal(err)
	}
	if !server.excelPricing.mutations.bindPreview(
		request.PreviewDigest,
		excelPricingPreviewBindingFingerprint(request),
	) {
		t.Fatal("preview binding unexpectedly conflicted")
	}
}

func validExcelPricingStateBody(source canonical.Source, page, limit int) string {
	payload := map[string]interface{}{
		"schema":         excelPricingLocalRequestSchema,
		"schema_version": 1,
		"operation":      "state",
		"client_id":      excelPricingContractClientID,
		"channel":        excelPricingContractChannel,
		"request_id":     "excel-state-test-0001",
		"source":         source,
		"page":           page,
		"limit":          limit,
		"locale":         "fa",
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func jsonMapForTest(t *testing.T, value interface{}) map[string]interface{} {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func excelPricingStateSourceForTest() canonical.Source {
	return canonical.Source{
		ID:       "source",
		Dataset:  "dataset",
		Revision: excelPricingRevisionForTest("state-source"),
	}
}

func canonicalProjectionSequence(sources ...canonical.Source) func(context.Context) (recordpipe.Result, error) {
	var index atomic.Int32
	return func(context.Context) (recordpipe.Result, error) {
		position := int(index.Add(1)) - 1
		if position >= len(sources) {
			position = len(sources) - 1
		}
		source := sources[position]
		return recordpipe.Result{Contract: &canonical.Envelope{
			Schema:        canonical.ContractName,
			EventType:     "snapshot",
			EventID:       excelPricingRevisionForTest("event-" + source.Revision),
			Source:        source,
			GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
			Products:      []canonical.Product{},
			Categories:    []canonical.Category{},
			ExcludedCodes: []string{},
			Warnings:      []string{},
		}}, nil
	}
}

func writeRemotePricingResponse(t *testing.T, w http.ResponseWriter, schema, stateRevision string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"schema":         schema,
		"state_revision": stateRevision,
		"status":         "ready",
		"warnings":       []interface{}{},
	})
}

func excelPricingRevisionForTest(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(digest[:])
}
