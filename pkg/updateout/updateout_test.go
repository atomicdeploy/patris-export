package updateout

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/recorddiff"
)

func TestEncodeCSVFullPayload(t *testing.T) {
	body, contentType, err := encode(Config{Format: "csv", Mode: "full"}, Event{
		Type:     "initial",
		KeyField: "sku",
		Records: []map[string]interface{}{
			{"sku": "100", "title": "Bolt"},
		},
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("expected CSV content type, got %q", contentType)
	}
	if !strings.Contains(string(body), "sku,title") || !strings.Contains(string(body), "100,Bolt") {
		t.Fatalf("unexpected CSV body: %s", body)
	}
}

func TestNormalizeDefaults(t *testing.T) {
	cfg := Normalize(Config{})
	if cfg.Method != "POST" || cfg.Format != "json" || cfg.Mode != "changes" || cfg.Timeout != "10s" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestEncodeJSONChangesKeepsChangeSetAndDropsFullRecords(t *testing.T) {
	changes := recorddiff.ChangeSet{
		Type:       "update",
		Timestamp:  "2026-07-16T06:30:00+03:30",
		KeyField:   "sku",
		TotalCount: 2,
		Added:      []map[string]interface{}{{"sku": "200", "title": "Nut"}},
	}
	body, contentType, err := encode(Config{Format: "json", Mode: "changes"}, Event{
		Type:     "update",
		Records:  []map[string]interface{}{{"sku": "100", "title": "Bolt"}},
		Changes:  &changes,
		KeyField: "sku",
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("expected JSON content type, got %q", contentType)
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, exists := envelope["records"]; exists {
		t.Fatalf("change mode must omit the full records payload: %s", body)
	}
	encodedChanges, ok := envelope["changes"].(map[string]interface{})
	if !ok || len(encodedChanges["added"].([]interface{})) != 1 {
		t.Fatalf("expected a non-empty changeset: %s", body)
	}
}

func TestEncodeCSVChangesIncludesTypedModifiedRowsAndDeletionTombstones(t *testing.T) {
	changes := recorddiff.ChangeSet{
		Type:       "update",
		KeyField:   "sku",
		TotalCount: 2,
		Added:      []map[string]interface{}{{"sku": "300", "title": "Washer", "price": 3}},
		Modified: []recorddiff.RecordChange{{
			Code:          "100",
			ChangeType:    "modified",
			ChangedFields: []string{"price"},
			NewValues:     map[string]interface{}{"price": 12},
			Record:        map[string]interface{}{"sku": "100", "title": "Bolt", "price": 12},
		}},
		Deleted: []string{"200"},
	}
	body, _, err := encode(Config{Format: "csv", Mode: "changes"}, Event{
		Type:     "update",
		Changes:  &changes,
		KeyField: "sku",
	})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	text := string(body)
	for _, expected := range []string{"_change_type", "_changed_fields", "added", "modified", "deleted", "100", "200", "300", "Bolt", "price"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("CSV changes are missing %q:\n%s", expected, text)
		}
	}
}

func TestDispatchHTTPChangePayloadAndHeaders(t *testing.T) {
	var method string
	var eventHeader string
	var sourceHeader string
	var customHeader string
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		eventHeader = r.Header.Get("X-Patris-Event")
		sourceHeader = r.Header.Get("X-Patris-Source")
		customHeader = r.Header.Get("X-Integration-Test")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	changes := recorddiff.ChangeSet{
		Type:       "update",
		Timestamp:  "2026-07-16T06:30:00+03:30",
		KeyField:   "sku",
		TotalCount: 1,
		Modified: []recorddiff.RecordChange{{
			Code:          "100",
			ChangeType:    "modified",
			ChangedFields: []string{"price"},
			NewValues:     map[string]interface{}{"price": 12},
			Record:        map[string]interface{}{"sku": "100", "title": "Bolt", "price": 12},
		}},
	}
	err := Dispatch(t.Context(), Config{
		Enabled: true,
		URL:     server.URL,
		Method:  http.MethodPut,
		Format:  "json",
		Mode:    "changes",
		Headers: map[string]string{"X-Integration-Test": "yes"},
	}, Event{
		Type:      "update",
		Timestamp: changes.Timestamp,
		Source:    "kala.db",
		Records:   []map[string]interface{}{{"sku": "100", "price": 12}},
		Changes:   &changes,
		KeyField:  "sku",
	})
	if err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	if method != http.MethodPut || eventHeader != "update" || sourceHeader != "kala.db" || customHeader != "yes" {
		t.Fatalf("unexpected request metadata: method=%q event=%q source=%q custom=%q", method, eventHeader, sourceHeader, customHeader)
	}
	if !strings.Contains(string(body), `"modified"`) || !strings.Contains(string(body), `"sku": "100"`) {
		t.Fatalf("HTTP body did not contain the typed changeset: %s", body)
	}
	if strings.Contains(string(body), `"records"`) {
		t.Fatalf("change-mode HTTP body included the full records snapshot: %s", body)
	}
}

func TestDispatchHTTPReportsNonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	err := Dispatch(t.Context(), Config{Enabled: true, URL: server.URL + "?credential=must-not-be-logged"}, Event{Type: "update"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "retryable=true") {
		t.Fatalf("expected non-2xx error, got %v", err)
	}
	if strings.Contains(err.Error(), "must-not-be-logged") {
		t.Fatalf("delivery error leaked endpoint query string: %v", err)
	}
}

func TestDispatchCanonicalContractUsesDirectProductSyncEnvelope(t *testing.T) {
	var body []byte
	headers := http.Header{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		headers = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	zeroStock := 0.0
	salePrice := 125000.0
	product := canonical.Product{
		ProductCode: "113007045", RecordHash: "sha256:record",
		TotalStock: &zeroStock, WeightGrams: pricingcatalog.DecimalFromFloat(240),
		SalePriceSource: &salePrice,
	}
	contract := canonical.NewEnvelope([]canonical.Product{product}, "kala.db", "patris-office", time.Unix(1, 0))
	err := Dispatch(t.Context(), Config{Enabled: true, URL: server.URL, Format: "json", Mode: "changes"}, Event{
		Type: "update", Source: "kala.db", KeyField: "product_code",
		Records:  []map[string]interface{}{{"Code": "113007045", "Sharh1": "must-not-cross"}},
		Contract: contract,
	})
	if err != nil {
		t.Fatalf("canonical dispatch failed: %v", err)
	}
	if headers.Get("X-Patris-Contract") != canonical.ContractName || headers.Get("X-Patris-Event-ID") != contract.EventID {
		t.Fatalf("contract identity headers missing: %v", headers)
	}
	var decoded canonical.Envelope
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("webhook body is not a direct contract: %v\n%s", err, body)
	}
	if decoded.Schema != canonical.ContractName || len(decoded.Products) != 1 || decoded.Products[0].ProductCode != "113007045" {
		t.Fatalf("unexpected webhook contract: %+v", decoded)
	}
	if decoded.Products[0].TotalStock == nil || *decoded.Products[0].TotalStock != 0 ||
		decoded.Products[0].WeightGrams == nil || decoded.Products[0].WeightGrams.String() != "240" ||
		decoded.Products[0].SalePriceSource == nil || *decoded.Products[0].SalePriceSource != salePrice {
		t.Fatalf("canonical transport changed a valid priced zero-stock product: %+v", decoded.Products[0])
	}
	text := string(body)
	for _, forbidden := range []string{"\"records\"", "Sharh1", "must-not-cross", "FOROSH", "KHARYD", "ALLANBAR"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("canonical webhook leaked %q: %s", forbidden, body)
		}
	}
}

func TestDispatchProductSyncRetriesIdenticalEventAndSurfacesRecovery(t *testing.T) {
	const secret = "test-product-sync-secret-must-not-be-persisted"
	t.Setenv("PATRIS_PRODUCT_SYNC_TEST_SECRET", secret)

	product := canonical.Product{ProductCode: "113007045", RecordHash: "sha256:record"}
	contract := canonical.NewEnvelope([]canonical.Product{product}, "kala.db", "patris-office", time.Unix(1, 0))
	var bodies [][]byte
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		if got := r.Header.Get(productSyncSecretHeader); got != secret {
			t.Errorf("product-sync secret header = %q", got)
		}
		if got := r.Header.Get("X-Patris-Contract"); got != canonical.ContractName {
			t.Errorf("contract header = %q", got)
		}
		if got := r.Header.Get("X-Patris-Event-ID"); got != contract.EventID {
			t.Errorf("event identity header = %q", got)
		}
		if got := r.Header.Get("X-Patris-Event"); got != "update" {
			t.Errorf("event type header = %q", got)
		}
		if got := r.Header.Get("X-Patris-Source"); got != "patris-office" {
			t.Errorf("source header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch attempts {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"success":false,"code":"receiver_product_sync_busy","details":{"retryable":true}}`)
		case 2:
			fmt.Fprintf(w, `{"success":true,"data":{"status":"partially_applied","event_id":%q,"retryable":true,"pending_products":1,"deferred_products":2}}`, contract.EventID)
		default:
			fmt.Fprintf(w, `{"success":true,"data":{"status":"recovered","event_id":%q,"retryable":false,"pending_products":0,"deferred_products":2}}`, contract.EventID)
		}
	}))
	defer server.Close()

	cfg := Config{
		Enabled: true, URL: server.URL, Format: "json", Mode: "changes", RequireContract: true,
		RetryAttempts: 3, RetryBackoff: "1ns", ProductSyncSecretEnv: "PATRIS_PRODUCT_SYNC_TEST_SECRET",
		Headers: map[string]string{
			"X-Patris-Contract": "spoofed",
			"X-Patris-Event-ID": "spoofed",
			"X-Patris-Event":    "spoofed",
			"X-Patris-Source":   "spoofed",
		},
	}
	result, err := DispatchWithResult(t.Context(), cfg, Event{Type: "update", Source: `C:\Patris\data4\kala.db`, Contract: contract})
	if err != nil {
		t.Fatalf("product-sync dispatch failed: %v", err)
	}
	if result.Status != "recovered" || result.EventID != contract.EventID || result.Attempts != 3 || result.Retryable || result.PendingProducts != 0 || result.DeferredProducts != 2 {
		t.Fatalf("unexpected recovery result: %+v", result)
	}
	if attempts != 3 || len(bodies) != 3 || !bytes.Equal(bodies[0], bodies[1]) || !bytes.Equal(bodies[1], bodies[2]) {
		t.Fatalf("retries did not preserve identical event bytes: attempts=%d bodies=%d", attempts, len(bodies))
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("secret value entered persisted configuration: %s", encoded)
	}
}

func TestDispatchProductSyncTerminalDeferredProductsDoNotRetry(t *testing.T) {
	t.Setenv("PATRIS_PRODUCT_SYNC_TEST_SECRET", "terminal-deferred")
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"data":{"status":"accepted","event_id":%q,"retryable":false,"pending_products":0,"deferred_products":781,"deferred_reconciliation":{"missing":780,"ambiguous":1,"details":[{"product_code":"MISSING-SENSITIVE","reason":"missing","code":"not_found"},{"product_code":"AMBIGUOUS-SENSITIVE","reason":"ambiguous","code":"collision"}],"details_truncated":779}}}`, contract.EventID)
	}))
	defer server.Close()

	result, err := DispatchWithResult(t.Context(), Config{
		Enabled: true, URL: server.URL, Format: "json", RequireContract: true,
		RetryAttempts: 3, RetryBackoff: "1ns", ProductSyncSecretEnv: "PATRIS_PRODUCT_SYNC_TEST_SECRET",
	}, Event{Type: "initial", Source: "kala.db", Contract: contract})
	if err != nil {
		t.Fatalf("terminal deferred reconciliation was rejected: %v", err)
	}
	if attempts != 1 || result.Attempts != 1 || result.Retryable ||
		result.PendingProducts != 0 || result.DeferredProducts != 781 ||
		result.DeferredMissing != 780 || result.DeferredAmbiguous != 1 {
		t.Fatalf("terminal deferred reconciliation triggered a retry: attempts=%d result=%+v", attempts, result)
	}
	if summary := result.DiagnosticSummary(); !strings.Contains(summary, "deferred_products=781") ||
		!strings.Contains(summary, "deferred_missing=780") ||
		!strings.Contains(summary, "deferred_ambiguous=1") ||
		strings.Contains(summary, "kala.db") || strings.Contains(summary, "SENSITIVE") {
		t.Fatalf("diagnostic summary was incomplete or exposed source data: %s", summary)
	}
}

func TestDispatchProductSyncPartialExhaustionIsSafeToLog(t *testing.T) {
	const secret = "must-never-appear-in-errors"
	t.Setenv("PATRIS_PRODUCT_SYNC_TEST_SECRET", secret)
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"data":{"status":"retry_pending","event_id":%q,"retryable":true,"pending_products":2,"deferred_products":4,"deferred_product_codes":["SENSITIVE-CODE"]}}`, contract.EventID)
	}))
	defer server.Close()

	result, err := DispatchWithResult(t.Context(), Config{
		Enabled: true, URL: server.URL, Format: "json", RequireContract: true,
		RetryAttempts: 2, RetryBackoff: "1ns", ProductSyncSecretEnv: "PATRIS_PRODUCT_SYNC_TEST_SECRET",
	}, Event{Type: "initial", Source: "kala.db", Contract: contract})
	if err == nil {
		t.Fatal("expected retry exhaustion")
	}
	if result.Status != "retry_pending" || result.Attempts != 2 || result.PendingProducts != 2 || result.DeferredProducts != 4 || !result.Retryable {
		t.Fatalf("retry state was not surfaced: %+v", result)
	}
	text := err.Error()
	for _, forbidden := range []string{secret, productSyncSecretHeader, "SENSITIVE-CODE"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("safe delivery error leaked %q: %s", forbidden, text)
		}
	}
	for _, expected := range []string{"status=retry_pending", "pending_products=2", "deferred_products=4", "attempts=2", "retryable=true"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("delivery error did not surface %q: %s", expected, text)
		}
	}
}

func TestDispatchDoesNotForwardProductSyncSecretAcrossRedirect(t *testing.T) {
	const secret = "redirect-sensitive-secret"
	t.Setenv("PATRIS_PRODUCT_SYNC_TEST_SECRET", secret)
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	var redirectedSecret string
	var targetCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls++
		redirectedSecret = r.Header.Get(productSyncSecretHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	err := Dispatch(t.Context(), Config{
		Enabled: true, URL: origin.URL, Format: "json", RequireContract: true,
		ProductSyncSecretEnv: "PATRIS_PRODUCT_SYNC_TEST_SECRET",
	}, Event{Type: "initial", Contract: contract})
	if err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("redirect was not rejected: %v", err)
	}
	if targetCalls != 0 || redirectedSecret != "" {
		t.Fatalf("product-sync request followed redirect: calls=%d secret=%q", targetCalls, redirectedSecret)
	}
}

func TestDispatchRequiresReceiverEventIdentityInStrictProductSyncResponse(t *testing.T) {
	t.Setenv("PATRIS_PRODUCT_SYNC_TEST_SECRET", "strict-response")
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"status":"accepted","retryable":false,"pending_products":0,"deferred_products":0}}`)
	}))
	defer server.Close()

	err := Dispatch(t.Context(), Config{
		Enabled: true, URL: server.URL, Format: "json", RequireContract: true,
		ProductSyncSecretEnv: "PATRIS_PRODUCT_SYNC_TEST_SECRET",
	}, Event{Type: "initial", Contract: contract})
	if err == nil || !strings.Contains(err.Error(), "inconsistent product-sync delivery state") {
		t.Fatalf("missing receiver event identity was accepted: %v", err)
	}
}

func TestClassifyProductSyncReceiverStateMachine(t *testing.T) {
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	tests := []struct {
		name      string
		status    string
		retryable bool
		pending   int
		deferred  int
		wantError bool
	}{
		{name: "accepted", status: "accepted", deferred: 4},
		{name: "already current", status: "already_current", deferred: 3},
		{name: "replayed", status: "replayed", deferred: 2},
		{name: "recovered", status: "recovered", deferred: 1},
		{name: "partial", status: "partially_applied", retryable: true, pending: 1, deferred: 4},
		{name: "retry pending", status: "retry_pending", retryable: true, pending: 2, deferred: 3},
		{name: "unknown", status: "queued", wantError: true},
		{name: "terminal retryable", status: "accepted", retryable: true, wantError: true},
		{name: "terminal pending", status: "recovered", pending: 1, wantError: true},
		{name: "partial not retryable", status: "partially_applied", pending: 1, wantError: true},
		{name: "partial without pending", status: "retry_pending", retryable: true, wantError: true},
		{name: "punctuated status", status: "accep!ted", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Appendf(nil, `{"success":true,"data":{"status":%q,"event_id":%q,"retryable":%t,"pending_products":%d,"deferred_products":%d}}`, test.status, contract.EventID, test.retryable, test.pending, test.deferred)
			result, err := classifyHTTPResponse(DeliveryResult{HTTPStatus: http.StatusOK, Attempts: 1}, body, contract, true)
			if test.wantError {
				if !errors.Is(err, errReceiverStateInvalid) || result.Retryable {
					t.Fatalf("invalid receiver state was not rejected safely: result=%+v err=%v", result, err)
				}
				return
			}
			if err != nil || result.Status != test.status || result.Retryable != test.retryable || result.PendingProducts != test.pending || result.DeferredProducts != test.deferred {
				t.Fatalf("valid receiver state changed: result=%+v err=%v", result, err)
			}
		})
	}
}

func TestClassifyProductSyncReceiverDoesNotNormalizeEventIdentity(t *testing.T) {
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	alteredEventID := strings.Replace(contract.EventID, ":", ": ", 1)
	body := fmt.Appendf(nil, `{"success":true,"data":{"status":"accepted","event_id":%q,"retryable":false,"pending_products":0,"deferred_products":0}}`, alteredEventID)
	result, err := classifyHTTPResponse(DeliveryResult{HTTPStatus: http.StatusOK, Attempts: 1}, body, contract, true)
	if !errors.Is(err, errReceiverIdentityMismatch) || result.EventID != alteredEventID || result.Retryable {
		t.Fatalf("altered receiver event identity was normalized or accepted: result=%+v err=%v", result, err)
	}
}

func TestClassifyProductSyncReceiverRequiresPresentNonNullStateFields(t *testing.T) {
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	valid := fmt.Sprintf(`{"success":true,"data":{"status":"accepted","event_id":%q,"retryable":false,"pending_products":0,"deferred_products":0}}`, contract.EventID)
	tests := []struct {
		name string
		body string
	}{
		{name: "missing status", body: strings.Replace(valid, `"status":"accepted",`, "", 1)},
		{name: "null status", body: strings.Replace(valid, `"status":"accepted"`, `"status":null`, 1)},
		{name: "missing event id", body: strings.Replace(valid, fmt.Sprintf(`"event_id":%q,`, contract.EventID), "", 1)},
		{name: "null event id", body: strings.Replace(valid, fmt.Sprintf(`"event_id":%q`, contract.EventID), `"event_id":null`, 1)},
		{name: "missing retryable", body: strings.Replace(valid, `"retryable":false,`, "", 1)},
		{name: "null retryable", body: strings.Replace(valid, `"retryable":false`, `"retryable":null`, 1)},
		{name: "missing pending products", body: strings.Replace(valid, `"pending_products":0,`, "", 1)},
		{name: "null pending products", body: strings.Replace(valid, `"pending_products":0`, `"pending_products":null`, 1)},
		{name: "missing deferred products", body: strings.Replace(valid, `,"deferred_products":0`, "", 1)},
		{name: "null deferred products", body: strings.Replace(valid, `"deferred_products":0`, `"deferred_products":null`, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := classifyHTTPResponse(DeliveryResult{HTTPStatus: http.StatusOK, Attempts: 1}, []byte(test.body), contract, true)
			if !errors.Is(err, errReceiverStateInvalid) || result.Retryable {
				t.Fatalf("missing or null receiver state was not rejected: result=%+v err=%v body=%s", result, err, test.body)
			}
		})
	}
}

func TestClassifyProductSyncReceiverAcceptsMaximumBoundedProductCounts(t *testing.T) {
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	body := fmt.Appendf(nil, `{"success":true,"data":{"status":"accepted","event_id":%q,"retryable":false,"pending_products":0,"deferred_products":%d}}`, contract.EventID, maxReceiverReportedProducts)
	result, err := classifyHTTPResponse(DeliveryResult{HTTPStatus: http.StatusOK, Attempts: 1}, body, contract, true)
	if err != nil || uint64(result.DeferredProducts) != maxReceiverReportedProducts {
		t.Fatalf("maximum bounded deferred count was not accepted: result=%+v err=%v", result, err)
	}
}

func TestClassifyProductSyncReceiverRejectsInvalidDeferredProductCounts(t *testing.T) {
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	tests := []struct {
		name  string
		value string
	}{
		{name: "negative", value: "-1"},
		{name: "above bound", value: "2147483648"},
		{name: "uint64 overflow", value: "18446744073709551616"},
		{name: "fractional", value: "1.5"},
		{name: "quoted", value: `"1"`},
		{name: "null", value: "null"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Appendf(nil, `{"success":true,"data":{"status":"accepted","event_id":%q,"retryable":false,"pending_products":0,"deferred_products":%s}}`, contract.EventID, test.value)
			result, err := classifyHTTPResponse(DeliveryResult{HTTPStatus: http.StatusOK, Attempts: 1}, body, contract, true)
			if !errors.Is(err, errReceiverStateInvalid) || result.Retryable || result.DeferredProducts != 0 {
				t.Fatalf("invalid deferred count was not rejected fail-closed: result=%+v err=%v", result, err)
			}
		})
	}
}

func TestClassifyProductSyncReceiverRejectsInconsistentDeferredReconciliation(t *testing.T) {
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	detailObjects := strings.TrimSuffix(strings.Repeat(`{},`, maxReceiverDeferredDetails+1), ",")
	tests := []struct {
		name    string
		total   int
		summary string
	}{
		{name: "reason totals", total: 2, summary: `{"missing":1,"ambiguous":0,"details":[],"details_truncated":2}`},
		{name: "detail totals", total: 2, summary: `{"missing":2,"ambiguous":0,"details":[{}],"details_truncated":0}`},
		{name: "negative count", total: 1, summary: `{"missing":-1,"ambiguous":2,"details":[],"details_truncated":1}`},
		{name: "overflowing reason sum", total: 1, summary: `{"missing":2147483647,"ambiguous":1,"details":[],"details_truncated":1}`},
		{name: "missing typed field", total: 0, summary: `{"missing":0,"ambiguous":0,"details":[]}`},
		{name: "null summary", total: 0, summary: `null`},
		{name: "null details", total: 0, summary: `{"missing":0,"ambiguous":0,"details":null,"details_truncated":0}`},
		{name: "non-object detail", total: 1, summary: `{"missing":1,"ambiguous":0,"details":["CODE-MUST-NOT-BE-RETAINED"],"details_truncated":0}`},
		{name: "too many details", total: maxReceiverDeferredDetails + 1, summary: fmt.Sprintf(`{"missing":%d,"ambiguous":0,"details":[%s],"details_truncated":0}`, maxReceiverDeferredDetails+1, detailObjects)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Appendf(nil, `{"success":true,"data":{"status":"accepted","event_id":%q,"retryable":false,"pending_products":0,"deferred_products":%d,"deferred_reconciliation":%s}}`, contract.EventID, test.total, test.summary)
			result, err := classifyHTTPResponse(DeliveryResult{HTTPStatus: http.StatusOK, Attempts: 1}, body, contract, true)
			if !errors.Is(err, errReceiverStateInvalid) || result.Retryable || result.DeferredProducts != 0 {
				t.Fatalf("inconsistent deferred summary was not rejected fail-closed: result=%+v err=%v", result, err)
			}
		})
	}
}

func TestDispatchGenericWebhookIgnoresProductSyncReceiverFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"status":"accepted","retryable":false,"pending_products":0,"deferred_products":-1}}`)
	}))
	defer server.Close()

	result, err := DispatchWithResult(t.Context(), Config{Enabled: true, URL: server.URL, Format: "json"}, Event{Type: "update"})
	if err != nil || result.Status != "" || result.DeferredProducts != 0 {
		t.Fatalf("generic webhook semantics changed: result=%+v err=%v", result, err)
	}
}

func TestDispatchRejectsPersistedProductSyncSecretHeader(t *testing.T) {
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	for _, header := range []string{
		"x-patris-product-sync-secret",
		"x-ReTiReD-PrOdUcT-SyNc-SeCrEt",
		"Product-Sync-Secret",
	} {
		t.Run(header, func(t *testing.T) {
			err := Dispatch(t.Context(), Config{
				Enabled: true, URL: "https://example.invalid", Format: "json",
				Headers: map[string]string{header: "plaintext"},
			}, Event{Type: "initial", Contract: contract})
			if err == nil || !strings.Contains(err.Error(), "product_sync_secret_env") {
				t.Fatalf("persisted receiver secret header %q was not rejected: %v", header, err)
			}
		})
	}
}

func TestDispatchRequiresConfiguredProductSyncSecretEnvironmentValue(t *testing.T) {
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	err := Dispatch(t.Context(), Config{
		Enabled: true, URL: "https://example.invalid", Format: "json",
		ProductSyncSecretEnv: "PATRIS_PRODUCT_SYNC_MISSING_SECRET",
	}, Event{Type: "initial", Contract: contract})
	if err == nil || !strings.Contains(err.Error(), "missing or empty") {
		t.Fatalf("missing receiver secret did not fail closed: %v", err)
	}
}

func TestResolveProductSyncSecretUsesOnlyNamedEnvironmentValue(t *testing.T) {
	t.Setenv("PATRIS_PRODUCT_SYNC_RESOLVE_TEST", "protected-companion-secret")
	secret, err := ResolveProductSyncSecret(Config{
		ProductSyncSecretEnv: "PATRIS_PRODUCT_SYNC_RESOLVE_TEST",
	})
	if err != nil {
		t.Fatal(err)
	}
	if secret != "protected-companion-secret" {
		t.Fatalf("resolved secret = %q", secret)
	}
	for _, invalid := range []string{"", "NOT-PORTABLE", "1STARTS_WITH_DIGIT"} {
		if _, err := ResolveProductSyncSecret(Config{ProductSyncSecretEnv: invalid}); err == nil {
			t.Errorf("invalid environment name %q was accepted", invalid)
		}
	}
}

func TestDispatchRejectsProductSyncDestinationQueryAuthentication(t *testing.T) {
	t.Setenv("PATRIS_PRODUCT_SYNC_TEST_SECRET", "header-only")
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	err := Dispatch(t.Context(), Config{
		Enabled: true, URL: "https://example.invalid/product-sync?token=legacy", Format: "json",
		ProductSyncSecretEnv: "PATRIS_PRODUCT_SYNC_TEST_SECRET",
	}, Event{Type: "initial", Contract: contract})
	if err == nil || !strings.Contains(err.Error(), "header-only") {
		t.Fatalf("query authentication was not rejected: %v", err)
	}
}

func TestDispatchRejectsRemotePlainHTTPProductSyncDestination(t *testing.T) {
	t.Setenv("PATRIS_PRODUCT_SYNC_TEST_SECRET", "https-only")
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	err := Dispatch(t.Context(), Config{
		Enabled: true, URL: "http://receiver.example/wp-json/receiver/patris/product-sync", Format: "json",
		ProductSyncSecretEnv: "PATRIS_PRODUCT_SYNC_TEST_SECRET",
	}, Event{Type: "initial", Contract: contract})
	if err == nil || !strings.Contains(err.Error(), "requires HTTPS") {
		t.Fatalf("remote plaintext destination was not rejected: %v", err)
	}
}

func TestCanonicalFullModeSelectsSnapshotInsteadOfDelta(t *testing.T) {
	generated := time.Unix(1, 0)
	snapshot := canonical.NewEnvelope([]canonical.Product{
		{ProductCode: "A", RecordHash: "sha256:a"},
		{ProductCode: "B", RecordHash: "sha256:b"},
	}, "kala.db", "office", generated)
	delta := canonical.NewEnvelope([]canonical.Product{
		{ProductCode: "B", RecordHash: "sha256:b"},
	}, "kala.db", "office", generated)
	delta.EventType = "update"
	body, _, err := encode(Config{Format: "json", Mode: "full"}, Event{
		Type: "update", Contract: delta, SnapshotContract: snapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded canonical.Envelope
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Products) != 2 || decoded.EventID != snapshot.EventID {
		t.Fatalf("full mode did not select the snapshot: %+v", decoded)
	}
}

func TestCanonicalChangeModeCarriesDeletedCodeTombstone(t *testing.T) {
	snapshot := canonical.NewEnvelope([]canonical.Product{
		{ProductCode: "B", RecordHash: "sha256:b"},
	}, "kala.db", "office", time.Unix(1, 0))
	changes := recorddiff.ChangeSet{Type: "update", KeyField: "product_code", Deleted: []string{"A"}}
	delta := canonical.ChangeEnvelope(snapshot, &changes)
	body, _, err := encode(Config{Format: "json", Mode: "changes"}, Event{
		Type: "update", Contract: delta, SnapshotContract: snapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded canonical.Envelope
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.EventType != "update" || len(decoded.DeletedCodes) != 1 || decoded.DeletedCodes[0].ProductCode != "A" || !decoded.DeletedCodes[0].Deleted {
		t.Fatalf("canonical webhook tombstone missing: %+v", decoded)
	}
}

func TestDispatchRejectsRawOutboundUnlessExplicitlyAllowed(t *testing.T) {
	cfg := Config{Enabled: true, Command: []string{"must-not-run"}}
	err := Dispatch(t.Context(), cfg, Event{Type: "update", Raw: true, Records: []map[string]interface{}{{"Sharh1": "secret"}}})
	if err == nil || !strings.Contains(err.Error(), "raw outbound updates are disabled") {
		t.Fatalf("raw outbound event was not rejected: %v", err)
	}
}

func TestDispatchRequireContractRejectsGenericNonRawPayload(t *testing.T) {
	cfg := Config{Enabled: true, RequireContract: true, Command: []string{"must-not-run"}}
	err := Dispatch(t.Context(), cfg, Event{Type: "update", Records: []map[string]interface{}{{"Sharh1": "must-not-cross"}}})
	if err == nil || !strings.Contains(err.Error(), "requires a canonical contract") {
		t.Fatalf("generic payload bypassed require_contract: %v", err)
	}

	cfg.Format = "csv"
	err = Dispatch(t.Context(), cfg, Event{Type: "update", Contract: &canonical.Envelope{Schema: canonical.ContractName}})
	if err == nil || !strings.Contains(err.Error(), "requires JSON") {
		t.Fatalf("CSV bypassed require_contract: %v", err)
	}
}

func TestDispatchCommandReceivesChangePayloadAndMetadata(t *testing.T) {
	t.Setenv("GO_WANT_UPDATEOUT_HELPER", "1")
	output := filepath.Join(t.TempDir(), "payload.json")
	t.Setenv("UPDATEOUT_HELPER_FILE", output)

	changes := recorddiff.ChangeSet{
		Type:       "update",
		Timestamp:  "2026-07-16T06:30:00+03:30",
		KeyField:   "sku",
		TotalCount: 1,
		Added:      []map[string]interface{}{{"sku": "100", "title": "Bolt"}},
	}
	err := Dispatch(t.Context(), Config{
		Enabled: true,
		Command: []string{os.Args[0], "-test.run=TestUpdateoutHelperProcess", "--"},
		Format:  "json",
		Mode:    "changes",
	}, Event{
		Type:      "update",
		Timestamp: changes.Timestamp,
		Source:    "kala.db",
		Changes:   &changes,
		KeyField:  "sku",
	})
	if err != nil {
		t.Fatalf("command dispatch failed: %v", err)
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read helper payload: %v", err)
	}
	if !strings.Contains(string(body), `"added"`) || !strings.Contains(string(body), `"100"`) {
		t.Fatalf("command received an incomplete payload: %s", body)
	}
}

func TestCommandSinkDoesNotInheritHTTPProductSyncSecret(t *testing.T) {
	const secretEnv = "PATRIS_PRODUCT_SYNC_TEST_SECRET"
	t.Setenv("GO_WANT_UPDATEOUT_HELPER", "1")
	t.Setenv(secretEnv, "must-not-reach-command")
	t.Setenv("UPDATEOUT_FORBIDDEN_ENV_NAME", secretEnv)
	output := filepath.Join(t.TempDir(), "payload.json")
	t.Setenv("UPDATEOUT_HELPER_FILE", output)
	contract := canonical.NewEnvelope(nil, "kala.db", "patris-office", time.Unix(1, 0))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(productSyncSecretHeader); got != "must-not-reach-command" {
			t.Errorf("HTTP sink did not receive its dedicated secret")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"data":{"status":"accepted","event_id":%q,"retryable":false,"pending_products":0,"deferred_products":0}}`, contract.EventID)
	}))
	defer server.Close()

	result, err := DispatchWithResult(t.Context(), Config{
		Enabled: true, URL: server.URL, Format: "json", RequireContract: true,
		ProductSyncSecretEnv: secretEnv,
		Command:              []string{os.Args[0], "-test.run=TestUpdateoutHelperProcess", "--"},
	}, Event{Type: "update", Source: "kala.db", KeyField: "sku", Contract: contract})
	if err != nil || result.Status != "accepted" {
		t.Fatalf("combined HTTP/command dispatch failed: result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("command helper did not receive the contract body: %v", err)
	}
}

func TestUpdateoutHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_UPDATEOUT_HELPER") != "1" {
		return
	}
	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	if os.Getenv("PATRIS_EXPORT_EVENT_TYPE") != "update" || os.Getenv("PATRIS_EXPORT_EVENT_KEY_FIELD") != "sku" {
		os.Exit(3)
	}
	if forbidden := os.Getenv("UPDATEOUT_FORBIDDEN_ENV_NAME"); forbidden != "" && os.Getenv(forbidden) != "" {
		os.Exit(5)
	}
	if err := os.WriteFile(os.Getenv("UPDATEOUT_HELPER_FILE"), payload, 0o600); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}
