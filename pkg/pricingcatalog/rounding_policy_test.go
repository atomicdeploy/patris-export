package pricingcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCatalogTransportsPolicyAndRejectsMalformedPolicy(t *testing.T) {
	for _, policy := range []string{`null`, `{"tiers":[{"threshold_irt":"1000","digits":1}],"extend_decades":true}`, `{"tiers":[{"threshold_irt":"1000","digits":4}],"extend_decades":true}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"data":{"schema":"digitalogic.integration-catalog","revision":"r1","currency":{"local":"IRT","cny_to_irt":1},"pricing":{"formula_id":"landed_price","rounding_policy":%s}}}`, policy)
		}))
		provider := newHTTPProvider(DigitalogicConfig{BaseURL: server.URL, CatalogPath: "integration/catalog"}, server.Client(), time.Now)
		catalog, err := provider.fetchCatalog(context.Background())
		if policy == `null` {
			if err != nil || catalog.roundingPolicy != nil {
				t.Fatalf("null policy: %v", err)
			}
		} else if policy == `{"tiers":[{"threshold_irt":"1000","digits":4}],"extend_decades":true}` {
			if err == nil {
				t.Fatal("invalid policy accepted")
			}
		} else {
			if err != nil || catalog.roundingPolicy == nil || catalog.roundingPolicy.Tiers[0].Digits != 1 {
				t.Fatalf("policy lost: %v", err)
			}
			copy := cloneCatalog(catalog)
			copy.roundingPolicy.Tiers[0].Digits = 0
			if catalog.roundingPolicy.Tiers[0].Digits != 1 {
				t.Fatal("policy cache alias")
			}
		}
		server.Close()
	}
	p := RoundingPolicy{Tiers: []RoundingTier{{"1000", 1}}, ExtendDecades: true}
	encoded, _ := json.Marshal(p)
	if string(encoded) != `{"tiers":[{"threshold_irt":"1000","digits":1}],"extend_decades":true}` {
		t.Fatalf("noncanonical policy hash material %s", encoded)
	}
}

func TestRoundingPolicyValidationAndCustomContinuation(t *testing.T) {
	for _, data := range []string{
		`{"tiers":[],"extend_decades":true}`,
		`{"tiers":[{"threshold_irt":"1000","digits":4}],"extend_decades":true}`,
		`{"tiers":[{"threshold_irt":"01000","digits":1}],"extend_decades":true}`,
		`{"tiers":[{"threshold_irt":"1000","digits":1},{"threshold_irt":"900","digits":1}],"extend_decades":true}`,
		`{"tiers":[{"threshold_irt":"1000","digits":2},{"threshold_irt":"10000","digits":1}],"extend_decades":true}`,
		`{"tiers":[{"threshold_irt":"1000","digits":null}],"extend_decades":true}`,
		`{"tiers":[{"threshold_irt":"1000","digits":1}]}`,
	} {
		var p RoundingPolicy
		if json.Unmarshal([]byte(data), &p) == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	p := RoundingPolicy{Tiers: []RoundingTier{{"2500", 2}, {"50000", 3}}, ExtendDecades: true}
	for _, tt := range []struct {
		value string
		want  int
	}{{"2499.999", 0}, {"2500", 2}, {"49999", 2}, {"50000", 3}, {"499999", 3}, {"500000", 4}, {"500000000000000000000000000000", 18}} {
		v, _ := new(big.Rat).SetString(tt.value)
		got, err := p.DigitsForIRT(v)
		if err != nil || got != tt.want {
			t.Fatalf("%s=%d %v", tt.value, got, err)
		}
	}
	p.ExtendDecades = false
	v, _ := new(big.Rat).SetString("5000000")
	got, _ := p.DigitsForIRT(v)
	if got != 3 {
		t.Fatal(got)
	}
}
