package recentsales

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseQueryRequiresBoundedDeterministicWindowAndPaging(t *testing.T) {
	cfg := DefaultConfig()
	valid := url.Values{
		"from":      {"2026-07-01T00:00:00Z"},
		"to":        {"2026-07-08T00:00:00Z"},
		"page":      {"2"},
		"page_size": {"25"},
	}
	query, err := ParseQuery(valid, cfg)
	if err != nil {
		t.Fatalf("ParseQuery valid request: %v", err)
	}
	if query.Page != 2 || query.PageSize != 25 || query.From.Location() != time.UTC || query.To.Location() != time.UTC {
		t.Fatalf("unexpected normalized query: %+v", query)
	}

	tests := []struct {
		name   string
		values url.Values
	}{
		{"missing from", url.Values{"to": {"2026-07-08T00:00:00Z"}}},
		{"missing to", url.Values{"from": {"2026-07-01T00:00:00Z"}}},
		{"bad timestamp", url.Values{"from": {"2026-07-01"}, "to": {"2026-07-08T00:00:00Z"}}},
		{"reversed", url.Values{"from": {"2026-07-08T00:00:00Z"}, "to": {"2026-07-01T00:00:00Z"}}},
		{"too wide", url.Values{"from": {"2026-01-01T00:00:00Z"}, "to": {"2026-07-08T00:00:00Z"}}},
		{"page zero", url.Values{"from": {"2026-07-01T00:00:00Z"}, "to": {"2026-07-08T00:00:00Z"}, "page": {"0"}}},
		{"page size too large", url.Values{"from": {"2026-07-01T00:00:00Z"}, "to": {"2026-07-08T00:00:00Z"}, "page_size": {"501"}}},
		{"unknown parameter", url.Values{"from": {"2026-07-01T00:00:00Z"}, "to": {"2026-07-08T00:00:00Z"}, "customer": {"x"}}},
		{"duplicate parameter", url.Values{"from": {"2026-07-01T00:00:00Z", "2026-07-02T00:00:00Z"}, "to": {"2026-07-08T00:00:00Z"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseQuery(test.values, cfg); err == nil {
				t.Fatal("invalid query was accepted")
			}
		})
	}
}

func TestLoadAggregatesDeduplicatesOrdersAndReturnsClosedPrivacyShape(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "sales-events.json")
	rows := []map[string]interface{}{
		{
			"sale_event_id": "event-3", "product_code": "B-20", "quantity": 1.25,
			"sold_at": "2026-07-03T10:00:00+03:30", "customer_name": "must-not-cross",
			"address": "must-not-cross", "invoice_id": "must-not-cross", "payment": "must-not-cross",
			"discount": "must-not-cross", "destination": "must-not-cross",
		},
		{
			"sale_event_id": "event-1", "product_code": "A-10", "quantity": 2,
			"sold_at": "2026-07-02T00:00:00Z", "customer_contact": "must-not-cross",
		},
		{
			"sale_event_id": "event-2", "product_code": "A-10", "quantity": 3.5,
			"sold_at": "2026-07-04T00:00:00Z", "order_id": "must-not-cross",
		},
		// Exact duplicate event rows are counted once.
		{
			"sale_event_id": "event-2", "product_code": "A-10", "quantity": 3.5,
			"sold_at": "2026-07-04T00:00:00Z", "order_id": "different-private-value",
		},
		// This event is outside the requested [from,to) window.
		{
			"sale_event_id": "event-4", "product_code": "A-10", "quantity": 99,
			"sold_at": "2026-06-30T23:59:59Z",
		},
	}
	writeJSONFixture(t, source, rows)

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source = source
	cfg.SourceID = "office-sales"
	query := Query{
		From:     mustTime(t, "2026-07-01T00:00:00Z"),
		To:       mustTime(t, "2026-07-08T00:00:00Z"),
		Page:     1,
		PageSize: 100,
	}
	got, err := Load(context.Background(), cfg, query, filepath.Join(dir, "products.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Schema != SchemaName || got.Version != SchemaVersion || got.Source.ID != "office-sales" ||
		got.Source.Dataset != "sales-events.json" || !strings.HasPrefix(got.Source.Revision, "sha256:") {
		t.Fatalf("unexpected contract metadata: %+v", got)
	}
	want := []Aggregate{
		{ProductCode: "A-10", SoldQuantity: 5.5, SaleFrequency: 2, LastSoldAt: mustTime(t, "2026-07-04T00:00:00Z")},
		{ProductCode: "B-20", SoldQuantity: 1.25, SaleFrequency: 1, LastSoldAt: mustTime(t, "2026-07-03T06:30:00Z")},
	}
	if !reflect.DeepEqual(got.Sales, want) {
		t.Fatalf("aggregates differ:\n got: %#v\nwant: %#v", got.Sales, want)
	}
	if got.Page.Number != 1 || got.Page.Size != 100 || got.Page.TotalItems != 2 || got.Page.TotalPages != 1 {
		t.Fatalf("unexpected page metadata: %+v", got.Page)
	}
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(body)
	for _, forbidden := range []string{
		"must-not-cross", "different-private-value", "customer", "contact", "address",
		"invoice", "payment", "discount", "destination", "order_id", "sale_event_id",
	} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("response leaked forbidden source data %q: %s", forbidden, encoded)
		}
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if gotKeys := sortedKeys(decoded); !reflect.DeepEqual(gotKeys, []string{"page", "sales", "schema", "source", "version", "window"}) {
		t.Fatalf("top-level response shape is not closed: %v", gotKeys)
	}
	sales, ok := decoded["sales"].([]interface{})
	if !ok || len(sales) != 2 {
		t.Fatalf("unexpected sales payload: %#v", decoded["sales"])
	}
	if gotKeys := sortedKeys(sales[0].(map[string]interface{})); !reflect.DeepEqual(gotKeys, []string{"last_sold_at", "product_code", "sale_frequency", "sold_quantity"}) {
		t.Fatalf("aggregate response shape is not closed: %v", gotKeys)
	}

	again, err := Load(context.Background(), cfg, query, filepath.Join(dir, "products.json"))
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if !reflect.DeepEqual(got, again) {
		t.Fatalf("same source and window did not produce deterministic output")
	}
}

func TestLoadPaginatesAfterDeterministicProductOrdering(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "sales.json")
	writeJSONFixture(t, source, []map[string]interface{}{
		{"sale_event_id": "3", "product_code": "C", "quantity": 1, "sold_at": "2026-07-02T00:00:00Z"},
		{"sale_event_id": "1", "product_code": "A", "quantity": 1, "sold_at": "2026-07-02T00:00:00Z"},
		{"sale_event_id": "2", "product_code": "B", "quantity": 1, "sold_at": "2026-07-02T00:00:00Z"},
	})
	cfg := DefaultConfig()
	cfg.Source = source
	got, err := Load(context.Background(), cfg, Query{
		From: mustTime(t, "2026-07-01T00:00:00Z"), To: mustTime(t, "2026-07-03T00:00:00Z"),
		Page: 2, PageSize: 1,
	}, filepath.Join(dir, "products.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sales) != 1 || got.Sales[0].ProductCode != "B" || got.Page.TotalItems != 3 || got.Page.TotalPages != 3 {
		t.Fatalf("unexpected deterministic second page: %+v", got)
	}
}

func TestLoadFailsClosedForConflictingDuplicateAndProductDatabase(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "sales.json")
	writeJSONFixture(t, source, []map[string]interface{}{
		{"sale_event_id": "same", "product_code": "A", "quantity": 1, "sold_at": "2026-07-02T00:00:00Z"},
		{"sale_event_id": "same", "product_code": "B", "quantity": 1, "sold_at": "2026-07-02T00:00:00Z"},
	})
	cfg := DefaultConfig()
	cfg.Source = source
	query := Query{From: mustTime(t, "2026-07-01T00:00:00Z"), To: mustTime(t, "2026-07-03T00:00:00Z"), Page: 1, PageSize: 100}
	if _, err := Load(context.Background(), cfg, query, filepath.Join(dir, "products.db")); err == nil || !strings.Contains(err.Error(), "conflicting duplicate") {
		t.Fatalf("conflicting duplicate did not fail closed: %v", err)
	}

	writeJSONFixture(t, source, []map[string]interface{}{
		{"sale_event_id": "numeric-code", "product_code": 113006048, "quantity": 1, "sold_at": "2026-07-02T00:00:00Z"},
	})
	if _, err := Load(context.Background(), cfg, query, filepath.Join(dir, "products.db")); err == nil || !strings.Contains(err.Error(), "invalid product_code") {
		t.Fatalf("lossy numeric JSON product code was accepted: %v", err)
	}

	cfg.Source = filepath.Join(dir, "kala.db")
	if _, err := Load(context.Background(), cfg, query, filepath.Join(dir, "products.db")); err == nil || !strings.Contains(err.Error(), "kala.db") {
		t.Fatalf("kala.db was accepted as sales source: %v", err)
	}

	cfg.Source = filepath.Join(dir, "products.db")
	if _, err := Load(context.Background(), cfg, query, filepath.Join(dir, "products.db")); err == nil || !strings.Contains(err.Error(), "primary product database") {
		t.Fatalf("primary product database was accepted as sales source: %v", err)
	}
}

func writeJSONFixture(t *testing.T, path string, value interface{}) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.UTC()
}

func sortedKeys(value map[string]interface{}) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
