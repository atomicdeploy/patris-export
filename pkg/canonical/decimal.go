package canonical

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

const (
	maximumCalculationIntegerDigits  = 15
	maximumCalculationFractionDigits = 12
)

var calculationDecimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(?:\.([0-9]+))?$`)

// validateCalculationDecimal matches the shared PHP formula input domain.
// Check the original token before trimming zeros; raw/report Decimal values
// retain their wider domain and are only restricted when selected for pricing.
func validateCalculationDecimal(input string) error {
	parts := calculationDecimalPattern.FindStringSubmatch(input)
	if parts == nil {
		return fmt.Errorf("calculation input must be a non-negative base-10 decimal without exponent notation")
	}
	if len(parts[1]) > maximumCalculationIntegerDigits {
		return fmt.Errorf("calculation input exceeds %d integer digits", maximumCalculationIntegerDigits)
	}
	if len(parts[2]) > maximumCalculationFractionDigits {
		return fmt.Errorf("calculation input exceeds %d fractional digits", maximumCalculationFractionDigits)
	}
	return nil
}

// LandedPrice evaluates the CNY pricing path with exact decimal rationals.
// Freight may be quoted in CNY per kilogram or IRR per kilogram; IRR is
// converted to IRT at ten IRR per IRT. The result is rounded exactly once to
// the nearest 10^roundingDigits IRT using deterministic half-up ties.
func LandedPrice(weightGrams, shippingPricePerKg, shippingCurrency, foreignCNY, markupPercent, irtPerCNY string, roundingDigits int, policies ...*pricingcatalog.RoundingPolicy) (int64, error) {
	values := make([]*big.Rat, 0, 5)
	for _, input := range []string{weightGrams, shippingPricePerKg, foreignCNY, markupPercent, irtPerCNY} {
		if err := validateCalculationDecimal(input); err != nil {
			return 0, err
		}
		value, ok := new(big.Rat).SetString(strings.TrimSpace(input))
		if !ok || value.Sign() < 0 {
			return 0, fmt.Errorf("landed_price inputs must be finite non-negative decimals")
		}
		values = append(values, value)
	}

	if shippingCurrency != pricingcatalog.CurrencyCNY && shippingCurrency != pricingcatalog.CurrencyIRR {
		return 0, fmt.Errorf("shipping currency must be CNY or IRR")
	}

	weight, shipping, foreign, markup, fx := values[0], values[1], values[2], values[3], values[4]
	shippingCost := new(big.Rat).Mul(weight, shipping)
	shippingCost.Quo(shippingCost, big.NewRat(1000, 1))
	if shippingCurrency == pricingcatalog.CurrencyCNY {
		shippingCost.Mul(shippingCost, fx)
	} else {
		shippingCost.Quo(shippingCost, big.NewRat(10, 1))
	}
	goodsCost := new(big.Rat).Mul(foreign, fx)
	landed := new(big.Rat).Add(goodsCost, shippingCost)
	markupMultiplier := new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(markup, big.NewRat(100, 1)))
	result := new(big.Rat).Mul(landed, markupMultiplier)
	return roundPriceWithPolicy(result, roundingDigits, policies...)
}

// PartnerPrice evaluates the direct Patris partner-price fallback. The source
// amount is explicitly IRR, so it is converted to IRT before markup and the
// same single final rounding operation. Freight and FX are deliberately absent
// from this path.
func PartnerPrice(partnerIRR, markupPercent string, roundingDigits int, policies ...*pricingcatalog.RoundingPolicy) (int64, error) {
	values := make([]*big.Rat, 0, 2)
	for _, input := range []string{partnerIRR, markupPercent} {
		if err := validateCalculationDecimal(input); err != nil {
			return 0, err
		}
		value, ok := new(big.Rat).SetString(strings.TrimSpace(input))
		if !ok || value.Sign() < 0 {
			return 0, fmt.Errorf("partner_price inputs must be finite non-negative decimals")
		}
		values = append(values, value)
	}
	partner, markup := values[0], values[1]
	goodsIRT := new(big.Rat).Quo(partner, big.NewRat(10, 1))
	markupMultiplier := new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(markup, big.NewRat(100, 1)))
	return roundPriceWithPolicy(new(big.Rat).Mul(goodsIRT, markupMultiplier), roundingDigits, policies...)
}

// DirectSalePrice uses Patris' sale amount without freight, markup, or
// commercial rounding. The source amount is IRR while final_price is expressed
// in the contract's IRT local currency, so the only operation is the exact
// ten-to-one currency-unit conversion. Values that cannot be represented as a
// whole IRT integer fail closed instead of being rounded.
func DirectSalePrice(saleIRR string) (int64, error) { return DirectSalePriceWithPolicy(saleIRR, nil) }

func DirectSalePriceWithPolicy(saleIRR string, policies ...*pricingcatalog.RoundingPolicy) (int64, error) {
	if err := validateCalculationDecimal(saleIRR); err != nil {
		return 0, err
	}
	value, ok := new(big.Rat).SetString(strings.TrimSpace(saleIRR))
	if !ok || value.Sign() <= 0 {
		return 0, fmt.Errorf("sale_price_direct input must be a finite positive decimal")
	}
	value.Quo(value, big.NewRat(10, 1))
	if len(policies) > 0 && policies[0] != nil {
		return roundPriceWithPolicy(value, 0, policies...)
	}
	if !value.IsInt() || !value.Num().IsInt64() {
		return 0, fmt.Errorf("sale_price_direct must convert exactly to a whole IRT integer")
	}
	return value.Num().Int64(), nil
}

func roundPrice(result *big.Rat, roundingDigits int) (int64, error) {
	if result == nil || result.Sign() < 0 {
		return 0, fmt.Errorf("price result must be a non-negative rational")
	}
	if roundingDigits < pricingcatalog.MinimumRoundDigits || roundingDigits > pricingcatalog.MaximumPolicyRoundDigits {
		return 0, fmt.Errorf("price rounding digits must be between %d and %d", pricingcatalog.MinimumRoundDigits, pricingcatalog.MaximumPolicyRoundDigits)
	}
	quantum := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(roundingDigits)), nil)
	scaled := new(big.Rat).Quo(new(big.Rat).Set(result), new(big.Rat).SetInt(quantum))
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(scaled.Num(), scaled.Denom(), remainder)
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(scaled.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	quotient.Mul(quotient, quantum)
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("price result exceeds int64")
	}
	return quotient.Int64(), nil
}

func roundPriceWithPolicy(total *big.Rat, digits int, policies ...*pricingcatalog.RoundingPolicy) (int64, error) {
	if len(policies) > 0 && policies[0] != nil {
		var err error
		digits, err = policies[0].DigitsForIRT(total)
		if err != nil {
			return 0, err
		}
	} else if digits > pricingcatalog.MaximumRoundDigits {
		return 0, fmt.Errorf("fixed rounding digits exceed supported range")
	}
	return roundPrice(total, digits)
}

// EffectiveRoundingDigits computes provenance from the unrounded formula, never from the rounded output.
func EffectiveRoundingDigits(kind string, amount, weight, shipping, currency, markup, fx string, p *pricingcatalog.RoundingPolicy) (int, error) {
	rat := func(s string) *big.Rat { v, _ := new(big.Rat).SetString(s); return v }
	value := rat(amount)
	if value == nil {
		return 0, fmt.Errorf("missing price amount")
	}
	switch kind {
	case PriceSourceKindForeign:
		w, s, m, f := rat(weight), rat(shipping), rat(markup), rat(fx)
		if w == nil || s == nil || m == nil || f == nil {
			return 0, fmt.Errorf("missing foreign formula input")
		}
		freight := new(big.Rat).Quo(new(big.Rat).Mul(w, s), big.NewRat(1000, 1))
		if currency == pricingcatalog.CurrencyCNY {
			freight.Mul(freight, f)
		} else {
			freight.Quo(freight, big.NewRat(10, 1))
		}
		value.Add(value.Mul(value, f), freight)
		value.Mul(value, new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(m, big.NewRat(100, 1))))
	case PriceSourceKindPartner:
		m := rat(markup)
		if m == nil {
			return 0, fmt.Errorf("missing markup")
		}
		value.Quo(value, big.NewRat(10, 1))
		value.Mul(value, new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(m, big.NewRat(100, 1))))
	case PriceSourceKindSaleDirect:
		value.Quo(value, big.NewRat(10, 1))
	default:
		return 0, fmt.Errorf("unknown price source")
	}
	return p.DigitsForIRT(value)
}
