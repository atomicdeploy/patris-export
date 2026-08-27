package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

func TestExcelPricingRemoteSnapshotConflictCodesAreRecoverableAtEveryStage(t *testing.T) {
	for _, code := range []string{
		"digitalogic_pricing_snapshot_source_revision_conflict",
		"digitalogic_pricing_snapshot_state_revision_conflict",
	} {
		err := readExcelPricingRemoteSnapshotResponseError(
			bytes.NewBufferString(`{"code":"`+code+`"}`),
			errExcelPricingRemoteSnapshotUnavailable,
		)
		if !errors.Is(err, errExcelPricingRemoteSnapshotSourceConflict) {
			t.Fatalf("code %q mapped to %v", code, err)
		}
		for _, stage := range []string{
			excelPricingRemoteSnapshotStageRevisionFetch,
			excelPricingRemoteSnapshotStageSnapshotStart,
			excelPricingRemoteSnapshotStageStatusFallback,
			excelPricingRemoteSnapshotStageSnapshotPayload,
		} {
			wrapped := wrapExcelPricingRemoteSnapshotStage(stage, err)
			gotStage, gotCode, ok := excelPricingRemoteSnapshotFailureDetails(wrapped)
			if !ok || gotStage != stage || gotCode != "snapshot_source_revision_conflict" {
				t.Fatalf("stage %q details=(%q,%q,%v)", stage, gotStage, gotCode, ok)
			}
			if got := excelPricingRemoteSnapshotFailureCode(context.Background(), wrapped); got != "snapshot_source_revision_conflict" {
				t.Fatalf("stage %q public code=%q", stage, got)
			}
		}
	}
}

func TestExcelPricingRemoteSnapshotUnknownErrorDoesNotBecomeConflict(t *testing.T) {
	want := errExcelPricingRemoteSnapshotUnavailable
	got := readExcelPricingRemoteSnapshotResponseError(
		bytes.NewBufferString(`{"code":"unrelated_failure"}`),
		want,
	)
	if !errors.Is(got, want) || errors.Is(got, errExcelPricingRemoteSnapshotSourceConflict) {
		t.Fatalf("unknown remote error mapped to %v", got)
	}
}

func TestExcelPricingSnapshotLiveDeliveryMayBePendingButNeverAmbiguous(t *testing.T) {
	eventID := testExcelPricingRevision('e')
	result := updateout.DeliveryResult{
		HTTPStatus:      http.StatusAccepted,
		Status:          "accepted",
		EventID:         eventID,
		Attempts:        1,
		PendingProducts: 976,
	}
	if !excelPricingSnapshotDeliveryAccepted(result, eventID) {
		t.Fatal("current stored source was rejected only because product actuation remains pending")
	}
	result.DeferredAmbiguous = 1
	if excelPricingSnapshotDeliveryAccepted(result, eventID) {
		t.Fatal("ambiguous product identity must remain blocking")
	}
	result.DeferredAmbiguous = 0
	if excelPricingSnapshotDeliveryAccepted(result, testExcelPricingRevision('f')) {
		t.Fatal("a different event/source envelope must not be selected as current")
	}
}

func TestExcelPricingRemoteSnapshotRevisionConflictIsActionable(t *testing.T) {
	t.Setenv(excelPricingRemoteSnapshotTestSecretEnv, "test-only-secret")
	source := canonical.Source{
		ID:       "patris-office",
		Dataset:  "kala.db",
		Revision: testExcelPricingRevision('a'),
	}
	remote := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
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

func TestExcelPricingRemoteSnapshotBypassesAmbientProxy(t *testing.T) {
	t.Setenv(excelPricingRemoteSnapshotTestSecretEnv, "test-only-secret")
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	client, err := newExcelPricingRemoteSnapshotClient(updateout.Config{
		Enabled:              true,
		URL:                  "https://digitalogic.invalid/wp-json/digitalogic/patris/product-sync",
		Format:               "json",
		Method:               http.MethodPost,
		Mode:                 "changes",
		ProductSyncSecretEnv: excelPricingRemoteSnapshotTestSecretEnv,
	}, canonical.Source{
		ID:       "patris-office",
		Dataset:  "kala.db",
		Revision: testExcelPricingRevision('a'),
	}, excelPricingRemoteSnapshotClientOptions{
		HTTPClient: &http.Client{Transport: transport},
		Terminals:  newExcelPricingRemoteSnapshotTerminalHub(),
	})
	if err != nil {
		t.Fatal(err)
	}
	direct, ok := client.client.Transport.(*http.Transport)
	if !ok || direct.Proxy != nil {
		t.Fatalf("snapshot HTTP transport = %#v", client.client.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("source transport was mutated")
	}
}
