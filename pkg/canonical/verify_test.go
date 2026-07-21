package canonical

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const emptySourceRevision = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func TestStandaloneProductSyncGoldenFixture(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "patris-product-sync.standalone.json")
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	envelope, _, err := VerifySnapshotJSON(expected)
	if err != nil {
		t.Fatalf("standalone fixture did not verify: %v", err)
	}
	encoded, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	actual := append(encoded, '\n')
	if !bytes.Equal(actual, expected) {
		t.Fatalf("standalone product-sync fixture is not the producer's canonical JSON: %s", path)
	}
}

func TestEventIdentityOmitsAbsentOptionalPricingFields(t *testing.T) {
	envelope := emptyIdentityFixture(false)
	const expected = "sha256:ab188e19ce426fdb627306f2a0d95dfb4049012a097cb0f303c578da089faa63"
	if envelope.EventID != expected {
		t.Fatalf("standalone event_id = %q, want %q", envelope.EventID, expected)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"local_currency"`) || strings.Contains(string(encoded), `"formula_id"`) {
		t.Fatalf("absent optional fields leaked onto the wire: %s", encoded)
	}
	if _, _, err := VerifySnapshotJSON(encoded); err != nil {
		t.Fatalf("standalone identity did not verify: %v", err)
	}
}

func TestIntegratedEventIdentityIncludesPresentPricingFields(t *testing.T) {
	envelope := emptyIdentityFixture(true)
	const expected = "sha256:fc48b56e2c9fa6edec5c9d2828389f492ea0464c9780a91e656c6df97839cb71"
	if envelope.EventID != expected {
		t.Fatalf("integrated event_id = %q, want %q", envelope.EventID, expected)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{`"local_currency":"IRT"`, `"formula_id":"landed_price"`} {
		if !strings.Contains(string(encoded), member) {
			t.Fatalf("present pricing identity field %s is missing: %s", member, encoded)
		}
	}
	if _, _, err := VerifySnapshotJSON(encoded); err != nil {
		t.Fatalf("integrated identity did not verify: %v", err)
	}
}

func TestValidateEnvelopeIdentityChecksEveryIdentityLayer(t *testing.T) {
	valid := syntheticGoldenEnvelope()
	if err := ValidateEnvelopeIdentity(valid); err != nil {
		t.Fatalf("valid producer snapshot was rejected: %v", err)
	}

	tests := map[string]struct {
		mutate func(*Envelope)
		want   string
	}{
		"product record hash": {
			mutate: func(value *Envelope) { value.Products[0].RecordHash = hashBytes([]byte("tampered")) },
			want:   "record_hash mismatch",
		},
		"category record hash": {
			mutate: func(value *Envelope) { value.Categories[0].RecordHash = hashBytes([]byte("tampered")) },
			want:   "record_hash mismatch",
		},
		"source revision": {
			mutate: func(value *Envelope) { value.Source.Revision = hashBytes([]byte("tampered")) },
			want:   "source.revision mismatch",
		},
		"event id": {
			mutate: func(value *Envelope) { value.EventID = hashBytes([]byte("tampered")) },
			want:   "event_id mismatch",
		},
		"duplicate product": {
			mutate: func(value *Envelope) {
				value.Products = append(value.Products, cloneProduct(value.Products[len(value.Products)-1]))
			},
			want: "sorted by unique product_code",
		},
		"null warnings shape": {
			mutate: func(value *Envelope) { value.Warnings = nil },
			want:   "warnings must be an array",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := cloneEnvelope(valid)
			test.mutate(candidate)
			err := ValidateEnvelopeIdentity(candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestVerifySnapshotJSONRejectsExplicitEmptyOptionalIdentityFields(t *testing.T) {
	payload := `{
  "schema":"patris.product-sync",
  "event_type":"snapshot",
  "event_id":"sha256:ab188e19ce426fdb627306f2a0d95dfb4049012a097cb0f303c578da089faa63",
  "local_currency":"",
  "formula_id":"",
  "source":{"id":"fixture","dataset":"kala.json","revision":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
  "generated_at":"2026-07-21T00:00:00Z",
  "products":[],
  "categories":[],
  "excluded_codes":[],
  "quarantined_codes":[],
  "warnings":[]
}`
	if _, _, err := VerifySnapshotJSON([]byte(payload)); err == nil || !strings.Contains(err.Error(), "local_currency must be") {
		t.Fatalf("explicit empty optional fields were not rejected: %v", err)
	}
}

func TestVerifySnapshotJSONRejectsDuplicateObjectFields(t *testing.T) {
	payload := `{"schema":"patris.product-sync","schema":"patris.product-sync"}`
	if _, _, err := VerifySnapshotJSON([]byte(payload)); err == nil || !strings.Contains(err.Error(), "duplicate JSON field") {
		t.Fatalf("duplicate object member was not rejected: %v", err)
	}
}

func emptyIdentityFixture(withPricing bool) *Envelope {
	envelope := &Envelope{
		Schema:           ContractName,
		EventType:        "snapshot",
		Source:           Source{ID: "fixture", Dataset: "kala.json", Revision: emptySourceRevision},
		GeneratedAt:      "2026-07-21T00:00:00Z",
		Products:         []Product{},
		Categories:       []Category{},
		ExcludedCodes:    []string{},
		QuarantinedCodes: []string{},
		Warnings:         []string{},
	}
	if withPricing {
		envelope.LocalCurrency = LocalCurrency
		envelope.FormulaID = FormulaID
	}
	envelope.EventID = eventID(envelope)
	return envelope
}
