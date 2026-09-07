package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
)

func TestPricingIdentityAndReservationsIgnoreOptionalSourceMetadata(t *testing.T) {
	var left, right canonical.Source
	raw := []byte(`{"id":"fixture","dataset":"kala.db","revision":"sha256:` + strings.Repeat("a", 64) + `","audit":{"delivery":1}}`)
	for _, value := range []*canonical.Source{&left, &right} {
		if err := json.Unmarshal(raw, value); err != nil {
			t.Fatal(err)
		}
	}
	(*right.Extensions)["audit"] = json.RawMessage(`{"delivery":2}`)
	a, b := excelPricingRemoteRevision{Source: left}, excelPricingRemoteRevision{Source: right}
	if !sameExcelPricingRemoteCompositeRevision(&a, b) {
		t.Fatal("optional delivery metadata changed a composite identity")
	}
	if excelPricingSnapshotCacheKey(left, "fa", "full") != excelPricingSnapshotCacheKey(right, "fa", "full") {
		t.Fatal("metadata split the snapshot cache")
	}
	first, second := excelPricingSnapshotStartRequest{Source: left}, excelPricingSnapshotStartRequest{Source: right}
	if excelPricingSnapshotRequestFingerprint(first) != excelPricingSnapshotRequestFingerprint(second) {
		t.Fatal("metadata conflicted with an identical snapshot reservation")
	}
	m, n := excelPricingLocalRequest{Source: &left}, excelPricingLocalRequest{Source: &right}
	if excelPricingMutationFingerprint(m) != excelPricingMutationFingerprint(n) {
		t.Fatal("metadata conflicted with an identical mutation reservation")
	}
	right.Revision = "sha256:" + strings.Repeat("b", 64)
	if excelPricingSnapshotCacheKey(left, "fa", "full") == excelPricingSnapshotCacheKey(right, "fa", "full") {
		t.Fatal("a changed owner revision reused the cache")
	}
	if left.Extensions == nil || right.Extensions == nil {
		t.Fatal("fingerprinting mutated the original provider metadata")
	}
}

func TestApplicationMetadataAdvertisesCollectionCapabilitiesWithoutReadingSource(t *testing.T) {
	for _, tc := range []struct {
		name     string
		products bool
	}{{"kala.db", true}, {"arbitrary.db", false}} {
		srv := &Server{dbPath: tc.name}
		recorder := httptest.NewRecorder()
		srv.handleGetApp(recorder, httptest.NewRequest(http.MethodGet, "/api/app", nil))
		if recorder.Code != http.StatusOK {
			t.Fatal(recorder.Code)
		}
		var body struct {
			Capabilities struct {
				Records      bool
				Products     bool
				Categories   bool
				RecordHashes bool `json:"record_hashes"`
			}
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if !body.Capabilities.Records || body.Capabilities.Products != tc.products || body.Capabilities.Categories != tc.products {
			t.Fatalf("incorrect collection capabilities: %+v", body)
		}
	}
}
