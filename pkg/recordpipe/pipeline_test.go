package recordpipe

import (
	"testing"

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
