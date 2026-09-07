package server

import (
	"context"
	"errors"
	"testing"
)

func TestPHPAuthoritySnapshotBindsInputThenPinsFinalSource(t *testing.T) {
	for _, mode := range []string{"ready", "event_before_response"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newExcelPricingRemoteSnapshotFixture(t, mode)
			defer fixture.Close()
			input := fixture.source
			input.Revision = excelPricingRevisionForTest("original-input")
			fixture.revision.InputSource = &input
			fixture.revision.OwnerCatalogRevision = excelPricingRevisionForTest("owner-catalog")
			client := fixture.Client(t)
			client.source = input
			client.inputCatalogRevision = fixture.revision.OwnerCatalogRevision
			result, err := client.Collect(context.Background(), fixture.requestID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Source.SameIdentity(fixture.source) || result.Source.SameIdentity(input) {
				t.Fatalf("final source was not retained: %+v", result.Source)
			}
			if !client.source.SameIdentity(input) {
				t.Fatal("collection mutated the reusable input-scoped client")
			}
			fixture.assertCalls(t, 1, 1, 1, 0, 0)
		})
	}
}

func TestPHPAuthoritySnapshotRejectsMissingOrMismatchedInputBinding(t *testing.T) {
	for _, mode := range []string{"missing", "wrong input revision", "wrong final owner", "wrong final dataset", "wrong catalog", "missing owner catalog", "invalid final revision"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
			defer fixture.Close()
			input := fixture.source
			fixture.revision.InputSource = &input
			fixture.revision.OwnerCatalogRevision = excelPricingRevisionForTest("owner-catalog")
			client := fixture.Client(t)
			client.inputCatalogRevision = fixture.revision.OwnerCatalogRevision
			switch mode {
			case "missing":
				fixture.revision.InputSource = nil
			case "wrong input revision":
				input.Revision = excelPricingRevisionForTest("wrong-input")
			case "wrong final owner":
				fixture.revision.Source.ID = "other-owner"
			case "wrong final dataset":
				fixture.revision.Source.Dataset = "other.db"
			case "wrong catalog":
				client.inputCatalogRevision = excelPricingRevisionForTest("changed-catalog")
			case "missing owner catalog":
				fixture.revision.OwnerCatalogRevision = ""
			case "invalid final revision":
				fixture.revision.Source.Revision = "invalid"
			}
			if _, err := client.Collect(context.Background(), fixture.requestID, 0); !errors.Is(err, errExcelPricingRemoteSnapshotProtocol) {
				t.Fatalf("invalid binding accepted or wrong error: %v", err)
			}
			fixture.assertCalls(t, 1, 0, 0, 0, 0)
		})
	}
}
