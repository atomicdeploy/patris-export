package server

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
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

	mu                      sync.Mutex
	revisionCalls           int
	startCalls              int
	bulkCalls               int
	statusCalls             int
	cancelCalls             int
	legacyCalls             int
	headerFailure           string
	revisionIdentityCalls   int
	revisionCompressedCalls int
	payloadIdentityCalls    int
	payloadCompressedCalls  int
	payloadConditionalCalls int
}

type rejectingExcelPricingRemoteTerminalSource struct{}

type excelPricingRemoteSnapshotRoundTripFunc func(*http.Request) (*http.Response, error)

type failingExcelPricingRemoteSnapshotReader struct{}

func (failingExcelPricingRemoteSnapshotReader) Read([]byte) (int, error) {
	return 0, errors.New("private response read failure")
}

func (roundTrip excelPricingRemoteSnapshotRoundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

func (rejectingExcelPricingRemoteTerminalSource) Subscribe(
	string,
	canonical.Source,
	string,
) (excelPricingRemoteSnapshotTerminalSubscription, error) {
	return nil, errors.New("private subscription implementation detail")
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
		len(result.ProjectedRowFields) != len(excelPricingSnapshotExcelV1RowFields) {
		t.Fatalf("projection sizes = rows:%d projected:%d fields:%d",
			len(result.Rows), len(result.ProjectedRows), len(result.ProjectedRowFields))
	}
	var projected []json.RawMessage
	if err := json.Unmarshal(result.ProjectedRows[0], &projected); err != nil {
		t.Fatalf("projected row: %v", err)
	}
	if len(projected) != len(excelPricingSnapshotExcelV1RowFields) ||
		excelPricingSnapshotString(projected[0]) != "woo:101" ||
		excelPricingSnapshotString(projected[19]) != "محصول آزمایشی" {
		t.Fatalf("projected row = %s", result.ProjectedRows[0])
	}
	fixture.assertCalls(t, 1, 1, 1, 0, 0)
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

func TestExcelPricingRemoteSnapshotIdentityEncodingPreservesOriginETags(t *testing.T) {
	t.Run("revision", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "cdn_rewritten_etag")
		defer fixture.Close()

		client := fixture.Client(t)
		base := client.client.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		autoDecompressed := false
		client.client.Transport = excelPricingRemoteSnapshotRoundTripFunc(
			func(request *http.Request) (*http.Response, error) {
				clone := request.Clone(request.Context())
				clone.Header = request.Header.Clone()
				if clone.URL.Path == "/wp-json/digitalogic/pricing/sync/revision" {
					clone.Header.Del("Accept-Encoding")
				}
				response, err := base.RoundTrip(clone)
				if response != nil && clone.URL.Path == "/wp-json/digitalogic/pricing/sync/revision" {
					autoDecompressed = response.Uncompressed
				}
				return response, err
			},
		)
		if _, err := client.fetchRevision(context.Background()); !errors.Is(
			err, errExcelPricingRemoteSnapshotProtocol,
		) {
			t.Fatalf("compressed representation revision error = %v", err)
		}
		if !autoDecompressed {
			t.Fatal("control response was not transparently decompressed")
		}

		client = fixture.Client(t)
		if _, err := client.fetchRevision(context.Background()); err != nil {
			t.Fatalf("identity revision error = %v", err)
		}
		fixture.assertRepresentationCalls(t, 1, 1, 0, 0)
	})

	t.Run("immutable payload", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "cdn_rewritten_etag")
		defer fixture.Close()

		client := fixture.Client(t)
		revision, err := client.fetchRevision(context.Background())
		if err != nil {
			t.Fatalf("revision error = %v", err)
		}
		build := fixture.buildResponse()
		base := client.client.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		autoDecompressed := false
		client.client.Transport = excelPricingRemoteSnapshotRoundTripFunc(
			func(request *http.Request) (*http.Response, error) {
				clone := request.Clone(request.Context())
				clone.Header = request.Header.Clone()
				if strings.HasPrefix(clone.URL.Path, "/wp-json/digitalogic/pricing/sync/snapshots/") {
					clone.Header.Del("Accept-Encoding")
				}
				response, err := base.RoundTrip(clone)
				if response != nil && strings.HasPrefix(
					clone.URL.Path, "/wp-json/digitalogic/pricing/sync/snapshots/",
				) {
					autoDecompressed = response.Uncompressed
				}
				return response, err
			},
		)
		if _, err := client.fetchSnapshot(context.Background(), build, revision); !errors.Is(
			err, errExcelPricingRemoteSnapshotProtocol,
		) {
			t.Fatalf("compressed representation payload error = %v", err)
		}
		if !autoDecompressed {
			t.Fatal("control payload was not transparently decompressed")
		}

		client = fixture.Client(t)
		if _, err := client.fetchSnapshot(context.Background(), build, revision); err != nil {
			t.Fatalf("identity payload error = %v", err)
		}
		fixture.assertRepresentationCalls(t, 1, 0, 1, 1)
	})

	t.Run("collector keeps start and terminal semantics", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "cdn_rewritten_etag")
		defer fixture.Close()

		result, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
		if err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		if result.CompositeStateRevision != fixture.revision.StateRevision ||
			result.SnapshotRevision != fixture.payload.SnapshotRevision {
			t.Fatalf("collector identity = %+v", result)
		}
		fixture.assertCalls(t, 1, 1, 1, 0, 0)
		fixture.assertRepresentationCalls(t, 1, 0, 1, 0)
	})
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
				excelPricingSnapshotProjectionExcelV1,
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
		payload.Projection != excelPricingSnapshotProjectionExcelV1 ||
		len(payload.RowFields) != len(excelPricingSnapshotExcelV1RowFields) ||
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

func TestExcelPricingRemoteSnapshotCollectAnnotatesEveryRemoteStage(t *testing.T) {
	assertStage := func(t *testing.T, err error, wantStage, wantCode string) {
		t.Helper()
		stage, code, ok := excelPricingRemoteSnapshotFailureDetails(err)
		if !ok || stage != wantStage || code != wantCode {
			t.Fatalf("failure details = (%q, %q, %v), want (%q, %q, true): %v",
				stage, code, ok, wantStage, wantCode, err)
		}
		if strings.Contains(err.Error(), "private subscription implementation detail") ||
			strings.Contains(err.Error(), "private transport implementation detail") {
			t.Fatalf("structured error leaked raw cause: %v", err)
		}
	}
	rejectTransport := func(
		client *excelPricingRemoteSnapshotClient,
		predicate func(*http.Request) bool,
	) {
		base := client.client.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		client.client.Transport = excelPricingRemoteSnapshotRoundTripFunc(
			func(request *http.Request) (*http.Response, error) {
				if predicate(request) {
					return nil, errors.New("private transport implementation detail")
				}
				return base.RoundTrip(request)
			},
		)
	}

	t.Run("revision fetch protocol", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
		defer fixture.Close()
		fixture.revision.Schema = "invalid-revision-schema"
		_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageRevisionFetch,
			"snapshot_revision_fetch_protocol_failed")
		fixture.assertCalls(t, 1, 0, 0, 0, 0)
	})

	t.Run("revision fetch wrong content type", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "revision_wrong_content_type")
		defer fixture.Close()
		_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageRevisionFetch,
			"snapshot_revision_fetch_protocol_failed")
		fixture.assertCalls(t, 1, 0, 0, 0, 0)
	})

	for name, mode := range map[string]string{
		"revision fetch empty body":     "revision_empty_body",
		"revision fetch oversized body": "revision_oversized_body",
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newExcelPricingRemoteSnapshotFixture(t, mode)
			defer fixture.Close()
			_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
			assertStage(t, err, excelPricingRemoteSnapshotStageRevisionFetch,
				"snapshot_revision_fetch_protocol_failed")
			fixture.assertCalls(t, 1, 0, 0, 0, 0)
		})
	}

	t.Run("revision fetch unavailable", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
		defer fixture.Close()
		client := fixture.Client(t)
		rejectTransport(client, func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				request.URL.Path == "/wp-json/digitalogic/pricing/sync/revision"
		})
		_, err := client.Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageRevisionFetch,
			"snapshot_revision_fetch_unavailable")
		fixture.assertCalls(t, 0, 0, 0, 0, 0)
	})

	t.Run("terminal subscription", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
		defer fixture.Close()
		client := fixture.Client(t)
		client.terminals = rejectingExcelPricingRemoteTerminalSource{}
		_, err := client.Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageTerminalSubscription,
			"snapshot_terminal_subscription_failed")
		fixture.assertCalls(t, 1, 0, 0, 0, 0)
	})

	t.Run("snapshot start protocol", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
		defer fixture.Close()
		fixture.badSnapshotURL = "https://attacker.invalid/wp-json/digitalogic/pricing/sync/snapshots/" +
			fixture.payload.SnapshotToken
		_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageSnapshotStart,
			"snapshot_start_protocol_failed")
		fixture.assertCalls(t, 1, 1, 0, 0, 0)
	})

	t.Run("snapshot start unavailable", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
		defer fixture.Close()
		client := fixture.Client(t)
		rejectTransport(client, func(request *http.Request) bool {
			return request.Method == http.MethodPost &&
				request.URL.Path == "/wp-json/digitalogic/pricing/sync/snapshots"
		})
		_, err := client.Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageSnapshotStart,
			"snapshot_start_unavailable")
		fixture.assertCalls(t, 1, 0, 0, 0, 0)
	})

	t.Run("snapshot start rejected", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "start_rejected")
		defer fixture.Close()
		_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageSnapshotStart,
			"snapshot_start_rejected")
		fixture.assertCalls(t, 1, 1, 0, 0, 0)
	})

	t.Run("terminal wait", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "cold")
		defer fixture.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		_, err := fixture.Client(t).Collect(ctx, fixture.requestID, 0)
		assertStage(t, err, excelPricingRemoteSnapshotStageTerminalWait,
			"snapshot_terminal_wait_failed")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("terminal wait lost deadline cause: %v", err)
		}
	})

	t.Run("terminal match", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "event_bad_match")
		defer fixture.Close()
		_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 0)
		assertStage(t, err, excelPricingRemoteSnapshotStageTerminalMatch,
			"snapshot_terminal_match_failed")
	})

	t.Run("remote terminal", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "event_failed")
		defer fixture.Close()
		_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 0)
		assertStage(t, err, excelPricingRemoteSnapshotStageRemoteTerminal,
			"snapshot_remote_terminal_failed")
	})

	t.Run("snapshot payload protocol", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
		defer fixture.Close()
		fixture.badETag = true
		_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageSnapshotPayload,
			"snapshot_payload_protocol_failed")
		fixture.assertCalls(t, 1, 1, 1, 0, 0)
	})

	t.Run("snapshot payload missing ETag verified once", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "payload_missing_etag_verified")
		defer fixture.Close()
		result, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
		if err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		if result.ETag != `"`+fixture.payload.Digest+`"` {
			t.Fatalf("result ETag = %q", result.ETag)
		}
		fixture.assertCalls(t, 1, 1, 2, 0, 0)
		fixture.assertConditionalPayloadCalls(t, 1)
	})

	t.Run("snapshot payload present empty ETag does not fall back", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "payload_present_empty_etag")
		defer fixture.Close()
		_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageSnapshotPayload,
			"snapshot_payload_protocol_failed")
		fixture.assertCalls(t, 1, 1, 1, 0, 0)
		fixture.assertConditionalPayloadCalls(t, 0)
	})

	t.Run("snapshot payload duplicate ETag does not fall back", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "payload_duplicate_etag")
		defer fixture.Close()
		_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageSnapshotPayload,
			"snapshot_payload_protocol_failed")
		fixture.assertCalls(t, 1, 1, 1, 0, 0)
		fixture.assertConditionalPayloadCalls(t, 0)
	})

	t.Run("snapshot payload wrong strong ETag does not fall back", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "payload_wrong_strong_etag")
		defer fixture.Close()
		_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageSnapshotPayload,
			"snapshot_payload_protocol_failed")
		fixture.assertCalls(t, 1, 1, 1, 0, 0)
		fixture.assertConditionalPayloadCalls(t, 0)
	})

	for name, mode := range map[string]string{
		"snapshot payload missing ETag conditional non-304":                 "payload_missing_etag_non304",
		"snapshot payload missing ETag conditional wrong present validator": "payload_missing_etag_wrong_confirmation_etag",
		"snapshot payload missing ETag conditional duplicate validator":     "payload_missing_etag_duplicate_confirmation_etag",
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newExcelPricingRemoteSnapshotFixture(t, mode)
			defer fixture.Close()
			_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
			assertStage(t, err, excelPricingRemoteSnapshotStageSnapshotPayload,
				"snapshot_payload_protocol_failed")
			fixture.assertCalls(t, 1, 1, 2, 0, 0)
			fixture.assertConditionalPayloadCalls(t, 1)
		})
	}

	t.Run("snapshot payload wrong content type", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "payload_wrong_content_type")
		defer fixture.Close()
		_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageSnapshotPayload,
			"snapshot_payload_protocol_failed")
		fixture.assertCalls(t, 1, 1, 1, 0, 0)
	})

	for name, mode := range map[string]string{
		"snapshot payload empty body":     "payload_empty_body",
		"snapshot payload oversized body": "payload_oversized_body",
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newExcelPricingRemoteSnapshotFixture(t, mode)
			defer fixture.Close()
			_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
			assertStage(t, err, excelPricingRemoteSnapshotStageSnapshotPayload,
				"snapshot_payload_protocol_failed")
			fixture.assertCalls(t, 1, 1, 1, 0, 0)
		})
	}

	t.Run("snapshot payload unavailable", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
		defer fixture.Close()
		client := fixture.Client(t)
		rejectTransport(client, func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.HasPrefix(request.URL.Path, "/wp-json/digitalogic/pricing/sync/snapshots/")
		})
		_, err := client.Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageSnapshotPayload,
			"snapshot_payload_unavailable")
		fixture.assertCalls(t, 1, 1, 0, 0, 0)
	})

	t.Run("snapshot payload integrity", func(t *testing.T) {
		fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
		defer fixture.Close()
		var columns []map[string]json.RawMessage
		if err := json.Unmarshal(fixture.payload.Catalog.Columns, &columns); err != nil {
			t.Fatal(err)
		}
		columns[0], columns[1] = columns[1], columns[0]
		fixture.payload.Catalog.Columns = mustMarshalExcelPricingRemoteSnapshotTestJSON(t, columns)
		fixture.finalizePayload()
		_, err := fixture.Client(t).Collect(context.Background(), fixture.requestID, 60)
		assertStage(t, err, excelPricingRemoteSnapshotStageSnapshotPayload,
			"snapshot_payload_integrity_failed")
	})
}

func TestExcelPricingRemoteSnapshotStageCodeCoversConfigurationAndTransportClasses(t *testing.T) {
	for _, test := range []struct {
		name  string
		stage string
		cause error
		code  string
	}{
		{"revision configuration", excelPricingRemoteSnapshotStageRevisionFetch,
			errExcelPricingRemoteSnapshotConfiguration, "snapshot_revision_fetch_configuration_failed"},
		{"revision transport", excelPricingRemoteSnapshotStageRevisionFetch,
			errExcelPricingRemoteSnapshotUnavailable, "snapshot_revision_fetch_unavailable"},
		{"start configuration", excelPricingRemoteSnapshotStageSnapshotStart,
			errExcelPricingRemoteSnapshotConfiguration, "snapshot_start_configuration_failed"},
		{"start transport", excelPricingRemoteSnapshotStageSnapshotStart,
			errExcelPricingRemoteSnapshotUnavailable, "snapshot_start_unavailable"},
		{"start response", excelPricingRemoteSnapshotStageSnapshotStart,
			errExcelPricingRemoteSnapshotRejected, "snapshot_start_rejected"},
		{"payload configuration", excelPricingRemoteSnapshotStageSnapshotPayload,
			errExcelPricingRemoteSnapshotConfiguration, "snapshot_payload_configuration_failed"},
		{"payload transport", excelPricingRemoteSnapshotStageSnapshotPayload,
			errExcelPricingRemoteSnapshotUnavailable, "snapshot_payload_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := wrapExcelPricingRemoteSnapshotStage(test.stage, test.cause)
			stage, code, ok := excelPricingRemoteSnapshotFailureDetails(err)
			if !ok || stage != test.stage || code != test.code {
				t.Fatalf("failure details=(%q,%q,%v), want=(%q,%q,true)",
					stage, code, ok, test.stage, test.code)
			}
		})
	}

	if _, err := readExcelPricingRemoteSnapshotBody(failingExcelPricingRemoteSnapshotReader{}, 8); !errors.Is(
		err,
		errExcelPricingRemoteSnapshotUnavailable,
	) {
		t.Fatalf("body read failure=%v, want unavailable", err)
	}
	for name, body := range map[string]string{
		"empty":     "",
		"oversized": "123456789",
	} {
		t.Run(name+" body", func(t *testing.T) {
			if _, err := readExcelPricingRemoteSnapshotBody(strings.NewReader(body), 8); !errors.Is(
				err,
				errExcelPricingRemoteSnapshotProtocol,
			) {
				t.Fatalf("body error=%v, want protocol", err)
			}
		})
	}
}

func TestExcelPricingRemoteSnapshotMissingETagConfirmationRejectsNonempty304(t *testing.T) {
	digest := testExcelPricingRevision('a')
	requestURL, err := url.Parse("https://example.test/wp-json/digitalogic/pricing/sync/snapshots/snap_00000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &excelPricingRemoteSnapshotClient{
		source: canonical.Source{
			ID:       "patris-office",
			Dataset:  "kala.db",
			Revision: testExcelPricingRevision('1'),
		},
		secret: "test-remote-snapshot-secret-value",
		client: &http.Client{Transport: excelPricingRemoteSnapshotRoundTripFunc(
			func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != http.MethodGet ||
					request.Header.Get("Accept-Encoding") != excelPricingRemoteIdentityEncoding ||
					request.Header.Get("If-None-Match") != `"`+digest+`"` ||
					request.Header.Get(excelPricingRemoteSecretHeader) != "test-remote-snapshot-secret-value" {
					t.Fatal("conditional validator request headers are invalid")
				}
				return &http.Response{
					StatusCode: http.StatusNotModified,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("unexpected")),
					Request:    request,
				}, nil
			},
		)},
	}
	if err := client.confirmSnapshotValidator(context.Background(), requestURL, digest); !errors.Is(
		err,
		errExcelPricingRemoteSnapshotProtocol,
	) {
		t.Fatalf("confirmation error = %v, want protocol", err)
	}
	if calls != 1 {
		t.Fatalf("conditional calls = %d, want 1", calls)
	}
}

func TestExcelPricingSnapshotFailureEvidencePreservesPublicCodeAndPrivacy(t *testing.T) {
	fixture := newExcelPricingRemoteSnapshotFixture(t, "ready")
	defer fixture.Close()
	fixture.acceptAnyID = true
	fixture.revision.Schema = "invalid-revision-schema"
	server, token := newExcelPricingRemoteSnapshotProductionServer(t, fixture)
	requestID := "snapshot-stage-evidence-0001"
	request := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(fixture.source, requestID, "fa", 0),
		token,
	)
	request.Header.Set("Idempotency-Key", requestID)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("snapshot start=%d: %s", response.Code, response.Body.String())
	}
	jobID := excelPricingSnapshotJobIDForTest(t, response.Body.Bytes())
	status := waitForExcelPricingSnapshotStatus(t, server, token, jobID, "failed")
	if status["code"] != "snapshot_integrity_failed" {
		t.Fatalf("stable public code changed: %#v", status)
	}
	failure, ok := status["failure"].(map[string]interface{})
	if !ok || len(failure) != 3 ||
		failure["schema"] != excelPricingSnapshotFailureSchema ||
		failure["stage"] != excelPricingRemoteSnapshotStageRevisionFetch ||
		failure["code"] != "snapshot_revision_fetch_protocol_failed" {
		t.Fatalf("structured failure = %#v", status["failure"])
	}
	rendered, err := json.Marshal(failure)
	if err != nil {
		t.Fatal(err)
	}
	for _, protected := range []string{
		"test-remote-snapshot-secret-value",
		fixture.server.URL,
		fixture.source.ID,
		fixture.source.Dataset,
		fixture.requestID,
		fixture.buildID,
	} {
		if protected != "" && strings.Contains(string(rendered), protected) {
			t.Fatalf("structured failure leaked protected material: %s", rendered)
		}
	}
	fixture.assertCalls(t, 1, 0, 0, 0, 0)
}

func TestExcelPricingSnapshotFailureEvidenceRejectsUnreviewedValues(t *testing.T) {
	for _, test := range []struct {
		stage string
		code  string
	}{
		{stage: "https://private.invalid/path", code: "snapshot_revision_fetch_protocol_failed"},
		{stage: excelPricingRemoteSnapshotStageRevisionFetch, code: "private raw transport error"},
		{stage: excelPricingSnapshotStageRemoteConfiguration, code: "snapshot_payload_unavailable"},
	} {
		if validExcelPricingSnapshotFailure(test.stage, test.code) {
			t.Fatalf("unreviewed failure pair accepted: stage=%q code=%q", test.stage, test.code)
		}
	}

	for _, test := range []struct {
		stage string
		code  string
	}{
		{excelPricingRemoteSnapshotStageRevisionFetch, "snapshot_revision_fetch_protocol_failed"},
		{excelPricingRemoteSnapshotStageTerminalSubscription, "snapshot_terminal_subscription_failed"},
		{excelPricingRemoteSnapshotStageSnapshotStart, "snapshot_start_protocol_failed"},
		{excelPricingRemoteSnapshotStageTerminalWait, "snapshot_terminal_wait_failed"},
		{excelPricingRemoteSnapshotStageTerminalMatch, "snapshot_terminal_match_failed"},
		{excelPricingRemoteSnapshotStageRemoteTerminal, "snapshot_remote_terminal_failed"},
		{excelPricingRemoteSnapshotStageSnapshotPayload, "snapshot_payload_integrity_failed"},
		{excelPricingSnapshotStageRemoteConfiguration, "snapshot_remote_configuration_failed"},
		{excelPricingSnapshotStageLocalProjection, "snapshot_local_projection_integrity_failed"},
	} {
		if !validExcelPricingSnapshotFailure(test.stage, test.code) {
			t.Fatalf("reviewed failure pair rejected: stage=%q code=%q", test.stage, test.code)
		}
	}
}

func TestExcelPricingSnapshotProductionFailureStagesCoverConfigurationAndLocalProjection(t *testing.T) {
	source := excelPricingStateSourceForTest()
	for _, test := range []struct {
		name       string
		publicCode string
		stage      string
		code       string
		configure  func(*Server)
	}{
		{
			name:       "remote configuration",
			publicCode: "remote_unavailable",
			stage:      excelPricingSnapshotStageRemoteConfiguration,
			code:       "snapshot_remote_configuration_failed",
			configure: func(server *Server) {
				server.excelPricing.snapshotCollector = nil
				server.excelPricingRemote = nil
			},
		},
		{
			name:       "local projection",
			publicCode: "snapshot_integrity_failed",
			stage:      excelPricingSnapshotStageLocalProjection,
			code:       "snapshot_local_projection_integrity_failed",
			configure: func(server *Server) {
				server.excelPricing.snapshotCollector = func(
					_ context.Context,
					jobID string,
					request excelPricingSnapshotStartRequest,
					_ updateout.Config,
				) (*excelPricingSnapshot, string) {
					return server.finalizeExcelPricingRemoteSnapshot(
						jobID,
						nil,
						excelPricingSnapshotProjection(request.Projection),
					)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newExcelPricingTestServer(
				t,
				"http://127.0.0.1:1/wp-json/digitalogic/patris/product-sync",
			)
			test.configure(server)
			token := openExcelPricingSession(t, server)
			requestID := "snapshot-production-failure-" + strings.ReplaceAll(test.name, " ", "-")
			request := authenticatedExcelPricingRequest(
				http.MethodPost,
				"/api/pricing-sync/snapshots",
				validExcelPricingSnapshotStartBody(source, requestID, "fa", 0),
				token,
			)
			request.Header.Set("Idempotency-Key", requestID)
			response := httptest.NewRecorder()
			server.router.ServeHTTP(response, request)
			if response.Code != http.StatusAccepted {
				t.Fatalf("snapshot start=%d: %s", response.Code, response.Body.String())
			}
			jobID := excelPricingSnapshotJobIDForTest(t, response.Body.Bytes())
			status := waitForExcelPricingSnapshotStatus(t, server, token, jobID, "failed")
			assertExcelPricingSnapshotFailureForTest(
				t,
				status,
				test.publicCode,
				test.stage,
				test.code,
			)
		})
	}
}

func TestExcelPricingSnapshotFailurePropagatesToTerminalWaitSSEAndFollower(t *testing.T) {
	source := excelPricingStateSourceForTest()
	server := newExcelPricingTestServer(
		t,
		"http://127.0.0.1:1/wp-json/digitalogic/patris/product-sync",
	)
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	server.excelPricing.snapshotCollector = func(
		ctx context.Context,
		jobID string,
		_ excelPricingSnapshotStartRequest,
		_ updateout.Config,
	) (*excelPricingSnapshot, string) {
		startedOnce.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return nil, excelPricingSnapshotContextCode(ctx)
		}
		server.setExcelPricingSnapshotFailure(
			jobID,
			excelPricingRemoteSnapshotStageRevisionFetch,
			"snapshot_revision_fetch_protocol_failed",
		)
		return nil, "snapshot_integrity_failed"
	}
	token := openExcelPricingSession(t, server)
	start := func(requestID string) (string, map[string]interface{}) {
		t.Helper()
		request := authenticatedExcelPricingRequest(
			http.MethodPost,
			"/api/pricing-sync/snapshots",
			validExcelPricingSnapshotStartBody(source, requestID, "fa", 0),
			token,
		)
		request.Header.Set("Idempotency-Key", requestID)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("snapshot start=%d: %s", response.Code, response.Body.String())
		}
		var status map[string]interface{}
		if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		return excelPricingSnapshotJobIDForTest(t, response.Body.Bytes()), status
	}
	leaderID, leaderStart := start("snapshot-failure-leader-0001")
	if leaderStart["coalesced"] != false {
		t.Fatalf("leader start=%#v", leaderStart)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("leader collector did not start")
	}
	followerID, followerStart := start("snapshot-failure-follower-0001")
	if followerStart["coalesced"] != true {
		t.Fatalf("follower start=%#v", followerStart)
	}
	close(release)
	leader := waitForExcelPricingSnapshotStatus(t, server, token, leaderID, "failed")
	follower := waitForExcelPricingSnapshotStatus(t, server, token, followerID, "failed")
	for name, status := range map[string]map[string]interface{}{
		"leader":   leader,
		"follower": follower,
	} {
		t.Run(name+" terminal wait", func(t *testing.T) {
			assertExcelPricingSnapshotFailureForTest(
				t,
				status,
				"snapshot_integrity_failed",
				excelPricingRemoteSnapshotStageRevisionFetch,
				"snapshot_revision_fetch_protocol_failed",
			)
		})
	}

	loopback := httptest.NewServer(server.router)
	defer loopback.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		loopback.URL+"/api/pricing-sync/snapshots/"+followerID,
		nil,
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
	if response.StatusCode != http.StatusOK {
		t.Fatalf("SSE status=%d", response.StatusCode)
	}
	found := false
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
		if event.Kind != "snapshot_job" || event.Job["status"] != "failed" {
			continue
		}
		assertExcelPricingSnapshotFailureForTest(
			t,
			event.Job,
			"snapshot_integrity_failed",
			excelPricingRemoteSnapshotStageRevisionFetch,
			"snapshot_revision_fetch_protocol_failed",
		)
		found = true
		break
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("follower SSE did not replay the structured terminal failure")
	}
}

func TestExcelPricingSnapshotCancellationNeverRetainsUnrelatedFailureEvidence(t *testing.T) {
	source := excelPricingStateSourceForTest()
	server := newExcelPricingTestServer(
		t,
		"http://127.0.0.1:1/wp-json/digitalogic/patris/product-sync",
	)
	started := make(chan struct{})
	release := make(chan struct{})
	server.excelPricing.snapshotCollector = func(
		_ context.Context,
		jobID string,
		_ excelPricingSnapshotStartRequest,
		_ updateout.Config,
	) (*excelPricingSnapshot, string) {
		close(started)
		<-release
		server.setExcelPricingSnapshotFailure(
			jobID,
			excelPricingRemoteSnapshotStageRevisionFetch,
			"snapshot_revision_fetch_protocol_failed",
		)
		return nil, "snapshot_integrity_failed"
	}
	token := openExcelPricingSession(t, server)
	requestID := "snapshot-cancel-failure-race-0001"
	start := authenticatedExcelPricingRequest(
		http.MethodPost,
		"/api/pricing-sync/snapshots",
		validExcelPricingSnapshotStartBody(source, requestID, "fa", 0),
		token,
	)
	start.Header.Set("Idempotency-Key", requestID)
	startResponse := httptest.NewRecorder()
	server.router.ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("snapshot start=%d: %s", startResponse.Code, startResponse.Body.String())
	}
	jobID := excelPricingSnapshotJobIDForTest(t, startResponse.Body.Bytes())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("collector did not start")
	}
	cancel := authenticatedExcelPricingRequest(
		http.MethodDelete,
		"/api/pricing-sync/snapshots/"+jobID,
		"",
		token,
	)
	cancelResponse := httptest.NewRecorder()
	server.router.ServeHTTP(cancelResponse, cancel)
	if cancelResponse.Code != http.StatusAccepted {
		t.Fatalf("cancel=%d: %s", cancelResponse.Code, cancelResponse.Body.String())
	}
	server.setExcelPricingSnapshotFailure(
		jobID,
		excelPricingRemoteSnapshotStageRevisionFetch,
		"snapshot_revision_fetch_protocol_failed",
	)
	statusRequest := authenticatedExcelPricingRequest(
		http.MethodGet,
		"/api/pricing-sync/snapshots/"+jobID,
		"",
		token,
	)
	statusResponse := httptest.NewRecorder()
	server.router.ServeHTTP(statusResponse, statusRequest)
	var cancelling map[string]interface{}
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &cancelling); err != nil {
		t.Fatal(err)
	}
	if cancelling["status"] != "cancelling" || cancelling["code"] != "request_cancelled" {
		t.Fatalf("cancelling status=%#v", cancelling)
	}
	if _, exists := cancelling["failure"]; exists {
		t.Fatalf("cancelling response retained unrelated failure=%#v", cancelling["failure"])
	}
	close(release)
	terminal := waitForExcelPricingSnapshotStatus(t, server, token, jobID, "cancelled")
	if terminal["code"] != "request_cancelled" {
		t.Fatalf("cancelled status=%#v", terminal)
	}
	if _, exists := terminal["failure"]; exists {
		t.Fatalf("cancelled response retained unrelated failure=%#v", terminal["failure"])
	}
}

func assertExcelPricingSnapshotFailureForTest(
	t *testing.T,
	status map[string]interface{},
	publicCode, stage, code string,
) {
	t.Helper()
	if status["code"] != publicCode {
		t.Fatalf("public code=%#v, want %q", status["code"], publicCode)
	}
	failure, ok := status["failure"].(map[string]interface{})
	if !ok || len(failure) != 3 ||
		failure["schema"] != excelPricingSnapshotFailureSchema ||
		failure["stage"] != stage || failure["code"] != code {
		t.Fatalf("structured failure=%#v", status["failure"])
	}
	rendered, err := json.Marshal(failure)
	if err != nil {
		t.Fatal(err)
	}
	for _, protected := range []string{
		"secret", "http://", "https://", "request_id", "build_id", "source_id", "dataset",
	} {
		if strings.Contains(strings.ToLower(string(rendered)), protected) {
			t.Fatalf("structured failure exposed protected material: %s", rendered)
		}
	}
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
		SchemaVersion:         1,
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
		if fixture.mode == "revision_wrong_content_type" {
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		}
		if fixture.mode == "cdn_rewritten_etag" {
			body, err := json.Marshal(fixture.revision)
			if err != nil {
				http.Error(w, "revision fixture", http.StatusInternalServerError)
				return
			}
			fixture.writeIdentityBoundRepresentation(
				w, r, body, fixture.revision.StateRevision, "revision",
			)
			return
		}
		w.Header().Set("ETag", `"`+fixture.revision.StateRevision+`"`)
		if fixture.mode == "revision_empty_body" {
			return
		}
		if fixture.mode == "revision_oversized_body" {
			_, _ = w.Write(bytes.Repeat([]byte{'x'}, excelPricingRemoteRevisionMaxBytes+1))
			return
		}
		_ = json.NewEncoder(w).Encode(fixture.revision)
	case r.Method == http.MethodPost && r.URL.Path == "/wp-json/digitalogic/pricing/sync/snapshots":
		fixture.mu.Lock()
		fixture.startCalls++
		if fixture.mode == "cdn_rewritten_etag" &&
			r.Header.Get("Accept-Encoding") == excelPricingRemoteIdentityEncoding {
			fixture.headerFailure = "snapshot start unexpectedly forced identity encoding"
		}
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
		if fixture.mode == "start_rejected" {
			http.Error(w, "rejected", http.StatusConflict)
			return
		}
		build := fixture.buildResponse()
		if fixture.mode == "ready" || fixture.mode == "cdn_rewritten_etag" ||
			fixture.mode == "payload_wrong_content_type" ||
			fixture.mode == "payload_empty_body" || fixture.mode == "payload_oversized_body" ||
			fixture.mode == "payload_present_empty_etag" || fixture.mode == "payload_duplicate_etag" ||
			fixture.mode == "payload_wrong_strong_etag" ||
			strings.HasPrefix(fixture.mode, "payload_missing_etag_") {
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
		if fixture.mode == "event_after_response" || fixture.mode == "event_bad_match" ||
			fixture.mode == "event_failed" {
			time.AfterFunc(10*time.Millisecond, func() {
				event := fixture.terminalEvent(1)
				switch fixture.mode {
				case "event_bad_match":
					event.BuildID = "build_0000000000000002"
				case "event_failed":
					event.Status = "failed"
					event.SnapshotToken = ""
					event.SnapshotRevision = ""
					event.Digest = ""
					event.SnapshotPath = ""
					event.Code = "digitalogic_pricing_snapshot_test_failed"
					event.Retryable = true
				}
				_ = fixture.hub.publishAuthenticated(event)
			})
		}
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/wp-json/digitalogic/pricing/sync/builds/"):
		fixture.mu.Lock()
		fixture.statusCalls++
		statusCalls := fixture.statusCalls
		fixture.mu.Unlock()
		if fixture.mode == "poll_ready" {
			w.Header().Set("Content-Type", "application/json")
			build := fixture.buildResponse()
			if statusCalls < 2 {
				build.Status = "running"
				build.SnapshotToken = ""
				build.Revision = ""
				build.SnapshotRevision = ""
				build.Digest = ""
				build.SnapshotURL = ""
				w.WriteHeader(http.StatusAccepted)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			_ = json.NewEncoder(w).Encode(build)
			return
		}
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
		if strings.HasPrefix(fixture.mode, "payload_missing_etag_") {
			if r.Header.Get("If-None-Match") == "" {
				_, _ = w.Write(fixture.payloadBody)
				return
			}
			fixture.mu.Lock()
			fixture.payloadConditionalCalls++
			if r.Header.Get("Accept-Encoding") != excelPricingRemoteIdentityEncoding ||
				r.Header.Get("If-None-Match") != `"`+fixture.payload.Digest+`"` {
				fixture.headerFailure = "conditional payload validator headers are invalid"
			}
			fixture.mu.Unlock()
			switch fixture.mode {
			case "payload_missing_etag_verified":
				w.WriteHeader(http.StatusNotModified)
			case "payload_missing_etag_non304":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(fixture.payloadBody)
			case "payload_missing_etag_wrong_confirmation_etag":
				w.Header().Set("ETag", `"`+testExcelPricingRevision('b')+`"`)
				w.WriteHeader(http.StatusNotModified)
			case "payload_missing_etag_duplicate_confirmation_etag":
				w.Header().Add("ETag", `"`+fixture.payload.Digest+`"`)
				w.Header().Add("ETag", `"`+fixture.payload.Digest+`"`)
				w.WriteHeader(http.StatusNotModified)
			default:
				http.Error(w, "unknown missing ETag mode", http.StatusInternalServerError)
			}
			return
		}
		if fixture.mode == "payload_present_empty_etag" {
			w.Header()["ETag"] = []string{""}
			_, _ = w.Write(fixture.payloadBody)
			return
		}
		if fixture.mode == "payload_duplicate_etag" {
			w.Header().Add("ETag", `"`+fixture.payload.Digest+`"`)
			w.Header().Add("ETag", `"`+fixture.payload.Digest+`"`)
			_, _ = w.Write(fixture.payloadBody)
			return
		}
		if fixture.mode == "payload_wrong_strong_etag" {
			w.Header().Set("ETag", `"`+testExcelPricingRevision('b')+`"`)
			_, _ = w.Write(fixture.payloadBody)
			return
		}
		if fixture.mode == "cdn_rewritten_etag" {
			fixture.writeIdentityBoundRepresentation(
				w, r, fixture.payloadBody, fixture.payload.Digest, "payload",
			)
			return
		}
		if fixture.badETag {
			w.Header().Set("ETag", `W/"`+fixture.payload.Digest+`"`)
		} else {
			w.Header().Set("ETag", `"`+fixture.payload.Digest+`"`)
		}
		if fixture.mode == "payload_wrong_content_type" {
			w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
		}
		if fixture.mode == "payload_empty_body" {
			return
		}
		if fixture.mode == "payload_oversized_body" {
			_, _ = w.Write(bytes.Repeat([]byte{'x'}, excelPricingRemoteSnapshotMaxResponseBytes+1))
			return
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

func (fixture *excelPricingRemoteSnapshotFixture) writeIdentityBoundRepresentation(
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	originRevision string,
	kind string,
) {
	encoding := r.Header.Get("Accept-Encoding")
	fixture.mu.Lock()
	switch {
	case kind == "revision" && encoding == excelPricingRemoteIdentityEncoding:
		fixture.revisionIdentityCalls++
	case kind == "revision" && strings.Contains(encoding, "gzip"):
		fixture.revisionCompressedCalls++
	case kind == "payload" && encoding == excelPricingRemoteIdentityEncoding:
		fixture.payloadIdentityCalls++
	case kind == "payload" && strings.Contains(encoding, "gzip"):
		fixture.payloadCompressedCalls++
	default:
		fixture.headerFailure = "identity-bound response received an unexpected content encoding"
	}
	fixture.mu.Unlock()

	if encoding == excelPricingRemoteIdentityEncoding {
		w.Header().Set("ETag", `"`+originRevision+`"`)
		_, _ = w.Write(body)
		return
	}

	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("ETag", `"cdn-gzip-representation"`)
	compressed := gzip.NewWriter(w)
	_, _ = compressed.Write(body)
	_ = compressed.Close()
}

func (fixture *excelPricingRemoteSnapshotFixture) validSourceQuery(query url.Values) bool {
	return query.Get("source_id") == fixture.source.ID &&
		query.Get("source_dataset") == fixture.source.Dataset &&
		query.Get("source_revision") == fixture.source.Revision &&
		query.Get("locale") == "fa" &&
		query.Get("page_size") == strconv.Itoa(excelPricingSnapshotPageSize) &&
		query.Get("schema_version") == "1"
}

func (fixture *excelPricingRemoteSnapshotFixture) buildResponse() excelPricingRemoteSnapshotBuildResponse {
	snapshotURL := "/wp-json/digitalogic/pricing/sync/snapshots/" + fixture.payload.SnapshotToken + fixture.sourceQuery()
	if fixture.badSnapshotURL != "" {
		snapshotURL = fixture.badSnapshotURL
	}
	return excelPricingRemoteSnapshotBuildResponse{
		Schema:               excelPricingRemoteSnapshotBuildSchema,
		SchemaVersion:        1,
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
		SchemaVersion:        1,
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
	columns := make([]map[string]json.RawMessage, len(excelPricingRemoteSnapshotExcelV1Fields))
	row := make(map[string]interface{}, len(excelPricingRemoteSnapshotExcelV1Fields))
	for index, field := range excelPricingRemoteSnapshotExcelV1Fields {
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
		SchemaVersion:         1,
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
		SchemaVersion         int               `json:"schema_version"`
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
		1, payload.StateRevision, payload.PricingStateRevision,
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

func (fixture *excelPricingRemoteSnapshotFixture) assertRepresentationCalls(
	t *testing.T,
	revisionIdentity, revisionCompressed, payloadIdentity, payloadCompressed int,
) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.revisionIdentityCalls != revisionIdentity ||
		fixture.revisionCompressedCalls != revisionCompressed ||
		fixture.payloadIdentityCalls != payloadIdentity ||
		fixture.payloadCompressedCalls != payloadCompressed {
		t.Fatalf(
			"representation calls = revision identity:%d compressed:%d payload identity:%d compressed:%d",
			fixture.revisionIdentityCalls,
			fixture.revisionCompressedCalls,
			fixture.payloadIdentityCalls,
			fixture.payloadCompressedCalls,
		)
	}
	if fixture.headerFailure != "" {
		t.Fatal(fixture.headerFailure)
	}
}

func (fixture *excelPricingRemoteSnapshotFixture) assertConditionalPayloadCalls(
	t *testing.T,
	want int,
) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.payloadConditionalCalls != want {
		t.Fatalf("conditional payload calls = %d, want %d", fixture.payloadConditionalCalls, want)
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
