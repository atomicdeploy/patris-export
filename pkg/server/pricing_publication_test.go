package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

func TestAuthorityWaitPublishesOnlySelectedFinalProjection(t *testing.T) {
	for _, mode := range []string{"go", "php", "bad-binding", "missing-authority", "replayed-new-owner"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
			fixture.acceptAnyID = true
			defer fixture.Close()
			authority := mode
			if mode == "replayed-new-owner" {
				authority = "go"
			}
			if mode == "bad-binding" {
				authority = "php"
			}
			if mode == "missing-authority" {
				authority = ""
			}
			fx, freight, markup := pricingcatalog.Decimal("29000"), pricingcatalog.Decimal("120"), pricingcatalog.Decimal("30")
			ownerRevision := excelPricingRevisionForTest("owner-settings")
			srv, _ := newRefreshWaitTestServer(t, fixture.server.URL+"/wp-json/digitalogic/product-sync", func(cfg *appconfig.Config) {
				cfg.SendUpdates.ProductSyncSecretEnv = excelPricingRemoteSnapshotTestSecretEnv
				cfg.Canonical.Profiles["source.json"] = canonical.ProfileConfig{Type: canonical.ProfileKala}
				cfg.Canonical.Pricing = pricingcatalog.Config{Mode: pricingcatalog.ModeStatic, Static: pricingcatalog.StaticConfig{
					Authority: authority, Revision: ownerRevision, CNYToIRT: &fx,
					Methods:           []pricingcatalog.Method{{ID: "air", PricePerKg: &freight, Currency: pricingcatalog.CurrencyCNY}},
					DefaultAssignment: &pricingcatalog.Assignment{MethodID: "air", ProfitPercent: &markup},
				}}
			})
			if srv.configWatcher != nil {
				_ = srv.configWatcher.Close()
				srv.configWatcher = nil
			}
			srv.excelPricing.canonical = srv.canonicalRecordResultContext
			srv.excelPricingRemote.terminals = fixture.hub
			body := []byte(`{"1":{"Code":"P-1","foreign_price":"10.123456789012","weight_grams":1000,"ALLANBAR":1}}`)
			if err := os.WriteFile(srv.currentDBPath(), body, 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "missing-authority" {
				srv.excelPricing.dispatch = func(context.Context, updateout.Config, updateout.Event) (updateout.DeliveryResult, error) {
					t.Fatal("missing authority dispatched")
					return updateout.DeliveryResult{}, nil
				}
				response := httptest.NewRecorder()
				srv.router.ServeHTTP(response, newAuthenticatedRefreshWaitRequest(t, srv, `{"delivery":"wait"}`))
				if response.Code != 503 {
					t.Fatalf("missing authority status=%d", response.Code)
				}
				products := httptest.NewRecorder()
				srv.router.ServeHTTP(products, httptest.NewRequest(http.MethodGet, "/api/products", nil))
				if products.Code == http.StatusOK {
					t.Fatal("missing authority published final products")
				}
				return
			}
			input, err := srv.canonicalRecordResultContext(context.Background())
			if err != nil || input.Contract == nil {
				t.Fatalf("input: %v", err)
			}
			if authority == "php" && input.Contract.Products[0].FinalPrice != nil {
				t.Fatal("PHP input published an independent Go final price")
			}
			options := srv.recordOptions()
			options.Canonical.Pricing.Static.Authority = "go"
			options.CatalogProvider = pricingcatalog.NewProvider(options.Canonical.Pricing)
			var keyed map[string]map[string]interface{}
			if err := json.Unmarshal(body, &keyed); err != nil {
				t.Fatal(err)
			}
			owner, err := recordpipe.Build([]map[string]interface{}{keyed["1"]}, srv.currentDBPath(), options)
			if err != nil {
				t.Fatal(err)
			}
			fixture.source = owner.Contract.Source
			fixture.revision.Source = fixture.source
			fixture.revision.InputSource = &input.Contract.Source
			fixture.revision.OwnerCatalogRevision = ownerRevision
			if mode == "bad-binding" {
				fixture.revision.InputSource = nil
			}
			fixture.payload = fixture.basePayload()
			var row map[string]json.RawMessage
			_ = json.Unmarshal(fixture.payload.Catalog.Rows[0], &row)
			row["patris_code"] = json.RawMessage(`"P-1"`)
			productBody, _ := json.Marshal(owner.Contract.Products[0])
			var productFields map[string]json.RawMessage
			_ = json.Unmarshal(productBody, &productFields)
			// PHP stores exact numeric tokens as strings. Preserve decimal digits
			// and normalize only the transport representation before hash checks.
			for _, key := range []string{"final_price", "weight_grams", "foreign_price"} {
				if value := productFields[key]; len(value) > 0 && value[0] != '"' {
					productFields[key], _ = json.Marshal(string(value))
				}
			}
			row["canonical_product"], _ = json.Marshal(productFields)
			fixture.payload.Catalog.Rows[0], _ = json.Marshal(row)
			fixture.finalizePayload()
			fixture.requestID = excelPricingRemoteSnapshotRequestID(input.Contract.EventID + ownerRevision)
			dispatches := 0
			var acceptedEventID string
			srv.excelPricing.dispatch = func(_ context.Context, _ updateout.Config, event updateout.Event) (updateout.DeliveryResult, error) {
				dispatches++
				acceptedEventID = event.Contract.EventID
				if !event.Contract.Source.SameIdentity(input.Contract.Source) {
					t.Error("dispatch changed input source")
				}
				if authority == "php" && event.Contract.Products[0].FinalPrice != nil {
					t.Error("dispatch leaked Go price")
				}
				reply := pricingFixtureDelivery(event.Contract, updateout.DeliveryResult{HTTPStatus: 200, Status: "accepted", EventID: event.Contract.EventID, Attempts: 1})
				if authority == "php" {
					reply.Delivery.Source = fixture.source
					reply.Delivery.OwnerCatalogRevision = ""
				}
				if mode == "replayed-new-owner" {
					reply.Status = "replayed"
					reply.Delivery.EventID = excelPricingRevisionForTest("new-event")
					reply.Delivery.OwnerCatalogRevision = excelPricingRevisionForTest("new-owner")
				}
				return reply, nil
			}
			response := httptest.NewRecorder()
			srv.router.ServeHTTP(response, newAuthenticatedRefreshWaitRequest(t, srv, `{"delivery":"wait"}`))
			if dispatches != 1 {
				t.Fatalf("dispatches=%d", dispatches)
			}
			if mode == "replayed-new-owner" {
				if response.Code != 502 {
					t.Fatalf("new owner receipt falsely completed old wait: %d %s", response.Code, response.Body.String())
				}
				return
			}
			products := httptest.NewRecorder()
			srv.router.ServeHTTP(products, httptest.NewRequest(http.MethodGet, "/api/products", nil))
			if authority == "php" {
				if response.Code != 200 || products.Code != 503 || !strings.Contains(products.Body.String(), "snapshot_disabled") {
					t.Fatalf("website receipt/snapshot disabled: refresh=%d products=%d", response.Code, products.Code)
				}
				var completion refreshWaitResponse
				if err := json.Unmarshal(response.Body.Bytes(), &completion); err != nil {
					t.Fatal(err)
				}
				if !completion.Delivered || completion.SourceRevision != fixture.source.Revision || completion.Delivery == nil || completion.Delivery.EventID != acceptedEventID {
					t.Fatalf("website receipt identity lost: %+v", completion)
				}
				fixture.mu.Lock()
				remoteCalls := fixture.revisionCalls + fixture.startCalls + fixture.bulkCalls + fixture.statusCalls
				fixture.mu.Unlock()
				if remoteCalls != 0 || dispatches != 1 {
					t.Fatalf("optional snapshot ran: calls=%d dispatches=%d", remoteCalls, dispatches)
				}
				return
			}
			if response.Code != 200 || products.Code != 200 {
				t.Fatalf("publication refresh=%d %s products=%d %s", response.Code, response.Body.String(), products.Code, products.Body.String())
			}
			var completion refreshWaitResponse
			_ = json.Unmarshal(response.Body.Bytes(), &completion)
			if completion.SourceRevision != owner.Contract.Source.Revision || completion.Delivery.EventID != acceptedEventID {
				t.Fatalf("completion conflated input ACK and final source: %+v", completion)
			}
			var published map[string]canonical.Product
			if err := json.Unmarshal(products.Body.Bytes(), &published); err != nil {
				t.Fatal(err)
			}
			got := published["P-1"]
			want := owner.Contract.Products[0]
			if got.FinalPrice == nil || *got.FinalPrice != *want.FinalPrice || got.RecordHash != want.RecordHash {
				t.Fatalf("wrong final product: %+v want %+v", got, want)
			}

		})
	}
}

func TestOwnerProductProjectionRejectsForgedRecordHash(t *testing.T) {
	input := canonical.NewEnvelope(nil, "kala.db", "owner", time.Now())
	input.Products = []canonical.Product{{ProductCode: "P-1"}}
	remote := &excelPricingRemoteSnapshotResult{Source: input.Source, Rows: []json.RawMessage{json.RawMessage(`{"patris_code":"P-1","canonical_product":{"product_code":"P-1","record_hash":"sha256:bad"}}`)}}
	if _, err := ownerProductProjection(input, remote, "owner-catalog"); err == nil {
		t.Fatal("forged record hash accepted")
	}
}

func TestPublicationCacheRetainsAcceptedInputAndOwnerBinding(t *testing.T) {
	cache := newCanonicalProjectionCache()
	input := canonical.Source{ID: "owner", Dataset: "kala.db", Revision: excelPricingRevisionForTest("input")}
	contract := canonical.NewEnvelope(nil, "kala.db", "owner", time.Now())
	builds := 0
	build := func(context.Context) (recordpipe.Result, error) {
		builds++
		return recordpipe.Result{Contract: contract, PricingAuthority: "php", PricingInputSource: input, OwnerCatalogRevision: "owner-revision"}, nil
	}
	for range 2 {
		got, err := cache.get(context.Background(), func() time.Duration { return time.Hour }, build)
		if err != nil || got.PricingAuthority != "php" || !got.PricingInputSource.SameIdentity(input) || got.OwnerCatalogRevision != "owner-revision" {
			t.Fatalf("cache lost authority binding: %+v %v", got, err)
		}
	}
	if builds != 1 {
		t.Fatalf("builds=%d", builds)
	}
}
