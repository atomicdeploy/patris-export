package server

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/pricingcurrency"
)

// Explicit operator-only probe. Default is read-only; admitting the unchanged
// current value requires a second opt-in and a caller-supplied stable identity.
func TestLiveOwnerCurrencyReadback(t *testing.T) {
	if os.Getenv("DIGITALOGIC_LIVE_OWNER_PROBE") != "1" {
		t.Skip("operator opt-in required")
	}
	path := filepath.Join(os.Getenv("APPDATA"), "Patris Export", "config.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal("existing configuration required")
	}
	manager, err := appconfig.Load(path)
	if err != nil {
		t.Fatal("configuration unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	server := &Server{config: manager, excelPricing: newExcelPricingState()}
	queue := newExcelPricingWritebackQueue(server)
	origin, err := url.Parse(server.Config().SendUpdates.URL)
	if err != nil {
		t.Fatal("invalid origin")
	}
	c := pricingcurrency.Client{Origin: origin.Scheme + "://" + origin.Host, Key: os.Getenv("DIGITALOGIC_PRICING_WRITE_KEY"), Secret: os.Getenv("DIGITALOGIC_PRICING_WRITE_SECRET")}
	state, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var settings excelPricingSettings
	if json.Unmarshal(state.Settings, &settings) != nil || validateExcelPricingSettings(settings) != nil {
		t.Fatal("canonical owner settings invalid")
	}
	if os.Getenv("DIGITALOGIC_LIVE_OWNER_MODE") == "admit_unchanged" {
		id := os.Getenv("DIGITALOGIC_LIVE_OWNER_REQUEST_ID")
		if !strings.HasPrefix(id, "go-owner-unchanged-") {
			t.Fatal("explicit unique probe request identity required")
		}
		request := excelPricingWritebackRequest{Schema: excelPricingWritebackRequestSchema, RequestID: id, SettingKey: "yuan_price", Settings: settings, ExpectedStateRevision: state.StateRevision, PreviousConfirmedValue: strconv.FormatInt(settings.YuanPrice, 10)}
		accepted, err := queue.enqueue(request)
		if err != nil {
			t.Fatal(err)
		}
		job, _ := queue.next()
		if job == nil || job.JobID != accepted.JobID {
			t.Fatal("queue did not select submitted intent")
		}
		started := time.Now()
		result := queue.processRemote(ctx, job)
		queue.finish(job, result)
		confirmed := queue.get(job.JobID)
		if confirmed.Status != "confirmed" || confirmed.TransactionID != "" || confirmed.ACKDeadline != 0 || confirmed.ConfirmedSettings == nil || *confirmed.ConfirmedSettings != settings {
			t.Fatalf("request %s needs observation: status=%s code=%s", id, confirmed.Status, confirmed.Code)
		}
		owner, err := c.Observe(ctx, id)
		if err != nil || owner.Status != "confirmed" {
			t.Fatalf("request %s independent owner observation failed", id)
		}
		t.Logf("unchanged admission confirmed: request=%s owner_job=%s generation=%d cny=%d date=%s elapsed_ms=%d; no ACK", id, owner.JobID, owner.Generation, settings.YuanPrice, settings.CNYEffectiveDate, time.Since(started).Milliseconds())
		return
	}
	job := &excelPricingWritebackJob{JobID: strings.Repeat("e", 32), RequestID: "cli-cny-restore-20260909-02", SettingKey: "yuan_price", ownerObserveOnly: true, settings: settings, DesiredValue: strconv.FormatInt(settings.YuanPrice, 10)}
	started := time.Now()
	result := queue.processOwnerCurrency(ctx, job)
	if result.status != "confirmed" || result.transactionID != "" || result.ackDeadline != 0 {
		t.Fatalf("unconfirmed: status=%s code=%s", result.status, result.code)
	}
	t.Logf("read-only owner request confirmed: cny=%d date=%s elapsed_ms=%d; no admission permitted", result.settings.YuanPrice, result.settings.CNYEffectiveDate, time.Since(started).Milliseconds())
}
