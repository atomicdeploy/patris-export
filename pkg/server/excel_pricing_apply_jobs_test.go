package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

type excelPricingRoundTripFunc func(*http.Request) (*http.Response, error)

func (function excelPricingRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestExcelPricingApplyLostAdmissionResponseRecoversByOriginalRequestOnly(t *testing.T) {
	source := excelPricingStateSourceForTest()
	stateRevision := excelPricingRevisionForTest("lost-response-state")
	previewDigest := excelPricingRevisionForTest("lost-response-preview")
	requestID := "excel-apply-lost-response-0001"
	jobID := "currency-11111111111111111111111111111111"
	server := newExcelPricingTestServer(
		t,
		"http://127.0.0.1:18081/wp-json/digitalogic/patris/product-sync",
	)
	server.excelPricing.canonical = canonicalProjectionSequence(source)
	var postCalls atomic.Int32
	var getCalls atomic.Int32
	server.excelPricing.client = &http.Client{Transport: excelPricingRoundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			switch request.Method {
			case http.MethodPost:
				postCalls.Add(1)
				return nil, errors.New("synthetic lost response")
			case http.MethodGet:
				getCalls.Add(1)
				if !strings.Contains(request.URL.EscapedPath(), requestID) {
					t.Fatalf("lost admission reconciled with %q, want original request identity", request.URL.EscapedPath())
				}
				remote := excelPricingRemoteApplyJobForTest(
					jobID, requestID, source, stateRevision, previewDigest, "queued", false, "",
				)
				return excelPricingHTTPResponseForTest(http.StatusAccepted, remote.StatusURL, remote), nil
			default:
				t.Fatalf("unexpected method %s", request.Method)
				return nil, errors.New("unexpected method")
			}
		},
	)}
	token := openExcelPricingSession(t, server)
	body := validExcelPricingMutationBody("apply", requestID, stateRevision, previewDigest, "APPLY")
	bindExcelPricingPreviewForTest(t, server, body)
	request := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/apply", body, token)
	request.Header.Set("Idempotency-Key", requestID)
	request.Header.Set("If-Match", `"`+stateRevision+`"`)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted ||
		!strings.Contains(response.Body.String(), `"status":"queued"`) {
		t.Fatalf("lost admission recovery status=%d body=%s", response.Code, response.Body.String())
	}
	if postCalls.Load() != 1 || getCalls.Load() != 1 {
		t.Fatalf("lost admission calls post=%d get=%d, want 1/1", postCalls.Load(), getCalls.Load())
	}

	replay := authenticatedExcelPricingRequest(http.MethodPost, "/api/pricing-sync/apply", body, token)
	replay.Header.Set("Idempotency-Key", requestID)
	replay.Header.Set("If-Match", `"`+stateRevision+`"`)
	replayResponse := httptest.NewRecorder()
	server.router.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusAccepted || postCalls.Load() != 1 || getCalls.Load() != 1 {
		t.Fatalf("replay repeated remote action: status=%d post=%d get=%d", replayResponse.Code, postCalls.Load(), getCalls.Load())
	}

	statusRequest := authenticatedExcelPricingRequest(
		http.MethodGet,
		excelPricingLocalApplyJobPath(requestID, source),
		"",
		token,
	)
	statusResponse := httptest.NewRecorder()
	server.router.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusAccepted || getCalls.Load() != 1 {
		t.Fatalf("cached status polled remote: status=%d get=%d", statusResponse.Code, getCalls.Load())
	}

	ledgerBody, err := os.ReadFile(server.excelPricing.applyJobs.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ledgerBody, []byte(excelPricingTestSecret)) ||
		bytes.Contains(ledgerBody, []byte("127.0.0.1:18081")) {
		t.Fatalf("durable apply ledger retained protected transport material: %s", ledgerBody)
	}
	restarted := newExcelPricingApplyJobStore(server.excelPricing.applyJobs.path, server.excelPricing.now)
	restored, err := restarted.lookup(requestID)
	if err != nil || restored == nil || restored.JobID != jobID || restored.Status != "queued" {
		t.Fatalf("durable apply recovery=%#v err=%v", restored, err)
	}
}

func TestExcelPricingApplyCancelReconcilesUnknownAdmissionThenCancelsOnce(t *testing.T) {
	source := excelPricingStateSourceForTest()
	stateRevision := excelPricingRevisionForTest("cancel-state")
	previewDigest := excelPricingRevisionForTest("cancel-preview")
	requestID := "excel-apply-cancel-0001"
	jobID := "currency-22222222222222222222222222222222"
	server := newExcelPricingTestServer(
		t,
		"http://127.0.0.1:18081/wp-json/digitalogic/patris/product-sync",
	)
	local := excelPricingLocalRequest{
		RequestID:             requestID,
		IdempotencyKey:        requestID,
		ExpectedStateRevision: stateRevision,
		PreviewDigest:         previewDigest,
	}
	fingerprint := excelPricingRevisionForTest("cancel-fingerprint")
	if reservation, _, err := server.excelPricing.applyJobs.reserve(local, source, fingerprint); err != nil ||
		reservation != excelPricingApplyReservationNew {
		t.Fatalf("reserve=%v err=%v", reservation, err)
	}
	if err := server.excelPricing.applyJobs.markPostStarted(requestID); err != nil {
		t.Fatal(err)
	}
	if err := server.excelPricing.applyJobs.markAdmissionUnknown(requestID, "admission_response_unknown"); err != nil {
		t.Fatal(err)
	}
	var getCalls atomic.Int32
	var deleteCalls atomic.Int32
	server.excelPricing.client = &http.Client{Transport: excelPricingRoundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			var status string
			switch request.Method {
			case http.MethodGet:
				getCalls.Add(1)
				if !strings.Contains(request.URL.EscapedPath(), requestID) {
					t.Fatalf("cancel reconciliation did not use original request: %s", request.URL.EscapedPath())
				}
				status = "queued"
			case http.MethodDelete:
				deleteCalls.Add(1)
				if !strings.Contains(request.URL.EscapedPath(), jobID) {
					t.Fatalf("cancel did not use recovered job identity: %s", request.URL.EscapedPath())
				}
				status = "cancelling"
			default:
				t.Fatalf("unexpected method %s", request.Method)
			}
			remote := excelPricingRemoteApplyJobForTest(
				jobID, requestID, source, stateRevision, previewDigest, status, false, "",
			)
			return excelPricingHTTPResponseForTest(http.StatusAccepted, remote.StatusURL, remote), nil
		},
	)}
	token := openExcelPricingSession(t, server)
	request := authenticatedExcelPricingRequest(
		http.MethodDelete,
		excelPricingLocalApplyJobPath(requestID, source),
		"",
		token,
	)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted ||
		!strings.Contains(response.Body.String(), `"status":"cancelling"`) {
		t.Fatalf("cancel status=%d body=%s", response.Code, response.Body.String())
	}
	if getCalls.Load() != 1 || deleteCalls.Load() != 1 {
		t.Fatalf("cancel remote calls get=%d delete=%d, want 1/1", getCalls.Load(), deleteCalls.Load())
	}
}

func TestExcelPricingApplyTerminalCursorAdvancesOnlyAfterDurableAcceptance(t *testing.T) {
	source := excelPricingStateSourceForTest()
	stateRevision := excelPricingRevisionForTest("cursor-state")
	previewDigest := excelPricingRevisionForTest("cursor-preview")
	requestID := "excel-apply-cursor-0001"
	store := newExcelPricingApplyJobStore(t.TempDir()+"/pricing-apply-jobs.json", nil)
	local := excelPricingLocalRequest{
		RequestID:             requestID,
		IdempotencyKey:        requestID,
		ExpectedStateRevision: stateRevision,
		PreviewDigest:         previewDigest,
	}
	if reservation, _, err := store.reserve(
		local,
		source,
		excelPricingRevisionForTest("cursor-fingerprint"),
	); err != nil || reservation != excelPricingApplyReservationNew {
		t.Fatalf("reserve=%v err=%v", reservation, err)
	}
	event := excelPricingRemoteApplyTerminalEventForTest(
		requestID, source, previewDigest, stateRevision, "completed",
	)
	event.EventID = 41
	eventBody, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	frameBody, err := json.Marshal(excelPricingRemoteWireFrame{
		Event:   excelPricingApplyEventName,
		Name:    excelPricingApplyEventName,
		Success: true,
		Data:    eventBody,
		ID:      event.EventID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var cursor atomic.Uint64
	client := &excelPricingRemoteEventsClient{
		source:   source,
		cursor:   40,
		onCursor: cursor.Store,
		onApply: func(excelPricingRemoteApplyTerminalEvent) error {
			return errors.New("synthetic durable write failure")
		},
	}
	if _, err := client.handleExcelPricingRemoteFrame(context.Background(), frameBody, true); err == nil {
		t.Fatal("terminal event acknowledged before durable acceptance")
	}
	if client.currentCursor() != 40 || cursor.Load() != 0 {
		t.Fatalf("cursor advanced after failed durability: current=%d persisted=%d", client.currentCursor(), cursor.Load())
	}
	client.onApply = func(delivered excelPricingRemoteApplyTerminalEvent) error {
		_, _, acceptErr := store.acceptTerminalEvent(delivered)
		return acceptErr
	}
	if _, err := client.handleExcelPricingRemoteFrame(context.Background(), frameBody, true); err != nil {
		t.Fatal(err)
	}
	if client.currentCursor() != event.EventID || cursor.Load() != event.EventID {
		t.Fatalf("cursor did not advance after durable acceptance: current=%d persisted=%d", client.currentCursor(), cursor.Load())
	}
	accepted, err := store.lookup(requestID)
	if err != nil || accepted == nil || accepted.Status != "finalizing" {
		t.Fatalf("durably accepted event=%#v err=%v", accepted, err)
	}
	staleActive := excelPricingRemoteApplyJobForTest(
		event.JobID,
		requestID,
		source,
		stateRevision,
		previewDigest,
		"running",
		false,
		"",
	)
	accepted, err = store.acceptRemote(requestID, staleActive)
	if err != nil || accepted == nil || accepted.Status != "finalizing" || accepted.Terminal {
		t.Fatalf("stale status regressed durable terminal acceptance: job=%#v err=%v", accepted, err)
	}
	if err := store.markAdmissionUnknown(requestID, "late_transport_error"); err != nil {
		t.Fatal(err)
	}
	accepted, err = store.lookup(requestID)
	if err != nil || accepted == nil || accepted.Status != "finalizing" {
		t.Fatalf("late admission error regressed durable terminal acceptance: job=%#v err=%v", accepted, err)
	}
}

func TestExcelPricingLivingApplyContractsAcceptAdditiveFields(t *testing.T) {
	source := excelPricingStateSourceForTest()
	expectedStateRevision := excelPricingRevisionForTest("living-additive-expected-state")
	terminalStateRevision := excelPricingRevisionForTest("living-additive-terminal-state")
	previewDigest := excelPricingRevisionForTest("living-additive-preview")
	requestID := "excel-apply-living-additive-0001"
	remote := excelPricingRemoteApplyJobForTest(
		"currency-12121212121212121212121212121212",
		requestID,
		source,
		expectedStateRevision,
		previewDigest,
		"completed",
		true,
		terminalStateRevision,
	)
	remoteBody, err := json.Marshal(remote)
	if err != nil {
		t.Fatal(err)
	}
	var remotePayload map[string]interface{}
	if err := json.Unmarshal(remoteBody, &remotePayload); err != nil {
		t.Fatal(err)
	}
	remotePayload["future_capability"] = map[string]interface{}{"available": true}
	remoteBody, err = json.Marshal(remotePayload)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeExcelPricingRemoteApplyJob(remoteBody)
	if err != nil {
		t.Fatalf("Living apply response rejected an additive field: %v", err)
	}
	if err := validateExcelPricingRemoteApplyJob(decoded, excelPricingApplyJob{
		RequestID:             requestID,
		Source:                source,
		ExpectedStateRevision: expectedStateRevision,
		PreviewDigest:         previewDigest,
	}); err != nil {
		t.Fatalf("Living apply response failed stable-field validation: %v", err)
	}

	event := excelPricingRemoteApplyTerminalEventForTest(
		requestID,
		source,
		previewDigest,
		terminalStateRevision,
		"completed",
	)
	event.EventID = 73
	eventBody, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var eventPayload map[string]interface{}
	if err := json.Unmarshal(eventBody, &eventPayload); err != nil {
		t.Fatal(err)
	}
	eventPayload["future_capability"] = map[string]interface{}{"available": true}
	eventBody, err = json.Marshal(eventPayload)
	if err != nil {
		t.Fatal(err)
	}
	frameBody, err := json.Marshal(excelPricingRemoteWireFrame{
		Event:   excelPricingApplyEventName,
		Name:    excelPricingApplyEventName,
		Success: true,
		Data:    eventBody,
		ID:      event.EventID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Bool
	client := &excelPricingRemoteEventsClient{
		source: source,
		cursor: event.EventID - 1,
		onApply: func(excelPricingRemoteApplyTerminalEvent) error {
			accepted.Store(true)
			return nil
		},
	}
	if _, err := client.handleExcelPricingRemoteFrame(context.Background(), frameBody, true); err != nil {
		t.Fatalf("Living terminal event rejected an additive field: %v", err)
	}
	if !accepted.Load() || client.currentCursor() != event.EventID {
		t.Fatalf("Living terminal event was not durably accepted: accepted=%v cursor=%d", accepted.Load(), client.currentCursor())
	}
}

func TestExcelPricingCoalescedRemoteJobFinalizesCanonicalDeliveryOnce(t *testing.T) {
	source := excelPricingStateSourceForTest()
	expectedStateRevision := excelPricingRevisionForTest("coalesced-expected-state")
	terminalStateRevision := excelPricingRevisionForTest("coalesced-terminal-state")
	previewDigest := excelPricingRevisionForTest("coalesced-preview")
	jobID := "currency-33333333333333333333333333333333"
	requestIDs := []string{"excel-apply-coalesced-0001", "excel-apply-coalesced-0002"}
	var stateCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || !strings.HasSuffix(request.URL.Path, "/state") {
			t.Fatalf("unexpected coalesced finalizer request %s %s", request.Method, request.URL.Path)
		}
		stateCalls.Add(1)
		writeRemotePricingResponse(t, w, excelPricingStateSchema, terminalStateRevision)
	}))
	defer remote.Close()
	server := newExcelPricingTestServer(t, remote.URL+"/wp-json/digitalogic/patris/product-sync")
	server.excelPricing.canonical = canonicalProjectionSequence(source)
	var deliveries atomic.Int32
	server.excelPricing.dispatch = func(
		_ context.Context,
		_ updateout.Config,
		event updateout.Event,
	) (updateout.DeliveryResult, error) {
		deliveries.Add(1)
		return updateout.DeliveryResult{
			HTTPStatus: http.StatusOK,
			Status:     "accepted",
			EventID:    event.Contract.EventID,
			Attempts:   1,
		}, nil
	}
	for index, requestID := range requestIDs {
		local := excelPricingLocalRequest{
			RequestID:             requestID,
			IdempotencyKey:        requestID,
			ExpectedStateRevision: expectedStateRevision,
			PreviewDigest:         previewDigest,
		}
		if reservation, _, err := server.excelPricing.applyJobs.reserve(
			local,
			source,
			excelPricingRevisionForTest("coalesced-fingerprint-"+requestID),
		); err != nil || reservation != excelPricingApplyReservationNew {
			t.Fatalf("reserve %d=%v err=%v", index, reservation, err)
		}
		if err := server.excelPricing.applyJobs.markPostStarted(requestID); err != nil {
			t.Fatal(err)
		}
		remoteJob := excelPricingRemoteApplyJobForTest(
			jobID,
			requestID,
			source,
			expectedStateRevision,
			previewDigest,
			"queued",
			false,
			"",
		)
		if _, err := server.excelPricing.applyJobs.acceptRemote(requestID, remoteJob); err != nil {
			t.Fatal(err)
		}
	}
	event := excelPricingRemoteApplyTerminalEventForTest(
		requestIDs[0], source, previewDigest, terminalStateRevision, "completed",
	)
	event.JobID = jobID
	event.RequestIDs = requestIDs
	event.PrimaryRequestID = requestIDs[0]
	event.StatusPath = excelPricingRemoteApplyJobPath(jobID, source)
	if err := server.acceptExcelPricingRemoteApplyTerminal(event); err != nil {
		t.Fatal(err)
	}
	for _, requestID := range requestIDs {
		waitForExcelPricingApplyJobForTest(t, server, requestID, "completed")
	}
	if deliveries.Load() != 1 || stateCalls.Load() != 1 {
		t.Fatalf("coalesced finalization repeated work: delivery=%d state=%d", deliveries.Load(), stateCalls.Load())
	}
}

func TestExcelPricingApplyFinalizerRestartReusesStableDeliveryIdentity(t *testing.T) {
	source := excelPricingStateSourceForTest()
	expectedStateRevision := excelPricingRevisionForTest("restart-expected-state")
	terminalStateRevision := excelPricingRevisionForTest("restart-terminal-state")
	previewDigest := excelPricingRevisionForTest("restart-preview")
	requestID := "excel-apply-finalizer-restart-0001"
	statePath := t.TempDir() + "/pricing-apply-jobs.json"
	store := newExcelPricingApplyJobStore(statePath, nil)
	local := excelPricingLocalRequest{
		RequestID:             requestID,
		IdempotencyKey:        requestID,
		ExpectedStateRevision: expectedStateRevision,
		PreviewDigest:         previewDigest,
	}
	if reservation, _, err := store.reserve(
		local,
		source,
		excelPricingRevisionForTest("restart-fingerprint"),
	); err != nil || reservation != excelPricingApplyReservationNew {
		t.Fatalf("reserve=%v err=%v", reservation, err)
	}
	event := excelPricingRemoteApplyTerminalEventForTest(
		requestID, source, previewDigest, terminalStateRevision, "completed",
	)
	if _, accepted, err := store.acceptTerminalEvent(event); err != nil || !accepted {
		t.Fatalf("terminal accepted=%v err=%v", accepted, err)
	}
	claimed, ok, err := store.claimFinalization(requestID)
	if err != nil || !ok || claimed.FinalizationState != "running" {
		t.Fatalf("claim=%#v ok=%v err=%v", claimed, ok, err)
	}
	deliveryEventID := excelPricingRevisionForTest("restart-delivery-event")
	if err := store.recordDeliveryEvent(requestID, deliveryEventID); err != nil {
		t.Fatal(err)
	}

	restarted := newExcelPricingApplyJobStore(statePath, nil)
	recovered, err := restarted.lookup(requestID)
	if err != nil || recovered.FinalizationState != "retryable" ||
		recovered.DeliveryEventID != deliveryEventID {
		t.Fatalf("recovered=%#v err=%v", recovered, err)
	}
	claimed, ok, err = restarted.claimFinalization(requestID)
	if err != nil || !ok || claimed.FinalizationState != "running" {
		t.Fatalf("reclaim=%#v ok=%v err=%v", claimed, ok, err)
	}
	if err := restarted.recordDeliveryEvent(requestID, deliveryEventID); err != nil {
		t.Fatalf("stable delivery replay was rejected: %v", err)
	}
	completed, err := restarted.completeFinalization(requestID, terminalStateRevision)
	if err != nil || len(completed) != 1 || !completed[0].Terminal {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
}

func excelPricingRemoteApplyJobForTest(
	jobID string,
	requestID string,
	source canonical.Source,
	expectedStateRevision string,
	previewDigest string,
	status string,
	terminal bool,
	stateRevision string,
) excelPricingRemoteApplyJob {
	retryAfter := 2
	statusPath := excelPricingRemoteApplyJobPath(jobID, source)
	job := excelPricingRemoteApplyJob{
		Schema:                excelPricingRemoteApplySchema,
		JobID:                 jobID,
		RequestID:             requestID,
		IdempotencyKey:        requestID,
		Status:                status,
		Terminal:              terminal,
		ExpectedStateRevision: expectedStateRevision,
		PreviewDigest:         previewDigest,
		Source:                source,
		Progress:              map[string]interface{}{},
		RetryAfter:            &retryAfter,
		EventDelivery:         map[string]interface{}{},
		StatusURL:             statusPath,
		CancelURL:             statusPath,
	}
	if terminal {
		job.RetryAfter = nil
	}
	if status == "completed" {
		job.Result = &excelPricingApplyResult{
			Schema:         excelPricingApplySchema,
			Mode:           "apply",
			Status:         "applied",
			StateRevision:  stateRevision,
			Source:         source,
			ClientID:       excelPricingContractClientID,
			Channel:        excelPricingContractChannel,
			RequestID:      requestID,
			PreviewDigest:  previewDigest,
			Settings:       json.RawMessage(`{}`),
			Warnings:       json.RawMessage(`[]`),
			ProductResults: json.RawMessage(`[]`),
		}
	}
	return job
}

func excelPricingHTTPResponseForTest(
	status int,
	location string,
	payload interface{},
) *http.Response {
	body, _ := json.Marshal(payload)
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	if location != "" {
		headers.Set("Location", location)
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}
