package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
)

func TestWriteExcelPricingSnapshotSSEPadsFrameForWinHTTPDelivery(t *testing.T) {
	response := httptest.NewRecorder()
	payload := map[string]interface{}{
		"schema":   excelPricingSnapshotEventSchema,
		"sequence": 7,
		"kind":     "pricing_state_changed",
	}
	if err := writeExcelPricingSnapshotSSE(response, 7, "pricing_state_changed", payload); err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if len(body) < excelPricingSnapshotSSEFlushPadding || !strings.HasPrefix(body, ": ") {
		t.Fatalf("SSE frame was not padded for WinHTTP delivery: bytes=%d", len(body))
	}
	if !strings.Contains(body, "\nid: 7\nevent: pricing_state_changed\ndata: ") ||
		!strings.HasSuffix(body, "\n\n") {
		t.Fatalf("padded SSE frame lost its semantic event: %q", body[len(body)-200:])
	}
}

func TestExcelPricingSnapshotStartValidatesFreshCanonicalSourceRevision(t *testing.T) {
	current := excelPricingStateSourceForTest()
	stale := current
	stale.Revision = excelPricingRevisionForTest("stale-snapshot-source")
	stateRevision := excelPricingRevisionForTest("fresh-source-state")
	datasetRevision := excelPricingRevisionForTest("fresh-source-dataset")
	rows := []map[string]interface{}{{
		"sync_key":              "product-1",
		"reconciliation_status": "matched",
		"name":                  "product 1",
	}}
	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		writeExcelPricingSnapshotRemotePage(
			t, w, current, stateRevision, datasetRevision, 1, 1, rows, rows,
		)
	}))
	defer remote.Close()

	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	var canonicalCalls atomic.Int32
	projection := canonicalProjectionSequence(current)
	server.excelPricing.canonical = func(ctx context.Context) (recordpipe.Result, error) {
		canonicalCalls.Add(1)
		return projection(ctx)
	}
	token := openExcelPricingSession(t, server)

	staleID := "snapshot-stale-source-0001"
	staleRequest := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(stale, staleID, "fa", 0),
		token,
	)
	staleRequest.Header.Set("Idempotency-Key", staleID)
	staleResponse := httptest.NewRecorder()
	server.router.ServeHTTP(staleResponse, staleRequest)
	if staleResponse.Code != http.StatusConflict || !strings.Contains(staleResponse.Body.String(), "canonical_source_mismatch") {
		t.Fatalf("stale start status=%d, want 409 canonical_source_mismatch: %s", staleResponse.Code, staleResponse.Body.String())
	}
	if remoteCalls.Load() != 0 {
		t.Fatalf("stale start reached remote pricing service %d time(s), want 0", remoteCalls.Load())
	}

	currentID := "snapshot-current-source-0001"
	currentRequest := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(current, currentID, "fa", 0),
		token,
	)
	currentRequest.Header.Set("Idempotency-Key", currentID)
	currentResponse := httptest.NewRecorder()
	server.router.ServeHTTP(currentResponse, currentRequest)
	if currentResponse.Code != http.StatusAccepted {
		t.Fatalf("current start status=%d, want 202: %s", currentResponse.Code, currentResponse.Body.String())
	}
	jobID := excelPricingSnapshotJobIDForTest(t, currentResponse.Body.Bytes())
	waitForExcelPricingSnapshotStatus(t, server, token, jobID, "ready")
	if canonicalCalls.Load() != 2 {
		t.Fatalf("canonical validation calls=%d, want 2", canonicalCalls.Load())
	}
	if remoteCalls.Load() != 1 {
		t.Fatalf("current start remote calls=%d, want 1", remoteCalls.Load())
	}
}

func TestExcelPricingSnapshotAggregatesCachesAndServesETag(t *testing.T) {
	source := excelPricingStateSourceForTest()
	stateRevision := excelPricingRevisionForTest("snapshot-state")
	datasetRevision := excelPricingRevisionForTest("snapshot-dataset")
	rows := make([]map[string]interface{}, excelPricingSnapshotPageSize*4+37)
	for index := range rows {
		rows[index] = map[string]interface{}{
			"sync_key":              "product-" + strconv.Itoa(index+1),
			"reconciliation_status": "matched",
			"name":                  "product " + strconv.Itoa(index+1),
		}
	}
	pages := (len(rows) + excelPricingSnapshotPageSize - 1) / excelPricingSnapshotPageSize
	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		var request excelPricingRemoteRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Operation != "state" || request.Limit != excelPricingSnapshotPageSize {
			t.Errorf("remote request=%+v", request)
			return
		}
		start := (request.Page - 1) * excelPricingSnapshotPageSize
		end := start + excelPricingSnapshotPageSize
		if end > len(rows) {
			end = len(rows)
		}
		pageRows := rows[start:end]
		writeExcelPricingSnapshotRemotePage(
			t, w, source, stateRevision, datasetRevision, request.Page, pages, rows, pageRows,
		)
	}))
	defer remote.Close()

	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	requestID := "snapshot-start-test-0001"
	start := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 60),
		token,
	)
	start.Header.Set("Idempotency-Key", requestID)
	startResponse := httptest.NewRecorder()
	server.router.ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("start status=%d: %s", startResponse.Code, startResponse.Body.String())
	}
	jobID := excelPricingSnapshotJobIDForTest(t, startResponse.Body.Bytes())
	status := waitForExcelPricingSnapshotStatus(t, server, token, jobID, "ready")
	if status["snapshot_revision"] == "" || status["state_revision"] != stateRevision {
		t.Fatalf("ready status=%#v", status)
	}
	if got := remoteCalls.Load(); got != int32(pages) {
		t.Fatalf("remote state calls=%d, want %d", got, pages)
	}

	payloadRequest := authenticatedExcelPricingRequest(
		http.MethodGet,
		"/api/pricing-sync/snapshots/"+jobID+"/payload",
		"",
		token,
	)
	payloadResponse := httptest.NewRecorder()
	server.router.ServeHTTP(payloadResponse, payloadRequest)
	if payloadResponse.Code != http.StatusOK {
		t.Fatalf("payload status=%d: %s", payloadResponse.Code, payloadResponse.Body.String())
	}
	etag := payloadResponse.Header().Get("ETag")
	if etag == "" || etag != status["etag"] ||
		etag != excelPricingSnapshotETag(excelPricingSnapshotDigest(payloadResponse.Body.Bytes())) {
		t.Fatalf("payload ETag=%q, job ETag=%#v", etag, status["etag"])
	}
	var payload excelPricingSnapshotPayload
	if err := json.Unmarshal(payloadResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Schema != excelPricingSnapshotPayloadSchema ||
		payload.Projection != excelPricingSnapshotProjectionFull ||
		len(payload.RowFields) != 0 ||
		payload.StateRevision != stateRevision ||
		payload.Integrity.RowCount != len(rows) ||
		payload.Integrity.DistinctSyncKeys != len(rows) ||
		payload.Integrity.RemoteTotal != len(rows) ||
		payload.Integrity.PageCount != pages ||
		payload.Integrity.StateDigest == "" ||
		payload.MutationGuard.ExpectedStateRevision != stateRevision ||
		!payload.MutationGuard.Preview.RequiresIdempotencyKey ||
		!payload.MutationGuard.Apply.RequiresIfMatch ||
		payload.MutationGuard.Apply.Confirmation != "APPLY" {
		t.Fatalf("payload contract=%+v", payload)
	}
	if payload.Integrity.StateDigest != excelPricingSnapshotDigest(payload.State) {
		t.Fatalf("state digest=%q does not bind the exact state", payload.Integrity.StateDigest)
	}
	var state struct {
		Catalog struct {
			Rows       []map[string]interface{}       `json:"rows"`
			Pagination excelPricingSnapshotPagination `json:"pagination"`
		} `json:"catalog"`
	}
	if err := json.Unmarshal(payload.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Catalog.Rows) != len(rows) || state.Catalog.Pagination.Pages != 1 ||
		state.Catalog.Pagination.Limit != len(rows) || state.Catalog.Pagination.HasMore {
		t.Fatalf("bulk state pagination=%+v rows=%d", state.Catalog.Pagination, len(state.Catalog.Rows))
	}

	notModified := authenticatedExcelPricingRequest(
		http.MethodGet,
		"/api/pricing-sync/snapshots/"+jobID+"/payload",
		"",
		token,
	)
	notModified.Header.Set("If-None-Match", etag)
	notModifiedResponse := httptest.NewRecorder()
	server.router.ServeHTTP(notModifiedResponse, notModified)
	if notModifiedResponse.Code != http.StatusNotModified || notModifiedResponse.Body.Len() != 0 {
		t.Fatalf("conditional payload status=%d body=%q", notModifiedResponse.Code, notModifiedResponse.Body.String())
	}

	replay := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 60),
		token,
	)
	replay.Header.Set("Idempotency-Key", requestID)
	replayResponse := httptest.NewRecorder()
	server.router.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusOK || excelPricingSnapshotJobIDForTest(t, replayResponse.Body.Bytes()) != jobID {
		t.Fatalf("replay status=%d: %s", replayResponse.Code, replayResponse.Body.String())
	}

	cachedRequestID := "snapshot-start-test-0002"
	cached := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, cachedRequestID, "fa", 60),
		token,
	)
	cached.Header.Set("Idempotency-Key", cachedRequestID)
	cachedResponse := httptest.NewRecorder()
	server.router.ServeHTTP(cachedResponse, cached)
	if cachedResponse.Code != http.StatusOK {
		t.Fatalf("cached status=%d: %s", cachedResponse.Code, cachedResponse.Body.String())
	}
	var cachedStatus map[string]interface{}
	if err := json.Unmarshal(cachedResponse.Body.Bytes(), &cachedStatus); err != nil {
		t.Fatal(err)
	}
	if cachedStatus["cached"] != true || remoteCalls.Load() != int32(pages) {
		t.Fatalf("cached status=%#v remote calls=%d", cachedStatus, remoteCalls.Load())
	}

	// The workbook itself remains empty on disk. A long-lived companion can
	// still serve this same validated in-memory projection promptly, but only
	// while the authenticated bridge proves its exact revision tuple.
	store := server.excelPricing.snapshots
	store.mu.Lock()
	for _, snapshot := range store.cache {
		snapshot.createdAt = store.now().UTC().Add(-6 * time.Hour)
	}
	store.mu.Unlock()
	workdayRequestID := "snapshot-start-test-workday-cache-0001"
	workdayCached := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(
			source,
			workdayRequestID,
			"fa",
			int(excelPricingSnapshotMaxCacheAge/time.Second),
		),
		token,
	)
	workdayCached.Header.Set("Idempotency-Key", workdayRequestID)
	workdayResponse := httptest.NewRecorder()
	server.router.ServeHTTP(workdayResponse, workdayCached)
	if workdayResponse.Code != http.StatusOK || remoteCalls.Load() != int32(pages) {
		t.Fatalf("workday cache status=%d remote calls=%d: %s",
			workdayResponse.Code, remoteCalls.Load(), workdayResponse.Body.String())
	}

	revisionRequestID := "snapshot-start-test-0003"
	revisionCached := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBodyWithExpected(source, revisionRequestID, "fa", 0, stateRevision),
		token,
	)
	revisionCached.Header.Set("Idempotency-Key", revisionRequestID)
	revisionResponse := httptest.NewRecorder()
	server.router.ServeHTTP(revisionResponse, revisionCached)
	if revisionResponse.Code != http.StatusOK {
		t.Fatalf("revision cache status=%d: %s", revisionResponse.Code, revisionResponse.Body.String())
	}
	var revisionStatus map[string]interface{}
	if err := json.Unmarshal(revisionResponse.Body.Bytes(), &revisionStatus); err != nil {
		t.Fatal(err)
	}
	if revisionStatus["cached"] != true || remoteCalls.Load() != int32(pages) {
		t.Fatalf("revision cache=%#v remote calls=%d", revisionStatus, remoteCalls.Load())
	}

	// Age alone is never sufficient for a local replay. If the authenticated
	// revision bridge cannot verify the exact source/state/catalog tuple, the
	// request must take the full collector path even while the cache is young.
	server.excelPricing.snapshotRevisionCurrent = func(canonical.Source, string, string) bool {
		return false
	}
	unverifiedRequestID := "snapshot-start-test-unverified-0001"
	unverified := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, unverifiedRequestID, "fa", 60),
		token,
	)
	unverified.Header.Set("Idempotency-Key", unverifiedRequestID)
	unverifiedResponse := httptest.NewRecorder()
	server.router.ServeHTTP(unverifiedResponse, unverified)
	if unverifiedResponse.Code != http.StatusAccepted {
		t.Fatalf("unverified start status=%d, want 202: %s",
			unverifiedResponse.Code, unverifiedResponse.Body.String())
	}
	unverifiedJobID := excelPricingSnapshotJobIDForTest(t, unverifiedResponse.Body.Bytes())
	waitForExcelPricingSnapshotStatus(t, server, token, unverifiedJobID, "ready")
	if remoteCalls.Load() != int32(pages*2) {
		t.Fatalf("unverified cache reached remote %d times, want %d",
			remoteCalls.Load(), pages*2)
	}
}

func TestExcelPricingSnapshotReadyReplayPublishesReturnedCachedJob(t *testing.T) {
	source := excelPricingStateSourceForTest()
	stateRevision := excelPricingRevisionForTest("cached-ready-state")
	datasetRevision := excelPricingRevisionForTest("cached-ready-dataset")
	row := map[string]interface{}{
		"sync_key":              "cached-ready-product",
		"reconciliation_status": "matched",
		"name":                  "cached ready product",
	}
	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		writeExcelPricingSnapshotRemotePage(
			t, w, source, stateRevision, datasetRevision, 1, 1,
			[]map[string]interface{}{row}, []map[string]interface{}{row},
		)
	}))
	defer remote.Close()

	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	start := func(requestID string) (*httptest.ResponseRecorder, string) {
		t.Helper()
		request := authenticatedExcelPricingRequest(
			http.MethodPost, "/api/pricing-sync/snapshots",
			validExcelPricingSnapshotStartBody(source, requestID, "fa", 60), token,
		)
		request.Header.Set("Idempotency-Key", requestID)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		return response, excelPricingSnapshotJobIDForTest(t, response.Body.Bytes())
	}

	leaderResponse, leaderID := start("snapshot-cached-ready-leader-0001")
	if leaderResponse.Code != http.StatusAccepted {
		t.Fatalf("leader start=%d: %s", leaderResponse.Code, leaderResponse.Body.String())
	}
	waitForExcelPricingSnapshotStatus(t, server, token, leaderID, "ready")
	store := server.excelPricing.snapshots
	store.mu.Lock()
	leaderReady := cloneExcelPricingStateChangeEvent(*store.latestChange)
	store.mu.Unlock()
	if leaderReady.Kind != "snapshot_ready" || leaderReady.JobID != leaderID {
		t.Fatalf("leader ready event=%+v", leaderReady)
	}

	cachedResponse, cachedID := start("snapshot-cached-ready-returned-0001")
	if cachedResponse.Code != http.StatusOK || cachedID == leaderID {
		t.Fatalf("cached start=%d leader=%q cached=%q: %s",
			cachedResponse.Code, leaderID, cachedID, cachedResponse.Body.String())
	}
	store.mu.Lock()
	cachedReady := cloneExcelPricingStateChangeEvent(*store.latestChange)
	cachedJobSequence := store.jobs[cachedID].eventSequence
	store.mu.Unlock()
	if cachedReady.Kind != "snapshot_ready" ||
		cachedReady.Reason != "snapshot_cache_reused" ||
		cachedReady.JobID != cachedID ||
		cachedReady.Sequence <= leaderReady.Sequence ||
		cachedReady.Sequence <= cachedJobSequence {
		t.Fatalf("cached ready=%+v job_sequence=%d leader_sequence=%d",
			cachedReady, cachedJobSequence, leaderReady.Sequence)
	}

	replayResponse, replayID := start("snapshot-cached-ready-returned-0001")
	if replayResponse.Code != http.StatusOK || replayID != cachedID {
		t.Fatalf("ready replay=%d cached=%q replay=%q: %s",
			replayResponse.Code, cachedID, replayID, replayResponse.Body.String())
	}
	store.mu.Lock()
	replayedReady := cloneExcelPricingStateChangeEvent(*store.latestChange)
	store.mu.Unlock()
	if replayedReady.Kind != "snapshot_ready" ||
		replayedReady.Reason != "snapshot_idempotent_replayed" ||
		replayedReady.JobID != cachedID ||
		replayedReady.Sequence <= cachedReady.Sequence {
		t.Fatalf("replayed ready=%+v cached_sequence=%d", replayedReady, cachedReady.Sequence)
	}
	if remoteCalls.Load() != 1 {
		t.Fatalf("remote calls=%d, want one cold build", remoteCalls.Load())
	}

	loopback := httptest.NewServer(server.router)
	defer loopback.Close()
	streamContext, cancelStream := context.WithCancel(context.Background())
	streamRequest, err := http.NewRequestWithContext(
		streamContext, http.MethodGet, loopback.URL+"/api/pricing-sync/events", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	streamRequest.Header.Set(excelPricingClientHeader, excelPricingClientID)
	streamRequest.Header.Set(excelPricingCSRFHeader, token)
	streamRequest.Header.Set("Accept", "text/event-stream")
	streamResponse, err := loopback.Client().Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	if streamResponse.StatusCode != http.StatusOK {
		_ = streamResponse.Body.Close()
		t.Fatalf("durable stream=%d", streamResponse.StatusCode)
	}
	newest := readExcelPricingSnapshotSSEEventForTest(t, bufio.NewScanner(streamResponse.Body))
	cancelStream()
	_ = streamResponse.Body.Close()
	if newest.Sequence != replayedReady.Sequence || newest.Kind != "snapshot_ready" ||
		newest.Change == nil || newest.Change.JobID != cachedID {
		t.Fatalf("no-cursor durable replay=%+v, want cached job %q at %d",
			newest, cachedID, replayedReady.Sequence)
	}

	payloadRequest := authenticatedExcelPricingRequest(
		http.MethodGet, "/api/pricing-sync/snapshots/"+cachedID+"/payload", "", token,
	)
	payloadResponse := httptest.NewRecorder()
	server.router.ServeHTTP(payloadResponse, payloadRequest)
	if payloadResponse.Code != http.StatusOK {
		t.Fatalf("cached payload=%d: %s", payloadResponse.Code, payloadResponse.Body.String())
	}
	var payload excelPricingSnapshotPayload
	if err := json.Unmarshal(payloadResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	etag := payloadResponse.Header().Get("ETag")
	expectedIdentity := excelPricingSnapshotIdentity{
		Source:           payload.Source,
		CatalogRevision:  payload.Integrity.DatasetRevision,
		StateRevision:    payload.StateRevision,
		SnapshotRevision: payload.SnapshotRevision,
		ETag:             etag,
	}
	change := newest.Change
	if etag == "" || etag != excelPricingSnapshotETag(excelPricingSnapshotDigest(payloadResponse.Body.Bytes())) ||
		change.Source == nil || *change.Source != source || change.Identity == nil ||
		*change.Identity != expectedIdentity || change.CatalogRevision != datasetRevision ||
		change.StateRevision != stateRevision || change.SnapshotRevision != payload.SnapshotRevision ||
		change.ETag != etag || !change.Verified || change.Stale {
		t.Fatalf("ready identity=%+v payload=%+v etag=%q", change, payload, etag)
	}
}

func TestExcelPricingSnapshotExcelV1UsesPositionalRowsAndSeparateCache(t *testing.T) {
	source := excelPricingStateSourceForTest()
	stateRevision := excelPricingRevisionForTest("excel-v1-state")
	datasetRevision := excelPricingRevisionForTest("excel-v1-dataset")
	row := excelPricingSnapshotFullRowForTest(1)
	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		writeExcelPricingSnapshotRemotePage(
			t, w, source, stateRevision, datasetRevision, 1, 1,
			[]map[string]interface{}{row}, []map[string]interface{}{row},
		)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	requestID := "snapshot-excel-v1-test-0001"
	start := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBodyWithProjection(
			source, requestID, "fa", 30, "", excelPricingSnapshotProjectionExcelV1,
		),
		token,
	)
	start.Header.Set("Idempotency-Key", requestID)
	startResponse := httptest.NewRecorder()
	server.router.ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("excel-v1 start=%d: %s", startResponse.Code, startResponse.Body.String())
	}
	jobID := excelPricingSnapshotJobIDForTest(t, startResponse.Body.Bytes())
	status := waitForExcelPricingSnapshotStatus(t, server, token, jobID, "ready")
	if status["projection"] != excelPricingSnapshotProjectionExcelV1 {
		t.Fatalf("job projection=%#v", status["projection"])
	}
	payloadRequest := authenticatedExcelPricingRequest(
		http.MethodGet, "/api/pricing-sync/snapshots/"+jobID+"/payload", "", token,
	)
	payloadResponse := httptest.NewRecorder()
	server.router.ServeHTTP(payloadResponse, payloadRequest)
	if payloadResponse.Code != http.StatusOK {
		t.Fatalf("excel-v1 payload=%d: %s", payloadResponse.Code, payloadResponse.Body.String())
	}
	var payload excelPricingSnapshotPayload
	if err := json.Unmarshal(payloadResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Projection != excelPricingSnapshotProjectionExcelV1 ||
		len(payload.RowFields) != len(excelPricingSnapshotExcelV1RowFields) {
		t.Fatalf("excel-v1 payload contract=%+v", payload)
	}
	for index, field := range excelPricingSnapshotExcelV1RowFields {
		if payload.RowFields[index] != field {
			t.Fatalf("row_fields[%d]=%q, want %q", index, payload.RowFields[index], field)
		}
	}
	var state struct {
		Catalog struct {
			Rows    [][]json.RawMessage          `json:"rows"`
			Columns []map[string]json.RawMessage `json:"columns"`
		} `json:"catalog"`
	}
	if err := json.Unmarshal(payload.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Catalog.Rows) != 1 ||
		len(state.Catalog.Rows[0]) != len(excelPricingSnapshotExcelV1RowFields) {
		t.Fatalf("projected rows=%d fields=%d", len(state.Catalog.Rows), len(state.Catalog.Rows[0]))
	}
	fieldIndex := make(map[string]int, len(payload.RowFields))
	for index, field := range payload.RowFields {
		fieldIndex[field] = index
	}
	var syncKey, statusText, productName, imageURL string
	_ = json.Unmarshal(state.Catalog.Rows[0][fieldIndex["sync_key"]], &syncKey)
	_ = json.Unmarshal(state.Catalog.Rows[0][fieldIndex["reconciliation_status"]], &statusText)
	_ = json.Unmarshal(state.Catalog.Rows[0][fieldIndex["name"]], &productName)
	_ = json.Unmarshal(state.Catalog.Rows[0][fieldIndex["image_url"]], &imageURL)
	if syncKey != "woo:100001" || statusText != "matched" || productName == "" ||
		imageURL != row["image_url"] || len(state.Catalog.Columns) != 3 {
		t.Fatalf("projected identity=%q/%q name=%q image=%q columns=%d", syncKey, statusText, productName, imageURL, len(state.Catalog.Columns))
	}
	if payload.Integrity.StateDigest != excelPricingSnapshotDigest(payload.State) ||
		payloadResponse.Header().Get("ETag") !=
			excelPricingSnapshotETag(excelPricingSnapshotDigest(payloadResponse.Body.Bytes())) {
		t.Fatal("excel-v1 integrity or strong ETag mismatch")
	}
	if excelPricingSnapshotCacheKey(source, "fa", excelPricingSnapshotProjectionFull) ==
		excelPricingSnapshotCacheKey(source, "fa", excelPricingSnapshotProjectionExcelV1) {
		t.Fatal("full and excel-v1 projections shared a cache key")
	}

	invalidID := "snapshot-invalid-projection-0001"
	invalid := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBodyWithProjection(
			source, invalidID, "fa", 0, "", "unknown-v1",
		),
		token,
	)
	invalid.Header.Set("Idempotency-Key", invalidID)
	invalidResponse := httptest.NewRecorder()
	server.router.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest || remoteCalls.Load() != 1 {
		t.Fatalf("invalid projection status=%d remote=%d", invalidResponse.Code, remoteCalls.Load())
	}
}

func TestExcelPricingSnapshotExcelV1PayloadSizeForRepresentativeCatalog(t *testing.T) {
	source := excelPricingStateSourceForTest()
	rows := make([]map[string]interface{}, excelPricingSnapshotPageSize*4+37)
	rawRows := make([]json.RawMessage, len(rows))
	for index := range rows {
		rows[index] = excelPricingSnapshotFullRowForTest(index + 1)
		encoded, err := json.Marshal(rows[index])
		if err != nil {
			t.Fatal(err)
		}
		rawRows[index] = encoded
	}
	recorder := httptest.NewRecorder()
	writeExcelPricingSnapshotRemotePage(
		t,
		recorder,
		source,
		excelPricingRevisionForTest("size-state"),
		excelPricingRevisionForTest("size-dataset"),
		1,
		(len(rows)+excelPricingSnapshotPageSize-1)/excelPricingSnapshotPageSize,
		rows,
		rows[:excelPricingSnapshotPageSize],
	)
	first, err := parseExcelPricingSnapshotPage(recorder.Body.Bytes(), source, 1)
	if err != nil {
		t.Fatal(err)
	}
	pageRevisions := make([]string, first.pages)
	for page := range pageRevisions {
		pageRevisions[page] = excelPricingRevisionForTest("size-page-" + strconv.Itoa(page+1))
	}
	now := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	full, err := buildExcelPricingSnapshot(
		first, source, rawRows, pageRevisions, excelPricingSnapshotProjectionFull, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	excel, err := buildExcelPricingSnapshot(
		first, source, rawRows, pageRevisions, excelPricingSnapshotProjectionExcelV1, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(
		"representative payload rows=%d bytes: full=%d excel-v1=%d reduction=%.1f%%",
		len(rows),
		len(full.body),
		len(excel.body),
		100*(1-float64(len(excel.body))/float64(len(full.body))),
	)
	if len(excel.body)*2 >= len(full.body) {
		t.Fatalf("excel-v1 payload=%d is not less than half of full=%d", len(excel.body), len(full.body))
	}
	var payload excelPricingSnapshotPayload
	if err := json.Unmarshal(excel.body, &payload); err != nil {
		t.Fatal(err)
	}
	var state struct {
		Catalog struct {
			Rows [][]json.RawMessage `json:"rows"`
		} `json:"catalog"`
	}
	if err := json.Unmarshal(payload.State, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Catalog.Rows) != len(rows) || len(state.Catalog.Rows[0]) != 27 ||
		payload.Integrity.RowCount != len(rows) || payload.Integrity.DistinctSyncKeys != len(rows) {
		t.Fatalf("excel-v1 representative contract failed: rows=%d fields=%d integrity=%+v", len(state.Catalog.Rows), len(state.Catalog.Rows[0]), payload.Integrity)
	}
}

func TestExcelPricingSnapshotExcelV1RejectsInvalidLeanRows(t *testing.T) {
	base := excelPricingSnapshotFullRowForTest(1)
	tests := map[string]func(map[string]interface{}){
		"missing status": func(row map[string]interface{}) {
			delete(row, "reconciliation_status")
		},
		"ambiguous status": func(row map[string]interface{}) {
			row["reconciliation_status"] = "ambiguous"
		},
		"nested consumed field": func(row map[string]interface{}) {
			row["categories"] = []string{"unsafe", "nested"}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			row := make(map[string]interface{}, len(base))
			for key, value := range base {
				row[key] = value
			}
			mutate(row)
			encoded, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := projectExcelPricingSnapshotRows(
				[]json.RawMessage{encoded}, excelPricingSnapshotProjectionExcelV1,
			); err == nil {
				t.Fatal("invalid excel-v1 row was accepted")
			}
		})
	}
	encoded, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	full, fields, err := projectExcelPricingSnapshotRows(
		[]json.RawMessage{encoded}, excelPricingSnapshotProjectionFull,
	)
	if err != nil || len(full) != 1 || fields != nil || string(full[0]) != string(encoded) {
		t.Fatalf("full projection changed: rows=%d fields=%v err=%v", len(full), fields, err)
	}
}

func TestExcelPricingSnapshotReturnsBusyWithoutWaiting(t *testing.T) {
	source := excelPricingStateSourceForTest()
	started := make(chan struct{})
	release := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
			writeExcelPricingSnapshotRemotePage(
				t, w, source, excelPricingRevisionForTest("busy-state"),
				excelPricingRevisionForTest("busy-dataset"), 1, 1,
				[]map[string]interface{}{{"sync_key": "A", "reconciliation_status": "matched"}},
				[]map[string]interface{}{{"sync_key": "A", "reconciliation_status": "matched"}},
			)
		case <-r.Context().Done():
		}
	}))
	defer remote.Close()
	defer close(release)

	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	firstID := "snapshot-busy-test-0001"
	first := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, firstID, "fa", 0), token,
	)
	first.Header.Set("Idempotency-Key", firstID)
	firstResponse := httptest.NewRecorder()
	server.router.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusAccepted {
		t.Fatalf("first status=%d: %s", firstResponse.Code, firstResponse.Body.String())
	}
	jobID := excelPricingSnapshotJobIDForTest(t, firstResponse.Body.Bytes())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("remote snapshot did not start")
	}

	secondID := "snapshot-busy-test-0002"
	second := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, secondID, "fa_IR", 0), token,
	)
	second.Header.Set("Idempotency-Key", secondID)
	startedAt := time.Now()
	secondResponse := httptest.NewRecorder()
	server.router.ServeHTTP(secondResponse, second)
	if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
		t.Fatalf("busy response waited %s", elapsed)
	}
	if secondResponse.Code != http.StatusTooManyRequests || secondResponse.Header().Get("Retry-After") != "1" {
		t.Fatalf("busy status=%d headers=%v body=%s", secondResponse.Code, secondResponse.Header(), secondResponse.Body.String())
	}

	cancel := authenticatedExcelPricingRequest(
		http.MethodDelete, "/api/pricing-sync/snapshots/"+jobID, "", token,
	)
	cancelResponse := httptest.NewRecorder()
	server.router.ServeHTTP(cancelResponse, cancel)
	if cancelResponse.Code != http.StatusAccepted {
		t.Fatalf("cancel status=%d: %s", cancelResponse.Code, cancelResponse.Body.String())
	}
	status := waitForExcelPricingSnapshotStatus(t, server, token, jobID, "cancelled")
	if status["code"] != "request_cancelled" {
		t.Fatalf("cancelled status=%#v", status)
	}
}

func TestExcelPricingSnapshotCoalescesSameSourceAndLocale(t *testing.T) {
	source := excelPricingStateSourceForTest()
	started := make(chan struct{})
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
			writeExcelPricingSnapshotRemotePage(
				t, w, source, excelPricingRevisionForTest("coalesced-state"),
				excelPricingRevisionForTest("coalesced-dataset"), 1, 1,
				[]map[string]interface{}{{"sync_key": "A", "reconciliation_status": "matched"}},
				[]map[string]interface{}{{"sync_key": "A", "reconciliation_status": "matched"}},
			)
		case <-r.Context().Done():
		}
	}))
	defer remote.Close()

	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	startJob := func(requestID string) *httptest.ResponseRecorder {
		request := authenticatedExcelPricingRequest(
			http.MethodPost, "/api/pricing-sync/snapshots",
			validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
		)
		request.Header.Set("Idempotency-Key", requestID)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		return response
	}
	leaderResponse := startJob("snapshot-coalesced-test-0001")
	if leaderResponse.Code != http.StatusAccepted {
		t.Fatalf("leader status=%d: %s", leaderResponse.Code, leaderResponse.Body.String())
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("coalesced leader did not start")
	}
	followerResponse := startJob("snapshot-coalesced-test-0002")
	if followerResponse.Code != http.StatusAccepted {
		t.Fatalf("follower status=%d: %s", followerResponse.Code, followerResponse.Body.String())
	}
	var followerStart map[string]interface{}
	if err := json.Unmarshal(followerResponse.Body.Bytes(), &followerStart); err != nil {
		t.Fatal(err)
	}
	if followerStart["coalesced"] != true || remoteCalls.Load() != 1 {
		t.Fatalf("follower=%#v remote calls=%d", followerStart, remoteCalls.Load())
	}
	leaderID := excelPricingSnapshotJobIDForTest(t, leaderResponse.Body.Bytes())
	followerID := excelPricingSnapshotJobIDForTest(t, followerResponse.Body.Bytes())
	close(release)
	released = true
	waitForExcelPricingSnapshotStatus(t, server, token, leaderID, "ready")
	waitForExcelPricingSnapshotStatus(t, server, token, followerID, "ready")
	if remoteCalls.Load() != 1 {
		t.Fatalf("coalesced jobs made %d remote calls", remoteCalls.Load())
	}
}

func TestExcelPricingSnapshotCancelsGroupWhenLastFollowerCancels(t *testing.T) {
	source := excelPricingStateSourceForTest()
	started := make(chan struct{})
	remoteCancelled := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(remoteCancelled)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	startJob := func(requestID string) string {
		request := authenticatedExcelPricingRequest(
			http.MethodPost, "/api/pricing-sync/snapshots",
			validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
		)
		request.Header.Set("Idempotency-Key", requestID)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("start status=%d: %s", response.Code, response.Body.String())
		}
		return excelPricingSnapshotJobIDForTest(t, response.Body.Bytes())
	}
	leaderID := startJob("snapshot-group-cancel-0001")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("group leader did not start")
	}
	followerID := startJob("snapshot-group-cancel-0002")
	cancelJob := func(jobID string) {
		request := authenticatedExcelPricingRequest(
			http.MethodDelete, "/api/pricing-sync/snapshots/"+jobID, "", token,
		)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("cancel status=%d: %s", response.Code, response.Body.String())
		}
	}
	cancelJob(leaderID)
	select {
	case <-remoteCancelled:
		t.Fatal("leader cancellation stopped work needed by a follower")
	case <-time.After(25 * time.Millisecond):
	}
	cancelJob(followerID)
	select {
	case <-remoteCancelled:
	case <-time.After(time.Second):
		t.Fatal("last follower cancellation did not cancel remote work")
	}
	waitForExcelPricingSnapshotStatus(t, server, token, leaderID, "cancelled")
	waitForExcelPricingSnapshotStatus(t, server, token, followerID, "cancelled")
}

func TestExcelPricingSnapshotReportsCapacityBeforePagingPastLimit(t *testing.T) {
	source := excelPricingStateSourceForTest()
	rows := make([]map[string]interface{}, excelPricingSnapshotPageSize*excelPricingSnapshotMaxPages+1)
	for index := range rows {
		rows[index] = map[string]interface{}{
			"sync_key":              "too-large-" + strconv.Itoa(index),
			"reconciliation_status": "matched",
		}
	}
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeExcelPricingSnapshotRemotePage(
			t, w, source, excelPricingRevisionForTest("too-large-state"),
			excelPricingRevisionForTest("too-large-dataset"), 1,
			excelPricingSnapshotMaxPages+1, rows, rows[:excelPricingSnapshotPageSize],
		)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	requestID := "snapshot-too-large-0001"
	request := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
	)
	request.Header.Set("Idempotency-Key", requestID)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	jobID := excelPricingSnapshotJobIDForTest(t, response.Body.Bytes())
	status := waitForExcelPricingSnapshotStatus(t, server, token, jobID, "failed")
	capacity := status["capacity"].(map[string]interface{})
	if status["code"] != "snapshot_too_large" ||
		capacity["max_rows"] != float64(excelPricingSnapshotPageSize*excelPricingSnapshotMaxPages) ||
		calls.Load() != 1 {
		t.Fatalf("too-large status=%#v calls=%d", status, calls.Load())
	}
}

func TestExcelPricingSnapshotRejectsUnsafeMetadata(t *testing.T) {
	source := excelPricingStateSourceForTest()
	stateRevision := excelPricingRevisionForTest("unsafe-state")
	datasetRevision := excelPricingRevisionForTest("unsafe-dataset")
	mutations := map[string]func(map[string]interface{}){
		"missing submitted revision": func(root map[string]interface{}) {
			delete(root["source"].(map[string]interface{}), "submitted_revision")
		},
		"mismatched current revision": func(root map[string]interface{}) {
			root["source"].(map[string]interface{})["current_revision"] = excelPricingRevisionForTest("drift")
		},
		"mismatched optional source revision": func(root map[string]interface{}) {
			root["source"].(map[string]interface{})["revision"] = excelPricingRevisionForTest("drift")
		},
		"missing reconciliation source id": func(root map[string]interface{}) {
			catalog := root["catalog"].(map[string]interface{})
			reconciliation := catalog["reconciliation"].(map[string]interface{})
			delete(reconciliation["source"].(map[string]interface{}), "id")
		},
		"ambiguous identity": func(root map[string]interface{}) {
			catalog := root["catalog"].(map[string]interface{})
			reconciliation := catalog["reconciliation"].(map[string]interface{})
			reconciliation["counts"].(map[string]interface{})["ambiguous_codes"] = float64(1)
		},
		"count algebra mismatch": func(root map[string]interface{}) {
			catalog := root["catalog"].(map[string]interface{})
			reconciliation := catalog["reconciliation"].(map[string]interface{})
			reconciliation["counts"].(map[string]interface{})["matched"] = float64(0)
		},
		"product type cache drift": func(root map[string]interface{}) {
			catalog := root["catalog"].(map[string]interface{})
			catalog["integrity"] = map[string]interface{}{
				"warnings": []interface{}{map[string]interface{}{
					"code": "product_type_cache_drift_term_changed",
				}},
			}
		},
		"projection integrity": func(root map[string]interface{}) {
			root["warnings"] = []interface{}{map[string]interface{}{
				"code": "projection_integrity_product_type_readback_failed",
			}}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeExcelPricingSnapshotRemotePage(
				t, recorder, source, stateRevision, datasetRevision, 1, 1,
				[]map[string]interface{}{{"sync_key": "A"}},
				[]map[string]interface{}{{"sync_key": "A"}},
			)
			var root map[string]interface{}
			if err := json.Unmarshal(recorder.Body.Bytes(), &root); err != nil {
				t.Fatal(err)
			}
			mutate(root)
			body, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseExcelPricingSnapshotPage(body, source, 1); err == nil {
				t.Fatal("unsafe metadata was accepted")
			}
		})
	}
}

func TestExcelPricingSnapshotRejectsDuplicateMissingAndAmbiguousRowIdentity(t *testing.T) {
	tests := map[string][]map[string]interface{}{
		"duplicate sync key": {
			{"sync_key": "A"},
			{"sync_key": "A"},
		},
		"missing sync key": {
			{"name": "missing"},
		},
		"ambiguous status": {
			{"sync_key": "A", "reconciliation_status": "ambiguous"},
		},
	}
	for name, rows := range tests {
		t.Run(name, func(t *testing.T) {
			source := excelPricingStateSourceForTest()
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeExcelPricingSnapshotRemotePage(
					t, w, source, excelPricingRevisionForTest("bad-row-state"),
					excelPricingRevisionForTest("bad-row-dataset"), 1, 1, rows, rows,
				)
			}))
			defer remote.Close()
			server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
			token := openExcelPricingSession(t, server)
			requestID := "snapshot-bad-row-" + strings.ReplaceAll(name, " ", "-")
			request := authenticatedExcelPricingRequest(
				http.MethodPost, "/api/pricing-sync/snapshots",
				validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
			)
			request.Header.Set("Idempotency-Key", requestID)
			response := httptest.NewRecorder()
			server.router.ServeHTTP(response, request)
			jobID := excelPricingSnapshotJobIDForTest(t, response.Body.Bytes())
			status := waitForExcelPricingSnapshotStatus(t, server, token, jobID, "failed")
			if status["code"] != "snapshot_integrity_failed" {
				t.Fatalf("bad row status=%#v", status)
			}
		})
	}
}

func TestExcelPricingSnapshotIdempotencyConflictAndOwnerIsolation(t *testing.T) {
	source := excelPricingStateSourceForTest()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeExcelPricingSnapshotRemotePage(
			t, w, source, excelPricingRevisionForTest("isolation-state"),
			excelPricingRevisionForTest("isolation-dataset"), 1, 1,
			[]map[string]interface{}{{"sync_key": "A", "reconciliation_status": "matched"}},
			[]map[string]interface{}{{"sync_key": "A", "reconciliation_status": "matched"}},
		)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	ownerToken := openExcelPricingSession(t, server)
	requestID := "snapshot-isolation-test-0001"
	start := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), ownerToken,
	)
	start.Header.Set("Idempotency-Key", requestID)
	startResponse := httptest.NewRecorder()
	server.router.ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("start status=%d: %s", startResponse.Code, startResponse.Body.String())
	}
	jobID := excelPricingSnapshotJobIDForTest(t, startResponse.Body.Bytes())
	waitForExcelPricingSnapshotStatus(t, server, ownerToken, jobID, "ready")

	conflict := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 30), ownerToken,
	)
	conflict.Header.Set("Idempotency-Key", requestID)
	conflictResponse := httptest.NewRecorder()
	server.router.ServeHTTP(conflictResponse, conflict)
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("idempotency conflict status=%d: %s", conflictResponse.Code, conflictResponse.Body.String())
	}

	otherToken := openExcelPricingSession(t, server)
	otherStatus := authenticatedExcelPricingRequest(
		http.MethodGet, "/api/pricing-sync/snapshots/"+jobID, "", otherToken,
	)
	otherResponse := httptest.NewRecorder()
	server.router.ServeHTTP(otherResponse, otherStatus)
	if otherResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-session status=%d, want 404", otherResponse.Code)
	}
}

func TestExcelPricingSnapshotEventStreamReplaysCreationAndStateChanges(t *testing.T) {
	source := excelPricingStateSourceForTest()
	stateRevision := excelPricingRevisionForTest("event-state")
	row := map[string]interface{}{
		"sync_key":              "event-product",
		"reconciliation_status": "matched",
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeExcelPricingSnapshotRemotePage(
			t, w, source, stateRevision, excelPricingRevisionForTest("event-dataset"),
			1, 1, []map[string]interface{}{row}, []map[string]interface{}{row},
		)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	requestID := "snapshot-events-test-0001"
	start := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
	)
	start.Header.Set("Idempotency-Key", requestID)
	startResponse := httptest.NewRecorder()
	server.router.ServeHTTP(startResponse, start)
	jobID := excelPricingSnapshotJobIDForTest(t, startResponse.Body.Bytes())
	waitForExcelPricingSnapshotStatus(t, server, token, jobID, "ready")

	// Publish after job creation but before subscription. The first stream read
	// must replay both current job state and retained state-change events.
	server.notifyExcelPricingSourceChanged("local-change-token")
	loopback := httptest.NewServer(server.router)
	defer loopback.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, loopback.URL+"/api/pricing-sync/snapshots/"+jobID, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(excelPricingClientHeader, excelPricingClientID)
	request.Header.Set(excelPricingCSRFHeader, token)
	request.Header.Set("Accept", "text/event-stream")
	response, err := loopback.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		!strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("event stream status=%d headers=%v", response.StatusCode, response.Header)
	}

	foundCurrent, foundSourceChange := false, false
	seenSequences := make(map[uint64]struct{})
	lastSequence := uint64(0)
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event excelPricingSnapshotEventEnvelope
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event.Schema != excelPricingSnapshotEventSchema || event.Sequence == 0 {
			t.Fatalf("invalid event envelope=%+v", event)
		}
		if _, duplicate := seenSequences[event.Sequence]; duplicate {
			t.Fatalf("duplicate SSE event id=%d", event.Sequence)
		}
		if event.Sequence <= lastSequence {
			t.Fatalf("non-monotonic SSE event id=%d after %d", event.Sequence, lastSequence)
		}
		seenSequences[event.Sequence] = struct{}{}
		lastSequence = event.Sequence
		if event.Kind == "snapshot_ready" && event.Change != nil &&
			event.Change.StateRevision == stateRevision && event.Change.Verified && !event.Change.Stale {
			foundCurrent = true
		}
		if event.Kind == "source_changed" && event.Change != nil &&
			event.Change.SourceChangeToken == "local-change-token" && event.Change.Stale {
			foundSourceChange = true
		}
		if foundCurrent && foundSourceChange {
			cancel()
			break
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !foundCurrent || !foundSourceChange {
		t.Fatalf("event replay current=%v source_change=%v", foundCurrent, foundSourceChange)
	}
}

func TestExcelPricingSnapshotEventStreamSurvivesExpiryWithAttachedWatcher(t *testing.T) {
	source := excelPricingStateSourceForTest()
	stateRevision := excelPricingRevisionForTest("watcher-expiry-state")
	row := map[string]interface{}{
		"sync_key":              "watcher-expiry-product",
		"reconciliation_status": "matched",
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeExcelPricingSnapshotRemotePage(
			t, w, source, stateRevision, excelPricingRevisionForTest("watcher-expiry-dataset"),
			1, 1, []map[string]interface{}{row}, []map[string]interface{}{row},
		)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	requestID := "snapshot-watcher-expiry-test-0001"
	start := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
	)
	start.Header.Set("Idempotency-Key", requestID)
	startResponse := httptest.NewRecorder()
	server.router.ServeHTTP(startResponse, start)
	jobID := excelPricingSnapshotJobIDForTest(t, startResponse.Body.Bytes())
	waitForExcelPricingSnapshotStatus(t, server, token, jobID, "ready")

	loopback := httptest.NewServer(server.router)
	streamContext, cancelStream := context.WithTimeout(context.Background(), 2*time.Second)
	request, err := http.NewRequestWithContext(
		streamContext, http.MethodGet,
		loopback.URL+"/api/pricing-sync/snapshots/"+jobID, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(excelPricingClientHeader, excelPricingClientID)
	request.Header.Set(excelPricingCSRFHeader, token)
	request.Header.Set("Accept", "text/event-stream")
	response, err := loopback.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}

	server.excelPricing.snapshots.mu.Lock()
	job := server.excelPricing.snapshots.jobs[jobID]
	if job == nil || job.eventWatchers != 1 {
		server.excelPricing.snapshots.mu.Unlock()
		t.Fatalf("attached watcher job=%+v", job)
	}
	job.snapshot.expiresAt = server.excelPricing.snapshots.now().UTC().Add(-time.Second)
	nextStateRevision := excelPricingRevisionForTest("watcher-expiry-next-state")
	change := excelPricingStateChangeEvent{
		Kind:          "pricing_state_changed",
		Reason:        "pricing_apply_verified",
		StateRevision: nextStateRevision,
		Stale:         true,
		Verified:      true,
	}
	bindExcelPricingPreviousState(&change, server.excelPricing.snapshots.lastVerifiedChangeLocked())
	server.excelPricing.snapshots.publishChangeLocked(change)
	server.excelPricing.snapshots.mu.Unlock()

	foundExpired, foundLaterEvent := false, false
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event excelPricingSnapshotEventEnvelope
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event.Kind == "snapshot_job" && event.Job["status"] == "expired" {
			foundExpired = true
		}
		if event.Kind == "pricing_state_changed" && event.Change != nil {
			foundLaterEvent = true
		}
		if foundExpired && foundLaterEvent {
			break
		}
	}
	cancelStream()
	_ = response.Body.Close()
	loopback.Close()
	if !foundExpired || !foundLaterEvent {
		t.Fatalf("stream after expiry expired=%v later_event=%v", foundExpired, foundLaterEvent)
	}
	server.excelPricing.snapshots.mu.Lock()
	job = server.excelPricing.snapshots.jobs[jobID]
	server.excelPricing.snapshots.mu.Unlock()
	if job == nil {
		t.Fatal("attached stream job was pruned at expiry")
	}
}

func TestExcelPricingSnapshotEventStreamSignalsRetainedHistoryGap(t *testing.T) {
	source := excelPricingStateSourceForTest()
	row := map[string]interface{}{
		"sync_key":              "event-gap-product",
		"reconciliation_status": "matched",
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeExcelPricingSnapshotRemotePage(
			t, w, source, excelPricingRevisionForTest("event-gap-state"),
			excelPricingRevisionForTest("event-gap-dataset"), 1, 1,
			[]map[string]interface{}{row}, []map[string]interface{}{row},
		)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	requestID := "snapshot-event-gap-test-0001"
	start := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
	)
	start.Header.Set("Idempotency-Key", requestID)
	startResponse := httptest.NewRecorder()
	server.router.ServeHTTP(startResponse, start)
	jobID := excelPricingSnapshotJobIDForTest(t, startResponse.Body.Bytes())
	waitForExcelPricingSnapshotStatus(t, server, token, jobID, "ready")

	store := server.excelPricing.snapshots
	store.mu.Lock()
	for index := 0; index < excelPricingSnapshotEventHistory+4; index++ {
		store.publishChangeLocked(excelPricingStateChangeEvent{
			Kind:   "catalog_changed",
			Reason: "test_history_advance_" + strconv.Itoa(index),
			Stale:  true,
		})
	}
	if store.droppedEventSequence == 0 {
		store.mu.Unlock()
		t.Fatal("event history did not advance past retention")
	}
	store.mu.Unlock()

	loopback := httptest.NewServer(server.router)
	streamContext, cancelStream := context.WithTimeout(context.Background(), 2*time.Second)
	request, err := http.NewRequestWithContext(
		streamContext, http.MethodGet,
		loopback.URL+"/api/pricing-sync/snapshots/"+jobID, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(excelPricingClientHeader, excelPricingClientID)
	request.Header.Set(excelPricingCSRFHeader, token)
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Last-Event-ID", "1")
	response, err := loopback.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	foundGap := false
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event excelPricingSnapshotEventEnvelope
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event.Kind == "replay_required" && event.Change != nil && event.Change.Stale {
			foundGap = true
			break
		}
	}
	cancelStream()
	_ = response.Body.Close()
	loopback.Close()
	if !foundGap {
		t.Fatal("retained-history gap did not require a fresh snapshot")
	}
}

func TestExcelPricingSnapshotStartAdvertisesDurableAndJobEventStreams(t *testing.T) {
	source := excelPricingStateSourceForTest()
	row := map[string]interface{}{
		"sync_key":              "event-contract-product",
		"reconciliation_status": "matched",
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeExcelPricingSnapshotRemotePage(
			t, w, source, excelPricingRevisionForTest("event-contract-state"),
			excelPricingRevisionForTest("event-contract-dataset"), 1, 1,
			[]map[string]interface{}{row}, []map[string]interface{}{row},
		)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	requestID := "snapshot-event-contract-test-0001"
	request := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
	)
	request.Header.Set("Idempotency-Key", requestID)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("start status=%d: %s", response.Code, response.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	jobID, _ := body["job_id"].(string)
	if body["events_url"] != "/api/pricing-sync/events" ||
		body["job_events_url"] != "/api/pricing-sync/snapshots/"+jobID ||
		body["events_lifecycle"] != "session_scoped_durable" ||
		body["job_events_lifecycle"] != "job_scoped_progress" ||
		body["events_keepalive_seconds"] != float64(15) {
		t.Fatalf("event contract=%#v", body)
	}
}

func TestExcelPricingDurableEventsReplaysLatestAndSurvivesJobPrune(t *testing.T) {
	server := newExcelPricingTestServer(t, "http://127.0.0.1:1/wp-json/digitalogic/patris/product-sync")
	registerExcelPricingDurableEventsRouteForTest(server)
	token := openExcelPricingSession(t, server)
	store := server.excelPricing.snapshots
	store.mu.Lock()
	store.publishChangeLocked(excelPricingStateChangeEvent{
		Kind:   "catalog_changed",
		Reason: "older_semantic_event",
		Stale:  true,
	})
	latestSequence := store.publishChangeLocked(excelPricingStateChangeEvent{
		Kind:   "pricing_state_changed",
		Reason: "latest_semantic_event",
		Stale:  true,
	})
	store.mu.Unlock()

	loopback := httptest.NewServer(server.router)
	defer loopback.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		loopback.URL+"/api/pricing-sync/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(excelPricingClientHeader, excelPricingClientID)
	request.Header.Set(excelPricingCSRFHeader, token)
	request.Header.Set("Accept", "text/event-stream")
	response, err := loopback.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("durable stream status=%d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	first := readExcelPricingSnapshotSSEEventForTest(t, scanner)
	if first.Sequence != latestSequence || first.Kind != "pricing_state_changed" {
		t.Fatalf("initial durable event=%+v", first)
	}

	// Durable delivery has no job watcher or job-retention dependency.
	store.mu.Lock()
	store.jobs = make(map[string]*excelPricingSnapshotJob)
	store.idempotency = make(map[string]string)
	nextSequence := store.publishChangeLocked(excelPricingStateChangeEvent{
		Kind:   "source_changed",
		Reason: "after_job_prune",
		Stale:  true,
	})
	store.mu.Unlock()
	second := readExcelPricingSnapshotSSEEventForTest(t, scanner)
	if second.Sequence != nextSequence || second.Kind != "source_changed" {
		t.Fatalf("post-prune durable event=%+v", second)
	}
}

func TestExcelPricingDurableEventsDisconnectNeverCancelsSnapshotJob(t *testing.T) {
	source := excelPricingStateSourceForTest()
	started := make(chan struct{})
	remoteCancelled := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(remoteCancelled)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	registerExcelPricingDurableEventsRouteForTest(server)
	token := openExcelPricingSession(t, server)
	requestID := "snapshot-durable-disconnect-test-0001"
	start := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
	)
	start.Header.Set("Idempotency-Key", requestID)
	startResponse := httptest.NewRecorder()
	server.router.ServeHTTP(startResponse, start)
	jobID := excelPricingSnapshotJobIDForTest(t, startResponse.Body.Bytes())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("remote build did not start")
	}

	loopback := httptest.NewServer(server.router)
	streamContext, cancelStream := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(streamContext, http.MethodGet,
		loopback.URL+"/api/pricing-sync/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(excelPricingClientHeader, excelPricingClientID)
	request.Header.Set(excelPricingCSRFHeader, token)
	request.Header.Set("Accept", "text/event-stream")
	response, err := loopback.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = readExcelPricingSnapshotSSEEventForTest(t, bufio.NewScanner(response.Body))
	cancelStream()
	_ = response.Body.Close()
	loopback.Close()

	select {
	case <-remoteCancelled:
		t.Fatal("durable stream disconnect cancelled snapshot work")
	case <-time.After(50 * time.Millisecond):
	}
	store := server.excelPricing.snapshots
	store.mu.Lock()
	job := store.jobs[jobID]
	var jobCopy *excelPricingSnapshotJob
	if job != nil {
		copyJob := *job
		jobCopy = &copyJob
	}
	store.mu.Unlock()
	if jobCopy == nil || jobCopy.status != "running" {
		t.Fatalf("job after durable disconnect=%+v", jobCopy)
	}

	cancel := authenticatedExcelPricingRequest(
		http.MethodDelete, "/api/pricing-sync/snapshots/"+jobID, "", token,
	)
	cancelResponse := httptest.NewRecorder()
	server.router.ServeHTTP(cancelResponse, cancel)
	select {
	case <-remoteCancelled:
	case <-time.After(time.Second):
		t.Fatal("explicit cancellation did not reach remote work")
	}
}

func TestExcelPricingDurableEventsNoCursorReplayIsSessionIsolated(t *testing.T) {
	server := newExcelPricingTestServer(t, "http://127.0.0.1:1/wp-json/digitalogic/patris/product-sync")
	registerExcelPricingDurableEventsRouteForTest(server)
	loopback := httptest.NewServer(server.router)
	defer loopback.Close()
	readInitial := func(token string) excelPricingSnapshotEventEnvelope {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet,
			loopback.URL+"/api/pricing-sync/events", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set(excelPricingClientHeader, excelPricingClientID)
		request.Header.Set(excelPricingCSRFHeader, token)
		request.Header.Set("Accept", "text/event-stream")
		response, err := loopback.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return readExcelPricingSnapshotSSEEventForTest(t, bufio.NewScanner(response.Body))
	}
	first := readInitial(openExcelPricingSession(t, server))
	second := readInitial(openExcelPricingSession(t, server))
	if first.Kind != "replay_required" || first.Change == nil ||
		first.Change.Reason != "initial_state_unavailable" ||
		second.Kind != "replay_required" || second.Change == nil ||
		second.Change.Reason != "initial_state_unavailable" || first.Sequence == second.Sequence {
		t.Fatalf("session replay first=%+v second=%+v", first, second)
	}
}

func TestExcelPricingDurableEventsSignalsPastAndAheadCursors(t *testing.T) {
	server := newExcelPricingTestServer(t, "http://127.0.0.1:1/wp-json/digitalogic/patris/product-sync")
	registerExcelPricingDurableEventsRouteForTest(server)
	token := openExcelPricingSession(t, server)
	store := server.excelPricing.snapshots
	store.mu.Lock()
	for index := 0; index < excelPricingSnapshotEventHistory+4; index++ {
		store.publishChangeLocked(excelPricingStateChangeEvent{
			Kind:   "catalog_changed",
			Reason: "durable_history_" + strconv.Itoa(index),
			Stale:  true,
		})
	}
	ahead := store.eventSequence + 100
	store.mu.Unlock()

	loopback := httptest.NewServer(server.router)
	defer loopback.Close()
	readForCursor := func(cursor string) excelPricingSnapshotEventEnvelope {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet,
			loopback.URL+"/api/pricing-sync/events", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set(excelPricingClientHeader, excelPricingClientID)
		request.Header.Set(excelPricingCSRFHeader, token)
		request.Header.Set("Accept", "text/event-stream")
		request.Header.Set("Last-Event-ID", cursor)
		response, err := loopback.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return readExcelPricingSnapshotSSEEventForTest(t, bufio.NewScanner(response.Body))
	}
	past := readForCursor("0")
	if past.Kind != "replay_required" || past.Change == nil ||
		past.Change.Reason != "cursor_expired" {
		t.Fatalf("past cursor event=%+v", past)
	}
	future := readForCursor(strconv.FormatUint(ahead, 10))
	if future.Kind != "replay_required" || future.Change == nil ||
		future.Change.Reason != "cursor_ahead" {
		t.Fatalf("ahead cursor event=%+v", future)
	}
}

func TestExcelPricingSnapshotEventCursorIsStrictlyNumeric(t *testing.T) {
	for _, value := range []string{"0", "1", "18446744073709551615"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("Last-Event-ID", value)
		if _, present, err := excelPricingSnapshotEventCursor(request); err != nil || !present {
			t.Fatalf("valid cursor %q present=%v err=%v", value, present, err)
		}
	}
	for _, value := range []string{"+1", "-1", " 1", "1 ", "1.0", "1,2", "abc", "18446744073709551616"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header["Last-Event-Id"] = []string{value}
		if _, _, err := excelPricingSnapshotEventCursor(request); err == nil {
			t.Fatalf("invalid cursor %q accepted", value)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header["Last-Event-Id"] = []string{"1", "2"}
	if _, _, err := excelPricingSnapshotEventCursor(request); err == nil {
		t.Fatal("multiple Last-Event-ID headers accepted")
	}
}

func TestExcelPricingSnapshotSourceChangeCancelsRunningBuildAndInvalidatesPayload(t *testing.T) {
	source := excelPricingStateSourceForTest()
	stateRevision := excelPricingRevisionForTest("source-change-state")
	datasetRevision := excelPricingRevisionForTest("source-change-dataset")
	row := map[string]interface{}{
		"sync_key":              "source-change-product",
		"reconciliation_status": "matched",
	}
	secondStarted := make(chan struct{})
	secondCancelled := make(chan struct{})
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeExcelPricingSnapshotRemotePage(
				t, w, source, stateRevision, datasetRevision, 1, 1,
				[]map[string]interface{}{row}, []map[string]interface{}{row},
			)
			return
		}
		close(secondStarted)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(secondCancelled)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	startJob := func(requestID string) string {
		request := authenticatedExcelPricingRequest(
			http.MethodPost, "/api/pricing-sync/snapshots",
			validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
		)
		request.Header.Set("Idempotency-Key", requestID)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("start status=%d: %s", response.Code, response.Body.String())
		}
		return excelPricingSnapshotJobIDForTest(t, response.Body.Bytes())
	}

	lastGoodJobID := startJob("snapshot-source-change-last-good-0001")
	lastGood := waitForExcelPricingSnapshotStatus(t, server, token, lastGoodJobID, "ready")
	if lastGood["etag"] == "" || lastGood["state_revision"] != stateRevision {
		t.Fatalf("last-good status=%#v", lastGood)
	}
	runningJobID := startJob("snapshot-source-change-running-0001")
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second remote build did not start")
	}

	loopback := httptest.NewServer(server.router)
	streamContext, cancelStream := context.WithTimeout(context.Background(), 2*time.Second)
	request, err := http.NewRequestWithContext(
		streamContext, http.MethodGet,
		loopback.URL+"/api/pricing-sync/snapshots/"+runningJobID, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(excelPricingClientHeader, excelPricingClientID)
	request.Header.Set(excelPricingCSRFHeader, token)
	request.Header.Set("Accept", "text/event-stream")
	response, err := loopback.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}

	server.notifyExcelPricingSourceChanged("changed-file-token")
	foundInvalidation := false
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event excelPricingSnapshotEventEnvelope
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event.Kind != "source_changed" || event.Change == nil {
			continue
		}
		change := event.Change
		if !change.Stale || change.Verified || change.SourceChangeToken != "changed-file-token" ||
			change.Source != nil || change.CatalogRevision != "" ||
			change.StateRevision != "" || change.ETag != "" ||
			change.PreviousSource == nil || *change.PreviousSource != source ||
			change.PreviousCatalogRevision != datasetRevision ||
			change.PreviousStateRevision != stateRevision || change.PreviousETag == "" {
			t.Fatalf("source invalidation=%+v", change)
		}
		foundInvalidation = true
		break
	}
	cancelStream()
	_ = response.Body.Close()
	loopback.Close()
	if !foundInvalidation {
		t.Fatal("source invalidation was not delivered before import")
	}
	select {
	case <-secondCancelled:
	case <-time.After(time.Second):
		t.Fatal("source change did not cancel the in-flight remote build")
	}
	terminal := waitForExcelPricingSnapshotStatus(t, server, token, runningJobID, "cancelled")
	if terminal["code"] != "snapshot_source_changed" {
		t.Fatalf("source-change terminal=%#v", terminal)
	}
	payload := authenticatedExcelPricingRequest(
		http.MethodGet, "/api/pricing-sync/snapshots/"+lastGoodJobID+"/payload", "", token,
	)
	payloadResponse := httptest.NewRecorder()
	server.router.ServeHTTP(payloadResponse, payload)
	if payloadResponse.Code != http.StatusGone ||
		!strings.Contains(payloadResponse.Body.String(), "snapshot_invalidated") {
		t.Fatalf("invalidated payload=%d: %s", payloadResponse.Code, payloadResponse.Body.String())
	}
}

func TestExcelPricingSnapshotPricingInvalidationCancelsLeaderAndFollower(t *testing.T) {
	source := excelPricingStateSourceForTest()
	started := make(chan struct{}, 1)
	remoteCancelled := make(chan struct{}, 1)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		remoteCancelled <- struct{}{}
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	startJob := func(requestID string) string {
		request := authenticatedExcelPricingRequest(
			http.MethodPost, "/api/pricing-sync/snapshots",
			validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
		)
		request.Header.Set("Idempotency-Key", requestID)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("start status=%d: %s", response.Code, response.Body.String())
		}
		return excelPricingSnapshotJobIDForTest(t, response.Body.Bytes())
	}
	leaderID := startJob("snapshot-pricing-race-leader-0001")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("remote build did not start")
	}
	followerID := startJob("snapshot-pricing-race-follower-0001")
	store := server.excelPricing.snapshots
	store.mu.Lock()
	initialGeneration := store.generation
	store.mu.Unlock()
	stateRevision := excelPricingRevisionForTest("pricing-race-next-state")
	store.publishPricingStateInvalidated(stateRevision)
	select {
	case <-remoteCancelled:
	case <-time.After(time.Second):
		t.Fatal("pricing invalidation did not cancel remote work")
	}
	leader := waitForExcelPricingSnapshotStatus(t, server, token, leaderID, "cancelled")
	follower := waitForExcelPricingSnapshotStatus(t, server, token, followerID, "cancelled")
	if leader["code"] != "snapshot_pricing_state_changed" ||
		follower["code"] != "snapshot_pricing_state_changed" {
		t.Fatalf("pricing invalidation leader=%#v follower=%#v", leader, follower)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.generation != initialGeneration+1 || len(store.cache) != 0 {
		t.Fatalf("generation=%d cache=%d", store.generation, len(store.cache))
	}
}

func TestExcelPricingSnapshotGenerationFenceRejectsLateCompletion(t *testing.T) {
	source := excelPricingStateSourceForTest()
	stateRevision := excelPricingRevisionForTest("generation-fence-state")
	datasetRevision := excelPricingRevisionForTest("generation-fence-dataset")
	row := map[string]interface{}{
		"sync_key":              "generation-fence-product",
		"reconciliation_status": "matched",
	}
	started := make(chan struct{})
	release := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		writeExcelPricingSnapshotRemotePage(
			t, w, source, stateRevision, datasetRevision, 1, 1,
			[]map[string]interface{}{row}, []map[string]interface{}{row},
		)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	requestID := "snapshot-generation-fence-test-0001"
	request := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
	)
	request.Header.Set("Idempotency-Key", requestID)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	jobID := excelPricingSnapshotJobIDForTest(t, response.Body.Bytes())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("remote build did not start")
	}
	store := server.excelPricing.snapshots
	store.mu.Lock()
	store.generation++ // Simulate an invalidation racing a non-cooperative response.
	store.mu.Unlock()
	close(release)
	terminal := waitForExcelPricingSnapshotStatus(t, server, token, jobID, "cancelled")
	if terminal["code"] != "snapshot_generation_changed" {
		t.Fatalf("generation terminal=%#v", terminal)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.cache) != 0 || store.jobs[jobID].snapshot != nil {
		t.Fatalf("late completion cached=%d job=%+v", len(store.cache), store.jobs[jobID])
	}
}

func TestExcelPricingSnapshotUpstreamCatalogInvalidationIsAtomic(t *testing.T) {
	server := newExcelPricingTestServer(t, "http://127.0.0.1:1/wp-json/digitalogic/patris/product-sync")
	store := server.excelPricing.snapshots
	source := excelPricingStateSourceForTest()
	previousCatalog := excelPricingRevisionForTest("upstream-previous-catalog")
	previousState := excelPricingRevisionForTest("upstream-previous-state")
	previousSnapshot := excelPricingRevisionForTest("upstream-previous-snapshot")
	previousETag := `"` + excelPricingRevisionForTest("upstream-previous-payload") + `"`
	owner := sha256.Sum256([]byte("upstream-owner"))
	cancelContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.mu.Lock()
	store.publishChangeLocked(excelPricingStateChangeEvent{
		Kind:             "snapshot_ready",
		Source:           &source,
		CatalogRevision:  previousCatalog,
		StateRevision:    previousState,
		SnapshotRevision: previousSnapshot,
		ETag:             previousETag,
		Verified:         true,
	})
	store.jobs["leader"] = &excelPricingSnapshotJob{
		id:              "leader",
		owner:           owner,
		status:          "running",
		startGeneration: store.generation,
		cancel:          cancel,
	}
	store.jobs["follower"] = &excelPricingSnapshotJob{
		id:              "follower",
		owner:           owner,
		status:          "running",
		leaderJobID:     "leader",
		startGeneration: store.generation,
	}
	store.activeJobID = "leader"
	initialGeneration := store.generation
	store.mu.Unlock()

	nextCatalog := excelPricingRevisionForTest("upstream-next-catalog")
	nextState := excelPricingRevisionForTest("upstream-next-state")
	nextETag := `"` + nextState + `"`
	if err := server.notifyExcelPricingRemoteRevisionChanged(excelPricingRemoteRevision{
		Source:          source,
		CatalogRevision: nextCatalog,
		StateRevision:   nextState,
		ETag:            nextETag,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelContext.Done():
	case <-time.After(time.Second):
		t.Fatal("upstream invalidation did not cancel active leader")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.generation != initialGeneration+1 || store.jobs["leader"].status != "cancelling" ||
		store.jobs["follower"].status != "cancelled" || len(store.cache) != 0 {
		t.Fatalf("upstream generation=%d leader=%+v follower=%+v cache=%d",
			store.generation, store.jobs["leader"], store.jobs["follower"], len(store.cache))
	}
	change := store.events[len(store.events)-1].Change
	if change == nil || change.Kind != "catalog_changed" || change.Source == nil ||
		*change.Source != source || change.CatalogRevision != nextCatalog ||
		change.StateRevision != nextState || change.ETag != nextETag ||
		change.SnapshotRevision != "" || change.PreviousCatalogRevision != previousCatalog ||
		change.PreviousStateRevision != previousState ||
		change.PreviousSnapshotRevision != previousSnapshot || change.PreviousETag != previousETag {
		t.Fatalf("upstream semantic event=%+v", change)
	}
}

func TestExcelPricingRemoteRevisionAdvanceIsNotSourceChange(t *testing.T) {
	server := newExcelPricingTestServer(t, "http://127.0.0.1:1/wp-json/digitalogic/patris/product-sync")
	store := server.excelPricing.snapshots
	previousSource := excelPricingStateSourceForTest()
	currentSource := previousSource
	currentSource.Revision = excelPricingRevisionForTest("live-patris-revision-advance")
	catalogRevision := excelPricingRevisionForTest("unchanged-catalog")
	previousState := excelPricingRevisionForTest("previous-pricing-state")
	currentState := excelPricingRevisionForTest("current-pricing-state")
	currentETag := `"` + currentState + `"`

	store.mu.Lock()
	store.publishChangeLocked(excelPricingStateChangeEvent{
		Kind:            "snapshot_ready",
		Source:          &previousSource,
		CatalogRevision: catalogRevision,
		StateRevision:   previousState,
		ETag:            `"` + previousState + `"`,
		Verified:        true,
	})
	store.mu.Unlock()

	if err := server.notifyExcelPricingRemoteRevisionChanged(excelPricingRemoteRevision{
		Source:          currentSource,
		CatalogRevision: catalogRevision,
		StateRevision:   currentState,
		ETag:            currentETag,
	}); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	change := store.events[len(store.events)-1].Change
	if change == nil || change.Kind != "pricing_state_changed" || change.Source == nil ||
		*change.Source != currentSource || change.StateRevision != currentState {
		t.Fatalf("live revision advance event=%+v", change)
	}
}

func TestExcelPricingRemoteWebsiteCommitPrefersFastConfirmationOverDerivedCatalogChange(t *testing.T) {
	server := newExcelPricingTestServer(t, "http://127.0.0.1:1/wp-json/digitalogic/patris/product-sync")
	store := server.excelPricing.snapshots
	source := excelPricingStateSourceForTest()
	previousState := excelPricingRevisionForTest("website-previous-state")
	currentState := excelPricingRevisionForTest("website-current-state")
	previousPricing := excelPricingRevisionForTest("website-previous-pricing")
	currentPricing := excelPricingRevisionForTest("website-current-pricing")

	store.mu.Lock()
	store.publishChangeLocked(excelPricingStateChangeEvent{
		Kind:                 "snapshot_ready",
		Source:               &source,
		CatalogRevision:      excelPricingRevisionForTest("website-previous-catalog"),
		StateRevision:        previousState,
		PricingStateRevision: previousPricing,
		ETag:                 `"` + previousState + `"`,
		Verified:             true,
	})
	store.mu.Unlock()

	if err := server.notifyExcelPricingRemoteRevisionChanged(excelPricingRemoteRevision{
		Source:                source,
		CatalogRevision:       excelPricingRevisionForTest("website-current-catalog"),
		StateRevision:         currentState,
		PricingStateRevision:  currentPricing,
		PricingPolicyRevision: excelPricingRevisionForTest("website-current-policy"),
		ETag:                  `"` + currentState + `"`,
	}); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	change := store.events[len(store.events)-1].Change
	if change == nil || change.Kind != "pricing_state_changed" ||
		change.PricingStateRevision != currentPricing {
		t.Fatalf("website commit event=%+v", change)
	}
}

func TestExcelPricingRemoteCatalogChangeRemainsCatalogChangeWhenPricingStateIsStable(t *testing.T) {
	server := newExcelPricingTestServer(t, "http://127.0.0.1:1/wp-json/digitalogic/patris/product-sync")
	store := server.excelPricing.snapshots
	source := excelPricingStateSourceForTest()
	previousState := excelPricingRevisionForTest("catalog-previous-state")
	currentState := excelPricingRevisionForTest("catalog-current-state")
	pricingState := excelPricingRevisionForTest("catalog-stable-pricing")

	store.mu.Lock()
	store.publishChangeLocked(excelPricingStateChangeEvent{
		Kind:                 "snapshot_ready",
		Source:               &source,
		CatalogRevision:      excelPricingRevisionForTest("catalog-previous-catalog"),
		StateRevision:        previousState,
		PricingStateRevision: pricingState,
		ETag:                 `"` + previousState + `"`,
		Verified:             true,
	})
	store.mu.Unlock()

	if err := server.notifyExcelPricingRemoteRevisionChanged(excelPricingRemoteRevision{
		Source:                source,
		CatalogRevision:       excelPricingRevisionForTest("catalog-current-catalog"),
		StateRevision:         currentState,
		PricingStateRevision:  pricingState,
		PricingPolicyRevision: excelPricingRevisionForTest("catalog-current-policy"),
		ETag:                  `"` + currentState + `"`,
	}); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	change := store.events[len(store.events)-1].Change
	if change == nil || change.Kind != "catalog_changed" {
		t.Fatalf("catalog event=%+v", change)
	}
}

func TestExcelPricingSnapshotWaitDisconnectCancelsRemoteWork(t *testing.T) {
	source := excelPricingStateSourceForTest()
	started := make(chan struct{})
	remoteCancelled := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(remoteCancelled)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	requestID := "snapshot-wait-cancel-test-0001"
	start := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
	)
	start.Header.Set("Idempotency-Key", requestID)
	startResponse := httptest.NewRecorder()
	server.router.ServeHTTP(startResponse, start)
	jobID := excelPricingSnapshotJobIDForTest(t, startResponse.Body.Bytes())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("remote request did not start")
	}

	waitRequest := authenticatedExcelPricingRequest(
		http.MethodGet, "/api/pricing-sync/snapshots/"+jobID+"?wait=terminal", "", token,
	)
	waitContext, cancelWait := context.WithCancel(waitRequest.Context())
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		server.router.ServeHTTP(httptest.NewRecorder(), waitRequest.WithContext(waitContext))
	}()
	time.Sleep(10 * time.Millisecond)
	cancelWait()
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("terminal wait did not observe disconnect")
	}
	select {
	case <-remoteCancelled:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel remote request")
	}
	waitForExcelPricingSnapshotStatus(t, server, token, jobID, "cancelled")
}

func TestExcelPricingSnapshotExpiryWinsOverConditionalETag(t *testing.T) {
	source := excelPricingStateSourceForTest()
	row := map[string]interface{}{
		"sync_key":              "expiry-product",
		"reconciliation_status": "matched",
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeExcelPricingSnapshotRemotePage(
			t, w, source, excelPricingRevisionForTest("expiry-state"),
			excelPricingRevisionForTest("expiry-dataset"), 1, 1,
			[]map[string]interface{}{row}, []map[string]interface{}{row},
		)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	requestID := "snapshot-expiry-test-0001"
	start := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
	)
	start.Header.Set("Idempotency-Key", requestID)
	startResponse := httptest.NewRecorder()
	server.router.ServeHTTP(startResponse, start)
	jobID := excelPricingSnapshotJobIDForTest(t, startResponse.Body.Bytes())
	status := waitForExcelPricingSnapshotStatus(t, server, token, jobID, "ready")

	server.excelPricing.snapshots.mu.Lock()
	server.excelPricing.snapshots.jobs[jobID].snapshot.expiresAt =
		server.excelPricing.snapshots.now().UTC().Add(-time.Second)
	server.excelPricing.snapshots.mu.Unlock()
	payload := authenticatedExcelPricingRequest(
		http.MethodGet, "/api/pricing-sync/snapshots/"+jobID+"/payload", "", token,
	)
	payload.Header.Set("If-None-Match", status["etag"].(string))
	payloadResponse := httptest.NewRecorder()
	server.router.ServeHTTP(payloadResponse, payload)
	if payloadResponse.Code != http.StatusGone ||
		!strings.Contains(payloadResponse.Body.String(), "snapshot_expired") {
		t.Fatalf("expired conditional payload=%d: %s", payloadResponse.Code, payloadResponse.Body.String())
	}
}

func TestExcelPricingSnapshotCoalescedExpectedRevisionIsPerCaller(t *testing.T) {
	source := excelPricingStateSourceForTest()
	stateRevision := excelPricingRevisionForTest("coalesced-expected-state")
	row := map[string]interface{}{
		"sync_key":              "coalesced-expected-product",
		"reconciliation_status": "matched",
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		writeExcelPricingSnapshotRemotePage(
			t, w, source, stateRevision, excelPricingRevisionForTest("coalesced-expected-dataset"),
			1, 1, []map[string]interface{}{row}, []map[string]interface{}{row},
		)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	startJob := func(requestID, expected string) string {
		request := authenticatedExcelPricingRequest(
			http.MethodPost, "/api/pricing-sync/snapshots",
			validExcelPricingSnapshotStartBodyWithExpected(source, requestID, "fa", 0, expected), token,
		)
		request.Header.Set("Idempotency-Key", requestID)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("start status=%d: %s", response.Code, response.Body.String())
		}
		return excelPricingSnapshotJobIDForTest(t, response.Body.Bytes())
	}
	leaderID := startJob("snapshot-expected-leader-0001", excelPricingRevisionForTest("stale-expected"))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("remote request did not start")
	}
	followerID := startJob("snapshot-expected-follower-0001", stateRevision)
	close(release)
	leader := waitForExcelPricingSnapshotStatus(t, server, token, leaderID, "failed")
	follower := waitForExcelPricingSnapshotStatus(t, server, token, followerID, "ready")
	if leader["code"] != "snapshot_state_revision_mismatch" ||
		follower["state_revision"] != stateRevision || calls.Load() != 1 {
		t.Fatalf("leader=%#v follower=%#v calls=%d", leader, follower, calls.Load())
	}
}

func validExcelPricingSnapshotStartBody(
	source canonical.Source,
	requestID, locale string,
	maxAgeSeconds int,
) string {
	return validExcelPricingSnapshotStartBodyWithExpected(source, requestID, locale, maxAgeSeconds, "")
}

func validExcelPricingSnapshotStartBodyWithExpected(
	source canonical.Source,
	requestID, locale string,
	maxAgeSeconds int,
	expectedStateRevision string,
) string {
	return validExcelPricingSnapshotStartBodyWithProjection(
		source, requestID, locale, maxAgeSeconds, expectedStateRevision, "",
	)
}

func validExcelPricingSnapshotStartBodyWithProjection(
	source canonical.Source,
	requestID, locale string,
	maxAgeSeconds int,
	expectedStateRevision, projection string,
) string {
	payload := excelPricingSnapshotStartRequest{
		Schema:                excelPricingSnapshotRequestSchema,
		SchemaVersion:         1,
		ClientID:              excelPricingContractClientID,
		Channel:               excelPricingContractChannel,
		RequestID:             requestID,
		Source:                source,
		Locale:                locale,
		Projection:            projection,
		MaxAgeSeconds:         maxAgeSeconds,
		ExpectedStateRevision: expectedStateRevision,
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func excelPricingSnapshotJobIDForTest(t *testing.T, body []byte) string {
	t.Helper()
	var response struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.JobID == "" {
		t.Fatalf("snapshot response has no job_id: %s", body)
	}
	return response.JobID
}

func waitForExcelPricingSnapshotStatus(
	t *testing.T,
	server *Server,
	token, jobID, wanted string,
) map[string]interface{} {
	t.Helper()
	request := authenticatedExcelPricingRequest(
		http.MethodGet, "/api/pricing-sync/snapshots/"+jobID+"?wait=terminal", "", token,
	)
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request.WithContext(ctx))
	if response.Code != http.StatusOK {
		t.Fatalf("terminal wait=%d: %s", response.Code, response.Body.String())
	}
	var status map[string]interface{}
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["status"] != wanted {
		t.Fatalf("snapshot reached terminal status %#v, want %q", status, wanted)
	}
	return status
}

func registerExcelPricingDurableEventsRouteForTest(server *Server) {
	server.router.HandleFunc(
		"/api/pricing-sync/events", server.handleGetExcelPricingEvents,
	).Methods(http.MethodGet)
}

func readExcelPricingSnapshotSSEEventForTest(
	t *testing.T,
	scanner *bufio.Scanner,
) excelPricingSnapshotEventEnvelope {
	t.Helper()
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event excelPricingSnapshotEventEnvelope
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		return event
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatal("SSE stream ended before a data event")
	return excelPricingSnapshotEventEnvelope{}
}

func writeExcelPricingSnapshotRemotePage(
	t *testing.T,
	w http.ResponseWriter,
	source canonical.Source,
	stateRevision, datasetRevision string,
	page, pages int,
	allRows, pageRows []map[string]interface{},
) {
	t.Helper()
	counts := excelPricingSnapshotCountsForTest(allRows)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"schema":         excelPricingStateSchema,
		"state_revision": stateRevision,
		"status":         "ready",
		"warnings":       []interface{}{},
		"source": map[string]interface{}{
			"id":                       source.ID,
			"dataset":                  source.Dataset,
			"current_revision":         source.Revision,
			"submitted_revision":       source.Revision,
			"revision_matches_current": true,
		},
		"catalog": map[string]interface{}{
			"dataset":          "reconciled_products",
			"dataset_revision": datasetRevision,
			"page_revision":    excelPricingRevisionForTest("snapshot-page-" + strconv.Itoa(page)),
			"columns": []map[string]interface{}{
				{"key": "sync_key"},
				{"key": "reconciliation_status"},
				{"key": "name"},
			},
			"reconciliation": map[string]interface{}{
				"source": map[string]interface{}{
					"id":       source.ID,
					"dataset":  source.Dataset,
					"revision": source.Revision,
				},
				"counts": counts,
			},
			"rows": pageRows,
			"pagination": map[string]interface{}{
				"page":     page,
				"limit":    excelPricingSnapshotPageSize,
				"total":    len(allRows),
				"pages":    pages,
				"has_more": page < pages,
			},
		},
	})
}

func excelPricingSnapshotCountsForTest(rows []map[string]interface{}) map[string]interface{} {
	matched, patrisOnly, wooOnly := 0, 0, 0
	for _, row := range rows {
		switch row["reconciliation_status"] {
		case "patris_only":
			patrisOnly++
		case "woo_only":
			wooOnly++
		default:
			matched++
		}
	}
	return map[string]interface{}{
		"patris_products":           matched + patrisOnly,
		"woocommerce_raw":           matched + wooOnly,
		"woocommerce_leaves":        matched + wooOnly,
		"union_rows":                len(rows),
		"matched":                   matched,
		"patris_only":               patrisOnly,
		"woo_only":                  wooOnly,
		"ambiguous_codes":           0,
		"variable_parents_excluded": 0,
	}
}

func excelPricingSnapshotFullRowForTest(index int) map[string]interface{} {
	id := strconv.Itoa(100000 + index)
	return map[string]interface{}{
		"sync_key":                       "woo:" + id,
		"reconciliation_status":          "matched",
		"patris_code":                    "PATRIS-" + id,
		"woocommerce_id":                 id,
		"parent_id":                      0,
		"product_type":                   "simple",
		"publication_status":             "publish",
		"name":                           "Digitalogic precision electronic component " + id,
		"part_number":                    "PART-" + id,
		"sku":                            "SKU-" + id,
		"categories":                     "Integrated Circuits > Power Management",
		"category_ids":                   []int{12, 34, 56},
		"currency":                       "IRR",
		"regular_price":                  1840000 + index,
		"sale_price":                     1790000 + index,
		"effective_price":                1790000 + index,
		"patris_final_price":             1775000 + index,
		"price_status":                   "available",
		"stock_quantity":                 25 + index%10,
		"stock_status":                   "instock",
		"patris_total_stock":             30 + index%10,
		"patris_minimum_stock":           2,
		"patris_location":                "A-" + strconv.Itoa(index%50),
		"weight_grams":                   12.5,
		"woocommerce_weight":             "0.0125",
		"woocommerce_weight_unit":        "kg",
		"foreign_price":                  3.75,
		"foreign_currency":               "CNY",
		"partner_price_irr":              1700000 + index,
		"price_source_amount":            3.75,
		"price_source_currency":          "CNY",
		"price_source_kind":              "patris_foreign",
		"price_rounding_digits":          2,
		"price_rounding_mode":            "nearest_half_up",
		"shipping_method_id":             "air-express",
		"shipping_method_name_en":        "International priority air express",
		"shipping_method_name_fa":        "حمل هوایی سریع بین المللی",
		"shipping_price_per_kg":          120,
		"shipping_price_per_kg_currency": "CNY",
		"profit_margin_percent":          30,
		"permalink":                      "https://digitalogic.ir/product/component-" + id + "/",
		"image_url":                      "https://digitalogic.ir/wp-content/uploads/catalog/2026/08/component-high-resolution-" + id + ".webp",
		"updated_at":                     "2026-08-16T00:00:00Z",
		"sync_status":                    "current",
		"sync_error":                     "",
		"record_revision":                excelPricingRevisionForTest("record-" + id),
	}
}

func TestExcelPricingSnapshotCancelPropagatesContext(t *testing.T) {
	source := excelPricingStateSourceForTest()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(cancelled)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	token := openExcelPricingSession(t, server)
	requestID := "snapshot-cancel-test-0001"
	start := authenticatedExcelPricingRequest(
		http.MethodPost, "/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 0), token,
	)
	start.Header.Set("Idempotency-Key", requestID)
	startResponse := httptest.NewRecorder()
	server.router.ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("start status=%d: %s", startResponse.Code, startResponse.Body.String())
	}
	jobID := excelPricingSnapshotJobIDForTest(t, startResponse.Body.Bytes())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("remote request did not start")
	}
	cancel := authenticatedExcelPricingRequest(
		http.MethodDelete, "/api/pricing-sync/snapshots/"+jobID, "", token,
	)
	cancelResponse := httptest.NewRecorder()
	server.router.ServeHTTP(cancelResponse, cancel)
	if cancelResponse.Code != http.StatusAccepted {
		t.Fatalf("cancel status=%d: %s", cancelResponse.Code, cancelResponse.Body.String())
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("remote context was not cancelled")
	}
	status := waitForExcelPricingSnapshotStatus(t, server, token, jobID, "cancelled")
	if status["code"] != "request_cancelled" {
		t.Fatalf("cancel status=%#v", status)
	}
}

func TestExcelPricingSnapshotNormalizesRemoteSnapshotDrift(t *testing.T) {
	if got := excelPricingSnapshotFailureCode(" DIGITALOGIC_RECONCILED_SNAPSHOT_CHANGED "); got != "snapshot_revision_changed" {
		t.Fatalf("normalized snapshot drift = %q", got)
	}
	if got := excelPricingSnapshotFailureCode("remote_unavailable"); got != "remote_unavailable" {
		t.Fatalf("unrelated failure code changed to %q", got)
	}
}
