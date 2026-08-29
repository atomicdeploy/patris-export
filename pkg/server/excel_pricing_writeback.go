package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
	"github.com/gorilla/mux"
)

const (
	excelPricingWritebackRequestSchema    = "patris.pricing-input-writeback-request/v1"
	excelPricingWritebackJobSchema        = "patris.pricing-input-writeback-job/v1"
	excelPricingConfirmationRequestSchema = "patris.pricing-confirmation-ack-request/v1"
	excelPricingWritebackMaxJobs          = 256
	excelPricingWritebackMaxAttempts      = 3
	excelPricingWritebackJobTTL           = 30 * time.Minute
	excelPricingWritebackTimeout          = 8 * time.Minute
)

var excelPricingWritebackKeys = map[string]struct{}{
	"yuan_price":               {},
	"dollar_price":             {},
	"cny_effective_date":       {},
	"usd_effective_date":       {},
	"profit_margin_percent":    {},
	"air_express_price_per_kg": {},
	"price_rounding_digits":    {},
}

type excelPricingWritebackRequest struct {
	Schema                 string               `json:"schema"`
	RequestID              string               `json:"request_id"`
	SettingKey             string               `json:"setting_key"`
	ExpectedStateRevision  string               `json:"expected_state_revision"`
	PreviousConfirmedValue string               `json:"previous_confirmed_value"`
	Settings               excelPricingSettings `json:"settings"`
}

type excelPricingWritebackJob struct {
	Schema         string `json:"schema"`
	JobID          string `json:"job_id"`
	RequestID      string `json:"request_id"`
	SettingKey     string `json:"setting_key"`
	DesiredValue   string `json:"desired_value"`
	ConfirmedValue string `json:"confirmed_value,omitempty"`
	Status         string `json:"status"`
	Code           string `json:"code"`
	MessageFA      string `json:"message_fa"`
	Attempts       int    `json:"attempts"`
	Blocking       bool   `json:"blocking"`
	StateRevision  string `json:"state_revision,omitempty"`
	TransactionID  string `json:"transaction_id,omitempty"`
	SettingsDigest string `json:"confirmed_settings_digest,omitempty"`
	ACKDeadline    int64  `json:"ack_deadline,omitempty"`
	UpdatedAt      string `json:"updated_at"`
	RetryCount     int    `json:"retry_count,omitempty"`
	LastRetryCode  string `json:"last_retry_code,omitempty"`
	LastRetryAt    string `json:"last_retry_at,omitempty"`
	LastAttemptMS  int64  `json:"last_attempt_ms,omitempty"`
	TotalElapsedMS int64  `json:"total_elapsed_ms,omitempty"`

	settings               excelPricingSettings
	expectedStateRevision  string
	previousConfirmedValue string
	sequence               uint64
	confirmedSettings      excelPricingSettings
	confirmationSource     canonical.Source
	ackOnly                bool
	createdAt              time.Time
	nextAttemptAt          time.Time
}

type excelPricingWritebackQueue struct {
	mu          sync.Mutex
	server      *Server
	jobs        map[string]*excelPricingWritebackJob
	latestByKey map[string]string
	sequence    uint64
	wake        chan struct{}
	now         func() time.Time
	retryDelay  func(int) time.Duration
	process     func(context.Context, *excelPricingWritebackJob) excelPricingWritebackResult
}

type excelPricingWritebackResult struct {
	status         string
	code           string
	messageFA      string
	confirmedValue string
	stateRevision  string
	retryable      bool
	transactionID  string
	settingsDigest string
	ackDeadline    int64
	settings       excelPricingSettings
	source         canonical.Source
	attemptMS      int64
}

type excelPricingConfirmationRequest struct {
	Schema                  string               `json:"schema"`
	RequestID               string               `json:"request_id"`
	TransactionID           string               `json:"transaction_id"`
	CommittedStateRevision  string               `json:"committed_state_revision"`
	ConfirmedSettingsDigest string               `json:"confirmed_settings_digest"`
	ConfirmedSettings       excelPricingSettings `json:"confirmed_settings"`
}

func newExcelPricingWritebackQueue(server *Server) *excelPricingWritebackQueue {
	queue := &excelPricingWritebackQueue{
		server:      server,
		jobs:        make(map[string]*excelPricingWritebackJob),
		latestByKey: make(map[string]string),
		wake:        make(chan struct{}, 1),
		now:         time.Now,
		retryDelay: func(attempt int) time.Duration {
			return time.Duration(attempt*attempt) * time.Second
		},
	}
	queue.process = queue.processRemote
	return queue
}

func (queue *excelPricingWritebackQueue) start(ctx context.Context, group *sync.WaitGroup) {
	if queue == nil || ctx == nil || group == nil {
		return
	}
	group.Add(1)
	go func() {
		defer group.Done()
		queue.run(ctx)
	}()
}

func (queue *excelPricingWritebackQueue) run(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		job, wait := queue.next()
		if job != nil {
			result := queue.process(ctx, job)
			queue.finish(job, result)
			continue
		}
		if wait <= 0 {
			wait = time.Hour
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return
		case <-queue.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}

func (queue *excelPricingWritebackQueue) next() (*excelPricingWritebackJob, time.Duration) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	now := queue.now().UTC()
	queue.purgeLocked(now)
	var selected *excelPricingWritebackJob
	var earliest time.Time
	for _, job := range queue.jobs {
		if job.Status != "pending" && job.Status != "pending_ack" {
			continue
		}
		if !job.ackOnly {
			if current := queue.latestByKey[job.SettingKey]; current != job.JobID {
				queue.supersedeLocked(job, now)
				continue
			}
		}
		if job.nextAttemptAt.After(now) {
			if earliest.IsZero() || job.nextAttemptAt.Before(earliest) {
				earliest = job.nextAttemptAt
			}
			continue
		}
		if selected == nil || job.sequence < selected.sequence {
			selected = job
		}
	}
	if selected != nil {
		if selected.Status == "pending_ack" {
			selected.Status = "sending_ack"
			selected.Code = "sending_ack"
			selected.MessageFA = "نرخ تأییدشده در اکسل اعمال شد؛ در انتظار ثبت تأیید نهایی وب‌سایت است."
		} else {
			selected.Status = "sending"
			selected.Code = "sending"
			selected.MessageFA = "در حال ارسال امن به وردپرس و بررسی نتیجه است."
		}
		selected.Attempts++
		selected.UpdatedAt = now.Format(time.RFC3339)
		return cloneExcelPricingWritebackJob(selected), 0
	}
	if earliest.IsZero() {
		return nil, time.Hour
	}
	return nil, time.Until(earliest)
}

func (queue *excelPricingWritebackQueue) finish(job *excelPricingWritebackJob, result excelPricingWritebackResult) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	stored := queue.jobs[job.JobID]
	if stored == nil || stored.Status == "superseded" {
		return
	}
	now := queue.now().UTC()
	stored.LastAttemptMS = result.attemptMS
	stored.TotalElapsedMS = now.Sub(stored.createdAt).Milliseconds()
	if !stored.ackOnly && queue.latestByKey[stored.SettingKey] != stored.JobID {
		queue.supersedeLocked(stored, now)
		return
	}
	if result.retryable && stored.Attempts < excelPricingWritebackMaxAttempts {
		if stored.TransactionID != "" {
			stored.Status = "pending_ack"
		} else {
			stored.Status = "pending"
		}
		stored.Code = result.code
		stored.MessageFA = result.messageFA
		stored.Blocking = false
		stored.RetryCount++
		stored.LastRetryCode = result.code
		stored.LastRetryAt = now.Format(time.RFC3339)
		stored.nextAttemptAt = now.Add(queue.retryDelay(stored.Attempts))
		stored.UpdatedAt = now.Format(time.RFC3339)
		queue.signal()
		return
	}
	stored.Status = result.status
	stored.Code = result.code
	stored.MessageFA = result.messageFA
	stored.Blocking = result.status != "confirmed" && result.status != "awaiting_excel"
	stored.ConfirmedValue = result.confirmedValue
	stored.StateRevision = result.stateRevision
	if result.transactionID != "" {
		stored.TransactionID = result.transactionID
		stored.SettingsDigest = result.settingsDigest
		stored.ACKDeadline = result.ackDeadline
		stored.confirmedSettings = result.settings
		stored.confirmationSource = result.source
	}
	stored.UpdatedAt = now.Format(time.RFC3339)
}

func (queue *excelPricingWritebackQueue) persistSafeRebase(job *excelPricingWritebackJob) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	stored := queue.jobs[job.JobID]
	if stored == nil || stored.Status == "superseded" || stored.ackOnly {
		return
	}
	if queue.latestByKey[stored.SettingKey] != stored.JobID {
		return
	}
	stored.expectedStateRevision = job.expectedStateRevision
	stored.settings = job.settings
}

func (queue *excelPricingWritebackQueue) enqueue(request excelPricingWritebackRequest) (*excelPricingWritebackJob, error) {
	if request.Schema != excelPricingWritebackRequestSchema {
		return nil, errors.New("invalid_request_schema")
	}
	if !excelPricingIdempotencyPattern.MatchString(request.RequestID) {
		return nil, errors.New("invalid_request_id")
	}
	if !isSHA256Revision(request.ExpectedStateRevision) {
		return nil, errors.New("invalid_state_revision")
	}
	if err := validateExcelPricingSettings(request.Settings); err != nil {
		switch err.Error() {
		case "currency rate is invalid":
			return nil, errors.New("invalid_currency_rate")
		case "effective date is invalid":
			return nil, errors.New("invalid_pricing_date")
		case "profit is invalid":
			return nil, errors.New("invalid_profit_margin")
		case "shipping price is invalid":
			return nil, errors.New("invalid_shipping_price")
		case "shipping currency is invalid":
			return nil, errors.New("invalid_shipping_currency")
		case "shipping catalog revision is invalid":
			return nil, errors.New("invalid_shipping_revision")
		case "price rounding digits are invalid":
			return nil, errors.New("invalid_rounding_digits")
		case "price rounding mode is invalid":
			return nil, errors.New("invalid_rounding_mode")
		default:
			return nil, errors.New("invalid_pricing_settings")
		}
	}
	if strings.TrimSpace(request.PreviousConfirmedValue) == "" {
		return nil, errors.New("invalid_previous_confirmed_value")
	}
	if _, ok := excelPricingWritebackKeys[request.SettingKey]; !ok {
		return nil, errors.New("unsupported_setting")
	}
	desired, err := excelPricingSettingValue(request.Settings, request.SettingKey)
	if err != nil {
		return nil, errors.New("invalid_setting")
	}
	jobID, err := randomExcelPricingWritebackID()
	if err != nil {
		return nil, errors.New("queue_unavailable")
	}
	now := queue.now().UTC()
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.purgeLocked(now)
	if previousID := queue.latestByKey[request.SettingKey]; previousID != "" {
		if previous := queue.jobs[previousID]; previous != nil && previous.Status == "pending" {
			queue.supersedeLocked(previous, now)
		}
	}
	queue.sequence++
	job := &excelPricingWritebackJob{
		Schema:                 excelPricingWritebackJobSchema,
		JobID:                  jobID,
		RequestID:              request.RequestID,
		SettingKey:             request.SettingKey,
		DesiredValue:           desired,
		Status:                 "pending",
		Code:                   "queued",
		MessageFA:              "تغییر در صف ارسال امن به وردپرس قرار گرفت.",
		Blocking:               false,
		UpdatedAt:              now.Format(time.RFC3339),
		settings:               request.Settings,
		expectedStateRevision:  request.ExpectedStateRevision,
		previousConfirmedValue: strings.TrimSpace(request.PreviousConfirmedValue),
		sequence:               queue.sequence,
		createdAt:              now,
		nextAttemptAt:          now,
	}
	queue.jobs[jobID] = job
	queue.latestByKey[request.SettingKey] = jobID
	queue.signal()
	return cloneExcelPricingWritebackJob(job), nil
}

func (queue *excelPricingWritebackQueue) enqueueConfirmation(
	request excelPricingConfirmationRequest,
	source canonical.Source,
) (*excelPricingWritebackJob, error) {
	return queue.enqueueConfirmationState(request, source, false, 0)
}

func (queue *excelPricingWritebackQueue) enqueueDiscoveredConfirmation(
	request excelPricingConfirmationRequest,
	source canonical.Source,
	ackDeadline int64,
) (*excelPricingWritebackJob, error) {
	return queue.enqueueConfirmationState(request, source, true, ackDeadline)
}

func (queue *excelPricingWritebackQueue) enqueueConfirmationState(
	request excelPricingConfirmationRequest,
	source canonical.Source,
	awaitingExcel bool,
	ackDeadline int64,
) (*excelPricingWritebackJob, error) {
	if request.Schema != excelPricingConfirmationRequestSchema ||
		!excelPricingIdempotencyPattern.MatchString(request.RequestID) ||
		!validExcelPricingTransactionID(request.TransactionID) ||
		!isSHA256Revision(request.CommittedStateRevision) ||
		!isSHA256Revision(request.ConfirmedSettingsDigest) ||
		validateExcelPricingSettings(request.ConfirmedSettings) != nil ||
		!validExcelPricingRemoteSource(source) {
		return nil, errors.New("invalid_confirmation")
	}
	jobID, err := randomExcelPricingWritebackID()
	if err != nil {
		return nil, errors.New("queue_unavailable")
	}
	now := queue.now().UTC()
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.purgeLocked(now)
	queue.sequence++
	job := &excelPricingWritebackJob{
		Schema: excelPricingWritebackJobSchema, JobID: jobID, RequestID: request.RequestID,
		SettingKey: "site_confirmation", DesiredValue: strconv.FormatInt(request.ConfirmedSettings.YuanPrice, 10),
		ConfirmedValue: strconv.FormatInt(request.ConfirmedSettings.YuanPrice, 10),
		Status:         "pending_ack", Code: "excel_applied", MessageFA: "تغییر وب‌سایت در اکسل اعمال شد؛ تأیید نهایی در صف است.",
		StateRevision: request.CommittedStateRevision, TransactionID: request.TransactionID,
		SettingsDigest: request.ConfirmedSettingsDigest, UpdatedAt: now.Format(time.RFC3339),
		ACKDeadline:       ackDeadline,
		confirmedSettings: request.ConfirmedSettings, confirmationSource: source, ackOnly: true,
		sequence: queue.sequence, createdAt: now, nextAttemptAt: now,
	}
	if awaitingExcel {
		job.Status = "awaiting_excel"
		job.Code = "website_committed"
		job.MessageFA = "وب‌سایت نرخ را ثبت و قیمت‌ها را بازتولید کرد؛ در انتظار اعمال در اکسل و تأیید نهایی است."
	}
	queue.jobs[jobID] = job
	queue.signal()
	return cloneExcelPricingWritebackJob(job), nil
}

func validExcelPricingTransactionID(value string) bool {
	if !strings.HasPrefix(value, "ptx_") || len(value) != 36 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "ptx_"))
	return err == nil
}

func (queue *excelPricingWritebackQueue) acknowledge(jobID string) (*excelPricingWritebackJob, error) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	job := queue.jobs[jobID]
	if job == nil {
		return nil, errors.New("writeback_not_found")
	}
	switch job.Status {
	case "awaiting_excel":
		if queue.now().Unix() > job.ACKDeadline {
			return nil, errors.New("ack_deadline_expired")
		}
		job.Status = "pending_ack"
		job.Code = "excel_applied"
		job.MessageFA = "نرخ تأییدشده در اکسل اعمال شد؛ تأیید نهایی در صف است."
		job.UpdatedAt = queue.now().UTC().Format(time.RFC3339)
		queue.signal()
	case "pending_ack", "sending_ack", "confirmed":
		// Idempotent local acknowledgement/readback.
	default:
		return nil, errors.New("writeback_not_acknowledgeable")
	}
	return cloneExcelPricingWritebackJob(job), nil
}

func (queue *excelPricingWritebackQueue) get(jobID string) *excelPricingWritebackJob {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.purgeLocked(queue.now().UTC())
	return cloneExcelPricingWritebackJob(queue.jobs[jobID])
}

func (queue *excelPricingWritebackQueue) isLatest(job *excelPricingWritebackJob) bool {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return queue.latestByKey[job.SettingKey] == job.JobID
}

func (queue *excelPricingWritebackQueue) supersedeLocked(job *excelPricingWritebackJob, now time.Time) {
	job.Status = "superseded"
	job.Code = "superseded"
	job.MessageFA = "این تغییر با مقدار جدیدتر همان تنظیم جایگزین شد."
	job.Blocking = false
	job.UpdatedAt = now.Format(time.RFC3339)
}

func (queue *excelPricingWritebackQueue) purgeLocked(now time.Time) {
	for id, job := range queue.jobs {
		if now.Sub(job.createdAt) > excelPricingWritebackJobTTL {
			delete(queue.jobs, id)
			if queue.latestByKey[job.SettingKey] == id {
				delete(queue.latestByKey, job.SettingKey)
			}
		}
	}
	if len(queue.jobs) <= excelPricingWritebackMaxJobs {
		return
	}
	for id, job := range queue.jobs {
		if job.Status == "pending" || job.Status == "sending" ||
			job.Status == "pending_ack" || job.Status == "sending_ack" ||
			job.Status == "awaiting_excel" {
			continue
		}
		delete(queue.jobs, id)
		if len(queue.jobs) <= excelPricingWritebackMaxJobs {
			return
		}
	}
}

func (queue *excelPricingWritebackQueue) signal() {
	select {
	case queue.wake <- struct{}{}:
	default:
	}
}

func (queue *excelPricingWritebackQueue) processRemote(ctx context.Context, job *excelPricingWritebackJob) (result excelPricingWritebackResult) {
	started := time.Now()
	defer func() {
		result.attemptMS = time.Since(started).Milliseconds()
	}()
	server := queue.server
	if server == nil || server.excelPricing == nil || server.config == nil {
		return excelPricingWritebackFailure("remote_not_configured", true)
	}
	bounded, cancel := context.WithTimeout(ctx, excelPricingWritebackTimeout)
	defer cancel()
	// The queue is already single-worker and coalesces newer edits per setting.
	// Do not share the catalog snapshot permit here: an inbound product refresh
	// may legitimately run for tens of seconds while a pricing proposal must
	// still reach WordPress immediately. WordPress owns the cross-consumer
	// mutation lock, revision fence, atomic repricing, and ACK transaction.
	cfg := server.Config()
	if job.TransactionID != "" {
		return queue.ackRemote(bounded, cfg.SendUpdates, job)
	}
	source, err := server.excelPricingWritebackSource(bounded, cfg)
	if err != nil {
		return excelPricingWritebackFailure("canonical_source_unavailable", true)
	}
	// Read the live website state before mutating. Patris may legitimately move
	// the aggregate revision while the user is editing. Rebase only when the
	// edited setting still equals Excel's last confirmed value and every other
	// pricing setting is unchanged. A concurrent edit to the same setting stays
	// a blocking conflict and is never overwritten.
	currentDocument, readErr := queue.readbackDocument(bounded, job)
	if readErr == nil {
		// This revision belongs to WordPress's shipping-method catalog; it is not
		// the Patris product-source revision. Always rebase the derived marker from
		// the fresh website state while separately conflict-checking every editable
		// shipping/pricing value. This permits live Patris movement without coupling
		// two unrelated revision domains.
		job.settings = excelPricingSettingsWithCurrentWebsiteState(job.settings, currentDocument.Settings)
		currentValue, valueErr := excelPricingSettingValue(currentDocument.Settings, job.SettingKey)
		if valueErr == nil && currentValue == job.DesiredValue {
			return queue.resultFromCurrentDocument(job, source, currentDocument)
		}
		if currentDocument.StateRevision != job.expectedStateRevision {
			if valueErr != nil || currentValue != job.previousConfirmedValue ||
				!excelPricingSettingsEqualExcept(currentDocument.Settings, job.settings, job.SettingKey) {
				return excelPricingWritebackResult{
					status: "conflict", code: "unsafe_concurrent_setting_change",
					messageFA:      "همان تنظیم یا یکی از تنظیمات قیمت‌گذاری هم‌زمان تغییر کرده است؛ مقدار جدیدتر بازنویسی نشد.",
					confirmedValue: currentValue, stateRevision: currentDocument.StateRevision,
				}
			}
			job.expectedStateRevision = currentDocument.StateRevision
			job.settings.ShippingCatalogRevision = currentDocument.Settings.ShippingCatalogRevision
			queue.persistSafeRebase(job)
		}
	}
	previewID := "excel-writeback-" + job.JobID + "-preview"
	previewLocal := excelPricingLocalRequest{
		Schema: excelPricingLocalRequestSchema, SchemaVersion: 1,
		Operation: "preview", ClientID: excelPricingContractClientID,
		Channel: excelPricingContractChannel, RequestID: previewID,
		IdempotencyKey: previewID, ExpectedStateRevision: job.expectedStateRevision,
		Settings: &job.settings, ProductChanges: json.RawMessage(`[]`),
	}
	previewRemote := buildExcelPricingRemoteRequest("preview", previewLocal, source)
	preview, err := server.forwardExcelPricing(bounded, cfg.SendUpdates, "preview", previewRemote, previewLocal)
	if err != nil {
		return excelPricingWritebackErrorResult(err)
	}
	digest, present, err := excelPricingPreviewDigest(preview.body)
	if err != nil || !present {
		return excelPricingWritebackFailure("preview_contract_invalid", false)
	}
	if !queue.isLatest(job) {
		return excelPricingWritebackResult{status: "superseded", code: "superseded", messageFA: "این تغییر با مقدار جدیدتر همان تنظیم جایگزین شد."}
	}
	applyID := "excel-writeback-" + job.JobID + "-apply"
	applyLocal := previewLocal
	applyLocal.Operation = "apply"
	applyLocal.RequestID = applyID
	applyLocal.IdempotencyKey = applyID
	applyLocal.PreviewDigest = digest
	applyLocal.Confirmation = "APPLY"
	applyRemote := buildExcelPricingRemoteRequest("apply", applyLocal, source)
	applied, err := server.forwardExcelPricing(bounded, cfg.SendUpdates, "apply", applyRemote, applyLocal)
	if err != nil {
		if readback := queue.readback(bounded, job); readback.status == "confirmed" {
			return readback
		}
		return excelPricingWritebackErrorResult(err)
	}
	server.invalidateCanonicalProjection(true)
	server.excelPricing.snapshots.publishPricingStateInvalidated(applied.stateRevision)
	document, err := excelPricingStateDocumentFromBody(applied.body, excelPricingApplySchema)
	if err != nil || document.Confirmation.Status != "awaiting_ack" ||
		document.Confirmation.TransactionID == "" ||
		document.Confirmation.CommittedRevision != applied.stateRevision ||
		document.Confirmation.CommittedSettingsDigest == "" ||
		document.Confirmation.ACKDeadline <= time.Now().Unix() ||
		document.Confirmation.ACKPath != "/wp-json/digitalogic/pricing/sync/ack" ||
		document.Confirmation.ConsumerID != excelPricingContractClientID ||
		document.Confirmation.Channel != excelPricingContractChannel {
		return excelPricingWritebackFailure("confirmation_contract_invalid", false)
	}
	confirmed, err := excelPricingSettingValue(document.Settings, job.SettingKey)
	if err != nil || confirmed != job.DesiredValue {
		return excelPricingWritebackFailure("confirmation_value_conflict", false)
	}
	// The authenticated apply response is the website's terminal commit and
	// repricing receipt. Do not block the short ACK window on another full state
	// projection before Excel can apply the committed value. The ACK worker below
	// still performs an independent website readback before it marks the cell
	// green, so success is never inferred from the mutation response alone.
	server.excelPricing.snapshots.publishPricingStateVerified(applied.stateRevision)
	return excelPricingWritebackResult{
		status: "awaiting_excel", code: "website_committed",
		messageFA:      "وب‌سایت نرخ را ثبت و قیمت‌ها را بازتولید کرد؛ در انتظار اعمال در اکسل و تأیید نهایی است.",
		confirmedValue: confirmed, stateRevision: applied.stateRevision,
		transactionID:  document.Confirmation.TransactionID,
		settingsDigest: document.Confirmation.CommittedSettingsDigest,
		ackDeadline:    document.Confirmation.ACKDeadline,
		settings:       document.Settings, source: source,
	}
}

func (queue *excelPricingWritebackQueue) ackRemote(
	ctx context.Context,
	cfg updateout.Config,
	job *excelPricingWritebackJob,
) excelPricingWritebackResult {
	server := queue.server
	result, err := server.forwardExcelPricingACK(ctx, cfg, job)
	if err != nil {
		document, readErr := queue.readbackDocument(ctx, job)
		if readErr == nil {
			confirmed, valueErr := excelPricingSettingValue(document.Settings, job.SettingKey)
			if job.SettingKey == "site_confirmation" {
				confirmed = strconv.FormatInt(document.Settings.YuanPrice, 10)
				valueErr = nil
			}
			if valueErr == nil && document.StateRevision != job.StateRevision &&
				(document.Confirmation.Status == "rolled_back" || document.Confirmation.Status == "clear") {
				return excelPricingWritebackResult{
					status: "failed", code: "website_rollback_confirmed",
					messageFA:      "مهلت تأیید پایان یافت و وب‌سایت نرخ قبلی را بازگرداند؛ اکسل نیز به مقدار تأییدشده بازمی‌گردد.",
					confirmedValue: confirmed, stateRevision: document.StateRevision,
				}
			}
			if document.Confirmation.Status == "awaiting_ack" || document.Confirmation.Status == "rolling_back" ||
				document.Confirmation.Status == "rollback_pending" {
				return excelPricingWritebackResult{
					status: "failed", code: "ack_waiting_for_terminal_state",
					messageFA: "وب‌سایت در حال تعیین وضعیت نهایی تأیید یا بازگردانی است؛ تلاش مجدد خودکار انجام می‌شود.", retryable: true,
				}
			}
		}
		return excelPricingWritebackErrorResult(err)
	}
	if result.Status != "acknowledged" && result.Status != "replayed" {
		return excelPricingWritebackFailure("ack_contract_invalid", false)
	}
	document, err := queue.readbackDocument(ctx, job)
	if err != nil || document.StateRevision != job.StateRevision || document.Confirmation.Status != "clear" {
		return excelPricingWritebackFailure("ack_readback_unavailable", true)
	}
	confirmed, err := excelPricingSettingValue(document.Settings, job.SettingKey)
	if job.SettingKey == "site_confirmation" {
		confirmed = strconv.FormatInt(document.Settings.YuanPrice, 10)
		err = nil
	}
	if err != nil || confirmed != job.DesiredValue {
		return excelPricingWritebackFailure("ack_readback_conflict", false)
	}
	state := excelPricingWritebackResult{status: "confirmed", code: "confirmed", confirmedValue: confirmed, stateRevision: document.StateRevision}
	state.messageFA = "نرخ در وب‌سایت ثبت، در اکسل اعمال و تأیید نهایی تراکنش دریافت شد."
	return state
}

func (queue *excelPricingWritebackQueue) readback(ctx context.Context, job *excelPricingWritebackJob) excelPricingWritebackResult {
	document, err := queue.readbackDocument(ctx, job)
	if err != nil {
		return excelPricingWritebackFailure("readback_contract_invalid", false)
	}
	confirmed, err := excelPricingSettingValue(document.Settings, job.SettingKey)
	if job.SettingKey == "site_confirmation" {
		confirmed = strconv.FormatInt(document.Settings.YuanPrice, 10)
		err = nil
	}
	if err != nil {
		return excelPricingWritebackFailure("readback_contract_invalid", false)
	}
	if confirmed != job.DesiredValue {
		return excelPricingWritebackResult{
			status: "conflict", code: "readback_value_conflict",
			messageFA:      "وردپرس مقدار دیگری را برگرداند؛ صفحه را به‌روزرسانی کنید و دوباره تلاش کنید.",
			confirmedValue: confirmed, stateRevision: document.StateRevision,
		}
	}
	return excelPricingWritebackResult{
		status: "confirmed", code: "confirmed",
		messageFA:      "مقدار در وردپرس ثبت و با خواندن مجدد تأیید شد.",
		confirmedValue: confirmed, stateRevision: document.StateRevision,
	}
}

func (queue *excelPricingWritebackQueue) readbackDocument(ctx context.Context, job *excelPricingWritebackJob) (excelPricingStateDocument, error) {
	server := queue.server
	cfg := server.Config()
	source := job.confirmationSource
	if !validExcelPricingRemoteSource(source) {
		var err error
		source, err = server.excelPricingWritebackSource(ctx, cfg)
		if err != nil {
			return excelPricingStateDocument{}, err
		}
	}
	requestID := "excel-writeback-" + job.JobID + "-readback"
	local := excelPricingLocalRequest{
		Schema: excelPricingLocalRequestSchema, SchemaVersion: 1,
		Operation: "state", ClientID: excelPricingContractClientID,
		Channel: excelPricingContractChannel, RequestID: requestID,
		Source: &source, Page: 1, Limit: 1, Locale: "fa", Projection: "settings",
	}
	remoteRequest := buildExcelPricingRemoteRequest("state", local, source)
	state, err := server.forwardExcelPricing(ctx, cfg.SendUpdates, "state", remoteRequest, local)
	if err != nil {
		return excelPricingStateDocument{}, err
	}
	return excelPricingStateDocumentFromBody(state.body, excelPricingStateSchema)
}

func excelPricingStateDocumentFromBody(body []byte, expectedSchema string) (excelPricingStateDocument, error) {
	var document excelPricingStateDocument
	if json.Unmarshal(body, &document) != nil || document.Schema != expectedSchema ||
		!isSHA256Revision(document.StateRevision) || validateExcelPricingSettings(document.Settings) != nil {
		return excelPricingStateDocument{}, errors.New("invalid pricing state document")
	}
	return document, nil
}

func (s *Server) handlePostExcelPricingWriteback(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	if !s.authorizeExcelPricingWriteback(r) {
		writeExcelPricingError(w, http.StatusForbidden, "local_session_required")
		return
	}
	if !singleJSONContentType(r) {
		writeExcelPricingError(w, http.StatusUnsupportedMediaType, "json_required")
		return
	}
	var captured bytes.Buffer
	originalBody := r.Body
	r.Body = struct {
		io.Reader
		io.Closer
	}{Reader: io.TeeReader(originalBody, &captured), Closer: originalBody}
	var request excelPricingWritebackRequest
	if err := decodeBoundedJSON(w, r, excelPricingMaxRequestBytes, &request); err != nil {
		code := "invalid_json_body"
		var syntaxError *json.SyntaxError
		var typeError *json.UnmarshalTypeError
		switch {
		case errors.As(err, &syntaxError):
			field := excelPricingJSONFieldNear(captured.Bytes(), syntaxError.Offset)
			code = "invalid_json_syntax_at_" + strconv.FormatInt(syntaxError.Offset, 10)
			if field != "" {
				code += "_field_" + field
			}
		case errors.As(err, &typeError):
			field := strings.NewReplacer(".", "_", "-", "_").Replace(typeError.Field)
			code = "invalid_field_type_" + field
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			field := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), "\"")
			field = strings.NewReplacer(".", "_", "-", "_").Replace(field)
			code = "invalid_unknown_field_" + field
		case strings.Contains(err.Error(), "trailing JSON"):
			code = "invalid_json_trailing_data"
		}
		writeExcelPricingError(w, http.StatusBadRequest, code)
		return
	}
	job, err := s.excelPricingWrites.enqueue(request)
	if err != nil {
		code := err.Error()
		status := http.StatusBadRequest
		if code == "queue_unavailable" {
			status = http.StatusServiceUnavailable
		}
		writeExcelPricingError(w, status, code)
		return
	}
	writeExcelPricingJSON(w, http.StatusAccepted, job)
}

func excelPricingJSONFieldNear(body []byte, offset int64) string {
	end := int(offset)
	if end < 0 {
		return ""
	}
	if end > len(body) {
		end = len(body)
	}
	prefix := string(body[:end])
	colon := strings.LastIndex(prefix, ":")
	if colon < 0 {
		return ""
	}
	before := prefix[:colon]
	quoteEnd := strings.LastIndex(before, "\"")
	if quoteEnd < 1 {
		return ""
	}
	quoteStart := strings.LastIndex(before[:quoteEnd], "\"")
	if quoteStart < 0 || quoteEnd-quoteStart > 64 {
		return ""
	}
	field := before[quoteStart+1 : quoteEnd]
	var clean strings.Builder
	for _, character := range field {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' {
			clean.WriteRune(character)
		}
	}
	return clean.String()
}

func (s *Server) handleGetExcelPricingWriteback(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	if !s.authorizeExcelPricingWriteback(r) {
		writeExcelPricingError(w, http.StatusForbidden, "local_session_required")
		return
	}
	jobID := strings.TrimSpace(mux.Vars(r)["job_id"])
	if len(jobID) != 32 {
		writeExcelPricingError(w, http.StatusNotFound, "writeback_not_found")
		return
	}
	job := s.excelPricingWrites.get(jobID)
	if job == nil {
		writeExcelPricingError(w, http.StatusNotFound, "writeback_not_found")
		return
	}
	writeExcelPricingJSON(w, http.StatusOK, job)
}

func (s *Server) handlePostExcelPricingWritebackACK(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	if !s.authorizeExcelPricingWriteback(r) {
		writeExcelPricingError(w, http.StatusForbidden, "local_session_required")
		return
	}
	if !singleJSONContentType(r) {
		writeExcelPricingError(w, http.StatusUnsupportedMediaType, "json_required")
		return
	}
	var empty map[string]json.RawMessage
	if decodeBoundedJSON(w, r, 1024, &empty) != nil || empty == nil || len(empty) != 0 {
		writeExcelPricingError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	job, err := s.excelPricingWrites.acknowledge(strings.TrimSpace(mux.Vars(r)["job_id"]))
	if err != nil {
		status := http.StatusConflict
		if err.Error() == "writeback_not_found" {
			status = http.StatusNotFound
		}
		writeExcelPricingError(w, status, err.Error())
		return
	}
	writeExcelPricingJSON(w, http.StatusAccepted, job)
}

func (s *Server) handlePostExcelPricingConfirmation(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	if !s.authorizeExcelPricingWriteback(r) {
		writeExcelPricingError(w, http.StatusForbidden, "local_session_required")
		return
	}
	if !singleJSONContentType(r) {
		writeExcelPricingError(w, http.StatusUnsupportedMediaType, "json_required")
		return
	}
	var request excelPricingConfirmationRequest
	if decodeBoundedJSON(w, r, excelPricingMaxRequestBytes, &request) != nil {
		writeExcelPricingError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if request == (excelPricingConfirmationRequest{}) {
		job, err := s.discoverExcelPricingConfirmation(r.Context())
		if err != nil {
			status := http.StatusConflict
			if err.Error() == "remote_unavailable" || err.Error() == "canonical_source_unavailable" {
				status = http.StatusServiceUnavailable
			}
			writeExcelPricingError(w, status, err.Error())
			return
		}
		writeExcelPricingJSON(w, http.StatusAccepted, job)
		return
	}
	source, err := s.excelPricingWritebackSource(r.Context(), s.Config())
	if err != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "canonical_source_unavailable")
		return
	}
	job, err := s.excelPricingWrites.enqueueConfirmation(request, source)
	if err != nil {
		writeExcelPricingError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeExcelPricingJSON(w, http.StatusAccepted, job)
}

func (s *Server) discoverExcelPricingConfirmation(ctx context.Context) (*excelPricingWritebackJob, error) {
	if s == nil || s.excelPricingWrites == nil {
		return nil, errors.New("remote_unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	source, err := s.excelPricingWritebackSource(bounded, s.Config())
	if err != nil {
		return nil, errors.New("canonical_source_unavailable")
	}
	probe := &excelPricingWritebackJob{SettingKey: "site_confirmation", confirmationSource: source}
	document, err := s.excelPricingWrites.readbackDocument(bounded, probe)
	if err != nil {
		return nil, errors.New("remote_unavailable")
	}
	confirmation := document.Confirmation
	if confirmation.Status != "awaiting_ack" ||
		!validExcelPricingTransactionID(confirmation.TransactionID) ||
		confirmation.CommittedRevision != document.StateRevision ||
		!isSHA256Revision(confirmation.CommittedSettingsDigest) ||
		confirmation.ACKDeadline <= time.Now().Unix() ||
		confirmation.ACKPath != "/wp-json/digitalogic/pricing/sync/ack" ||
		confirmation.ConsumerID != excelPricingContractClientID ||
		confirmation.Channel != excelPricingContractChannel {
		return nil, errors.New("confirmation_not_pending")
	}
	request := excelPricingConfirmationRequest{
		Schema:                  excelPricingConfirmationRequestSchema,
		RequestID:               "excel-confirmation-" + strings.TrimPrefix(confirmation.TransactionID, "ptx_"),
		TransactionID:           confirmation.TransactionID,
		CommittedStateRevision:  document.StateRevision,
		ConfirmedSettingsDigest: confirmation.CommittedSettingsDigest,
		ConfirmedSettings:       document.Settings,
	}
	return s.excelPricingWrites.enqueueDiscoveredConfirmation(request, source, confirmation.ACKDeadline)
}

func (s *Server) excelPricingWritebackSource(ctx context.Context, cfg appconfig.Config) (canonical.Source, error) {
	if s != nil && s.excelPricingRemote != nil {
		if source, ok := s.excelPricingRemote.currentVerifiedSource(); ok {
			return source, nil
		}
	}
	if s != nil {
		s.lastRecordsMu.RLock()
		ready := s.lastRecordsReady
		revision := s.lastContractRevision
		s.lastRecordsMu.RUnlock()
		if ready && isSHA256Revision(revision) {
			source := canonical.SourceIdentity(s.currentDBPath(), cfg.Canonical.SourceID, revision)
			if validExcelPricingRemoteSource(source) {
				return source, nil
			}
		}
	}
	// Bounded cold-start fallback only. Once the authenticated event subscriber
	// or the in-memory watch baseline is live, all subsequent writebacks stay
	// independent of catalog projection.
	contract, err := s.excelPricingCanonical(ctx, cfg)
	if err != nil || contract == nil || !validExcelPricingRemoteSource(contract.Source) {
		return canonical.Source{}, errors.New("pricing source is unavailable")
	}
	return contract.Source, nil
}

func (s *Server) authorizeExcelPricingWriteback(r *http.Request) bool {
	return s != nil && s.excelPricing != nil && s.excelPricingWrites != nil &&
		excelPricingLocalRequestAllowed(r) &&
		singleHeaderEquals(r, excelPricingClientHeader, excelPricingClientID) &&
		s.excelPricing.authorizedSession(r)
}

func excelPricingSettingsFromState(body []byte) (excelPricingSettings, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil {
		return excelPricingSettings{}, errors.New("invalid state")
	}
	raw := root["settings"]
	if len(raw) == 0 && len(root["data"]) > 0 {
		var data map[string]json.RawMessage
		if json.Unmarshal(root["data"], &data) == nil {
			raw = data["settings"]
		}
	}
	var settings excelPricingSettings
	if len(raw) == 0 || json.Unmarshal(raw, &settings) != nil || validateExcelPricingSettings(settings) != nil {
		return excelPricingSettings{}, errors.New("state settings are invalid")
	}
	return settings, nil
}

func excelPricingSettingValue(settings excelPricingSettings, key string) (string, error) {
	switch key {
	case "yuan_price":
		return strconv.FormatInt(settings.YuanPrice, 10), nil
	case "dollar_price":
		return strconv.FormatInt(settings.DollarPrice, 10), nil
	case "cny_effective_date":
		return settings.CNYEffectiveDate, nil
	case "usd_effective_date":
		return settings.USDEffectiveDate, nil
	case "profit_margin_percent":
		return canonicalExcelPricingNumber(settings.ProfitMarginPercent.String())
	case "air_express_price_per_kg":
		return canonicalExcelPricingNumber(settings.AirExpressPricePerKG.String())
	case "price_rounding_digits":
		return canonicalExcelPricingNumber(settings.PriceRoundingDigits.String())
	default:
		return "", errors.New("unsupported setting")
	}
}

func canonicalExcelPricingNumber(value string) (string, error) {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return "", err
	}
	return strconv.FormatFloat(number, 'f', -1, 64), nil
}

func excelPricingWritebackErrorResult(err error) excelPricingWritebackResult {
	var remote *excelPricingRemoteError
	if errors.As(err, &remote) {
		retryable := remote.status == http.StatusTooManyRequests || remote.status >= 500 ||
			excelPricingWritebackRebaseCode(remote.code)
		message := "وردپرس تغییر را نپذیرفت: " + remote.code
		if remote.status == http.StatusConflict || remote.status == http.StatusPreconditionFailed {
			message = "مقدار یا نسخهٔ وردپرس تغییر کرده است؛ بازبینی خودکار انجام شد اما تعارض ایمن باقی ماند (" + remote.code + ")."
		}
		status := "failed"
		if !retryable && (remote.status == http.StatusConflict || remote.status == http.StatusPreconditionFailed) {
			status = "conflict"
		}
		return excelPricingWritebackResult{status: status, code: remote.code, messageFA: message, retryable: retryable}
	}
	return excelPricingWritebackFailure("remote_unavailable", true)
}

func excelPricingWritebackRebaseCode(code string) bool {
	switch strings.TrimSpace(code) {
	case "digitalogic_pricing_state_revision_conflict",
		"digitalogic_pricing_snapshot_source_revision_conflict",
		"digitalogic_pricing_source_revision_conflict",
		"digitalogic_product_sync_busy":
		return true
	default:
		return false
	}
}

func excelPricingSettingsEqualExcept(current, proposed excelPricingSettings, key string) bool {
	// The shipping catalog revision is a derived WordPress coherence marker,
	// not a user pricing input. Compare semantic pricing values while rebasing
	// this marker from the live website state.
	proposed.ShippingCatalogRevision = current.ShippingCatalogRevision
	switch key {
	case "yuan_price":
		proposed.YuanPrice = current.YuanPrice
	case "dollar_price":
		proposed.DollarPrice = current.DollarPrice
	case "cny_effective_date":
		proposed.CNYEffectiveDate = current.CNYEffectiveDate
		proposed.EffectiveDate = current.EffectiveDate
	case "usd_effective_date":
		proposed.USDEffectiveDate = current.USDEffectiveDate
	case "profit_margin_percent":
		proposed.ProfitMarginPercent = current.ProfitMarginPercent
	case "air_express_price_per_kg":
		proposed.AirExpressPricePerKG = current.AirExpressPricePerKG
	case "price_rounding_digits":
		proposed.PriceRoundingDigits = current.PriceRoundingDigits
	default:
		return false
	}
	return current == proposed
}

func excelPricingSettingsWithCurrentWebsiteState(settings, current excelPricingSettings) excelPricingSettings {
	settings.ShippingCatalogRevision = current.ShippingCatalogRevision
	return settings
}

func (queue *excelPricingWritebackQueue) resultFromCurrentDocument(
	job *excelPricingWritebackJob,
	source canonical.Source,
	document excelPricingStateDocument,
) excelPricingWritebackResult {
	confirmed, err := excelPricingSettingValue(document.Settings, job.SettingKey)
	if err != nil {
		return excelPricingWritebackFailure("readback_contract_invalid", false)
	}
	confirmation := document.Confirmation
	if confirmation.Status == "awaiting_ack" && confirmation.TransactionID != "" &&
		confirmation.CommittedRevision == document.StateRevision &&
		confirmation.CommittedSettingsDigest != "" &&
		confirmation.ACKDeadline > time.Now().Unix() &&
		confirmation.ACKPath == "/wp-json/digitalogic/pricing/sync/ack" &&
		confirmation.ConsumerID == excelPricingContractClientID &&
		confirmation.Channel == excelPricingContractChannel {
		return excelPricingWritebackResult{
			status: "awaiting_excel", code: "website_committed",
			messageFA:      "وب‌سایت نرخ را ثبت و قیمت‌ها را بازتولید کرد؛ در انتظار اعمال در اکسل و تأیید نهایی است.",
			confirmedValue: confirmed, stateRevision: document.StateRevision,
			transactionID: confirmation.TransactionID, settingsDigest: confirmation.CommittedSettingsDigest,
			ackDeadline: confirmation.ACKDeadline, settings: document.Settings, source: source,
		}
	}
	return excelPricingWritebackResult{
		status: "confirmed", code: "confirmed",
		messageFA:      "مقدار از قبل در وردپرس همین بود و با خواندن مجدد تأیید شد.",
		confirmedValue: confirmed, stateRevision: document.StateRevision,
	}
}

func excelPricingWritebackFailure(code string, retryable bool) excelPricingWritebackResult {
	messages := map[string]string{
		"remote_not_configured":          "ارتباط امن وردپرس در companion پیکربندی نشده است.",
		"pricing_busy":                   "عملیات قیمت دیگری در حال اجراست؛ تلاش مجدد خودکار انجام می‌شود.",
		"canonical_source_unavailable":   "دادهٔ مبنا آماده نیست؛ تلاش مجدد خودکار انجام می‌شود.",
		"preview_contract_invalid":       "پاسخ پیش‌نمایش وردپرس کامل نبود و هیچ تغییری اعمال نشد.",
		"post_apply_verification_failed": "ثبت انجام شد ولی تأیید نهایی کامل نشد؛ خواندن مجدد خودکار انجام می‌شود.",
		"readback_unavailable":           "خواندن مجدد وردپرس موقتاً ممکن نیست؛ تلاش مجدد خودکار انجام می‌شود.",
		"readback_contract_invalid":      "پاسخ خواندن مجدد وردپرس معتبر نبود؛ وضعیت موفق اعلام نشد.",
		"remote_unavailable":             "ارتباط با وردپرس برقرار نشد؛ تلاش مجدد خودکار انجام می‌شود.",
	}
	message := messages[code]
	if message == "" {
		message = "به‌روزرسانی قیمت به‌صورت ایمن تکمیل نشد: " + code
	}
	return excelPricingWritebackResult{status: "failed", code: code, messageFA: message, retryable: retryable}
}

func randomExcelPricingWritebackID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func cloneExcelPricingWritebackJob(job *excelPricingWritebackJob) *excelPricingWritebackJob {
	if job == nil {
		return nil
	}
	copy := *job
	return &copy
}
