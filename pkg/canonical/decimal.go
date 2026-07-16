package canonical

import (
	"fmt"
	"math/big"
	"strings"
)

// LandedPriceV1 evaluates the workbook formula with exact decimal rationals
// and rounds once, half-up, to the final whole IRT amount.
func LandedPriceV1(weightGrams, freightCNYPerKg, foreignCNY, markupPercent, irtPerCNY string) (int64, error) {
	values := make([]*big.Rat, 0, 5)
	for _, input := range []string{weightGrams, freightCNYPerKg, foreignCNY, markupPercent, irtPerCNY} {
		value, ok := new(big.Rat).SetString(strings.TrimSpace(input))
		if !ok || value.Sign() < 0 {
			return 0, fmt.Errorf("landed_price_v1 inputs must be finite non-negative decimals")
		}
		values = append(values, value)
	}

	weight, freight, foreign, markup, fx := values[0], values[1], values[2], values[3], values[4]
	freightCost := new(big.Rat).Mul(weight, freight)
	freightCost.Quo(freightCost, big.NewRat(1000, 1))
	landed := new(big.Rat).Add(foreign, freightCost)
	markupMultiplier := new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(markup, big.NewRat(100, 1)))
	result := new(big.Rat).Mul(landed, markupMultiplier)
	result.Mul(result, fx)

	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(result.Num(), result.Denom(), remainder)
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(result.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("landed_price_v1 result exceeds int64")
	}
	return quotient.Int64(), nil
}
