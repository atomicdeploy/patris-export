package canonical

import (
	"strings"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

func TestCalculationDecimalAdmissionMatchesSharedPHPBoundaries(t *testing.T) {
	engines := []struct {
		name     string
		evaluate func(string) (int64, error)
	}{
		{"foreign", func(value string) (int64, error) {
			return LandedPrice("1", "1", pricingcatalog.CurrencyCNY, value, "0", "1", 0)
		}},
		{"partner", func(value string) (int64, error) { return PartnerPrice(value, "0", 0) }},
		{"direct", DirectSalePrice},
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			for _, value := range []string{"10", "999999999999990", "10.000000000000"} {
				if _, err := engine.evaluate(value); err != nil {
					t.Fatalf("supported boundary %q rejected: %v", value, err)
				}
			}
			for _, value := range []string{"1000000000000000", "10.0000000000000", "1e1", "+10", "010", " 10", "10\n", "10/1"} {
				if _, err := engine.evaluate(value); err == nil {
					t.Fatalf("unsupported calculation token %q accepted", value)
				}
			}
		})
	}
	if _, err := PartnerPrice("10.123456789012", "0", 0); err != nil {
		t.Fatalf("nonzero twelve-place fraction rejected: %v", err)
	}
}

func TestCalculationDecimalAdmissionCoversEveryForeignOperand(t *testing.T) {
	for index := 0; index < 5; index++ {
		values := []string{"1", "1", "1", "0", "1"}
		values[index] = "1000000000000000"
		if _, err := LandedPrice(values[0], values[1], pricingcatalog.CurrencyCNY, values[2], values[3], values[4], 0); err == nil {
			t.Fatalf("foreign operand %d bypassed the range guard", index)
		}
	}
	if _, err := PartnerPrice("10", "0.0000000000001", 0); err == nil {
		t.Fatal("partner markup bypassed the fraction guard")
	}
}

func TestSelectedContractRejectsUnsupportedCalculationDecimals(t *testing.T) {
	for _, field := range []string{"price_source_amount", "weight_grams", "shipping_price_per_kg", "irt_per_cny", "markup_percent"} {
		t.Run(field, func(t *testing.T) {
			product := syntheticGoldenEnvelope().Products[0]
			value := pricingcatalog.Decimal("1000000000000000")
			switch field {
			case "price_source_amount":
				product.PriceSourceAmount = &value
			case "weight_grams":
				product.WeightGrams = &value
			case "shipping_price_per_kg":
				product.ShippingPricePerKg = &value
			case "irt_per_cny":
				product.IRTPerCNY = &value
			case "markup_percent":
				product.MarkupPercent = &value
			}
			product.RecordHash = recordHash(product)
			if err := validateProductIdentity(product, 0); err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("selected %s bypassed shared domain validation: %v", field, err)
			}
		})
	}
}

func TestSelectedCalculationGuardPreservesUnconsumedRawDecimals(t *testing.T) {
	product := syntheticGoldenEnvelope().Products[0]
	raw := pricingcatalog.Decimal("1000000000000000000.1234567890123456789")
	product.ForeignPrice = &raw
	product.PartnerPriceSource = &raw
	product.RecordHash = recordHash(product)
	if err := validateProductIdentity(product, 0); err != nil {
		t.Fatalf("unconsumed raw source decimal was unnecessarily restricted: %v", err)
	}
	for _, kind := range []string{PriceSourceKindPartner, PriceSourceKindSaleDirect} {
		product.PriceSourceKind = kind
		product.PriceSourceAmount = &raw
		if err := validateSelectedCalculationInputs(product, "product"); err == nil || !strings.Contains(err.Error(), "price_source_amount") {
			t.Fatalf("%s selected amount bypassed domain validation: %v", kind, err)
		}
	}
}
