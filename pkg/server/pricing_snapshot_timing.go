package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

type pricingSnapshotTimingKey struct{}

type refreshDispatchDiagnostic struct {
	ElapsedMS         int64                      `json:"elapsed_ms"`
	HTTPStatus        int                        `json:"http_status"`
	Status            string                     `json:"status,omitempty"`
	Attempts          int                        `json:"attempts"`
	PendingProducts   int                        `json:"pending_products"`
	DeferredProducts  int                        `json:"deferred_products"`
	DeferredMissing   int                        `json:"deferred_missing"`
	DeferredAmbiguous int                        `json:"deferred_ambiguous"`
	Retryable         bool                       `json:"retryable"`
	Code              string                     `json:"code,omitempty"`
	Delivery          *updateout.DeliveryReceipt `json:"delivery,omitempty"`
}

func refreshDispatchDetails(result updateout.DeliveryResult, err error, started time.Time) *refreshDispatchDiagnostic {
	d := &refreshDispatchDiagnostic{ElapsedMS: time.Since(started).Milliseconds(), HTTPStatus: result.HTTPStatus,
		Status: result.Status, Attempts: result.Attempts, PendingProducts: result.PendingProducts,
		DeferredProducts: result.DeferredProducts, DeferredMissing: result.DeferredMissing,
		DeferredAmbiguous: result.DeferredAmbiguous, Retryable: result.Retryable}
	if result.Delivery != nil {
		receipt := *result.Delivery
		d.Delivery = &receipt
	}
	var deliveryErr *updateout.DeliveryError
	if errors.As(err, &deliveryErr) {
		switch deliveryErr.Reason {
		case "request failed":
			d.Code = "request_failed"
		case "response read failed":
			d.Code = "response_read_failed"
		case "receiver event identity mismatch":
			d.Code = "receiver_identity_mismatch"
		case "receiver response is missing product-sync delivery state":
			d.Code = "receiver_state_missing"
		case "receiver response has inconsistent product-sync delivery state":
			d.Code = "receiver_state_invalid"
		case "receiver returned non-success HTTP status":
			d.Code = "receiver_http_status"
		case "invalid destination URL":
			d.Code = "invalid_destination"
		case "delivery cancelled":
			d.Code = "delivery_cancelled"
		default:
			d.Code = "receiver_rejected"
		}
	} else if err != nil {
		d.Code = "dispatch_failed"
	}
	return d
}

// Delivery telemetry binds the actual receiver ledger to this dispatch. A
// successful webhook without a ledger is not proof of completed website writes.
func backgroundDeliveryOutcome(result updateout.DeliveryResult, err error, input *canonical.Envelope) string {
	if err != nil {
		return "delivery_failed"
	}
	receipt := result.Delivery
	if input == nil || receipt == nil || receipt.EventID != input.EventID ||
		!receipt.InputSource.SameIdentity(input.Source) || receipt.Source.ID != input.Source.ID ||
		receipt.Source.Dataset != input.Source.Dataset || result.EventID != receipt.EventID ||
		result.HTTPStatus < 200 || result.HTTPStatus >= 300 || result.Attempts < 1 ||
		result.PendingProducts != receipt.PendingProducts || result.DeferredProducts != receipt.DeferredProducts ||
		result.DeferredMissing != receipt.DeferredMissing || result.DeferredAmbiguous != receipt.DeferredAmbiguous {
		return "delivery_receipt_unresolved"
	}
	if receipt.Status == "pending" && receipt.PendingProducts > 0 {
		return "delivery_pending"
	}
	if receipt.Status == "deferred" && receipt.PendingProducts == 0 && receipt.DeferredProducts > 0 {
		return "delivery_deferred"
	}
	if receipt.Status == "complete" && receipt.PendingProducts == 0 && receipt.DeferredProducts == 0 &&
		receipt.DeferredMissing == 0 && receipt.DeferredAmbiguous == 0 && excelPricingDeliveryComplete(result, input.EventID) {
		// Startup did not capture the pricing owner alongside its input. Preserve
		// the receiver's complete status without claiming current-owner validation.
		return "receipt_received"
	}
	return "delivery_receipt_unresolved"
}

type pricingSnapshotTimingReport struct {
	ActiveStage   string           `json:"active_stage,omitempty"`
	ActiveStageMS int64            `json:"active_stage_ms,omitempty"`
	ProjectionMS  int64            `json:"projection_ms"`
	CollectMS     int64            `json:"collect_ms"`
	CollectRuns   int              `json:"collect_runs"`
	Failures      int              `json:"failures"`
	HTTPRequests  int              `json:"http_requests"`
	StageMS       map[string]int64 `json:"stage_ms"`
}

// Request-local counters contain only fixed stage names and elapsed durations.
// Zero collection runs means a cached/shared build supplied this request.
type pricingSnapshotTiming struct {
	mu            sync.Mutex
	report        pricingSnapshotTimingReport
	activeStage   string
	activeStarted time.Time
}

func (t *pricingSnapshotTiming) active(stage string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.activeStage, t.activeStarted = stage, time.Now()
}

func (t *pricingSnapshotTiming) stage(name string, started time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.report.StageMS == nil {
		t.report.StageMS = make(map[string]int64)
	}
	t.report.StageMS[name] += time.Since(started).Milliseconds()
}

func (t *pricingSnapshotTiming) finish(started time.Time, failed bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.report.CollectRuns++
	t.activeStage = ""
	t.report.CollectMS += time.Since(started).Milliseconds()
	if failed {
		t.report.Failures++
	}
}

func (t *pricingSnapshotTiming) snapshot(started time.Time) *pricingSnapshotTimingReport {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.report
	if t.activeStage != "" {
		r.ActiveStage = t.activeStage
		r.ActiveStageMS = time.Since(t.activeStarted).Milliseconds()
	}
	r.ProjectionMS = time.Since(started).Milliseconds()
	r.StageMS = make(map[string]int64, len(t.report.StageMS))
	for stage, ms := range t.report.StageMS {
		r.StageMS[stage] = ms
	}
	return &r
}

// One bounded in-memory record survives a disconnected refresh caller. It is
// diagnostic state only; it never substitutes for a receiver delivery receipt.
type refreshOperationDiagnostic struct {
	mu                sync.Mutex
	started           time.Time
	phaseStarted      time.Time
	ended             time.Time
	operation         string
	phase             string
	code              string
	errorStage        string
	errorDetail       string
	dispatch          *refreshDispatchDiagnostic
	timing            *pricingSnapshotTiming
	projectionStarted time.Time
	snapshot          *pricingSnapshotTimingReport
}

type refreshOperationStatus struct {
	Busy           bool                         `json:"busy"`
	Operation      string                       `json:"operation,omitempty"`
	Phase          string                       `json:"phase,omitempty"`
	Active         bool                         `json:"active"`
	StartedAt      time.Time                    `json:"started_at,omitempty"`
	ElapsedMS      int64                        `json:"elapsed_ms"`
	PhaseElapsedMS int64                        `json:"phase_elapsed_ms"`
	Code           string                       `json:"code,omitempty"`
	ErrorStage     string                       `json:"error_stage,omitempty"`
	ErrorDetail    string                       `json:"error_detail,omitempty"`
	Dispatch       *refreshDispatchDiagnostic   `json:"dispatch_diagnostic,omitempty"`
	Snapshot       *pricingSnapshotTimingReport `json:"snapshot_timing,omitempty"`
}

func (s *Server) beginRefreshDiagnostic() *refreshOperationDiagnostic {
	return s.beginPricingOperationDiagnostic("manual_refresh", "owner_inputs")
}

func (s *Server) beginPricingOperationDiagnostic(operation, phase string) *refreshOperationDiagnostic {
	now := time.Now()
	d := &refreshOperationDiagnostic{started: now, phaseStarted: now, operation: operation, phase: phase}
	s.refreshDiagnosticMu.Lock()
	s.refreshDiagnostic = d
	s.refreshDiagnosticMu.Unlock()
	return d
}

func (d *refreshOperationDiagnostic) phaseChanged(phase string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.phase, d.phaseStarted = phase, time.Now()
}

func (d *refreshOperationDiagnostic) finish(code, stage, detail string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ended, d.code, d.errorStage, d.errorDetail = time.Now(), code, stage, detail
}

func (s *Server) refreshDiagnosticStatus(busy bool) refreshOperationStatus {
	s.refreshDiagnosticMu.Lock()
	d := s.refreshDiagnostic
	s.refreshDiagnosticMu.Unlock()
	r := refreshOperationStatus{Busy: busy}
	if d == nil {
		return r
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	end := d.ended
	if end.IsZero() {
		end = time.Now()
		r.Active = true
	}
	r.Phase, r.StartedAt, r.ElapsedMS, r.PhaseElapsedMS = d.phase, d.started, end.Sub(d.started).Milliseconds(), end.Sub(d.phaseStarted).Milliseconds()
	r.Operation = d.operation
	r.Code, r.ErrorStage, r.ErrorDetail, r.Dispatch, r.Snapshot = d.code, d.errorStage, d.errorDetail, d.dispatch, d.snapshot
	if r.Active && d.timing != nil {
		r.Snapshot = d.timing.snapshot(d.projectionStarted)
	}
	return r
}

func (client *excelPricingRemoteSnapshotClient) timedRequest(request *http.Request) (*http.Response, error) {
	if client.timing != nil {
		client.timing.mu.Lock()
		client.timing.report.HTTPRequests++
		client.timing.mu.Unlock()
	}
	return client.client.Do(request)
}

func snapshotTimingFromContext(ctx context.Context) *pricingSnapshotTiming {
	if ctx == nil {
		return nil
	}
	timing, _ := ctx.Value(pricingSnapshotTimingKey{}).(*pricingSnapshotTiming)
	return timing
}
