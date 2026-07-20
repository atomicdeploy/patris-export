package canonical

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

func TestTransformSeparatesPatrisHierarchyFromLeafProducts(t *testing.T) {
	rows := []map[string]interface{}{
		{"Code": "101", "Name": "IC"},
		{"Code": "101001", "Name": "Regulators"},
		{"Code": "101001001", "Name": "LM2576", "ANBAR": []interface{}{5}},
		{"Code": "109", "Name": "Automotive"},
		{"Code": "109001", "Name": "SPC563", "ANBAR": []interface{}{20}},
		{"Code": "998", "Name": "Discount"},
		{"Code": "CUSTOM-SKU", "Name": "Custom leaf"},
		{"Code": "102", "Name": "Sensors"},
		{"Code": "102001", "Name": "DHT11", "ANBAR": []interface{}{3}},
	}
	cfg := DefaultConfig()
	generated := time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC)
	productRows, envelope := Transform(context.Background(), rows, "kala.db", cfg, pricingcatalog.NewProvider(cfg.Pricing), generated)

	productCodes := make([]string, 0, len(productRows))
	for _, row := range productRows {
		productCodes = append(productCodes, row["product_code"].(string))
	}
	wantProducts := []string{"101001001", "102001", "109001", "CUSTOM-SKU"}
	if !reflect.DeepEqual(productCodes, wantProducts) {
		t.Fatalf("product codes = %#v, want %#v", productCodes, wantProducts)
	}
	wantCategoryCodes := []string{"101001", "102", "109", ""}
	for index, row := range productRows {
		got, exists := row["category_code"]
		if wantCategoryCodes[index] == "" && !exists {
			continue
		}
		if got != wantCategoryCodes[index] {
			t.Fatalf("product %s category = %#v, want %q", row["product_code"], got, wantCategoryCodes[index])
		}
	}
	if envelope.SchemaVersion != ContractVersion {
		t.Fatalf("schema version = %q, want %q", envelope.SchemaVersion, ContractVersion)
	}
	if len(envelope.Categories) != 5 {
		t.Fatalf("categories = %d, want 5", len(envelope.Categories))
	}
	byCode := map[string]Category{}
	for _, category := range envelope.Categories {
		byCode[category.CategoryCode] = category
	}
	if got := byCode["101001"]; got.ParentCode != "101" || got.Depth != 2 {
		t.Fatalf("subcategory hierarchy = %+v", got)
	}
	if got := byCode["998"]; got.ParentCode != "" || got.Depth != 1 {
		t.Fatalf("empty root hierarchy = %+v", got)
	}
	if got := byCode["102"].Warnings; len(got) != 0 {
		t.Fatalf("category warnings = %#v, want none", got)
	}
	for _, code := range []string{"101", "101001", "102", "109", "998"} {
		if byCode[code].RecordHash == "" {
			t.Fatalf("category %s has no record hash", code)
		}
	}
}

func TestTransformQuarantinesContradictoryParentsAndExcludesServiceRows(t *testing.T) {
	rows := []map[string]interface{}{
		{"Code": "101", "Name": "Contradictory root", "ANBAR": []interface{}{1}},
		{"Code": "101001", "Name": "Sellable child", "ANBAR": []interface{}{2}},
		{"Code": "103", "Name": "Transistors"},
		{"Code": "103001", "Name": "BJT", "Serial": "103001"},
		{"Code": "103002", "Name": "FET", "Serial": "103002"},
		{"Code": "117", "Name": "Remittance"},
		{"Code": "117001", "Name": "Yuan remittance", "Serial": "117001"},
		{"Code": "999", "Name": "Services"},
		{"Code": "999010", "Name": "Freight"},
		{"Code": "999994", "Name": "-", "Serial": "999994"},
	}
	cfg := DefaultConfig()
	productRows, envelope := Transform(context.Background(), rows, "kala.db", cfg, pricingcatalog.NewProvider(cfg.Pricing), time.Unix(10, 0))
	if got := []string{productRows[0]["product_code"].(string)}; !reflect.DeepEqual(got, []string{"101001"}) {
		t.Fatalf("products = %#v, want only sellable child", got)
	}
	if !reflect.DeepEqual(envelope.QuarantinedCodes, []string{"101"}) {
		t.Fatalf("quarantined = %#v", envelope.QuarantinedCodes)
	}
	if !reflect.DeepEqual(envelope.ExcludedCodes, []string{"117001", "999010", "999994"}) {
		t.Fatalf("excluded = %#v", envelope.ExcludedCodes)
	}
	categoryCodes := make([]string, 0, len(envelope.Categories))
	for _, category := range envelope.Categories {
		categoryCodes = append(categoryCodes, category.CategoryCode)
	}
	if !reflect.DeepEqual(categoryCodes, []string{"103", "103001", "103002", "117", "999"}) {
		t.Fatalf("categories = %#v", categoryCodes)
	}
}

func TestCategoryChangesParticipateInRevisionAndEventIdentity(t *testing.T) {
	cfg := DefaultConfig()
	generated := time.Unix(100, 0).UTC()
	firstRows := []map[string]interface{}{
		{"Code": "101", "Name": "IC"},
		{"Code": "101001", "Name": "Regulators"},
		{"Code": "101001001", "Name": "LM2576"},
	}
	secondRows := []map[string]interface{}{
		{"Code": "101", "Name": "Integrated circuits"},
		{"Code": "101001", "Name": "Regulators"},
		{"Code": "101001001", "Name": "LM2576"},
	}
	_, first := Transform(context.Background(), firstRows, "kala.db", cfg, pricingcatalog.NewProvider(cfg.Pricing), generated)
	_, second := Transform(context.Background(), secondRows, "kala.db", cfg, pricingcatalog.NewProvider(cfg.Pricing), generated)
	if first.Products[0].RecordHash != second.Products[0].RecordHash {
		t.Fatal("category-only edit changed product hash")
	}
	if first.Source.Revision == second.Source.Revision || first.EventID == second.EventID {
		t.Fatalf("category-only edit did not change source/event identity: %s / %s", first.Source.Revision, first.EventID)
	}
	delta := ChangeEnvelope(second, nil)
	if len(delta.Categories) != 2 || delta.Categories[0].RecordHash == "" {
		t.Fatalf("delta lost category snapshot: %+v", delta.Categories)
	}
}
