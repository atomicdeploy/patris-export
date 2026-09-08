package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/pricingtransport"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

type fakePricingCommands struct {
	catalogs, receives, closes int
	fail                       bool
}

func (f *fakePricingCommands) Catalog(context.Context) (json.RawMessage, error) {
	f.catalogs++
	return json.RawMessage(`{"schema":"digitalogic.integration-catalog","revision":"owner-r1","currency":{"local":"IRT","cny_to_local":30000,"cny_to_irt":30000},"pricing":{"authority":"php","formula_id":"landed_price"},"shipping_methods":[]}`), nil
}
func (f *fakePricingCommands) Receive(_ context.Context, envelope []byte) (json.RawMessage, error) {
	f.receives++
	if f.fail {
		return nil, &pricingtransport.Error{Code: "response_failed", OutcomeUnknown: true}
	}
	var wire struct {
		EventID string `json:"event_id"`
	}
	_ = json.Unmarshal(envelope, &wire)
	return json.RawMessage(fmt.Sprintf(`{"status":"already_current","event_id":%q,"retryable":false,"pending_products":0,"deferred_products":0}`, wire.EventID)), nil
}
func (f *fakePricingCommands) Close() error { f.closes++; return nil }

func commandTestConfig(t *testing.T) appconfig.Config {
	t.Helper()
	t.Setenv("PATRIS_COMMAND_TEST_SECRET", "command-test-secret")
	t.Setenv("PATRIS_COMMAND_TEST_OWNER", "command-test-owner")
	var cfg appconfig.Config
	cfg.Canonical.SourceID = "test-source"
	cfg.Canonical.Pricing = pricingcatalog.Config{Mode: pricingcatalog.ModeDigitalogic, Digitalogic: pricingcatalog.DigitalogicConfig{BaseURL: "https://owner.invalid/wp-json/digitalogic", CommandWebSocketURL: "wss://owner.invalid/wordpress-ws", BearerTokenEnv: "PATRIS_COMMAND_TEST_OWNER"}}
	cfg.SendUpdates = updateout.Config{Enabled: true, URL: "https://owner.invalid/wp-json/digitalogic/product-sync", Format: "json", Method: "POST", ProductSyncSecretEnv: "PATRIS_COMMAND_TEST_SECRET", RetryAttempts: 10}
	return cfg
}

func TestPricingCommandsFreshOwnerAndDeliveryReuseAndRotateCredentials(t *testing.T) {
	cfg := commandTestConfig(t)
	s := &Server{dbPath: "kala.db"}
	var clients []*fakePricingCommands
	s.pricingCommands.factory = func(transport pricingtransport.Config) (pricingCommandConnection, error) {
		if transport.SourceID != "test-source" || transport.SourceDataset != "kala.db" {
			t.Fatal("source identity changed")
		}
		client := &fakePricingCommands{}
		clients = append(clients, client)
		return client, nil
	}
	defer s.closePricingCommands()
	provider := s.pricingCatalogProvider(cfg).(pricingcatalog.OwnerProvider)
	if owner := provider.Owner(context.Background()); owner.Authority != "php" || owner.CatalogStatus != "fresh" {
		t.Fatalf("owner freshness lost: %+v", owner)
	}
	s.invalidateCanonicalProjection(true)
	provider = s.pricingCatalogProvider(cfg).(pricingcatalog.OwnerProvider)
	if owner := provider.Owner(context.Background()); owner.CatalogStatus != "fresh" {
		t.Fatal("fresh owner unavailable")
	}
	contract := &canonical.Envelope{Schema: canonical.ContractName, EventID: "test-event", Source: canonical.Source{ID: "test-source", Dataset: "kala.db"}}
	ctx := s.pricingCommandContext(context.Background(), cfg, cfg.SendUpdates)
	result, err := updateout.DispatchWithResult(ctx, cfg.SendUpdates, updateout.Event{Type: "update", Contract: contract})
	if err != nil || result.Status != "already_current" || result.Attempts != 1 {
		t.Fatalf("receipt not classified: %+v %v", result, err)
	}
	if len(clients) != 1 || clients[0].catalogs != 2 || clients[0].receives != 1 {
		t.Fatal("fresh provider failed to reuse one command connection")
	}
	t.Setenv("PATRIS_COMMAND_TEST_OWNER", "rotated-owner")
	provider = s.pricingCatalogProvider(cfg).(pricingcatalog.OwnerProvider)
	_ = provider.Owner(context.Background())
	if len(clients) != 2 || clients[0].closes != 1 || clients[1].catalogs != 1 {
		t.Fatal("credential rotation did not replace client/provider")
	}
	clients[1].fail = true
	failed, dispatchErr := updateout.DispatchWithResult(ctx, cfg.SendUpdates, updateout.Event{Type: "update", Contract: contract})
	if dispatchErr == nil {
		t.Fatal("ambiguous receive accepted")
	}
	diagnostic := refreshDispatchDetails(failed, dispatchErr, time.Now())
	if diagnostic.Code != "response_failed" || diagnostic.OutcomeUnknown == nil || !*diagnostic.OutcomeUnknown {
		t.Fatalf("typed transport failure lost: %+v", diagnostic)
	}
	if clients[1].receives != 1 {
		t.Fatal("ambiguous receive retried")
	}
}

type commandTestRoundTripper func(*http.Request) (*http.Response, error)

func (f commandTestRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPricingCommandsOnlyInterceptsCatalogAndRejectsCrossOrigin(t *testing.T) {
	cfg := commandTestConfig(t)
	s := &Server{dbPath: "kala.db"}
	fake := &fakePricingCommands{}
	s.pricingCommands.factory = func(pricingtransport.Config) (pricingCommandConnection, error) { return fake, nil }
	defer s.closePricingCommands()
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	httpCalls := 0
	http.DefaultTransport = commandTestRoundTripper(func(r *http.Request) (*http.Response, error) {
		httpCalls++
		if r.URL.Path != "/wp-json/digitalogic/integration/products/by-code/A/pricing" {
			t.Fatal("unexpected HTTP route")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})
	adapter := pricingCommandRoundTripper{s, cfg}
	request, _ := http.NewRequest("GET", "https://owner.invalid/wp-json/digitalogic/integration/products/by-code/A/pricing", nil)
	response, err := adapter.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if httpCalls != 1 || fake.catalogs != 0 {
		t.Fatal("assignment read intercepted")
	}
	cfg.Canonical.Pricing.Digitalogic.CommandWebSocketURL = "wss://other.invalid/wordpress-ws"
	if _, err = s.pricingCommandClient(cfg); !errors.Is(err, errPricingCommandsUnavailable) {
		t.Fatal("cross-origin command endpoint allowed")
	}
}
