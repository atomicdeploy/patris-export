package pricingcatalog

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
)

const MaximumPolicyRoundDigits = 18

type RoundingTier struct {
	ThresholdIRT string `json:"threshold_irt" yaml:"threshold_irt" toml:"threshold_irt"`
	Digits       int    `json:"digits" yaml:"digits" toml:"digits"`
}

// RoundingPolicy selects a quantum from the exact, unrounded final IRT total.
type RoundingPolicy struct {
	Tiers         []RoundingTier `json:"tiers" yaml:"tiers" toml:"tiers"`
	ExtendDecades bool           `json:"extend_decades" yaml:"extend_decades" toml:"extend_decades"`
}

var thresholdPattern = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

func (p *RoundingPolicy) Validate() error {
	if p == nil {
		return nil
	}
	if len(p.Tiers) < 1 || len(p.Tiers) > 32 {
		return fmt.Errorf("rounding policy requires 1 through 32 tiers")
	}
	previous := new(big.Int)
	digits := -1
	for _, t := range p.Tiers {
		if !thresholdPattern.MatchString(t.ThresholdIRT) || t.Digits < 0 || t.Digits > MaximumPolicyRoundDigits || t.Digits < digits || t.Digits > len(t.ThresholdIRT)-1 {
			return fmt.Errorf("invalid rounding tier")
		}
		threshold, _ := new(big.Int).SetString(t.ThresholdIRT, 10)
		if threshold.Cmp(previous) <= 0 {
			return fmt.Errorf("rounding thresholds must increase")
		}
		previous = threshold
		digits = t.Digits
	}
	return nil
}

func (p *RoundingPolicy) UnmarshalJSON(data []byte) error {
	type plain RoundingPolicy
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if string(raw["extend_decades"]) != "true" && string(raw["extend_decades"]) != "false" {
		return fmt.Errorf("extend_decades must be a boolean")
	}
	var tiers []map[string]json.RawMessage
	if err := json.Unmarshal(raw["tiers"], &tiers); err != nil {
		return err
	}
	for _, t := range tiers {
		if len(t["digits"]) == 0 || string(t["digits"]) == "null" {
			return fmt.Errorf("tier digits must be an integer")
		}
	}
	*p = RoundingPolicy(v)
	return p.Validate()
}

func CloneRoundingPolicy(p *RoundingPolicy) *RoundingPolicy {
	if p == nil {
		return nil
	}
	copy := *p
	copy.Tiers = append([]RoundingTier(nil), p.Tiers...)
	return &copy
}

func (p *RoundingPolicy) DigitsForIRT(total *big.Rat) (int, error) {
	if p == nil || total == nil || total.Sign() < 0 {
		return 0, fmt.Errorf("rounding policy and non-negative total required")
	}
	if err := p.Validate(); err != nil {
		return 0, err
	}
	digits := 0
	for _, tier := range p.Tiers {
		threshold, _ := new(big.Rat).SetString(tier.ThresholdIRT)
		if total.Cmp(threshold) < 0 {
			return digits, nil
		}
		digits = tier.Digits
	}
	if p.ExtendDecades {
		threshold, _ := new(big.Rat).SetString(p.Tiers[len(p.Tiers)-1].ThresholdIRT)
		for digits < MaximumPolicyRoundDigits {
			threshold.Mul(threshold, big.NewRat(10, 1))
			if total.Cmp(threshold) < 0 {
				break
			}
			digits++
		}
	}
	return digits, nil
}
