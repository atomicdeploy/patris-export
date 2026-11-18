package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

func TestExcelPricingForwardAcceptsVersionlessProviderSchemas(t *testing.T) {
	for _, operation := range []string{"state", "preview", "apply"} {
		t.Run(operation, func(t *testing.T) {
			source := excelPricingStateSourceForTest()
			revision := excelPricingRevisionForTest("provider-settings")
			local := excelPricingLocalRequest{
				Operation: operation, Source: &source,
				IdempotencyKey: "provider-schema-test-0001", ExpectedStateRevision: revision,
			}
			providerSchema := strings.TrimSuffix(excelPricingExpectedSchema(operation), "/v1")
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/wp-json/digitalogic/pricing/sync/"+operation ||
					r.Header.Get(updateout.ProductSyncSecretHeader) != excelPricingTestSecret {
					t.Error("provider request lost its operation or protected credential")
				}
				if operation != "state" && (r.Header.Get("If-Match") != `"`+revision+`"` ||
					r.Header.Get("Idempotency-Key") != local.IdempotencyKey) {
					t.Error("provider request lost its revision or idempotency fence")
				}
				var request struct {
					Source canonical.Source `json:"source"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Source != source {
					t.Errorf("provider source changed: %+v, error=%v", request.Source, err)
				}
				writeRemotePricingResponse(t, w, providerSchema, revision)
			}))
			defer remote.Close()
			server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
			response, err := server.forwardExcelPricing(context.Background(), server.Config().SendUpdates,
				operation, buildExcelPricingRemoteRequest(operation, local, source), local)
			if err != nil || response.status != http.StatusOK || response.schema != providerSchema ||
				response.stateRevision != revision {
				t.Fatalf("coherent provider response rejected: status=%d schema=%q error=%v", response.status, response.schema, err)
			}
		})
	}
}

func TestExcelPricingProviderSchemaStillRejectsWrongOperationAndRevision(t *testing.T) {
	for name, body := range map[string]string{
		"wrong operation":  `{"schema":"digitalogic.pricing-sync-apply","state_revision":"` + excelPricingRevisionForTest("settings") + `"}`,
		"unknown version":  `{"schema":"digitalogic.pricing-sync-preview/v2","state_revision":"` + excelPricingRevisionForTest("settings") + `"}`,
		"missing revision": `{"schema":"digitalogic.pricing-sync-preview"}`,
		"invalid revision": `{"schema":"digitalogic.pricing-sync-preview","state_revision":"untrusted"}`,
	} {
		t.Run(name, func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer remote.Close()
			server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
			_, err := server.forwardExcelPricing(context.Background(), server.Config().SendUpdates,
				"preview", excelPricingRemoteRequest{}, excelPricingLocalRequest{})
			if err == nil {
				t.Fatal("unsafe provider response was accepted")
			}
		})
	}
	if excelPricingResponseSchemaMatches("unknown", "") {
		t.Fatal("an empty schema must not match an unknown operation")
	}
}

func TestExcelPricingWritebackReadbackAcceptsVersionlessState(t *testing.T) {
	settings := validExcelPricingWritebackRequest("provider-readback-0001", "yuan_price", 29500).Settings
	body, err := json.Marshal(map[string]interface{}{
		"schema":         "digitalogic.pricing-sync-state",
		"state_revision": excelPricingRevisionForTest("readback"), "settings": settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	document, err := excelPricingStateDocumentFromBody(body, excelPricingStateSchema)
	if err != nil || !reflect.DeepEqual(document.Settings, settings) {
		t.Fatalf("versionless settings readback rejected: %v", err)
	}
}

func TestExcelPricingACKAcceptsOnlyKnownSchemaAndExactTransaction(t *testing.T) {
	const transaction = "ptx_provider_schema_test"
	for _, test := range []struct {
		name, schema, transaction, status string
		accepted                          bool
	}{
		{"versionless", "digitalogic.pricing-confirmation", transaction, "acknowledged", true},
		{"loopback schema", excelPricingConfirmationSchema, transaction, "replayed", true},
		{"wrong transaction", "digitalogic.pricing-confirmation", "ptx_another", "acknowledged", false},
		{"pending", "digitalogic.pricing-confirmation", transaction, "awaiting_ack", false},
		{"unknown version", "digitalogic.pricing-confirmation/v2", transaction, "acknowledged", false},
		{"wrong operation", "digitalogic.pricing-sync-apply", transaction, "acknowledged", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			job := &excelPricingWritebackJob{
				JobID: "provider-ack-job-0001", TransactionID: transaction,
				StateRevision:      excelPricingRevisionForTest("committed"),
				SettingsDigest:     excelPricingRevisionForTest("settings"),
				confirmedSettings:  validExcelPricingWritebackRequest("provider-ack-0001", "yuan_price", 29500).Settings,
				confirmationSource: excelPricingStateSourceForTest(),
			}
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if r.Header.Get(updateout.ProductSyncSecretHeader) != excelPricingTestSecret ||
					payload["consumer_id"] != excelPricingContractClientID ||
					payload["channel"] != excelPricingContractChannel ||
					payload["transaction_id"] != transaction ||
					payload["committed_state_revision"] != job.StateRevision ||
					payload["confirmed_settings_digest"] != job.SettingsDigest {
					t.Error("ACK lost its authentication, consumer, transaction, revision or settings binding")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(excelPricingACKResponse{
					Schema: test.schema, Status: test.status, TransactionID: test.transaction,
				})
			}))
			defer remote.Close()
			server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
			_, err := server.forwardExcelPricingACK(context.Background(), server.Config().SendUpdates, job)
			if (err == nil) != test.accepted {
				t.Fatalf("ACK acceptance=%v, want %v (error=%v)", err == nil, test.accepted, err)
			}
		})
	}
}
