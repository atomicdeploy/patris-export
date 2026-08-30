package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

func TestExcelPricingWritebackQueueCoalescesNewestSettingAndRetriesBoundedly(t *testing.T) {
	queue := newExcelPricingWritebackQueue(nil)
	queue.retryDelay = func(int) time.Duration { return time.Millisecond }
	var calls atomic.Int32
	queue.process = func(_ context.Context, job *excelPricingWritebackJob) excelPricingWritebackResult {
		if calls.Add(1) == 1 {
			return excelPricingWritebackFailure("remote_unavailable", true)
		}
		return excelPricingWritebackResult{
			status: "confirmed", code: "confirmed", messageFA: "تأیید شد",
			confirmedValue: job.DesiredValue,
			stateRevision:  excelPricingRevisionForTest("confirmed"),
		}
	}

	first := validExcelPricingWritebackRequest("excel-writeback-test-0001", "yuan_price", 29500)
	firstJob, err := queue.enqueue(first)
	if err != nil {
		t.Fatal(err)
	}
	second := validExcelPricingWritebackRequest("excel-writeback-test-0002", "yuan_price", 29600)
	secondJob, err := queue.enqueue(second)
	if err != nil {
		t.Fatal(err)
	}
	if got := queue.get(firstJob.JobID); got == nil || got.Status != "superseded" {
		t.Fatalf("first job=%#v, want superseded", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var group sync.WaitGroup
	queue.start(ctx, &group)
	t.Cleanup(func() { cancel(); group.Wait() })
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job := queue.get(secondJob.JobID)
		if job != nil && job.Status == "confirmed" {
			if job.Attempts != 2 || job.ConfirmedValue != "29600" {
				t.Fatalf("confirmed job=%#v", job)
			}
			if job.RetryCount != 1 || job.LastRetryCode != "remote_unavailable" || job.LastRetryAt == "" {
				t.Fatalf("retry diagnostics=%#v", job)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("second job did not confirm: %#v", queue.get(secondJob.JobID))
}

func TestExcelPricingWritebackRejectsUnsupportedOrProductFields(t *testing.T) {
	server := newExcelPricingTestServer(t, "http://127.0.0.1:9/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	request := validExcelPricingWritebackRequest("excel-writeback-test-0003", "yuan_price", 29500)
	body, _ := json.Marshal(request)
	var payload map[string]interface{}
	_ = json.Unmarshal(body, &payload)
	payload["product_changes"] = []interface{}{map[string]interface{}{"sku": "unsafe"}}
	body, _ = json.Marshal(payload)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/writebacks", string(body), token,
	))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("product field status=%d, want 400: %s", response.Code, response.Body.String())
	}

	request = validExcelPricingWritebackRequest("excel-writeback-test-0004", "supplier_price", 29500)
	body, _ = json.Marshal(request)
	response = httptest.NewRecorder()
	server.router.ServeHTTP(response, authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/writebacks", string(body), token,
	))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "unsupported_setting") {
		t.Fatalf("unsupported status=%d: %s", response.Code, response.Body.String())
	}
}

func TestExcelPricingWritebackWorkerUsesPreviewApplyIdempotencyAndReadback(t *testing.T) {
	initialRevision := excelPricingRevisionForTest("writeback-initial")
	confirmedRevision := excelPricingRevisionForTest("writeback-confirmed")
	previewDigest := excelPricingRevisionForTest("writeback-preview")
	settingsDigest := excelPricingRevisionForTest("writeback-settings")
	settings := validExcelPricingWritebackRequest("excel-writeback-test-0005", "yuan_price", 29500).Settings
	var previewCalls atomic.Int32
	var applyCalls atomic.Int32
	var stateCalls atomic.Int32
	var ackReceived atomic.Bool
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/preview"):
			previewCalls.Add(1)
			if !strings.Contains(r.Header.Get("Idempotency-Key"), "-preview") ||
				r.Header.Get("If-Match") != `"`+initialRevision+`"` {
				t.Fatalf("preview headers idempotency=%q if-match=%q", r.Header.Get("Idempotency-Key"), r.Header.Get("If-Match"))
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schema": excelPricingPreviewSchema, "state_revision": initialRevision,
				"preview_digest": previewDigest,
			})
		case strings.HasSuffix(r.URL.Path, "/apply"):
			applyCalls.Add(1)
			if !strings.Contains(r.Header.Get("Idempotency-Key"), "-apply") ||
				r.Header.Get("If-Match") != `"`+initialRevision+`"` {
				t.Fatalf("apply headers idempotency=%q if-match=%q", r.Header.Get("Idempotency-Key"), r.Header.Get("If-Match"))
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schema": excelPricingApplySchema, "state_revision": confirmedRevision,
				"settings": settings,
				"confirmation": map[string]interface{}{
					"schema": excelPricingConfirmationSchema, "status": "awaiting_ack",
					"transaction_id":            "ptx_0123456789abcdef0123456789abcdef",
					"committed_revision":        confirmedRevision,
					"committed_settings_digest": settingsDigest,
					"ack_deadline":              time.Now().Add(time.Minute).Unix(),
					"ack_path":                  "/wp-json/digitalogic/pricing/sync/ack",
					"consumer_id":               excelPricingContractClientID,
					"channel":                   excelPricingContractChannel,
				},
			})
		case strings.HasSuffix(r.URL.Path, "/ack"):
			ackReceived.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schema": excelPricingConfirmationSchema, "status": "acknowledged",
				"transaction_id": "ptx_0123456789abcdef0123456789abcdef",
			})
		case strings.HasSuffix(r.URL.Path, "/state"):
			var stateRequest excelPricingRemoteRequest
			if err := json.NewDecoder(r.Body).Decode(&stateRequest); err != nil || stateRequest.Projection != "settings" {
				t.Errorf("writeback state projection=%q error=%v, want settings", stateRequest.Projection, err)
			}
			call := stateCalls.Add(1)
			stateSettings := settings
			stateRevision := confirmedRevision
			if call == 1 {
				stateSettings.YuanPrice = 29400
				stateRevision = initialRevision
			}
			confirmationStatus := "awaiting_ack"
			if ackReceived.Load() {
				confirmationStatus = "clear"
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schema": excelPricingStateSchema, "state_revision": stateRevision,
				"settings": stateSettings, "confirmation": map[string]interface{}{
					"schema": excelPricingConfirmationSchema, "status": confirmationStatus,
				},
			})
		default:
			t.Fatalf("unexpected remote path %q", r.URL.Path)
		}
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.excelPricing.dispatch = func(_ context.Context, _ updateout.Config, event updateout.Event) (updateout.DeliveryResult, error) {
		return updateout.DeliveryResult{
			Status: "accepted", HTTPStatus: http.StatusOK, Attempts: 1, EventID: event.Contract.EventID,
		}, nil
	}
	token := openExcelPricingSession(t, server)
	request := validExcelPricingWritebackRequest("excel-writeback-test-0005", "yuan_price", 29500)
	request.ExpectedStateRevision = initialRevision
	request.PreviousConfirmedValue = "29400"
	body, _ := json.Marshal(request)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/writebacks", string(body), token,
	))
	if response.Code != http.StatusAccepted {
		t.Fatalf("enqueue status=%d: %s", response.Code, response.Body.String())
	}
	var accepted excelPricingWritebackJob
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	ackSent := false
	for time.Now().Before(deadline) {
		poll := httptest.NewRecorder()
		server.router.ServeHTTP(poll, authenticatedExcelPricingRequest(
			http.MethodGet, "/api/pricing-sync/writebacks/"+accepted.JobID, "", token,
		))
		var job excelPricingWritebackJob
		if poll.Code == http.StatusOK && json.Unmarshal(poll.Body.Bytes(), &job) == nil && job.Status == "awaiting_excel" && !ackSent {
			ack := httptest.NewRecorder()
			server.router.ServeHTTP(ack, authenticatedExcelPricingRequest(
				http.MethodPost, "/api/pricing-sync/writebacks/"+accepted.JobID+"/ack", "{}", token,
			))
			if ack.Code != http.StatusAccepted {
				t.Fatalf("ack status=%d: %s", ack.Code, ack.Body.String())
			}
			ackSent = true
		}
		if poll.Code == http.StatusOK && json.Unmarshal(poll.Body.Bytes(), &job) == nil && job.Status == "confirmed" {
			if job.ConfirmedValue != "29500" || job.StateRevision != confirmedRevision {
				t.Fatalf("confirmed job=%#v", job)
			}
			if !ackSent || previewCalls.Load() != 1 || applyCalls.Load() != 1 || stateCalls.Load() < 2 {
				t.Fatalf("remote calls preview=%d apply=%d state=%d", previewCalls.Load(), applyCalls.Load(), stateCalls.Load())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("writeback did not confirm: %#v", server.excelPricingWrites.get(accepted.JobID))
}

func TestExcelPricingBatchWritebackReturnsFullWebsiteReadbackBeforeACK(t *testing.T) {
	initialRevision := excelPricingRevisionForTest("batch-initial")
	confirmedRevision := excelPricingRevisionForTest("batch-confirmed")
	previewDigest := excelPricingRevisionForTest("batch-preview")
	settingsDigest := excelPricingRevisionForTest("batch-settings")
	request := validExcelPricingWritebackRequest("excel-settings-batch-remote", "yuan_price", 30000)
	request.Schema = excelPricingWritebackBatchRequestSchema
	request.SettingKey = ""
	request.PreviousConfirmedValue = ""
	request.SettingKeys = []string{"yuan_price", "profit_margin_percent"}
	request.PreviousConfirmedValues = map[string]string{
		"yuan_price": "29500", "profit_margin_percent": "30",
	}
	request.ExpectedStateRevision = initialRevision
	request.Settings.ProfitMarginPercent = json.Number("31")
	current := request.Settings
	current.YuanPrice = 29500
	current.ProfitMarginPercent = json.Number("30")
	var applied atomic.Bool
	var acked atomic.Bool
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/preview"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schema": excelPricingPreviewSchema, "state_revision": initialRevision,
				"preview_digest": previewDigest,
			})
		case strings.HasSuffix(r.URL.Path, "/apply"):
			applied.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schema": excelPricingApplySchema, "state_revision": confirmedRevision,
				"settings": request.Settings,
				"confirmation": map[string]interface{}{
					"schema": excelPricingConfirmationSchema, "status": "awaiting_ack",
					"transaction_id":            "ptx_abcdef0123456789abcdef0123456789",
					"committed_revision":        confirmedRevision,
					"committed_settings_digest": settingsDigest,
					"ack_deadline":              time.Now().Add(time.Minute).Unix(),
					"ack_path":                  "/wp-json/digitalogic/pricing/sync/ack",
					"consumer_id":               excelPricingContractClientID,
					"channel":                   excelPricingContractChannel,
				},
			})
		case strings.HasSuffix(r.URL.Path, "/ack"):
			acked.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schema": excelPricingConfirmationSchema, "status": "acknowledged",
				"transaction_id": "ptx_abcdef0123456789abcdef0123456789",
			})
		case strings.HasSuffix(r.URL.Path, "/state"):
			settings := current
			revision := initialRevision
			status := "clear"
			if applied.Load() {
				settings = request.Settings
				revision = confirmedRevision
				status = "awaiting_ack"
			}
			if acked.Load() {
				status = "clear"
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schema": excelPricingStateSchema, "state_revision": revision,
				"settings": settings, "confirmation": map[string]interface{}{
					"schema": excelPricingConfirmationSchema, "status": status,
				},
			})
		default:
			t.Fatalf("unexpected remote path %q", r.URL.Path)
		}
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	body, _ := json.Marshal(request)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/writebacks", string(body), token,
	))
	if response.Code != http.StatusAccepted {
		t.Fatalf("batch enqueue status=%d: %s", response.Code, response.Body.String())
	}
	var accepted excelPricingWritebackJob
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	ackSent := false
	for time.Now().Before(deadline) {
		job := server.excelPricingWrites.get(accepted.JobID)
		if job != nil && job.Status == "awaiting_excel" && !ackSent {
			if job.ConfirmedSettings == nil || job.ConfirmedSettings.YuanPrice != 30000 ||
				job.ConfirmedSettings.ProfitMarginPercent != json.Number("31") ||
				job.ConfirmedValues["yuan_price"] != "30000" ||
				job.ConfirmedValues["profit_margin_percent"] != "31" {
				t.Fatalf("batch website readback=%#v", job)
			}
			if _, err := server.excelPricingWrites.acknowledge(job.JobID); err != nil {
				t.Fatal(err)
			}
			ackSent = true
		}
		if job != nil && job.Status == "confirmed" {
			if !ackSent || job.ConfirmedSettings == nil ||
				job.ConfirmedValues["yuan_price"] != "30000" ||
				job.ConfirmedValues["profit_margin_percent"] != "31" {
				t.Fatalf("confirmed batch=%#v", job)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("batch writeback did not confirm: %#v", server.excelPricingWrites.get(accepted.JobID))
}

func TestExcelPricingWritebackNoopConfirmsFromReadbackWithoutApply(t *testing.T) {
	confirmedRevision := excelPricingRevisionForTest("writeback-noop-confirmed")
	settings := validExcelPricingWritebackRequest("excel-writeback-test-0006", "yuan_price", 29500).Settings
	var stateCalls atomic.Int32
	var mutationCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasSuffix(r.URL.Path, "/state") {
			mutationCalls.Add(1)
			t.Fatalf("no-op writeback reached mutation path %q", r.URL.Path)
		}
		var stateRequest excelPricingRemoteRequest
		if err := json.NewDecoder(r.Body).Decode(&stateRequest); err != nil || stateRequest.Projection != "settings" {
			t.Errorf("no-op state projection=%q error=%v, want settings", stateRequest.Projection, err)
		}
		stateCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"schema": excelPricingStateSchema, "state_revision": confirmedRevision,
			"settings": settings,
		})
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	// Simulate an unrelated inbound catalog refresh. Pricing writeback has its
	// own serial queue and must not wait for this projection permit.
	server.excelPricing.permit <- struct{}{}
	t.Cleanup(func() { <-server.excelPricing.permit })
	token := openExcelPricingSession(t, server)
	request := validExcelPricingWritebackRequest("excel-writeback-test-0006", "yuan_price", 29500)
	body, _ := json.Marshal(request)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/writebacks", string(body), token,
	))
	if response.Code != http.StatusAccepted {
		t.Fatalf("enqueue status=%d: %s", response.Code, response.Body.String())
	}
	var accepted excelPricingWritebackJob
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		job := server.excelPricingWrites.get(accepted.JobID)
		if job != nil && job.Status == "confirmed" {
			if job.ConfirmedValue != "29500" || job.StateRevision != confirmedRevision {
				t.Fatalf("confirmed job=%#v", job)
			}
			if stateCalls.Load() != 1 || mutationCalls.Load() != 0 {
				t.Fatalf("state calls=%d mutation calls=%d", stateCalls.Load(), mutationCalls.Load())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no-op writeback did not confirm: %#v", server.excelPricingWrites.get(accepted.JobID))
}

func TestExcelPricingWritebackACKIsBoundedAndCannotConfirmLateOrWrongJobs(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	queue := newExcelPricingWritebackQueue(nil)
	queue.now = func() time.Time { return now }
	queue.jobs["0123456789abcdef0123456789abcdef"] = &excelPricingWritebackJob{
		JobID: "0123456789abcdef0123456789abcdef", Status: "awaiting_excel",
		TransactionID: "ptx_0123456789abcdef0123456789abcdef", ACKDeadline: now.Add(-time.Second).Unix(),
		createdAt: now,
	}
	if _, err := queue.acknowledge("ffffffffffffffffffffffffffffffff"); err == nil || err.Error() != "writeback_not_found" {
		t.Fatalf("wrong job acknowledgement error=%v", err)
	}
	if _, err := queue.acknowledge("0123456789abcdef0123456789abcdef"); err == nil || err.Error() != "ack_deadline_expired" {
		t.Fatalf("late acknowledgement error=%v", err)
	}
	if got := queue.get("0123456789abcdef0123456789abcdef"); got == nil || got.Status != "awaiting_excel" {
		t.Fatalf("late acknowledgement mutated job: %#v", got)
	}
}

func TestExcelPricingWritebackRebaseAllowsLivePatrisRevisionButNotConcurrentInputs(t *testing.T) {
	proposed := validExcelPricingWritebackRequest("excel-writeback-rebase-0001", "yuan_price", 29500).Settings
	current := proposed
	current.YuanPrice = 29501
	current.ShippingCatalogRevision = excelPricingRevisionForTest("new-live-patris-revision")
	if !excelPricingSettingsEqualExcept(current, proposed, "yuan_price") {
		t.Fatal("live Patris shipping revision should be rebased while the target rate uses its prior confirmed value")
	}
	current.DollarPrice++
	if excelPricingSettingsEqualExcept(current, proposed, "yuan_price") {
		t.Fatal("a concurrent user pricing-input change must remain blocking")
	}
	for _, code := range []string{
		"digitalogic_pricing_state_revision_conflict",
		"digitalogic_pricing_snapshot_source_revision_conflict",
		"digitalogic_pricing_source_revision_conflict",
		"digitalogic_product_sync_busy",
	} {
		if !excelPricingWritebackRebaseCode(code) {
			t.Fatalf("%s should trigger bounded automatic rebase", code)
		}
	}
	websiteState := current
	websiteState.ShippingCatalogRevision = excelPricingRevisionForTest("fresh-website-shipping-catalog")
	rebased := excelPricingSettingsWithCurrentWebsiteState(proposed, websiteState)
	if rebased.ShippingCatalogRevision != websiteState.ShippingCatalogRevision {
		t.Fatalf("derived shipping revision=%q, want current website state %q", rebased.ShippingCatalogRevision, websiteState.ShippingCatalogRevision)
	}
	if rebased.YuanPrice != proposed.YuanPrice || rebased.DollarPrice != proposed.DollarPrice ||
		rebased.ProfitMarginPercent != proposed.ProfitMarginPercent {
		t.Fatal("live-source rebase changed a user-controlled pricing input")
	}
	if rebased.ShippingCatalogRevision == excelPricingRevisionForTest("new-live-patris-revision") {
		t.Fatal("WordPress shipping revision was incorrectly coupled to the Patris product-source revision")
	}
}

func TestExcelPricingBatchWritebackAcceptsOnlyChangedDeclaredSettings(t *testing.T) {
	queue := newExcelPricingWritebackQueue(nil)
	request := validExcelPricingWritebackRequest(
		"excel-settings-batch-0001", "yuan_price", 30000,
	)
	request.Schema = excelPricingWritebackBatchRequestSchema
	request.SettingKey = ""
	request.PreviousConfirmedValue = ""
	request.SettingKeys = []string{"yuan_price", "profit_margin_percent"}
	request.PreviousConfirmedValues = map[string]string{
		"yuan_price": "29500", "profit_margin_percent": "30",
	}
	request.Settings.ProfitMarginPercent = json.Number("31")
	job, err := queue.enqueue(request)
	if err != nil {
		t.Fatal(err)
	}
	if job.SettingKey != "settings_batch" ||
		len(job.SettingKeys) != 2 ||
		job.DesiredValues["yuan_price"] != "30000" ||
		job.DesiredValues["profit_margin_percent"] != "31" {
		t.Fatalf("batch job=%#v", job)
	}

	// Enqueue owns an immutable copy of every conflict fence and desired value.
	request.PreviousConfirmedValues["yuan_price"] = "1"
	request.SettingKeys[0] = "dollar_price"
	request.Settings.YuanPrice = 1
	stored := queue.get(job.JobID)
	if stored.SettingKeys[0] != "yuan_price" ||
		stored.previousConfirmedValues["yuan_price"] != "29500" ||
		stored.settings.YuanPrice != 30000 {
		t.Fatalf("queued intent was mutable: %#v", stored)
	}

	unchanged := validExcelPricingWritebackRequest(
		"excel-settings-batch-0002", "yuan_price", 30000,
	)
	unchanged.Schema = excelPricingWritebackBatchRequestSchema
	unchanged.SettingKey = ""
	unchanged.PreviousConfirmedValue = ""
	unchanged.SettingKeys = []string{"yuan_price"}
	unchanged.PreviousConfirmedValues = map[string]string{"yuan_price": "30000"}
	if _, err := queue.enqueue(unchanged); err == nil || err.Error() != "unchanged_setting" {
		t.Fatalf("unchanged batch error=%v", err)
	}

	extraFence := unchanged
	extraFence.PreviousConfirmedValues = map[string]string{
		"yuan_price": "29500", "dollar_price": "187891",
	}
	if _, err := queue.enqueue(extraFence); err == nil || err.Error() != "unexpected_previous_confirmed_value" {
		t.Fatalf("extra batch fence error=%v", err)
	}
}

func TestExcelPricingWritebackCannotReplaceInFlightWebsiteTransaction(t *testing.T) {
	queue := newExcelPricingWritebackQueue(nil)
	request := validExcelPricingWritebackRequest("excel-writeback-inflight-0001", "yuan_price", 29500)
	job, err := queue.enqueue(request)
	if err != nil {
		t.Fatal(err)
	}
	queue.mu.Lock()
	queue.jobs[job.JobID].Status = "awaiting_excel"
	queue.mu.Unlock()
	newer := validExcelPricingWritebackRequest("excel-writeback-inflight-0002", "yuan_price", 29600)
	if _, err := queue.enqueue(newer); err == nil || err.Error() != "writeback_in_flight" {
		t.Fatalf("overlapping in-flight writeback error=%v", err)
	}
	if got := queue.get(job.JobID); got == nil || got.Status != "awaiting_excel" {
		t.Fatalf("in-flight transaction was superseded: %#v", got)
	}
}

func TestExcelPricingBatchWritebackRebaseFencesAllAndOnlyChangedSettings(t *testing.T) {
	proposed := validExcelPricingWritebackRequest(
		"excel-settings-batch-rebase", "yuan_price", 30000,
	).Settings
	proposed.ProfitMarginPercent = json.Number("31")
	current := proposed
	current.YuanPrice = 29500
	current.ProfitMarginPercent = json.Number("30")
	current.ShippingCatalogRevision = excelPricingRevisionForTest("fresh-shipping")
	keys := []string{"yuan_price", "profit_margin_percent"}
	if !excelPricingSettingsEqualExceptKeys(current, proposed, keys) {
		t.Fatal("declared batch settings should be safely rebased together")
	}
	job := &excelPricingWritebackJob{
		SettingKey: "settings_batch", SettingKeys: keys,
		DesiredValues: map[string]string{
			"yuan_price": "30000", "profit_margin_percent": "31",
		},
		previousConfirmedValues: map[string]string{
			"yuan_price": "29500", "profit_margin_percent": "30",
		},
	}
	values, err := excelPricingWritebackValues(current, job)
	if err != nil || !excelPricingWritebackCurrentValuesSafe(values, job) {
		t.Fatalf("safe batch values=%v error=%v", values, err)
	}
	current.DollarPrice++
	if excelPricingSettingsEqualExceptKeys(current, proposed, keys) {
		t.Fatal("an undeclared website setting change must remain a conflict")
	}
	current.DollarPrice = proposed.DollarPrice
	current.YuanPrice = 29700
	values, _ = excelPricingWritebackValues(current, job)
	if excelPricingWritebackCurrentValuesSafe(values, job) {
		t.Fatal("a third value for a declared setting must remain a conflict")
	}
}

func TestExcelPricingWritebackPersistsSafeRebaseForBoundedRetry(t *testing.T) {
	queue := newExcelPricingWritebackQueue(nil)
	jobID := "0123456789abcdef0123456789abcdef"
	originalRevision := excelPricingRevisionForTest("original-writeback-state")
	rebasedRevision := excelPricingRevisionForTest("rebased-writeback-state")
	settings := validExcelPricingWritebackRequest("excel-writeback-rebase-persist", "yuan_price", 31000).Settings
	queue.jobs[jobID] = &excelPricingWritebackJob{
		JobID: jobID, SettingKey: "yuan_price", Status: "sending",
		expectedStateRevision: originalRevision, settings: settings,
	}
	queue.latestByKey["yuan_price"] = jobID
	clone := cloneExcelPricingWritebackJob(queue.jobs[jobID])
	clone.expectedStateRevision = rebasedRevision
	clone.settings.ShippingCatalogRevision = excelPricingRevisionForTest("fresh-shipping-state")
	queue.persistSafeRebase(clone)
	stored := queue.jobs[jobID]
	if stored.expectedStateRevision != rebasedRevision {
		t.Fatalf("persisted revision=%q, want %q", stored.expectedStateRevision, rebasedRevision)
	}
	if stored.settings.ShippingCatalogRevision != clone.settings.ShippingCatalogRevision {
		t.Fatal("persisted retry settings did not retain the safe website-state rebase")
	}
}

func TestExcelPricingWritebackConflictKeepsExactRemoteReason(t *testing.T) {
	result := excelPricingWritebackErrorResult(&excelPricingRemoteError{
		status: http.StatusConflict,
		code:   "digitalogic_pricing_source_revision_conflict",
	})
	if !strings.Contains(result.messageFA, "digitalogic_pricing_source_revision_conflict") {
		t.Fatalf("conflict reason was hidden: %q", result.messageFA)
	}
}

func TestExcelPricingWritebackSourceUsesLiveEventIdentityWithoutCatalogProjection(t *testing.T) {
	source := canonical.Source{
		ID: "patris-office", Dataset: "kala.db",
		Revision: excelPricingRevisionForTest("event-source"),
	}
	bridge := newExcelPricingRemoteEventsBridgeWithDependencies(excelPricingRemoteBridgeDependencies{})
	bridge.verifiedRevision.Store(&excelPricingRemoteRevision{
		Source: source, StateRevision: excelPricingRevisionForTest("event-state"),
		CatalogRevision: excelPricingRevisionForTest("event-catalog"),
	})
	server := &Server{excelPricingRemote: bridge}
	started := time.Now()
	got, err := server.excelPricingWritebackSource(context.Background(), appconfig.Config{})
	if err != nil || got != source {
		t.Fatalf("writeback source=%#v error=%v", got, err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("event-backed source lookup took %s; writeback must not build a catalog projection", elapsed)
	}
}

func TestExcelPricingWritebackSourceRetainsMinimalAuthenticatedIdentityAcrossTransientDisconnect(t *testing.T) {
	source := canonical.Source{
		ID: "patris-office", Dataset: "kala.db",
		Revision: excelPricingRevisionForTest("event-source-before-disconnect"),
	}
	bridge := newExcelPricingRemoteEventsBridgeWithDependencies(excelPricingRemoteBridgeDependencies{
		apply: func(excelPricingRemoteRevision) error { return nil },
	})
	bridge.mu.Lock()
	bridge.epoch = 1
	bridge.mu.Unlock()
	if err := bridge.acceptRevision(1, source, excelPricingRemoteRevision{
		Source: source, StateRevision: excelPricingRevisionForTest("event-state-before-disconnect"),
		CatalogRevision: excelPricingRevisionForTest("event-catalog-before-disconnect"),
	}); err != nil {
		t.Fatal(err)
	}
	bridge.clearVerifiedRevision(1)

	server := &Server{excelPricingRemote: bridge}
	started := time.Now()
	got, err := server.excelPricingWritebackSource(context.Background(), appconfig.Config{})
	if err != nil || got != source {
		t.Fatalf("retained writeback source=%#v error=%v", got, err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("retained source lookup took %s; transient disconnect must not rebuild a catalog projection", elapsed)
	}
}

func TestExcelPricingWritebackSourceUsesWatchBaselineWithoutCatalogProjection(t *testing.T) {
	revision := excelPricingRevisionForTest("watch-baseline")
	server := &Server{
		dbPath: "C:/Patris/data4/kala.db", lastRecordsReady: true,
		lastContractRevision: revision,
	}
	cfg := appconfig.Config{}
	cfg.Canonical.SourceID = "patris-office"
	started := time.Now()
	got, err := server.excelPricingWritebackSource(context.Background(), cfg)
	if err != nil || got.ID != "patris-office" || got.Dataset != "kala.db" || got.Revision != revision {
		t.Fatalf("writeback source=%#v error=%v", got, err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("watch-baseline source lookup took %s; writeback must not build a catalog projection", elapsed)
	}
}

func TestExcelPricingDirectWebsiteConfirmationEnqueuesOnlyExactCommittedContract(t *testing.T) {
	queue := newExcelPricingWritebackQueue(nil)
	request := excelPricingConfirmationRequest{
		Schema:                  excelPricingConfirmationRequestSchema,
		RequestID:               "excel-direct-confirmation-0001",
		TransactionID:           "ptx_0123456789abcdef0123456789abcdef",
		CommittedStateRevision:  excelPricingRevisionForTest("direct-confirmation-state"),
		ConfirmedSettingsDigest: excelPricingRevisionForTest("direct-confirmation-settings"),
		ConfirmedSettings:       validExcelPricingWritebackRequest("excel-direct-confirmation-settings", "yuan_price", 29501).Settings,
	}
	source := canonical.Source{ID: "patris", Dataset: "product-catalog", Revision: excelPricingRevisionForTest("direct-confirmation-source")}
	job, err := queue.enqueueConfirmation(request, source)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "pending_ack" || job.SettingKey != "site_confirmation" || job.DesiredValue != "29501" || !job.ackOnly {
		t.Fatalf("direct confirmation job=%#v", job)
	}
	discovered, err := queue.enqueueDiscoveredConfirmation(request, source, time.Now().Add(time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if discovered.Status != "awaiting_excel" || discovered.Code != "website_committed" ||
		discovered.SettingKey != "site_confirmation" || discovered.DesiredValue != "29501" ||
		discovered.ConfirmedValue != "29501" ||
		!discovered.ackOnly {
		t.Fatalf("discovered confirmation job=%#v", discovered)
	}
	acknowledged, err := queue.acknowledge(discovered.JobID)
	if err != nil || acknowledged.Status != "pending_ack" {
		t.Fatalf("discovered confirmation acknowledgement=%#v error=%v", acknowledged, err)
	}
	request.TransactionID = "ptx_not_hex"
	if _, err := queue.enqueueConfirmation(request, source); err == nil || err.Error() != "invalid_confirmation" {
		t.Fatalf("invalid transaction error=%v", err)
	}
}

func validExcelPricingWritebackRequest(requestID, key string, yuan int64) excelPricingWritebackRequest {
	return excelPricingWritebackRequest{
		Schema: excelPricingWritebackRequestSchema, RequestID: requestID,
		SettingKey: key, ExpectedStateRevision: excelPricingRevisionForTest("writeback-state"),
		PreviousConfirmedValue: strconv.FormatInt(yuan, 10),
		Settings: excelPricingSettings{
			DollarPrice: 187891, YuanPrice: yuan,
			EffectiveDate: "2026-07-27", USDEffectiveDate: "2026-07-26", CNYEffectiveDate: "2026-07-27",
			ProfitMarginPercent: json.Number("30"), AirExpressPricePerKG: json.Number("120"),
			AirExpressCurrency: "CNY", ShippingCatalogRevision: excelPricingRevisionForTest("shipping-catalog"),
			PriceRoundingDigits: json.Number("2"), PriceRoundingMode: "nearest_half_up",
		},
	}
}
