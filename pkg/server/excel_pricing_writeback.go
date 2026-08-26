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

	"github.com/gorilla/mux"
)

const (
	excelPricingWritebackRequestSchema = "patris.pricing-input-writeback-request/v1"
	excelPricingWritebackJobSchema     = "patris.pricing-input-writeback-job/v1"
	excelPricingWritebackMaxJobs       = 256
	excelPricingWritebackMaxAttempts   = 3
	excelPricingWritebackJobTTL        = 30 * time.Minute
	excelPricingWritebackTimeout       = 8 * time.Minute
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
	Schema                string               `json:"schema"`
	RequestID             string               `json:"request_id"`
	SettingKey            string               `json:"setting_key"`
	ExpectedStateRevision string               `json:"expected_state_revision"`
	Settings              excelPricingSettings `json:"settings"`
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
	UpdatedAt      string `json:"updated_at"`

	settings              excelPricingSettings
	expectedStateRevision string
	sequence              uint64
	createdAt             time.Time
	nextAttemptAt         time.Time
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
		if job.Status != "pending" {
			continue
		}
		if current := queue.latestByKey[job.SettingKey]; current != job.JobID {
			queue.supersedeLocked(job, now)
			continue
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
		selected.Status = "sending"
		selected.Code = "sending"
		selected.MessageFA = "در حال ارسال امن به وردپرس و بررسی نتیجه است."
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
	if queue.latestByKey[stored.SettingKey] != stored.JobID {
		queue.supersedeLocked(stored, now)
		return
	}
	if result.retryable && stored.Attempts < excelPricingWritebackMaxAttempts {
		stored.Status = "pending"
		stored.Code = result.code
		stored.MessageFA = result.messageFA
		stored.Blocking = false
		stored.nextAttemptAt = now.Add(queue.retryDelay(stored.Attempts))
		stored.UpdatedAt = now.Format(time.RFC3339)
		queue.signal()
		return
	}
	stored.Status = result.status
	stored.Code = result.code
	stored.MessageFA = result.messageFA
	stored.Blocking = result.status != "confirmed"
	stored.ConfirmedValue = result.confirmedValue
	stored.StateRevision = result.stateRevision
	stored.UpdatedAt = now.Format(time.RFC3339)
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
		Schema:                excelPricingWritebackJobSchema,
		JobID:                 jobID,
		RequestID:             request.RequestID,
		SettingKey:            request.SettingKey,
		DesiredValue:          desired,
		Status:                "pending",
		Code:                  "queued",
		MessageFA:             "تغییر در صف ارسال امن به وردپرس قرار گرفت.",
		Blocking:              false,
		UpdatedAt:             now.Format(time.RFC3339),
		settings:              request.Settings,
		expectedStateRevision: request.ExpectedStateRevision,
		sequence:              queue.sequence,
		createdAt:             now,
		nextAttemptAt:         now,
	}
	queue.jobs[jobID] = job
	queue.latestByKey[request.SettingKey] = jobID
	queue.signal()
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
		if job.Status == "pending" || job.Status == "sending" {
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

func (queue *excelPricingWritebackQueue) processRemote(ctx context.Context, job *excelPricingWritebackJob) excelPricingWritebackResult {
	server := queue.server
	if server == nil || server.excelPricing == nil || server.config == nil {
		return excelPricingWritebackFailure("remote_not_configured", true)
	}
	bounded, cancel := context.WithTimeout(ctx, excelPricingWritebackTimeout)
	defer cancel()
	select {
	case server.excelPricing.permit <- struct{}{}:
		defer func() { <-server.excelPricing.permit }()
	case <-bounded.Done():
		return excelPricingWritebackFailure("pricing_busy", true)
	}
	cfg := server.Config()
	contract, err := server.excelPricingCanonical(bounded, cfg)
	if err != nil {
		return excelPricingWritebackFailure("canonical_source_unavailable", true)
	}
	// A same-value edit is the safest live acceptance strategy. Confirm it from
	// WordPress state before preview/apply so an idempotent no-op never waits on
	// a mutation pipeline that has nothing to change.
	if current := queue.readback(bounded, job); current.status == "confirmed" {
		current.messageFA = "مقدار از قبل در وردپرس همین بود و با خواندن مجدد تأیید شد."
		return current
	}
	previewID := "excel-writeback-" + job.JobID + "-preview"
	previewLocal := excelPricingLocalRequest{
		Schema: excelPricingLocalRequestSchema, SchemaVersion: 1,
		Operation: "preview", ClientID: excelPricingContractClientID,
		Channel: excelPricingContractChannel, RequestID: previewID,
		IdempotencyKey: previewID, ExpectedStateRevision: job.expectedStateRevision,
		Settings: &job.settings, ProductChanges: json.RawMessage(`[]`),
	}
	previewRemote := buildExcelPricingRemoteRequest("preview", previewLocal, contract.Source)
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
	applyRemote := buildExcelPricingRemoteRequest("apply", applyLocal, contract.Source)
	applied, err := server.forwardExcelPricing(bounded, cfg.SendUpdates, "apply", applyRemote, applyLocal)
	if err != nil {
		if readback := queue.readback(bounded, job); readback.status == "confirmed" {
			return readback
		}
		return excelPricingWritebackErrorResult(err)
	}
	server.invalidateCanonicalProjection(true)
	server.excelPricing.snapshots.publishPricingStateInvalidated(applied.stateRevision)
	if err := server.completeExcelPricingApply(bounded, cfg, applied); err != nil {
		if readback := queue.readback(bounded, job); readback.status == "confirmed" {
			return readback
		}
		return excelPricingWritebackFailure("post_apply_verification_failed", true)
	}
	server.excelPricing.snapshots.publishPricingStateVerified(applied.stateRevision)
	return queue.readback(bounded, job)
}

func (queue *excelPricingWritebackQueue) readback(ctx context.Context, job *excelPricingWritebackJob) excelPricingWritebackResult {
	server := queue.server
	cfg := server.Config()
	contract, err := server.excelPricingCanonical(ctx, cfg)
	if err != nil {
		return excelPricingWritebackFailure("readback_unavailable", true)
	}
	requestID := "excel-writeback-" + job.JobID + "-readback"
	local := excelPricingLocalRequest{
		Schema: excelPricingLocalRequestSchema, SchemaVersion: 1,
		Operation: "state", ClientID: excelPricingContractClientID,
		Channel: excelPricingContractChannel, RequestID: requestID,
		Source: &contract.Source, Page: 1, Limit: 1, Locale: "fa",
	}
	remoteRequest := buildExcelPricingRemoteRequest("state", local, contract.Source)
	state, err := server.forwardExcelPricing(ctx, cfg.SendUpdates, "state", remoteRequest, local)
	if err != nil {
		return excelPricingWritebackErrorResult(err)
	}
	settings, err := excelPricingSettingsFromState(state.body)
	if err != nil {
		return excelPricingWritebackFailure("readback_contract_invalid", false)
	}
	confirmed, err := excelPricingSettingValue(settings, job.SettingKey)
	if err != nil {
		return excelPricingWritebackFailure("readback_contract_invalid", false)
	}
	if confirmed != job.DesiredValue {
		return excelPricingWritebackResult{
			status: "conflict", code: "readback_value_conflict",
			messageFA:      "وردپرس مقدار دیگری را برگرداند؛ صفحه را به‌روزرسانی کنید و دوباره تلاش کنید.",
			confirmedValue: confirmed, stateRevision: state.stateRevision,
		}
	}
	return excelPricingWritebackResult{
		status: "confirmed", code: "confirmed",
		messageFA:      "مقدار در وردپرس ثبت و با خواندن مجدد تأیید شد.",
		confirmedValue: confirmed, stateRevision: state.stateRevision,
	}
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
		retryable := remote.status == http.StatusTooManyRequests || remote.status >= 500
		message := "وردپرس تغییر را نپذیرفت: " + remote.code
		if remote.status == http.StatusConflict || remote.status == http.StatusPreconditionFailed {
			message = "مقدار یا نسخهٔ وردپرس تغییر کرده است؛ ابتدا به‌روزرسانی کنید و دوباره تلاش کنید."
		}
		status := "failed"
		if !retryable && (remote.status == http.StatusConflict || remote.status == http.StatusPreconditionFailed) {
			status = "conflict"
		}
		return excelPricingWritebackResult{status: status, code: remote.code, messageFA: message, retryable: retryable}
	}
	return excelPricingWritebackFailure("remote_unavailable", true)
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
