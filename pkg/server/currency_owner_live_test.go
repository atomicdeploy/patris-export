package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

type liveCurrencyReadOnlyTransport struct{ writes int }

func (transport *liveCurrencyReadOnlyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet {
		transport.writes++
		return nil, errors.New("live recovery probe forbids mutation")
	}
	return http.DefaultTransport.RoundTrip(request)
}

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
	queue := newExcelPricingWritebackQueue(nil)
	queue.server = server
	queue.currencyJournalDir = t.TempDir()
	readOnly := &liveCurrencyReadOnlyTransport{}
	if os.Getenv("DIGITALOGIC_LIVE_OWNER_MODE") != "admit_unchanged" {
		server.excelPricing.client = &http.Client{Transport: readOnly, Timeout: 30 * time.Second}
	}
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
	request := excelPricingWritebackRequest{Schema: excelPricingWritebackRequestSchema, RequestID: "cli-cny-restore-20260909-02", SettingKey: "yuan_price", Settings: settings, ExpectedStateRevision: state.StateRevision, PreviousConfirmedValue: strconv.FormatInt(settings.YuanPrice, 10)}
	accepted, err := queue.enqueue(request)
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct from disk without starting a service or dispatching admission.
	restored := newExcelPricingWritebackQueue(nil)
	restored.server = server
	restored.currencyJournalDir = queue.currencyJournalDir
	if err = restored.loadCurrencyJournal(); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.observeCurrency(accepted.JobID); err != nil {
		t.Fatal(err)
	}
	job, _ := restored.next()
	if job == nil || !job.ownerObserveOnly || job.RequestID != request.RequestID {
		t.Fatal("restart identity lost")
	}
	started := time.Now()
	result := restored.processOwnerCurrency(ctx, job)
	restored.finish(job, result)
	if result.status != "confirmed" || result.transactionID != "" || result.ackDeadline != 0 {
		t.Fatalf("unconfirmed: status=%s code=%s", result.status, result.code)
	}
	entries, err := restored.readCurrencyJournal()
	if err != nil || len(entries) != 1 || entries[0].terminal == nil || readOnly.writes != 0 {
		t.Fatal("terminal persistence or read-only boundary failed")
	}
	t.Logf("read-only restart confirmed: request=%s cny=%d date=%s elapsed_ms=%d; terminal persisted; mutation attempts=%d", request.RequestID, result.settings.YuanPrice, result.settings.CNYEffectiveDate, time.Since(started).Milliseconds(), readOnly.writes)
}
