package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
)

func TestExcelPricingPreviewIdempotencyAndExactApplyBinding(t *testing.T) {
	stateRevision := excelPricingRevisionForTest("idempotent-preview-state")
	previewDigest := excelPricingRevisionForTest("idempotent-preview")
	source := canonical.Source{
		ID: "source", Dataset: "dataset", Revision: excelPricingRevisionForTest("idempotent-source"),
	}
	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		var request excelPricingRemoteRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Operation != "preview" {
			t.Fatalf("unexpected remote operation %q", request.Operation)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"schema":         excelPricingPreviewSchema,
			"state_revision": stateRevision,
			"preview_digest": previewDigest,
			"status":         "ready",
		})
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	var canonicalCalls atomic.Int32
	server.excelPricing.canonical = func(context.Context) (recordpipe.Result, error) {
		canonicalCalls.Add(1)
		return canonicalProjectionSequence(source)(context.Background())
	}
	token := openExcelPricingSession(t, server)

	previewID := "excel-preview-idempotency-0001"
	previewBody := validExcelPricingMutationBody("preview", previewID, stateRevision, "", "")
	request := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/preview", previewBody, token)
	request.Header.Set("Idempotency-Key", previewID)
	request.Header.Set("If-Match", `"`+stateRevision+`"`)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("preview status=%d: %s", response.Code, response.Body.String())
	}

	replay := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/preview", previewBody, token)
	replay.Header.Set("Idempotency-Key", previewID)
	replay.Header.Set("If-Match", `"`+stateRevision+`"`)
	replayResponse := httptest.NewRecorder()
	server.router.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusOK || replayResponse.Body.String() != response.Body.String() {
		t.Fatalf("preview replay status=%d: %s", replayResponse.Code, replayResponse.Body.String())
	}
	if remoteCalls.Load() != 1 || canonicalCalls.Load() != 1 {
		t.Fatalf("preview replay repeated work: remote=%d canonical=%d", remoteCalls.Load(), canonicalCalls.Load())
	}

	conflictingBody := mutateExcelPricingDollarPriceForTest(t, previewBody, 187892)
	conflict := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/preview", conflictingBody, token,
	)
	conflict.Header.Set("Idempotency-Key", previewID)
	conflict.Header.Set("If-Match", `"`+stateRevision+`"`)
	conflictResponse := httptest.NewRecorder()
	server.router.ServeHTTP(conflictResponse, conflict)
	if conflictResponse.Code != http.StatusConflict ||
		!strings.Contains(conflictResponse.Body.String(), "idempotency_conflict") {
		t.Fatalf("preview conflict status=%d: %s", conflictResponse.Code, conflictResponse.Body.String())
	}

	applyID := "excel-apply-binding-0001"
	applyBody := validExcelPricingMutationBody("apply", applyID, stateRevision, previewDigest, "APPLY")
	applyBody = mutateExcelPricingDollarPriceForTest(t, applyBody, 187892)
	apply := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/apply", applyBody, token)
	apply.Header.Set("Idempotency-Key", applyID)
	apply.Header.Set("If-Match", `"`+stateRevision+`"`)
	applyResponse := httptest.NewRecorder()
	server.router.ServeHTTP(applyResponse, apply)
	if applyResponse.Code != http.StatusConflict ||
		!strings.Contains(applyResponse.Body.String(), "preview_binding_conflict") {
		t.Fatalf("apply binding status=%d: %s", applyResponse.Code, applyResponse.Body.String())
	}

	unknownID := "excel-apply-unknown-preview-0001"
	unknownBody := validExcelPricingMutationBody(
		"apply", unknownID, stateRevision, excelPricingRevisionForTest("unknown-preview"), "APPLY",
	)
	unknown := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/apply", unknownBody, token)
	unknown.Header.Set("Idempotency-Key", unknownID)
	unknown.Header.Set("If-Match", `"`+stateRevision+`"`)
	unknownResponse := httptest.NewRecorder()
	server.router.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusConflict ||
		!strings.Contains(unknownResponse.Body.String(), "preview_required") {
		t.Fatalf("unknown preview status=%d: %s", unknownResponse.Code, unknownResponse.Body.String())
	}
	if remoteCalls.Load() != 1 || canonicalCalls.Load() != 1 {
		t.Fatalf("rejected apply reached work: remote=%d canonical=%d", remoteCalls.Load(), canonicalCalls.Load())
	}
}

func mutateExcelPricingDollarPriceForTest(t *testing.T, body string, value int64) string {
	t.Helper()
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	payload["settings"].(map[string]interface{})["dollar_price"] = value
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
