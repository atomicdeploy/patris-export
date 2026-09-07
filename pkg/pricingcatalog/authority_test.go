package pricingcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOwnerAuthorityIsExplicitForRemoteAndLocalProviders(t *testing.T) {
	for _, value := range []struct{ name, wire, local, want, warning string }{
		{"php", `"php"`, "php", AuthorityPHP, ""},
		{"go", `"go"`, "go", AuthorityGo, ""},
		{"missing", "", "", "", "pricing_authority_missing"},
		{"null", "null", "", "", "pricing_authority_missing"},
		{"unknown", `"both"`, "both", "", "pricing_authority_invalid"},
		{"case", `"Go"`, "Go", "", "pricing_authority_invalid"},
		{"whitespace", `" go "`, " go ", "", "pricing_authority_invalid"},
	} {
		t.Run(value.name, func(t *testing.T) {
			owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path != "/integration/catalog" {
					fmt.Fprint(w, `{"data":{"shipping_method_id":"air","profit_percent":30}}`)
					return
				}
				authority := ""
				if value.wire != "" {
					authority = `,"authority":` + value.wire
				}
				fmt.Fprintf(w, `{"data":{"schema":"digitalogic.integration-catalog","revision":"owner-revision","currency":{"local":"IRT","cny_to_irt":30000},"pricing":{"formula_id":"landed_price"%s},"shipping_methods":[]}}`, authority)
			}))
			defer owner.Close()
			for name, provider := range map[string]Provider{
				"remote": NewProvider(Config{Mode: ModeDigitalogic, Digitalogic: DigitalogicConfig{BaseURL: owner.URL}}),
				"local":  NewProvider(Config{Mode: ModeStatic, Static: StaticConfig{Authority: value.local}}),
			} {
				got := provider.Resolve(context.Background(), "fixture")
				if got.Authority != value.want {
					t.Fatalf("%s authority=%q want=%q", name, got.Authority, value.want)
				}
				if got.AuthorityError != value.warning {
					t.Fatalf("%s authority error=%q want=%q", name, got.AuthorityError, value.warning)
				}
				if name == "remote" && got.CatalogRevision != "owner-revision" {
					t.Fatalf("owner revision changed: %q", got.CatalogRevision)
				}
			}
		})
	}
}

func TestStaticAuthorityRoundTripsAndChangesExistingDerivedRevision(t *testing.T) {
	var previous string
	for _, authority := range []string{AuthorityPHP, AuthorityGo} {
		encoded, err := json.Marshal(Config{Mode: ModeStatic, Static: StaticConfig{Authority: authority}})
		if err != nil {
			t.Fatal(err)
		}
		var cfg Config
		if err := json.Unmarshal(encoded, &cfg); err != nil {
			t.Fatal(err)
		}
		got := NewProvider(cfg).Resolve(context.Background(), "fixture")
		if got.Authority != authority || got.CatalogRevision == previous {
			t.Fatalf("authority/revision did not round-trip: %+v", got)
		}
		previous = got.CatalogRevision
	}
}
