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
	request := newExcelPricingRequest(http.MethodPost, "/api/excel/pricing-sync/session", `{}`)
	request.RemoteAddr = "203.0.113.10:4000"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("remote session status=%d, want 403: %s", response.Code, response.Body.String())
	}

	request = newExcelPricingRequest(http.MethodPost, "/api/excel/pricing-sync/session", `{}`)
	request.Header.Set("X-Forwarded-For", "127.0.0.1")
	response = httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("proxy-marked session status=%d, want 403: %s", response.Code, response.Body.String())
	}

	request = newExcelPricingRequest(http.MethodPost, "/api/excel/pricing-sync/session", `{}`)
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

func TestExcelPricingStateInjectsProtectedCredentialAndCanonicalSource(t *testing.T) {
	var received map[string]interface{}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wp-json/digitalogic/excel/pricing-sync/state" {
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
		"/api/excel/pricing-sync/state",
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
	if received["page"] != float64(1) || received["limit"] != float64(250) {
		t.Fatalf("remote pagination=%#v/%#v, want 1/250", received["page"], received["limit"])
	}
	remoteSource, ok := received["source"].(map[string]interface{})
	if !ok || remoteSource["id"] != source.ID || remoteSource["dataset"] != source.Dataset || remoteSource["revision"] != source.Revision {
		t.Fatalf("remote source=%#v, want %#v", received["source"], source)
	}
	if canonicalCalls.Load() != 0 {
		t.Fatalf("read-only state rebuilt canonical source %d time(s), want 0", canonicalCalls.Load())
	}
}

func TestExcelPricingStateRejectsPageSizeAboveBound(t *testing.T) {
	server := newExcelPricingTestServer(t, "https://digitalogic.example/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	request := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/excel/pricing-sync/state",
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
				"/api/excel/pricing-sync/state",
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
		http.MethodPost, "/api/excel/pricing-sync/preview", string(body), token,
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
		if changes, ok := payload["product_changes"].([]interface{}); !ok || len(changes) != 0 {
			t.Fatalf("product_changes=%#v, want empty array", payload["product_changes"])
		}
		writeRemotePricingResponse(t, w, excelPricingPreviewSchema, stateRevision)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.excelPricing.canonical = canonicalProjectionSequence(canonical.Source{
		ID: "source", Dataset: "dataset", Revision: excelPricingRevisionForTest("source"),
	})
	token := openExcelPricingSession(t, server)
	body := validExcelPricingMutationBody("preview", "excel-preview-0001", stateRevision, "", "")
	request := authenticatedExcelPricingRequest(http.MethodPost, "/api/excel/pricing-sync/preview", body, token)
	request.Header.Set("Idempotency-Key", "excel-preview-0001")
	request.Header.Set("If-Match", `"`+stateRevision+`"`)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("preview status=%d: %s", response.Code, response.Body.String())
	}

	drift := authenticatedExcelPricingRequest(http.MethodPost, "/api/excel/pricing-sync/preview", body, token)
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
	server.excelPricing.canonical = canonicalProjectionSequence(canonical.Source{
		ID: "source", Dataset: "dataset", Revision: excelPricingRevisionForTest("source"),
	})
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
			request := authenticatedExcelPricingRequest(http.MethodPost, "/api/excel/pricing-sync/preview", body, token)
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
		request := authenticatedExcelPricingRequest(http.MethodPost, "/api/excel/pricing-sync/preview", body, token)
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
	request := authenticatedExcelPricingRequest(http.MethodPost, "/api/excel/pricing-sync/apply", body, token)
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
	if !strings.Contains(response.Body.String(), excelPricingApplySchema) ||
		strings.Contains(response.Body.String(), excelPricingTestSecret) {
		t.Fatalf("unsafe or wrong apply response: %s", response.Body.String())
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
	request := authenticatedExcelPricingRequest(http.MethodPost, "/api/excel/pricing-sync/apply", body, token)
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
	request := authenticatedExcelPricingRequest(http.MethodPost, "/api/excel/pricing-sync/apply", body, token)
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
	server.excelPricing.canonical = canonicalProjectionSequence(canonical.Source{
		ID: "source", Dataset: "dataset", Revision: excelPricingRevisionForTest("source"),
	})
	token := openExcelPricingSession(t, server)
	request := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/excel/pricing-sync/state",
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
	server.excelPricing.canonical = canonicalProjectionSequence(canonical.Source{
		ID: "source", Dataset: "dataset", Revision: excelPricingRevisionForTest("source"),
	})
	token := openExcelPricingSession(t, server)
	request := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/excel/pricing-sync/state",
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
	for value, want := range map[string]time.Duration{
		"invalid": 10 * time.Second,
		"1ms":     time.Second,
		"2m":      30 * time.Second,
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
	server.excelPricing.canonical = canonicalProjectionSequence(canonical.Source{
		ID: "source", Dataset: "dataset", Revision: excelPricingRevisionForTest("source"),
	})
	token := openExcelPricingSession(t, server)

	oversized := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/excel/pricing-sync/state",
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
		"/api/excel/pricing-sync/state",
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
	if got != "https://digitalogic.ir/subdir/wp-json/digitalogic/excel/pricing-sync/preview" {
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
	request := newExcelPricingRequest(http.MethodPost, "/api/excel/pricing-sync/session", `{}`)
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
		"idempotency_key":         idempotency,
		"expected_state_revision": stateRevision,
		"settings": map[string]interface{}{
			"dollar_price":           170000,
			"yuan_price":             25300,
			"effective_date":         "2026-07-26",
			"default_profit_percent": "30",
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

func validExcelPricingStateBody(source canonical.Source, page, limit int) string {
	payload := map[string]interface{}{
		"schema":         excelPricingLocalRequestSchema,
		"schema_version": 1,
		"operation":      "state",
		"source":         source,
		"page":           page,
		"limit":          limit,
		"locale":         "fa",
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
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
