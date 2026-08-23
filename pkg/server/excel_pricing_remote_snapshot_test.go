package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

const excelPricingRemoteSnapshotTestSecretEnv = "PATRIS_REMOTE_SNAPSHOT_TEST_SECRET"

type excelPricingRemoteSnapshotFixture struct {
	t              *testing.T
	server         *httptest.Server
	hub            *excelPricingRemoteSnapshotTerminalHub
	source         canonical.Source
	revision       excelPricingRemoteRevisionResponse
	payload        excelPricingRemoteSnapshotPayload
	payloadBody    []byte
	requestID      string
	buildID        string
	mode           string
	badSnapshotURL string
	badETag        bool
	acceptAnyID    bool
	startHeld      chan struct{}
	releaseStart   chan struct{}
	startHeldOnce  sync.Once
	releaseOnce    sync.Once

	mu            sync.Mutex
	revisionCalls int
	startCalls    int
	bulkCalls     int
	statusCalls   int
	cancelCalls   int
	legacyCalls   int
	headerFailure string
}

func TestExcelPricingRemoteSnapshotReadyUsesOneBulkFetchWithoutStatusPolling(t *testing.T) {
	fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
	defer fixture.Close()
	client := fixture.Client(t)

	result, err := client.Collect(context.Background(), fixture.requestID, 60)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.CompositeStateRevision != fixture.revision.StateRevision ||
		result.PricingStateRevision != fixture.revision.PricingStateRevision ||
		result.MutationStateRevision != fixture.revision.PricingStateRevision {
		t.Fatalf("result revisions = %+v", result)
	}
	if result.CompositeStateRevision == result.MutationStateRevision {
		t.Fatal("composite snapshot identity was conflated with the pricing mutation guard")
	}
	if len(result.Rows) != 1 || len(result.ProjectedRows) != 1 ||
		len(result.ProjectedRowFields) != len(excelPricingSnapshotExcelRowFields) {
		t.Fatalf("projection sizes = rows:%d projected:%d fields:%d",
			len(result.Rows), len(result.ProjectedRows), len(result.ProjectedRowFields))
	}
	var projected []json.RawMessage
	if err := json.Unmarshal(result.ProjectedRows[0], &projected); err != nil {
		t.Fatalf("projected row: %v", err)
	}
	if len(projected) != len(excelPricingSnapshotExcelRowFields) ||
		excelPricingSnapshotString(projected[0]) != "woo:101" ||
		excelPricingSnapshotString(projected[19]) != "محصول آزمایشی" {
		t.Fatalf("projected row = %s", result.ProjectedRows[0])
	}
	fixture.assertCalls(t, 1, 1, 1, 0, 0)
}

func TestExcelPricingRemoteSnapshotAcceptsReviewed1093RowReconciliationFixture(t *testing.T) {
	fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
	defer fixture.Close()

	const (
		matched                 = 838
		patrisOnly              = 124
		wooOnly                 = 131
		variableParentsExcluded = 15
	)
	rows := make([]json.RawMessage, 0, matched+patrisOnly+wooOnly)
	for index := 0; index < matched+patrisOnly+wooOnly; index++ {
		row := make(map[string]interface{}, len(excelPricingSnapshotExcelRowFields))
		for _, field := range excelPricingSnapshotExcelRowFields {
			row[field] = nil
		}
		switch {
		case index < matched:
			row["sync_key"] = "woo:" + strconv.Itoa(index+1)
			row["reconciliation_status"] = "matched"
		case index < matched+patrisOnly:
			row["sync_key"] = "patris:" + strconv.Itoa(index+1)
			row["reconciliation_status"] = "patris_only"
		default:
			row["sync_key"] = "woo:" + strconv.Itoa(index+1)
			row["reconciliation_status"] = "woo_only"
		}
		rows = append(rows, mustMarshalExcelPricingRemoteSnapshotTestJSON(t, row))
	}
	fixture.payload.Catalog.Rows = rows
	reconciliation := fixture.reconciliationBody(matched, patrisOnly, wooOnly, nil)
	fixture.payload.Reconciliation = reconciliation
	fixture.payload.Catalog.Reconciliation = reconciliation
	fixture.setReconciliationCount("woocommerce_raw", matched+wooOnly+variableParentsExcluded)
	fixture.setReconciliationCount("variable_parents_excluded", variableParentsExcluded)
	fixture.finalizePayload()

	result, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(result.Rows) != 1093 || len(result.ProjectedRows) != 1093 ||
		len(result.ProjectedRowFields) != 26 {
		t.Fatalf(
			"Living fixture rows=%d projected=%d fields=%d",
			len(result.Rows),
			len(result.ProjectedRows),
			len(result.ProjectedRowFields),
		)
	}
	fixture.assertCalls(t, 1, 1, 1, 0, 0)
	fixture.assertNoLegacyStateCalls(t)
}

func TestExcelPricingRemoteSnapshotColdTerminalEventCannotRacePOST(t *testing.T) {
	fixture := newExcelPricingRemoteSnapshotFixture(t, "event_before_response")
	defer fixture.Close()
	client := fixture.Client(t)

	result, err := client.Collect(context.Background(), fixture.requestID, 0)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.SnapshotRevision != fixture.payload.SnapshotRevision {
		t.Fatalf("snapshot revision = %q", result.SnapshotRevision)
	}
	fixture.assertCalls(t, 1, 1, 1, 0, 0)
}

func TestExcelPricingRemoteSnapshotColdCompletesFromTerminalEventOnly(t *testing.T) {
	fixture := newExcelPricingRemoteSnapshotFixture(t, "event_after_response")
	defer fixture.Close()
	client := fixture.Client(t)

	result, err := client.Collect(context.Background(), fixture.requestID, 0)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.SnapshotRevision != fixture.payload.SnapshotRevision {
		t.Fatalf("snapshot revision = %q", result.SnapshotRevision)
	}
	fixture.assertCalls(t, 1, 1, 1, 0, 0)
}

func TestExcelPricingSnapshotProductionCollectorUsesBulkAndPreservesCompositeIdentity(t *testing.T) {
	fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
	defer fixture.Close()
	fixture.acceptAnyID = true
	server, token := newExcelPricingRemoteSnapshotProductionServer(t, fixture)

	start := func(requestID, expected string) (*httptest.ResponseRecorder, string) {
		t.Helper()
		request := authenticatedExcelPricingRequest(
			http.MethodPost,
			"/api/pricing-sync/snapshots",
			validExcelPricingSnapshotStartBodyWithProjection(
				fixture.source,
				requestID,
				"fa",
				0,
				expected,
				excelPricingSnapshotProjectionExcel,
			),
			token,
		)
		request.Header.Set("Idempotency-Key", requestID)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		return response, excelPricingSnapshotJobIDForTest(t, response.Body.Bytes())
	}

	firstResponse, firstJobID := start("snapshot-production-bulk-0001", "")
	if firstResponse.Code != http.StatusAccepted {
		t.Fatalf("first start=%d: %s", firstResponse.Code, firstResponse.Body.String())
	}
	firstStatus := waitForExcelPricingSnapshotStatus(t, server, token, firstJobID, "ready")
	if firstStatus["state_revision"] != fixture.revision.StateRevision {
		t.Fatalf("first composite status=%#v", firstStatus)
	}

	payloadRequest := authenticatedExcelPricingRequest(
		http.MethodGet,
		"/api/pricing-sync/snapshots/"+firstJobID+"/payload",
		"",
		token,
	)
	payloadResponse := httptest.NewRecorder()
	server.router.ServeHTTP(payloadResponse, payloadRequest)
	if payloadResponse.Code != http.StatusOK ||
		payloadResponse.Header().Get("ETag") !=
			excelPricingSnapshotETag(excelPricingSnapshotDigest(payloadResponse.Body.Bytes())) {
		t.Fatalf("payload status=%d etag=%q", payloadResponse.Code, payloadResponse.Header().Get("ETag"))
	}
	var payload excelPricingSnapshotPayload
	if err := json.Unmarshal(payloadResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	var adaptedState struct {
		StateRevision        string `json:"state_revision"`
		PricingStateRevision string `json:"pricing_state_revision"`
	}
	if err := json.Unmarshal(payload.State, &adaptedState); err != nil {
		t.Fatal(err)
	}
	if payload.StateRevision != fixture.revision.StateRevision ||
		payload.Projection != excelPricingSnapshotProjectionExcel ||
		len(payload.RowFields) != len(excelPricingSnapshotExcelRowFields) ||
		payload.MutationGuard.ExpectedStateRevision != fixture.revision.StateRevision ||
		adaptedState.StateRevision != fixture.revision.StateRevision ||
		adaptedState.PricingStateRevision != fixture.revision.PricingStateRevision ||
		adaptedState.StateRevision == adaptedState.PricingStateRevision ||
		payload.Integrity.StateDigest != excelPricingSnapshotDigest(payload.State) {
		t.Fatalf("adapted payload=%+v adapted state=%+v", payload, adaptedState)
	}
	server.excelPricing.snapshots.mu.Lock()
	firstSnapshot := server.excelPricing.snapshots.jobs[firstJobID].snapshot
	server.excelPricing.snapshots.mu.Unlock()
	if firstSnapshot == nil ||
		firstSnapshot.stateRevision != fixture.revision.StateRevision ||
		firstSnapshot.pricingStateRevision != fixture.revision.PricingStateRevision ||
		firstSnapshot.upstreamCatalogRevision != fixture.revision.CatalogRevision {
		t.Fatalf("internal snapshot identity=%+v", firstSnapshot)
	}

	bridge := server.excelPricingRemote
	bridge.verifiedRevision.Store(&excelPricingRemoteRevision{
		Source:          fixture.source,
		StateRevision:   fixture.revision.StateRevision,
		CatalogRevision: fixture.revision.CatalogRevision,
	})

	// max_age=0 without an expected revision remains a forced rebuild even
	// while the authenticated event bridge confirms the cached composite.
	secondResponse, secondJobID := start("snapshot-production-bulk-0002", "")
	if secondResponse.Code != http.StatusAccepted {
		t.Fatalf("second start=%d: %s", secondResponse.Code, secondResponse.Body.String())
	}
	waitForExcelPricingSnapshotStatus(t, server, token, secondJobID, "ready")

	// An exact caller revision may reuse only while the bridge still confirms
	// that same source/composite/catalog identity.
	thirdResponse, thirdJobID := start(
		"snapshot-production-bulk-0003",
		fixture.revision.StateRevision,
	)
	if thirdResponse.Code != http.StatusOK || thirdJobID == secondJobID {
		t.Fatalf("exact cache start=%d second=%q third=%q: %s",
			thirdResponse.Code, secondJobID, thirdJobID, thirdResponse.Body.String())
	}
	var thirdStatus map[string]interface{}
	if err := json.Unmarshal(thirdResponse.Body.Bytes(), &thirdStatus); err != nil {
		t.Fatal(err)
	}
	if thirdStatus["cached"] != true {
		t.Fatalf("exact cache status=%#v", thirdStatus)
	}
	bridge.verifiedRevision.Store(&excelPricingRemoteRevision{
		Source:          fixture.source,
		StateRevision:   testExcelPricingRevision('9'),
		CatalogRevision: fixture.revision.CatalogRevision,
	})
	fourthResponse, fourthJobID := start(
		"snapshot-production-bulk-0004",
		fixture.revision.StateRevision,
	)
	if fourthResponse.Code != http.StatusAccepted {
		t.Fatalf("stale exact cache start=%d: %s", fourthResponse.Code, fourthResponse.Body.String())
	}
	waitForExcelPricingSnapshotStatus(t, server, token, fourthJobID, "ready")
	fixture.assertCalls(t, 3, 3, 3, 0, 0)
	fixture.assertNoLegacyStateCalls(t)
}

func TestExcelPricingSnapshotProductionCollectorColdTerminalAndCancel(t *testing.T) {
	t.Run("terminal", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "event_after_response")
		defer fixture.Close()
		fixture.acceptAnyID = true
		server, token := newExcelPricingRemoteSnapshotProductionServer(t, fixture)
		requestID := "snapshot-production-cold-terminal-0001"
		request := authenticatedExcelPricingRequest(
			http.MethodPost,
			"/api/pricing-sync/snapshots",
			validExcelPricingSnapshotStartBody(fixture.source, requestID, "fa", 0),
			token,
		)
		request.Header.Set("Idempotency-Key", requestID)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		jobID := excelPricingSnapshotJobIDForTest(t, response.Body.Bytes())
		status := waitForExcelPricingSnapshotStatus(t, server, token, jobID, "ready")
		if status["state_revision"] != fixture.revision.StateRevision {
			t.Fatalf("cold terminal status=%#v", status)
		}
		fixture.assertCalls(t, 1, 1, 1, 0, 0)
		fixture.assertNoLegacyStateCalls(t)
	})

	t.Run("cancel", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "cold_start_inflight")
		defer fixture.Close()
		fixture.acceptAnyID = true
		server, token := newExcelPricingRemoteSnapshotProductionServer(t, fixture)
		requestID := "snapshot-production-cold-cancel-0001"
		request := authenticatedExcelPricingRequest(
			http.MethodPost,
			"/api/pricing-sync/snapshots",
			validExcelPricingSnapshotStartBody(fixture.source, requestID, "fa", 0),
			token,
		)
		request.Header.Set("Idempotency-Key", requestID)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		jobID := excelPricingSnapshotJobIDForTest(t, response.Body.Bytes())
		select {
		case <-fixture.startHeld:
		case <-time.After(time.Second):
			t.Fatal("remote cold build did not reach the in-flight response gate")
		}
		cancelRequest := authenticatedExcelPricingRequest(
			http.MethodDelete,
			"/api/pricing-sync/snapshots/"+jobID,
			"",
			token,
		)
		cancelResponse := httptest.NewRecorder()
		server.router.ServeHTTP(cancelResponse, cancelRequest)
		if cancelResponse.Code != http.StatusAccepted {
			t.Fatalf("cancel status=%d: %s", cancelResponse.Code, cancelResponse.Body.String())
		}
		fixture.releaseHeldStart()
		waitForExcelPricingSnapshotStatus(t, server, token, jobID, "cancelled")
		fixture.assertCalls(t, 1, 1, 0, 0, 1)
		fixture.assertNoLegacyStateCalls(t)
	})
}

func newExcelPricingRemoteSnapshotProductionServer(
	t *testing.T,
	fixture *excelPricingRemoteSnapshotFixture,
) (*Server, string) {
	t.Helper()
	server := newExcelPricingTestServer(
		t,
		fixture.server.URL+"/wp-json/digitalogic/product-sync",
	)
	if err := server.config.Update(func(cfg *appconfig.Config) {
		cfg.SendUpdates.ProductSyncSecretEnv = excelPricingRemoteSnapshotTestSecretEnv
	}); err != nil {
		t.Fatal(err)
	}
	server.excelPricing.canonical = canonicalProjectionSequence(fixture.source)
	server.excelPricing.snapshotCollector = nil
	server.excelPricingRemote = &excelPricingRemoteEventsBridge{terminals: fixture.hub}
	server.excelPricing.snapshotRevisionCurrent = server.excelPricingRemote.revisionCurrent
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	return server, openExcelPricingSession(t, server)
}

func TestExcelPricingRemoteSnapshotTerminalReplayIsConsumedWithoutStatusPolling(t *testing.T) {
	fixture := newExcelPricingRemoteSnapshotFixture(t, "cold")
	defer fixture.Close()
	if err := fixture.hub.publishAuthenticated(fixture.terminalEvent(41)); err != nil {
		t.Fatalf("publish replay: %v", err)
	}
	client := fixture.Client(t)
	if _, err := client.Collect(context.Background(), fixture.requestID, 0); err != nil {
		t.Fatalf("Collect() replay error = %v", err)
	}
	fixture.assertCalls(t, 1, 1, 1, 0, 0)
}

func TestExcelPricingRemoteSnapshotCancellationPropagatesWithoutStatusPolling(t *testing.T) {
	fixture := newExcelPricingRemoteSnapshotFixture(t, "cold")
	defer fixture.Close()
	client := fixture.Client(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := client.Collect(ctx, fixture.requestID, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Collect() error = %v, want deadline", err)
	}
	fixture.assertCalls(t, 1, 1, 0, 0, 1)
}

func TestExcelPricingRemoteSnapshotRejectsCrossOriginReturnedPath(t *testing.T) {
	fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
	defer fixture.Close()
	fixture.badSnapshotURL = "https://attacker.invalid/wp-json/digitalogic/pricing/sync/snapshots/" + fixture.payload.SnapshotToken
	client := fixture.Client(t)
	_, err := client.Collect(context.Background(), fixture.requestID, 60)
	if !errors.Is(err, errExcelPricingRemoteSnapshotProtocol) {
		t.Fatalf("Collect() error = %v", err)
	}
	fixture.assertCalls(t, 1, 1, 0, 0, 0)
}

func TestExcelPricingRemoteSnapshotFailsClosedOnIntegrityViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*excelPricingRemoteSnapshotFixture)
	}{
		{
			name: "weak payload etag",
			mutate: func(fixture *excelPricingRemoteSnapshotFixture) {
				fixture.badETag = true
			},
		},
		{
			name: "wrong pinned column order",
			mutate: func(fixture *excelPricingRemoteSnapshotFixture) {
				var columns []map[string]json.RawMessage
				if err := json.Unmarshal(fixture.payload.Catalog.Columns, &columns); err != nil {
					fixture.t.Fatal(err)
				}
				columns[0], columns[1] = columns[1], columns[0]
				fixture.payload.Catalog.Columns = mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, columns)
				fixture.finalizePayload()
			},
		},
		{
			name: "unsafe integrity warning",
			mutate: func(fixture *excelPricingRemoteSnapshotFixture) {
				fixture.setWarnings([]interface{}{map[string]interface{}{"code": "product_type_cache_drift"}})
				fixture.finalizePayload()
			},
		},
		{
			name: "ambiguous reconciliation",
			mutate: func(fixture *excelPricingRemoteSnapshotFixture) {
				fixture.setReconciliationCount("ambiguous_codes", 1)
				fixture.finalizePayload()
			},
		},
		{
			name: "duplicate sync key",
			mutate: func(fixture *excelPricingRemoteSnapshotFixture) {
				fixture.addDuplicateRow()
				fixture.finalizePayload()
			},
		},
		{
			name: "payload digest mismatch",
			mutate: func(fixture *excelPricingRemoteSnapshotFixture) {
				fixture.payload.Digest = testExcelPricingRevision('9')
				fixture.payload.Revision = fixture.payload.Digest
				fixture.payload.SnapshotRevision = fixture.payload.Digest
				fixture.payload.Integrity.PayloadDigest = fixture.payload.Digest
				fixture.payloadBody = mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, fixture.payload)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
			defer fixture.Close()
			test.mutate(fixture)
			client := fixture.Client(t)
			_, err := client.Collect(context.Background(), fixture.requestID, 60)
			if !errors.Is(err, errExcelPricingRemoteSnapshotIntegrity) &&
				!errors.Is(err, errExcelPricingRemoteSnapshotProtocol) {
				t.Fatalf("Collect() error = %v", err)
			}
			fixture.assertNoStatusCalls(t)
		})
	}
}

func TestExcelPricingRemoteSnapshotTerminalHubRejectsCursorRegressionAndConflicts(t *testing.T) {
	fixture := newExcelPricingRemoteSnapshotFixture(t, "cold")
	defer fixture.Close()
	event := fixture.terminalEvent(9)
	if err := fixture.hub.publishAuthenticated(event); err != nil {
		t.Fatalf("first event: %v", err)
	}
	if err := fixture.hub.publishAuthenticated(event); err != nil {
		t.Fatalf("identical replay: %v", err)
	}
	regressed := event
	regressed.EventID = 8
	if err := fixture.hub.publishAuthenticated(regressed); !errors.Is(err, errExcelPricingRemoteSnapshotProtocol) {
		t.Fatalf("regression error = %v", err)
	}
	conflict := event
	conflict.EventID = 10
	conflict.Digest = testExcelPricingRevision('8')
	conflict.SnapshotRevision = conflict.Digest
	if err := fixture.hub.publishAuthenticated(conflict); !errors.Is(err, errExcelPricingRemoteSnapshotProtocol) {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestExcelPricingRemoteSnapshotAuthenticatedStreamAcknowledgesOnlyAfterWaiterAccepts(t *testing.T) {
	fixture := newExcelPricingRemoteSnapshotFixture(t, "cold")
	defer fixture.Close()
	subscription, err := fixture.hub.Subscribe(
		fixture.requestID,
		fixture.source,
		fixture.revision.StateRevision,
	)
	if err != nil {
		t.Fatalf("Subscribe(): %v", err)
	}
	defer subscription.Close()
	client := &excelPricingRemoteEventsClient{
		source:     fixture.source,
		onTerminal: fixture.hub.publishAuthenticated,
		seen:       make(map[string]struct{}),
	}
	event := fixture.terminalEvent(0)
	data := mustMarshalExcelPricingRemoteSnapshotTestJSON(t, event)
	frame := excelPricingRemoteWireFrame{
		Event:   "pricing.snapshot.build.terminal",
		Name:    "pricing.snapshot.build.terminal",
		Success: true,
		Data:    data,
		ID:      17,
	}
	body := mustMarshalExcelPricingRemoteSnapshotTestJSON(t, frame)
	if _, err := client.handleExcelPricingRemoteFrame(context.Background(), body, true); err != nil {
		t.Fatalf("handle frame: %v", err)
	}
	if client.currentCursor() != 17 {
		t.Fatalf("cursor = %d", client.currentCursor())
	}
	received, err := subscription.Wait(context.Background())
	if err != nil || received.EventID != 17 || received.RequestID != fixture.requestID {
		t.Fatalf("received = %+v, err = %v", received, err)
	}

	rejecting := &excelPricingRemoteEventsClient{
		source: fixture.source,
		onTerminal: func(excelPricingRemoteSnapshotTerminalEvent) error {
			return errors.New("retain failed")
		},
		seen: make(map[string]struct{}),
	}
	if _, err := rejecting.handleExcelPricingRemoteFrame(context.Background(), body, true); !errors.Is(err, errExcelPricingRemoteProtocol) {
		t.Fatalf("rejected frame error = %v", err)
	}
	if rejecting.currentCursor() != 0 {
		t.Fatalf("rejected cursor = %d", rejecting.currentCursor())
	}
}

func newExcelPricingRemoteSnapshotFixture(t *testing.T, mode string) *excelPricingRemoteSnapshotFixture {
	t.Helper()
	fixture := &excelPricingRemoteSnapshotFixture{
		t:         t,
		hub:       newExcelPricingRemoteSnapshotTerminalHub(),
		source:    canonical.Source{ID: "patris-office", Dataset: "kala.db", Revision: testExcelPricingRevision('1')},
		requestID: "snapshot-request-00000001",
		buildID:   "build_0000000000000001",
		mode:      mode,
	}
	if mode == "cold_start_inflight" {
		fixture.startHeld = make(chan struct{})
		fixture.releaseStart = make(chan struct{})
	}
	fixture.revision = excelPricingRemoteRevisionResponse{
		Schema:                excelPricingRemoteRevisionSchema,
		Projection:            excelPricingRemoteProjection,
		ProjectionSchema:      excelPricingRemoteProjectionSchema,
		StateRevision:         testExcelPricingRevision('2'),
		Source:                fixture.source,
		CatalogRevision:       testExcelPricingRevision('3'),
		PricingStateRevision:  testExcelPricingRevision('4'),
		PricingPolicyRevision: testExcelPricingRevision('5'),
		Locale:                "fa",
		PageSize:              excelPricingSnapshotPageSize,
	}
	fixture.payload = fixture.basePayload()
	fixture.finalizePayload()
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.handle))
	t.Setenv(excelPricingRemoteSnapshotTestSecretEnv, "test-remote-snapshot-secret-value")
	return fixture
}

func (fixture *excelPricingRemoteSnapshotFixture) Close() {
	fixture.releaseHeldStart()
	fixture.server.Close()
}

func (fixture *excelPricingRemoteSnapshotFixture) releaseHeldStart() {
	if fixture == nil || fixture.releaseStart == nil {
		return
	}
	fixture.releaseOnce.Do(func() {
		close(fixture.releaseStart)
	})
}

func (fixture *excelPricingRemoteSnapshotFixture) Client(t *testing.T) *excelPricingRemoteSnapshotClient {
	t.Helper()
	client, err := newExcelPricingRemoteSnapshotClient(updateout.Config{
		Enabled:              true,
		URL:                  fixture.server.URL + "/wp-json/digitalogic/product-sync",
		Method:               http.MethodPost,
		Format:               "json",
		Timeout:              "2s",
		ProductSyncSecretEnv: excelPricingRemoteSnapshotTestSecretEnv,
	}, fixture.source, excelPricingRemoteSnapshotClientOptions{
		HTTPClient: fixture.server.Client(),
		Terminals:  fixture.hub,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

func (fixture *excelPricingRemoteSnapshotFixture) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(excelPricingRemoteSecretHeader) != "test-remote-snapshot-secret-value" ||
		r.Header.Get(excelPricingRemoteSourceIDHeader) != fixture.source.ID ||
		r.Header.Get(excelPricingRemoteDatasetHeader) != fixture.source.Dataset {
		fixture.mu.Lock()
		fixture.headerFailure = "missing protected machine headers"
		fixture.mu.Unlock()
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/wp-json/digitalogic/pricing/sync/revision":
		fixture.mu.Lock()
		fixture.revisionCalls++
		fixture.mu.Unlock()
		if !fixture.validSourceQuery(r.URL.Query()) {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		w.Header().Set("ETag", `"`+fixture.revision.StateRevision+`"`)
		_ = json.NewEncoder(w).Encode(fixture.revision)
	case r.Method == http.MethodPost && r.URL.Path == "/wp-json/digitalogic/pricing/sync/snapshots":
		fixture.mu.Lock()
		fixture.startCalls++
		fixture.mu.Unlock()
		var request excelPricingRemoteSnapshotStartRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.RequestID == "" ||
			request.Source != fixture.source || request.ExpectedStateRevision != fixture.revision.StateRevision ||
			r.Header.Get("Idempotency-Key") != request.RequestID ||
			r.Header.Get("If-Match") != `"`+fixture.revision.StateRevision+`"` {
			http.Error(w, "bad start", http.StatusBadRequest)
			return
		}
		if fixture.acceptAnyID {
			fixture.mu.Lock()
			fixture.requestID = request.RequestID
			fixture.mu.Unlock()
		} else if request.RequestID != fixture.currentRequestID() {
			http.Error(w, "bad start", http.StatusBadRequest)
			return
		}
		build := fixture.buildResponse()
		if fixture.mode == "ready" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(build)
			return
		}
		build.Status = "queued"
		build.SnapshotToken = ""
		build.Revision = ""
		build.SnapshotRevision = ""
		build.Digest = ""
		build.SnapshotURL = ""
		if fixture.startHeld != nil {
			fixture.startHeldOnce.Do(func() {
				close(fixture.startHeld)
			})
			<-fixture.releaseStart
		}
		if fixture.mode == "event_before_response" {
			if err := fixture.hub.publishAuthenticated(fixture.terminalEvent(1)); err != nil {
				http.Error(w, "event", http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(build)
		if fixture.mode == "event_after_response" {
			time.AfterFunc(10*time.Millisecond, func() {
				_ = fixture.hub.publishAuthenticated(fixture.terminalEvent(1))
			})
		}
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/wp-json/digitalogic/pricing/sync/builds/"):
		fixture.mu.Lock()
		fixture.statusCalls++
		fixture.mu.Unlock()
		http.Error(w, "status polling forbidden", http.StatusTeapot)
	case r.Method == http.MethodDelete && r.URL.Path == "/wp-json/digitalogic/pricing/sync/builds/"+fixture.buildID:
		fixture.mu.Lock()
		fixture.cancelCalls++
		fixture.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"cancelled"}`))
	case r.Method == http.MethodGet && r.URL.Path == "/wp-json/digitalogic/pricing/sync/snapshots/"+fixture.payload.SnapshotToken:
		fixture.mu.Lock()
		fixture.bulkCalls++
		fixture.mu.Unlock()
		if fixture.badETag {
			w.Header().Set("ETag", `W/"`+fixture.payload.Digest+`"`)
		} else {
			w.Header().Set("ETag", `"`+fixture.payload.Digest+`"`)
		}
		_, _ = w.Write(fixture.payloadBody)
	case r.Method == http.MethodPost && r.URL.Path == "/wp-json/digitalogic/pricing/sync/state":
		fixture.mu.Lock()
		fixture.legacyCalls++
		fixture.mu.Unlock()
		http.Error(w, "legacy state paging forbidden", http.StatusTeapot)
	default:
		http.NotFound(w, r)
	}
}

func (fixture *excelPricingRemoteSnapshotFixture) validSourceQuery(query url.Values) bool {
	return query.Get("source_id") == fixture.source.ID &&
		query.Get("source_dataset") == fixture.source.Dataset &&
		query.Get("source_revision") == fixture.source.Revision &&
		query.Get("locale") == "fa" &&
		query.Get("page_size") == strconv.Itoa(excelPricingSnapshotPageSize)
}

func (fixture *excelPricingRemoteSnapshotFixture) buildResponse() excelPricingRemoteSnapshotBuildResponse {
	snapshotURL := "/wp-json/digitalogic/pricing/sync/snapshots/" + fixture.payload.SnapshotToken + fixture.sourceQuery()
	if fixture.badSnapshotURL != "" {
		snapshotURL = fixture.badSnapshotURL
	}
	return excelPricingRemoteSnapshotBuildResponse{
		Schema:               excelPricingRemoteSnapshotBuildSchema,
		BuildID:              fixture.buildID,
		RequestID:            fixture.currentRequestID(),
		Status:               "ready",
		Source:               fixture.source,
		Locale:               "fa",
		StateRevision:        fixture.revision.StateRevision,
		PricingStateRevision: fixture.revision.PricingStateRevision,
		CatalogRevision:      fixture.revision.CatalogRevision,
		SnapshotToken:        fixture.payload.SnapshotToken,
		Revision:             fixture.payload.SnapshotRevision,
		SnapshotRevision:     fixture.payload.SnapshotRevision,
		Digest:               fixture.payload.Digest,
		StatusURL:            "/wp-json/digitalogic/pricing/sync/builds/" + fixture.buildID + fixture.sourceQuery(),
		CancelURL:            "/wp-json/digitalogic/pricing/sync/builds/" + fixture.buildID + fixture.sourceQuery(),
		SnapshotURL:          snapshotURL,
	}
}

func (fixture *excelPricingRemoteSnapshotFixture) terminalEvent(eventID uint64) excelPricingRemoteSnapshotTerminalEvent {
	return excelPricingRemoteSnapshotTerminalEvent{
		Schema:               excelPricingRemoteSnapshotEventSchema,
		BuildID:              fixture.buildID,
		RequestID:            fixture.currentRequestID(),
		Status:               "ready",
		Source:               fixture.source,
		StateRevision:        fixture.revision.StateRevision,
		PricingStateRevision: fixture.revision.PricingStateRevision,
		CatalogRevision:      fixture.revision.CatalogRevision,
		SnapshotToken:        fixture.payload.SnapshotToken,
		SnapshotRevision:     fixture.payload.SnapshotRevision,
		Digest:               fixture.payload.Digest,
		SnapshotPath:         "/wp-json/digitalogic/pricing/sync/snapshots/" + fixture.payload.SnapshotToken + fixture.sourceQuery(),
		IdempotencyKey:       testExcelPricingRevision('6'),
		EventID:              eventID,
	}
}

func (fixture *excelPricingRemoteSnapshotFixture) currentRequestID() string {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.requestID
}

func (fixture *excelPricingRemoteSnapshotFixture) sourceQuery() string {
	query := make(url.Values)
	query.Set("source_id", fixture.source.ID)
	query.Set("source_dataset", fixture.source.Dataset)
	query.Set("source_revision", fixture.source.Revision)
	return "?" + query.Encode()
}

func (fixture *excelPricingRemoteSnapshotFixture) basePayload() excelPricingRemoteSnapshotPayload {
	columns := make([]map[string]json.RawMessage, len(excelPricingSnapshotExcelRowFields))
	row := make(map[string]interface{}, len(excelPricingSnapshotExcelRowFields))
	for index, field := range excelPricingSnapshotExcelRowFields {
		columns[index] = map[string]json.RawMessage{"key": json.RawMessage(strconv.Quote(field))}
		row[field] = nil
	}
	row["sync_key"] = "woo:101"
	row["reconciliation_status"] = "matched"
	row["patris_code"] = "101"
	row["woocommerce_id"] = 101
	row["name"] = "محصول آزمایشی"
	row["sale_price"] = "123500"
	row["effective_price"] = "123500"
	row["patris_final_price"] = "123500"
	row["stock_quantity"] = 5
	row["patris_total_stock"] = 5
	row["foreign_price"] = "120"
	row["foreign_currency"] = "CNY"
	row["record_revision"] = testExcelPricingRevision('7')
	rowBody := mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, row)
	columnsBody := mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, columns)
	reconciliation := fixture.reconciliationBody(1, 0, 0, nil)
	settings := json.RawMessage(`{"dollar_price":95000,"yuan_price":29500,"profit_margin_percent":"30"}`)
	guard := excelPricingRemoteSnapshotMutationGuard{
		ExpectedStateRevision: fixture.revision.PricingStateRevision,
		Preview: excelPricingSnapshotMutationOperation{
			Method:                 http.MethodPost,
			Path:                   "/wp-json/digitalogic/pricing/sync/preview",
			RequiresIdempotencyKey: true,
			RequiresIfMatch:        true,
		},
		Apply: excelPricingSnapshotMutationOperation{
			Method:                 http.MethodPost,
			Path:                   "/wp-json/digitalogic/pricing/sync/apply",
			RequiresIdempotencyKey: true,
			RequiresIfMatch:        true,
			Confirmation:           "APPLY",
		},
	}
	createdAt := time.Now().UTC().Add(-time.Minute)
	return excelPricingRemoteSnapshotPayload{
		Schema:                excelPricingRemoteSnapshotPayloadSchema,
		Projection:            excelPricingRemoteProjection,
		ProjectionSchema:      excelPricingRemoteProjectionSchema,
		SnapshotToken:         "snap_0000000000000001",
		StateRevision:         fixture.revision.StateRevision,
		PricingStateRevision:  fixture.revision.PricingStateRevision,
		PricingPolicyRevision: fixture.revision.PricingPolicyRevision,
		CatalogRevision:       fixture.revision.CatalogRevision,
		DatasetRevision:       testExcelPricingRevision('8'),
		Source:                fixture.source,
		CreatedAt:             createdAt.Format(time.RFC3339),
		ExpiresAt:             createdAt.Add(15 * time.Minute).Format(time.RFC3339),
		RowCount:              1,
		DistinctSyncKeys:      1,
		RemoteTotal:           1,
		PageSize:              excelPricingSnapshotPageSize,
		PageCount:             1,
		MutationGuard:         guard,
		Settings:              settings,
		Reconciliation:        reconciliation,
		Catalog: excelPricingRemoteSnapshotCatalog{
			Dataset:         "reconciled_products",
			Locale:          "fa",
			DatasetRevision: testExcelPricingRevision('8'),
			Columns:         columnsBody,
			Rows:            []json.RawMessage{rowBody},
			Reconciliation:  reconciliation,
			Pagination: excelPricingSnapshotPagination{
				Page: 1, Limit: excelPricingSnapshotPageSize, Total: 1, Pages: 1, HasMore: false,
			},
		},
	}
}

func (fixture *excelPricingRemoteSnapshotFixture) reconciliationBody(
	matched, patrisOnly, wooOnly int,
	warnings []interface{},
) json.RawMessage {
	if warnings == nil {
		warnings = []interface{}{}
	}
	return mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, struct {
		Status          string                                    `json:"status"`
		IntegrityStatus string                                    `json:"integrity_status"`
		Warnings        []interface{}                             `json:"warnings"`
		Counts          excelPricingRemoteSnapshotReconcileCounts `json:"counts"`
		Source          canonical.Source                          `json:"source"`
	}{
		Status:          "current",
		IntegrityStatus: "current",
		Warnings:        warnings,
		Counts: excelPricingRemoteSnapshotReconcileCounts{
			PatrisProducts:          matched + patrisOnly,
			WooCommerceRaw:          matched + wooOnly,
			WooCommerceLeaves:       matched + wooOnly,
			UnionRows:               matched + patrisOnly + wooOnly,
			Matched:                 matched,
			SourceOnly:              patrisOnly,
			PatrisOnly:              patrisOnly,
			WooOnly:                 wooOnly,
			VariableParentsExcluded: 0,
		},
		Source: fixture.source,
	})
}

func (fixture *excelPricingRemoteSnapshotFixture) finalizePayload() {
	fixture.t.Helper()
	payload := &fixture.payload
	payload.RowCount = len(payload.Catalog.Rows)
	payload.DistinctSyncKeys = payload.RowCount
	payload.RemoteTotal = payload.RowCount
	payload.PageCount = max(1, (payload.RowCount+payload.PageSize-1)/payload.PageSize)
	payload.Catalog.Pagination.Total = payload.RowCount
	payload.Catalog.Pagination.Pages = payload.PageCount
	payload.PageDigests = nil
	for page := 1; page <= payload.PageCount; page++ {
		start := (page - 1) * payload.PageSize
		end := min(start+payload.PageSize, len(payload.Catalog.Rows))
		pageBody := mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, struct {
			Page int               `json:"page"`
			Rows []json.RawMessage `json:"rows"`
		}{page, payload.Catalog.Rows[start:end]})
		payload.PageDigests = append(payload.PageDigests, excelPricingSnapshotDigest(pageBody))
	}
	var reconciliation excelPricingRemoteSnapshotReconciliation
	if err := json.Unmarshal(payload.Reconciliation, &reconciliation); err != nil {
		fixture.t.Fatalf("reconciliation: %v", err)
	}
	pageBody := mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, payload.PageDigests)
	catalogBody := mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, struct {
		DatasetRevision string          `json:"dataset_revision"`
		Columns         json.RawMessage `json:"columns"`
		Reconciliation  json.RawMessage `json:"reconciliation"`
		RowCount        int             `json:"row_count"`
	}{payload.DatasetRevision, payload.Catalog.Columns, payload.Reconciliation, payload.RowCount})
	pricingRevision, _ := json.Marshal(payload.PricingStateRevision)
	stateBody := mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, []json.RawMessage{
		pricingRevision, payload.Settings,
		mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, payload.MutationGuard),
	})
	snapshotBody := mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, struct {
		StateRevision         string            `json:"state_revision"`
		PricingStateRevision  string            `json:"pricing_state_revision"`
		PricingPolicyRevision string            `json:"pricing_policy_revision"`
		CatalogRevision       string            `json:"catalog_revision"`
		DatasetRevision       string            `json:"dataset_revision"`
		Source                canonical.Source  `json:"source"`
		Locale                string            `json:"locale"`
		Columns               json.RawMessage   `json:"columns"`
		Reconciliation        json.RawMessage   `json:"reconciliation"`
		Settings              json.RawMessage   `json:"settings"`
		PageDigests           []string          `json:"page_digests"`
		Rows                  []json.RawMessage `json:"rows"`
	}{
		payload.StateRevision, payload.PricingStateRevision,
		payload.PricingPolicyRevision, payload.CatalogRevision, payload.DatasetRevision,
		payload.Source, payload.Catalog.Locale, payload.Catalog.Columns,
		payload.Reconciliation, payload.Settings, payload.PageDigests, payload.Catalog.Rows,
	})
	digest := excelPricingSnapshotDigest(snapshotBody)
	payload.Digest = digest
	payload.Revision = digest
	payload.SnapshotRevision = digest
	payload.Integrity = excelPricingRemoteSnapshotIntegrity{
		Algorithm:             "sha256",
		PayloadDigest:         digest,
		StateDigest:           excelPricingSnapshotDigest(stateBody),
		CatalogMetadataDigest: excelPricingSnapshotDigest(catalogBody),
		PageRevisionsDigest:   excelPricingSnapshotDigest(pageBody),
		DatasetRevision:       payload.DatasetRevision,
		RowCount:              payload.RowCount,
		DistinctSyncKeys:      payload.RowCount,
		RemoteTotal:           payload.RowCount,
		PageCount:             payload.PageCount,
		WarningCount:          len(reconciliation.Warnings),
	}
	fixture.payloadBody = mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, payload)
}

func (fixture *excelPricingRemoteSnapshotFixture) setWarnings(warnings []interface{}) {
	fixture.t.Helper()
	var reconciliation map[string]interface{}
	if err := json.Unmarshal(fixture.payload.Reconciliation, &reconciliation); err != nil {
		fixture.t.Fatal(err)
	}
	reconciliation["warnings"] = warnings
	body := mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, reconciliation)
	fixture.payload.Reconciliation = body
	fixture.payload.Catalog.Reconciliation = body
}

func (fixture *excelPricingRemoteSnapshotFixture) setReconciliationCount(key string, value int) {
	fixture.t.Helper()
	var reconciliation map[string]interface{}
	if err := json.Unmarshal(fixture.payload.Reconciliation, &reconciliation); err != nil {
		fixture.t.Fatal(err)
	}
	counts := reconciliation["counts"].(map[string]interface{})
	counts[key] = value
	body := mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, reconciliation)
	fixture.payload.Reconciliation = body
	fixture.payload.Catalog.Reconciliation = body
}

func (fixture *excelPricingRemoteSnapshotFixture) addDuplicateRow() {
	fixture.t.Helper()
	fixture.payload.Catalog.Rows = append(fixture.payload.Catalog.Rows,
		append(json.RawMessage(nil), fixture.payload.Catalog.Rows[0]...))
	var reconciliation map[string]interface{}
	if err := json.Unmarshal(fixture.payload.Reconciliation, &reconciliation); err != nil {
		fixture.t.Fatal(err)
	}
	counts := reconciliation["counts"].(map[string]interface{})
	for _, key := range []string{"patris_products", "woocommerce_raw", "woocommerce_leaves", "union_rows", "matched"} {
		counts[key] = int(counts[key].(float64)) + 1
	}
	body := mustMarshalExcelPricingRemoteSnapshotTestJSON(fixture.t, reconciliation)
	fixture.payload.Reconciliation = body
	fixture.payload.Catalog.Reconciliation = body
}

func (fixture *excelPricingRemoteSnapshotFixture) assertCalls(
	t *testing.T,
	revision, start, bulk, status, cancel int,
) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.revisionCalls != revision || fixture.startCalls != start ||
		fixture.bulkCalls != bulk || fixture.statusCalls != status || fixture.cancelCalls != cancel {
		t.Fatalf("calls = revision:%d start:%d bulk:%d status:%d cancel:%d",
			fixture.revisionCalls, fixture.startCalls, fixture.bulkCalls,
			fixture.statusCalls, fixture.cancelCalls)
	}
	if fixture.headerFailure != "" {
		t.Fatal(fixture.headerFailure)
	}
}

func (fixture *excelPricingRemoteSnapshotFixture) assertNoStatusCalls(t *testing.T) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.statusCalls != 0 {
		t.Fatalf("status calls = %d", fixture.statusCalls)
	}
}

func (fixture *excelPricingRemoteSnapshotFixture) assertNoLegacyStateCalls(t *testing.T) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.legacyCalls != 0 {
		t.Fatalf("legacy /state calls = %d", fixture.legacyCalls)
	}
}

func mustMarshalExcelPricingRemoteSnapshotTestJSON(t *testing.T, value interface{}) []byte {
	t.Helper()
	body, err := marshalExcelPricingRemoteSnapshotJSON(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

func testExcelPricingRevision(character byte) string {
	return "sha256:" + strings.Repeat(string(character), 64)
}
