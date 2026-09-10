package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMixedOwnerSettingsUsesOneFieldIntentAndNoLegacyOperations(t *testing.T) {
	request := validExcelPricingWritebackRequest("mixed-owner-settings-01", "yuan_price", 29500)
	request.Schema = excelPricingWritebackBatchRequestSchema
	request.SettingKey = ""
	request.PreviousConfirmedValue = ""
	request.SettingKeys = []string{"yuan_price", "profit_margin_percent", "air_express_price_per_kg", "price_rounding_digits"}
	request.PreviousConfirmedValues = map[string]string{"yuan_price": "29000", "profit_margin_percent": "30", "air_express_price_per_kg": "120", "price_rounding_digits": "2"}
	request.Settings.ProfitMarginPercent = json.Number("31.00")
	request.Settings.AirExpressPricePerKG = json.Number("125.0")
	request.Settings.PriceRoundingDigits = json.Number("3")
	confirmed := request.Settings
	confirmed.CNYEffectiveDate = "2026-09-09"
	confirmed.EffectiveDate = confirmed.CNYEffectiveDate
	confirmed.ProfitMarginPercent = json.Number("31")
	confirmed.AirExpressPricePerKG = json.Number("125")
	// Owner changes its derived metadata; the old input revision is not a field intent.
	confirmed.ShippingCatalogRevision = excelPricingRevisionForTest("new-shipping")
	desired := map[string]any{"yuan_price": 29500, "profit_margin_percent": "31", "air_express_price_per_kg": "125", "price_rounding_digits": 3, "cny_effective_date": confirmed.CNYEffectiveDate, "effective_date": confirmed.EffectiveDate}
	var writes atomic.Int32
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/currency/requests/"):
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(map[string]any{"success": false, "code": "digitalogic_currency_async_request_not_found"})
		case r.URL.Path == "/wp-json/digitalogic/v1/pricing/settings" && r.Method == "POST":
			writes.Add(1)
			var body struct {
				Settings  map[string]string `json:"settings"`
				RequestID string            `json:"request_id"`
				Expected  string            `json:"expected_state_revision"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Settings) != 4 || body.RequestID != request.RequestID || body.Expected != request.ExpectedStateRevision || body.Settings["profit_margin_percent"] != "31" {
				t.Errorf("intent changed: %+v", body)
			}
			if r.Header.Get("If-Match") != `"`+request.ExpectedStateRevision+`"` || r.Header.Get("Idempotency-Key") != request.RequestID {
				t.Error("lost identity")
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"job_id": strings.Repeat("d", 32), "generation": 1, "request_id": request.RequestID, "status": "confirmed", "desired_fields": desired, "desired_settings": request.Settings}})
		case r.URL.Path == "/wp-json/digitalogic/v1/currency" && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"state_revision": excelPricingRevisionForTest("mixed-confirmed"), "settings": confirmed}})
		default:
			t.Errorf("legacy/source operation used: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer remote.Close()
	t.Setenv("DIGITALOGIC_PRICING_WRITE_KEY", "test-key")
	t.Setenv("DIGITALOGIC_PRICING_WRITE_SECRET", "test-secret")
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.excelPricing.client = remote.Client()
	q := journalTestQueue(t.TempDir(), time.Now().UTC())
	q.server = server
	admitted, err := q.enqueue(request)
	if err != nil {
		t.Fatal(err)
	}
	selected, _ := q.next()
	result := q.processRemote(context.Background(), selected)
	q.finish(selected, result)
	job := q.jobs[admitted.JobID]
	if writes.Load() != 1 || job.Status != "confirmed" || job.TransactionID != "" || job.ACKDeadline != 0 || job.ConfirmedSettings == nil || job.ConfirmedSettings.ShippingCatalogRevision != confirmed.ShippingCatalogRevision {
		t.Fatalf("shared delivery failed: %+v writes=%d", job, writes.Load())
	}
	entries, err := q.readCurrencyJournal()
	if err != nil || len(entries) != 1 || entries[0].terminal == nil {
		t.Fatalf("shared journal missing: %v", err)
	}
	restored := journalTestQueue(q.currencyJournalDir, time.Now().UTC())
	restored.server = server
	if err = restored.loadCurrencyJournal(); err != nil {
		t.Fatal(err)
	}
	again, err := restored.enqueue(request)
	if err != nil || again.JobID != admitted.JobID || !again.ownerObserveOnly {
		t.Fatalf("mixed request identity lost: %v", err)
	}
}
