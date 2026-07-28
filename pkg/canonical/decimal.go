package canonical

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

// LandedPrice evaluates the CNY pricing path with exact decimal rationals.
// Freight may be quoted in CNY per kilogram or IRR per kilogram; IRR is
// converted to IRT at ten IRR per IRT. The result is rounded exactly once to
// the nearest 10^roundingDigits IRT using deterministic half-up ties.
func LandedPrice(weightGrams, shippingPricePerKg, shippingCurrency, foreignCNY, markupPercent, irtPerCNY string, roundingDigits int) (int64, error) {
	values := make([]*big.Rat, 0, 5)
	for _, input := range []string{weightGrams, shippingPricePerKg, foreignCNY, markupPercent, irtPerCNY} {
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
	return roundPrice(result, roundingDigits)
}

// PartnerPrice evaluates the direct Patris partner-price fallback. The source
// amount is explicitly IRR, so it is converted to IRT before markup and the
// same single final rounding operation. Freight and FX are deliberately absent
// from this path.
func PartnerPrice(partnerIRR, markupPercent string, roundingDigits int) (int64, error) {
	values := make([]*big.Rat, 0, 2)
	for _, input := range []string{partnerIRR, markupPercent} {
		value, ok := new(big.Rat).SetString(strings.TrimSpace(input))
		if !ok || value.Sign() < 0 {
			return 0, fmt.Errorf("partner_price inputs must be finite non-negative decimals")
		}
		values = append(values, value)
	}
	partner, markup := values[0], values[1]
	goodsIRT := new(big.Rat).Quo(partner, big.NewRat(10, 1))
	markupMultiplier := new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(markup, big.NewRat(100, 1)))
	return roundPrice(new(big.Rat).Mul(goodsIRT, markupMultiplier), roundingDigits)
}

func roundPrice(result *big.Rat, roundingDigits int) (int64, error) {
	if result == nil || result.Sign() < 0 {
		return 0, fmt.Errorf("price result must be a non-negative rational")
	}
	if roundingDigits < pricingcatalog.MinimumRoundDigits || roundingDigits > pricingcatalog.MaximumRoundDigits {
		return 0, fmt.Errorf("price rounding digits must be between %d and %d", pricingcatalog.MinimumRoundDigits, pricingcatalog.MaximumRoundDigits)
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
