package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

func TestPostRefreshWaitReadsFreshSourceAndOwnerOncePerSnapshot(t *testing.T) {
	t.Setenv(refreshWaitTestSecretEnv, "fixture-secret")
	var ownerRevision, catalogCalls, batchCalls, singleCalls, deliveryCalls atomic.Int32
	ownerRevision.Store(1)
	owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		revision := ownerRevision.Load()
		rate, markup := "29000", "30"
		if revision > 1 {
			rate, markup = "30000", "40"
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/integration/catalog":
			catalogCalls.Add(1)
			fmt.Fprintf(w, `{"data":{"schema":"digitalogic.integration-catalog","revision":"r%d","currency":{"local":"IRT","cny_to_local":%s,"cny_to_irt":%s},"pricing":{"formula_id":"landed_price","authority":"go"},"shipping_methods":[{"id":"air","price_per_kg":120,"currency":"CNY"}]}}`, revision, rate, rate)
		case "/integration/pricing-assignments/batch":
			batchCalls.Add(1)
			var request struct {
				Codes []string `json:"codes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			results := make([]map[string]interface{}, 0, len(request.Codes))
			for _, code := range request.Codes {
				results = append(results, map[string]interface{}{"code": code, "status": "ok", "assignment": map[string]interface{}{
					"code": code, "shipping_method_id": "air", "profit_percent": markup, "profit_percent_source": "global_default", "pricing_warnings": []string{},
				}})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{
				"schema": "digitalogic.pricing-assignment-batch", "requested_count": len(request.Codes), "resolved_count": len(request.Codes), "error_count": 0, "maximum_codes": 500,
				"default_percentage_markup": map[string]interface{}{
					"schema": "digitalogic.default-percentage-markup", "configured": true, "type": "percentage", "profit_percent": markup, "source": "global_default", "revision": fmt.Sprintf("r%d", revision),
				}, "results": results,
			}})
		default:
			singleCalls.Add(1)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer owner.Close()
	delivered := make(chan canonical.Envelope, 2)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deliveryCalls.Add(1)
		var envelope canonical.Envelope
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		delivered <- envelope
		w.Header().Set("Content-Type", "application/json")
		receipt := &updateout.DeliveryReceipt{Status: "complete", EventID: envelope.EventID, Source: envelope.Source, InputSource: envelope.Source, OwnerCatalogRevision: envelope.Products[0].PricingCatalogRevision}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"status": "accepted", "event_id": envelope.EventID, "retryable": false, "pending_products": 0, "deferred_products": 0, "delivery": receipt}})
	}))
	defer receiver.Close()
	srv, _ := newRefreshWaitTestServer(t, receiver.URL, func(cfg *appconfig.Config) {
		cfg.Canonical.Profiles["source.json"] = canonical.ProfileConfig{Type: canonical.ProfileKala}
		cfg.Canonical.Pricing = pricingcatalog.Config{Mode: pricingcatalog.ModeDigitalogic, Digitalogic: pricingcatalog.DigitalogicConfig{
			BaseURL: owner.URL, FreshFor: "1h", MaxStale: "1h",
		}}
	})
	srv.excelPricing.canonical = nil
	if srv.configWatcher != nil {
		if err := srv.configWatcher.Close(); err != nil {
			t.Fatal(err)
		}
		srv.configWatcher = nil
	}
	writeSource := func(foreign string) {
		t.Helper()
		body := fmt.Sprintf(`{"1":{"Code":"P-1","foreign_price":"%s","weight_grams":1000,"ALLANBAR":1},"2":{"Code":"P-2","foreign_price":"%s","weight_grams":1000,"ALLANBAR":1}}`, foreign, foreign)
		if err := os.WriteFile(srv.currentDBPath(), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeSource("10")
	warm, err := srv.excelPricingCanonical(context.Background(), srv.Config())
	if err != nil {
		t.Fatal(err)
	}
	if len(warm.Products) != 2 || warm.Products[0].FinalPrice == nil {
		t.Fatalf("invalid warm fixture: %+v", warm.Products)
	}
	previous := warm
	for _, change := range []struct{ name, foreign, rate, markup string }{
		{"source", "20", "29000", "30"},
		{"owner", "20", "30000", "40"},
	} {
		t.Run(change.name, func(t *testing.T) {
			writeSource(change.foreign)
			if change.name == "owner" {
				ownerRevision.Store(2)
			}
			cached, err := srv.excelPricingCanonical(context.Background(), srv.Config())
			if err != nil {
				t.Fatal(err)
			}
			if cached.Source.Revision != previous.Source.Revision {
				t.Fatal("fixture did not retain the old cached projection")
			}
			generation := canonicalProjectionCacheGenerationForTest(srv)
			response := httptest.NewRecorder()
			srv.router.ServeHTTP(response, newAuthenticatedRefreshWaitRequest(t, srv, `{"delivery":"wait"}`))
			if response.Code != http.StatusOK {
				t.Fatalf("wait status=%d body=%s", response.Code, response.Body.String())
			}
			var receipt refreshWaitResponse
			if err := json.NewDecoder(response.Body).Decode(&receipt); err != nil {
				t.Fatal(err)
			}
			got := <-delivered
			want, err := canonical.LandedPrice("1000", "120", pricingcatalog.CurrencyCNY, change.foreign, change.markup, change.rate, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, product := range got.Products {
				if product.FinalPrice == nil || *product.FinalPrice != want {
					t.Fatalf("price=%v want=%d product=%+v", product.FinalPrice, want, product)
				}
			}
			if len(got.Products) != 2 || got.Source.Revision == previous.Source.Revision || !receipt.Delivered || receipt.SourceRevision != got.Source.Revision || receipt.Delivery.EventID != got.EventID {
				t.Fatalf("receipt did not pin the fresh snapshot: receipt=%+v envelope=%+v", receipt, got)
			}
			if canonicalProjectionCacheGenerationForTest(srv) != generation+1 {
				t.Fatal("wait must invalidate once per snapshot")
			}
			previous = &got
		})
	}
	if catalogCalls.Load() != 3 || batchCalls.Load() != 3 || singleCalls.Load() != 0 || deliveryCalls.Load() != 2 {
		t.Fatalf("warm + two waits: catalog=%d batch=%d singles=%d deliveries=%d", catalogCalls.Load(), batchCalls.Load(), singleCalls.Load(), deliveryCalls.Load())
	}
}
