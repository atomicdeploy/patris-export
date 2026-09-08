package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

func TestQueuedStartupRetainsActiveOwnerUntilPermitReleased(t *testing.T) {
	called := make(chan struct{}, 1)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer remote.Close()
	s := newCanonicalProjectionTestServer(t, remote.URL, "1s", true, true)
	defer s.Close()
	cfg := s.Config()
	cfg.SendUpdates.URL = remote.URL
	if err := s.config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	s.excelPricing.permit <- struct{}{}
	owner := s.beginPricingOperationDiagnostic("manual_refresh", "dispatch")
	s.dispatchUpdateEvent(updateout.Event{Type: "initial"}, time.Now().Add(-time.Second))
	select {
	case <-called:
		t.Fatal("startup dispatched while another operation owned permit")
	case <-time.After(25 * time.Millisecond):
	}
	if got := s.refreshDiagnosticStatus(true); got.Operation != "manual_refresh" || !got.Active {
		t.Fatalf("waiting operation replaced active owner: %+v", got)
	}
	owner.finish("complete", "", "")
	<-s.excelPricing.permit
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("startup did not finish after permit release")
		case <-tick.C:
			got := s.refreshDiagnosticStatus(false)
			if got.Operation == "startup_delivery" && !got.Active {
				if got.StageMS["source_prepare"] < 1000 {
					t.Fatalf("preparation missing: %+v", got)
				}
				if got.StageMS["permit_wait"] < 20 {
					t.Fatalf("missing actual wait: %+v", got)
				}
				if got.Code == "complete" {
					t.Fatalf("rejected dispatch reported complete: %+v", got)
				}
				select {
				case <-called:
				default:
					t.Fatal("dispatch never reached the rejecting receiver")
				}
				return
			}
		}
	}
}

func TestQueuedDeliveryTimingSeparatesWaitFromDispatch(t *testing.T) {
	s := &Server{}
	active := s.beginPricingOperationDiagnostic("manual_refresh", "dispatch")
	queuedAt := time.Now().Add(-2 * time.Second)
	if got := s.refreshDiagnosticStatus(true); got.Operation != "manual_refresh" {
		t.Fatalf("active owner was replaced before acquisition: %+v", got)
	}
	active.finish("complete", "", "")
	d := s.beginQueuedPricingOperationDiagnostic("startup_delivery", "dispatch", queuedAt)
	preparedAt := queuedAt.Add(-time.Second)
	d.includePreparation(preparedAt)
	d.finish("complete", "", "")
	got := s.refreshDiagnosticStatus(false)
	if got.Active || got.Code != "complete" || got.Operation != "startup_delivery" {
		t.Fatalf("incorrect terminal status: %+v", got)
	}
	if !got.StartedAt.Equal(preparedAt) || got.StageMS["source_prepare"] != 1000 || got.StageMS["permit_wait"] < 2000 {
		t.Fatalf("queue wait missing: %+v", got)
	}
	// Millisecond truncation can lose one millisecond across the two stages.
	delta := got.ElapsedMS - got.StageMS["source_prepare"] - got.StageMS["permit_wait"] - got.StageMS["dispatch"]
	if delta < 0 || delta > 1 {
		t.Fatalf("wait double-counted or absent from elapsed time: %+v", got)
	}
	if again := s.refreshDiagnosticStatus(false); again.ElapsedMS != got.ElapsedMS {
		t.Fatal("completed duration continued increasing")
	}
}

func TestQueuedDeliveryTimingPreservesZeroWait(t *testing.T) {
	s := &Server{}
	// A future input is clamped to acquisition time, deterministically exercising zero wait.
	d := s.beginQueuedPricingOperationDiagnostic("startup_delivery", "dispatch", time.Now().Add(time.Hour))
	d.finish("receipt_received", "", "")
	got := s.refreshDiagnosticStatus(false)
	wait, present := got.StageMS["permit_wait"]
	if !present || wait != 0 || got.ElapsedMS < 0 {
		t.Fatalf("zero wait missing or negative duration: %+v", got)
	}
}

func TestCancelledQueuedDeliveryPreservesActiveOperation(t *testing.T) {
	s := newCanonicalProjectionTestServer(t, "http://127.0.0.1:1", "1s", true, true)
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.backgroundCtx = ctx
	s.excelPricing.permit <- struct{}{}
	defer func() { <-s.excelPricing.permit }()
	s.beginPricingOperationDiagnostic("manual_refresh", "dispatch")
	s.dispatchUpdateEvent(updateout.Event{Type: "initial"}, time.Now())
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got := s.refreshDiagnosticStatus(true)
		if got.LastPreDispatchFailure != nil {
			if got.Operation != "manual_refresh" || !got.Active || got.LastPreDispatchFailure.Code != "operation_cancelled" || got.LastPreDispatchFailure.Stage != "permit_wait" {
				t.Fatalf("incorrect cancellation status: %+v", got)
			}
			if got.Dispatch != nil {
				t.Fatal("cancelled waiter dispatched")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("cancelled queued delivery has no failure status")
}

func TestStartupPreparationFailurePreservesActiveOperation(t *testing.T) {
	s := newCanonicalProjectionTestServer(t, "http://127.0.0.1:1", "1s", true, true)
	defer s.Close()
	s.dataSourceMu.Lock()
	original := s.dataSource
	s.dataSource = nil
	s.dataSourceMu.Unlock()
	defer func() { s.dataSourceMu.Lock(); s.dataSource = original; s.dataSourceMu.Unlock() }()
	s.beginPricingOperationDiagnostic("manual_refresh", "dispatch")
	s.dispatchInitialUpdate(context.Background())
	got := s.refreshDiagnosticStatus(true)
	failure := got.LastPreDispatchFailure
	if got.Operation != "manual_refresh" || !got.Active || got.Dispatch != nil || failure == nil {
		t.Fatalf("preparation failure replaced active status: %+v", got)
	}
	if failure.Operation != "startup_delivery" || failure.Stage != "source_prepare" || failure.Code != "source_prepare_failed" || failure.EndedAt.Before(failure.StartedAt) {
		t.Fatalf("incorrect preparation failure: %+v", failure)
	}
}
