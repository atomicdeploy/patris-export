package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

// This is a bounded operation checkpoint, not a second product outbox. An
// uncertain delivery can only advance through the receiver's existing ledger.
type pricingDeliveryReceipt struct {
	EventID              string           `json:"event_id"`
	Status               string           `json:"status"`
	Source               canonical.Source `json:"source"`
	InputSource          canonical.Source `json:"input_source"`
	OwnerCatalogRevision string           `json:"owner_catalog_revision"`
	PendingProducts      int              `json:"pending_products"`
	DeferredProducts     int              `json:"deferred_products"`
	DeferredMissing      int              `json:"deferred_missing"`
	DeferredAmbiguous    int              `json:"deferred_ambiguous"`
}

func (r *pricingDeliveryReceipt) UnmarshalJSON(data []byte) error {
	type wireReceipt pricingDeliveryReceipt
	var wire wireReceipt
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &wire) != nil || json.Unmarshal(data, &fields) != nil {
		return errExcelPricingRemoteSnapshotProtocol
	}
	for _, key := range []string{"event_id", "status", "source", "input_source", "pending_products", "deferred_products", "deferred_missing", "deferred_ambiguous"} {
		value, ok := fields[key]
		if !ok || string(value) == "null" {
			return errExcelPricingRemoteSnapshotProtocol
		}
	}
	if !isSHA256Revision(wire.EventID) || !validExcelPricingRemoteSource(wire.Source) || !validExcelPricingRemoteSource(wire.InputSource) || wire.PendingProducts < 0 || wire.DeferredProducts < 0 || wire.DeferredMissing < 0 || wire.DeferredAmbiguous < 0 {
		return errExcelPricingRemoteSnapshotProtocol
	}
	*r = pricingDeliveryReceipt(wire)
	return nil
}

type pricingActuationStatus struct {
	Phase         string                  `json:"phase"`
	Authority     string                  `json:"authority,omitempty"`
	OwnerRevision string                  `json:"owner_catalog_revision,omitempty"`
	Source        canonical.Source        `json:"source"`
	EventID       string                  `json:"event_id,omitempty"`
	Error         string                  `json:"error,omitempty"`
	Pending       bool                    `json:"pending"`
	LatestSource  canonical.Source        `json:"latest_source"`
	UpdatedAt     time.Time               `json:"updated_at"`
	Delivery      *pricingDeliveryReceipt `json:"delivery,omitempty"`
}

type pricingActuator struct {
	mu     sync.Mutex
	state  pricingActuationStatus
	path   string
	wake   chan struct{}
	server *Server
	// Narrow boundaries allow deterministic concurrency and restart tests.
	owner    func(context.Context) (pricingcatalog.Resolution, error)
	input    func(context.Context) (*canonical.Envelope, error)
	dispatch func(context.Context, *canonical.Envelope) (updateout.DeliveryResult, error)
	receipt  func(context.Context, pricingActuationStatus) (*pricingDeliveryReceipt, error)
	project  func(context.Context, canonical.Source, pricingcatalog.Resolution) error
}

func newPricingActuator(s *Server, path string) *pricingActuator {
	a := &pricingActuator{server: s, path: path, wake: make(chan struct{}, 1), state: pricingActuationStatus{Phase: "idle"}}
	if data, err := os.ReadFile(path); err == nil {
		a.state = pricingActuationStatus{}
		if len(data) > 65536 || json.Unmarshal(data, &a.state) != nil || !validPricingCheckpoint(a.state) {
			a.state = pricingActuationStatus{Phase: "recovery_required", Error: "checkpoint_invalid"}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		a.state.Phase = "recovery_required"
		a.state.Error = "checkpoint_unreadable"
	}
	a.owner = func(ctx context.Context) (pricingcatalog.Resolution, error) {
		s.invalidateCanonicalProjection(true)
		return s.selectedPricingOwner(ctx, s.Config())
	}
	a.input = func(ctx context.Context) (*canonical.Envelope, error) {
		return s.excelPricingCanonical(ctx, s.Config())
	}
	a.dispatch = func(ctx context.Context, e *canonical.Envelope) (updateout.DeliveryResult, error) {
		cfg := updateout.Normalize(s.Config().SendUpdates)
		if !cfg.Enabled || cfg.Format != "json" || cfg.Method != "POST" {
			return updateout.DeliveryResult{}, errPricingAuthorityUnavailable
		}
		if _, err := updateout.ResolveProductSyncSecret(cfg); err != nil {
			return updateout.DeliveryResult{}, err
		}
		dispatch := s.excelPricing.dispatch
		if dispatch == nil {
			dispatch = updateout.DispatchWithResult
		}
		return dispatch(ctx, cfg, updateout.Event{Type: "update", Timestamp: time.Now().UTC().Format(time.RFC3339), Source: s.currentDBPath(), Contract: e, SnapshotContract: e})
	}
	a.receipt = func(ctx context.Context, st pricingActuationStatus) (*pricingDeliveryReceipt, error) {
		client, err := newExcelPricingRemoteSnapshotClient(s.Config().SendUpdates, st.Source, excelPricingRemoteSnapshotClientOptions{HTTPClient: s.excelPricing.client, Terminals: s.excelPricingRemote.snapshotTerminals(), InputCatalogRevision: st.OwnerRevision})
		if err != nil {
			return nil, err
		}
		// A newer owner may be current while the ledger still proves the exact
		// previous accepted event. The receipt itself must match the saved owner.
		client.receiptProbe = true
		revision, err := client.fetchRevision(ctx)
		return revision.Delivery, err
	}
	a.project = func(ctx context.Context, source canonical.Source, owner pricingcatalog.Resolution) error {
		_, err := s.pricingPublication.get(ctx, func() time.Duration { return canonicalProjectionMaxAge(s.Config()) }, func(ctx context.Context) (recordpipe.Result, error) {
			input, err := s.canonicalRecordResultContext(ctx)
			if err != nil {
				return recordpipe.Result{}, err
			}
			return s.projectPricingFinal(ctx, input, source, owner)
		})
		return err
	}
	return a
}

func validPricingCheckpoint(st pricingActuationStatus) bool {
	switch st.Phase {
	case "idle", "recovery_required":
		return true
	case "projecting", "dispatching", "delivery_pending", "complete":
		return validExcelPricingRemoteSource(st.Source) && isSHA256Revision(st.OwnerRevision) &&
			((st.Authority == pricingcatalog.AuthorityGo && isSHA256Revision(st.EventID)) || st.Authority == pricingcatalog.AuthorityPHP)
	default:
		return false
	}
}

func (a *pricingActuator) status() pricingActuationStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *pricingActuator) saveLocked() error {
	a.state.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(a.state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(a.path), ".pricing-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, a.path)
	}
	return err
}

func (a *pricingActuator) enqueue(source canonical.Source) error {
	a.mu.Lock()
	a.state.Pending = true
	a.state.LatestSource = source
	err := a.saveLocked()
	a.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case a.wake <- struct{}{}:
	default:
	}
	return nil
}

func (a *pricingActuator) start(ctx context.Context, wg *sync.WaitGroup) {
	if a == nil {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		st := a.status()
		if validExcelPricingRemoteSource(st.LatestSource) || st.Pending || st.Phase == "dispatching" || st.Phase == "delivery_pending" || st.Phase == "recovery_required" {
			select {
			case a.wake <- struct{}{}:
			default:
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.wake:
			}
			for {
				a.mu.Lock()
				source := a.state.LatestSource
				a.state.Pending = false
				err := a.saveLocked()
				a.mu.Unlock()
				if err != nil {
					a.fail("checkpoint_write_failed")
					break
				}
				select {
				case a.server.excelPricing.permit <- struct{}{}:
				case <-ctx.Done():
					return
				}
				runCtx, cancel := context.WithTimeout(ctx, refreshWaitTimeout)
				a.run(runCtx, source)
				cancel()
				<-a.server.excelPricing.permit
				settled := a.status()
				if !settled.Pending {
					if settled.Phase == "complete" {
						a.server.broadcastMessage(map[string]interface{}{"type": "pricing_progress", "pricing": settled})
					}
					break
				}
			}
		}
	}()
}

func (a *pricingActuator) transition(update func(*pricingActuationStatus)) bool {
	a.mu.Lock()
	update(&a.state)
	err := a.saveLocked()
	if err != nil {
		a.state.Error = "checkpoint_write_failed"
		a.state.Phase = "recovery_required"
	}
	st := a.state
	a.mu.Unlock()
	if a.server != nil && st.Phase != "complete" {
		a.server.broadcastMessage(map[string]interface{}{"type": "pricing_progress", "pricing": st})
	}
	return err == nil
}

func (a *pricingActuator) fail(code string) {
	a.transition(func(st *pricingActuationStatus) { st.Phase = "recovery_required"; st.Error = code })
}

func receiptComplete(r *pricingDeliveryReceipt, st pricingActuationStatus) bool {
	return r != nil && r.Status == "complete" && r.EventID == st.EventID && r.InputSource.SameIdentity(st.Source) &&
		r.Source.SameIdentity(st.Source) && r.OwnerCatalogRevision == st.OwnerRevision &&
		r.PendingProducts == 0 && r.DeferredProducts == 0 && r.DeferredMissing == 0 && r.DeferredAmbiguous == 0
}

func (a *pricingActuator) run(ctx context.Context, source canonical.Source) {
	initial := a.status()
	if initial.Error == "checkpoint_invalid" || initial.Error == "checkpoint_unreadable" {
		return
	}
	owner, err := a.owner(ctx)
	if err != nil {
		a.fail("owner_unavailable")
		return
	}
	if !isSHA256Revision(owner.CatalogRevision) {
		a.fail("owner_revision_missing")
		return
	}
	previous := a.status()
	if previous.Error == "checkpoint_invalid" || previous.Error == "checkpoint_unreadable" {
		return
	}
	if owner.Authority == pricingcatalog.AuthorityPHP {
		if previous.Phase == "complete" && previous.Authority == owner.Authority && previous.OwnerRevision == owner.CatalogRevision && previous.Source.SameIdentity(source) {
			return
		}
		if !a.transition(func(st *pricingActuationStatus) {
			st.Phase = "projecting"
			st.Authority = owner.Authority
			st.OwnerRevision = owner.CatalogRevision
			st.Source = source
			st.EventID = ""
			st.Error = ""
			st.Delivery = nil
		}) {
			return
		}
		if err := a.project(ctx, source, owner); err != nil {
			a.fail("owner_projection_unavailable")
			return
		}
		a.transition(func(st *pricingActuationStatus) { st.Phase = "complete" })
		return
	}
	if previous.OwnerRevision == owner.CatalogRevision && previous.Authority == owner.Authority && previous.Source.ID == source.ID && previous.Source.Dataset == source.Dataset {
		if previous.Phase == "complete" {
			return
		}
		if previous.EventID != "" {
			receipt, err := a.receipt(ctx, previous)
			if err != nil || !receiptComplete(receipt, previous) {
				a.fail("delivery_receipt_unresolved")
				return
			}
			a.transition(func(st *pricingActuationStatus) { st.Phase = "complete"; st.Error = ""; st.Delivery = receipt })
			return
		}
	}
	// Do not supersede an uncertain mutation with a new owner write. Reconcile
	// the previous receipt first; an absent receipt is never permission to resend.
	if previous.EventID != "" && previous.Phase != "complete" {
		receipt, err := a.receipt(ctx, previous)
		if err != nil || !receiptComplete(receipt, previous) {
			a.fail("delivery_receipt_unresolved")
			return
		}
	}
	input, err := a.input(ctx)
	if err != nil || input == nil || input.Source.ID != source.ID || input.Source.Dataset != source.Dataset {
		a.fail("input_unavailable")
		return
	}
	for _, p := range input.Products {
		if p.PricingCatalogRevision != owner.CatalogRevision {
			a.fail("owner_changed")
			return
		}
	}
	if !a.transition(func(st *pricingActuationStatus) {
		st.Phase = "dispatching"
		st.Authority = owner.Authority
		st.OwnerRevision = owner.CatalogRevision
		st.Source = input.Source
		st.EventID = input.EventID
		st.Error = ""
		st.Delivery = nil
	}) {
		return
	}
	result, err := a.dispatch(ctx, input)
	if err != nil || result.EventID != input.EventID {
		a.fail("delivery_uncertain")
		return
	}
	receipt := &pricingDeliveryReceipt{EventID: result.EventID, Status: result.Status, Source: input.Source, InputSource: input.Source, OwnerCatalogRevision: owner.CatalogRevision, PendingProducts: result.PendingProducts, DeferredProducts: result.DeferredProducts, DeferredMissing: result.DeferredMissing, DeferredAmbiguous: result.DeferredAmbiguous}
	if excelPricingDeliveryComplete(result, input.EventID) && result.DeferredProducts == 0 && result.DeferredMissing == 0 {
		receipt.Status = "complete"
	}
	a.transition(func(st *pricingActuationStatus) {
		st.Delivery = receipt
		if result.HTTPStatus >= 200 && result.HTTPStatus < 300 && receiptComplete(receipt, *st) {
			st.Phase = "complete"
		} else {
			st.Phase = "delivery_pending"
		}
	})
}
