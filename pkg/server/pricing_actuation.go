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
type pricingDeliveryReceipt = updateout.DeliveryReceipt

type pricingActuationStatus struct {
	Previous      *pricingPreviousOperation `json:"previous_operation,omitempty"`
	Phase         string                    `json:"phase"`
	Authority     string                    `json:"authority,omitempty"`
	OwnerRevision string                    `json:"owner_catalog_revision,omitempty"`
	Source        canonical.Source          `json:"source"`
	EventID       string                    `json:"event_id,omitempty"`
	Error         string                    `json:"error,omitempty"`
	Pending       bool                      `json:"pending"`
	LatestSource  canonical.Source          `json:"latest_source"`
	UpdatedAt     time.Time                 `json:"updated_at"`
	Delivery      *pricingDeliveryReceipt   `json:"delivery,omitempty"`
}

// Retain one displaced operation's outcome, never an accumulating outbox.
type pricingPreviousOperation struct {
	Phase         string                  `json:"phase"`
	Outcome       string                  `json:"outcome"`
	OwnerRevision string                  `json:"owner_catalog_revision"`
	Source        canonical.Source        `json:"source"`
	EventID       string                  `json:"event_id"`
	Error         string                  `json:"error,omitempty"`
	Delivery      *pricingDeliveryReceipt `json:"delivery,omitempty"`
}

func supersededPricingOperation(st pricingActuationStatus) *pricingPreviousOperation {
	if st.EventID == "" {
		return st.Previous
	}
	return &pricingPreviousOperation{Phase: "superseded", Outcome: st.Phase, OwnerRevision: st.OwnerRevision, Source: st.Source, EventID: st.EventID, Error: st.Error, Delivery: st.Delivery}
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
		operationConfig := s.Config()
		cfg := updateout.Normalize(operationConfig.SendUpdates)
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
		ctx = s.pricingCommandContext(ctx, operationConfig, cfg)
		event := updateout.Event{Type: "update", Timestamp: time.Now().UTC().Format(time.RFC3339), Source: s.currentDBPath(), Contract: e, SnapshotContract: e}
		ackKey := s.sourceDeliveryKey(operationConfig, cfg)
		result, err := dispatch(ctx, cfg, event)
		s.recordSourceDeliveryAcknowledgement(ackKey, cfg, event, result, err)
		return result, err
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

func pricingWaitReceiptComplete(receipt *pricingDeliveryReceipt, input *canonical.Envelope, owner pricingcatalog.Resolution) bool {
	if receipt == nil || receipt.Status != "complete" || receipt.EventID != input.EventID || !receipt.InputSource.SameIdentity(input.Source) || receipt.Source.ID != input.Source.ID || receipt.Source.Dataset != input.Source.Dataset || receipt.PendingProducts != 0 || receipt.DeferredProducts != 0 || receipt.DeferredMissing != 0 || receipt.DeferredAmbiguous != 0 {
		return false
	}
	if owner.Authority == pricingcatalog.AuthorityGo {
		return receipt.Source.SameIdentity(input.Source) && receipt.OwnerCatalogRevision == owner.CatalogRevision
	}
	// PHP receipt proves accepted website writes; no optional snapshot is collected.
	return owner.Authority == pricingcatalog.AuthorityPHP
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
		// Website pricing is owned and published by PHP. Its optional return
		// snapshot must not start background work or hold the pricing permit.
		a.transition(func(st *pricingActuationStatus) {
			st.Phase = "idle"
			st.Authority = owner.Authority
			st.OwnerRevision = owner.CatalogRevision
			st.Source = source
			st.EventID = ""
			st.Error = ""
			st.Delivery = nil
		})
		return
	}

	if previous.OwnerRevision == owner.CatalogRevision && previous.Authority == owner.Authority && previous.Source.ID == source.ID && previous.Source.Dataset == source.Dataset {
		if previous.Phase == "complete" {
			return
		}
		if previous.EventID != "" {
			// A legitimate synchronous refresh can advance the receiver ledger.
			// Probe the authenticated current source, then bind its receipt to
			// freshly built desired input before adopting that existing operation.
			probe := previous
			probe.Source = source
			receipt, err := a.receipt(ctx, probe)
			if err != nil || receipt == nil {
				a.fail("delivery_receipt_unresolved")
				return
			}
			desired, err := a.input(ctx)
			if err != nil || desired == nil || !receipt.InputSource.SameIdentity(desired.Source) || !receipt.Source.SameIdentity(desired.Source) || receipt.OwnerCatalogRevision != owner.CatalogRevision || !isSHA256Revision(receipt.EventID) {
				a.fail("delivery_receipt_unresolved")
				return
			}
			for _, p := range desired.Products {
				if p.PricingCatalogRevision != owner.CatalogRevision {
					a.fail("owner_changed")
					return
				}
			}
			a.transition(func(st *pricingActuationStatus) {
				if receipt.EventID != previous.EventID {
					st.Previous = supersededPricingOperation(previous)
				}
				st.EventID = receipt.EventID
				st.Source = receipt.InputSource
				st.Delivery = receipt
				st.Error = ""
				if receiptComplete(receipt, *st) {
					st.Phase = "complete"
				} else {
					st.Phase = "delivery_pending"
				}
			})
			return
		}
	}
	// A committed new owner revision is a new operation. The receiver fences
	// old-owner deliveries before writes, so an old deferred/unknown receipt
	// must not prevent the new desired prices from reaching known products.
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
	if previous.EventID != "" && previous.OwnerRevision != owner.CatalogRevision && input.EventID == previous.EventID {
		a.fail("owner_changed")
		return
	}
	if !a.transition(func(st *pricingActuationStatus) {
		st.Previous = supersededPricingOperation(previous)
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
	receipt := result.Delivery
	a.transition(func(st *pricingActuationStatus) {
		st.Delivery = receipt
		if result.HTTPStatus >= 200 && result.HTTPStatus < 300 && receiptComplete(receipt, *st) {
			st.Phase = "complete"
		} else if receipt != nil && receipt.EventID == st.EventID && receipt.InputSource.SameIdentity(st.Source) && receipt.Source.SameIdentity(st.Source) && receipt.OwnerCatalogRevision == st.OwnerRevision {
			st.Phase = "delivery_pending"
		} else {
			st.Phase = "recovery_required"
			st.Error = "delivery_receipt_unresolved"
		}
	})
}
