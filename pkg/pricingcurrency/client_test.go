package pricingcurrency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubmitFieldIntentAndObserve(t *testing.T) {
	id := "currency:test-123"
	revision := "sha256:" + strings.Repeat("a", 64)
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		key, secret, ok := r.BasicAuth()
		if !ok || key != "test-key" || secret != "test-secret" {
			t.Error("dedicated credentials missing")
		}
		if r.Method == "POST" {
			if r.Header.Get("If-Match") != `"`+revision+`"` || r.Header.Get("Idempotency-Key") != id {
				t.Error("identity headers missing")
			}
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 3 || body["yuan_price"] != "34000" || body["request_id"] != id || body["expected_state_revision"] != revision {
				t.Errorf("intent changed: %v", body)
			}
		} else if r.URL.Path != "/wp-json/digitalogic/v1/currency/requests/"+id {
			t.Errorf("wrong observation path: %s", r.URL.Path)
		}
		fmt.Fprintf(w, `{"success":true,"data":{"job_id":"%s","generation":1,"request_id":%q,"status":"queued","desired_currency":{"yuan_price":"34000"}}}`, strings.Repeat("b", 32), id)
	}))
	defer server.Close()
	client := Client{Origin: server.URL, Key: "test-key", Secret: "test-secret", HTTPClient: server.Client()}
	job, err := client.Submit(context.Background(), id, revision, map[string]string{"yuan_price": "34000"})
	if err != nil || job.RequestID != id {
		t.Fatalf("submit: %v", err)
	}
	if _, err = client.Observe(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("unexpected retries: %d", calls)
	}
}

func TestRedirectAndMalformedResponseNeverRetryOrLeak(t *testing.T) {
	for _, mode := range []string{"redirect", "malformed", "oversized", "wrong_identity"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch mode {
				case "redirect":
					http.Redirect(w, r, "/stolen", http.StatusTemporaryRedirect)
				case "oversized":
					fmt.Fprint(w, strings.Repeat("x", maxResponseBytes+1))
				case "wrong_identity":
					fmt.Fprint(w, `{"success":true,"data":{"job_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","generation":1,"request_id":"different-id","status":"queued"}}`)
				default:
					fmt.Fprint(w, "private-secret endpoint internals")
				}
			}))
			defer server.Close()
			supplied := server.Client()
			followed := false
			supplied.CheckRedirect = func(*http.Request, []*http.Request) error { followed = true; return nil }
			client := Client{Origin: server.URL, Key: "test-key", Secret: "test-secret", HTTPClient: supplied}
			_, err := client.Submit(context.Background(), "currency:test-123", "sha256:"+strings.Repeat("a", 64), map[string]string{"yuan_price": "34000"})
			var safe *Error
			if !errors.As(err, &safe) || !safe.OutcomeUnknown || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe outcome: %v", err)
			}
			if calls != 1 || followed {
				t.Fatalf("write followed or retried: %d", calls)
			}
		})
	}
}

func TestReadAndRejectInvalidIntentBeforeTransmission(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprintf(w, `{"success":true,"data":{"state_revision":"sha256:%s","yuan_price":34000,"dollar_price":"60000","cny_effective_date":"2026-09-09","usd_effective_date":null}}`, strings.Repeat("a", 64))
	}))
	defer server.Close()
	client := Client{Origin: server.URL, Key: "test-key", Secret: "test-secret", HTTPClient: server.Client()}
	state, err := client.Read(context.Background())
	if err != nil || string(state.Currency()["yuan_price"]) != "34000" {
		t.Fatalf("read: %v", err)
	}
	for _, values := range []map[string]string{{"effective_date": "2026-09-09"}, {"yuan_price": "0"}, {"cny_effective_date": "2026-02-30"}, {}} {
		if _, err := client.Submit(context.Background(), "currency:test-123", "sha256:"+strings.Repeat("a", 64), values); err == nil {
			t.Fatal("invalid intent accepted")
		}
	}
	client.Origin = server.URL + "/unexpected"
	if _, err := client.Read(context.Background()); err == nil {
		t.Fatal("path origin accepted")
	}
	if calls != 1 {
		t.Fatalf("invalid request transmitted: %d", calls)
	}
}

func TestOnlyExactOwnerRequestNotFoundIsActionable(t *testing.T) {
	for _, body := range []string{`{"success":false,"code":"digitalogic_currency_async_request_not_found"}`, `{"success":true,"code":"digitalogic_currency_async_request_not_found"}`, `{"success":false,"code":"proxy_not_found"}`, `not found`} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404); fmt.Fprint(w, body) }))
		client := Client{Origin: server.URL, Key: "test-key", Secret: "test-secret", HTTPClient: server.Client()}
		_, err := client.Observe(context.Background(), "currency:test-123")
		var safe *Error
		expected := body == `{"success":false,"code":"digitalogic_currency_async_request_not_found"}`
		if !errors.As(err, &safe) || safe.NotFound != expected || safe.OutcomeUnknown {
			t.Errorf("incorrect absence classification: %v", err)
		}
		server.Close()
	}
}

func TestSubmitSettingsAllSevenFieldsOnly(t *testing.T) {
	values := map[string]string{"yuan_price": "34000", "dollar_price": "60000", "cny_effective_date": "2026-09-09", "usd_effective_date": "2026-09-08", "profit_margin_percent": "30.25", "air_express_price_per_kg": "125.5", "price_rounding_digits": "2"}
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/wp-json/digitalogic/v1/pricing/settings" {
			t.Error("wrong settings operation")
		}
		var body struct {
			Settings  map[string]string `json:"settings"`
			RequestID string            `json:"request_id"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Settings) != 7 {
			t.Error("settings fields lost")
		}
		for key, want := range values {
			if body.Settings[key] != want {
				t.Errorf("field changed: %s", key)
			}
		}
		fmt.Fprintf(w, `{"success":true,"data":{"job_id":"%s","generation":1,"request_id":%q,"status":"queued","desired_fields":{"profit_margin_percent":"30.25"}}}`, strings.Repeat("a", 32), body.RequestID)
	}))
	defer server.Close()
	client := Client{Origin: server.URL, Key: "key", Secret: "secret", HTTPClient: server.Client()}
	job, err := client.SubmitSettings(context.Background(), "settings-request-01", "sha256:"+strings.Repeat("b", 64), values)
	if err != nil || string(job.DesiredFields["profit_margin_percent"]) != `"30.25"` {
		t.Fatalf("settings transport: %v", err)
	}
	for _, invalid := range []map[string]string{{"shipping_catalog_revision": "sha256:" + strings.Repeat("a", 64)}, {"effective_date": "2026-09-09"}, {"profit_margin_percent": "1001"}, {"air_express_price_per_kg": "0"}, {"price_rounding_digits": "10"}} {
		if _, err = client.SubmitSettings(context.Background(), "settings-request-02", "sha256:"+strings.Repeat("b", 64), invalid); err == nil {
			t.Fatal("invalid settings accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid settings transmitted")
	}
}
