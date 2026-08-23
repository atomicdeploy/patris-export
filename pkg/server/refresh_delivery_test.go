package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

const refreshWaitTestSecretEnv = "PATRIS_REFRESH_WAIT_TEST_SECRET"

func TestPostRefreshWaitDeliversCanonicalSnapshotWhenInitialDeliveryIsDisabled(t *testing.T) {
	const secret = "refresh-wait-test-secret"
	t.Setenv(refreshWaitTestSecretEnv, secret)

	var calls atomic.Int32
	var received canonical.Envelope
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get(updateout.ProductSyncSecretHeader); got != secret {
			t.Errorf("product-sync secret header = %q", got)
		}
		if got := r.Header.Get("X-Patris-Event"); got != "update" {
			t.Errorf("explicit delivery event type = %q, want update", got)
		}
		if got := r.Header.Get("X-Patris-Contract"); got != canonical.ContractName {
			t.Errorf("contract header = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode delivered contract: %v", err)
			http.Error(w, "receiver-sensitive-body", http.StatusBadRequest)
			return
		}
		eventID := r.Header.Get("X-Patris-Event-ID")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"data":{"status":"accepted","event_id":%q,"retryable":false,"pending_products":0,"deferred_products":2,"deferred_reconciliation":{"missing":2,"ambiguous":0,"details":[{},{}],"details_truncated":0}}}`, eventID)
	}))
	defer receiver.Close()

	srv, contract := newRefreshWaitTestServer(t, receiver.URL)
	request := newAuthenticatedRefreshWaitRequest(t, srv, `{"delivery":"wait"}`)
	recorder := httptest.NewRecorder()
	srv.router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("wait refresh status = %d: %s", recorder.Code, recorder.Body.String())
	}
	assertJSONKeys(t, recorder.Body.Bytes(),
		[]string{"refreshed", "delivered", "source_revision", "delivery"},
		map[string][]string{"delivery": {
			"status", "event_id", "attempts", "pending_products",
			"deferred_products", "deferred_missing", "deferred_ambiguous",
		}},
	)
	var response refreshWaitResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode wait refresh response: %v", err)
	}
	if !response.Refreshed || !response.Delivered ||
		response.SourceRevision != contract.Source.Revision ||
		response.Code != "" || response.Delivery == nil {
		t.Fatalf("unexpected wait refresh response: %+v", response)
	}
	if response.Delivery.Status != "accepted" ||
		response.Delivery.EventID != contract.EventID ||
		response.Delivery.Attempts != 1 ||
		response.Delivery.PendingProducts != 0 ||
		response.Delivery.DeferredProducts != 2 ||
		response.Delivery.DeferredMissing != 2 ||
		response.Delivery.DeferredAmbiguous != 0 {
		t.Fatalf("unexpected terminal delivery state: %+v", response.Delivery)
	}
	if calls.Load() != 1 {
		t.Fatalf("receiver calls = %d, want 1 despite initial=false", calls.Load())
	}
	if received.Schema != canonical.ContractName ||
		received.EventID != contract.EventID ||
		received.Source != contract.Source {
		t.Fatalf("delivered canonical identity = %+v", received)
	}
}

func TestPostRefreshEmptyBodyPreservesLegacyResponse(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "source.json")
	if err := os.WriteFile(sourcePath, []byte(`{"1":{"Code":"1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(sourcePath, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	recorder := httptest.NewRecorder()
	srv.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/refresh", nil))
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"refreshed":true}` {
		t.Fatalf("legacy refresh response changed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestPostRefreshWaitFailsClosedWithBoundedNonSecretResponse(t *testing.T) {
	const secret = "refresh-wait-secret-must-not-leak"
	t.Setenv(refreshWaitTestSecretEnv, secret)

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"status":"accepted","event_id":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","retryable":false,"pending_products":0,"deferred_products":0},"receiver_sensitive":"must-not-leak"}`)
	}))
	defer receiver.Close()

	srv, contract := newRefreshWaitTestServer(t, receiver.URL)
	recorder := httptest.NewRecorder()
	srv.router.ServeHTTP(recorder, newAuthenticatedRefreshWaitRequest(t, srv, `{"delivery":"wait"}`))

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("invalid terminal state status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response refreshWaitResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode failed wait refresh response: %v", err)
	}
	if !response.Refreshed || response.Delivered ||
		response.SourceRevision != contract.Source.Revision ||
		response.Code != "delivery_failed" || response.Delivery != nil {
		t.Fatalf("unexpected failed wait refresh response: %+v", response)
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{secret, receiver.URL, "receiver_sensitive", "must-not-leak"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("wait refresh error leaked %q: %s", forbidden, body)
		}
	}
	if len(body) > 512 {
		t.Fatalf("wait refresh error response is not bounded: %d bytes", len(body))
	}
}

func TestPostRefreshWaitUnavailableDeliveryIsSanitized(t *testing.T) {
	t.Setenv(refreshWaitTestSecretEnv, "")
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	srv, _ := newRefreshWaitTestServer(t, receiver.URL)
	recorder := httptest.NewRecorder()
	srv.router.ServeHTTP(recorder, newAuthenticatedRefreshWaitRequest(t, srv, `{"delivery":"wait"}`))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable delivery status = %d: %s", recorder.Code, recorder.Body.String())
	}
	assertJSONKeys(t, recorder.Body.Bytes(), []string{"refreshed", "delivered", "code"}, nil)
	var response refreshWaitResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode unavailable wait refresh response: %v", err)
	}
	if response.Refreshed || response.Delivered || response.Code != "delivery_unavailable" ||
		response.SourceRevision != "" || response.Delivery != nil {
		t.Fatalf("unexpected unavailable wait refresh response: %+v", response)
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{refreshWaitTestSecretEnv, receiver.URL, updateout.ProductSyncSecretHeader} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("unavailable wait refresh leaked %q: %s", forbidden, body)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("unavailable delivery reached receiver %d time(s)", calls.Load())
	}
}

func TestPostRefreshWaitRequiresLoopbackCaller(t *testing.T) {
	const secret = "refresh-wait-test-secret"
	t.Setenv(refreshWaitTestSecretEnv, secret)

	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	srv, _ := newRefreshWaitTestServer(t, receiver.URL)
	request := newAuthenticatedRefreshWaitRequest(t, srv, `{"delivery":"wait"}`)
	request.RemoteAddr = "192.0.2.10:43123"
	recorder := httptest.NewRecorder()
	srv.router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("remote wait refresh status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response refreshWaitResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode forbidden wait refresh response: %v", err)
	}
	if response.Refreshed || response.Delivered || response.Code != "local_session_required" {
		t.Fatalf("unexpected forbidden wait refresh response: %+v", response)
	}
	if calls.Load() != 0 {
		t.Fatalf("remote wait refresh reached receiver %d time(s)", calls.Load())
	}
}

func TestPostRefreshWaitBodyWithoutCompanionHeadersPreservesLegacyResponse(t *testing.T) {
	const secret = "refresh-wait-test-secret"
	t.Setenv(refreshWaitTestSecretEnv, secret)

	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	srv, _ := newRefreshWaitTestServer(t, receiver.URL)
	recorder := httptest.NewRecorder()
	srv.router.ServeHTTP(recorder, newLocalRefreshWaitRequest(`{"delivery":"wait"}`))

	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"refreshed":true}` {
		t.Fatalf("legacy body response changed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("unauthenticated legacy refresh reached receiver %d time(s)", calls.Load())
	}
}

func TestPostRefreshWaitRequiresAuthorizedSession(t *testing.T) {
	const secret = "refresh-wait-test-secret"
	t.Setenv(refreshWaitTestSecretEnv, secret)

	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	srv, _ := newRefreshWaitTestServer(t, receiver.URL)
	tests := map[string]func() *http.Request{
		"missing session": func() *http.Request {
			request := newLocalRefreshWaitRequest(`{"delivery":"wait"}`)
			request.Header.Set(excelPricingClientHeader, excelPricingClientID)
			return request
		},
		"invalid session": func() *http.Request {
			request := newLocalRefreshWaitRequest(`{"delivery":"wait"}`)
			request.Header.Set(excelPricingClientHeader, excelPricingClientID)
			request.Header.Set(excelPricingCSRFHeader, strings.Repeat("x", 43))
			return request
		},
		"duplicate client header": func() *http.Request {
			request := newAuthenticatedRefreshWaitRequest(t, srv, `{"delivery":"wait"}`)
			request.Header.Add(excelPricingClientHeader, excelPricingClientID)
			return request
		},
		"duplicate session header": func() *http.Request {
			request := newAuthenticatedRefreshWaitRequest(t, srv, `{"delivery":"wait"}`)
			request.Header.Add(excelPricingCSRFHeader, strings.Repeat("x", 43))
			return request
		},
	}
	for name, requestForTest := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			srv.router.ServeHTTP(recorder, requestForTest())
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("unauthorized wait status = %d: %s", recorder.Code, recorder.Body.String())
			}
			var response refreshWaitResponse
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatal(err)
			}
			if response.Refreshed || response.Delivered || response.Code != "local_session_required" {
				t.Fatalf("unexpected unauthorized response: %+v", response)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("unauthorized wait refresh reached receiver %d time(s)", calls.Load())
	}
}

func TestPostRefreshWaitRequiresSingleJSONContentType(t *testing.T) {
	const secret = "refresh-wait-test-secret"
	t.Setenv(refreshWaitTestSecretEnv, secret)

	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	srv, _ := newRefreshWaitTestServer(t, receiver.URL)
	tests := map[string]func(*http.Request){
		"missing": func(request *http.Request) {
			request.Header.Del("Content-Type")
		},
		"not JSON": func(request *http.Request) {
			request.Header.Set("Content-Type", "text/plain")
		},
		"duplicate": func(request *http.Request) {
			request.Header.Add("Content-Type", "application/json")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := newAuthenticatedRefreshWaitRequest(t, srv, `{"delivery":"wait"}`)
			mutate(request)
			recorder := httptest.NewRecorder()
			srv.router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("content-type status = %d: %s", recorder.Code, recorder.Body.String())
			}
			var response refreshWaitResponse
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatal(err)
			}
			if response.Refreshed || response.Delivered || response.Code != "json_required" {
				t.Fatalf("unexpected content-type response: %+v", response)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid content type reached receiver %d time(s)", calls.Load())
	}
}

func TestPostRefreshWaitReturnsBusyWithoutQueueing(t *testing.T) {
	const secret = "refresh-wait-test-secret"
	t.Setenv(refreshWaitTestSecretEnv, secret)

	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	srv, _ := newRefreshWaitTestServer(t, receiver.URL)
	srv.excelPricing.permit <- struct{}{}
	defer func() { <-srv.excelPricing.permit }()

	request := newAuthenticatedRefreshWaitRequest(t, srv, `{"delivery":"wait"}`)
	recorder := httptest.NewRecorder()
	srv.router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "1" {
		t.Fatalf("busy wait status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code         string `json:"code"`
		RetryAfterMS int    `json:"retry_after_ms"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Code != "pricing_busy" || response.RetryAfterMS != 1000 {
		t.Fatalf("unexpected busy wait response: %+v", response)
	}
	if calls.Load() != 0 {
		t.Fatalf("busy wait refresh reached receiver %d time(s)", calls.Load())
	}
}

func newRefreshWaitTestServer(t *testing.T, receiverURL string) (*Server, *canonical.Envelope) {
	t.Helper()
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "source.json")
	if err := os.WriteFile(sourcePath, []byte(`{"1":{"Code":"1"}}`), 0600); err != nil {
		t.Fatalf("write refresh source: %v", err)
	}
	manager, err := appconfig.Load(filepath.Join(tempDir, "config.json"))
	if err != nil {
		t.Fatalf("load refresh config: %v", err)
	}
	if err := manager.Update(func(cfg *appconfig.Config) {
		cfg.SendUpdates = updateout.Config{
			Enabled:              true,
			URL:                  receiverURL,
			Method:               http.MethodPost,
			Format:               "json",
			Mode:                 "changes",
			Initial:              false,
			RequireContract:      true,
			Timeout:              "1s",
			RetryAttempts:        1,
			ProductSyncSecretEnv: refreshWaitTestSecretEnv,
		}
	}); err != nil {
		t.Fatalf("configure refresh delivery: %v", err)
	}
	srv, err := NewServerWithOptions(sourcePath, nil, Options{Config: manager}, false)
	if err != nil {
		t.Fatalf("create refresh server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	contract := canonical.NewEnvelope(nil, "kala.db", "refresh-wait-test", time.Unix(1, 0))
	srv.excelPricing.canonical = func(context.Context) (recordpipe.Result, error) {
		return recordpipe.Result{Contract: contract}, nil
	}
	return srv, contract
}

func newLocalRefreshWaitRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080/api/refresh", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:43123"
	request.Header.Set("Content-Type", "application/json")
	return request
}

func newAuthenticatedRefreshWaitRequest(t *testing.T, srv *Server, body string) *http.Request {
	t.Helper()
	request := newLocalRefreshWaitRequest(body)
	request.Header.Set(excelPricingClientHeader, excelPricingClientID)
	request.Header.Set(excelPricingCSRFHeader, openExcelPricingSession(t, srv))
	return request
}

func TestPostRefreshWaitRejectsInvalidOrUnsupportedBody(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "source.json")
	if err := os.WriteFile(sourcePath, []byte(`{"1":{"Code":"1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(sourcePath, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	for name, body := range map[string]string{
		"unknown mode":   `{"delivery":"later"}`,
		"malformed JSON": `{"delivery":`,
		"unknown field":  `{"delivery":"wait","extra":true}`,
		"oversized body": `{"delivery":"wait","padding":"` + strings.Repeat("x", refreshWaitMaxRequestBytes) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := newAuthenticatedRefreshWaitRequest(t, srv, body)
			recorder := httptest.NewRecorder()
			srv.router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				responseBody, _ := io.ReadAll(recorder.Body)
				t.Fatalf("invalid delivery status = %d: %s", recorder.Code, responseBody)
			}
			assertJSONKeys(t, recorder.Body.Bytes(), []string{"refreshed", "delivered", "code"}, nil)
			var response refreshWaitResponse
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatal(err)
			}
			if response.Refreshed || response.Delivered || response.Code != "invalid_request" {
				t.Fatalf("unexpected invalid-delivery response: %+v", response)
			}
		})
	}
}

func assertJSONKeys(t *testing.T, data []byte, topLevel []string, nested map[string][]string) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("decode response keys: %v", err)
	}
	if len(object) != len(topLevel) {
		t.Fatalf("top-level response fields = %v, want %v", object, topLevel)
	}
	for _, key := range topLevel {
		if _, exists := object[key]; !exists {
			t.Fatalf("response is missing top-level field %q: %s", key, data)
		}
	}
	for key, expected := range nested {
		var child map[string]json.RawMessage
		if err := json.Unmarshal(object[key], &child); err != nil {
			t.Fatalf("decode response field %q: %v", key, err)
		}
		if len(child) != len(expected) {
			t.Fatalf("response field %q keys = %v, want %v", key, child, expected)
		}
		for _, childKey := range expected {
			if _, exists := child[childKey]; !exists {
				t.Fatalf("response field %q is missing %q: %s", key, childKey, data)
			}
		}
	}
}
