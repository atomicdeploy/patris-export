package server

import (
	"encoding/json"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
)

func TestExcelPricingReconciliationAcceptsReceiverAuditMetadata(t *testing.T) {
	var value excelPricingRemoteSnapshotReconciliation
	err := json.Unmarshal([]byte(`{
		"status":"current",
		"integrity_status":"current",
		"warnings":[],
		"source":{
			"id":"patris-office",
			"dataset":"kala.db",
			"revision":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"generated_at":"2026-08-26T20:16:39Z",
			"received_at":"2026-08-26 23:47:51",
			"last_event_id":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"last_event_type":"snapshot"
		},
		"counts":{}
	}`), &value)
	if err != nil {
		t.Fatal(err)
	}
	want := canonical.Source{
		ID:       "patris-office",
		Dataset:  "kala.db",
		Revision: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if value.Source != want {
		t.Fatalf("source = %+v, want %+v", value.Source, want)
	}
}
