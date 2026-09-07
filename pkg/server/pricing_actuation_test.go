package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

func pricingFixtureDelivery(input *canonical.Envelope, result updateout.DeliveryResult) updateout.DeliveryResult {
	status := "complete"
	if result.PendingProducts > 0 || result.DeferredProducts > 0 {
		status = "pending"
	}
	result.Delivery = &updateout.DeliveryReceipt{Status: status, EventID: result.EventID, Source: input.Source, InputSource: input.Source, OwnerCatalogRevision: input.Products[0].PricingCatalogRevision, PendingProducts: result.PendingProducts, DeferredProducts: result.DeferredProducts, DeferredMissing: result.DeferredMissing, DeferredAmbiguous: result.DeferredAmbiguous}
	return result
}

func pricingActuatorFixture(t *testing.T) (*pricingActuator, *atomic.Value, *atomic.Int32) {
	t.Helper()
	s := &Server{excelPricing: newExcelPricingState(), canonicalProjection: newCanonicalProjectionCache(), pricingPublication: newCanonicalProjectionCache()}
	a := newPricingActuator(s, filepath.Join(t.TempDir(), "config.pricing.json"))
	s.pricingActuation = a
	owner := &atomic.Value{}
	owner.Store(pricingcatalog.Resolution{Authority: "go", CatalogRevision: excelPricingRevisionForTest("owner-1")})
	calls := &atomic.Int32{}
	a.owner = func(context.Context) (pricingcatalog.Resolution, error) {
		return owner.Load().(pricingcatalog.Resolution), nil
	}
	a.input = func(context.Context) (*canonical.Envelope, error) {
		o := owner.Load().(pricingcatalog.Resolution)
		return &canonical.Envelope{Source: excelPricingRemoteTestSource(), EventID: excelPricingRevisionForTest(o.CatalogRevision), Products: []canonical.Product{{ProductCode: "P-1", PricingCatalogRevision: o.CatalogRevision}}}, nil
	}
	a.dispatch = func(_ context.Context, e *canonical.Envelope) (updateout.DeliveryResult, error) {
		calls.Add(1)
		return pricingFixtureDelivery(e, updateout.DeliveryResult{HTTPStatus: 200, Status: "accepted", EventID: e.EventID, Attempts: 1}), nil
	}
	a.receipt = func(context.Context, pricingActuationStatus) (*pricingDeliveryReceipt, error) { return nil, nil }
	a.project = func(context.Context, canonical.Source, pricingcatalog.Resolution) error {
		t.Error("unexpected PHP projection")
		return nil
	}
	return a, owner, calls
}

func awaitPricingPhase(t *testing.T, a *pricingActuator, phase string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if a.status().Phase == phase {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("phase=%+v, want %s", a.status(), phase)
}

func TestPricingOwnerEventDispatchesAndSuppressesFeedback(t *testing.T) {
	a, _, calls := pricingActuatorFixture(t)
	probes := make(chan struct{}, 4)
	readOwner := a.owner
	a.owner = func(ctx context.Context) (pricingcatalog.Resolution, error) {
		owner, err := readOwner(ctx)
		probes <- struct{}{}
		return owner, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	a.start(ctx, &wg)
	defer func() { cancel(); wg.Wait() }()
	source := excelPricingRemoteTestSource()
	revision := excelPricingRemoteRevision{Source: source, StateRevision: excelPricingRevisionForTest("state"), CatalogRevision: excelPricingRevisionForTest("report")}
	revision.ETag = `"` + revision.StateRevision + `"`
	if err := a.server.notifyExcelPricingRemoteRevisionChanged(revision); err != nil {
		t.Fatal(err)
	}
	awaitPricingPhase(t, a, "complete")
	<-probes
	// A different report revision caused by this very delivery must not trigger
	// another price writer when the actual owner catalog has not changed.
	revision.StateRevision = excelPricingRevisionForTest("feedback")
	revision.CatalogRevision = excelPricingRevisionForTest("new-report")
	revision.ETag = `"` + revision.StateRevision + `"`
	if err := a.server.notifyExcelPricingRemoteRevisionChanged(revision); err != nil {
		t.Fatal(err)
	}
	select {
	case <-probes:
	case <-time.After(3 * time.Second):
		t.Fatal("feedback was not processed")
	}
	cancel()
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("dispatches=%d", calls.Load())
	}
	var disk pricingActuationStatus
	data, err := os.ReadFile(a.path)
	if err != nil || json.Unmarshal(data, &disk) != nil || disk.Phase != "complete" || disk.EventID == "" {
		t.Fatalf("checkpoint=%+v err=%v", disk, err)
	}
}

func TestPricingNewOwnerWhileBusyReceivesFinalPass(t *testing.T) {
	a, owner, calls := pricingActuatorFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	a.dispatch = func(_ context.Context, e *canonical.Envelope) (updateout.DeliveryResult, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return pricingFixtureDelivery(e, updateout.DeliveryResult{HTTPStatus: 200, Status: "accepted", EventID: e.EventID, Attempts: 1}), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	a.start(ctx, &wg)
	defer func() { cancel(); wg.Wait() }()
	source := excelPricingRemoteTestSource()
	if err := a.enqueue(source); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no dispatch")
	}
	second := pricingcatalog.Resolution{Authority: "go", CatalogRevision: excelPricingRevisionForTest("owner-2")}
	owner.Store(second)
	for i := 0; i < 20; i++ {
		if err := a.enqueue(source); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st := a.status()
		if st.Phase == "complete" && st.OwnerRevision == second.CatalogRevision {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if st := a.status(); st.OwnerRevision != second.CatalogRevision || st.Phase != "complete" || calls.Load() != 2 {
		t.Fatalf("status=%+v calls=%d", st, calls.Load())
	}
}

func TestPricingUncertainDeliveryRecoversWithoutResending(t *testing.T) {
	for _, mode := range []string{"unknown", "pending", "complete", "advanced-event", "wrong-owner", "wrong-source"} {
		t.Run(mode, func(t *testing.T) {
			a, _, calls := pricingActuatorFixture(t)
			a.dispatch = func(context.Context, *canonical.Envelope) (updateout.DeliveryResult, error) {
				calls.Add(1)
				return updateout.DeliveryResult{}, errors.New("transport failed")
			}
			source := excelPricingRemoteTestSource()
			a.run(context.Background(), source)
			if a.status().Phase != "recovery_required" {
				t.Fatal(a.status())
			}
			restarted := newPricingActuator(a.server, a.path)
			restarted.owner = a.owner
			restarted.input = a.input
			restarted.dispatch = a.dispatch
			restarted.receipt = func(_ context.Context, st pricingActuationStatus) (*pricingDeliveryReceipt, error) {
				if mode == "unknown" {
					return nil, nil
				}
				r := &pricingDeliveryReceipt{EventID: st.EventID, Status: "complete", Source: st.Source, InputSource: st.Source, OwnerCatalogRevision: st.OwnerRevision}
				switch mode {
				case "pending":
					r.PendingProducts = 1
				case "advanced-event":
					r.EventID = excelPricingRevisionForTest("wrong")
				case "wrong-owner":
					r.OwnerCatalogRevision = excelPricingRevisionForTest("wrong")
				case "wrong-source":
					r.InputSource.Revision = excelPricingRevisionForTest("wrong")
				}
				return r, nil
			}
			restarted.run(context.Background(), source)
			if calls.Load() != 1 {
				t.Fatalf("uncertain mutation resent %d times", calls.Load())
			}
			want := "recovery_required"
			if mode == "complete" || mode == "advanced-event" {
				want = "complete"
			}
			if mode == "pending" {
				want = "delivery_pending"
			}
			if restarted.status().Phase != want {
				t.Fatal(restarted.status())
			}
			if mode == "advanced-event" && (restarted.status().Previous == nil || restarted.status().Previous.EventID != a.status().EventID) {
				t.Fatal("manual receipt lost displaced operation")
			}
		})
	}
}

func TestPricingNewOwnerSupersedesOldPendingOrUncertainOperation(t *testing.T) {
	for _, outcome := range []string{"deferred_missing", "uncertain"} {
		t.Run(outcome, func(t *testing.T) {
			a, owner, calls := pricingActuatorFixture(t)
			oldOwner := owner.Load().(pricingcatalog.Resolution).CatalogRevision
			var firstEvent string
			a.dispatch = func(_ context.Context, input *canonical.Envelope) (updateout.DeliveryResult, error) {
				attempt := calls.Add(1)
				if attempt == 1 {
					firstEvent = input.EventID
					if outcome == "uncertain" {
						return updateout.DeliveryResult{}, errors.New("lost receipt")
					}
					return pricingFixtureDelivery(input, updateout.DeliveryResult{HTTPStatus: 200, Status: "accepted", EventID: input.EventID, Attempts: 1, DeferredProducts: 1, DeferredMissing: 1}), nil
				}
				if input.EventID == firstEvent || input.Products[0].PricingCatalogRevision == oldOwner {
					t.Fatal("new owner retried old mutation")
				}
				return pricingFixtureDelivery(input, updateout.DeliveryResult{HTTPStatus: 200, Status: "accepted", EventID: input.EventID, Attempts: 1, DeferredProducts: 1, DeferredMissing: 1}), nil
			}
			a.receipt = func(context.Context, pricingActuationStatus) (*pricingDeliveryReceipt, error) {
				t.Fatal("new owner blocked on impossible old receipt")
				return nil, nil
			}
			source := excelPricingRemoteTestSource()
			a.run(context.Background(), source)
			previous := a.status()
			owner.Store(pricingcatalog.Resolution{Authority: "go", CatalogRevision: excelPricingRevisionForTest("new-owner")})
			a.run(context.Background(), source)
			st := a.status()
			if calls.Load() != 2 || st.Phase != "delivery_pending" || st.EventID == firstEvent || st.Previous == nil || st.Previous.Phase != "superseded" || st.Previous.EventID != firstEvent || st.Previous.Outcome != previous.Phase {
				t.Fatalf("supersession failed calls=%d status=%+v previous=%+v", calls.Load(), st, st.Previous)
			}
		})
	}
}

func TestPricingNewOwnerRejectsStaleDesiredInput(t *testing.T) {
	a, owner, calls := pricingActuatorFixture(t)
	oldInput, _ := a.input(context.Background())
	a.dispatch = func(context.Context, *canonical.Envelope) (updateout.DeliveryResult, error) {
		calls.Add(1)
		return updateout.DeliveryResult{}, errors.New("uncertain")
	}
	a.run(context.Background(), excelPricingRemoteTestSource())
	owner.Store(pricingcatalog.Resolution{Authority: "go", CatalogRevision: excelPricingRevisionForTest("new-owner")})
	a.input = func(context.Context) (*canonical.Envelope, error) { return oldInput, nil }
	a.run(context.Background(), excelPricingRemoteTestSource())
	if calls.Load() != 1 || a.status().Error != "owner_changed" {
		t.Fatalf("stale input sent calls=%d status=%+v", calls.Load(), a.status())
	}
}

func TestPricingManualRefreshAdvancesSourceWithoutResend(t *testing.T) {
	a, _, calls := pricingActuatorFixture(t)
	a.dispatch = func(context.Context, *canonical.Envelope) (updateout.DeliveryResult, error) {
		calls.Add(1)
		return updateout.DeliveryResult{}, errors.New("lost receipt")
	}
	source := excelPricingRemoteTestSource()
	a.run(context.Background(), source)
	old := a.status()
	desired, _ := a.input(context.Background())
	desired.Source.Revision = excelPricingRevisionForTest("manual-fresh-input")
	a.input = func(context.Context) (*canonical.Envelope, error) { return desired, nil }
	manualEvent := excelPricingRevisionForTest("manual-refresh-event")
	a.receipt = func(_ context.Context, probe pricingActuationStatus) (*pricingDeliveryReceipt, error) {
		if !probe.Source.SameIdentity(desired.Source) {
			t.Fatal("probe pinned obsolete source")
		}
		return &pricingDeliveryReceipt{Status: "complete", EventID: manualEvent, Source: desired.Source, InputSource: desired.Source, OwnerCatalogRevision: old.OwnerRevision}, nil
	}
	a.run(context.Background(), desired.Source)
	st := a.status()
	if calls.Load() != 1 || st.Phase != "complete" || st.EventID != manualEvent || !st.Source.SameIdentity(desired.Source) || st.Previous == nil || st.Previous.EventID != old.EventID {
		t.Fatalf("manual advancement failed calls=%d status=%+v", calls.Load(), st)
	}
}

func TestPricingReplayCurrentOwnerReceiptCannotCompleteOldOperation(t *testing.T) {
	a, _, _ := pricingActuatorFixture(t)
	a.dispatch = func(_ context.Context, input *canonical.Envelope) (updateout.DeliveryResult, error) {
		result := pricingFixtureDelivery(input, updateout.DeliveryResult{HTTPStatus: 200, Status: "replayed", EventID: input.EventID, Attempts: 1})
		result.Delivery.EventID = excelPricingRevisionForTest("current-receiver-event")
		result.Delivery.OwnerCatalogRevision = excelPricingRevisionForTest("current-receiver-owner")
		result.Delivery.Source.Revision = excelPricingRevisionForTest("current-receiver-source")
		result.Delivery.InputSource = result.Delivery.Source
		return result, nil
	}
	a.run(context.Background(), excelPricingRemoteTestSource())
	st := a.status()
	if st.Phase != "recovery_required" || st.Error != "delivery_receipt_unresolved" || st.Delivery == nil || st.Delivery.EventID == st.EventID {
		t.Fatalf("old operation falsely complete: %+v", st)
	}
}

func TestPricingAcknowledgmentWithoutActualReceiptCannotComplete(t *testing.T) {
	a, _, _ := pricingActuatorFixture(t)
	a.dispatch = func(_ context.Context, input *canonical.Envelope) (updateout.DeliveryResult, error) {
		return updateout.DeliveryResult{HTTPStatus: 200, Status: "accepted", EventID: input.EventID, Attempts: 1}, nil
	}
	a.run(context.Background(), excelPricingRemoteTestSource())
	if a.status().Phase != "recovery_required" {
		t.Fatal(a.status())
	}
}

func TestPricingPendingAndDeferredAreNotComplete(t *testing.T) {
	for _, mode := range []string{"pending", "missing", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			a, _, _ := pricingActuatorFixture(t)
			a.dispatch = func(_ context.Context, e *canonical.Envelope) (updateout.DeliveryResult, error) {
				r := updateout.DeliveryResult{HTTPStatus: 200, Status: "accepted", EventID: e.EventID, Attempts: 1}
				switch mode {
				case "pending":
					r.PendingProducts = 1
				case "missing":
					r.DeferredProducts = 1
					r.DeferredMissing = 1
				case "ambiguous":
					r.DeferredProducts = 1
					r.DeferredAmbiguous = 1
				}
				return pricingFixtureDelivery(e, r), nil
			}
			a.run(context.Background(), excelPricingRemoteTestSource())
			if a.status().Phase != "delivery_pending" {
				t.Fatal(a.status())
			}
		})
	}
}

func TestPricingPHPOwnerRefreshDoesNotDeliverInput(t *testing.T) {
	a, owner, calls := pricingActuatorFixture(t)
	owner.Store(pricingcatalog.Resolution{Authority: "php", CatalogRevision: excelPricingRevisionForTest("php-owner")})
	projects := 0
	a.project = func(context.Context, canonical.Source, pricingcatalog.Resolution) error { projects++; return nil }
	source := excelPricingRemoteTestSource()
	a.run(context.Background(), source)
	a.run(context.Background(), source)
	source.Revision = excelPricingRevisionForTest("new-final")
	a.run(context.Background(), source)
	if calls.Load() != 0 || projects != 2 || a.status().Phase != "complete" {
		t.Fatalf("dispatch=%d project=%d status=%+v", calls.Load(), projects, a.status())
	}
	a.project = func(context.Context, canonical.Source, pricingcatalog.Resolution) error {
		return errPricingProjectionUnavailable
	}
	source.Revision = excelPricingRevisionForTest("bad-final")
	a.run(context.Background(), source)
	if a.status().Phase != "recovery_required" || calls.Load() != 0 {
		t.Fatal(a.status())
	}
}

func TestPricingCheckpointFailureRejectsEvent(t *testing.T) {
	a, _, calls := pricingActuatorFixture(t)
	a.path = filepath.Join(t.TempDir(), "missing", "checkpoint.json")
	if a.enqueue(excelPricingRemoteTestSource()) == nil {
		t.Fatal("unpersisted event accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("unpersisted event dispatched")
	}
}

func TestRemoteSnapshotSchemaMetadataDoesNotGateSemanticProtocol(t *testing.T) {
	f := newExcelPricingRemoteSnapshotFixture(t, "ready")
	defer f.Close()
	f.revision.Schema = "pricing-sync-revision"
	f.revision.SchemaVersion = 0
	f.revision.ProjectionSchema = "unversioned"
	if _, err := f.Client(t).Collect(context.Background(), f.requestID, 0); err != nil {
		t.Fatal(err)
	}
}

func TestPricingReceiptRecoveryBindsSavedOwnerAfterNewOwnerSelection(t *testing.T) {
	f := newExcelPricingRemoteSnapshotFixture(t, "ready")
	defer f.Close()
	oldOwner := excelPricingRevisionForTest("old-owner")
	st := pricingActuationStatus{Source: f.source, EventID: excelPricingRevisionForTest("accepted-event"), OwnerRevision: oldOwner}
	f.revision.InputSource = &f.source
	f.revision.OwnerCatalogRevision = excelPricingRevisionForTest("new-owner")
	f.revision.Delivery = &pricingDeliveryReceipt{Status: "complete", EventID: st.EventID, Source: f.source, InputSource: f.source, OwnerCatalogRevision: oldOwner}
	client := f.Client(t)
	client.inputCatalogRevision = oldOwner
	client.receiptProbe = true
	revision, err := client.fetchRevision(context.Background())
	if err != nil || !receiptComplete(revision.Delivery, st) {
		t.Fatalf("saved receipt not reconciled: %+v %v", revision.Delivery, err)
	}
	client.receiptProbe = false
	if _, err := client.fetchRevision(context.Background()); err == nil {
		t.Fatal("final projection allowed old owner binding")
	}
}

func TestPricingReceiptMissingCountsCannotProveCompletion(t *testing.T) {
	var receipt pricingDeliveryReceipt
	if json.Unmarshal([]byte(`{"event_id":"unknown","status":"complete"}`), &receipt) == nil {
		t.Fatal("incomplete receipt accepted")
	}
}

func TestPricingCorruptCheckpointNeverDispatches(t *testing.T) {
	a, _, calls := pricingActuatorFixture(t)
	if err := os.WriteFile(a.path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	restarted := newPricingActuator(a.server, a.path)
	restarted.owner = a.owner
	restarted.input = a.input
	restarted.dispatch = a.dispatch
	restarted.run(context.Background(), excelPricingRemoteTestSource())
	if calls.Load() != 0 || restarted.status().Error != "checkpoint_invalid" {
		t.Fatal(restarted.status())
	}
}
