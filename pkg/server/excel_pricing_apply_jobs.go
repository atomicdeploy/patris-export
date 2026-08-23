package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
	"github.com/gorilla/mux"
)

const (
	excelPricingApplyLedgerSchema = "patris.pricing-apply-ledger"
	excelPricingApplyJobSchema    = "patris.pricing-apply-job"
	excelPricingRemoteApplySchema = "digitalogic.pricing-apply-job"
	excelPricingApplyEventSchema  = "digitalogic.pricing-apply-terminal"
	excelPricingApplyEventName    = "pricing.apply.terminal"

	excelPricingApplyLedgerFilename  = "pricing-apply-jobs.json"
	excelPricingApplyJobRetention    = 7 * 24 * time.Hour
	excelPricingApplyMaxJobs         = 128
	excelPricingApplyMaxEventHistory = 256
	excelPricingApplyRemoteMaxBytes  = 2 << 20
)

var excelPricingRemoteApplyJobPattern = regexp.MustCompile(`^currency-[a-f0-9]{32}$`)

type excelPricingApplyJob struct {
	RequestID              string           `json:"request_id"`
	Fingerprint            string           `json:"fingerprint"`
	Source                 canonical.Source `json:"source"`
	ExpectedStateRevision  string           `json:"expected_state_revision"`
	PreviewDigest          string           `json:"preview_digest"`
	JobID                  string           `json:"job_id,omitempty"`
	RemoteStatus           string           `json:"remote_status,omitempty"`
	RemoteStatusPath       string           `json:"remote_status_path,omitempty"`
	RemoteCancelPath       string           `json:"remote_cancel_path,omitempty"`
	Status                 string           `json:"status"`
	Terminal               bool             `json:"terminal"`
	ReadbackRequired       bool             `json:"readback_required"`
	Code                   string           `json:"code,omitempty"`
	StateRevision          string           `json:"state_revision,omitempty"`
	ResultDigest           string           `json:"result_digest,omitempty"`
	RemotePostStarted      bool             `json:"remote_post_started"`
	RemoteResponseAccepted bool             `json:"remote_response_accepted"`
	LastReconcileReason    string           `json:"last_reconcile_reason,omitempty"`
	LastReconciledAt       time.Time        `json:"last_reconciled_at,omitempty"`
	FinalizationState      string           `json:"finalization_state,omitempty"`
	FinalizationAttempts   int              `json:"finalization_attempts,omitempty"`
	DeliveryEventID        string           `json:"delivery_event_id,omitempty"`
	LocalEventPublished    bool             `json:"local_event_published"`
	CreatedAt              time.Time        `json:"created_at"`
	UpdatedAt              time.Time        `json:"updated_at"`
}

type excelPricingApplyLedger struct {
	Schema     string                           `json:"schema"`
	UpdatedAt  time.Time                        `json:"updated_at"`
	Jobs       map[string]*excelPricingApplyJob `json:"jobs"`
	Aliases    map[string]string                `json:"aliases"`
	SeenEvents map[string]string                `json:"seen_events"`
	EventOrder []string                         `json:"event_order"`
}

type excelPricingApplyJobStore struct {
	mu      sync.Mutex
	path    string
	now     func() time.Time
	ledger  excelPricingApplyLedger
	loadErr error
}

type excelPricingApplyReservation int

const (
	excelPricingApplyReservationNew excelPricingApplyReservation = iota
	excelPricingApplyReservationReplay
	excelPricingApplyReservationConflict
)

type excelPricingRemoteApplyJob struct {
	Schema                string                   `json:"schema"`
	JobID                 string                   `json:"job_id"`
	RequestID             string                   `json:"request_id"`
	IdempotencyKey        string                   `json:"idempotency_key"`
	Status                string                   `json:"status"`
	Terminal              bool                     `json:"terminal"`
	ExpectedStateRevision string                   `json:"expected_state_revision"`
	PreviewDigest         string                   `json:"preview_digest"`
	Source                canonical.Source         `json:"source"`
	CreatedAt             string                   `json:"created_at"`
	UpdatedAt             string                   `json:"updated_at"`
	HeartbeatAt           string                   `json:"heartbeat_at"`
	DeadlineAt            string                   `json:"deadline_at"`
	Progress              map[string]interface{}   `json:"progress"`
	CancelRequested       bool                     `json:"cancel_requested"`
	Replayed              bool                     `json:"replayed"`
	Coalesced             bool                     `json:"coalesced"`
	RetryAfter            *int                     `json:"retry_after"`
	ReadbackRequired      bool                     `json:"readback_required"`
	TerminalReason        string                   `json:"terminal_reason"`
	Error                 *excelPricingJobError    `json:"error"`
	EventDelivery         map[string]interface{}   `json:"event_delivery"`
	StatusURL             string                   `json:"status_url"`
	CancelURL             string                   `json:"cancel_url"`
	Result                *excelPricingApplyResult `json:"result"`
	Accepted              *bool                    `json:"accepted,omitempty"`
}

type excelPricingJobError struct {
	Code string `json:"code"`
}

type excelPricingApplyResult struct {
	Schema         string           `json:"schema"`
	Mode           string           `json:"mode"`
	Status         string           `json:"status"`
	StateRevision  string           `json:"state_revision"`
	Source         canonical.Source `json:"source"`
	ClientID       string           `json:"client_id"`
	Channel        string           `json:"channel"`
	RequestID      string           `json:"request_id"`
	PreviewDigest  string           `json:"preview_digest"`
	Settings       json.RawMessage  `json:"settings"`
	ExpiresAt      string           `json:"expires_at"`
	Warnings       json.RawMessage  `json:"warnings"`
	ProductResults json.RawMessage  `json:"product_results"`
}

type excelPricingRemoteApplyTerminalEvent struct {
	Schema           string                    `json:"schema"`
	Projection       string                    `json:"projection"`
	JobID            string                    `json:"job_id"`
	RequestIDs       []string                  `json:"request_ids"`
	PrimaryRequestID string                    `json:"primary_request_id"`
	Status           string                    `json:"status"`
	Source           canonical.Source          `json:"source"`
	PreviewDigest    string                    `json:"preview_digest"`
	StateRevision    string                    `json:"state_revision,omitempty"`
	ResultDigest     string                    `json:"result_digest"`
	ReadbackRequired bool                      `json:"readback_required"`
	Retryable        bool                      `json:"retryable"`
	Code             string                    `json:"code"`
	StatusPath       string                    `json:"status_path"`
	RevisionPath     string                    `json:"revision_path"`
	IdempotencyKey   string                    `json:"idempotency_key"`
	Audience         excelPricingApplyAudience `json:"audience"`
	EventID          uint64                    `json:"-"`
}

type excelPricingApplyAudience struct {
	Services []string `json:"services"`
}

func newExcelPricingApplyJobStore(path string, now func() time.Time) *excelPricingApplyJobStore {
	if now == nil {
		now = time.Now
	}
	store := &excelPricingApplyJobStore{
		path: strings.TrimSpace(path),
		now:  now,
		ledger: excelPricingApplyLedger{
			Schema:     excelPricingApplyLedgerSchema,
			Jobs:       make(map[string]*excelPricingApplyJob),
			Aliases:    make(map[string]string),
			SeenEvents: make(map[string]string),
			EventOrder: []string{},
		},
	}
	store.loadErr = store.load()
	return store
}

func excelPricingApplyJobStatePath(manager *appconfig.Manager, dbPath string) string {
	if manager != nil && strings.TrimSpace(manager.Path()) != "" {
		return filepath.Join(filepath.Dir(manager.Path()), excelPricingApplyLedgerFilename)
	}
	if strings.TrimSpace(dbPath) == "" || strings.Contains(dbPath, "://") {
		return ""
	}
	absolute, err := filepath.Abs(dbPath)
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(absolute), excelPricingApplyLedgerFilename)
}

func (store *excelPricingApplyJobStore) load() error {
	if store.path == "" {
		return errors.New("pricing apply durable state path is unavailable")
	}
	body, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var ledger excelPricingApplyLedger
	if len(body) == 0 || json.Unmarshal(body, &ledger) != nil ||
		ledger.Schema != excelPricingApplyLedgerSchema || ledger.Jobs == nil ||
		ledger.Aliases == nil || ledger.SeenEvents == nil || ledger.EventOrder == nil {
		return errors.New("pricing apply durable state is invalid")
	}
	for requestID, job := range ledger.Jobs {
		if requestID == "" || job == nil || job.RequestID != requestID ||
			validatePersistedExcelPricingApplyJob(*job) != nil {
			return errors.New("pricing apply durable job is invalid")
		}
		if job.FinalizationState == "running" {
			// A persisted running claim belongs to the previous process. The
			// canonical receiver deduplicates the stable delivery event identity,
			// so recovery may resume without repeating the pricing mutation.
			job.FinalizationState = "retryable"
		}
	}
	store.ledger = ledger
	store.pruneLocked(store.now().UTC())
	return nil
}

func (store *excelPricingApplyJobStore) ready() error {
	if store == nil || store.path == "" {
		return errors.New("pricing apply durable state is unavailable")
	}
	return store.loadErr
}

func (store *excelPricingApplyJobStore) reserve(
	request excelPricingLocalRequest,
	source canonical.Source,
	fingerprint string,
) (excelPricingApplyReservation, *excelPricingApplyJob, error) {
	if err := store.ready(); err != nil {
		return excelPricingApplyReservationConflict, nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.pruneLocked(store.now().UTC())
	if existing := store.ledger.Jobs[request.RequestID]; existing != nil {
		if existing.Fingerprint != fingerprint || existing.Source != source ||
			existing.ExpectedStateRevision != request.ExpectedStateRevision ||
			existing.PreviewDigest != request.PreviewDigest {
			return excelPricingApplyReservationConflict, nil, nil
		}
		return excelPricingApplyReservationReplay, cloneExcelPricingApplyJob(existing), nil
	}
	if len(store.ledger.Jobs) >= excelPricingApplyMaxJobs {
		return excelPricingApplyReservationConflict, nil, errors.New("pricing apply durable state is full")
	}
	now := store.now().UTC()
	job := &excelPricingApplyJob{
		RequestID:             request.RequestID,
		Fingerprint:           fingerprint,
		Source:                source,
		ExpectedStateRevision: request.ExpectedStateRevision,
		PreviewDigest:         request.PreviewDigest,
		Status:                "admitting",
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if err := store.mutateLocked(func(ledger *excelPricingApplyLedger) error {
		ledger.Jobs[job.RequestID] = job
		ledger.Aliases[job.RequestID] = job.RequestID
		return nil
	}); err != nil {
		return excelPricingApplyReservationConflict, nil, err
	}
	return excelPricingApplyReservationNew, cloneExcelPricingApplyJob(job), nil
}

func (store *excelPricingApplyJobStore) markPostStarted(requestID string) error {
	return store.updateJob(requestID, func(job *excelPricingApplyJob) error {
		if job.RemotePostStarted {
			return errors.New("pricing apply remote POST was already started")
		}
		job.RemotePostStarted = true
		job.Status = "admitting"
		return nil
	})
}

func (store *excelPricingApplyJobStore) markAdmissionUnknown(requestID, code string) error {
	return store.updateJob(requestID, func(job *excelPricingApplyJob) error {
		// A terminal event or status read may win the race with the original
		// admission response. Never let the later transport error regress that
		// durable result back to an unknown admission.
		if job.Terminal || job.Status == "finalizing" {
			return nil
		}
		job.Status = "admission_unknown"
		job.RemoteStatus = ""
		job.Terminal = false
		job.ReadbackRequired = true
		job.Code = safeExcelPricingApplyCode(code)
		return nil
	})
}

func (store *excelPricingApplyJobStore) acceptRemote(
	requestID string,
	remote excelPricingRemoteApplyJob,
) (*excelPricingApplyJob, error) {
	var accepted *excelPricingApplyJob
	err := store.updateJob(requestID, func(job *excelPricingApplyJob) error {
		if err := validateExcelPricingRemoteApplyJob(remote, *job); err != nil {
			return err
		}
		// Remote status reconciliation and the authenticated terminal stream can
		// race. Durable finalization/terminal state is monotonic: a stale active
		// response must not reopen an already accepted terminal result.
		if job.Terminal || job.Status == "finalizing" {
			accepted = cloneExcelPricingApplyJob(job)
			return nil
		}
		job.JobID = remote.JobID
		job.RemoteStatus = remote.Status
		job.RemoteStatusPath = remote.StatusURL
		job.RemoteCancelPath = remote.CancelURL
		job.RemoteResponseAccepted = true
		job.ReadbackRequired = remote.ReadbackRequired
		job.Code = remote.TerminalReason
		if remote.Error != nil && remote.Error.Code != "" {
			job.Code = remote.Error.Code
		}
		if remote.Terminal {
			switch remote.Status {
			case "completed":
				job.Status = "finalizing"
				job.Terminal = false
				job.ReadbackRequired = false
				job.Code = ""
				job.StateRevision = remote.Result.StateRevision
				if job.FinalizationState == "" {
					job.FinalizationState = "pending"
				}
			case "failed", "cancelled", "outcome_unknown":
				job.Status = remote.Status
				job.Terminal = true
			}
		} else {
			job.Status = remote.Status
			job.Terminal = false
		}
		accepted = cloneExcelPricingApplyJob(job)
		return nil
	})
	return accepted, err
}

func (store *excelPricingApplyJobStore) acceptTerminalEvent(
	event excelPricingRemoteApplyTerminalEvent,
) (*excelPricingApplyJob, bool, error) {
	if err := store.ready(); err != nil {
		return nil, false, err
	}
	if err := validateExcelPricingRemoteApplyTerminalEvent(event); err != nil {
		return nil, false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	eventKey := excelPricingApplyTerminalEventKey(event)
	if previous, exists := store.ledger.SeenEvents[eventKey]; exists {
		if previous != event.ResultDigest {
			return nil, false, errors.New("pricing apply terminal event conflicts with durable history")
		}
		job := store.findEventJobLocked(event)
		return cloneExcelPricingApplyJob(job), false, nil
	}
	jobs := store.findEventJobsLocked(event)
	if len(jobs) == 0 {
		if err := store.mutateLocked(func(ledger *excelPricingApplyLedger) error {
			ledger.SeenEvents[eventKey] = event.ResultDigest
			ledger.EventOrder = append(ledger.EventOrder, eventKey)
			pruneExcelPricingApplyEventHistory(ledger)
			return nil
		}); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}
	for _, job := range jobs {
		if job.Source != event.Source || job.PreviewDigest != event.PreviewDigest ||
			(job.JobID != "" && job.JobID != event.JobID) ||
			!excelPricingStringSliceContains(event.RequestIDs, job.RequestID) {
			return nil, false, errors.New("pricing apply terminal event binding mismatch")
		}
	}
	ownerRequestID := oldestExcelPricingApplyJobRequest(jobs)
	now := store.now().UTC()
	err := store.mutateLocked(func(ledger *excelPricingApplyLedger) error {
		for _, matched := range jobs {
			current := ledger.Jobs[matched.RequestID]
			current.JobID = event.JobID
			current.RemoteStatus = event.Status
			current.RemoteStatusPath = event.StatusPath
			current.RemoteCancelPath = event.StatusPath
			current.ResultDigest = event.ResultDigest
			current.ReadbackRequired = event.ReadbackRequired
			current.Code = event.Code
			current.RemoteResponseAccepted = true
			current.UpdatedAt = now
			switch event.Status {
			case "completed":
				current.Status = "finalizing"
				current.Terminal = false
				current.StateRevision = event.StateRevision
				current.ReadbackRequired = false
				current.Code = ""
				if current.FinalizationState == "" {
					if current.RequestID == ownerRequestID {
						current.FinalizationState = "pending"
					} else {
						current.FinalizationState = "waiting"
					}
				}
			case "failed", "cancelled", "outcome_unknown":
				current.Status = event.Status
				current.Terminal = true
				current.StateRevision = ""
			}
		}
		ledger.Aliases[event.JobID] = ownerRequestID
		ledger.SeenEvents[eventKey] = event.ResultDigest
		ledger.EventOrder = append(ledger.EventOrder, eventKey)
		pruneExcelPricingApplyEventHistory(ledger)
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return cloneExcelPricingApplyJob(store.ledger.Jobs[ownerRequestID]), true, nil
}

func (store *excelPricingApplyJobStore) lookup(identifier string) (*excelPricingApplyJob, error) {
	if err := store.ready(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.pruneLocked(store.now().UTC())
	requestID := store.ledger.Aliases[identifier]
	if requestID == "" {
		requestID = identifier
	}
	return cloneExcelPricingApplyJob(store.ledger.Jobs[requestID]), nil
}

func (store *excelPricingApplyJobStore) active() []*excelPricingApplyJob {
	if store == nil || store.ready() != nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	jobs := make([]*excelPricingApplyJob, 0)
	for _, job := range store.ledger.Jobs {
		if job != nil && !job.Terminal && job.Status != "finalizing" {
			jobs = append(jobs, cloneExcelPricingApplyJob(job))
		}
	}
	sort.Slice(jobs, func(left, right int) bool {
		return jobs[left].CreatedAt.Before(jobs[right].CreatedAt)
	})
	return jobs
}

func (store *excelPricingApplyJobStore) pendingFinalization() []*excelPricingApplyJob {
	if store == nil || store.ready() != nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	jobs := make([]*excelPricingApplyJob, 0)
	for _, job := range store.ledger.Jobs {
		if job != nil && job.Status == "finalizing" && !job.Terminal &&
			(job.FinalizationState == "pending" || job.FinalizationState == "retryable") {
			jobs = append(jobs, cloneExcelPricingApplyJob(job))
		}
	}
	return jobs
}

func (store *excelPricingApplyJobStore) group(identifier string) []*excelPricingApplyJob {
	if store == nil || store.ready() != nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	requestID := store.ledger.Aliases[identifier]
	if requestID == "" {
		requestID = identifier
	}
	group := excelPricingApplyJobGroup(&store.ledger, store.ledger.Jobs[requestID])
	clones := make([]*excelPricingApplyJob, 0, len(group))
	for _, job := range group {
		clones = append(clones, cloneExcelPricingApplyJob(job))
	}
	return clones
}

func (store *excelPricingApplyJobStore) claimFinalization(requestID string) (*excelPricingApplyJob, bool, error) {
	if err := store.ready(); err != nil {
		return nil, false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	identifier := strings.TrimSpace(requestID)
	requestID = store.ledger.Aliases[identifier]
	if requestID == "" {
		requestID = identifier
	}
	job := store.ledger.Jobs[requestID]
	if job == nil || job.Status != "finalizing" || job.Terminal {
		return cloneExcelPricingApplyJob(job), false, nil
	}
	claimed := false
	now := store.now().UTC()
	err := store.mutateLocked(func(ledger *excelPricingApplyLedger) error {
		current := ledger.Jobs[requestID]
		group := excelPricingApplyJobGroup(ledger, current)
		for _, member := range group {
			if member.FinalizationState == "completed" && member.Terminal {
				current.FinalizationState = "completed"
				current.Status = "completed"
				current.Terminal = true
				current.StateRevision = member.StateRevision
				current.ReadbackRequired = false
				current.Code = ""
				current.UpdatedAt = now
				return nil
			}
			if member.RequestID != current.RequestID && member.FinalizationState == "running" {
				current.FinalizationState = "waiting"
				current.UpdatedAt = now
				return nil
			}
		}
		owner := oldestExcelPricingApplyJobRequest(group)
		if owner != current.RequestID {
			current.FinalizationState = "waiting"
			current.UpdatedAt = now
			return nil
		}
		if current.FinalizationState == "running" || current.FinalizationState == "completed" {
			return nil
		}
		current.FinalizationState = "running"
		current.FinalizationAttempts++
		current.UpdatedAt = now
		claimed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return cloneExcelPricingApplyJob(store.ledger.Jobs[requestID]), claimed, nil
}

func (store *excelPricingApplyJobStore) recordDeliveryEvent(requestID, eventID string) error {
	return store.updateJob(requestID, func(job *excelPricingApplyJob) error {
		if job.FinalizationState != "running" || !isSHA256Revision(eventID) {
			return errors.New("pricing apply delivery event is invalid")
		}
		if job.DeliveryEventID != "" && job.DeliveryEventID != eventID {
			return errors.New("pricing apply delivery event changed")
		}
		job.DeliveryEventID = eventID
		return nil
	})
}

func (store *excelPricingApplyJobStore) completeFinalization(
	requestID string,
	stateRevision string,
) ([]*excelPricingApplyJob, error) {
	return store.finishFinalizationGroup(requestID, stateRevision, "", false)
}

func (store *excelPricingApplyJobStore) failFinalization(
	requestID string,
	code string,
) ([]*excelPricingApplyJob, error) {
	return store.finishFinalizationGroup(requestID, "", code, true)
}

func (store *excelPricingApplyJobStore) finishFinalizationGroup(
	identifier string,
	stateRevision string,
	code string,
	failed bool,
) ([]*excelPricingApplyJob, error) {
	if err := store.ready(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	requestID := store.ledger.Aliases[identifier]
	if requestID == "" {
		requestID = identifier
	}
	job := store.ledger.Jobs[requestID]
	if job == nil || job.FinalizationState != "running" {
		return nil, errors.New("pricing apply finalization is unavailable")
	}
	if !failed && (!isSHA256Revision(stateRevision) || job.StateRevision != stateRevision) {
		return nil, errors.New("pricing apply finalization readback is invalid")
	}
	finished := make([]*excelPricingApplyJob, 0)
	now := store.now().UTC()
	err := store.mutateLocked(func(ledger *excelPricingApplyLedger) error {
		current := ledger.Jobs[requestID]
		for _, member := range excelPricingApplyJobGroup(ledger, current) {
			if member.Source != current.Source || member.PreviewDigest != current.PreviewDigest {
				return errors.New("pricing apply coalesced binding is invalid")
			}
			if failed {
				member.FinalizationState = "outcome_unknown"
				member.Status = "outcome_unknown"
				member.Terminal = true
				member.ReadbackRequired = true
				member.Code = safeExcelPricingApplyCode(code)
			} else {
				member.DeliveryEventID = current.DeliveryEventID
				member.FinalizationState = "completed"
				member.Status = "completed"
				member.Terminal = true
				member.StateRevision = stateRevision
				member.ReadbackRequired = false
				member.Code = ""
			}
			member.UpdatedAt = now
			finished = append(finished, cloneExcelPricingApplyJob(member))
		}
		return nil
	})
	return finished, err
}

func (store *excelPricingApplyJobStore) markLocalEventPublished(requestID string) error {
	return store.updateJob(requestID, func(job *excelPricingApplyJob) error {
		job.LocalEventPublished = true
		return nil
	})
}

func (store *excelPricingApplyJobStore) recordReconciliation(requestID, reason string) error {
	return store.updateJob(requestID, func(job *excelPricingApplyJob) error {
		job.LastReconcileReason = reason
		job.LastReconciledAt = store.now().UTC()
		return nil
	})
}

func (store *excelPricingApplyJobStore) updateJob(
	identifier string,
	update func(*excelPricingApplyJob) error,
) error {
	if err := store.ready(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	requestID := store.ledger.Aliases[identifier]
	if requestID == "" {
		requestID = identifier
	}
	job := store.ledger.Jobs[requestID]
	if job == nil {
		return errors.New("pricing apply job is unavailable")
	}
	return store.mutateLocked(func(ledger *excelPricingApplyLedger) error {
		current := ledger.Jobs[requestID]
		if err := update(current); err != nil {
			return err
		}
		current.UpdatedAt = store.now().UTC()
		if current.JobID != "" {
			ledger.Aliases[current.JobID] = current.RequestID
		}
		return nil
	})
}

func (store *excelPricingApplyJobStore) mutateLocked(
	mutate func(*excelPricingApplyLedger) error,
) error {
	clone, err := cloneExcelPricingApplyLedger(store.ledger)
	if err != nil {
		return err
	}
	if err := mutate(&clone); err != nil {
		return err
	}
	clone.Schema = excelPricingApplyLedgerSchema
	clone.UpdatedAt = store.now().UTC()
	if err := store.persistLocked(clone); err != nil {
		return err
	}
	store.ledger = clone
	return nil
}

func (store *excelPricingApplyJobStore) persistLocked(ledger excelPricingApplyLedger) error {
	body, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(store.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".pricing-apply-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceExcelPricingApplyStateFile(temporaryPath, store.path); err != nil {
		return err
	}
	keep = true
	return os.Chmod(store.path, 0o600)
}

func (store *excelPricingApplyJobStore) pruneLocked(now time.Time) {
	for requestID, job := range store.ledger.Jobs {
		if job == nil || (job.Terminal && now.Sub(job.UpdatedAt) > excelPricingApplyJobRetention) {
			delete(store.ledger.Jobs, requestID)
		}
	}
	for alias, requestID := range store.ledger.Aliases {
		if store.ledger.Jobs[requestID] == nil {
			delete(store.ledger.Aliases, alias)
		}
	}
}

func (store *excelPricingApplyJobStore) findEventJobLocked(
	event excelPricingRemoteApplyTerminalEvent,
) *excelPricingApplyJob {
	jobs := store.findEventJobsLocked(event)
	if len(jobs) == 0 {
		return nil
	}
	requestID := oldestExcelPricingApplyJobRequest(jobs)
	return store.ledger.Jobs[requestID]
}

func (store *excelPricingApplyJobStore) findEventJobsLocked(
	event excelPricingRemoteApplyTerminalEvent,
) []*excelPricingApplyJob {
	jobs := make([]*excelPricingApplyJob, 0)
	seen := make(map[string]struct{})
	for _, requestID := range event.RequestIDs {
		resolved := store.ledger.Aliases[requestID]
		if resolved == "" {
			resolved = requestID
		}
		if job := store.ledger.Jobs[resolved]; job != nil {
			if _, exists := seen[job.RequestID]; !exists {
				seen[job.RequestID] = struct{}{}
				jobs = append(jobs, job)
			}
		}
	}
	if requestID := store.ledger.Aliases[event.JobID]; requestID != "" {
		if job := store.ledger.Jobs[requestID]; job != nil {
			if _, exists := seen[job.RequestID]; !exists {
				jobs = append(jobs, job)
			}
		}
	}
	return jobs
}

func excelPricingApplyJobGroup(
	ledger *excelPricingApplyLedger,
	job *excelPricingApplyJob,
) []*excelPricingApplyJob {
	if ledger == nil || job == nil {
		return nil
	}
	group := make([]*excelPricingApplyJob, 0)
	for _, candidate := range ledger.Jobs {
		if candidate == nil {
			continue
		}
		if job.JobID == "" {
			if candidate.RequestID == job.RequestID {
				group = append(group, candidate)
			}
		} else if candidate.JobID == job.JobID {
			group = append(group, candidate)
		}
	}
	return group
}

func oldestExcelPricingApplyJobRequest(jobs []*excelPricingApplyJob) string {
	if len(jobs) == 0 {
		return ""
	}
	oldest := jobs[0]
	for _, candidate := range jobs[1:] {
		if candidate.CreatedAt.Before(oldest.CreatedAt) ||
			(candidate.CreatedAt.Equal(oldest.CreatedAt) && candidate.RequestID < oldest.RequestID) {
			oldest = candidate
		}
	}
	return oldest.RequestID
}

func cloneExcelPricingApplyLedger(value excelPricingApplyLedger) (excelPricingApplyLedger, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return excelPricingApplyLedger{}, err
	}
	var clone excelPricingApplyLedger
	if json.Unmarshal(body, &clone) != nil {
		return excelPricingApplyLedger{}, errors.New("pricing apply durable state could not be cloned")
	}
	return clone, nil
}

func cloneExcelPricingApplyJob(job *excelPricingApplyJob) *excelPricingApplyJob {
	if job == nil {
		return nil
	}
	clone := *job
	return &clone
}

func validatePersistedExcelPricingApplyJob(job excelPricingApplyJob) error {
	if !excelPricingIdempotencyPattern.MatchString(job.RequestID) ||
		!isSHA256Revision(job.Fingerprint) || !validExcelPricingRemoteSource(job.Source) ||
		!isSHA256Revision(job.ExpectedStateRevision) || !isSHA256Revision(job.PreviewDigest) ||
		job.CreatedAt.IsZero() || job.UpdatedAt.IsZero() {
		return errors.New("pricing apply durable job identity is invalid")
	}
	if job.JobID != "" && !excelPricingRemoteApplyJobPattern.MatchString(job.JobID) {
		return errors.New("pricing apply durable remote identity is invalid")
	}
	return nil
}

func validateExcelPricingRemoteApplyJob(
	remote excelPricingRemoteApplyJob,
	local excelPricingApplyJob,
) error {
	if remote.Schema != excelPricingRemoteApplySchema ||
		!excelPricingRemoteApplyJobPattern.MatchString(remote.JobID) ||
		remote.RequestID != local.RequestID || remote.IdempotencyKey != local.RequestID ||
		remote.Source != local.Source || remote.ExpectedStateRevision != local.ExpectedStateRevision ||
		remote.PreviewDigest != local.PreviewDigest ||
		!validExcelPricingRemoteApplyLifecycle(remote.Status, remote.Terminal) {
		return errors.New("remote pricing apply job contract is invalid")
	}
	expectedRequestPath := excelPricingRemoteApplyJobPath(local.RequestID, local.Source)
	expectedJobPath := excelPricingRemoteApplyJobPath(remote.JobID, local.Source)
	if remote.StatusURL != expectedRequestPath && remote.StatusURL != expectedJobPath {
		return errors.New("remote pricing apply status path is invalid")
	}
	if remote.CancelURL != remote.StatusURL {
		return errors.New("remote pricing apply cancel path is invalid")
	}
	if remote.Status == "completed" {
		if !remote.Terminal || remote.Result == nil ||
			remote.Result.Schema != excelPricingApplySchema || remote.Result.Mode != "apply" ||
			!isSHA256Revision(remote.Result.StateRevision) ||
			remote.Result.Source != local.Source || remote.Result.RequestID != local.RequestID ||
			remote.Result.PreviewDigest != local.PreviewDigest ||
			remote.Result.ClientID != excelPricingContractClientID ||
			remote.Result.Channel != excelPricingContractChannel || remote.ReadbackRequired {
			return errors.New("remote pricing apply terminal result is invalid")
		}
	} else if remote.Terminal {
		if remote.Result != nil || (remote.Status == "outcome_unknown") != remote.ReadbackRequired {
			return errors.New("remote pricing apply terminal failure is invalid")
		}
	}
	return nil
}

func validExcelPricingRemoteApplyLifecycle(status string, terminal bool) bool {
	if terminal {
		switch status {
		case "completed", "failed", "cancelled", "outcome_unknown":
			return true
		default:
			return false
		}
	}
	switch status {
	case "queued", "running", "recovering", "cancelling":
		return true
	default:
		return false
	}
}

func validateExcelPricingRemoteApplyTerminalEvent(
	event excelPricingRemoteApplyTerminalEvent,
) error {
	if event.Schema != excelPricingApplyEventSchema ||
		event.Projection != excelPricingRemoteProjection ||
		!excelPricingRemoteApplyJobPattern.MatchString(event.JobID) ||
		!validExcelPricingRemoteSource(event.Source) ||
		!isSHA256Revision(event.PreviewDigest) || !isSHA256Revision(event.ResultDigest) ||
		!isSHA256Revision(event.IdempotencyKey) ||
		event.RevisionPath != "/wp-json/digitalogic/pricing/sync/revision" ||
		len(event.Audience.Services) != 1 || event.Audience.Services[0] != "patris_pricing" ||
		len(event.RequestIDs) == 0 || len(event.RequestIDs) > 32 ||
		!excelPricingStringSliceContains(event.RequestIDs, event.PrimaryRequestID) ||
		event.StatusPath != excelPricingRemoteApplyJobPath(event.JobID, event.Source) {
		return errors.New("pricing apply terminal event is invalid")
	}
	seen := make(map[string]struct{}, len(event.RequestIDs))
	for _, requestID := range event.RequestIDs {
		if !excelPricingIdempotencyPattern.MatchString(requestID) {
			return errors.New("pricing apply terminal request identity is invalid")
		}
		if _, exists := seen[requestID]; exists {
			return errors.New("pricing apply terminal request identity is duplicated")
		}
		seen[requestID] = struct{}{}
	}
	switch event.Status {
	case "completed":
		if !isSHA256Revision(event.StateRevision) || event.ReadbackRequired ||
			event.Code != "" || event.Retryable {
			return errors.New("pricing apply completed event is invalid")
		}
	case "failed", "cancelled", "outcome_unknown":
		if event.StateRevision != "" || event.Code == "" || event.Retryable ||
			(event.Status == "outcome_unknown") != event.ReadbackRequired {
			return errors.New("pricing apply failure event is invalid")
		}
	default:
		return errors.New("pricing apply terminal status is invalid")
	}
	return nil
}

func excelPricingRemoteApplyJobPath(identifier string, source canonical.Source) string {
	return "/wp-json/digitalogic/pricing/sync/jobs/" + url.PathEscape(identifier) +
		"?source_id=" + url.QueryEscape(source.ID) +
		"&source_dataset=" + url.QueryEscape(source.Dataset) +
		"&source_revision=" + url.QueryEscape(source.Revision)
}

func excelPricingLocalApplyJobPath(identifier string, source canonical.Source) string {
	return "/api/pricing-sync/jobs/" + url.PathEscape(identifier) +
		"?source_id=" + url.QueryEscape(source.ID) +
		"&source_dataset=" + url.QueryEscape(source.Dataset) +
		"&source_revision=" + url.QueryEscape(source.Revision)
}

func excelPricingApplyTerminalEventKey(event excelPricingRemoteApplyTerminalEvent) string {
	return event.JobID + "|" + event.Status + "|" + event.ResultDigest
}

func excelPricingStringSliceContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func pruneExcelPricingApplyEventHistory(ledger *excelPricingApplyLedger) {
	for len(ledger.EventOrder) > excelPricingApplyMaxEventHistory {
		delete(ledger.SeenEvents, ledger.EventOrder[0])
		ledger.EventOrder = ledger.EventOrder[1:]
	}
}

func safeExcelPricingApplyCode(code string) string {
	code = strings.TrimSpace(strings.ToLower(code))
	if code == "" || len(code) > 96 {
		return "pricing_apply_unavailable"
	}
	for _, character := range code {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return "pricing_apply_unavailable"
	}
	return code
}

func excelPricingApplyEventDigest(event excelPricingRemoteApplyTerminalEvent) string {
	body, _ := json.Marshal(event)
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func decodeExcelPricingRemoteApplyJob(body []byte) (excelPricingRemoteApplyJob, error) {
	var remote excelPricingRemoteApplyJob
	decoder := json.NewDecoder(bytes.NewReader(body))
	if decoder.Decode(&remote) != nil {
		return excelPricingRemoteApplyJob{}, errors.New("remote pricing apply job is invalid")
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return excelPricingRemoteApplyJob{}, errors.New("remote pricing apply job has trailing data")
	}
	return remote, nil
}

func (s *Server) handleExcelPricingApplyAdmission(
	w http.ResponseWriter,
	r *http.Request,
	local excelPricingLocalRequest,
) {
	store := s.excelPricing.applyJobs
	if store == nil || store.ready() != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "apply_state_unavailable")
		return
	}
	operationContext, cancel := context.WithTimeout(r.Context(), excelPricingOperationTimeout)
	defer cancel()
	cfg := s.Config()
	if _, _, _, err := resolveExcelPricingRemote(cfg.SendUpdates, "apply"); err != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "remote_not_configured")
		return
	}
	contract, err := s.excelPricingCanonical(operationContext, cfg)
	if err != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "canonical_source_unavailable")
		return
	}
	fingerprint := excelPricingMutationFingerprint(local)
	reservation, job, err := store.reserve(local, contract.Source, fingerprint)
	if err != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "apply_state_unavailable")
		return
	}
	switch reservation {
	case excelPricingApplyReservationConflict:
		writeExcelPricingError(w, http.StatusConflict, "idempotency_conflict")
		return
	case excelPricingApplyReservationReplay:
		writeExcelPricingApplyJob(w, job, 0)
		return
	case excelPricingApplyReservationNew:
	default:
		writeExcelPricingError(w, http.StatusServiceUnavailable, "apply_state_unavailable")
		return
	}
	if err := store.markPostStarted(local.RequestID); err != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "apply_state_unavailable")
		return
	}
	remoteRequest := buildExcelPricingRemoteRequest("apply", local, contract.Source)
	remote, status, err := s.forwardExcelPricingApplyAdmission(
		operationContext,
		cfg.SendUpdates,
		remoteRequest,
		local,
	)
	if err != nil {
		if markErr := store.markAdmissionUnknown(local.RequestID, "admission_response_unknown"); markErr != nil {
			writeExcelPricingError(w, http.StatusServiceUnavailable, "apply_state_unavailable")
			return
		}
		job, _ = s.reconcileExcelPricingApplyJob(operationContext, local.RequestID, "lost_response")
		if job == nil {
			job, _ = store.lookup(local.RequestID)
		}
		writeExcelPricingApplyJob(w, job, 0)
		return
	}
	accepted, err := store.acceptRemote(local.RequestID, remote)
	if err != nil {
		_ = store.markAdmissionUnknown(local.RequestID, "admission_contract_invalid")
		writeExcelPricingError(w, http.StatusBadGateway, "remote_contract_invalid")
		return
	}
	if accepted.Status == "finalizing" {
		s.startExcelPricingApplyFinalizer(accepted.RequestID)
	} else if accepted.Terminal {
		s.publishExcelPricingApplyTerminals(s.excelPricing.applyJobs.group(accepted.RequestID))
	}
	writeExcelPricingApplyJob(w, accepted, status)
}

func (s *Server) forwardExcelPricingApplyAdmission(
	ctx context.Context,
	cfg updateout.Config,
	payload excelPricingRemoteRequest,
	local excelPricingLocalRequest,
) (excelPricingRemoteApplyJob, int, error) {
	cfg, secret, endpoint, err := resolveExcelPricingRemote(cfg, "apply")
	if err != nil {
		return excelPricingRemoteApplyJob{}, 0, err
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > excelPricingMaxRequestBytes {
		return excelPricingRemoteApplyJob{}, 0, errors.New("pricing apply request is invalid")
	}
	timeout := excelPricingRemoteTimeout(cfg.Timeout)
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(bounded, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return excelPricingRemoteApplyJob{}, 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "patris-export-excel-companion")
	request.Header.Set(updateout.ProductSyncSecretHeader, secret)
	request.Header.Set("Idempotency-Key", local.IdempotencyKey)
	request.Header.Set("If-Match", `"`+local.ExpectedStateRevision+`"`)
	client := s.excelPricing.client
	if client == nil {
		client = newExcelPricingState().client
	}
	copyClient := *client
	copyClient.Timeout = timeout
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return excelPricingRemoteApplyJob{}, 0, errors.New("remote pricing apply response is unknown")
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, excelPricingApplyRemoteMaxBytes+1))
	if err != nil || len(responseBody) == 0 || len(responseBody) > excelPricingApplyRemoteMaxBytes ||
		!excelPricingRemoteApplyContentType(response) || bytes.Contains(responseBody, []byte(secret)) {
		return excelPricingRemoteApplyJob{}, response.StatusCode, errors.New("remote pricing apply response is invalid")
	}
	remote, err := decodeExcelPricingRemoteApplyJob(responseBody)
	if err != nil {
		if response.StatusCode >= 400 && response.StatusCode != http.StatusServiceUnavailable {
			return excelPricingRemoteApplyJob{}, response.StatusCode,
				safeExcelPricingRemoteError(response.StatusCode, responseBody)
		}
		return excelPricingRemoteApplyJob{}, response.StatusCode, err
	}
	if err := validateExcelPricingRemoteApplyJob(remote, excelPricingApplyJob{
		RequestID:             local.RequestID,
		Source:                payload.Source,
		ExpectedStateRevision: local.ExpectedStateRevision,
		PreviewDigest:         local.PreviewDigest,
	}); err != nil {
		return excelPricingRemoteApplyJob{}, response.StatusCode, err
	}
	switch response.StatusCode {
	case http.StatusAccepted:
		if remote.Terminal {
			return excelPricingRemoteApplyJob{}, response.StatusCode, errors.New("active pricing apply response is terminal")
		}
		location := strings.TrimSpace(response.Header.Get("Location"))
		if location != remote.StatusURL {
			return excelPricingRemoteApplyJob{}, response.StatusCode, errors.New("pricing apply Location is invalid")
		}
	case http.StatusOK:
		if !remote.Terminal {
			return excelPricingRemoteApplyJob{}, response.StatusCode, errors.New("terminal pricing apply response is active")
		}
	case http.StatusServiceUnavailable:
		if !remote.Terminal || remote.Status != "failed" || remote.Accepted == nil || *remote.Accepted {
			return excelPricingRemoteApplyJob{}, response.StatusCode, errors.New("pricing apply admission failure is invalid")
		}
	default:
		return excelPricingRemoteApplyJob{}, response.StatusCode,
			safeExcelPricingRemoteError(response.StatusCode, responseBody)
	}
	return remote, response.StatusCode, nil
}

func writeExcelPricingApplyJob(w http.ResponseWriter, job *excelPricingApplyJob, statusOverride int) {
	if job == nil {
		writeExcelPricingError(w, http.StatusNotFound, "apply_job_not_found")
		return
	}
	status := statusOverride
	if status == 0 {
		if job.Terminal {
			status = http.StatusOK
		} else {
			status = http.StatusAccepted
		}
	}
	if status != http.StatusOK && status != http.StatusAccepted && status != http.StatusServiceUnavailable {
		status = http.StatusBadGateway
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if !job.Terminal {
		w.Header().Set("Retry-After", "2")
	}
	localPath := excelPricingLocalApplyJobPath(job.RequestID, job.Source)
	w.Header().Set("Location", localPath)
	payload := map[string]interface{}{
		"schema":                  excelPricingApplyJobSchema,
		"job_id":                  job.JobID,
		"request_id":              job.RequestID,
		"idempotency_key":         job.RequestID,
		"status":                  job.Status,
		"terminal":                job.Terminal,
		"source":                  job.Source,
		"expected_state_revision": job.ExpectedStateRevision,
		"preview_digest":          job.PreviewDigest,
		"readback_required":       job.ReadbackRequired,
		"code":                    job.Code,
		"created_at":              job.CreatedAt,
		"updated_at":              job.UpdatedAt,
		"status_url":              localPath,
		"cancel_url":              localPath,
	}
	if !job.Terminal {
		payload["retry_after"] = 2
	}
	if job.Status == "completed" && isSHA256Revision(job.StateRevision) {
		payload["state_revision"] = job.StateRevision
	}
	writeExcelPricingJSON(w, status, payload)
}

func (s *Server) handleGetExcelPricingApplyJob(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	if !excelPricingLocalRequestAllowed(r) ||
		!singleHeaderEquals(r, excelPricingClientHeader, excelPricingClientID) ||
		!s.excelPricing.authorizedSession(r) {
		writeExcelPricingError(w, http.StatusForbidden, "local_session_required")
		return
	}
	identifier, source, reconcileReason, ok := excelPricingLocalApplyLookup(r)
	if !ok {
		writeExcelPricingError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	job, err := s.excelPricing.applyJobs.lookup(identifier)
	if err != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "apply_state_unavailable")
		return
	}
	if job == nil || job.Source != source {
		writeExcelPricingError(w, http.StatusNotFound, "apply_job_not_found")
		return
	}
	if !job.Terminal && reconcileReason != "" {
		ctx, cancel := context.WithTimeout(r.Context(), excelPricingRemoteTimeout(s.Config().SendUpdates.Timeout))
		defer cancel()
		if reconciled, reconcileErr := s.reconcileExcelPricingApplyJob(ctx, job.RequestID, reconcileReason); reconcileErr == nil && reconciled != nil {
			job = reconciled
		}
	}
	writeExcelPricingApplyJob(w, job, 0)
}

func (s *Server) handleDeleteExcelPricingApplyJob(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	if !excelPricingLocalRequestAllowed(r) ||
		!singleHeaderEquals(r, excelPricingClientHeader, excelPricingClientID) ||
		!s.excelPricing.authorizedSession(r) {
		writeExcelPricingError(w, http.StatusForbidden, "local_session_required")
		return
	}
	identifier, source, _, ok := excelPricingLocalApplyLookup(r)
	if !ok {
		writeExcelPricingError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	job, err := s.excelPricing.applyJobs.lookup(identifier)
	if err != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "apply_state_unavailable")
		return
	}
	if job == nil || job.Source != source {
		writeExcelPricingError(w, http.StatusNotFound, "apply_job_not_found")
		return
	}
	if job.Terminal {
		writeExcelPricingApplyJob(w, job, 0)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), excelPricingRemoteTimeout(s.Config().SendUpdates.Timeout))
	defer cancel()
	if job.Status == "admission_unknown" || job.JobID == "" {
		reconciled, reconcileErr := s.reconcileExcelPricingApplyJob(ctx, job.RequestID, "cancel")
		if reconcileErr != nil || reconciled == nil {
			writeExcelPricingApplyJob(w, job, 0)
			return
		}
		job = reconciled
		if job.Terminal {
			writeExcelPricingApplyJob(w, job, 0)
			return
		}
		if job.JobID == "" {
			writeExcelPricingApplyJob(w, job, 0)
			return
		}
	}
	remoteIdentifier := job.JobID
	if remoteIdentifier == "" {
		remoteIdentifier = job.RequestID
	}
	remote, _, err := s.forwardExcelPricingApplyJobRequest(ctx, http.MethodDelete, remoteIdentifier, job.Source)
	if err != nil {
		writeExcelPricingError(w, http.StatusBadGateway, "cancel_outcome_unknown")
		return
	}
	accepted, err := s.excelPricing.applyJobs.acceptRemote(job.RequestID, remote)
	if err != nil {
		writeExcelPricingError(w, http.StatusBadGateway, "remote_contract_invalid")
		return
	}
	if accepted.Status == "finalizing" {
		s.startExcelPricingApplyFinalizer(accepted.RequestID)
	} else if accepted.Terminal {
		s.publishExcelPricingApplyTerminals(s.excelPricing.applyJobs.group(accepted.RequestID))
	}
	writeExcelPricingApplyJob(w, accepted, 0)
}

func excelPricingLocalApplyLookup(
	r *http.Request,
) (string, canonical.Source, string, bool) {
	identifier := strings.TrimSpace(mux.Vars(r)["identifier"])
	if !excelPricingIdempotencyPattern.MatchString(identifier) &&
		!excelPricingRemoteApplyJobPattern.MatchString(identifier) {
		return "", canonical.Source{}, "", false
	}
	query := r.URL.Query()
	allowed := map[string]bool{
		"source_id": true, "source_dataset": true, "source_revision": true, "reconcile": true,
	}
	for key, values := range query {
		if !allowed[key] || len(values) != 1 {
			return "", canonical.Source{}, "", false
		}
	}
	source := canonical.Source{
		ID:       strings.TrimSpace(query.Get("source_id")),
		Dataset:  strings.TrimSpace(query.Get("source_dataset")),
		Revision: strings.TrimSpace(query.Get("source_revision")),
	}
	if !validExcelPricingRemoteSource(source) {
		return "", canonical.Source{}, "", false
	}
	reason := strings.TrimSpace(query.Get("reconcile"))
	if reason != "" && reason != "connect" && reason != "lost_response" {
		return "", canonical.Source{}, "", false
	}
	return identifier, source, reason, true
}

func (s *Server) reconcileExcelPricingApplyJob(
	ctx context.Context,
	identifier string,
	reason string,
) (*excelPricingApplyJob, error) {
	job, err := s.excelPricing.applyJobs.lookup(identifier)
	if err != nil || job == nil || job.Terminal || job.Status == "finalizing" {
		return job, err
	}
	if reason == "" {
		reason = "lost_response"
	}
	if err := s.excelPricing.applyJobs.recordReconciliation(job.RequestID, reason); err != nil {
		return nil, err
	}
	remoteIdentifier := job.JobID
	if remoteIdentifier == "" {
		// An admission response that was lost must be recovered by the exact
		// original request identity. The mutation POST is never repeated.
		remoteIdentifier = job.RequestID
	}
	remote, _, err := s.forwardExcelPricingApplyJobRequest(ctx, http.MethodGet, remoteIdentifier, job.Source)
	if err != nil {
		return job, err
	}
	accepted, err := s.excelPricing.applyJobs.acceptRemote(job.RequestID, remote)
	if err != nil {
		return nil, err
	}
	if accepted.Status == "finalizing" {
		s.startExcelPricingApplyFinalizer(accepted.RequestID)
	} else if accepted.Terminal {
		s.publishExcelPricingApplyTerminals(s.excelPricing.applyJobs.group(accepted.RequestID))
	}
	return accepted, nil
}

func (s *Server) reconcileExcelPricingApplyJobsOnConnect(ctx context.Context) error {
	if s == nil || s.excelPricing == nil || s.excelPricing.applyJobs == nil {
		return nil
	}
	for _, job := range s.excelPricing.applyJobs.active() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, _ = s.reconcileExcelPricingApplyJob(ctx, job.RequestID, "connect")
	}
	return nil
}

func (s *Server) acceptExcelPricingRemoteApplyTerminal(
	event excelPricingRemoteApplyTerminalEvent,
) error {
	if s == nil || s.excelPricing == nil || s.excelPricing.applyJobs == nil {
		return errors.New("pricing apply durable state is unavailable")
	}
	job, accepted, err := s.excelPricing.applyJobs.acceptTerminalEvent(event)
	if err != nil {
		return err
	}
	if !accepted || job == nil {
		return nil
	}
	if job.Status == "finalizing" {
		s.startExcelPricingApplyFinalizer(job.RequestID)
	} else if job.Terminal {
		s.publishExcelPricingApplyTerminals(s.excelPricing.applyJobs.group(job.RequestID))
	}
	return nil
}

func (s *Server) startExcelPricingApplyFinalizer(requestID string) {
	if s == nil || s.excelPricing == nil || s.excelPricing.applyJobs == nil {
		return
	}
	job, claimed, err := s.excelPricing.applyJobs.claimFinalization(requestID)
	if err != nil || job == nil {
		return
	}
	if !claimed {
		if job.Terminal {
			s.publishExcelPricingApplyTerminals(s.excelPricing.applyJobs.group(job.RequestID))
		}
		return
	}
	ctx := s.backgroundCtx
	if ctx == nil {
		ctx = context.Background()
	}
	s.backgroundWG.Add(1)
	go func() {
		defer s.backgroundWG.Done()
		s.runExcelPricingApplyFinalizer(ctx, job)
	}()
}

func (s *Server) runExcelPricingApplyFinalizer(
	parent context.Context,
	job *excelPricingApplyJob,
) {
	ctx, cancel := context.WithTimeout(parent, excelPricingOperationTimeout)
	defer cancel()
	if !isSHA256Revision(job.StateRevision) {
		failed, _ := s.excelPricing.applyJobs.failFinalization(job.RequestID, "terminal_state_revision_invalid")
		s.publishExcelPricingApplyTerminals(failed)
		return
	}
	s.invalidateCanonicalProjection(true)
	s.excelPricing.snapshots.publishPricingStateInvalidated(job.StateRevision)
	err := s.completeExcelPricingApply(
		ctx,
		s.Config(),
		excelPricingRemoteResponse{
			schema:        excelPricingApplySchema,
			stateRevision: job.StateRevision,
		},
		job.Source,
		func(eventID string) error {
			return s.excelPricing.applyJobs.recordDeliveryEvent(job.RequestID, eventID)
		},
	)
	if err != nil {
		failed, _ := s.excelPricing.applyJobs.failFinalization(job.RequestID, "post_apply_verification_failed")
		s.publishExcelPricingApplyTerminals(failed)
		return
	}
	completed, err := s.excelPricing.applyJobs.completeFinalization(job.RequestID, job.StateRevision)
	if err != nil {
		failed, _ := s.excelPricing.applyJobs.failFinalization(job.RequestID, "apply_state_commit_failed")
		s.publishExcelPricingApplyTerminals(failed)
		return
	}
	s.excelPricing.snapshots.publishPricingStateVerified(job.StateRevision)
	s.publishExcelPricingApplyTerminals(completed)
}

func (s *Server) publishExcelPricingApplyTerminals(jobs []*excelPricingApplyJob) {
	for _, job := range jobs {
		s.publishExcelPricingApplyTerminal(job)
	}
}

func (s *Server) recoverExcelPricingApplyFinalizers() {
	if s == nil || s.excelPricing == nil || s.excelPricing.applyJobs == nil {
		return
	}
	for _, job := range s.excelPricing.applyJobs.pendingFinalization() {
		s.startExcelPricingApplyFinalizer(job.RequestID)
	}
}

func (s *Server) publishExcelPricingApplyTerminal(job *excelPricingApplyJob) {
	if s == nil || job == nil || !job.Terminal || job.LocalEventPublished ||
		s.excelPricing == nil || s.excelPricing.snapshots == nil {
		return
	}
	s.excelPricing.snapshots.publishPricingApplyTerminal(*job)
	_ = s.excelPricing.applyJobs.markLocalEventPublished(job.RequestID)
}

func (store *excelPricingSnapshotStore) publishPricingApplyTerminal(job excelPricingApplyJob) {
	if store == nil || !job.Terminal {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	change := excelPricingStateChangeEvent{
		Kind:             "pricing_apply_terminal",
		JobID:            job.JobID,
		RequestID:        job.RequestID,
		Status:           job.Status,
		Code:             job.Code,
		PreviewDigest:    job.PreviewDigest,
		StateRevision:    job.StateRevision,
		ReadbackRequired: job.ReadbackRequired,
		Source:           &job.Source,
		Stale:            job.Status == "completed",
		Verified:         job.Status == "completed",
	}
	store.publishChangeLocked(change)
}

func (s *Server) forwardExcelPricingApplyJobRequest(
	ctx context.Context,
	method string,
	identifier string,
	source canonical.Source,
) (excelPricingRemoteApplyJob, int, error) {
	cfg, secret, applyEndpoint, err := resolveExcelPricingRemote(s.Config().SendUpdates, "apply")
	if err != nil {
		return excelPricingRemoteApplyJob{}, 0, err
	}
	parsed, err := url.Parse(applyEndpoint)
	if err != nil {
		return excelPricingRemoteApplyJob{}, 0, err
	}
	jobPath := excelPricingRemoteApplyJobPath(identifier, source)
	parts := strings.SplitN(jobPath, "?", 2)
	parsed.Path = parts[0]
	parsed.RawPath = ""
	parsed.RawQuery = parts[1]
	parsed.Fragment = ""
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), nil)
	if err != nil {
		return excelPricingRemoteApplyJob{}, 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set(updateout.ProductSyncSecretHeader, secret)
	request.Header.Set(excelPricingRemoteSourceIDHeader, source.ID)
	request.Header.Set(excelPricingRemoteDatasetHeader, source.Dataset)
	client := s.excelPricing.client
	if client == nil {
		client = newExcelPricingState().client
	}
	copyClient := *client
	copyClient.Timeout = excelPricingRemoteTimeout(cfg.Timeout)
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(request)
	if err != nil {
		return excelPricingRemoteApplyJob{}, 0, errors.New("remote pricing job request failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, excelPricingApplyRemoteMaxBytes+1))
	if err != nil || len(body) == 0 || len(body) > excelPricingApplyRemoteMaxBytes ||
		!excelPricingRemoteJSONContentType(response.Header.Get("Content-Type")) ||
		bytes.Contains(body, []byte(secret)) {
		return excelPricingRemoteApplyJob{}, response.StatusCode, errors.New("remote pricing job response is invalid")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return excelPricingRemoteApplyJob{}, response.StatusCode, safeExcelPricingRemoteError(response.StatusCode, body)
	}
	remote, err := decodeExcelPricingRemoteApplyJob(body)
	if err != nil {
		return excelPricingRemoteApplyJob{}, response.StatusCode, err
	}
	switch response.StatusCode {
	case http.StatusAccepted:
		if remote.Terminal || strings.TrimSpace(response.Header.Get("Location")) != remote.StatusURL {
			return excelPricingRemoteApplyJob{}, response.StatusCode,
				errors.New("active remote pricing job response is invalid")
		}
	case http.StatusOK:
		if !remote.Terminal {
			return excelPricingRemoteApplyJob{}, response.StatusCode,
				errors.New("terminal remote pricing job response is invalid")
		}
	default:
		return excelPricingRemoteApplyJob{}, response.StatusCode,
			errors.New("remote pricing job status is invalid")
	}
	return remote, response.StatusCode, nil
}

func excelPricingRemoteApplyContentType(response *http.Response) bool {
	if response == nil || len(response.Header.Values("Content-Type")) != 1 {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	return err == nil && (mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"))
}
