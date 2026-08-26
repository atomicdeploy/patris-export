package server

import (
	"context"
	"testing"
	"time"
)

func TestExcelPricingRemoteSnapshotFallsBackToBoundedStatusPolling(t *testing.T) {
	fixture := newExcelPricingRemoteSnapshotFixture(t, "poll_ready")
	defer fixture.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := fixture.Client(t).Collect(ctx, fixture.requestID, 0)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.SnapshotRevision != fixture.payload.SnapshotRevision {
		t.Fatalf("snapshot revision = %q", result.SnapshotRevision)
	}
	fixture.assertCalls(t, 1, 1, 1, 2, 0)
}

func TestExcelPricingRemoteSnapshotStatusFallbackFailsBoundedly(t *testing.T) {
	fixture := newExcelPricingRemoteSnapshotFixture(t, "no_terminal_event")
	defer fixture.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := fixture.Client(t).Collect(ctx, fixture.requestID, 0)
	stage, code, ok := excelPricingRemoteSnapshotFailureDetails(err)
	if !ok || stage != excelPricingRemoteSnapshotStageStatusFallback ||
		code != "snapshot_status_fallback_failed" {
		t.Fatalf("fallback error stage=%q code=%q err=%v", stage, code, err)
	}
	fixture.assertCalls(t, 1, 1, 0, 1, 1)
}
