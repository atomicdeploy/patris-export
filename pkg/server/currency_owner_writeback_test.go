package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOwnerCurrencyWritebackQueue(t *testing.T) {
	for _, mode := range []string{"confirmed_without_ack", "uncertain_no_retry", "existing_owner_request", "uncertain_recovery"} {
		t.Run(mode, func(t *testing.T) {
			uncertain := strings.HasPrefix(mode, "uncertain_")
			replay := mode == "existing_owner_request"
			t.Setenv("DIGITALOGIC_PRICING_WRITE_KEY", "ck_"+strings.Repeat("a", 40))
			t.Setenv("DIGITALOGIC_PRICING_WRITE_SECRET", "cs_"+strings.Repeat("b", 40))
			request := validExcelPricingWritebackRequest("owner-queue-proof-01", "yuan_price", 29500)
			settings := request.Settings
			settings.CNYEffectiveDate = "2026-09-09"
			settings.EffectiveDate = "2026-09-09"
			owner := map[string]any{"success": true, "data": map[string]any{"job_id": strings.Repeat("d", 32), "generation": 1, "request_id": request.RequestID, "status": "confirmed", "desired_currency": map[string]any{"yuan_price": 29500, "cny_effective_date": "2026-09-09"}}}
			var writes atomic.Int32
			var recovered atomic.Bool
			recoveryRequested := false
			remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(r.URL.Path, "/currency/requests/"):
					if replay || recovered.Load() {
						json.NewEncoder(w).Encode(owner)
						return
					}
					w.WriteHeader(404)
					json.NewEncoder(w).Encode(map[string]any{"success": false, "code": "digitalogic_currency_async_request_not_found"})
				case r.URL.Path == "/wp-json/digitalogic/v1/currency" && r.Method == "POST":
					writes.Add(1)
					var body map[string]string
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("invalid body")
					}
					if len(body) != 3 || body["yuan_price"] != "29500" || body["request_id"] != request.RequestID || body["expected_state_revision"] != request.ExpectedStateRevision {
						t.Errorf("intent changed: %v", body)
					}
					if uncertain {
						w.WriteHeader(502)
						w.Write([]byte(`upstream failure`))
						return
					}
					json.NewEncoder(w).Encode(owner)
				case r.URL.Path == "/wp-json/digitalogic/v1/currency" && r.Method == "GET":
					json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"state_revision": excelPricingRevisionForTest("owner-confirmed"), "settings": settings}})
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer remote.Close()
			server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
			server.excelPricing.client = remote.Client()
			accepted, err := server.excelPricingWrites.enqueue(request)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				job := server.excelPricingWrites.get(accepted.JobID)
				if job.Status == "confirmed" || job.Status == "observation_required" {
					if mode == "uncertain_recovery" && !recoveryRequested && job.Status == "observation_required" {
						recovered.Store(true)
						recoveryRequested = true
						if _, err := server.excelPricingWrites.observeCurrency(job.JobID); err != nil {
							t.Fatal(err)
						}
						continue
					}
					wantWrites := int32(1)
					if replay {
						wantWrites = 0
					}
					if writes.Load() != wantWrites {
						t.Fatalf("writes=%d", writes.Load())
					}
					if uncertain && !recoveryRequested {
						if job.Status != "observation_required" || job.RetryCount != 0 {
							t.Fatalf("unexpected uncertain state %#v", job)
						}
					} else if job.Status != "confirmed" || job.TransactionID != "" || job.ACKDeadline != 0 || job.ConfirmedSettings == nil || job.ConfirmedSettings.CNYEffectiveDate != "2026-09-09" {
						t.Fatalf("unexpected confirmed state %#v", job)
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("queue did not finish: %#v", server.excelPricingWrites.get(accepted.JobID))
		})
	}
}
