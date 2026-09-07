package canonical

import (
	"encoding/json"
	"testing"
)

func TestSourceIdentityExcludesOptionalProviderMetadata(t *testing.T) {
	var first, second Source
	for _, target := range []*Source{&first, &second} {
		if err := json.Unmarshal([]byte(`{"id":"owner","dataset":"kala.db","revision":"r1","audit":{"attempt":1}}`), target); err != nil {
			t.Fatal(err)
		}
	}
	if first.Extensions == second.Extensions || !first.SameIdentity(second) {
		t.Fatal("independently decoded metadata changed source identity")
	}
	(*second.Extensions)["audit"] = json.RawMessage(`{"attempt":2}`)
	if !first.SameIdentity(second) {
		t.Fatal("delivery metadata changed source identity")
	}
	for _, change := range []func(*Source){
		func(s *Source) { s.ID = "other" },
		func(s *Source) { s.Dataset = "other.db" },
		func(s *Source) { s.Revision = "r2" },
	} {
		candidate := second
		change(&candidate)
		if first.SameIdentity(candidate) {
			t.Fatal("a changed owner, dataset, or revision was accepted as the same identity")
		}
	}
}
