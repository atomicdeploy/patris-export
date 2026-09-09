package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/pricingcurrency"
)

func currencyOnlyWriteback(job *excelPricingWritebackJob) bool {
	keys := excelPricingWritebackJobKeys(job)
	if len(keys) == 0 || job.ackOnly || job.TransactionID != "" {
		return false
	}
	for _, key := range keys {
		switch key {
		case "yuan_price", "dollar_price", "cny_effective_date", "usd_effective_date":
		default:
			return false
		}
	}
	return true
}

// Currency jobs use the canonical owner's admission/status path. Their website
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
	if err != nil {
		var remote *pricingcurrency.Error
		if !errors.As(err, &remote) || !remote.NotFound {
			return failed("currency_owner_observation_failed")
		}
		if !queue.isLatest(job) {
			return excelPricingWritebackResult{status: "superseded", code: "superseded", messageFA: "درخواست جدیدتر جایگزین شد."}
		}
		owner, err = client.Submit(ctx, job.RequestID, job.expectedStateRevision, values)
		if err != nil {
			return failed("currency_owner_submission_unconfirmed")
		}
	}
	ownerID, generation := owner.JobID, owner.Generation
	for {
		if owner.JobID != ownerID || owner.Generation != generation || owner.RequestID != job.RequestID {
			return failed("currency_owner_identity_changed")
		}
		for key, want := range values {
			if currencyRawString(owner.DesiredCurrency[key]) != want {
				return failed("currency_owner_intent_conflict")
			}
		}
		switch owner.Status {
		case "confirmed":
			document, readErr := queue.readbackDocument(ctx, job)
			if readErr != nil {
				return failed("currency_owner_readback_failed")
			}
			if len(owner.DesiredCurrency) == 0 {
				return failed("currency_owner_desired_missing")
			}
			for key, raw := range owner.DesiredCurrency {
				var actual string
				if key == "effective_date" {
					actual = document.Settings.EffectiveDate
				} else {
					actual, err = excelPricingSettingValue(document.Settings, key)
					if err != nil {
						return failed("currency_owner_readback_field_invalid")
					}
				}
				if actual != currencyRawString(raw) {
					return failed("currency_owner_readback_changed")
				}
			}
			confirmed, readErr := excelPricingWritebackValues(document.Settings, job)
			if readErr != nil || !excelPricingWritebackValuesMatchDesired(confirmed, job) {
				return failed("currency_owner_readback_changed")
			}
			return excelPricingWritebackResult{status: "confirmed", code: "confirmed", messageFA: "نرخ و تاریخ در مالک ثبت و قیمت‌ها تأیید شدند.", confirmedValue: confirmed[job.SettingKey], confirmedValues: confirmed, stateRevision: document.StateRevision, settings: document.Settings}
		case "queued", "running", "publishing", "awaiting_delivery", "cancelling":
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

func currencyRawString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return strings.TrimSpace(string(raw))
}
