package server

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/pricingcurrency"
	"github.com/gorilla/mux"
)

func currencyOwnerTerminal(status string) bool {
	switch status {
	case "confirmed", "failed", "publication_failed", "cancelled", "superseded":
		return true
	}
	return false
}

func ownerSettingsWriteback(job *excelPricingWritebackJob) bool {
	keys := excelPricingWritebackJobKeys(job)
	if len(keys) == 0 || job.ackOnly || job.TransactionID != "" {
		return false
	}
	for _, key := range keys {
		switch key {
		case "yuan_price", "dollar_price", "cny_effective_date", "usd_effective_date", "profit_margin_percent", "air_express_price_per_kg", "price_rounding_digits":
		default:
			return false
		}
	}
	return true
}

// All seven editable settings use one canonical settings admission/status path.
// Their website
// commit never depends on a workbook ACK or a client-side rollback deadline.
func (queue *excelPricingWritebackQueue) processOwnerCurrency(ctx context.Context, job *excelPricingWritebackJob) excelPricingWritebackResult {
	failed := func(code string) excelPricingWritebackResult {
		return excelPricingWritebackResult{status: "observation_required", code: code, messageFA: "نتیجهٔ درخواست ارز تأیید نشد؛ همان شناسهٔ درخواست را پیگیری کنید. ارسال خودکار تکرار نمی‌شود."}
	}
	origin, err := url.Parse(queue.server.Config().SendUpdates.URL)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil {
		return failed("currency_owner_not_configured")
	}
	client := pricingcurrency.Client{Origin: origin.Scheme + "://" + origin.Host, Key: os.Getenv("DIGITALOGIC_PRICING_WRITE_KEY"), Secret: os.Getenv("DIGITALOGIC_PRICING_WRITE_SECRET"), HTTPClient: queue.server.excelPricing.client}
	values, err := excelPricingWritebackValues(job.settings, job)
	if err != nil {
		return failed("invalid_currency_intent")
	}
	// The caller's identity survives a new local queue ID or process restart.
	// An unclassified missing/error response is not permission to submit again.
	owner, err := client.Observe(ctx, job.RequestID)
	observedExisting := err == nil
	if err != nil {
		if job.ownerObserveOnly || !queue.isLatest(job) {
			return failed("currency_owner_observation_failed")
		}
		var remote *pricingcurrency.Error
		if !errors.As(err, &remote) || !remote.NotFound {
			return failed("currency_owner_observation_failed")
		}
		if !queue.isLatest(job) {
			return excelPricingWritebackResult{status: "superseded", code: "superseded", messageFA: "درخواست جدیدتر جایگزین شد."}
		}
		owner, err = client.SubmitSettings(ctx, job.RequestID, job.expectedStateRevision, values)
		if err != nil {
			return failed("currency_owner_submission_unconfirmed")
		}
	}
	ownerID, generation := owner.JobID, owner.Generation
	for {
		if owner.JobID != ownerID || owner.Generation != generation || owner.RequestID != job.RequestID {
			return failed("currency_owner_identity_changed")
		}
		desiredFields := owner.DesiredFields
		// Retained old owner requests can still be observed by their original
		// identity. Every new submission uses settings intent and desired_fields.
		if len(desiredFields) == 0 && observedExisting {
			desiredFields = owner.DesiredCurrency
		}
		for key, want := range values {
			if !ownerSettingValueMatches(key, currencyRawString(desiredFields[key]), want) {
				return failed("currency_owner_intent_conflict")
			}
		}
		switch owner.Status {
		case "confirmed":
			ownerComplete := func(code string) excelPricingWritebackResult {
				return excelPricingWritebackResult{status: "owner_terminal", ownerStatus: "confirmed", code: code, messageFA: "تکمیل درخواست قبلی در مالک تأیید شد؛ تنظیمات فعلی اکسل تغییر نمی‌کند."}
			}
			if !queue.isLatest(job) {
				return ownerComplete("currency_owner_confirmed_historical")
			}
			state, readErr := client.Read(ctx)
			if readErr != nil {
				return ownerComplete("currency_owner_readback_failed")
			}
			var settings excelPricingSettings
			if json.Unmarshal(state.Settings, &settings) != nil || validateExcelPricingSettings(settings) != nil {
				return ownerComplete("currency_owner_settings_unverified")
			}
			document := excelPricingStateDocument{Settings: settings, StateRevision: state.StateRevision}
			if len(desiredFields) == 0 {
				return ownerComplete("currency_owner_desired_missing")
			}
			for key, raw := range desiredFields {
				var actual string
				if key == "effective_date" {
					actual = document.Settings.EffectiveDate
				} else {
					actual, err = excelPricingSettingValue(document.Settings, key)
					if err != nil {
						return ownerComplete("currency_owner_readback_field_invalid")
					}
				}
				if !ownerSettingValueMatches(key, actual, currencyRawString(raw)) {
					return ownerComplete("currency_owner_readback_changed")
				}
			}
			confirmed, readErr := excelPricingWritebackValues(document.Settings, job)
			if readErr != nil {
				return ownerComplete("currency_owner_readback_changed")
			}
			for key, want := range values {
				if !ownerSettingValueMatches(key, confirmed[key], want) {
					return ownerComplete("currency_owner_readback_changed")
				}
			}
			return excelPricingWritebackResult{status: "confirmed", ownerStatus: "confirmed", code: "confirmed", messageFA: "نرخ و تاریخ در مالک ثبت و قیمت‌ها تأیید شدند.", confirmedValue: confirmed[job.SettingKey], confirmedValues: confirmed, stateRevision: document.StateRevision, settings: document.Settings}
		case "queued", "running", "publishing", "awaiting_delivery", "cancelling":
		case "failed", "publication_failed", "cancelled", "superseded":
			return excelPricingWritebackResult{status: "owner_terminal", ownerStatus: owner.Status, code: "currency_owner_" + owner.Status, messageFA: "درخواست قبلی در مالک به وضعیت نهایی رسید؛ تنظیمات اکسل تغییر نمی‌کند."}
		default:
			return failed("currency_owner_" + owner.Status)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return failed("currency_owner_observation_timeout")
		case <-timer.C:
		}
		owner, err = client.Observe(ctx, job.RequestID)
		if err != nil {
			return failed("currency_owner_observation_failed")
		}
	}
}

// Compare decimal representations exactly; this is value normalization only.
func ownerSettingValueMatches(key, actual, expected string) bool {
	if key != "profit_margin_percent" && key != "air_express_price_per_kg" {
		return actual == expected
	}
	left, leftOK := new(big.Rat).SetString(actual)
	right, rightOK := new(big.Rat).SetString(expected)
	return leftOK && rightOK && left.Cmp(right) == 0
}

// Explicit recovery observes the existing owner identity; it cannot submit.
func (queue *excelPricingWritebackQueue) observeCurrency(jobID string) (*excelPricingWritebackJob, error) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	job := queue.jobs[jobID]
	if job == nil {
		return nil, errors.New("writeback_not_found")
	}
	if !ownerSettingsWriteback(job) {
		return nil, errors.New("writeback_not_observable")
	}
	if currencyOwnerTerminal(job.OwnerStatus) || job.Status == "confirmed" || job.Status == "pending" || job.Status == "sending" {
		return cloneExcelPricingWritebackJob(job), nil
	}
	if job.Status != "observation_required" {
		return nil, errors.New("writeback_not_observable")
	}
	job.ownerObserveOnly = true
	job.Status = "pending"
	job.nextAttemptAt = queue.now().UTC()
	queue.signal()
	return cloneExcelPricingWritebackJob(job), nil
}

func (s *Server) handlePostCurrencyWritebackObserve(w http.ResponseWriter, r *http.Request) {
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
	job, err := s.excelPricingWrites.observeCurrency(strings.TrimSpace(mux.Vars(r)["job_id"]))
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

// Current owner values are a separate read, never confirmation of an obsolete
// intent. No queue mutation, source refresh, snapshot or pricing admission.
func (s *Server) handleGetCurrencyWritebackReconcile(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	if !s.authorizeExcelPricingWriteback(r) {
		writeExcelPricingError(w, http.StatusForbidden, "local_session_required")
		return
	}
	job := s.excelPricingWrites.get(strings.TrimSpace(mux.Vars(r)["job_id"]))
	if job == nil {
		writeExcelPricingError(w, http.StatusNotFound, "writeback_not_found")
		return
	}
	if !ownerSettingsWriteback(job) || !currencyOwnerTerminal(job.OwnerStatus) {
		writeExcelPricingError(w, http.StatusConflict, "currency_owner_not_terminal")
		return
	}
	origin, err := url.Parse(s.Config().SendUpdates.URL)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "currency_owner_not_configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	client := pricingcurrency.Client{Origin: origin.Scheme + "://" + origin.Host, Key: os.Getenv("DIGITALOGIC_PRICING_WRITE_KEY"), Secret: os.Getenv("DIGITALOGIC_PRICING_WRITE_SECRET"), HTTPClient: s.excelPricing.client}
	state, err := client.Read(ctx)
	var settings excelPricingSettings
	if err != nil || json.Unmarshal(state.Settings, &settings) != nil || validateExcelPricingSettings(settings) != nil || !isSHA256Revision(state.StateRevision) {
		writeExcelPricingError(w, http.StatusBadGateway, "currency_owner_current_read_failed")
		return
	}
	writeExcelPricingJSON(w, http.StatusOK, map[string]interface{}{
		"schema": excelPricingWritebackJobSchema,
		"job_id": job.JobID, "request_id": job.RequestID,
		"owner_status": job.OwnerStatus, "status": "owner_terminal",
		"current_settings": settings, "current_state_revision": state.StateRevision,
		"reconciled_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func currencyRawString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return strings.TrimSpace(string(raw))
}
