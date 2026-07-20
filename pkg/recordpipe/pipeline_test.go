package recordpipe

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/datasource"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/recorddiff"
	"github.com/atomicdeploy/patris-export/pkg/recordmap"
)

func TestBuildRawSkipsTransformAndMapping(t *testing.T) {
	rows := []map[string]interface{}{{"Code": "100", "Name": "Raw", "ANBAR1": 2}}
	result := Build(rows, "kala.db", Options{
		Raw: true,
		Mapping: recordmap.Config{
			Enabled: true,
			Fields:  map[string]string{"Name": "title"},
		},
	})
	if !result.Raw {
		t.Fatal("expected raw result")
	}
	if got := result.Rows[0]["Name"]; got != "Raw" {
		t.Fatalf("expected original Name field, got %#v", got)
	}
	if _, exists := result.Rows[0]["title"]; exists {
		t.Fatal("raw mode should not apply mapping")
	}
	if _, exists := result.Rows[0]["ANBAR1"]; !exists {
		t.Fatal("raw mode should keep numbered ANBAR fields")
	}
}

func TestBuildKalaProfileAgainstRealLegacyDatabaseFixture(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "kala.db")
	ds, err := datasource.NewDataSource(path, converter.DefaultCharMapping(), false)
	if err != nil {
		t.Fatalf("open real kala fixture: %v", err)
	}
	defer ds.Close()
	rawRows, err := ds.GetRawRecords()
	if err != nil {
		t.Fatalf("read real kala fixture: %v", err)
	}

	manager, err := appconfig.Load(filepath.Join("..", "..", "testdata", "canonical-static-config.json"))
	if err != nil {
		t.Fatalf("load canonical fixture config: %v", err)
	}
	cfg := manager.Get()
	result := Build(rawRows, path, Options{
		Canonical:       cfg.Canonical,
		CatalogProvider: pricingcatalog.NewProvider(cfg.Canonical.Pricing),
		GeneratedAt:     time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
	})
	if result.Contract == nil || result.KeyField != "product_code" || len(result.Rows) != 292 || len(result.Contract.Categories) != 54 {
		t.Fatalf("real fixture did not use canonical pipeline: contract=%v key=%q rows=%d", result.Contract != nil, result.KeyField, len(result.Rows))
	}
	var product map[string]interface{}
	for _, row := range result.Rows {
		if row["product_code"] == "102001011" {
			product = row
			break
		}
	}
	if product == nil {
		t.Fatal("known real fixture Code 102001011 was not transformed")
	}
	if fmt.Sprint(product["foreign_price"]) != "2.75" || fmt.Sprint(product["weight_grams"]) != "1.84" || product["total_stock"] != 20.0 || product["final_price"] != int64(111999) {
		t.Fatalf("real legacy fields were not parsed correctly: %#v", product)
	}
	if warnings, exists := product["warnings"].([]string); !exists || len(warnings) != 0 {
		t.Fatalf("known real fixture did not retain the required empty warnings array: %v", product["warnings"])
	}
	for _, raw := range []string{"Sharh1", "Sharh2", "FOROSH", "KHARYD", "ALLANBAR", "ANBAR"} {
		if _, exists := product[raw]; exists {
			t.Fatalf("raw field %s crossed canonical boundary", raw)
		}
	}
}

func TestBuildCanonicalBoundaryQuarantinesDuplicateCodesBeforeKeying(t *testing.T) {
	cfg := canonical.DefaultConfig()
	result := Build([]map[string]interface{}{
		{"Code": "DUP", "Sharh1": "0 0 0 1", "Sharh2": "1 گرم"},
		{"Code": "DUP", "Sharh1": "0 0 0 2", "Sharh2": "2 گرم"},
	}, "kala.db", Options{
		Canonical:       cfg,
		CatalogProvider: pricingcatalog.NewProvider(pricingcatalog.Config{Mode: pricingcatalog.ModeNone}),
	})
	if result.Contract == nil || len(result.Rows) != 0 {
		t.Fatalf("duplicate rows crossed canonical boundary: %#v", result.Rows)
	}
	if len(result.Contract.QuarantinedCodes) != 1 || result.Contract.QuarantinedCodes[0] != "DUP" {
		t.Fatalf("duplicate Code was silently collapsed: %+v", result.Contract)
	}
}

func TestQuarantinedCodeIsFilteredFromDeletionAndTombstone(t *testing.T) {
	result := Result{Contract: &canonical.Envelope{
		Schema:           canonical.ContractName,
		QuarantinedCodes: []string{"A"},
	}}
	changes := recorddiff.ChangeSet{KeyField: "product_code", Deleted: []string{"A", "B"}}
	safe := result.FilterChanges(changes)
	if !reflect.DeepEqual(safe.Deleted, []string{"B"}) {
		t.Fatalf("quarantined deletion was not filtered: %v", safe.Deleted)
	}
	envelope := result.SyncEnvelope(&changes)
	if len(envelope.DeletedCodes) != 1 || envelope.DeletedCodes[0].ProductCode != "B" {
		t.Fatalf("quarantined Code became a tombstone: %+v", envelope.DeletedCodes)
	}
}

func TestBuildAppliesTableSpecificMapping(t *testing.T) {
	round := 0
	result := Build([]map[string]interface{}{{"Code": "100", "Name": "Bolt", "FOROSH": 12.5}}, "kala.db", Options{
		Mapping: recordmap.Config{
			Enabled: true,
			Tables: map[string]recordmap.TableConfig{
				"kala.db": {
					KeyField: "sku",
					Fields:   map[string]string{"Code": "sku", "Name": "title", "FOROSH": "price"},
					Numeric:  map[string]recordmap.NumericRule{"FOROSH": {Multiplier: 2, Round: &round}},
				},
			},
		},
	})
	if result.Raw {
		t.Fatal("expected transformed result")
	}
	if result.KeyField != "sku" {
		t.Fatalf("expected sku key field, got %q", result.KeyField)
	}
	if got := result.Rows[0]["title"]; got != "Bolt" {
		t.Fatalf("expected mapped title, got %#v", got)
	}
	if got := result.Rows[0]["price"]; got != int64(25) {
		t.Fatalf("expected numeric transform result 25, got %#v (%T)", got, got)
	}
	payload, ok := result.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("expected keyed payload, got %T", result.Payload)
	}
	if _, ok := payload["100"]; !ok {
		t.Fatalf("expected payload keyed by sku, got %#v", payload)
	}
}
