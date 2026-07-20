package canonical

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

// LandedPrice evaluates the living pricing formula with exact decimal
// rationals and rounds once, half-up, to the final whole IRT amount. Freight
// may be quoted in CNY per kilogram or IRR per kilogram; IRR is converted to
// IRT at ten IRR per IRT before markup is applied.
func LandedPrice(weightGrams, shippingPricePerKg, shippingCurrency, foreignCNY, markupPercent, irtPerCNY string) (int64, error) {
	values := make([]*big.Rat, 0, 5)
	for _, input := range []string{weightGrams, shippingPricePerKg, foreignCNY, markupPercent, irtPerCNY} {
		value, ok := new(big.Rat).SetString(strings.TrimSpace(input))
		if !ok || value.Sign() < 0 {
			return 0, fmt.Errorf("landed_price inputs must be finite non-negative decimals")
		}
		values = append(values, value)
	}

	shippingCurrency = strings.TrimSpace(shippingCurrency)
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

	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(result.Num(), result.Denom(), remainder)
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(result.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("landed_price result exceeds int64")
	}
	return quotient.Int64(), nil
}
