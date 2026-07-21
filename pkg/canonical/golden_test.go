package canonical

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

func TestSyntheticProductSyncGoldenFixture(t *testing.T) {
	encoded, err := json.MarshalIndent(syntheticGoldenEnvelope(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	actual := append(encoded, '\n')
	path := filepath.Join("..", "..", "testdata", "patris-product-sync.synthetic.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, actual, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Git may materialize tracked text fixtures with CRLF on Windows. JSON is
	// insensitive to the line-ending representation, so compare the canonical
	// LF form emitted by json.MarshalIndent instead of making this test depend
	// on the checkout's core.autocrlf setting.
	expected = bytes.ReplaceAll(expected, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(actual, expected) {
		t.Fatalf("synthetic product-sync golden fixture drifted; regenerate %s from syntheticGoldenEnvelope", path)
	}
}

func syntheticGoldenEnvelope() *Envelope {
	fx := pricingcatalog.Decimal("29000")
	freight := pricingcatalog.Decimal("120")
	markup := pricingcatalog.Decimal("30")
	enabled := true
	cfg := DefaultConfig()
	cfg.SourceID = "synthetic-fixture"
	cfg.Pricing = pricingcatalog.Config{Mode: pricingcatalog.ModeStatic, Static: pricingcatalog.StaticConfig{
		Revision:              "synthetic-catalog",
		CNYToIRT:              &fx,
		CurrencyEffectiveDate: "2026-01-01",
		SelectedWarehouses:    []string{"1", "2"},
		Methods: []pricingcatalog.Method{{
			ID: "synthetic-air", Name: "Synthetic Air", Enabled: &enabled, PricePerKg: &freight, Currency: pricingcatalog.CurrencyCNY,
		}},
		Assignments: map[string]pricingcatalog.Assignment{
			"101001001": {MethodID: "synthetic-air", ProfitPercent: &markup},
		},
	}}
	rows := []map[string]interface{}{
		{"Code": "101", "Name": "Synthetic components"},
		{"Code": "101001", "Name": "Synthetic modules"},
		{
			"Code": "101001001", "Name": "Synthetic priced product", "Serial": "SYNTH-PN-001", "Vahed": "unit",
			"foreign_price": "24.5", "weight_grams": "240", "location": "TEST-A", "FOROSH": 100000, "KHARYD": 90000,
			"ANBAR": []interface{}{3, 2}, "ALLANBAR": 5, "Sefaresh": 1, "source_updated_at": "2026-01-01T00:00:00Z",
		},
		{
			"Code": "101001002", "Name": "Synthetic incomplete product", "Serial": "SYNTH-PN-002", "Vahed": "unit",
			"Sharh1": "price unavailable", "Sharh2": "weight unavailable", "ANBAR": []interface{}{0, 0}, "ALLANBAR": 0,
		},
		{"Code": "999010", "Name": "Synthetic freight accounting row"},
	}
	_, envelope := Transform(
		context.Background(), rows, "synthetic-kala.db", cfg, pricingcatalog.NewProvider(cfg.Pricing),
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	)
	return envelope
}
