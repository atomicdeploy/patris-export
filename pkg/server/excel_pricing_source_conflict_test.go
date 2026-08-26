package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

func TestExcelPricingRemoteSnapshotRevisionConflictIsActionable(t *testing.T) {
	t.Setenv(excelPricingRemoteSnapshotTestSecretEnv, "test-only-secret")
	source := canonical.Source{
		ID:       "patris-office",
		Dataset:  "kala.db",
		Revision: testExcelPricingRevision('a'),
	}
	remote := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(response, `{"code":"digitalogic_pricing_snapshot_source_revision_conflict"}`)
	}))
	defer remote.Close()

	client, err := newExcelPricingRemoteSnapshotClient(updateout.Config{
		Enabled:              true,
		URL:                  remote.URL + "/wp-json/digitalogic/patris/product-sync",
		Format:               "json",
		Method:               http.MethodPost,
		Mode:                 "changes",
		ProductSyncSecretEnv: excelPricingRemoteSnapshotTestSecretEnv,
	}, source, excelPricingRemoteSnapshotClientOptions{
		Terminals: newExcelPricingRemoteSnapshotTerminalHub(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.fetchRevision(context.Background()); !errors.Is(err, errExcelPricingRemoteSnapshotSourceConflict) {
		t.Fatalf("revision conflict=%v, want actionable source conflict", err)
	}
}

func TestExcelPricingRemoteSnapshotSourceConflictRemainsActionableForWorkbook(t *testing.T) {
	err := wrapExcelPricingRemoteSnapshotStage(
		excelPricingRemoteSnapshotStageRevisionFetch,
		errExcelPricingRemoteSnapshotSourceConflict,
	)
	if got := excelPricingRemoteSnapshotFailureCode(context.Background(), err); got != "snapshot_source_revision_conflict" {
		t.Fatalf("public source-conflict code = %q", got)
	}
}
