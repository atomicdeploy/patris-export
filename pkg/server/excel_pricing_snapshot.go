package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
	"github.com/gorilla/mux"
)

const (
	excelPricingSnapshotRequestSchema     = "patris.pricing-snapshot-request/v1"
	excelPricingSnapshotJobSchema         = "patris.pricing-snapshot-job/v1"
	excelPricingSnapshotFailureSchema     = "patris.pricing-snapshot-failure/v1"
	excelPricingSnapshotPayloadSchema     = "patris.pricing-snapshot/v1"
	excelPricingSnapshotProjectionFull    = "full"
	excelPricingSnapshotProjectionExcelV1 = "excel-v1"

	excelPricingSnapshotStageRemoteConfiguration = "remote_configuration"
	excelPricingSnapshotStageLocalProjection     = "local_projection"
	excelPricingSnapshotEventSchema              = "patris.pricing-state-event/v1"

	excelPricingSnapshotPageSize     = 250
	excelPricingSnapshotMaxPages     = 8
	excelPricingSnapshotRetryAfterMS = 1000
	// Product rows are cached only in this process and are reusable only while
	// the authenticated event bridge verifies the exact source/state/catalog
	// tuple. Keeping that revision-fenced projection for a workday prevents an
	// empty production template from rebuilding the same 1,000+ row snapshot on
	// every open; an event, reconnect gap, or process restart still fails closed.
	excelPricingSnapshotMaxCacheAge      = 24 * time.Hour
	excelPricingSnapshotRevisionProbeTTL = 5 * time.Second
	excelPricingSnapshotRetention        = 15 * time.Minute
	excelPricingSnapshotOperationTimeout = 8 * time.Minute
	excelPricingSnapshotHeartbeat        = time.Second
	excelPricingSnapshotHeartbeatStale   = 5 * time.Second
	excelPricingSnapshotSSEHeartbeat     = 15 * time.Second
	excelPricingSnapshotEventHistory     = 256
	// WinHTTP COM can buffer small chunks on an open response instead of
	// raising OnResponseDataAvailable. Prefix each semantic SSE frame with an
	// ignored comment large enough to cross that client-side delivery boundary.
	excelPricingSnapshotSSEFlushPadding = 4096
)

type excelPricingSnapshotStartRequest struct {
	Schema                string           `json:"schema"`
	SchemaVersion         int              `json:"schema_version"`
	ClientID              string           `json:"client_id"`
	Channel               string           `json:"channel"`
	RequestID             string           `json:"request_id"`
	Source                canonical.Source `json:"source"`
	Locale                string           `json:"locale"`
	Projection            string           `json:"projection,omitempty"`
	MaxAgeSeconds         int              `json:"max_age_seconds,omitempty"`
	ExpectedStateRevision string           `json:"expected_state_revision,omitempty"`
}

type excelPricingSnapshotProgress struct {
	Phase          string    `json:"phase"`
	CompletedPages int       `json:"completed_pages"`
	TotalPages     int       `json:"total_pages"`
	Rows           int       `json:"rows"`
	HeartbeatAt    time.Time `json:"heartbeat_at"`
}

// excelPricingSnapshotFailure is intentionally smaller than the internal
// error chain. It exposes only reviewed enum-like values, so a status or SSE
// response cannot disclose credentials, routes, identifiers, response bodies,
// or row data while still distinguishing the failed remote stage.
type excelPricingSnapshotFailure struct {
	Schema string `json:"schema"`
	Stage  string `json:"stage"`
	Code   string `json:"code"`
}

type excelPricingSnapshotIntegrity struct {
	Algorithm             string `json:"algorithm"`
	StateDigest           string `json:"state_digest"`
	CatalogMetadataDigest string `json:"catalog_metadata_digest"`
	PageRevisionsDigest   string `json:"page_revisions_digest"`
	WarningsDigest        string `json:"warnings_digest"`
	DatasetRevision       string `json:"dataset_revision"`
	RemoteTotal           int    `json:"remote_total"`
	RowCount              int    `json:"row_count"`
	DistinctSyncKeys      int    `json:"distinct_sync_keys"`
	PageCount             int    `json:"page_count"`
	WarningCount          int    `json:"warning_count"`
}

type excelPricingSnapshotCapacity struct {
	PageSize int `json:"page_size"`
	MaxPages int `json:"max_pages"`
	MaxRows  int `json:"max_rows"`
}

type excelPricingSnapshotMutationOperation struct {
	Method                 string `json:"method"`
	Path                   string `json:"path"`
	RequiresIdempotencyKey bool   `json:"requires_idempotency_key"`
	RequiresIfMatch        bool   `json:"requires_if_match"`
	Confirmation           string `json:"confirmation,omitempty"`
}

type excelPricingSnapshotMutationGuard struct {
	ExpectedStateRevision string                                `json:"expected_state_revision"`
	Preview               excelPricingSnapshotMutationOperation `json:"preview"`
	Apply                 excelPricingSnapshotMutationOperation `json:"apply"`
}

type excelPricingSnapshotPayload struct {
	Schema           string                            `json:"schema"`
	Projection       string                            `json:"projection"`
	RowFields        []string                          `json:"row_fields,omitempty"`
	SnapshotRevision string                            `json:"snapshot_revision"`
	Source           canonical.Source                  `json:"source"`
	StateRevision    string                            `json:"state_revision"`
	CreatedAt        time.Time                         `json:"created_at"`
	ExpiresAt        time.Time                         `json:"expires_at"`
	Integrity        excelPricingSnapshotIntegrity     `json:"integrity"`
	MutationGuard    excelPricingSnapshotMutationGuard `json:"mutation_guard"`
	State            json.RawMessage                   `json:"state"`
}

type excelPricingSnapshot struct {
	revision                string
	etagRevision            string
	stateRevision           string
	pricingStateRevision    string
	upstreamCatalogRevision string
	createdAt               time.Time
	expiresAt               time.Time
	integrity               excelPricingSnapshotIntegrity
	body                    []byte
}

type excelPricingSnapshotCollector func(
	context.Context,
	string,
	excelPricingSnapshotStartRequest,
	updateout.Config,
) (*excelPricingSnapshot, string)

type excelPricingSnapshotCacheAttestation struct {
	current bool
	drift   *excelPricingRemoteRevision
	err     error
}

func excelPricingSnapshotCacheLocallyAllowed(
	request excelPricingSnapshotStartRequest,
	cached *excelPricingSnapshot,
	now time.Time,
) bool {
	if cached == nil {
		return false
	}
	if request.ExpectedStateRevision != "" {
		return request.ExpectedStateRevision == cached.stateRevision
	}
	age := now.Sub(cached.createdAt)
	return request.MaxAgeSeconds > 0 && age >= 0 &&
		age <= time.Duration(request.MaxAgeSeconds)*time.Second
}

func (s *Server) attestExcelPricingSnapshotCache(
	ctx context.Context,
	cfg updateout.Config,
	source canonical.Source,
	cacheKey string,
	cached *excelPricingSnapshot,
) excelPricingSnapshotCacheAttestation {
	if cached == nil {
		return excelPricingSnapshotCacheAttestation{err: errExcelPricingRemoteRevision}
	}
	if current := s.excelPricing.snapshotRevisionCurrent; current != nil &&
		current(source, cached.stateRevision, cached.upstreamCatalogRevision) {
		return excelPricingSnapshotCacheAttestation{current: true}
	}
	probeContext, cancelProbe := context.WithTimeout(
		ctx,
		excelPricingSnapshotRevisionProbeTTL,
	)
	remoteRevision, err := s.probeExcelPricingRemoteRevisionCoalesced(
		probeContext,
		cfg,
		source,
		cacheKey,
	)
	cancelProbe()
	if err != nil ||
		!validExcelPricingRemoteRevisionParts(
			remoteRevision.StateRevision,
			remoteRevision.CatalogRevision,
			remoteRevision.PricingStateRevision,
			remoteRevision.PricingPolicyRevision,
		) ||
		!isStrongExcelPricingRevisionETag(
			remoteRevision.ETag,
			remoteRevision.StateRevision,
		) {
		if err == nil {
			err = errExcelPricingRemoteRevision
		}
		return excelPricingSnapshotCacheAttestation{err: err}
	}
	if remoteRevision.StateRevision == cached.stateRevision &&
		remoteRevision.CatalogRevision == cached.upstreamCatalogRevision {
		return excelPricingSnapshotCacheAttestation{current: true}
	}
	drift := excelPricingRemoteRevision{
		Source:                source,
		StateRevision:         remoteRevision.StateRevision,
		CatalogRevision:       remoteRevision.CatalogRevision,
		PricingStateRevision:  remoteRevision.PricingStateRevision,
		PricingPolicyRevision: remoteRevision.PricingPolicyRevision,
		ETag:                  remoteRevision.ETag,
		ValidationOrigin:      "snapshot_cache_revision_probe",
	}
	return excelPricingSnapshotCacheAttestation{drift: &drift}
}

type excelPricingStateChangeEvent struct {
	Schema                   string                        `json:"schema"`
	Sequence                 uint64                        `json:"sequence"`
	Kind                     string                        `json:"kind"`
	Reason                   string                        `json:"reason,omitempty"`
	OccurredAt               time.Time                     `json:"occurred_at"`
	JobID                    string                        `json:"job_id,omitempty"`
	Source                   *canonical.Source             `json:"source,omitempty"`
	SourceChangeToken        string                        `json:"source_change_token,omitempty"`
	CatalogRevision          string                        `json:"catalog_revision,omitempty"`
	StateRevision            string                        `json:"state_revision,omitempty"`
	PricingStateRevision     string                        `json:"pricing_state_revision,omitempty"`
	PricingPolicyRevision    string                        `json:"pricing_policy_revision,omitempty"`
	SnapshotRevision         string                        `json:"snapshot_revision,omitempty"`
	ETag                     string                        `json:"etag,omitempty"`
	PreviousSource           *canonical.Source             `json:"previous_source,omitempty"`
	PreviousCatalogRevision  string                        `json:"previous_catalog_revision,omitempty"`
	PreviousStateRevision    string                        `json:"previous_state_revision,omitempty"`
	PreviousSnapshotRevision string                        `json:"previous_snapshot_revision,omitempty"`
	PreviousETag             string                        `json:"previous_etag,omitempty"`
	Stale                    bool                          `json:"stale"`
	Verified                 bool                          `json:"verified"`
	Identity                 *excelPricingSnapshotIdentity `json:"identity,omitempty"`
	InvalidatedIdentity      *excelPricingSnapshotIdentity `json:"invalidated_identity,omitempty"`
}

type excelPricingSnapshotIdentity struct {
	Source           canonical.Source `json:"source"`
	CatalogRevision  string           `json:"catalog_revision"`
	StateRevision    string           `json:"state_revision"`
	SnapshotRevision string           `json:"snapshot_revision"`
	ETag             string           `json:"etag"`
}

type excelPricingSnapshotJob struct {
	id                    string
	owner                 [sha256.Size]byte
	requestID             string
	fingerprint           string
	cacheKey              string
	status                string
	createdAt             time.Time
	updatedAt             time.Time
	deadline              time.Time
	progress              excelPricingSnapshotProgress
	errorCode             string
	failure               *excelPricingSnapshotFailure
	cached                bool
	coalesced             bool
	leaderJobID           string
	cancel                context.CancelFunc
	snapshot              *excelPricingSnapshot
	source                canonical.Source
	locale                string
	projection            string
	expectedStateRevision string
	createdSequence       uint64
	eventSequence         uint64
	eventWatchers         int
	startGeneration       uint64
}

var excelPricingSnapshotExcelV1RowFields = []string{
	"sync_key",
	"reconciliation_status",
	"patris_code",
	"woocommerce_id",
	"sku",
	"weight_grams",
	"foreign_price",
	"patris_location",
	"categories",
	"foreign_currency",
	"shipping_price_per_kg",
	"shipping_price_per_kg_currency",
	"profit_margin_percent",
	"price_source_amount",
	"price_source_currency",
	"price_source_kind",
	"effective_price",
	"patris_total_stock",
	"stock_quantity",
	"name",
	"updated_at",
	"record_revision",
	"permalink",
	"patris_final_price",
	"sale_price",
	"publication_status",
	"image_url",
}

type excelPricingSnapshotStore struct {
	mu                   sync.Mutex
	now                  func() time.Time
	jobs                 map[string]*excelPricingSnapshotJob
	idempotency          map[string]string
	cache                map[string]*excelPricingSnapshot
	activeJobID          string
	changed              chan struct{}
	eventSequence        uint64
	latestChange         *excelPricingStateChangeEvent
	events               []excelPricingSnapshotEventEnvelope
	droppedEventSequence uint64
	generation           uint64
}

type excelPricingSnapshotEventEnvelope struct {
	Schema     string                        `json:"schema"`
	Sequence   uint64                        `json:"sequence"`
	Kind       string                        `json:"kind"`
	OccurredAt time.Time                     `json:"occurred_at"`
	Job        map[string]interface{}        `json:"job,omitempty"`
	Change     *excelPricingStateChangeEvent `json:"change,omitempty"`
	jobID      string
	owner      [sha256.Size]byte
}

type excelPricingSnapshotReconciliationCounts struct {
	PatrisProducts          int `json:"patris_products"`
	WooCommerceRaw          int `json:"woocommerce_raw"`
	WooCommerceLeaves       int `json:"woocommerce_leaves"`
	UnionRows               int `json:"union_rows"`
	Matched                 int `json:"matched"`
	PatrisOnly              int `json:"patris_only"`
	WooOnly                 int `json:"woo_only"`
	AmbiguousCodes          int `json:"ambiguous_codes"`
	VariableParentsExcluded int `json:"variable_parents_excluded"`
}

type excelPricingSnapshotPage struct {
	state                 map[string]json.RawMessage
	catalog               map[string]json.RawMessage
	rows                  []json.RawMessage
	stateRevision         string
	datasetRevision       string
	pageRevision          string
	catalogMetadataDigest string
	warningsDigest        string
	warningCount          int
	reconciliationCounts  excelPricingSnapshotReconciliationCounts
	page                  int
	limit                 int
	total                 int
	pages                 int
	hasMore               bool
}

type excelPricingSnapshotPagination struct {
	Page    int  `json:"page"`
	Limit   int  `json:"limit"`
	Total   int  `json:"total"`
	Pages   int  `json:"pages"`
	HasMore bool `json:"has_more"`
}

func newExcelPricingSnapshotStore(now func() time.Time) *excelPricingSnapshotStore {
	if now == nil {
		now = time.Now
	}
	return &excelPricingSnapshotStore{
		now:         now,
		jobs:        make(map[string]*excelPricingSnapshotJob),
		idempotency: make(map[string]string),
		cache:       make(map[string]*excelPricingSnapshot),
		changed:     make(chan struct{}),
		generation:  1,
	}
}

func (store *excelPricingSnapshotStore) wakeLocked() {
	close(store.changed)
	store.changed = make(chan struct{})
}

func (store *excelPricingSnapshotStore) nextEventSequenceLocked() uint64 {
	store.eventSequence++
	return store.eventSequence
}

func (store *excelPricingSnapshotStore) retainEventLocked(event excelPricingSnapshotEventEnvelope) {
	store.events = append(store.events, cloneExcelPricingSnapshotEventEnvelope(event))
	if len(store.events) > excelPricingSnapshotEventHistory {
		store.droppedEventSequence = store.events[len(store.events)-excelPricingSnapshotEventHistory-1].Sequence
		store.events = append([]excelPricingSnapshotEventEnvelope(nil),
			store.events[len(store.events)-excelPricingSnapshotEventHistory:]...)
	}
	store.wakeLocked()

}

func (store *excelPricingSnapshotStore) publishJobLocked(job *excelPricingSnapshotJob) uint64 {
	if job == nil {
		return 0
	}
	sequence := store.nextEventSequenceLocked()
	job.eventSequence = sequence
	store.retainEventLocked(excelPricingSnapshotEventEnvelope{
		Schema:     excelPricingSnapshotEventSchema,
		Sequence:   sequence,
		Kind:       "snapshot_job",
		OccurredAt: job.updatedAt,
		Job:        excelPricingSnapshotJobResponse(job),
		jobID:      job.id,
		owner:      job.owner,
	})
	return sequence
}

func (store *excelPricingSnapshotStore) publishChangeLocked(event excelPricingStateChangeEvent) uint64 {
	event.Schema = excelPricingSnapshotEventSchema
	event.OccurredAt = store.now().UTC()
	event.Sequence = store.nextEventSequenceLocked()
	if event.Identity == nil && event.Verified && !event.Stale {
		event.Identity = excelPricingSnapshotIdentityFromChange(&event)
	}
	copyEvent := cloneExcelPricingStateChangeEvent(event)
	if copyEvent.Verified && !copyEvent.Stale && copyEvent.Identity != nil {
		store.latestChange = &copyEvent
	}
	store.retainEventLocked(excelPricingSnapshotEventEnvelope{
		Schema:     excelPricingSnapshotEventSchema,
		Sequence:   copyEvent.Sequence,
		Kind:       copyEvent.Kind,
		OccurredAt: copyEvent.OccurredAt,
		Change:     &copyEvent,
	})
	return copyEvent.Sequence
}

func (s *Server) handlePostExcelPricingSnapshot(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	owner, ok := s.authorizeExcelPricingSnapshotRequest(r)
	if !ok {
		writeExcelPricingError(w, http.StatusForbidden, "local_session_required")
		return
	}
	if !singleJSONContentType(r) {
		writeExcelPricingError(w, http.StatusUnsupportedMediaType, "json_required")
		return
	}

	var request excelPricingSnapshotStartRequest
	if err := decodeBoundedJSON(w, r, excelPricingMaxRequestBytes, &request); err != nil ||
		validateExcelPricingSnapshotStartRequest(r, request) != nil {
		writeExcelPricingError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	appConfig := s.Config()
	matches, err := s.excelPricingStateSourceMatches(r.Context(), &request.Source, appConfig)
	if err != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "canonical_source_unavailable")
		return
	}
	if !matches {
		writeExcelPricingError(w, http.StatusConflict, "canonical_source_mismatch")
		return
	}
	cfg := appConfig.SendUpdates
	if _, _, _, err := resolveExcelPricingRemote(cfg, "state"); err != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "remote_not_configured")
		return
	}

	jobIDToken, err := randomExcelPricingToken()
	if err != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "snapshot_unavailable")
		return
	}
	jobID := "snap-" + jobIDToken[:24]
	fingerprint := excelPricingSnapshotRequestFingerprint(request)
	projection := excelPricingSnapshotProjection(request.Projection)
	cacheKey := excelPricingSnapshotCacheKey(request.Source, request.Locale, projection)
	now := s.excelPricing.snapshots.now().UTC()
	idempotencyKey := excelPricingSnapshotIdempotencyKey(owner, request.RequestID)

	store := s.excelPricing.snapshots
	writeExistingLocked := func(existing *excelPricingSnapshotJob, publishReady bool) {
		if publishReady && existing.status == "ready" && existing.snapshot != nil {
			store.publishSnapshotReadyLocked(existing, "snapshot_idempotent_replayed")
		}
		response := excelPricingSnapshotJobResponse(existing)
		status := http.StatusOK
		if existing.status == "running" {
			status = http.StatusAccepted
		}
		store.mu.Unlock()
		writeExcelPricingJSON(w, status, response)
	}

	// At most two revision checks are useful: the second observes a cache/job
	// that changed while the first authenticated probe was in flight. Beyond
	// that, a new request falls through to the collector and an idempotent replay
	// fails closed rather than waiting through an unbounded revision race.
	for cacheAttempt := 0; cacheAttempt < 2; cacheAttempt++ {
		now = store.now().UTC()
		store.mu.Lock()
		store.pruneLocked(now)
		existingID, replay := store.idempotency[idempotencyKey]
		var existing *excelPricingSnapshotJob
		var cached *excelPricingSnapshot
		if replay {
			existing = store.jobs[existingID]
			if existing == nil || existing.fingerprint != fingerprint {
				store.mu.Unlock()
				writeExcelPricingError(w, http.StatusConflict, "idempotency_conflict")
				return
			}
			if existing.status != "ready" || existing.snapshot == nil {
				writeExistingLocked(existing, false)
				return
			}
			cached = existing.snapshot
		} else {
			cached = store.cache[cacheKey]
			if !excelPricingSnapshotCacheLocallyAllowed(request, cached, now) {
				goto StartFreshSnapshotLocked
			}
		}
		cacheGeneration := store.generation
		store.mu.Unlock()

		// The event subscriber remains the zero-I/O fast path. During a stream
		// gap, concurrent callers share one bounded authenticated revision probe.
		// No result can be committed until the exact captured pointer/generation
		// (or idempotent job) is proven again under the store lock.
		attestation := s.attestExcelPricingSnapshotCache(
			r.Context(),
			cfg,
			request.Source,
			cacheKey,
			cached,
		)
		// A caller that disconnected while waiting for the shared probe owns no
		// background snapshot work. Healthy joined callers can still consume the
		// server-owned result and decide whether to reuse or rebuild.
		if r.Context().Err() != nil {
			return
		}

		now = store.now().UTC()
		store.mu.Lock()
		store.pruneLocked(now)
		fenceCurrent := store.generation == cacheGeneration
		if replay {
			currentID, stillExists := store.idempotency[idempotencyKey]
			current := store.jobs[currentID]
			if !stillExists || current == nil {
				// A ready job can legitimately age out while the authenticated
				// probe is in flight. Retry the same request as a fresh
				// idempotency reservation instead of inventing a conflict.
				store.mu.Unlock()
				continue
			}
			if current.fingerprint != fingerprint {
				store.mu.Unlock()
				writeExcelPricingError(w, http.StatusConflict, "idempotency_conflict")
				return
			}
			fenceCurrent = fenceCurrent && current == existing &&
				current.status == "ready" && current.snapshot == cached
			if !fenceCurrent && (current.status != "ready" || current.snapshot == nil) {
				writeExistingLocked(current, false)
				return
			}
		} else {
			if _, appeared := store.idempotency[idempotencyKey]; appeared {
				store.mu.Unlock()
				continue
			}
			fenceCurrent = fenceCurrent && store.cache[cacheKey] == cached
			if fenceCurrent && !excelPricingSnapshotCacheLocallyAllowed(request, cached, now) {
				// Max-age is a commit-time promise. A cache that crossed the
				// caller's bound while the probe ran must not be returned.
				goto StartFreshSnapshotLocked
			}
		}

		driftFence := fenceCurrent && store.cache[cacheKey] == cached
		if attestation.drift != nil && driftFence {
			cancel := store.invalidateRemoteRevisionLocked(*attestation.drift)
			store.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			// The snapshot generation is already fenced. Discard the canonical
			// projection before any retry can collect the authenticated new tuple.
			s.invalidateCanonicalProjection(true)
			continue
		}
		if attestation.current && fenceCurrent {
			if replay {
				writeExistingLocked(existing, true)
				return
			}
			job := &excelPricingSnapshotJob{
				id:                    jobID,
				owner:                 owner,
				requestID:             request.RequestID,
				fingerprint:           fingerprint,
				cacheKey:              cacheKey,
				status:                "ready",
				createdAt:             now,
				updatedAt:             now,
				deadline:              cached.expiresAt,
				cached:                true,
				snapshot:              cached,
				source:                request.Source,
				locale:                request.Locale,
				projection:            projection,
				expectedStateRevision: request.ExpectedStateRevision,
				startGeneration:       store.generation,
				progress: excelPricingSnapshotProgress{
					Phase:          "ready",
					CompletedPages: cached.integrity.PageCount,
					TotalPages:     cached.integrity.PageCount,
					Rows:           cached.integrity.RowCount,
					HeartbeatAt:    now,
				},
			}
			store.jobs[jobID] = job
			store.idempotency[idempotencyKey] = jobID
			store.publishJobLocked(job)
			job.createdSequence = job.eventSequence
			store.publishSnapshotReadyLocked(job, "snapshot_cache_reused")
			response := excelPricingSnapshotJobResponse(job)
			store.mu.Unlock()
			writeExcelPricingJSON(w, http.StatusOK, response)
			return
		}
		if attestation.err != nil && replay && fenceCurrent {
			store.mu.Unlock()
			writeExcelPricingError(w, http.StatusServiceUnavailable,
				"snapshot_revision_unavailable")
			return
		}
		if attestation.err != nil && !replay && fenceCurrent {
			goto StartFreshSnapshotLocked
		}
		store.mu.Unlock()
	}

	now = store.now().UTC()
	store.mu.Lock()
	store.pruneLocked(now)
	if existingID, exists := store.idempotency[idempotencyKey]; exists {
		existing := store.jobs[existingID]
		if existing == nil || existing.fingerprint != fingerprint {
			store.mu.Unlock()
			writeExcelPricingError(w, http.StatusConflict, "idempotency_conflict")
			return
		}
		if existing.status == "ready" && existing.snapshot != nil {
			store.mu.Unlock()
			writeExcelPricingError(w, http.StatusServiceUnavailable,
				"snapshot_revision_unavailable")
			return
		}
		writeExistingLocked(existing, false)
		return
	}

StartFreshSnapshotLocked:

	if store.activeJobID != "" {
		leader := store.jobs[store.activeJobID]
		if leader != nil && leader.status == "running" && leader.cacheKey == cacheKey {
			job := &excelPricingSnapshotJob{
				id:                    jobID,
				owner:                 owner,
				requestID:             request.RequestID,
				fingerprint:           fingerprint,
				cacheKey:              cacheKey,
				status:                "running",
				createdAt:             now,
				updatedAt:             now,
				deadline:              leader.deadline,
				progress:              leader.progress,
				coalesced:             true,
				leaderJobID:           leader.id,
				source:                request.Source,
				locale:                request.Locale,
				projection:            projection,
				expectedStateRevision: request.ExpectedStateRevision,
				startGeneration:       store.generation,
			}
			store.jobs[jobID] = job
			store.idempotency[idempotencyKey] = jobID
			store.publishJobLocked(job)
			job.createdSequence = job.eventSequence
			response := excelPricingSnapshotJobResponse(job)
			store.mu.Unlock()
			writeExcelPricingJSON(w, http.StatusAccepted, response)
			return
		}
		store.mu.Unlock()
		writeExcelPricingSnapshotBusy(w)
		return
	}
	select {
	case s.excelPricing.permit <- struct{}{}:
		// The background job owns the permit until its immutable snapshot is done.
	default:
		store.mu.Unlock()
		writeExcelPricingSnapshotBusy(w)
		return
	}

	parent := s.backgroundCtx
	if parent == nil {
		parent = context.Background()
	}
	jobContext, cancel := context.WithTimeout(parent, excelPricingSnapshotOperationTimeout)
	job := &excelPricingSnapshotJob{
		id:                    jobID,
		owner:                 owner,
		requestID:             request.RequestID,
		fingerprint:           fingerprint,
		cacheKey:              cacheKey,
		status:                "running",
		createdAt:             now,
		updatedAt:             now,
		deadline:              now.Add(excelPricingSnapshotOperationTimeout),
		cancel:                cancel,
		source:                request.Source,
		locale:                request.Locale,
		projection:            projection,
		expectedStateRevision: request.ExpectedStateRevision,
		startGeneration:       store.generation,
		progress: excelPricingSnapshotProgress{
			Phase:       "starting",
			HeartbeatAt: now,
		},
	}
	store.jobs[jobID] = job
	store.idempotency[idempotencyKey] = jobID
	store.activeJobID = jobID
	store.publishJobLocked(job)
	job.createdSequence = job.eventSequence
	response := excelPricingSnapshotJobResponse(job)
	store.mu.Unlock()

	s.backgroundWG.Add(1)
	go func() {
		defer s.backgroundWG.Done()
		s.runExcelPricingSnapshotJob(jobContext, jobID, request, cfg)
	}()
	writeExcelPricingJSON(w, http.StatusAccepted, response)
}

func (s *Server) handleGetExcelPricingSnapshot(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	owner, ok := s.authorizeExcelPricingSnapshotRequest(r)
	if !ok {
		writeExcelPricingError(w, http.StatusForbidden, "local_session_required")
		return
	}
	jobID := mux.Vars(r)["job_id"]
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
		s.handleExcelPricingSnapshotEvents(w, r, jobID, owner)
		return
	}
	if r.URL.Query().Get("wait") == "terminal" {
		s.handleWaitExcelPricingSnapshot(w, r, jobID, owner)
		return
	}
	job := s.lookupExcelPricingSnapshotJob(jobID, owner)
	if job == nil {
		writeExcelPricingError(w, http.StatusNotFound, "snapshot_not_found")
		return
	}
	writeExcelPricingJSON(w, http.StatusOK, excelPricingSnapshotJobResponse(job))
}

func (s *Server) handleDeleteExcelPricingSnapshot(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	owner, ok := s.authorizeExcelPricingSnapshotRequest(r)
	if !ok {
		writeExcelPricingError(w, http.StatusForbidden, "local_session_required")
		return
	}
	response, status, code := s.cancelExcelPricingSnapshotJob(mux.Vars(r)["job_id"], owner)
	if status == http.StatusNotFound {
		writeExcelPricingError(w, http.StatusNotFound, "snapshot_not_found")
		return
	}
	if code != "" {
		writeExcelPricingError(w, status, code)
		return
	}
	writeExcelPricingJSON(w, status, response)
}

func (s *Server) handleGetExcelPricingSnapshotPayload(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	owner, ok := s.authorizeExcelPricingSnapshotRequest(r)
	if !ok {
		writeExcelPricingError(w, http.StatusForbidden, "local_session_required")
		return
	}
	job := s.lookupExcelPricingSnapshotJob(mux.Vars(r)["job_id"], owner)
	if job == nil {
		writeExcelPricingError(w, http.StatusNotFound, "snapshot_not_found")
		return
	}
	if job.status == "expired" {
		writeExcelPricingError(w, http.StatusGone, "snapshot_expired")
		return
	}
	if job.status == "invalidated" {
		writeExcelPricingError(w, http.StatusGone, "snapshot_invalidated")
		return
	}
	if job.status != "ready" || job.snapshot == nil {
		writeExcelPricingError(w, http.StatusConflict, "snapshot_not_ready")
		return
	}
	etag := excelPricingSnapshotETag(job.snapshot.etagRevision)
	remaining := job.snapshot.expiresAt.Sub(s.excelPricing.snapshots.now().UTC())
	if remaining <= 0 {
		writeExcelPricingError(w, http.StatusGone, "snapshot_expired")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age="+strconv.FormatInt(int64(remaining/time.Second), 10)+", must-revalidate")
	w.Header().Del("Pragma")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("ETag", etag)
	if excelPricingSnapshotETagMatches(r.Header.Values("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(job.snapshot.body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(job.snapshot.body)
}

func (s *Server) handleWaitExcelPricingSnapshot(
	w http.ResponseWriter,
	r *http.Request,
	jobID string,
	owner [sha256.Size]byte,
) {
	store := s.excelPricing.snapshots
	for {
		store.mu.Lock()
		store.pruneLocked(store.now().UTC())
		job := store.jobs[jobID]
		if job == nil || job.owner != owner {
			store.mu.Unlock()
			writeExcelPricingError(w, http.StatusNotFound, "snapshot_not_found")
			return
		}
		if excelPricingSnapshotTerminalStatus(job.status) {
			response := excelPricingSnapshotJobResponse(job)
			store.mu.Unlock()
			writeExcelPricingJSON(w, http.StatusOK, response)
			return
		}
		changed := store.changed
		store.mu.Unlock()

		select {
		case <-changed:
		case <-r.Context().Done():
			_, _, _ = s.cancelExcelPricingSnapshotJob(jobID, owner)
			return
		}
	}
}

func (s *Server) handleExcelPricingSnapshotEvents(
	w http.ResponseWriter,
	r *http.Request,
	jobID string,
	owner [sha256.Size]byte,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeExcelPricingError(w, http.StatusNotImplemented, "event_stream_unavailable")
		return
	}
	lastSent, cursorPresent, err := excelPricingSnapshotEventCursor(r)
	if err != nil {
		writeExcelPricingError(w, http.StatusBadRequest, "invalid_event_cursor")
		return
	}
	store := s.excelPricing.snapshots
	store.mu.Lock()
	store.pruneLocked(store.now().UTC())
	job := store.jobs[jobID]
	if job == nil || job.owner != owner {
		store.mu.Unlock()
		writeExcelPricingError(w, http.StatusNotFound, "snapshot_not_found")
		return
	}
	job.eventWatchers++
	store.mu.Unlock()
	defer func() {
		store.mu.Lock()
		if current := store.jobs[jobID]; current != nil && current.owner == owner && current.eventWatchers > 0 {
			current.eventWatchers--
		}
		store.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Del("Content-Length")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "retry: %d\n\n", excelPricingSnapshotRetryAfterMS)
	flusher.Flush()

	firstDelivery := true
	var serverDone <-chan struct{}
	if s.backgroundCtx != nil {
		serverDone = s.backgroundCtx.Done()
	}
	for {
		sessionExpiresAt, authorized := s.excelPricing.excelPricingSnapshotOwnerSession(owner)
		if !authorized {
			return
		}
		store.mu.Lock()
		store.pruneLocked(store.now().UTC())
		job = store.jobs[jobID]
		if job == nil || job.owner != owner {
			store.mu.Unlock()
			return
		}
		jobCopy := *job
		pending := make([]excelPricingSnapshotEventEnvelope, 0, len(store.events))
		minimumSequence := lastSent
		if firstDelivery {
			replayReason := ""
			switch {
			case cursorPresent && lastSent > store.eventSequence:
				replayReason = "cursor_ahead"
			case cursorPresent && lastSent > 0 && lastSent <= store.droppedEventSequence:
				replayReason = "cursor_expired"
			case !cursorPresent && jobCopy.createdSequence > 0 &&
				jobCopy.createdSequence <= store.droppedEventSequence:
				replayReason = "initial_history_expired"
			}
			if replayReason != "" {
				minimumSequence = store.publishReplayRequiredLocked(job, replayReason) - 1
				lastSent = minimumSequence
				jobCopy = *job
			} else if !cursorPresent && jobCopy.createdSequence > 0 {
				minimumSequence = jobCopy.createdSequence - 1
			}
		}
		for index := range store.events {
			event := store.events[index]
			if event.Sequence <= minimumSequence ||
				!excelPricingSnapshotEventVisible(event, jobID, owner) {
				continue
			}
			pending = append(pending, cloneExcelPricingSnapshotEventEnvelope(event))
		}
		changed := store.changed
		store.mu.Unlock()

		for _, event := range pending {
			if event.Sequence <= lastSent {
				continue
			}
			if err := writeExcelPricingSnapshotSSE(w, event.Sequence, event.Kind, event); err != nil {
				if !excelPricingSnapshotTerminalStatus(jobCopy.status) {
					_, _, _ = s.cancelExcelPricingSnapshotJob(jobID, owner)
				}
				return
			}
			if event.Sequence > lastSent {
				lastSent = event.Sequence
			}
		}
		if len(pending) > 0 {
			flusher.Flush()
		}
		firstDelivery = false
		if excelPricingSnapshotSSECloseStatus(jobCopy.status) {
			return
		}

		waitFor := excelPricingSnapshotSSEHeartbeat
		if remaining := sessionExpiresAt.Sub(s.excelPricing.now().UTC()); remaining < waitFor {
			waitFor = remaining
		}
		if waitFor <= 0 {
			return
		}
		timer := time.NewTimer(waitFor)
		select {
		case <-changed:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			if !sessionExpiresAt.After(s.excelPricing.now().UTC()) {
				return
			}
			if _, err := fmt.Fprintf(w, ": keepalive %d\n\n", s.excelPricing.now().UTC().Unix()); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			if !timer.Stop() {
				<-timer.C
			}
			if !excelPricingSnapshotTerminalStatus(jobCopy.status) {
				_, _, _ = s.cancelExcelPricingSnapshotJob(jobID, owner)
			}
			return
		case <-serverDone:
			if !timer.Stop() {
				<-timer.C
			}
			if !excelPricingSnapshotTerminalStatus(jobCopy.status) {
				_, _, _ = s.cancelExcelPricingSnapshotJob(jobID, owner)
			}
			return
		}
	}
}

// handleGetExcelPricingEvents serves the durable, session-scoped semantic
// change stream. Unlike the per-job stream above, it never depends on a job's
// retention lifecycle and disconnecting it never cancels snapshot work.
func (s *Server) handleGetExcelPricingEvents(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	owner, ok := s.authorizeExcelPricingSnapshotRequest(r)
	if !ok {
		writeExcelPricingError(w, http.StatusForbidden, "local_session_required")
		return
	}
	if !strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
		writeExcelPricingError(w, http.StatusNotAcceptable, "event_stream_required")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeExcelPricingError(w, http.StatusNotImplemented, "event_stream_unavailable")
		return
	}
	lastSent, cursorPresent, err := excelPricingSnapshotEventCursor(r)
	if err != nil {
		writeExcelPricingError(w, http.StatusBadRequest, "invalid_event_cursor")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Del("Content-Length")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "retry: %d\n\n", excelPricingSnapshotRetryAfterMS)
	flusher.Flush()

	store := s.excelPricing.snapshots
	firstDelivery := true
	var serverDone <-chan struct{}
	if s.backgroundCtx != nil {
		serverDone = s.backgroundCtx.Done()
	}
	for {
		sessionExpiresAt, authorized := s.excelPricing.excelPricingSnapshotOwnerSession(owner)
		if !authorized {
			return
		}

		store.mu.Lock()
		store.pruneLocked(store.now().UTC())
		minimumSequence := lastSent
		if firstDelivery {
			replayReason := ""
			if cursorPresent {
				switch {
				case lastSent > store.eventSequence:
					replayReason = "cursor_ahead"
				case store.droppedEventSequence > 0 && lastSent <= store.droppedEventSequence:
					replayReason = "cursor_expired"
				}
			} else if latest := store.latestSemanticSequenceLocked(owner); latest > 0 {
				minimumSequence = latest - 1
				lastSent = minimumSequence
			} else {
				replayReason = "initial_state_unavailable"
			}
			if replayReason != "" {
				sequence := store.publishDurableReplayRequiredLocked(owner, replayReason)
				minimumSequence = sequence - 1
				lastSent = minimumSequence
			}
		}

		pending := make([]excelPricingSnapshotEventEnvelope, 0, len(store.events))
		for index := range store.events {
			event := store.events[index]
			if event.Sequence <= minimumSequence ||
				!excelPricingSemanticEventVisible(event, owner) {
				continue
			}
			pending = append(pending, cloneExcelPricingSnapshotEventEnvelope(event))
		}
		changed := store.changed
		store.mu.Unlock()

		for _, event := range pending {
			if event.Sequence <= lastSent {
				continue
			}
			if err := writeExcelPricingSnapshotSSE(w, event.Sequence, event.Kind, event); err != nil {
				return
			}
			lastSent = event.Sequence
		}
		if len(pending) > 0 {
			flusher.Flush()
		}
		firstDelivery = false

		waitFor := excelPricingSnapshotSSEHeartbeat
		if remaining := sessionExpiresAt.Sub(s.excelPricing.now().UTC()); remaining < waitFor {
			waitFor = remaining
		}
		if waitFor <= 0 {
			return
		}
		timer := time.NewTimer(waitFor)
		select {
		case <-changed:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			if !sessionExpiresAt.After(s.excelPricing.now().UTC()) {
				return
			}
			if _, err := fmt.Fprintf(w, ": keepalive %d\n\n", s.excelPricing.now().UTC().Unix()); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-serverDone:
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
	}
}

func (store *excelPricingSnapshotStore) latestSemanticSequenceLocked(
	owner [sha256.Size]byte,
) uint64 {
	for index := len(store.events) - 1; index >= 0; index-- {
		event := store.events[index]
		if excelPricingSemanticEventVisible(event, owner) {
			return event.Sequence
		}
	}
	return 0
}

func (store *excelPricingSnapshotStore) publishDurableReplayRequiredLocked(
	owner [sha256.Size]byte,
	reason string,
) uint64 {
	sequence := store.nextEventSequenceLocked()
	change := excelPricingStateChangeEvent{
		Schema:     excelPricingSnapshotEventSchema,
		Sequence:   sequence,
		Kind:       "replay_required",
		Reason:     reason,
		OccurredAt: store.now().UTC(),
		Stale:      true,
	}
	if previous := store.lastVerifiedChangeLocked(); previous != nil {
		bindExcelPricingPreviousState(&change, previous)
	}
	store.retainEventLocked(excelPricingSnapshotEventEnvelope{
		Schema:     excelPricingSnapshotEventSchema,
		Sequence:   sequence,
		Kind:       change.Kind,
		OccurredAt: change.OccurredAt,
		Change:     &change,
		owner:      owner,
	})
	return sequence
}

func excelPricingSemanticEventVisible(
	event excelPricingSnapshotEventEnvelope,
	owner [sha256.Size]byte,
) bool {
	if event.Change == nil || event.jobID != "" {
		return false
	}
	var publicOwner [sha256.Size]byte
	return event.owner == publicOwner || event.owner == owner
}

func excelPricingSnapshotEventCursor(r *http.Request) (uint64, bool, error) {
	values := r.Header.Values("Last-Event-ID")
	if len(values) == 0 {
		return 0, false, nil
	}
	if len(values) != 1 {
		return 0, false, errors.New("multiple event cursors")
	}
	value := values[0]
	if value == "" {
		return 0, false, nil
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, false, errors.New("event cursor is invalid")
		}
	}
	cursor, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false, errors.New("event cursor is invalid")
	}
	return cursor, true, nil
}

func (state *excelPricingState) excelPricingSnapshotOwnerSession(
	owner [sha256.Size]byte,
) (time.Time, bool) {
	now := state.now().UTC()
	state.mu.Lock()
	defer state.mu.Unlock()
	session, ok := state.sessions[owner]
	if !ok || !session.expiresAt.After(now) ||
		subtle.ConstantTimeCompare(owner[:], session.tokenHash[:]) != 1 {
		delete(state.sessions, owner)
		return time.Time{}, false
	}
	return session.expiresAt, true
}

func (store *excelPricingSnapshotStore) publishReplayRequiredLocked(
	job *excelPricingSnapshotJob,
	reason string,
) uint64 {
	sequence := store.nextEventSequenceLocked()
	change := excelPricingStateChangeEvent{
		Schema:     excelPricingSnapshotEventSchema,
		Sequence:   sequence,
		Kind:       "replay_required",
		Reason:     reason,
		OccurredAt: store.now().UTC(),
		JobID:      job.id,
		Stale:      true,
	}
	if previous := store.lastVerifiedChangeLocked(); previous != nil {
		bindExcelPricingPreviousState(&change, previous)
	}
	store.retainEventLocked(excelPricingSnapshotEventEnvelope{
		Schema:     excelPricingSnapshotEventSchema,
		Sequence:   sequence,
		Kind:       change.Kind,
		OccurredAt: change.OccurredAt,
		Change:     &change,
		jobID:      job.id,
		owner:      job.owner,
	})
	store.publishJobLocked(job)
	return sequence
}

func excelPricingSnapshotEventVisible(
	event excelPricingSnapshotEventEnvelope,
	jobID string,
	owner [sha256.Size]byte,
) bool {
	if event.jobID == "" {
		return true
	}
	return event.jobID == jobID && event.owner == owner
}

func writeExcelPricingSnapshotSSE(
	w http.ResponseWriter,
	sequence uint64,
	kind string,
	payload interface{},
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		w,
		": %s\nid: %d\nevent: %s\ndata: %s\n\n",
		strings.Repeat(" ", excelPricingSnapshotSSEFlushPadding),
		sequence,
		kind,
		body,
	)
	return err
}

func excelPricingSnapshotTerminalStatus(status string) bool {
	switch status {
	case "ready", "failed", "cancelled", "expired":
		return true
	case "invalidated":
		return true
	default:
		return false
	}
}

func excelPricingSnapshotSSECloseStatus(status string) bool {
	switch status {
	case "failed", "cancelled", "expired", "invalidated":
		return true
	default:
		return false
	}
}

func (s *Server) cancelExcelPricingSnapshotJob(
	jobID string,
	owner [sha256.Size]byte,
) (map[string]interface{}, int, string) {
	store := s.excelPricing.snapshots
	store.mu.Lock()
	store.pruneLocked(store.now().UTC())
	job := store.jobs[jobID]
	if job == nil || job.owner != owner {
		store.mu.Unlock()
		return nil, http.StatusNotFound, "snapshot_not_found"
	}
	switch job.status {
	case "ready":
		store.mu.Unlock()
		return nil, http.StatusConflict, "snapshot_already_ready"
	case "expired":
		store.mu.Unlock()
		return nil, http.StatusGone, "snapshot_expired"
	case "invalidated":
		store.mu.Unlock()
		return nil, http.StatusGone, "snapshot_invalidated"
	case "failed", "cancelled":
		response := excelPricingSnapshotJobResponse(job)
		store.mu.Unlock()
		return response, http.StatusOK, ""
	case "cancelling":
		response := excelPricingSnapshotJobResponse(job)
		store.mu.Unlock()
		return response, http.StatusAccepted, ""
	}

	now := store.now().UTC()
	cancel := context.CancelFunc(nil)
	status := http.StatusOK
	changedJobs := make([]*excelPricingSnapshotJob, 0, 2)
	if job.leaderJobID != "" {
		job.status = "cancelled"
		job.errorCode = "request_cancelled"
		job.failure = nil
		job.updatedAt = now
		changedJobs = append(changedJobs, job)
		leader := store.jobs[job.leaderJobID]
		if leader != nil && (leader.status == "cancelled" || leader.status == "cancelling") &&
			!store.hasRunningFollowersLocked(leader.id) {
			leader.status = "cancelling"
			leader.errorCode = "request_cancelled"
			leader.failure = nil
			leader.updatedAt = now
			cancel = leader.cancel
			changedJobs = append(changedJobs, leader)
		}
	} else if store.hasRunningFollowersLocked(job.id) {
		job.status = "cancelled"
		job.errorCode = "request_cancelled"
		job.failure = nil
		job.updatedAt = now
		changedJobs = append(changedJobs, job)
	} else {
		job.status = "cancelling"
		job.errorCode = "request_cancelled"
		job.failure = nil
		job.updatedAt = now
		cancel = job.cancel
		status = http.StatusAccepted
		changedJobs = append(changedJobs, job)
	}
	for _, changedJob := range changedJobs {
		store.publishJobLocked(changedJob)
	}
	response := excelPricingSnapshotJobResponse(job)
	store.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return response, status, ""
}

func (s *Server) authorizeExcelPricingSnapshotRequest(r *http.Request) ([sha256.Size]byte, bool) {
	var owner [sha256.Size]byte
	if !excelPricingLocalRequestAllowed(r) ||
		!singleHeaderEquals(r, excelPricingClientHeader, excelPricingClientID) ||
		!s.excelPricing.authorizedSession(r) {
		return owner, false
	}
	values := r.Header.Values(excelPricingCSRFHeader)
	if len(values) != 1 {
		return owner, false
	}
	return sha256.Sum256([]byte(strings.TrimSpace(values[0]))), true
}

func validateExcelPricingSnapshotStartRequest(r *http.Request, request excelPricingSnapshotStartRequest) error {
	if request.Schema != excelPricingSnapshotRequestSchema ||
		request.SchemaVersion != 1 ||
		request.ClientID != excelPricingContractClientID ||
		request.Channel != excelPricingContractChannel ||
		!excelPricingIdempotencyPattern.MatchString(request.RequestID) ||
		!singleHeaderEquals(r, "Idempotency-Key", request.RequestID) {
		return errors.New("snapshot request identity is invalid")
	}
	if strings.TrimSpace(request.Source.ID) == "" ||
		strings.TrimSpace(request.Source.Dataset) == "" ||
		!isSHA256Revision(request.Source.Revision) {
		return errors.New("snapshot source is invalid")
	}
	if request.Locale != "fa" && request.Locale != "fa_IR" {
		return errors.New("snapshot locale is invalid")
	}
	if request.Projection != "" && request.Projection != excelPricingSnapshotProjectionFull &&
		request.Projection != excelPricingSnapshotProjectionExcelV1 {
		return errors.New("snapshot projection is invalid")
	}
	if request.MaxAgeSeconds < 0 ||
		time.Duration(request.MaxAgeSeconds)*time.Second > excelPricingSnapshotMaxCacheAge {
		return errors.New("snapshot cache age is invalid")
	}
	if request.ExpectedStateRevision != "" && !isSHA256Revision(request.ExpectedStateRevision) {
		return errors.New("snapshot expected state revision is invalid")
	}
	return nil
}

func (s *Server) lookupExcelPricingSnapshotJob(jobID string, owner [sha256.Size]byte) *excelPricingSnapshotJob {
	store := s.excelPricing.snapshots
	store.mu.Lock()
	defer store.mu.Unlock()
	store.pruneLocked(store.now().UTC())
	job := store.jobs[jobID]
	if job == nil || job.owner != owner {
		return nil
	}
	copyJob := *job
	return &copyJob
}

func (store *excelPricingSnapshotStore) pruneLocked(now time.Time) {
	for key, snapshot := range store.cache {
		if snapshot == nil || !snapshot.expiresAt.After(now) {
			delete(store.cache, key)
		}
	}
	if active := store.jobs[store.activeJobID]; active != nil &&
		active.status == "running" && excelPricingSnapshotRunningJobStale(active, now) {
		code := "snapshot_stale"
		if !active.deadline.IsZero() && !active.deadline.After(now) {
			code = "snapshot_timeout"
		}
		active.status = "failed"
		active.errorCode = code
		active.failure = nil
		active.updatedAt = now
		delete(store.idempotency, excelPricingSnapshotIdempotencyKey(active.owner, active.requestID))
		store.publishJobLocked(active)
		for _, follower := range store.jobs {
			if follower == nil || follower.leaderJobID != active.id || follower.status != "running" {
				continue
			}
			follower.status = "failed"
			follower.errorCode = code
			follower.failure = nil
			follower.updatedAt = now
			delete(store.idempotency, excelPricingSnapshotIdempotencyKey(follower.owner, follower.requestID))
			store.publishJobLocked(follower)
		}
		store.activeJobID = ""
		if active.cancel != nil {
			active.cancel()
		}
	}
	for id, job := range store.jobs {
		if job != nil && (job.status == "ready" || job.status == "invalidated") &&
			(job.snapshot == nil || !job.snapshot.expiresAt.After(now)) {
			job.status = "expired"
			job.errorCode = "snapshot_expired"
			job.failure = nil
			job.updatedAt = now
			store.publishJobLocked(job)
		}
		if job == nil || (job.eventWatchers == 0 &&
			(excelPricingSnapshotTerminalStatus(job.status) || job.status == "cancelling") &&
			now.Sub(job.updatedAt) > excelPricingSnapshotRetention) {
			if job != nil {
				delete(store.idempotency, excelPricingSnapshotIdempotencyKey(job.owner, job.requestID))
			}
			delete(store.jobs, id)
			if store.activeJobID == id {
				store.activeJobID = ""
			}
		}
	}
}

func excelPricingSnapshotRunningJobStale(job *excelPricingSnapshotJob, now time.Time) bool {
	if job == nil || job.status != "running" {
		return false
	}
	if !job.deadline.IsZero() && !job.deadline.After(now) {
		return true
	}
	heartbeat := job.progress.HeartbeatAt
	return !heartbeat.IsZero() && now.Sub(heartbeat) > excelPricingSnapshotHeartbeatStale
}

func (store *excelPricingSnapshotStore) invalidateCache() {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.cache = make(map[string]*excelPricingSnapshot)
}

func (store *excelPricingSnapshotStore) invalidateReadyJobsLocked(code string) {
	store.invalidateReadyJobsExceptLocked(code, nil)
}

// invalidateGenerationLocked is the single atomic fence for external state
// invalidations. It advances the generation before cancellation so a remote
// response that races context cancellation can never become ready or warm the
// cache under the obsolete generation.
func (store *excelPricingSnapshotStore) invalidateGenerationLocked(
	code string,
) context.CancelFunc {
	store.generation++
	store.cache = make(map[string]*excelPricingSnapshot)
	store.invalidateReadyJobsLocked(code)

	leader := store.jobs[store.activeJobID]
	if leader == nil || (leader.status != "running" && leader.status != "cancelling") {
		return nil
	}
	now := store.now().UTC()
	leader.status = "cancelling"
	leader.errorCode = code
	leader.failure = nil
	leader.updatedAt = now
	store.publishJobLocked(leader)
	for _, follower := range store.jobs {
		if follower == nil || follower.leaderJobID != leader.id ||
			(follower.status != "running" && follower.status != "cancelling") {
			continue
		}
		follower.status = "cancelled"
		follower.errorCode = code
		follower.failure = nil
		follower.updatedAt = now
		store.publishJobLocked(follower)
	}
	return leader.cancel
}

func (store *excelPricingSnapshotStore) invalidateReadyJobsExceptLocked(
	code string,
	keep map[string]struct{},
) {
	now := store.now().UTC()
	invalidated := make([]*excelPricingSnapshotJob, 0)
	for _, job := range store.jobs {
		if job != nil && job.status == "ready" {
			if _, preserved := keep[job.id]; preserved {
				continue
			}
			job.status = "invalidated"
			job.errorCode = code
			job.failure = nil
			job.updatedAt = now
			invalidated = append(invalidated, job)
		}
	}
	for _, job := range invalidated {
		store.publishJobLocked(job)
	}
}

func excelPricingSnapshotIdentityChange(
	previous *excelPricingStateChangeEvent,
	current excelPricingSnapshotIdentity,
) (string, string) {
	if previous == nil || previous.Source == nil {
		return "", ""
	}
	if *previous.Source != current.Source {
		return "source_changed", "snapshot_source_changed"
	}
	if previous.CatalogRevision != current.CatalogRevision {
		return "catalog_changed", "snapshot_catalog_changed"
	}
	if previous.StateRevision != current.StateRevision {
		return "pricing_state_changed", "snapshot_pricing_state_changed"
	}
	return "", ""
}

func (store *excelPricingSnapshotStore) publishIdentityChangeLocked(
	kind, jobID string,
	previous *excelPricingStateChangeEvent,
	current excelPricingSnapshotIdentity,
) {
	reason := map[string]string{
		"source_changed":        "verified_source_revision_changed",
		"catalog_changed":       "verified_catalog_revision_changed",
		"pricing_state_changed": "verified_pricing_state_revision_changed",
	}[kind]
	source := current.Source
	event := excelPricingStateChangeEvent{
		Kind:             kind,
		Reason:           reason,
		JobID:            jobID,
		Source:           &source,
		CatalogRevision:  current.CatalogRevision,
		StateRevision:    current.StateRevision,
		SnapshotRevision: current.SnapshotRevision,
		ETag:             current.ETag,
		Stale:            true,
		Verified:         true,
		Identity:         cloneExcelPricingSnapshotIdentity(&current),
	}
	bindExcelPricingPreviousState(&event, previous)
	store.publishChangeLocked(event)
}

func (s *Server) notifyExcelPricingSourceChanged(sourceChangeToken string) {
	s.invalidateCanonicalProjection(false)
	store := s.excelPricing.snapshots
	store.mu.Lock()
	previous := store.lastVerifiedChangeLocked()
	cancel := store.invalidateGenerationLocked("snapshot_source_changed")
	event := excelPricingStateChangeEvent{
		Kind:              "source_changed",
		Reason:            "local_source_changed",
		SourceChangeToken: strings.TrimSpace(sourceChangeToken),
		Stale:             true,
	}
	bindExcelPricingPreviousState(&event, previous)
	store.publishChangeLocked(event)
	store.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (store *excelPricingSnapshotStore) publishPricingStateInvalidated(stateRevision string) {
	store.mu.Lock()
	previous := store.lastVerifiedChangeLocked()
	cancel := store.invalidateGenerationLocked("snapshot_pricing_state_changed")
	event := excelPricingStateChangeEvent{
		Kind:          "pricing_state_invalidated",
		Reason:        "pricing_apply_committed",
		StateRevision: stateRevision,
		Stale:         true,
	}
	bindExcelPricingPreviousState(&event, previous)
	store.publishChangeLocked(event)
	store.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (store *excelPricingSnapshotStore) publishPricingStateVerified(stateRevision string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	event := excelPricingStateChangeEvent{
		Kind:          "pricing_state_changed",
		Reason:        "pricing_apply_verified",
		StateRevision: stateRevision,
		Stale:         true,
		Verified:      true,
	}
	bindExcelPricingPreviousState(&event, store.lastVerifiedChangeLocked())
	store.publishChangeLocked(event)
}

// notifyExcelPricingRemoteRevisionChanged is the synchronous acceptance hook
// for the authenticated upstream event subscriber. Returning nil means the old
// local generation has already been fenced and it is safe to advance the
// remote cursor.
func (s *Server) notifyExcelPricingRemoteRevisionChanged(
	revision excelPricingRemoteRevision,
) error {
	if !validExcelPricingRemoteSource(revision.Source) ||
		!validExcelPricingRemoteRevisionParts(revision.StateRevision, revision.CatalogRevision) ||
		!isStrongExcelPricingRevisionETag(revision.ETag, revision.StateRevision) {
		return errExcelPricingRemoteRevision
	}
	// The authenticated composite revision is authoritative for every remote
	// pricing input. Fence the exact product-sync projection and discard the
	// provider's shorter-lived assignment cache before acknowledging its cursor.
	s.invalidateCanonicalProjection(true)

	store := s.excelPricing.snapshots
	store.mu.Lock()
	cancel := store.invalidateRemoteRevisionLocked(revision)
	store.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// invalidateRemoteRevisionLocked applies an already authenticated revision to
// the snapshot generation. Callers that obtained the revision outside the
// lock must first prove that their captured cache pointer and generation still
// match; this keeps duplicate probes from cancelling a newer collector.
func (store *excelPricingSnapshotStore) invalidateRemoteRevisionLocked(
	revision excelPricingRemoteRevision,
) context.CancelFunc {
	previous := store.lastVerifiedChangeLocked()
	kind := "pricing_state_changed"
	code := "snapshot_pricing_state_changed"
	// Patris revisions are expected to advance while the workbook is open.
	// Revision is snapshot coherence, not provider identity: classifying every
	// advance as source_changed sends Excel down the full catalog-refresh path
	// and can consume the website-first ACK window. Only an ID/dataset change is
	// a source change; a pricing-only revision can then use the fast confirmation
	// discovery path while catalog refresh remains independently event-driven.
	if previous != nil && previous.Source != nil &&
		!sameExcelPricingRemoteSourceIdentity(*previous.Source, revision.Source) {
		kind = "source_changed"
		code = "snapshot_source_changed"
	} else if previous != nil && previous.PricingStateRevision != "" &&
		previous.PricingStateRevision != revision.PricingStateRevision {
		// A website pricing commit can legitimately change the derived catalog
		// revision as every Woo price is rebuilt. Route that event to the fast
		// confirmation/ACK path before considering catalog drift; otherwise Excel
		// starts a full snapshot and can miss the bounded ACK deadline.
		kind = "pricing_state_changed"
		code = "snapshot_pricing_state_changed"
	} else if previous != nil && previous.CatalogRevision != "" &&
		previous.CatalogRevision != revision.CatalogRevision {
		kind = "catalog_changed"
		code = "snapshot_catalog_changed"
	}
	cancel := store.invalidateGenerationLocked(code)
	source := revision.Source
	event := excelPricingStateChangeEvent{
		Kind:                  kind,
		Reason:                "upstream_composite_revision_changed",
		Source:                &source,
		CatalogRevision:       revision.CatalogRevision,
		StateRevision:         revision.StateRevision,
		PricingStateRevision:  revision.PricingStateRevision,
		PricingPolicyRevision: revision.PricingPolicyRevision,
		ETag:                  revision.ETag,
		Stale:                 true,
		Verified:              true,
	}
	bindExcelPricingPreviousState(&event, previous)
	store.publishChangeLocked(event)
	return cancel
}

func (store *excelPricingSnapshotStore) lastVerifiedChangeLocked() *excelPricingStateChangeEvent {
	if event := store.latestChange; event != nil && event.Verified && !event.Stale &&
		event.Identity != nil {
		copyEvent := cloneExcelPricingStateChangeEvent(*event)
		return &copyEvent
	}
	for index := len(store.events) - 1; index >= 0; index-- {
		event := store.events[index].Change
		if event != nil && event.Verified && !event.Stale && event.Source != nil &&
			event.CatalogRevision != "" && event.StateRevision != "" && event.ETag != "" {
			copyEvent := cloneExcelPricingStateChangeEvent(*event)
			return &copyEvent
		}
	}
	return nil
}

func bindExcelPricingPreviousState(
	event *excelPricingStateChangeEvent,
	previous *excelPricingStateChangeEvent,
) {
	if event == nil || previous == nil {
		return
	}
	if previous.Source != nil {
		source := *previous.Source
		event.PreviousSource = &source
	}
	event.PreviousCatalogRevision = previous.CatalogRevision
	event.PreviousStateRevision = previous.StateRevision
	event.PreviousSnapshotRevision = previous.SnapshotRevision
	event.PreviousETag = previous.ETag
	event.InvalidatedIdentity = excelPricingSnapshotIdentityFromChange(previous)
}

func (store *excelPricingSnapshotStore) hasRunningFollowersLocked(leaderJobID string) bool {
	for _, job := range store.jobs {
		if job != nil && job.leaderJobID == leaderJobID && job.status == "running" {
			return true
		}
	}
	return false
}

func (store *excelPricingSnapshotStore) makeJobReadyLocked(
	job *excelPricingSnapshotJob,
	snapshot *excelPricingSnapshot,
) {
	job.status = "ready"
	job.errorCode = ""
	job.failure = nil
	job.snapshot = snapshot
	job.deadline = snapshot.expiresAt
	job.updatedAt = store.now().UTC()
	job.progress = excelPricingSnapshotProgress{
		Phase:          "ready",
		CompletedPages: snapshot.integrity.PageCount,
		TotalPages:     snapshot.integrity.PageCount,
		Rows:           snapshot.integrity.RowCount,
		HeartbeatAt:    store.now().UTC(),
	}
}

func (store *excelPricingSnapshotStore) publishSnapshotReadyLocked(
	job *excelPricingSnapshotJob,
	reason string,
) uint64 {
	if job == nil || job.status != "ready" || job.snapshot == nil {
		return 0
	}
	snapshot := job.snapshot
	source := job.source
	identity := &excelPricingSnapshotIdentity{
		Source:           source,
		CatalogRevision:  snapshot.integrity.DatasetRevision,
		StateRevision:    snapshot.stateRevision,
		SnapshotRevision: snapshot.revision,
		ETag:             excelPricingSnapshotETag(snapshot.etagRevision),
	}
	return store.publishChangeLocked(excelPricingStateChangeEvent{
		Kind:                 "snapshot_ready",
		Reason:               reason,
		JobID:                job.id,
		Source:               &source,
		CatalogRevision:      identity.CatalogRevision,
		StateRevision:        identity.StateRevision,
		PricingStateRevision: snapshot.pricingStateRevision,
		SnapshotRevision:     identity.SnapshotRevision,
		ETag:                 identity.ETag,
		Verified:             true,
		Identity:             identity,
	})
}

func (store *excelPricingSnapshotStore) completeFollowersLocked(
	leaderJobID string,
	snapshot *excelPricingSnapshot,
	code string,
) ([]*excelPricingSnapshotJob, int) {
	changed := make([]*excelPricingSnapshotJob, 0)
	ready := 0
	var leaderFailure *excelPricingSnapshotFailure
	if leader := store.jobs[leaderJobID]; leader != nil && leader.failure != nil {
		copyFailure := *leader.failure
		leaderFailure = &copyFailure
	}
	for _, follower := range store.jobs {
		if follower == nil || follower.leaderJobID != leaderJobID {
			continue
		}
		if follower.status == "cancelling" {
			follower.status = "cancelled"
			follower.errorCode = "request_cancelled"
			follower.failure = nil
			follower.updatedAt = store.now().UTC()
			changed = append(changed, follower)
			continue
		}
		if follower.status != "running" {
			continue
		}
		if snapshot == nil {
			if leaderFailure != nil {
				copyFailure := *leaderFailure
				follower.failure = &copyFailure
			}
			if code == "request_cancelled" {
				follower.status = "cancelled"
				follower.errorCode = "request_cancelled"
				follower.failure = nil
			} else {
				follower.status = "failed"
				follower.errorCode = code
			}
			follower.updatedAt = store.now().UTC()
			changed = append(changed, follower)
			continue
		}
		if follower.expectedStateRevision != "" &&
			follower.expectedStateRevision != snapshot.stateRevision {
			follower.status = "failed"
			follower.errorCode = "snapshot_state_revision_mismatch"
			follower.failure = nil
			follower.updatedAt = store.now().UTC()
			changed = append(changed, follower)
			continue
		}
		store.makeJobReadyLocked(follower, snapshot)
		changed = append(changed, follower)
		ready++
	}
	return changed, ready
}

func (s *Server) runExcelPricingSnapshotJob(
	ctx context.Context,
	jobID string,
	request excelPricingSnapshotStartRequest,
	cfg updateout.Config,
) {
	heartbeatDone := make(chan struct{})
	go s.heartbeatExcelPricingSnapshot(ctx, jobID, heartbeatDone)
	snapshot, code := s.collectExcelPricingSnapshot(ctx, jobID, request, cfg)
	close(heartbeatDone)
	// Release the shared mutation/snapshot gate before publishing a terminal
	// status. A client that observes ready/failed/cancelled can immediately issue
	// the next guarded operation without waiting behind stale work.
	<-s.excelPricing.permit

	store := s.excelPricing.snapshots
	store.mu.Lock()
	defer store.mu.Unlock()
	job := store.jobs[jobID]
	if job == nil {
		if store.activeJobID == jobID {
			store.activeJobID = ""
		}
		return
	}
	if job.cancel != nil {
		job.cancel()
		job.cancel = nil
	}
	if store.activeJobID == jobID {
		store.activeJobID = ""
	}
	job.updatedAt = store.now().UTC()
	if excelPricingSnapshotTerminalStatus(job.status) && job.status != "cancelled" {
		// A stale/deadline reaper or source invalidation already published the
		// authoritative terminal state. Late remote completion cannot revive it.
		return
	}
	if job.startGeneration != store.generation {
		code := job.errorCode
		if code == "" {
			code = "snapshot_generation_changed"
		}
		job.status = "cancelled"
		job.errorCode = code
		job.failure = nil
		job.snapshot = nil
		job.updatedAt = store.now().UTC()
		store.publishJobLocked(job)
		for _, follower := range store.jobs {
			if follower == nil || follower.leaderJobID != jobID ||
				excelPricingSnapshotTerminalStatus(follower.status) {
				continue
			}
			follower.status = "cancelled"
			follower.errorCode = code
			follower.failure = nil
			follower.snapshot = nil
			follower.updatedAt = store.now().UTC()
			store.publishJobLocked(follower)
		}
		return
	}
	ready := 0
	if snapshot == nil {
		if job.status == "cancelling" || job.status == "cancelled" || code == "request_cancelled" {
			job.status = "cancelled"
			if job.errorCode == "" {
				job.errorCode = "request_cancelled"
			}
			job.failure = nil
		} else {
			job.status = "failed"
			job.errorCode = code
		}
		followers, _ := store.completeFollowersLocked(jobID, nil, code)
		store.publishJobLocked(job)
		for _, follower := range followers {
			store.publishJobLocked(follower)
		}
		return
	}
	previous := store.lastVerifiedChangeLocked()
	jobChanged := false
	if job.status == "cancelling" {
		job.status = "cancelled"
		if job.errorCode == "" {
			job.errorCode = "request_cancelled"
		}
		job.failure = nil
		job.updatedAt = store.now().UTC()
		jobChanged = true
	} else if job.status == "running" {
		if job.expectedStateRevision != "" && job.expectedStateRevision != snapshot.stateRevision {
			job.status = "failed"
			job.errorCode = "snapshot_state_revision_mismatch"
			job.failure = nil
			job.updatedAt = store.now().UTC()
			jobChanged = true
		} else {
			store.makeJobReadyLocked(job, snapshot)
			jobChanged = true
			ready++
		}
	}
	followers, followerReady := store.completeFollowersLocked(jobID, snapshot, "")
	ready += followerReady
	if ready > 0 {
		currentIdentity := excelPricingSnapshotIdentity{
			Source:           job.source,
			CatalogRevision:  snapshot.integrity.DatasetRevision,
			StateRevision:    snapshot.stateRevision,
			SnapshotRevision: snapshot.revision,
			ETag:             excelPricingSnapshotETag(snapshot.etagRevision),
		}
		keep := make(map[string]struct{}, len(followers)+1)
		if job.status == "ready" && job.snapshot == snapshot {
			keep[job.id] = struct{}{}
		}
		for _, follower := range followers {
			if follower.status == "ready" && follower.snapshot == snapshot {
				keep[follower.id] = struct{}{}
			}
		}
		changeKind, invalidationCode := excelPricingSnapshotIdentityChange(previous, currentIdentity)
		if changeKind != "" {
			store.invalidateReadyJobsExceptLocked(invalidationCode, keep)
		}
		store.cache[job.cacheKey] = snapshot
		if jobChanged {
			store.publishJobLocked(job)
		}
		for _, follower := range followers {
			store.publishJobLocked(follower)
		}
		if changeKind != "" {
			store.publishIdentityChangeLocked(changeKind, jobID, previous, currentIdentity)
		}
		source := job.source
		store.publishChangeLocked(excelPricingStateChangeEvent{
			Kind:                 "snapshot_ready",
			Reason:               "snapshot_verified",
			JobID:                jobID,
			Source:               &source,
			CatalogRevision:      snapshot.integrity.DatasetRevision,
			StateRevision:        snapshot.stateRevision,
			PricingStateRevision: snapshot.pricingStateRevision,
			SnapshotRevision:     snapshot.revision,
			ETag:                 excelPricingSnapshotETag(snapshot.etagRevision),
			Verified:             true,
		})
		return
	}
	if jobChanged {
		store.publishJobLocked(job)
	}
	for _, follower := range followers {
		store.publishJobLocked(follower)
	}
}

func (s *Server) heartbeatExcelPricingSnapshot(
	ctx context.Context,
	jobID string,
	done <-chan struct{},
) {
	ticker := time.NewTicker(excelPricingSnapshotHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			store := s.excelPricing.snapshots
			store.mu.Lock()
			now := store.now().UTC()
			if job := store.jobs[jobID]; job != nil && job.status == "running" {
				job.progress.HeartbeatAt = now
				job.updatedAt = now
				for _, follower := range store.jobs {
					if follower != nil && follower.leaderJobID == jobID && follower.status == "running" {
						follower.progress.HeartbeatAt = now
						follower.updatedAt = now
					}
				}
			}
			store.mu.Unlock()
		case <-ctx.Done():
			return
		case <-done:
			return
		}
	}
}

func (s *Server) collectExcelPricingSnapshot(
	ctx context.Context,
	jobID string,
	request excelPricingSnapshotStartRequest,
	cfg updateout.Config,
) (*excelPricingSnapshot, string) {
	if s != nil && s.excelPricing != nil && s.excelPricing.snapshotCollector != nil {
		return s.excelPricing.snapshotCollector(ctx, jobID, request, cfg)
	}
	if s == nil || s.excelPricing == nil || s.excelPricingRemote == nil {
		if s != nil {
			s.setExcelPricingSnapshotFailure(
				jobID,
				excelPricingSnapshotStageRemoteConfiguration,
				"snapshot_remote_configuration_failed",
			)
		}
		return nil, "remote_unavailable"
	}
	s.updateExcelPricingSnapshotProgress(jobID, "fetching_remote", 0, 1, 0)
	client, err := newExcelPricingRemoteSnapshotClient(
		cfg,
		request.Source,
		excelPricingRemoteSnapshotClientOptions{
			HTTPClient: s.excelPricing.client,
			Terminals:  s.excelPricingRemote.snapshotTerminals(),
		},
	)
	if err != nil {
		s.setExcelPricingSnapshotFailure(
			jobID,
			excelPricingSnapshotStageRemoteConfiguration,
			"snapshot_remote_configuration_failed",
		)
		return nil, excelPricingRemoteSnapshotFailureCode(ctx, err)
	}
	result, err := client.Collect(
		ctx,
		excelPricingRemoteSnapshotRequestID(jobID),
		request.MaxAgeSeconds,
	)
	if errors.Is(err, errExcelPricingRemoteSnapshotSourceConflict) {
		// Patris is expected to change while Excel is open. Align WordPress to
		// this exact live source once, then rebuild the ephemeral snapshot. If
		// Patris advances again during delivery, retain the actionable conflict
		// so the workbook's bounded retry reacquires the newer source instead of
		// mixing revisions or falling back to a persisted snapshot.
		s.updateExcelPricingSnapshotProgress(jobID, "waiting_remote", 0, 1, 0)
		if s.deliverExcelPricingSnapshotSource(ctx, cfg, request.Source) == nil {
			result, err = client.Collect(
				ctx,
				excelPricingRemoteSnapshotRequestID(jobID),
				request.MaxAgeSeconds,
			)
		}
	}
	if err != nil {
		if stage, detail, ok := excelPricingRemoteSnapshotFailureDetails(err); ok {
			s.setExcelPricingSnapshotFailure(jobID, stage, detail)
		}
		return nil, excelPricingRemoteSnapshotFailureCode(ctx, err)
	}
	s.updateExcelPricingSnapshotProgress(
		jobID,
		"validating",
		resultRowsPageCount(result),
		resultRowsPageCount(result),
		len(result.Rows),
	)
	snapshot, code := s.finalizeExcelPricingRemoteSnapshot(
		jobID,
		result,
		excelPricingSnapshotProjection(request.Projection),
	)
	if snapshot == nil {
		return nil, code
	}
	s.updateExcelPricingSnapshotProgress(
		jobID,
		"building",
		snapshot.integrity.PageCount,
		snapshot.integrity.PageCount,
		snapshot.integrity.RowCount,
	)
	return snapshot, ""
}

func (s *Server) deliverExcelPricingSnapshotSource(
	ctx context.Context,
	cfg updateout.Config,
	expected canonical.Source,
) error {
	if s == nil || s.excelPricing == nil || !validExcelPricingRemoteSource(expected) {
		return errExcelPricingRemoteSnapshotSourceConflict
	}
	contract, err := s.excelPricingCanonical(ctx, s.Config())
	if err != nil || contract == nil || contract.Source != expected {
		return errExcelPricingRemoteSnapshotSourceConflict
	}
	deliveryConfig := updateout.Normalize(cfg)
	if !deliveryConfig.Enabled || deliveryConfig.Format != "json" ||
		deliveryConfig.Method != http.MethodPost {
		return errExcelPricingRemoteSnapshotConfiguration
	}
	if deliveryConfig.RetryAttempts < 10 {
		deliveryConfig.RetryAttempts = 10
	}
	if backoff, parseErr := time.ParseDuration(deliveryConfig.RetryBackoff); parseErr != nil || backoff < 2*time.Second {
		deliveryConfig.RetryBackoff = "2s"
	}
	event := updateout.Event{
		Type:             "update",
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		Source:           s.currentDBPath(),
		Raw:              false,
		Contract:         contract,
		SnapshotContract: contract,
	}
	dispatch := s.excelPricing.dispatch
	if dispatch == nil {
		dispatch = updateout.DispatchWithResult
	}
	delivery, err := dispatch(ctx, deliveryConfig, event)
	if err != nil || !excelPricingSnapshotDeliveryAccepted(delivery, contract.EventID) {
		return errExcelPricingRemoteSnapshotUnavailable
	}
	// A newer source supersedes the just-delivered coherence envelope. Never
	// reuse it as current; let the caller reacquire and retry the new revision.
	current, err := s.excelPricingCanonical(ctx, s.Config())
	if err != nil || current == nil || current.Source != expected {
		return errExcelPricingRemoteSnapshotSourceConflict
	}
	return nil
}

func excelPricingSnapshotDeliveryAccepted(result updateout.DeliveryResult, eventID string) bool {
	if result.HTTPStatus < 200 || result.HTTPStatus >= 300 || result.Retryable ||
		result.EventID != eventID || result.Attempts < 1 || result.DeferredAmbiguous != 0 {
		return false
	}
	switch result.Status {
	case "accepted", "already_current", "replayed", "recovered":
		return true
	default:
		return false
	}
}

func (s *Server) finalizeExcelPricingRemoteSnapshot(
	jobID string,
	result *excelPricingRemoteSnapshotResult,
	projection string,
) (*excelPricingSnapshot, string) {
	snapshot, err := buildExcelPricingSnapshotFromRemoteResult(
		result,
		projection,
		s.excelPricing.snapshots.now().UTC(),
	)
	if err != nil {
		s.setExcelPricingSnapshotFailure(
			jobID,
			excelPricingSnapshotStageLocalProjection,
			"snapshot_local_projection_integrity_failed",
		)
		return nil, "snapshot_integrity_failed"
	}
	return snapshot, ""
}

func resultRowsPageCount(result *excelPricingRemoteSnapshotResult) int {
	if result == nil || len(result.Rows) == 0 {
		return 1
	}
	return (len(result.Rows) + excelPricingSnapshotPageSize - 1) / excelPricingSnapshotPageSize
}

func excelPricingRemoteSnapshotFailureCode(ctx context.Context, err error) string {
	if ctx != nil && ctx.Err() != nil {
		return excelPricingSnapshotContextCode(ctx)
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		if ctx != nil {
			return excelPricingSnapshotContextCode(ctx)
		}
		return "remote_unavailable"
	case errors.Is(err, errExcelPricingRemoteSnapshotSourceConflict):
		return "snapshot_source_revision_conflict"
	case errors.Is(err, errExcelPricingRemoteSnapshotProtocol),
		errors.Is(err, errExcelPricingRemoteSnapshotIntegrity):
		return "snapshot_integrity_failed"
	case errors.Is(err, errExcelPricingRemoteSnapshotConfiguration):
		return "remote_not_configured"
	default:
		return "remote_unavailable"
	}
}

func (s *Server) collectExcelPricingSnapshotPages(
	ctx context.Context,
	jobID string,
	request excelPricingSnapshotStartRequest,
	cfg updateout.Config,
) (*excelPricingSnapshot, string) {
	var first *excelPricingSnapshotPage
	var allRows []json.RawMessage
	seenKeys := make(map[string]struct{})
	pageRevisions := make([]string, 0, excelPricingSnapshotMaxPages)
	totalPages := 1
	observedCounts := excelPricingSnapshotReconciliationCounts{}

	for pageNumber := 1; pageNumber <= totalPages; pageNumber++ {
		select {
		case <-ctx.Done():
			return nil, excelPricingSnapshotContextCode(ctx)
		default:
		}
		s.updateExcelPricingSnapshotProgress(
			jobID, "fetching_remote", pageNumber-1, totalPages, len(allRows),
		)
		local := excelPricingLocalRequest{
			Schema:        excelPricingLocalRequestSchema,
			SchemaVersion: 1,
			Operation:     "state",
			ClientID:      excelPricingContractClientID,
			Channel:       excelPricingContractChannel,
			RequestID:     excelPricingSnapshotPageRequestID(jobID, pageNumber),
			Source:        &request.Source,
			Page:          pageNumber,
			Limit:         excelPricingSnapshotPageSize,
			Locale:        request.Locale,
		}
		remoteRequest := buildExcelPricingRemoteRequest("state", local, request.Source)
		remote, err := s.forwardExcelPricing(ctx, cfg, "state", remoteRequest, local)
		if err != nil {
			var remoteError *excelPricingRemoteError
			if errors.As(err, &remoteError) && remoteError.code != "" {
				return nil, excelPricingSnapshotFailureCode(remoteError.code)
			}
			if ctx.Err() != nil {
				return nil, excelPricingSnapshotContextCode(ctx)
			}
			return nil, "remote_unavailable"
		}
		parsed, err := parseExcelPricingSnapshotPage(remote.body, request.Source, pageNumber)
		if err != nil {
			return nil, "snapshot_integrity_failed"
		}
		if first == nil {
			first = parsed
			totalPages = parsed.pages
			s.updateExcelPricingSnapshotProgress(jobID, "validating", 0, totalPages, 0)
			if totalPages < 1 || totalPages > excelPricingSnapshotMaxPages ||
				parsed.total > excelPricingSnapshotPageSize*excelPricingSnapshotMaxPages {
				return nil, "snapshot_too_large"
			}
		} else if parsed.stateRevision != first.stateRevision ||
			parsed.datasetRevision != first.datasetRevision ||
			parsed.catalogMetadataDigest != first.catalogMetadataDigest ||
			parsed.warningsDigest != first.warningsDigest ||
			parsed.warningCount != first.warningCount ||
			parsed.reconciliationCounts != first.reconciliationCounts ||
			parsed.total != first.total || parsed.pages != first.pages || parsed.limit != first.limit {
			return nil, "snapshot_revision_changed"
		}
		for _, row := range parsed.rows {
			var object map[string]json.RawMessage
			if json.Unmarshal(row, &object) != nil {
				return nil, "snapshot_integrity_failed"
			}
			var syncKey string
			if json.Unmarshal(object["sync_key"], &syncKey) != nil || strings.TrimSpace(syncKey) == "" {
				return nil, "snapshot_integrity_failed"
			}
			if _, exists := seenKeys[syncKey]; exists {
				return nil, "snapshot_integrity_failed"
			}
			status := excelPricingSnapshotString(object["reconciliation_status"])
			switch status {
			case "matched":
				observedCounts.Matched++
			case "patris_only":
				observedCounts.PatrisOnly++
			case "woo_only":
				observedCounts.WooOnly++
			default:
				return nil, "snapshot_integrity_failed"
			}
			canonicalRow, marshalErr := json.Marshal(object)
			if marshalErr != nil {
				return nil, "snapshot_integrity_failed"
			}
			seenKeys[syncKey] = struct{}{}
			allRows = append(allRows, canonicalRow)
		}
		pageRevisions = append(pageRevisions, parsed.pageRevision)
		s.updateExcelPricingSnapshotProgress(
			jobID, "validating", pageNumber, totalPages, len(allRows),
		)
	}

	if first == nil || len(allRows) != first.total || len(seenKeys) != first.total {
		return nil, "snapshot_integrity_failed"
	}
	if observedCounts.Matched != first.reconciliationCounts.Matched ||
		observedCounts.PatrisOnly != first.reconciliationCounts.PatrisOnly ||
		observedCounts.WooOnly != first.reconciliationCounts.WooOnly {
		return nil, "snapshot_integrity_failed"
	}
	s.updateExcelPricingSnapshotProgress(jobID, "building", totalPages, totalPages, len(allRows))
	snapshot, err := buildExcelPricingSnapshot(
		first,
		request.Source,
		allRows,
		pageRevisions,
		excelPricingSnapshotProjection(request.Projection),
		s.excelPricing.snapshots.now().UTC(),
	)
	if err != nil {
		return nil, "snapshot_integrity_failed"
	}
	return snapshot, ""
}

func parseExcelPricingSnapshotPage(
	body []byte,
	expectedSource canonical.Source,
	expectedPage int,
) (*excelPricingSnapshotPage, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil {
		return nil, errors.New("snapshot response is not an object")
	}
	state := root
	if rawData, exists := root["data"]; exists {
		var data map[string]json.RawMessage
		if json.Unmarshal(rawData, &data) != nil || data == nil {
			return nil, errors.New("snapshot data is invalid")
		}
		state = data
	}
	state = cloneExcelPricingRawMap(state)
	for _, key := range []string{"schema", "state_revision"} {
		if _, exists := state[key]; !exists {
			if raw, rootExists := root[key]; rootExists {
				state[key] = raw
			}
		}
	}
	var schema string
	var stateRevision string
	if json.Unmarshal(state["schema"], &schema) != nil || schema != excelPricingStateSchema ||
		json.Unmarshal(state["state_revision"], &stateRevision) != nil || !isSHA256Revision(stateRevision) {
		return nil, errors.New("snapshot state identity is invalid")
	}
	if rawStatus, exists := state["status"]; exists {
		status := excelPricingSnapshotString(rawStatus)
		if status != "ready" && status != "current" {
			return nil, errors.New("snapshot state status is invalid")
		}
	}
	if !excelPricingSnapshotSourceMatches(state["source"], expectedSource) {
		return nil, errors.New("snapshot source identity changed")
	}

	warningsJSON, warningCount, err := excelPricingSnapshotTopLevelWarnings(state)
	if err != nil {
		return nil, err
	}

	var catalog map[string]json.RawMessage
	if json.Unmarshal(state["catalog"], &catalog) != nil || catalog == nil {
		return nil, errors.New("snapshot catalog is invalid")
	}
	if err := excelPricingSnapshotValidateMetadataWarnings(state, catalog); err != nil {
		return nil, errors.New("snapshot catalog integrity warning")
	}
	var dataset, datasetRevision, pageRevision string
	if json.Unmarshal(catalog["dataset"], &dataset) != nil || dataset != "reconciled_products" ||
		json.Unmarshal(catalog["dataset_revision"], &datasetRevision) != nil || !isSHA256Revision(datasetRevision) ||
		json.Unmarshal(catalog["page_revision"], &pageRevision) != nil || !isSHA256Revision(pageRevision) {
		return nil, errors.New("snapshot catalog identity is invalid")
	}
	reconciliationCounts, ok := excelPricingSnapshotCatalogContract(catalog, expectedSource)
	if !ok {
		return nil, errors.New("snapshot catalog contract is invalid")
	}
	var pagination excelPricingSnapshotPagination
	if json.Unmarshal(catalog["pagination"], &pagination) != nil ||
		pagination.Page != expectedPage || pagination.Limit != excelPricingSnapshotPageSize ||
		pagination.Total < 1 || pagination.Pages < 1 ||
		pagination.HasMore != (pagination.Page < pagination.Pages) {
		return nil, errors.New("snapshot pagination is invalid")
	}
	if reconciliationCounts.UnionRows != pagination.Total {
		return nil, errors.New("snapshot reconciliation count is invalid")
	}
	var rows []json.RawMessage
	if json.Unmarshal(catalog["rows"], &rows) != nil || rows == nil || len(rows) > pagination.Limit {
		return nil, errors.New("snapshot rows are invalid")
	}
	if expectedPage < pagination.Pages && len(rows) == 0 {
		return nil, errors.New("snapshot page is unexpectedly empty")
	}

	metadata, err := excelPricingSnapshotStableCatalogMetadata(catalog)
	if err != nil {
		return nil, err
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	return &excelPricingSnapshotPage{
		state:                 state,
		catalog:               catalog,
		rows:                  rows,
		stateRevision:         stateRevision,
		datasetRevision:       datasetRevision,
		pageRevision:          pageRevision,
		catalogMetadataDigest: excelPricingSnapshotDigest(metadataJSON),
		warningsDigest:        excelPricingSnapshotDigest(warningsJSON),
		warningCount:          warningCount,
		reconciliationCounts:  reconciliationCounts,
		page:                  pagination.Page,
		limit:                 pagination.Limit,
		total:                 pagination.Total,
		pages:                 pagination.Pages,
		hasMore:               pagination.HasMore,
	}, nil
}

func excelPricingSnapshotSourceMatches(raw json.RawMessage, expected canonical.Source) bool {
	var source map[string]json.RawMessage
	if json.Unmarshal(raw, &source) != nil || source == nil {
		return false
	}
	if excelPricingSnapshotString(source["id"]) != expected.ID ||
		excelPricingSnapshotString(source["dataset"]) != expected.Dataset ||
		excelPricingSnapshotString(source["current_revision"]) != expected.Revision ||
		excelPricingSnapshotString(source["submitted_revision"]) != expected.Revision {
		return false
	}
	if revision, present := source["revision"]; present &&
		excelPricingSnapshotString(revision) != expected.Revision {
		return false
	}
	var matches bool
	return json.Unmarshal(source["revision_matches_current"], &matches) == nil && matches
}

func excelPricingSnapshotCatalogContract(
	catalog map[string]json.RawMessage,
	expected canonical.Source,
) (excelPricingSnapshotReconciliationCounts, bool) {
	var result excelPricingSnapshotReconciliationCounts
	var columns []map[string]json.RawMessage
	if json.Unmarshal(catalog["columns"], &columns) != nil || len(columns) == 0 {
		return result, false
	}
	seenColumns := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		key := excelPricingSnapshotString(column["key"])
		if key == "" {
			return result, false
		}
		if _, exists := seenColumns[key]; exists {
			return result, false
		}
		seenColumns[key] = struct{}{}
	}
	if _, exists := seenColumns["sync_key"]; !exists {
		return result, false
	}
	if _, exists := seenColumns["reconciliation_status"]; !exists {
		return result, false
	}

	var reconciliation map[string]json.RawMessage
	if json.Unmarshal(catalog["reconciliation"], &reconciliation) != nil || reconciliation == nil {
		return result, false
	}
	var reconciledSource map[string]json.RawMessage
	if json.Unmarshal(reconciliation["source"], &reconciledSource) != nil || reconciledSource == nil {
		return result, false
	}
	if excelPricingSnapshotString(reconciledSource["id"]) != expected.ID ||
		excelPricingSnapshotString(reconciledSource["dataset"]) != expected.Dataset ||
		excelPricingSnapshotString(reconciledSource["revision"]) != expected.Revision {
		return result, false
	}

	if json.Unmarshal(reconciliation["counts"], &result) != nil ||
		result.PatrisProducts < 0 || result.WooCommerceRaw < 0 ||
		result.WooCommerceLeaves < 0 || result.UnionRows < 0 ||
		result.Matched < 0 || result.PatrisOnly < 0 || result.WooOnly < 0 ||
		result.AmbiguousCodes != 0 || result.VariableParentsExcluded < 0 {
		return result, false
	}
	return result, result.PatrisProducts == result.Matched+result.PatrisOnly &&
		result.WooCommerceLeaves == result.Matched+result.WooOnly &&
		result.WooCommerceRaw == result.WooCommerceLeaves+result.VariableParentsExcluded &&
		result.UnionRows == result.Matched+result.PatrisOnly+result.WooOnly
}

func excelPricingSnapshotTopLevelWarnings(
	state map[string]json.RawMessage,
) ([]byte, int, error) {
	raw, exists := state["warnings"]
	if !exists {
		raw = json.RawMessage("[]")
	}
	var warnings []interface{}
	if json.Unmarshal(raw, &warnings) != nil || warnings == nil {
		return nil, 0, errors.New("snapshot warnings are invalid")
	}
	for _, warning := range warnings {
		if _, _, err := excelPricingSnapshotWarningCode(warning); err != nil {
			return nil, 0, err
		}
	}
	body, err := json.Marshal(warnings)
	return body, len(warnings), err
}

func excelPricingSnapshotValidateMetadataWarnings(
	state, catalog map[string]json.RawMessage,
) error {
	metadataState := cloneExcelPricingRawMap(state)
	metadataCatalog := cloneExcelPricingRawMap(catalog)
	delete(metadataCatalog, "rows")
	catalogJSON, err := json.Marshal(metadataCatalog)
	if err != nil {
		return err
	}
	metadataState["catalog"] = catalogJSON
	var metadata interface{}
	metadataJSON, err := json.Marshal(metadataState)
	if err != nil || json.Unmarshal(metadataJSON, &metadata) != nil {
		return errors.New("snapshot metadata is invalid")
	}
	return excelPricingSnapshotScanWarnings(metadata)
}

func excelPricingSnapshotScanWarnings(value interface{}) error {
	switch typed := value.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := typed[key]
			if strings.EqualFold(key, "warnings") {
				warnings, ok := child.([]interface{})
				if !ok {
					return errors.New("snapshot warning collection is malformed")
				}
				for _, warning := range warnings {
					code, unsafe, err := excelPricingSnapshotWarningCode(warning)
					if err != nil {
						return err
					}
					if unsafe {
						return fmt.Errorf("unsafe snapshot warning %q", code)
					}
				}
				continue
			}
			if err := excelPricingSnapshotScanWarnings(child); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, child := range typed {
			if err := excelPricingSnapshotScanWarnings(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func excelPricingSnapshotWarningCode(value interface{}) (string, bool, error) {
	var code string
	switch typed := value.(type) {
	case string:
		code = strings.TrimSpace(typed)
	case map[string]interface{}:
		rawCode, ok := typed["code"]
		if !ok {
			return "", false, errors.New("snapshot warning code is missing")
		}
		code, ok = rawCode.(string)
		if !ok {
			return "", false, errors.New("snapshot warning code is malformed")
		}
		code = strings.TrimSpace(code)
	default:
		return "", false, errors.New("snapshot warning entry is malformed")
	}
	if code == "" {
		return "", false, errors.New("snapshot warning code is empty")
	}
	lower := strings.ToLower(code)
	return code,
		strings.HasPrefix(lower, "projection_integrity") ||
			strings.HasPrefix(lower, "product_type_cache_drift"),
		nil
}

func excelPricingSnapshotUnionRows(catalog map[string]json.RawMessage) (int, bool) {
	var reconciliation map[string]json.RawMessage
	var counts excelPricingSnapshotReconciliationCounts
	if json.Unmarshal(catalog["reconciliation"], &reconciliation) != nil ||
		json.Unmarshal(reconciliation["counts"], &counts) != nil || counts.UnionRows < 0 {
		return 0, false
	}
	return counts.UnionRows, true
}

func excelPricingSnapshotStableCatalogMetadata(
	catalog map[string]json.RawMessage,
) (map[string]json.RawMessage, error) {
	metadata := make(map[string]json.RawMessage, 5)
	for _, key := range []string{"dataset", "dataset_revision", "columns"} {
		raw, exists := catalog[key]
		if !exists {
			return nil, errors.New("snapshot catalog metadata is incomplete")
		}
		metadata[key] = append(json.RawMessage(nil), raw...)
	}
	var reconciliation map[string]json.RawMessage
	if json.Unmarshal(catalog["reconciliation"], &reconciliation) != nil || reconciliation == nil {
		return nil, errors.New("snapshot reconciliation metadata is invalid")
	}
	for _, key := range []string{"source", "counts"} {
		raw, exists := reconciliation[key]
		if !exists {
			return nil, errors.New("snapshot reconciliation metadata is incomplete")
		}
		metadata["reconciliation_"+key] = append(json.RawMessage(nil), raw...)
	}
	return metadata, nil
}

func buildExcelPricingSnapshotFromRemoteResult(
	result *excelPricingRemoteSnapshotResult,
	projection string,
	now time.Time,
) (*excelPricingSnapshot, error) {
	if result == nil || !validExcelPricingRemoteSource(result.Source) ||
		!validExcelPricingRemoteRevisionParts(
			result.CompositeStateRevision,
			result.PricingStateRevision,
			result.PricingPolicyRevision,
			result.CatalogRevision,
			result.DatasetRevision,
			result.SnapshotRevision,
			result.MutationStateRevision,
		) ||
		result.MutationStateRevision != result.PricingStateRevision ||
		!isStrongExcelPricingRevisionETag(result.ETag, result.SnapshotRevision) {
		return nil, errExcelPricingRemoteSnapshotIntegrity
	}
	var remote excelPricingRemoteSnapshotPayload
	if len(result.RawPayload) == 0 || json.Unmarshal(result.RawPayload, &remote) != nil ||
		remote.Source != result.Source ||
		remote.StateRevision != result.CompositeStateRevision ||
		remote.PricingStateRevision != result.PricingStateRevision ||
		remote.PricingPolicyRevision != result.PricingPolicyRevision ||
		remote.CatalogRevision != result.CatalogRevision ||
		remote.DatasetRevision != result.DatasetRevision ||
		remote.SnapshotRevision != result.SnapshotRevision ||
		remote.MutationGuard.ExpectedStateRevision != result.MutationStateRevision ||
		remote.RowCount != len(result.Rows) ||
		remote.RowCount > excelPricingSnapshotPageSize*excelPricingSnapshotMaxPages ||
		remote.PageCount < 1 || remote.PageCount > excelPricingSnapshotMaxPages ||
		remote.Integrity.RowCount != len(result.Rows) {
		return nil, errExcelPricingRemoteSnapshotIntegrity
	}

	var rows []json.RawMessage
	var rowFields []string
	switch projection {
	case excelPricingSnapshotProjectionFull:
		rows = append([]json.RawMessage(nil), result.Rows...)
	case excelPricingSnapshotProjectionExcelV1:
		if len(result.ProjectedRows) != len(result.Rows) ||
			len(result.ProjectedRowFields) != len(excelPricingSnapshotExcelV1RowFields) {
			return nil, errExcelPricingRemoteSnapshotIntegrity
		}
		for index, expected := range excelPricingSnapshotExcelV1RowFields {
			if result.ProjectedRowFields[index] != expected {
				return nil, errExcelPricingRemoteSnapshotIntegrity
			}
		}
		rows = append([]json.RawMessage(nil), result.ProjectedRows...)
		rowFields = append([]string(nil), result.ProjectedRowFields...)
	default:
		return nil, errExcelPricingRemoteSnapshotConfiguration
	}

	var reconciliation excelPricingRemoteSnapshotReconciliation
	if json.Unmarshal(remote.Reconciliation, &reconciliation) != nil {
		return nil, errExcelPricingRemoteSnapshotIntegrity
	}
	warnings := reconciliation.Warnings
	if warnings == nil {
		warnings = []interface{}{}
	}
	warningsJSON, err := json.Marshal(warnings)
	if err != nil {
		return nil, errExcelPricingRemoteSnapshotIntegrity
	}

	type adaptedSource struct {
		ID                     string `json:"id"`
		Dataset                string `json:"dataset"`
		CurrentRevision        string `json:"current_revision"`
		SubmittedRevision      string `json:"submitted_revision"`
		RevisionMatchesCurrent bool   `json:"revision_matches_current"`
	}
	type adaptedCatalog struct {
		Dataset         string                         `json:"dataset"`
		DatasetRevision string                         `json:"dataset_revision"`
		Columns         json.RawMessage                `json:"columns"`
		Reconciliation  json.RawMessage                `json:"reconciliation"`
		Rows            []json.RawMessage              `json:"rows"`
		Pagination      excelPricingSnapshotPagination `json:"pagination"`
	}
	state := struct {
		Schema                string          `json:"schema"`
		StateRevision         string          `json:"state_revision"`
		PricingStateRevision  string          `json:"pricing_state_revision"`
		PricingPolicyRevision string          `json:"pricing_policy_revision"`
		CatalogRevision       string          `json:"catalog_revision"`
		Status                string          `json:"status"`
		Warnings              []interface{}   `json:"warnings"`
		Source                adaptedSource   `json:"source"`
		Settings              json.RawMessage `json:"settings"`
		Catalog               adaptedCatalog  `json:"catalog"`
	}{
		Schema:                excelPricingStateSchema,
		StateRevision:         result.CompositeStateRevision,
		PricingStateRevision:  result.PricingStateRevision,
		PricingPolicyRevision: result.PricingPolicyRevision,
		CatalogRevision:       result.CatalogRevision,
		Status:                "ready",
		Warnings:              warnings,
		Source: adaptedSource{
			ID:                     result.Source.ID,
			Dataset:                result.Source.Dataset,
			CurrentRevision:        result.Source.Revision,
			SubmittedRevision:      result.Source.Revision,
			RevisionMatchesCurrent: true,
		},
		Settings: append(json.RawMessage(nil), remote.Settings...),
		Catalog: adaptedCatalog{
			Dataset:         remote.Catalog.Dataset,
			DatasetRevision: remote.Catalog.DatasetRevision,
			Columns:         append(json.RawMessage(nil), remote.Catalog.Columns...),
			Reconciliation:  append(json.RawMessage(nil), remote.Catalog.Reconciliation...),
			Rows:            rows,
			Pagination: excelPricingSnapshotPagination{
				Page: 1, Limit: len(rows), Total: len(rows), Pages: 1, HasMore: false,
			},
		},
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, errExcelPricingRemoteSnapshotIntegrity
	}
	integrity := excelPricingSnapshotIntegrity{
		Algorithm:             "sha256",
		StateDigest:           excelPricingSnapshotDigest(stateJSON),
		CatalogMetadataDigest: remote.Integrity.CatalogMetadataDigest,
		PageRevisionsDigest:   remote.Integrity.PageRevisionsDigest,
		WarningsDigest:        excelPricingSnapshotDigest(warningsJSON),
		DatasetRevision:       result.DatasetRevision,
		RemoteTotal:           remote.RemoteTotal,
		RowCount:              len(rows),
		DistinctSyncKeys:      len(rows),
		PageCount:             remote.PageCount,
		WarningCount:          len(warnings),
	}
	expiresAt := now.Add(excelPricingSnapshotMaxCacheAge)
	revisionInput, _ := json.Marshal(struct {
		Source        canonical.Source              `json:"source"`
		StateRevision string                        `json:"state_revision"`
		Projection    string                        `json:"projection"`
		Integrity     excelPricingSnapshotIntegrity `json:"integrity"`
	}{result.Source, result.CompositeStateRevision, projection, integrity})
	snapshotRevision := excelPricingSnapshotDigest(revisionInput)
	payload := excelPricingSnapshotPayload{
		Schema:           excelPricingSnapshotPayloadSchema,
		Projection:       projection,
		RowFields:        rowFields,
		SnapshotRevision: snapshotRevision,
		Source:           result.Source,
		StateRevision:    result.CompositeStateRevision,
		CreatedAt:        now,
		ExpiresAt:        expiresAt,
		Integrity:        integrity,
		// The loopback contract intentionally keeps the composite revision as
		// the workbook guard. The remote pricing-only revision remains separately
		// validated above and in the adapted state's internal metadata.
		MutationGuard: excelPricingSnapshotMutationGuard{
			ExpectedStateRevision: result.CompositeStateRevision,
			Preview: excelPricingSnapshotMutationOperation{
				Method:                 http.MethodPost,
				Path:                   "/api/pricing-sync/preview",
				RequiresIdempotencyKey: true,
				RequiresIfMatch:        true,
			},
			Apply: excelPricingSnapshotMutationOperation{
				Method:                 http.MethodPost,
				Path:                   "/api/pricing-sync/apply",
				RequiresIdempotencyKey: true,
				RequiresIfMatch:        true,
				Confirmation:           "APPLY",
			},
		},
		State: stateJSON,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errExcelPricingRemoteSnapshotIntegrity
	}
	return &excelPricingSnapshot{
		revision:                snapshotRevision,
		etagRevision:            excelPricingSnapshotDigest(body),
		stateRevision:           result.CompositeStateRevision,
		pricingStateRevision:    result.PricingStateRevision,
		upstreamCatalogRevision: result.CatalogRevision,
		createdAt:               now,
		expiresAt:               expiresAt,
		integrity:               integrity,
		body:                    body,
	}, nil
}

func buildExcelPricingSnapshot(
	first *excelPricingSnapshotPage,
	source canonical.Source,
	rows []json.RawMessage,
	pageRevisions []string,
	projection string,
	now time.Time,
) (*excelPricingSnapshot, error) {
	rows, rowFields, err := projectExcelPricingSnapshotRows(rows, projection)
	if err != nil {
		return nil, err
	}
	state := cloneExcelPricingRawMap(first.state)
	catalog := cloneExcelPricingRawMap(first.catalog)
	rowsJSON, err := json.Marshal(rows)
	if err != nil {
		return nil, err
	}
	paginationJSON, err := json.Marshal(excelPricingSnapshotPagination{
		Page: 1, Limit: len(rows), Total: len(rows), Pages: 1, HasMore: false,
	})
	if err != nil {
		return nil, err
	}
	catalog["rows"] = rowsJSON
	catalog["pagination"] = paginationJSON
	delete(catalog, "page_revision")
	catalogJSON, err := json.Marshal(catalog)
	if err != nil {
		return nil, err
	}
	state["catalog"] = catalogJSON
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	pageRevisionJSON, _ := json.Marshal(pageRevisions)
	integrity := excelPricingSnapshotIntegrity{
		Algorithm:             "sha256",
		StateDigest:           excelPricingSnapshotDigest(stateJSON),
		CatalogMetadataDigest: first.catalogMetadataDigest,
		PageRevisionsDigest:   excelPricingSnapshotDigest(pageRevisionJSON),
		WarningsDigest:        first.warningsDigest,
		DatasetRevision:       first.datasetRevision,
		RemoteTotal:           first.total,
		RowCount:              len(rows),
		DistinctSyncKeys:      len(rows),
		PageCount:             first.pages,
		WarningCount:          first.warningCount,
	}
	expiresAt := now.Add(excelPricingSnapshotMaxCacheAge)
	revisionInput, _ := json.Marshal(struct {
		Source        canonical.Source              `json:"source"`
		StateRevision string                        `json:"state_revision"`
		Projection    string                        `json:"projection"`
		Integrity     excelPricingSnapshotIntegrity `json:"integrity"`
	}{source, first.stateRevision, projection, integrity})
	snapshotRevision := excelPricingSnapshotDigest(revisionInput)
	payload := excelPricingSnapshotPayload{
		Schema:           excelPricingSnapshotPayloadSchema,
		Projection:       projection,
		RowFields:        rowFields,
		SnapshotRevision: snapshotRevision,
		Source:           source,
		StateRevision:    first.stateRevision,
		CreatedAt:        now,
		ExpiresAt:        expiresAt,
		Integrity:        integrity,
		MutationGuard: excelPricingSnapshotMutationGuard{
			ExpectedStateRevision: first.stateRevision,
			Preview: excelPricingSnapshotMutationOperation{
				Method:                 http.MethodPost,
				Path:                   "/api/pricing-sync/preview",
				RequiresIdempotencyKey: true,
				RequiresIfMatch:        true,
			},
			Apply: excelPricingSnapshotMutationOperation{
				Method:                 http.MethodPost,
				Path:                   "/api/pricing-sync/apply",
				RequiresIdempotencyKey: true,
				RequiresIfMatch:        true,
				Confirmation:           "APPLY",
			},
		},
		State: stateJSON,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &excelPricingSnapshot{
		revision:      snapshotRevision,
		etagRevision:  excelPricingSnapshotDigest(body),
		stateRevision: first.stateRevision,
		createdAt:     now,
		expiresAt:     expiresAt,
		integrity:     integrity,
		body:          body,
	}, nil
}

func (s *Server) updateExcelPricingSnapshotProgress(
	jobID, phase string,
	completed, total, rows int,
) {
	store := s.excelPricing.snapshots
	store.mu.Lock()
	defer store.mu.Unlock()
	updatedAt := store.now().UTC()
	changedJobs := make([]*excelPricingSnapshotJob, 0)
	if job := store.jobs[jobID]; job != nil && job.status == "running" {
		semanticChange := job.progress.Phase != phase ||
			job.progress.CompletedPages != completed ||
			job.progress.TotalPages != total || job.progress.Rows != rows
		job.progress = excelPricingSnapshotProgress{
			Phase:          phase,
			CompletedPages: completed,
			TotalPages:     total,
			Rows:           rows,
			HeartbeatAt:    updatedAt,
		}
		job.updatedAt = updatedAt
		if semanticChange {
			changedJobs = append(changedJobs, job)
		}
	}
	for _, follower := range store.jobs {
		if follower != nil && follower.leaderJobID == jobID && follower.status == "running" {
			semanticChange := follower.progress.Phase != phase ||
				follower.progress.CompletedPages != completed ||
				follower.progress.TotalPages != total || follower.progress.Rows != rows
			follower.progress = excelPricingSnapshotProgress{
				Phase:          phase,
				CompletedPages: completed,
				TotalPages:     total,
				Rows:           rows,
				HeartbeatAt:    updatedAt,
			}
			follower.updatedAt = updatedAt
			if semanticChange {
				changedJobs = append(changedJobs, follower)
			}
		}
	}
	for _, changedJob := range changedJobs {
		store.publishJobLocked(changedJob)
	}
}

func (s *Server) setExcelPricingSnapshotFailure(jobID, stage, code string) {
	if s == nil || s.excelPricing == nil || s.excelPricing.snapshots == nil ||
		strings.TrimSpace(jobID) == "" || strings.TrimSpace(stage) == "" ||
		strings.TrimSpace(code) == "" || !validExcelPricingSnapshotFailure(stage, code) {
		return
	}
	failure := excelPricingSnapshotFailure{
		Schema: excelPricingSnapshotFailureSchema,
		Stage:  stage,
		Code:   code,
	}
	store := s.excelPricing.snapshots
	store.mu.Lock()
	defer store.mu.Unlock()
	if job := store.jobs[jobID]; job != nil && job.status == "running" {
		copyFailure := failure
		job.failure = &copyFailure
	}
	for _, follower := range store.jobs {
		if follower == nil || follower.leaderJobID != jobID ||
			follower.status != "running" {
			continue
		}
		copyFailure := failure
		follower.failure = &copyFailure
	}
}

func validExcelPricingSnapshotFailure(stage, code string) bool {
	switch stage {
	case excelPricingRemoteSnapshotStageRevisionFetch:
		return code == "snapshot_revision_fetch_protocol_failed" ||
			code == "snapshot_revision_fetch_configuration_failed" ||
			code == "snapshot_revision_fetch_unavailable"
	case excelPricingRemoteSnapshotStageTerminalSubscription:
		return code == "snapshot_terminal_subscription_failed"
	case excelPricingRemoteSnapshotStageSnapshotStart:
		return code == "snapshot_start_protocol_failed" ||
			code == "snapshot_start_configuration_failed" ||
			code == "snapshot_start_rejected" ||
			code == "snapshot_start_unavailable"
	case excelPricingRemoteSnapshotStageTerminalWait:
		return code == "snapshot_terminal_wait_failed"
	case excelPricingRemoteSnapshotStageTerminalMatch:
		return code == "snapshot_terminal_match_failed"
	case excelPricingRemoteSnapshotStageRemoteTerminal:
		return code == "snapshot_remote_terminal_failed"
	case excelPricingRemoteSnapshotStageSnapshotPayload:
		return code == "snapshot_payload_integrity_failed" ||
			code == "snapshot_payload_shape_or_counts_failed" ||
			code == "snapshot_payload_mutation_guard_failed" ||
			code == "snapshot_payload_columns_failed" ||
			code == "snapshot_payload_rows_or_reconciliation_failed" ||
			code == "snapshot_payload_integrity_metadata_failed" ||
			code == "snapshot_payload_digest_failed" ||
			strings.HasPrefix(code, "snapshot_payload_page_digest_") ||
			code == "snapshot_payload_page_revisions_digest_mismatch" ||
			code == "snapshot_payload_catalog_metadata_digest_mismatch" ||
			code == "snapshot_payload_state_digest_mismatch" ||
			code == "snapshot_payload_snapshot_digest_mismatch" ||
			code == "snapshot_payload_protocol_failed" ||
			code == "snapshot_payload_configuration_failed" ||
			code == "snapshot_payload_unavailable"
	case excelPricingSnapshotStageRemoteConfiguration:
		return code == "snapshot_remote_configuration_failed"
	case excelPricingSnapshotStageLocalProjection:
		return code == "snapshot_local_projection_integrity_failed"
	default:
		return false
	}
}

func excelPricingSnapshotJobResponse(job *excelPricingSnapshotJob) map[string]interface{} {
	response := map[string]interface{}{
		"schema":         excelPricingSnapshotJobSchema,
		"job_id":         job.id,
		"request_id":     job.requestID,
		"status":         job.status,
		"source":         job.source,
		"locale":         job.locale,
		"projection":     job.projection,
		"created_at":     job.createdAt,
		"updated_at":     job.updatedAt,
		"deadline":       job.deadline,
		"progress":       job.progress,
		"cached":         job.cached,
		"coalesced":      job.coalesced,
		"event_sequence": job.eventSequence,
		"capacity": excelPricingSnapshotCapacity{
			PageSize: excelPricingSnapshotPageSize,
			MaxPages: excelPricingSnapshotMaxPages,
			MaxRows:  excelPricingSnapshotPageSize * excelPricingSnapshotMaxPages,
		},
		"status_url":               "/api/pricing-sync/snapshots/" + job.id,
		"wait_url":                 "/api/pricing-sync/snapshots/" + job.id + "?wait=terminal",
		"events_url":               "/api/pricing-sync/events",
		"job_events_url":           "/api/pricing-sync/snapshots/" + job.id,
		"events_accept":            "text/event-stream",
		"events_cursor_header":     "Last-Event-ID",
		"events_keepalive_seconds": int(excelPricingSnapshotSSEHeartbeat / time.Second),
		"events_history_capacity":  excelPricingSnapshotEventHistory,
		"events_lifecycle":         "session_scoped_durable",
		"job_events_lifecycle":     "job_scoped_progress",
		"event_schema":             excelPricingSnapshotEventSchema,
		"payload_url":              "/api/pricing-sync/snapshots/" + job.id + "/payload",
		"cancel_url":               "/api/pricing-sync/snapshots/" + job.id,
	}
	if job.errorCode != "" {
		response["code"] = job.errorCode
	}
	if job.failure != nil {
		response["failure"] = *job.failure
	}
	if job.snapshot != nil {
		response["snapshot_revision"] = job.snapshot.revision
		response["state_revision"] = job.snapshot.stateRevision
		response["pricing_state_revision"] = job.snapshot.pricingStateRevision
		response["etag"] = excelPricingSnapshotETag(job.snapshot.etagRevision)
		response["expires_at"] = job.snapshot.expiresAt
		response["integrity"] = job.snapshot.integrity
		response["identity"] = excelPricingSnapshotIdentity{
			Source:           job.source,
			CatalogRevision:  job.snapshot.integrity.DatasetRevision,
			StateRevision:    job.snapshot.stateRevision,
			SnapshotRevision: job.snapshot.revision,
			ETag:             excelPricingSnapshotETag(job.snapshot.etagRevision),
		}
	}
	return response
}

func writeExcelPricingSnapshotBusy(w http.ResponseWriter) {
	writeExcelPricingBusy(w, "pricing_snapshot_busy")
}

func writeExcelPricingBusy(w http.ResponseWriter, code string) {
	w.Header().Set("Retry-After", "1")
	writeExcelPricingJSON(w, http.StatusTooManyRequests, map[string]interface{}{
		"success":        false,
		"code":           code,
		"retry_after_ms": excelPricingSnapshotRetryAfterMS,
	})
}

func excelPricingSnapshotRequestFingerprint(request excelPricingSnapshotStartRequest) string {
	request.Projection = excelPricingSnapshotProjection(request.Projection)
	body, _ := json.Marshal(request)
	return excelPricingSnapshotDigest(body)
}

func excelPricingSnapshotCacheKey(source canonical.Source, locale, projection string) string {
	body, _ := json.Marshal(struct {
		Source     canonical.Source `json:"source"`
		Locale     string           `json:"locale"`
		Projection string           `json:"projection"`
	}{source, locale, excelPricingSnapshotProjection(projection)})
	return excelPricingSnapshotDigest(body)
}

func excelPricingSnapshotProjection(projection string) string {
	if projection == excelPricingSnapshotProjectionExcelV1 {
		return excelPricingSnapshotProjectionExcelV1
	}
	return excelPricingSnapshotProjectionFull
}

func projectExcelPricingSnapshotRows(
	rows []json.RawMessage,
	projection string,
) ([]json.RawMessage, []string, error) {
	if projection != excelPricingSnapshotProjectionExcelV1 {
		return rows, nil, nil
	}
	projected := make([]json.RawMessage, len(rows))
	nullValue := json.RawMessage("null")
	for index, row := range rows {
		var object map[string]json.RawMessage
		if json.Unmarshal(row, &object) != nil || object == nil {
			return nil, nil, errors.New("snapshot projection row is invalid")
		}
		if excelPricingSnapshotString(object["sync_key"]) == "" {
			return nil, nil, errors.New("snapshot projection row identity is missing")
		}
		switch excelPricingSnapshotString(object["reconciliation_status"]) {
		case "matched", "patris_only", "woo_only":
		default:
			return nil, nil, errors.New("snapshot projection row status is invalid")
		}
		lean := make([]json.RawMessage, len(excelPricingSnapshotExcelV1RowFields))
		for fieldIndex, field := range excelPricingSnapshotExcelV1RowFields {
			if raw, exists := object[field]; exists && len(raw) > 0 {
				if !excelPricingSnapshotScalarJSON(raw) {
					return nil, nil, errors.New("snapshot projection field is not scalar")
				}
				lean[fieldIndex] = append(json.RawMessage(nil), raw...)
			} else {
				lean[fieldIndex] = append(json.RawMessage(nil), nullValue...)
			}
		}
		encoded, err := json.Marshal(lean)
		if err != nil {
			return nil, nil, err
		}
		projected[index] = encoded
	}
	return projected, append([]string(nil), excelPricingSnapshotExcelV1RowFields...), nil
}

func excelPricingSnapshotScalarJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && json.Valid(trimmed) && trimmed[0] != '{' && trimmed[0] != '['
}

func excelPricingSnapshotIdempotencyKey(owner [sha256.Size]byte, requestID string) string {
	return hex.EncodeToString(owner[:]) + "|" + requestID
}

func excelPricingSnapshotPageRequestID(jobID string, page int) string {
	digest := sha256.Sum256([]byte(jobID + "|" + strconv.Itoa(page)))
	return "snapshot-state-" + hex.EncodeToString(digest[:12]) + "-" + strconv.Itoa(page)
}

func excelPricingSnapshotDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func excelPricingSnapshotContextCode(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "snapshot_timeout"
	}
	return "request_cancelled"
}

func excelPricingSnapshotFailureCode(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "digitalogic_reconciled_snapshot_changed" {
		return "snapshot_revision_changed"
	}
	return code
}

func excelPricingSnapshotETag(revision string) string {
	return `"` + revision + `"`
}

func excelPricingSnapshotETagMatches(values []string, expected string) bool {
	for _, value := range values {
		for _, candidate := range strings.Split(value, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" || candidate == expected || strings.TrimPrefix(candidate, "W/") == expected {
				return true
			}
		}
	}
	return false
}

func excelPricingSnapshotString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return strings.TrimSpace(value)
}

func cloneExcelPricingRawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	clone := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		clone[key] = append(json.RawMessage(nil), value...)
	}
	return clone
}

func cloneExcelPricingSnapshotIdentity(
	identity *excelPricingSnapshotIdentity,
) *excelPricingSnapshotIdentity {
	if identity == nil {
		return nil
	}
	copyIdentity := *identity
	return &copyIdentity
}

func excelPricingSnapshotIdentityFromChange(
	event *excelPricingStateChangeEvent,
) *excelPricingSnapshotIdentity {
	if event == nil || event.Source == nil || event.CatalogRevision == "" ||
		event.StateRevision == "" || event.SnapshotRevision == "" || event.ETag == "" {
		return nil
	}
	return &excelPricingSnapshotIdentity{
		Source:           *event.Source,
		CatalogRevision:  event.CatalogRevision,
		StateRevision:    event.StateRevision,
		SnapshotRevision: event.SnapshotRevision,
		ETag:             event.ETag,
	}
}

func cloneExcelPricingStateChangeEvent(
	event excelPricingStateChangeEvent,
) excelPricingStateChangeEvent {
	copyEvent := event
	if event.Source != nil {
		source := *event.Source
		copyEvent.Source = &source
	}
	if event.PreviousSource != nil {
		source := *event.PreviousSource
		copyEvent.PreviousSource = &source
	}
	copyEvent.Identity = cloneExcelPricingSnapshotIdentity(event.Identity)
	copyEvent.InvalidatedIdentity = cloneExcelPricingSnapshotIdentity(event.InvalidatedIdentity)
	return copyEvent
}

func cloneExcelPricingSnapshotEventEnvelope(
	event excelPricingSnapshotEventEnvelope,
) excelPricingSnapshotEventEnvelope {
	copyEvent := event
	if event.Job != nil {
		copyEvent.Job = make(map[string]interface{}, len(event.Job))
		for key, value := range event.Job {
			copyEvent.Job[key] = value
		}
	}
	if event.Change != nil {
		change := cloneExcelPricingStateChangeEvent(*event.Change)
		copyEvent.Change = &change
	}
	return copyEvent
}
