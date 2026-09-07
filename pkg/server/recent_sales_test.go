package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/recentsales"
)

const recentSalesTestToken = "recent-sales-test-token-32-bytes"

func TestRecentSalesEndpointAuthenticatesBeforeReadingAndReturnsClosedAggregate(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "products.json")
	if err := os.WriteFile(primary, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "sales.json")
	rows := []map[string]interface{}{
		{
			"sale_event_id": "sale-2", "product_code": "B", "quantity": 1,
			"sold_at": "2026-07-02T00:00:00Z", "customer_name": "private-customer",
			"invoice_id": "private-invoice", "payment": "private-payment",
		},
		{
			"sale_event_id": "sale-1", "product_code": "A", "quantity": 2.5,
			"sold_at": "2026-07-03T00:00:00Z", "address": "private-address",
			"discount": "private-discount", "destination": "private-destination",
		},
	}
	writeServerJSON(t, source, rows)
	manager := recentSalesConfigManager(t, dir, map[string]interface{}{
		"enabled": true, "source": source, "source_id": "nightly-sales",
	})
	t.Setenv(recentsales.DefaultTokenEnv, recentSalesTestToken)
	server, err := NewServerWithOptions(primary, converter.DefaultCharMapping(), Options{Config: manager}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	endpoint := "/api/recent-sales?from=2026-07-01T00%3A00%3A00Z&to=2026-07-08T00%3A00%3A00Z&page=1&page_size=100"
	for _, test := range []struct {
		name   string
		header string
		target string
		status int
	}{
		{"missing bearer", "", endpoint, http.StatusUnauthorized},
		{"wrong bearer", "Bearer wrong-token-that-is-long-enough", endpoint, http.StatusUnauthorized},
		{"query token forbidden", "", endpoint + "&token=" + recentSalesTestToken, http.StatusUnauthorized},
		{"malformed bearer", "Basic " + recentSalesTestToken, endpoint, http.StatusUnauthorized},
		{"invalid query after auth", "Bearer " + recentSalesTestToken, "/api/recent-sales", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}
			response := httptest.NewRecorder()
			server.Router().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.status, response.Body)
			}
			if strings.Contains(response.Body.String(), source) || strings.Contains(response.Body.String(), recentSalesTestToken) {
				t.Fatalf("error response leaked source path or token: %s", response.Body)
			}
		})
	}

	request := httptest.NewRequest(http.MethodGet, endpoint, nil)
	request.Header.Set("Authorization", "Bearer "+recentSalesTestToken)
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", response.Code, response.Body)
	}
	if got := response.Header().Get("Content-Type"); got != recentsales.MediaType {
		t.Fatalf("content type=%q", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("cache control=%q", got)
	}
	body := response.Body.String()
	for _, forbidden := range []string{
		"private-", "customer", "invoice", "payment", "address", "discount",
		"destination", "sale_event_id", source, recentSalesTestToken,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("success response leaked %q: %s", forbidden, body)
		}
	}
	var payload struct {
		Schema string                  `json:"schema"`
		Source recentsales.Source      `json:"source"`
		Sales  []recentsales.Aggregate `json:"sales"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Schema != recentsales.SchemaName || payload.Source.ID != "nightly-sales" ||
		len(payload.Sales) != 2 || payload.Sales[0].ProductCode != "A" || payload.Sales[1].ProductCode != "B" {
		t.Fatalf("unexpected aggregate response: %+v", payload)
	}
}

func TestRecentSalesEndpointFailsClosedWhenDisabledOrCredentialMissing(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "products.json")
	if err := os.WriteFile(primary, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}

	disabledManager := recentSalesConfigManager(t, filepath.Join(dir, "disabled"), map[string]interface{}{"enabled": false})
	disabled, err := NewServerWithOptions(primary, converter.DefaultCharMapping(), Options{Config: disabledManager}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = disabled.Close() })
	response := serveRecentSalesRequest(disabled, "Bearer "+recentSalesTestToken)
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled status=%d, want 404: %s", response.Code, response.Body)
	}

	t.Setenv(recentsales.DefaultTokenEnv, "")
	missingManager := recentSalesConfigManager(t, filepath.Join(dir, "missing"), map[string]interface{}{
		"enabled": true, "source": filepath.Join(dir, "not-read.json"),
	})
	missing, err := NewServerWithOptions(primary, converter.DefaultCharMapping(), Options{Config: missingManager}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = missing.Close() })
	response = serveRecentSalesRequest(missing, "Bearer "+recentSalesTestToken)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing credential status=%d, want 503: %s", response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), "not-read.json") || strings.Contains(response.Body.String(), recentsales.DefaultTokenEnv) {
		t.Fatalf("missing credential response disclosed configuration: %s", response.Body)
	}
}

func TestBrowserConfigRedactsAndPreservesRecentSalesProfile(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "products.json")
	if err := os.WriteFile(primary, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := recentSalesConfigManager(t, dir, map[string]interface{}{
		"enabled": true, "source": "C:/protected/private-sales-source.json",
		"source_id": "protected-source-id", "token_env": "PROTECTED_RECENT_SALES_TOKEN",
		"product_code_field": "ProtectedCode", "quantity_field": "ProtectedQuantity",
		"sold_at_field": "ProtectedTime", "event_id_field": "ProtectedEvent",
	})
	server, err := NewServerWithOptions(primary, converter.DefaultCharMapping(), Options{Config: manager}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	assertRedacted := func(body string) {
		t.Helper()
		for _, forbidden := range []string{
			"private-sales-source", "protected-source-id", "PROTECTED_RECENT_SALES_TOKEN",
			"ProtectedCode", "ProtectedQuantity", "ProtectedTime", "ProtectedEvent",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("browser config exposed protected recent-sales profile %q: %s", forbidden, body)
			}
		}
	}
	get := httptest.NewRecorder()
	server.Router().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET config status=%d: %s", get.Code, get.Body)
	}
	assertRedacted(get.Body.String())

	clientConfig := browserConfig(manager.Get())
	clientConfig.UI.Theme = "dark"
	body, err := json.Marshal(clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	var crafted map[string]interface{}
	if err := json.Unmarshal(body, &crafted); err != nil {
		t.Fatal(err)
	}
	crafted["recent_sales"] = map[string]interface{}{
		"enabled": true, "source": "C:/attacker/replacement.json",
		"source_id": "attacker", "token_env": "ATTACKER_TOKEN",
	}
	body, err = json.Marshal(crafted)
	if err != nil {
		t.Fatal(err)
	}
	put := httptest.NewRecorder()
	server.Router().ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(string(body))))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT config status=%d: %s", put.Code, put.Body)
	}
	assertRedacted(put.Body.String())
	got := manager.Get()
	if got.UI.Theme != "dark" || got.RecentSales.Source != "C:/protected/private-sales-source.json" ||
		got.RecentSales.SourceID != "protected-source-id" || got.RecentSales.TokenEnv != "PROTECTED_RECENT_SALES_TOKEN" ||
		got.RecentSales.ProductCodeField != "ProtectedCode" || got.RecentSales.QuantityField != "ProtectedQuantity" ||
		got.RecentSales.SoldAtField != "ProtectedTime" || got.RecentSales.EventIDField != "ProtectedEvent" {
		t.Fatalf("browser save changed protected recent-sales profile: %+v", got.RecentSales)
	}
}

func TestBearerTokenAuthorizedUsesStrictReusableHTTPBoundary(t *testing.T) {
	tests := []struct {
		header string
		want   bool
	}{
		{"Bearer " + recentSalesTestToken, true},
		{"bearer " + recentSalesTestToken, true},
		{"Bearer wrong", false},
		{"Basic " + recentSalesTestToken, false},
		{"Bearer " + recentSalesTestToken + " extra", false},
		{"", false},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("Authorization", test.header)
		if got := bearerTokenAuthorized(request, recentSalesTestToken); got != test.want {
			t.Fatalf("header %q authorized=%t, want %t", test.header, got, test.want)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Add("Authorization", "Bearer "+recentSalesTestToken)
	request.Header.Add("Authorization", "Bearer "+recentSalesTestToken)
	if bearerTokenAuthorized(request, recentSalesTestToken) {
		t.Fatal("duplicate Authorization headers were accepted")
	}
}

func recentSalesConfigManager(t *testing.T, dir string, recentSales map[string]interface{}) *appconfig.Manager {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "patris-export.json")
	writeServerJSON(t, path, map[string]interface{}{"recent_sales": recentSales})
	manager, err := appconfig.LoadFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func writeServerJSON(t *testing.T, path string, value interface{}) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func serveRecentSalesRequest(server *Server, authorization string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/recent-sales?from=2026-07-01T00%3A00%3A00Z&to=2026-07-08T00%3A00%3A00Z", nil)
	request.Header.Set("Authorization", authorization)
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	return response
}
