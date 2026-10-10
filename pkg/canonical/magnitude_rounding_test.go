package canonical

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

func magnitudePolicy() *pricingcatalog.RoundingPolicy {
	return &pricingcatalog.RoundingPolicy{Tiers: []pricingcatalog.RoundingTier{{ThresholdIRT: "1000", Digits: 1}, {ThresholdIRT: "10000", Digits: 2}, {ThresholdIRT: "100000", Digits: 3}, {ThresholdIRT: "1000000", Digits: 4}, {ThresholdIRT: "10000000", Digits: 5}}, ExtendDecades: true}
}

func TestMagnitudeRoundingExactBoundariesAndRoutes(t *testing.T) {
	p := magnitudePolicy()
	for _, tt := range []struct {
		irt, irr string
		digits   int
		want     int64
	}{
		{"999", "9990", 0, 999}, {"999.5", "9995", 0, 1000}, {"1000", "10000", 1, 1000},
		{"1004.999999999999", "10049.99999999999", 1, 1000}, {"1005", "10050", 1, 1010},
		{"9999", "99990", 1, 10000}, {"10000", "100000", 2, 10000},
		{"10050", "100500", 2, 10100}, {"99999", "999990", 2, 100000},
		{"100000000", "1000000000", 6, 100000000},
	} {
		t.Run(tt.irt, func(t *testing.T) {
			got, err := LandedPrice("1", "0", pricingcatalog.CurrencyCNY, tt.irt, "0", "1", 2, p)
			if err != nil || got != tt.want {
				t.Fatalf("foreign=%d %v want %d", got, err, tt.want)
			}
			got, err = PartnerPrice(tt.irr, "0", 2, p)
			if err != nil || got != tt.want {
				t.Fatalf("partner=%d %v", got, err)
			}
			got, err = DirectSalePriceWithPolicy(tt.irr, p)
			if err != nil || got != tt.want {
				t.Fatalf("direct=%d %v", got, err)
			}
			digits, err := EffectiveRoundingDigits(PriceSourceKindSaleDirect, tt.irr, "", "", "", "", "", p)
			if err != nil || digits != tt.digits {
				t.Fatalf("digits=%d %v want %d", digits, err, tt.digits)
			}
		})
	}
	if _, err := DirectSalePrice("9995"); err == nil {
		t.Fatal("legacy direct rounding changed")
	}
}

func TestMagnitudeDirectProjectionPreservesInputsAndRejectsFalseProvenance(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SourceID = "magnitude-test"
	cfg.Pricing = pricingcatalog.Config{Mode: pricingcatalog.ModeStatic, Static: pricingcatalog.StaticConfig{Authority: pricingcatalog.AuthorityGo, RoundingPolicy: magnitudePolicy()}, UseSalePriceDirectFallback: true}
	input := []map[string]interface{}{{"Code": "123456", "Name": "Direct product", "FOROSH": 10050, "weight_grams": 1, "total_stock": 7}}
	rows, envelope, err := TransformContext(context.Background(), input, "kala.db", cfg, nil, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if rows[0]["final_price"] != int64(1010) || rows[0]["product_code"] != "123456" || input[0]["FOROSH"] != 10050 || input[0]["total_stock"] != 7 {
		t.Fatalf("unexpected mutation/projection: %#v", rows)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = VerifySnapshotJSON(encoded); err != nil {
		t.Fatal(err)
	}
	product := envelope.Products[0]
	*product.PriceRoundingDigits = 2
	product.RecordHash = recordHash(product)
	encoded, _ = json.Marshal(product)
	if _, err = VerifyProductJSON(encoded); err == nil {
		t.Fatal("accepted false effective digits")
	}
	product = envelope.Products[0]
	product.PriceRoundingPolicy = nil
	product.RecordHash = recordHash(product)
	encoded, _ = json.Marshal(product)
	if _, err = VerifyProductJSON(encoded); err == nil {
		t.Fatal("accepted rounded direct without policy")
	}
}
