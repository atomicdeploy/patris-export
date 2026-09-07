package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

type pricingSnapshotTimingKey struct{}

type refreshDispatchDiagnostic struct {
	ElapsedMS        int64  `json:"elapsed_ms"`
	HTTPStatus       int    `json:"http_status"`
	Status           string `json:"status,omitempty"`
	Attempts         int    `json:"attempts"`
	PendingProducts  int    `json:"pending_products"`
	DeferredProducts int    `json:"deferred_products"`
	Retryable        bool   `json:"retryable"`
	Code             string `json:"code,omitempty"`
}

func refreshDispatchDetails(result updateout.DeliveryResult, err error, started time.Time) *refreshDispatchDiagnostic {
	d := &refreshDispatchDiagnostic{ElapsedMS: time.Since(started).Milliseconds(), HTTPStatus: result.HTTPStatus,
		Status: result.Status, Attempts: result.Attempts, PendingProducts: result.PendingProducts,
		DeferredProducts: result.DeferredProducts, Retryable: result.Retryable}
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

type pricingSnapshotTimingReport struct {
	ProjectionMS int64            `json:"projection_ms"`
	CollectMS    int64            `json:"collect_ms"`
	CollectRuns  int              `json:"collect_runs"`
	Failures     int              `json:"failures"`
	HTTPRequests int              `json:"http_requests"`
	StageMS      map[string]int64 `json:"stage_ms"`
}

// Request-local counters contain only fixed stage names and elapsed durations.
// Zero collection runs means a cached/shared build supplied this request.
type pricingSnapshotTiming struct {
	mu     sync.Mutex
	report pricingSnapshotTimingReport
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
	t.report.CollectMS += time.Since(started).Milliseconds()
	if failed {
		t.report.Failures++
	}
}

func (t *pricingSnapshotTiming) snapshot(started time.Time) *pricingSnapshotTimingReport {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.report
	r.ProjectionMS = time.Since(started).Milliseconds()
	r.StageMS = make(map[string]int64, len(t.report.StageMS))
	for stage, ms := range t.report.StageMS {
		r.StageMS[stage] = ms
	}
	return &r
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
