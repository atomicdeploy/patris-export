package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
	"github.com/gorilla/websocket"
)

const excelPricingRemoteTestSecret = "companion-test-secret-value"

func excelPricingRemoteTestRevision(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func excelPricingRemoteTestIndexedRevision(index int) string {
	return fmt.Sprintf("sha256:%064x", index)
}

func excelPricingRemoteTestSource() canonical.Source {
	return canonical.Source{
		ID:       "patris-office",
		Dataset:  "kala.db",
		Revision: excelPricingRemoteTestRevision("a"),
	}
}

func excelPricingRemoteTestLifecycle(excelPricingRemoteSourceLifecycle) error {
	return nil
}

func excelPricingRemoteTestConfig(t *testing.T, baseURL string) updateout.Config {
	t.Helper()
	t.Setenv("PATRIS_PRICING_EVENTS_TEST_SECRET", excelPricingRemoteTestSecret)
	return updateout.Config{
		Enabled:              true,
		URL:                  baseURL + "/wp-json/digitalogic/receiver/v1/product-sync",
		Method:               http.MethodPost,
		Format:               "json",
		Timeout:              "2s",
		ProductSyncSecretEnv: "PATRIS_PRICING_EVENTS_TEST_SECRET",
	}
}

func excelPricingRemoteTestRevisionPayload(source canonical.Source, stateRevision string) excelPricingRemoteRevisionResponse {
	return excelPricingRemoteRevisionResponse{
		Schema:                excelPricingRemoteRevisionSchema,
		SchemaVersion:         1,
		Projection:            excelPricingRemoteProjection,
		ProjectionSchema:      excelPricingRemoteProjectionSchema,
		StateRevision:         stateRevision,
		Source:                source,
		CatalogRevision:       excelPricingRemoteTestRevision("c"),
		PricingStateRevision:  excelPricingRemoteTestRevision("d"),
		PricingPolicyRevision: excelPricingRemoteTestRevision("e"),
		Locale:                "fa",
		PageSize:              excelPricingSnapshotPageSize,
	}
}

func excelPricingRemoteWriteCurrentRevision(
	w http.ResponseWriter,
	source canonical.Source,
	stateRevision string,
) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", `"`+stateRevision+`"`)
	_ = json.NewEncoder(w).Encode(excelPricingRemoteTestRevisionPayload(source, stateRevision))
}

func excelPricingRemoteWriteSupersededRevision(
	w http.ResponseWriter,
	requested canonical.Source,
	currentRevision string,
) {
	success := false
	retryable := false
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(excelPricingRemoteErrorResponse{
		Success:   &success,
		Code:      "digitalogic_pricing_snapshot_source_revision_conflict",
		Message:   "requested source revision is no longer current",
		Retryable: &retryable,
		Details: excelPricingRemoteErrorDetails{
			SubmittedSourceRevision: &requested.Revision,
			CurrentSourceRevision:   &currentRevision,
			Retryable:               &retryable,
		},
	})
}

func excelPricingRemoteWriteAbsentRevision(w http.ResponseWriter) {
	empty := ""
	success := false
	retryable := false
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(excelPricingRemoteErrorResponse{
		Success:   &success,
		Code:      "digitalogic_excel_sync_source_scope_conflict",
		Message:   "source is no longer materialized",
		Retryable: &retryable,
		Details: excelPricingRemoteErrorDetails{
			CurrentSource: &excelPricingRemoteErrorSource{
				ID: &empty, Dataset: &empty, Revision: &empty,
			},
		},
	})
}

func excelPricingRemoteTestStateFrame(source canonical.Source, eventID uint64, stateRevision string) map[string]interface{} {
	return map[string]interface{}{
		// This deliberately mirrors the WordPress PHP bridge fixture: the
		// transport event uses an underscore while name is the dotted event.
		"event":   "pricing_state_changed",
		"name":    "pricing.state.changed",
		"success": true,
		"time":    "2026-08-23T00:00:00Z",
		"id":      eventID,
		"data": map[string]interface{}{
			"schema":                  excelPricingRemoteStateEventSchema,
			"schema_version":          1,
			"projection":              excelPricingRemoteProjection,
			"source":                  source,
			"state_revision":          stateRevision,
			"etag":                    `"` + stateRevision + `"`,
			"catalog_revision":        excelPricingRemoteTestRevision("c"),
			"pricing_state_revision":  excelPricingRemoteTestRevision("d"),
			"pricing_policy_revision": excelPricingRemoteTestRevision("e"),
			"cause":                   "projection-invalidated",
			"idempotency_key":         excelPricingRemoteTestRevision("f"),
			"revision_path":           "/wp-json/digitalogic/pricing/sync/revision",
			"audience": map[string]interface{}{
				"services": []string{"patris_pricing"},
			},
		},
	}
}

func excelPricingRemoteTestSourceFrame(
	source canonical.Source,
	eventID uint64,
	name string,
	change string,
	previous interface{},
	idempotencyKey string,
) map[string]interface{} {
	return map[string]interface{}{
		"event":   name,
		"name":    name,
		"success": true,
		"time":    "2026-08-26T00:00:00Z",
		"id":      eventID,
		"data": map[string]interface{}{
			"schema":                       excelPricingRemoteSourceEventSchema,
			"schema_version":               1,
			"projection":                   excelPricingRemoteProjection,
			"change":                       change,
			"source":                       source,
			"previous_source_revision":     previous,
			"idempotency_key":              idempotencyKey,
			"revision_validation_required": true,
			"revision_path":                "/wp-json/digitalogic/pricing/sync/revision",
			"audience": map[string]interface{}{
				"services": []string{"patris_pricing"},
			},
		},
	}
}

func excelPricingRemoteTestTerminalFrame(
	source canonical.Source,
	eventID uint64,
	stateRevision string,
) map[string]interface{} {
	return map[string]interface{}{
		"event":   "pricing.snapshot.build.terminal",
		"name":    "pricing.snapshot.build.terminal",
		"success": true,
		"time":    "2026-08-26T00:00:01Z",
		"id":      eventID,
		"data": map[string]interface{}{
			"schema":                 excelPricingRemoteSnapshotEventSchema,
			"schema_version":         1,
			"build_id":               "build_source_event_continuity",
			"request_id":             "request_source_event_continuity",
			"status":                 "failed",
			"source":                 source,
			"state_revision":         stateRevision,
			"pricing_state_revision": excelPricingRemoteTestRevision("d"),
			"catalog_revision":       excelPricingRemoteTestRevision("c"),
			"code":                   "source_event_fixture_failure",
			"retryable":              false,
			"idempotency_key":        excelPricingRemoteTestRevision("1"),
		},
	}
}

func excelPricingRemoteTestConnectedFrame(
	cursor uint64,
	oldest uint64,
	latest uint64,
	reset bool,
) map[string]interface{} {
	return map[string]interface{}{
		"event":   "connected",
		"success": true,
		"data": map[string]interface{}{
			"principal":                    "patris_pricing",
			"cursor":                       cursor,
			"oldest_event_id":              oldest,
			"latest_event_id":              latest,
			"cursor_reset_required":        reset,
			"revision_validation_required": true,
			"revision_path":                "/wp-json/digitalogic/pricing/sync/revision",
		},
	}
}

func excelPricingRemoteWriteJSON(t *testing.T, connection *websocket.Conn, value interface{}) {
	t.Helper()
	if err := connection.WriteJSON(value); err != nil {
		t.Errorf("write websocket fixture: %v", err)
	}
}

func TestExcelPricingRemoteEventEndpointsAreSameHostAndSecretFree(t *testing.T) {
	webSocketURL, revisionURL, revisionPath, err := excelPricingRemoteEventEndpoints(
		"https://digitalogic.example/wp-json/digitalogic/receiver/v1/product-sync",
	)
	if err != nil {
		t.Fatal(err)
	}
	if webSocketURL != "wss://digitalogic.example/wordpress-ws" {
		t.Fatalf("websocket URL = %q", webSocketURL)
	}
	if revisionURL != "https://digitalogic.example/wp-json/digitalogic/pricing/sync/revision" ||
		revisionPath != "/wp-json/digitalogic/pricing/sync/revision" {
		t.Fatalf("revision endpoint = %q path=%q", revisionURL, revisionPath)
	}
	if strings.Contains(webSocketURL+revisionURL, "secret") {
		t.Fatal("credential material crossed into an endpoint")
	}
	if _, _, _, err := excelPricingRemoteEventEndpoints(
		"http://digitalogic.example/wp-json/digitalogic/receiver/v1/product-sync",
	); err == nil {
		t.Fatal("remote plaintext destination was accepted")
	}
}

func TestExcelPricingRemoteEventsConnectValidateAndConsumePHPFrame(t *testing.T) {
	source := excelPricingRemoteTestSource()
	firstState := excelPricingRemoteTestRevision("1")
	secondState := excelPricingRemoteTestRevision("2")
	var revisionCalls atomic.Int32
	handshakeOK := make(chan bool, 1)
	upgrader := websocket.Upgrader{
		Subprotocols: []string{excelPricingRemoteWebSocketProtocol},
		CheckOrigin:  func(*http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-json/digitalogic/pricing/sync/revision":
			revisionCalls.Add(1)
			valid := r.Header.Get(excelPricingRemoteSecretHeader) == excelPricingRemoteTestSecret &&
				r.Header.Get("Accept-Encoding") == excelPricingRemoteIdentityEncoding &&
				r.Header.Get(excelPricingRemoteSourceIDHeader) == source.ID &&
				r.Header.Get(excelPricingRemoteDatasetHeader) == source.Dataset &&
				r.URL.Query().Get("source_id") == source.ID &&
				r.URL.Query().Get("source_dataset") == source.Dataset &&
				r.URL.Query().Get("source_revision") == source.Revision &&
				r.URL.Query().Get("locale") == "fa" &&
				r.URL.Query().Get("page_size") == strconv.Itoa(excelPricingSnapshotPageSize) &&
				r.URL.Query().Get("schema_version") == "1"
			if !valid {
				http.Error(w, "invalid protected request", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=UTF-8")
			w.Header().Set("ETag", `"`+firstState+`"`)
			_ = json.NewEncoder(w).Encode(excelPricingRemoteTestRevisionPayload(source, firstState))
		case "/wordpress-ws":
			valid := r.Header.Get(excelPricingRemoteSecretHeader) == excelPricingRemoteTestSecret &&
				r.Header.Get(excelPricingRemoteSourceIDHeader) == source.ID &&
				r.Header.Get(excelPricingRemoteDatasetHeader) == source.Dataset &&
				r.Header.Get("Last-Event-ID") == "" &&
				r.Header.Get("Sec-WebSocket-Protocol") == excelPricingRemoteWebSocketProtocol
			handshakeOK <- valid
			connection, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer connection.Close()
			excelPricingRemoteWriteJSON(t, connection, map[string]interface{}{
				"event":   "connected",
				"success": true,
				"data": map[string]interface{}{
					"principal":                    "patris_pricing",
					"cursor":                       40,
					"oldest_event_id":              1,
					"latest_event_id":              40,
					"cursor_reset_required":        false,
					"revision_validation_required": true,
					"revision_path":                "/wp-json/digitalogic/pricing/sync/revision",
				},
			})
			excelPricingRemoteWriteJSON(t, connection, excelPricingRemoteTestStateFrame(source, 41, secondState))
			_, _, _ = connection.ReadMessage()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	revisions := make(chan excelPricingRemoteRevision, 4)
	cursors := make(chan uint64, 4)
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL),
		source,
		excelPricingRemoteEventsOptions{
			MinReconnectBackoff: 5 * time.Millisecond,
			MaxReconnectBackoff: 10 * time.Millisecond,
			OnSourceLifecycle:   excelPricingRemoteTestLifecycle,
			OnRevision: func(revision excelPricingRemoteRevision) error {
				revisions <- revision
				return nil
			},
			OnCursor: func(cursor uint64) { cursors <- cursor },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	runResult := make(chan error, 1)
	go func() { runResult <- client.Run(ctx) }()

	for index, want := range []struct {
		origin string
		state  string
		id     uint64
	}{{"connection_validation", firstState, 40}, {"stream_event", secondState, 41}} {
		select {
		case revision := <-revisions:
			if revision.ValidationOrigin != want.origin || revision.StateRevision != want.state ||
				revision.EventID != want.id || revision.Source != source {
				t.Fatalf("revision[%d] = %+v", index, revision)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for revision %d", index)
		}
	}
	if !<-handshakeOK {
		t.Fatal("protected websocket handshake was incomplete")
	}
	if revisionCalls.Load() != 1 {
		t.Fatalf("revision calls = %d, want exactly 1", revisionCalls.Load())
	}
	var lastCursor uint64
	for index := 0; index < 2; index++ {
		select {
		case lastCursor = <-cursors:
		case <-time.After(time.Second):
			t.Fatal("cursor acknowledgement was not delivered")
		}
	}
	if lastCursor != 41 {
		t.Fatalf("last cursor = %d", lastCursor)
	}
	cancel()
	if err := <-runResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v", err)
	}
}

func TestExcelPricingRemoteEventsReconnectUsesCursorAndConditionalRevisionGETWithoutPolling(t *testing.T) {
	source := excelPricingRemoteTestSource()
	state := excelPricingRemoteTestRevision("3")
	etag := `"` + state + `"`
	var connections atomic.Int32
	var revisionCalls atomic.Int32
	secondConnected := make(chan struct{})
	upgrader := websocket.Upgrader{
		Subprotocols: []string{excelPricingRemoteWebSocketProtocol},
		CheckOrigin:  func(*http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-json/digitalogic/pricing/sync/revision":
			call := revisionCalls.Add(1)
			w.Header().Set("ETag", etag)
			if call == 1 {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(excelPricingRemoteTestRevisionPayload(source, state))
				return
			}
			if r.Header.Get("If-None-Match") != etag {
				http.Error(w, "conditional validator missing", http.StatusPreconditionRequired)
				return
			}
			w.WriteHeader(http.StatusNotModified)
		case "/wordpress-ws":
			connectionNumber := connections.Add(1)
			if connectionNumber == 2 && r.Header.Get("Last-Event-ID") != "7" {
				http.Error(w, "cursor missing", http.StatusBadRequest)
				return
			}
			connection, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer connection.Close()
			excelPricingRemoteWriteJSON(t, connection, map[string]interface{}{
				"event":   "connected",
				"success": true,
				"data": map[string]interface{}{
					"principal":                    "patris_pricing",
					"cursor":                       7,
					"oldest_event_id":              1,
					"latest_event_id":              7,
					"cursor_reset_required":        false,
					"revision_validation_required": true,
					"revision_path":                "/wp-json/digitalogic/pricing/sync/revision",
				},
			})
			if connectionNumber == 1 {
				return
			}
			close(secondConnected)
			_, _, _ = connection.ReadMessage()
		}
	}))
	defer server.Close()

	var accepted atomic.Int32
	client, err := newExcelPricingRemoteEventsClient(excelPricingRemoteTestConfig(t, server.URL), source,
		excelPricingRemoteEventsOptions{
			MinReconnectBackoff: 2 * time.Millisecond,
			MaxReconnectBackoff: 5 * time.Millisecond,
			OnSourceLifecycle:   excelPricingRemoteTestLifecycle,
			OnRevision: func(excelPricingRemoteRevision) error {
				accepted.Add(1)
				return nil
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- client.Run(ctx) }()
	select {
	case <-secondConnected:
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not reconnect")
	}
	time.Sleep(40 * time.Millisecond)
	if revisionCalls.Load() != 2 || accepted.Load() != 1 {
		t.Fatalf("revision calls=%d accepted=%d", revisionCalls.Load(), accepted.Load())
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v", err)
	}
}

func TestExcelPricingRemoteEventsStreamResetTriggersOneConditionalValidation(t *testing.T) {
	source := excelPricingRemoteTestSource()
	state := excelPricingRemoteTestRevision("4")
	etag := `"` + state + `"`
	var revisionCalls atomic.Int32
	resetWritten := make(chan struct{})
	upgrader := websocket.Upgrader{
		Subprotocols: []string{excelPricingRemoteWebSocketProtocol},
		CheckOrigin:  func(*http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-json/digitalogic/pricing/sync/revision":
			call := revisionCalls.Add(1)
			w.Header().Set("ETag", etag)
			if call == 1 {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(excelPricingRemoteTestRevisionPayload(source, state))
				return
			}
			if r.Header.Get("If-None-Match") != etag {
				http.Error(w, "missing validator", http.StatusPreconditionRequired)
				return
			}
			w.WriteHeader(http.StatusNotModified)
		case "/wordpress-ws":
			connection, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer connection.Close()
			excelPricingRemoteWriteJSON(t, connection, map[string]interface{}{
				"event":   "connected",
				"success": true,
				"data": map[string]interface{}{
					"principal":                    "patris_pricing",
					"cursor":                       10,
					"oldest_event_id":              1,
					"latest_event_id":              10,
					"revision_validation_required": true,
					"revision_path":                "/wp-json/digitalogic/pricing/sync/revision",
				},
			})
			excelPricingRemoteWriteJSON(t, connection, map[string]interface{}{
				"event":   "pricing.stream.reset",
				"success": true,
				"data": map[string]interface{}{
					"schema":                       excelPricingRemoteStreamResetSchema,
					"schema_version":               1,
					"reason":                       "cursor_gap",
					"cursor":                       20,
					"oldest_event_id":              15,
					"latest_event_id":              20,
					"revision_validation_required": true,
					"revision_path":                "/wp-json/digitalogic/pricing/sync/revision",
				},
			})
			close(resetWritten)
			_, _, _ = connection.ReadMessage()
		}
	}))
	defer server.Close()

	client, err := newExcelPricingRemoteEventsClient(excelPricingRemoteTestConfig(t, server.URL), source,
		excelPricingRemoteEventsOptions{
			MinReconnectBackoff: 2 * time.Millisecond,
			MaxReconnectBackoff: 5 * time.Millisecond,
			OnRevision:          func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle:   excelPricingRemoteTestLifecycle,
		})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- client.Run(ctx) }()
	select {
	case <-resetWritten:
	case <-time.After(2 * time.Second):
		t.Fatal("reset was not delivered")
	}
	deadline := time.Now().Add(time.Second)
	for revisionCalls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	if revisionCalls.Load() != 2 || client.currentCursor() != 20 {
		t.Fatalf("revision calls=%d cursor=%d", revisionCalls.Load(), client.currentCursor())
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v", err)
	}
}

func TestExcelPricingRemoteConnectedCursorResetMayClampAHighPersistedCursor(t *testing.T) {
	source := excelPricingRemoteTestSource()
	state := excelPricingRemoteTestRevision("b")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"`+state+`"`)
		_ = json.NewEncoder(w).Encode(excelPricingRemoteTestRevisionPayload(source, state))
	}))
	defer server.Close()
	client, err := newExcelPricingRemoteEventsClient(excelPricingRemoteTestConfig(t, server.URL), source,
		excelPricingRemoteEventsOptions{
			InitialCursor:     99,
			OnRevision:        func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: excelPricingRemoteTestLifecycle,
		})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal(map[string]interface{}{
		"event":   "connected",
		"success": true,
		"data": map[string]interface{}{
			"principal":                    "patris_pricing",
			"cursor":                       10,
			"oldest_event_id":              1,
			"latest_event_id":              10,
			"cursor_reset_required":        true,
			"revision_validation_required": true,
			"revision_path":                "/wp-json/digitalogic/pricing/sync/revision",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	connected, err := client.handleExcelPricingRemoteFrame(t.Context(), frame, false)
	if err != nil || !connected {
		t.Fatalf("connected=%t err=%v", connected, err)
	}
	if client.currentCursor() != 10 {
		t.Fatalf("reset cursor = %d, want clamped 10", client.currentCursor())
	}
}

func TestExcelPricingRemoteConnectedLowCursorResetSkipsRetainedStaleFrames(t *testing.T) {
	sourceA := excelPricingRemoteTestSource()
	sourceB := sourceA
	sourceB.Revision = excelPricingRemoteTestRevision("b")
	sourceC := sourceA
	sourceC.Revision = excelPricingRemoteTestRevision("c")
	stateC := excelPricingRemoteTestRevision("5")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("If-None-Match") != "" {
			t.Error("cursor-reset reconciliation used If-None-Match")
		}
		requested := sourceA
		requested.Revision = r.URL.Query().Get("source_revision")
		if requested == sourceC {
			excelPricingRemoteWriteCurrentRevision(w, sourceC, stateC)
			return
		}
		excelPricingRemoteWriteSupersededRevision(w, requested, sourceC.Revision)
	}))
	defer server.Close()
	var lifecycles []excelPricingRemoteSourceLifecycle
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL), sourceA,
		excelPricingRemoteEventsOptions{
			InitialCursor: 2,
			OnRevision:    func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: func(lifecycle excelPricingRemoteSourceLifecycle) error {
				lifecycles = append(lifecycles, lifecycle)
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	handle := func(frame map[string]interface{}, connected bool) (bool, error) {
		body, marshalErr := json.Marshal(frame)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return client.handleExcelPricingRemoteFrame(t.Context(), body, connected)
	}
	connected, err := handle(excelPricingRemoteTestConnectedFrame(5, 6, 10, true), false)
	current, present := client.currentStreamSource()
	if err != nil || !connected || current != sourceC || !present || client.currentCursor() != 10 ||
		requests.Load() != 2 || len(lifecycles) != 1 || lifecycles[0].Mode != "reconcile_present" {
		t.Fatalf("connected=%v err=%v source=%+v present=%v cursor=%d requests=%d lifecycles=%+v",
			connected, err, current, present, client.currentCursor(), requests.Load(), lifecycles)
	}
	if _, err := handle(excelPricingRemoteTestSourceFrame(
		sourceB, 6, "pricing.source.changed", "changed", sourceA.Revision,
		excelPricingRemoteTestRevision("1"),
	), true); err != nil {
		t.Fatalf("retained stale lifecycle frame: %v", err)
	}
	current, present = client.currentStreamSource()
	if current != sourceC || !present || client.currentCursor() != 10 ||
		requests.Load() != 2 || len(lifecycles) != 1 {
		t.Fatalf("source=%+v present=%v cursor=%d requests=%d lifecycles=%+v",
			current, present, client.currentCursor(), requests.Load(), lifecycles)
	}
}

func TestExcelPricingRemoteEventCursorAdvancesOnlyAfterAtomicAcceptance(t *testing.T) {
	source := excelPricingRemoteTestSource()
	state := excelPricingRemoteTestRevision("5")
	cfg := excelPricingRemoteTestConfig(t, "http://127.0.0.1:18080")
	client, err := newExcelPricingRemoteEventsClient(cfg, source, excelPricingRemoteEventsOptions{
		InitialCursor:     5,
		OnSourceLifecycle: excelPricingRemoteTestLifecycle,
		OnRevision: func(excelPricingRemoteRevision) error {
			return errors.New("local generation was not invalidated")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(excelPricingRemoteTestStateFrame(source, 6, state))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.handleExcelPricingRemoteFrame(t.Context(), body, true); !errors.Is(err, errExcelPricingRemoteRevision) {
		t.Fatalf("frame error = %v", err)
	}
	if client.currentCursor() != 5 {
		t.Fatalf("failed acceptance advanced cursor to %d", client.currentCursor())
	}
	client.onRevision = func(excelPricingRemoteRevision) error { return nil }
	if _, err := client.handleExcelPricingRemoteFrame(t.Context(), body, true); err != nil {
		t.Fatal(err)
	}
	if client.currentCursor() != 6 {
		t.Fatalf("accepted event cursor = %d", client.currentCursor())
	}
}

func TestExcelPricingRemoteSourceChangeStateAndTerminalPreserveOrder(t *testing.T) {
	sourceA := excelPricingRemoteTestSource()
	sourceB := sourceA
	sourceB.Revision = excelPricingRemoteTestRevision("b")
	stateB := excelPricingRemoteTestRevision("5")
	var requests atomic.Int32
	var conditional atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		conditional.Store(conditional.Load() || r.Header.Get("If-None-Match") != "")
		excelPricingRemoteWriteCurrentRevision(w, sourceB, stateB)
	}))
	defer server.Close()

	var lifecycleCalls atomic.Int32
	var revisionCalls atomic.Int32
	var terminalCalls atomic.Int32
	var client *excelPricingRemoteEventsClient
	var err error
	client, err = newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL), sourceA,
		excelPricingRemoteEventsOptions{
			OnRevision: func(revision excelPricingRemoteRevision) error {
				if revision.Source != sourceB {
					return errors.New("state crossed before source transition")
				}
				revisionCalls.Add(1)
				return nil
			},
			OnSourceLifecycle: func(lifecycle excelPricingRemoteSourceLifecycle) error {
				current, present := client.currentStreamSource()
				if current != sourceA || !present || lifecycle.Source != sourceB || lifecycle.Revision == nil {
					return errors.New("source lifecycle was not delivered before mutation")
				}
				lifecycleCalls.Add(1)
				return nil
			},
			OnSnapshotTerminal: func(event excelPricingRemoteSnapshotTerminalEvent) error {
				if event.Source != sourceB {
					return errors.New("wrong terminal source")
				}
				terminalCalls.Add(1)
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	handle := func(frame map[string]interface{}) error {
		body, marshalErr := json.Marshal(frame)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		_, frameErr := client.handleExcelPricingRemoteFrame(t.Context(), body, true)
		return frameErr
	}
	if err := handle(excelPricingRemoteTestSourceFrame(
		sourceB, 1, "pricing.source.changed", "changed", sourceA.Revision,
		excelPricingRemoteTestRevision("6"),
	)); err != nil {
		t.Fatalf("source A->B: %v", err)
	}
	if err := handle(excelPricingRemoteTestStateFrame(sourceB, 2, stateB)); err != nil {
		t.Fatalf("state B: %v", err)
	}
	if err := handle(excelPricingRemoteTestTerminalFrame(sourceB, 3, stateB)); err != nil {
		t.Fatalf("terminal B: %v", err)
	}
	current, present := client.currentStreamSource()
	if current != sourceB || !present || client.currentCursor() != 3 || requests.Load() != 1 ||
		conditional.Load() || lifecycleCalls.Load() != 1 || revisionCalls.Load() != 1 ||
		terminalCalls.Load() != 1 {
		t.Fatalf("source=%+v present=%v cursor=%d requests=%d conditional=%v lifecycle=%d revision=%d terminal=%d",
			current, present, client.currentCursor(), requests.Load(), conditional.Load(),
			lifecycleCalls.Load(), revisionCalls.Load(), terminalCalls.Load())
	}
}

func TestExcelPricingRemoteSourceCallbackFailureReplaysWithoutCursorOrStateAdvance(t *testing.T) {
	sourceA := excelPricingRemoteTestSource()
	sourceB := sourceA
	sourceB.Revision = excelPricingRemoteTestRevision("b")
	stateB := excelPricingRemoteTestRevision("5")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("If-None-Match") != "" {
			t.Error("lifecycle validation unexpectedly became conditional")
		}
		excelPricingRemoteWriteCurrentRevision(w, sourceB, stateB)
	}))
	defer server.Close()
	var callbacks atomic.Int32
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL), sourceA,
		excelPricingRemoteEventsOptions{
			OnRevision: func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: func(excelPricingRemoteSourceLifecycle) error {
				if callbacks.Add(1) == 1 {
					return errors.New("injected lifecycle failure")
				}
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(excelPricingRemoteTestSourceFrame(
		sourceB, 1, "pricing.source.changed", "changed", sourceA.Revision,
		excelPricingRemoteTestRevision("6"),
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.handleExcelPricingRemoteFrame(t.Context(), body, true); !errors.Is(err, errExcelPricingRemoteRevision) {
		t.Fatalf("first callback error=%v", err)
	}
	current, present := client.currentStreamSource()
	if current != sourceA || !present || client.currentCursor() != 0 {
		t.Fatalf("failed callback mutated source=%+v present=%v cursor=%d", current, present, client.currentCursor())
	}
	if _, err := client.handleExcelPricingRemoteFrame(t.Context(), body, true); err != nil {
		t.Fatalf("replayed callback: %v", err)
	}
	current, present = client.currentStreamSource()
	if current != sourceB || !present || client.currentCursor() != 1 ||
		callbacks.Load() != 2 || requests.Load() != 2 {
		t.Fatalf("replay source=%+v present=%v cursor=%d callbacks=%d requests=%d",
			current, present, client.currentCursor(), callbacks.Load(), requests.Load())
	}
}

func TestExcelPricingRemoteSupersededLifecycleAdvancesOnlyThroughQueuedTransitions(t *testing.T) {
	sourceA := excelPricingRemoteTestSource()
	sourceB := sourceA
	sourceB.Revision = excelPricingRemoteTestRevision("b")
	sourceC := sourceA
	sourceC.Revision = excelPricingRemoteTestRevision("c")
	stateC := excelPricingRemoteTestRevision("5")
	var requests []string
	var requestMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested := canonical.Source{
			ID:       r.URL.Query().Get("source_id"),
			Dataset:  r.URL.Query().Get("source_dataset"),
			Revision: r.URL.Query().Get("source_revision"),
		}
		requestMu.Lock()
		requests = append(requests, requested.Revision+"|"+r.Header.Get("If-None-Match"))
		requestMu.Unlock()
		if requested == sourceC {
			excelPricingRemoteWriteCurrentRevision(w, sourceC, stateC)
			return
		}
		excelPricingRemoteWriteSupersededRevision(w, requested, sourceC.Revision)
	}))
	defer server.Close()
	var lifecycles []excelPricingRemoteSourceLifecycle
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL), sourceA,
		excelPricingRemoteEventsOptions{
			OnRevision: func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: func(lifecycle excelPricingRemoteSourceLifecycle) error {
				lifecycles = append(lifecycles, lifecycle)
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	handle := func(source canonical.Source, id uint64, previous, key string) {
		t.Helper()
		body, marshalErr := json.Marshal(excelPricingRemoteTestSourceFrame(
			source, id, "pricing.source.changed", "changed", previous, key,
		))
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, frameErr := client.handleExcelPricingRemoteFrame(t.Context(), body, true); frameErr != nil {
			t.Fatalf("source event %d: %v", id, frameErr)
		}
	}
	handle(sourceB, 1, sourceA.Revision, excelPricingRemoteTestRevision("6"))
	current, present := client.currentStreamSource()
	if current != sourceB || !present || lifecycles[0].ValidationOutcome != excelPricingRemoteSourceSuperseded ||
		lifecycles[0].CurrentSourceRevision != sourceC.Revision || lifecycles[0].Revision != nil {
		t.Fatalf("superseded B lifecycle=%+v source=%+v present=%v", lifecycles[0], current, present)
	}
	handle(sourceC, 2, sourceB.Revision, excelPricingRemoteTestRevision("7"))
	current, present = client.currentStreamSource()
	if current != sourceC || !present || client.currentCursor() != 2 || len(lifecycles) != 2 ||
		lifecycles[1].Revision == nil {
		t.Fatalf("final lifecycle source=%+v present=%v cursor=%d lifecycles=%+v",
			current, present, client.currentCursor(), lifecycles)
	}
	requestMu.Lock()
	defer requestMu.Unlock()
	if len(requests) != 2 || requests[0] != sourceB.Revision+"|" || requests[1] != sourceC.Revision+"|" {
		t.Fatalf("lifecycle requests=%v", requests)
	}
}

func TestExcelPricingRemoteProcessesMoreThanProducerHistoryWithoutJumpingToFinalSource(t *testing.T) {
	source := excelPricingRemoteTestSource()
	finalSource := source
	finalSource.Revision = excelPricingRemoteTestIndexedRevision(205)
	state := excelPricingRemoteTestRevision("5")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("If-None-Match") != "" {
			t.Error("ordered lifecycle request used If-None-Match")
		}
		requested := source
		requested.Revision = r.URL.Query().Get("source_revision")
		if requested == finalSource {
			excelPricingRemoteWriteCurrentRevision(w, finalSource, state)
			return
		}
		excelPricingRemoteWriteSupersededRevision(w, requested, finalSource.Revision)
	}))
	defer server.Close()
	var callbacks atomic.Int32
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL), source,
		excelPricingRemoteEventsOptions{
			OnRevision: func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: func(excelPricingRemoteSourceLifecycle) error {
				callbacks.Add(1)
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	previous := source
	for index := 1; index <= 205; index++ {
		next := source
		next.Revision = excelPricingRemoteTestIndexedRevision(index)
		body, marshalErr := json.Marshal(excelPricingRemoteTestSourceFrame(
			next, uint64(index), "pricing.source.changed", "changed", previous.Revision,
			excelPricingRemoteTestIndexedRevision(1000+index),
		))
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, frameErr := client.handleExcelPricingRemoteFrame(t.Context(), body, true); frameErr != nil {
			t.Fatalf("transition %d: %v", index, frameErr)
		}
		current, present := client.currentStreamSource()
		if current != next || !present {
			t.Fatalf("transition %d jumped to source=%+v present=%v", index, current, present)
		}
		previous = next
	}
	if client.currentCursor() != 205 || callbacks.Load() != 205 || requests.Load() != 205 {
		t.Fatalf("cursor=%d callbacks=%d requests=%d",
			client.currentCursor(), callbacks.Load(), requests.Load())
	}
}

func TestExcelPricingRemoteSameRevisionRemoveAddRemoveAndOldTerminal(t *testing.T) {
	source := excelPricingRemoteTestSource()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		excelPricingRemoteWriteAbsentRevision(w)
	}))
	defer server.Close()
	var lifecycles atomic.Int32
	var terminals atomic.Int32
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL), source,
		excelPricingRemoteEventsOptions{
			OnRevision: func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: func(excelPricingRemoteSourceLifecycle) error {
				lifecycles.Add(1)
				return nil
			},
			OnSnapshotTerminal: func(excelPricingRemoteSnapshotTerminalEvent) error {
				terminals.Add(1)
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	frames := []map[string]interface{}{
		excelPricingRemoteTestSourceFrame(source, 1, "pricing.source.removed", "removed", source.Revision, excelPricingRemoteTestRevision("1")),
		excelPricingRemoteTestSourceFrame(source, 2, "pricing.source.changed", "added", nil, excelPricingRemoteTestRevision("2")),
		excelPricingRemoteTestSourceFrame(source, 3, "pricing.source.removed", "removed", source.Revision, excelPricingRemoteTestRevision("3")),
		excelPricingRemoteTestTerminalFrame(source, 4, excelPricingRemoteTestRevision("5")),
	}
	for index, frame := range frames {
		body, marshalErr := json.Marshal(frame)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, frameErr := client.handleExcelPricingRemoteFrame(t.Context(), body, true); frameErr != nil {
			t.Fatalf("frame %d: %v", index+1, frameErr)
		}
	}
	current, present := client.currentStreamSource()
	if current != source || present || client.currentCursor() != 4 ||
		lifecycles.Load() != 3 || terminals.Load() != 1 {
		t.Fatalf("source=%+v present=%v cursor=%d lifecycles=%d terminals=%d",
			current, present, client.currentCursor(), lifecycles.Load(), terminals.Load())
	}
}

func TestExcelPricingRemoteSourceIdempotencyRejectsConflictingPayload(t *testing.T) {
	sourceA := excelPricingRemoteTestSource()
	sourceB := sourceA
	sourceB.Revision = excelPricingRemoteTestRevision("b")
	sourceC := sourceA
	sourceC.Revision = excelPricingRemoteTestRevision("c")
	stateB := excelPricingRemoteTestRevision("5")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		excelPricingRemoteWriteCurrentRevision(w, sourceB, stateB)
	}))
	defer server.Close()
	var callbacks atomic.Int32
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL), sourceA,
		excelPricingRemoteEventsOptions{
			OnRevision: func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: func(excelPricingRemoteSourceLifecycle) error {
				callbacks.Add(1)
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	key := excelPricingRemoteTestRevision("6")
	first := excelPricingRemoteTestSourceFrame(
		sourceB, 1, "pricing.source.changed", "changed", sourceA.Revision, key,
	)
	for _, frame := range []map[string]interface{}{first, excelPricingRemoteTestSourceFrame(
		sourceB, 2, "pricing.source.changed", "changed", sourceA.Revision, key,
	)} {
		body, marshalErr := json.Marshal(frame)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, frameErr := client.handleExcelPricingRemoteFrame(t.Context(), body, true); frameErr != nil {
			t.Fatalf("exact lifecycle/replay: %v", frameErr)
		}
	}
	conflict, err := json.Marshal(excelPricingRemoteTestSourceFrame(
		sourceC, 3, "pricing.source.changed", "changed", sourceB.Revision, key,
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.handleExcelPricingRemoteFrame(t.Context(), conflict, true); !errors.Is(err, errExcelPricingRemoteProtocol) {
		t.Fatalf("idempotency conflict error=%v", err)
	}
	if client.currentCursor() != 2 || callbacks.Load() != 1 || requests.Load() != 1 {
		t.Fatalf("cursor=%d callbacks=%d requests=%d",
			client.currentCursor(), callbacks.Load(), requests.Load())
	}
}

func TestExcelPricingRemoteConnectedReplayGapPreservesOrderedSource(t *testing.T) {
	sourceA := excelPricingRemoteTestSource()
	sourceC := sourceA
	sourceC.Revision = excelPricingRemoteTestRevision("c")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested := sourceA
		requested.Revision = r.URL.Query().Get("source_revision")
		excelPricingRemoteWriteSupersededRevision(w, requested, sourceC.Revision)
	}))
	defer server.Close()
	var lifecycle excelPricingRemoteSourceLifecycle
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL), sourceA,
		excelPricingRemoteEventsOptions{
			InitialCursor: 5,
			OnRevision:    func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: func(candidate excelPricingRemoteSourceLifecycle) error {
				lifecycle = candidate
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]interface{}{
		"event": "connected", "success": true,
		"data": map[string]interface{}{
			"principal": "patris_pricing", "cursor": 5, "oldest_event_id": 1, "latest_event_id": 7,
			"cursor_reset_required": false, "revision_validation_required": true,
			"revision_path": "/wp-json/digitalogic/pricing/sync/revision",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	connected, err := client.handleExcelPricingRemoteFrame(t.Context(), body, false)
	current, present := client.currentStreamSource()
	if err != nil || !connected || current != sourceA || !present ||
		lifecycle.Mode != "validation_gap" || lifecycle.CurrentSourceRevision != sourceC.Revision {
		t.Fatalf("connected=%v err=%v source=%+v present=%v lifecycle=%+v",
			connected, err, current, present, lifecycle)
	}
}

func TestExcelPricingRemoteConnectedWithoutReplayReconcilesCurrentSource(t *testing.T) {
	sourceA := excelPricingRemoteTestSource()
	sourceC := sourceA
	sourceC.Revision = excelPricingRemoteTestRevision("c")
	stateC := excelPricingRemoteTestRevision("5")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		requested := sourceA
		requested.Revision = r.URL.Query().Get("source_revision")
		if r.Header.Get("If-None-Match") != "" {
			t.Error("reconciliation request used If-None-Match")
		}
		if requested == sourceC {
			excelPricingRemoteWriteCurrentRevision(w, sourceC, stateC)
			return
		}
		excelPricingRemoteWriteSupersededRevision(w, requested, sourceC.Revision)
	}))
	defer server.Close()
	var lifecycle excelPricingRemoteSourceLifecycle
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL), sourceA,
		excelPricingRemoteEventsOptions{
			OnRevision: func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: func(candidate excelPricingRemoteSourceLifecycle) error {
				lifecycle = candidate
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]interface{}{
		"event": "connected", "success": true,
		"data": map[string]interface{}{
			"principal": "patris_pricing", "cursor": 0, "oldest_event_id": 0, "latest_event_id": 0,
			"cursor_reset_required": false, "revision_validation_required": true,
			"revision_path": "/wp-json/digitalogic/pricing/sync/revision",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	connected, err := client.handleExcelPricingRemoteFrame(t.Context(), body, false)
	current, present := client.currentStreamSource()
	if err != nil || !connected || current != sourceC || !present || requests.Load() != 2 ||
		lifecycle.Mode != "reconcile_present" || lifecycle.Revision == nil {
		t.Fatalf("connected=%v err=%v source=%+v present=%v requests=%d lifecycle=%+v",
			connected, err, current, present, requests.Load(), lifecycle)
	}
}

func TestExcelPricingRemoteConnectedRejectsCursorDiscontinuityBeforeValidation(t *testing.T) {
	source := excelPricingRemoteTestSource()
	for name, fixture := range map[string]struct {
		initial uint64
		cursor  uint64
		oldest  uint64
		latest  uint64
	}{
		"persisted cursor changed without reset": {initial: 5, cursor: 6, oldest: 1, latest: 7},
		"new subscriber placed before latest":    {initial: 0, cursor: 0, oldest: 1, latest: 7},
	} {
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				requests.Add(1)
			}))
			defer server.Close()
			client, err := newExcelPricingRemoteEventsClient(
				excelPricingRemoteTestConfig(t, server.URL), source,
				excelPricingRemoteEventsOptions{
					InitialCursor:     fixture.initial,
					OnRevision:        func(excelPricingRemoteRevision) error { return nil },
					OnSourceLifecycle: excelPricingRemoteTestLifecycle,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(excelPricingRemoteTestConnectedFrame(
				fixture.cursor, fixture.oldest, fixture.latest, false,
			))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.handleExcelPricingRemoteFrame(t.Context(), body, false); !errors.Is(err, errExcelPricingRemoteProtocol) {
				t.Fatalf("cursor discontinuity error=%v", err)
			}
			if requests.Load() != 0 || client.currentCursor() != fixture.initial {
				t.Fatalf("requests=%d cursor=%d", requests.Load(), client.currentCursor())
			}
		})
	}
}

func TestExcelPricingRemoteStreamResetRejectsNonLatestCursorBeforeValidation(t *testing.T) {
	source := excelPricingRemoteTestSource()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL), source,
		excelPricingRemoteEventsOptions{
			InitialCursor:     5,
			OnRevision:        func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: excelPricingRemoteTestLifecycle,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]interface{}{
		"event": "pricing.stream.reset", "success": true,
		"data": map[string]interface{}{
			"schema": excelPricingRemoteStreamResetSchema, "schema_version": 1,
			"reason": "cursor_gap", "cursor": 5, "oldest_event_id": 6, "latest_event_id": 10,
			"revision_validation_required": true,
			"revision_path":                "/wp-json/digitalogic/pricing/sync/revision",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, frameErr := client.handleExcelPricingRemoteFrame(t.Context(), body, true); !errors.Is(frameErr, errExcelPricingRemoteProtocol) {
		t.Fatalf("stream reset error = %v", frameErr)
	}
	if requests.Load() != 0 || client.currentCursor() != 5 {
		t.Fatalf("requests=%d cursor=%d", requests.Load(), client.currentCursor())
	}
}

func TestExcelPricingRemoteReplayPreservesAbsenceWhenSameRevisionWasReadded(t *testing.T) {
	source := excelPricingRemoteTestSource()
	state := excelPricingRemoteTestRevision("5")
	var absent atomic.Bool
	absent.Store(true)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("If-None-Match") != "" {
			t.Error("absent replay reconciliation used If-None-Match")
		}
		if absent.Load() {
			excelPricingRemoteWriteAbsentRevision(w)
			return
		}
		excelPricingRemoteWriteCurrentRevision(w, source, state)
	}))
	defer server.Close()
	var modes []string
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL), source,
		excelPricingRemoteEventsOptions{
			OnRevision: func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: func(lifecycle excelPricingRemoteSourceLifecycle) error {
				modes = append(modes, lifecycle.Mode)
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	handle := func(frame map[string]interface{}, connected bool) (bool, error) {
		body, marshalErr := json.Marshal(frame)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return client.handleExcelPricingRemoteFrame(t.Context(), body, connected)
	}
	if _, err := handle(excelPricingRemoteTestSourceFrame(
		source, 1, "pricing.source.removed", "removed", source.Revision,
		excelPricingRemoteTestRevision("1"),
	), true); err != nil {
		t.Fatalf("initial removal: %v", err)
	}
	absent.Store(false)
	connected, err := handle(excelPricingRemoteTestConnectedFrame(1, 1, 2, false), false)
	current, present := client.currentStreamSource()
	if err != nil || !connected || current != source || present || client.currentCursor() != 1 {
		t.Fatalf("replay connection=%v err=%v source=%+v present=%v cursor=%d",
			connected, err, current, present, client.currentCursor())
	}
	if _, err := handle(excelPricingRemoteTestSourceFrame(
		source, 2, "pricing.source.changed", "added", nil,
		excelPricingRemoteTestRevision("2"),
	), true); err != nil {
		t.Fatalf("queued re-add: %v", err)
	}
	current, present = client.currentStreamSource()
	if current != source || !present || client.currentCursor() != 2 || requests.Load() != 3 ||
		strings.Join(modes, ",") != "ordered,validation_gap,ordered" {
		t.Fatalf("source=%+v present=%v cursor=%d requests=%d modes=%v",
			current, present, client.currentCursor(), requests.Load(), modes)
	}
}

func TestExcelPricingRemoteSourceEventsRejectMalformedLifecycleProofs(t *testing.T) {
	source := excelPricingRemoteTestSource()
	next := source
	next.Revision = excelPricingRemoteTestRevision("b")
	for name, mutate := range map[string]func(map[string]interface{}){
		"wrong exact source": func(frame map[string]interface{}) {
			data := frame["data"].(map[string]interface{})
			wrong := next
			wrong.Dataset = "other.db"
			data["source"] = wrong
		},
		"missing previous changed revision": func(frame map[string]interface{}) {
			data := frame["data"].(map[string]interface{})
			data["change"] = "changed"
			data["previous_source_revision"] = nil
		},
		"removal name mismatch": func(frame map[string]interface{}) {
			data := frame["data"].(map[string]interface{})
			data["change"] = "removed"
			data["previous_source_revision"] = source.Revision
		},
		"validation flag disabled": func(frame map[string]interface{}) {
			data := frame["data"].(map[string]interface{})
			data["revision_validation_required"] = false
		},
		"unexpected public field": func(frame map[string]interface{}) {
			data := frame["data"].(map[string]interface{})
			data["credential"] = "must-not-be-accepted"
		},
	} {
		t.Run(name, func(t *testing.T) {
			client, err := newExcelPricingRemoteEventsClient(
				excelPricingRemoteTestConfig(t, "http://127.0.0.1:18080"), source,
				excelPricingRemoteEventsOptions{
					OnRevision:        func(excelPricingRemoteRevision) error { return nil },
					OnSourceLifecycle: func(excelPricingRemoteSourceLifecycle) error { return nil },
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			frame := excelPricingRemoteTestSourceFrame(
				next, 1, "pricing.source.changed", "changed", source.Revision, excelPricingRemoteTestRevision("6"),
			)
			mutate(frame)
			body, err := json.Marshal(frame)
			if err != nil {
				t.Fatal(err)
			}
			if _, frameErr := client.handleExcelPricingRemoteFrame(t.Context(), body, true); !errors.Is(frameErr, errExcelPricingRemoteProtocol) {
				t.Fatalf("frame error=%v", frameErr)
			}
			if client.currentCursor() != 0 {
				t.Fatalf("malformed source event advanced cursor to %d", client.currentCursor())
			}
		})
	}
}

func TestExcelPricingRemoteEventsRejectWrongSourceWeakETagAndUnmappedAlias(t *testing.T) {
	source := excelPricingRemoteTestSource()
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, "http://127.0.0.1:18080"), source,
		excelPricingRemoteEventsOptions{
			OnRevision:        func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: excelPricingRemoteTestLifecycle,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]interface{}){
		"wrong source": func(frame map[string]interface{}) {
			data := frame["data"].(map[string]interface{})
			wrong := source
			wrong.Dataset = "other.db"
			data["source"] = wrong
		},
		"weak etag": func(frame map[string]interface{}) {
			data := frame["data"].(map[string]interface{})
			data["etag"] = "W/" + data["etag"].(string)
		},
		"unmapped alias": func(frame map[string]interface{}) {
			frame["event"] = "pricing_changed"
		},
	} {
		t.Run(name, func(t *testing.T) {
			frame := excelPricingRemoteTestStateFrame(source, 1, excelPricingRemoteTestRevision("6"))
			mutate(frame)
			body, marshalErr := json.Marshal(frame)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if _, frameErr := client.handleExcelPricingRemoteFrame(t.Context(), body, true); !errors.Is(frameErr, errExcelPricingRemoteProtocol) {
				t.Fatalf("frame error = %v", frameErr)
			}
		})
	}
}

func TestExcelPricingRemoteRevisionRejectsWeakValidator(t *testing.T) {
	source := excelPricingRemoteTestSource()
	state := excelPricingRemoteTestRevision("7")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `W/"`+state+`"`)
		_ = json.NewEncoder(w).Encode(excelPricingRemoteTestRevisionPayload(source, state))
	}))
	defer server.Close()
	client, err := newExcelPricingRemoteEventsClient(excelPricingRemoteTestConfig(t, server.URL), source,
		excelPricingRemoteEventsOptions{
			OnRevision:        func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: excelPricingRemoteTestLifecycle,
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.reconcileExcelPricingRemoteRevision(t.Context(), "connection_validation", 0, false); !errors.Is(err, errExcelPricingRemoteRevision) {
		t.Fatalf("revision error = %v", err)
	}
}

func TestExcelPricingRemoteRevisionRejectsConflictingDuplicateETags(t *testing.T) {
	source := excelPricingRemoteTestSource()
	state := excelPricingRemoteTestRevision("7")
	validETag := `"` + state + `"`
	conflictingETag := `"` + excelPricingRemoteTestRevision("8") + `"`
	for _, status := range []int{http.StatusOK, http.StatusNotModified} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("ETag", validETag)
				w.Header().Add("ETag", conflictingETag)
				if status == http.StatusNotModified {
					w.WriteHeader(status)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(excelPricingRemoteTestRevisionPayload(source, state))
			}))
			defer server.Close()
			client, err := newExcelPricingRemoteEventsClient(
				excelPricingRemoteTestConfig(t, server.URL), source,
				excelPricingRemoteEventsOptions{
					OnRevision:        func(excelPricingRemoteRevision) error { return nil },
					OnSourceLifecycle: excelPricingRemoteTestLifecycle,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			conditional := status == http.StatusNotModified
			if conditional {
				client.setValidatedRevision(excelPricingRemoteRevision{
					Source: source, StateRevision: state, ETag: validETag,
				})
			}
			if _, probeErr := client.probeExcelPricingRemoteRevision(
				t.Context(), source, conditional, "test", 0,
			); !errors.Is(probeErr, errExcelPricingRemoteRevision) {
				t.Fatalf("duplicate ETag error = %v", probeErr)
			}
		})
	}
}

func TestExcelPricingRemoteRevisionRejectsNonIdentityResponseEncoding(t *testing.T) {
	source := excelPricingRemoteTestSource()
	state := excelPricingRemoteTestRevision("7")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("ETag", `"`+state+`"`)
		_ = json.NewEncoder(w).Encode(excelPricingRemoteTestRevisionPayload(source, state))
	}))
	defer server.Close()
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL), source,
		excelPricingRemoteEventsOptions{
			OnRevision:        func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle: excelPricingRemoteTestLifecycle,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, probeErr := client.probeExcelPricingRemoteRevision(
		t.Context(), source, false, "test", 0,
	); !errors.Is(probeErr, errExcelPricingRemoteRevision) {
		t.Fatalf("content encoding error = %v", probeErr)
	}
}

func TestExcelPricingRemoteRevisionConflictRequiresExplicitEnvelopeBooleans(t *testing.T) {
	requested := excelPricingRemoteTestSource()
	currentRevision := excelPricingRemoteTestRevision("b")
	for _, code := range []string{
		"digitalogic_pricing_snapshot_source_revision_conflict",
		"digitalogic_excel_sync_source_scope_conflict",
	} {
		for _, missing := range []string{"success", "retryable"} {
			t.Run(code+"/missing "+missing, func(t *testing.T) {
				details := map[string]interface{}{"current_source": canonical.Source{}}
				if code == "digitalogic_pricing_snapshot_source_revision_conflict" {
					details = map[string]interface{}{
						"submitted_source_revision": requested.Revision,
						"current_source_revision":   currentRevision,
						"retryable":                 false,
					}
				}
				payload := map[string]interface{}{
					"success": false, "code": code, "message": "conflict",
					"retryable": false, "details": details,
				}
				delete(payload, missing)
				body, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := excelPricingRemoteRevisionConflict(http.StatusConflict, body, requested); ok {
					t.Fatalf("accepted conflict missing %q", missing)
				}
			})
		}
	}
	for _, missing := range []string{"id", "dataset", "revision"} {
		t.Run("empty current source missing "+missing, func(t *testing.T) {
			currentSource := map[string]interface{}{"id": "", "dataset": "", "revision": ""}
			delete(currentSource, missing)
			body, err := json.Marshal(map[string]interface{}{
				"success": false,
				"code":    "digitalogic_excel_sync_source_scope_conflict",
				"message": "conflict", "retryable": false,
				"details": map[string]interface{}{"current_source": currentSource},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := excelPricingRemoteRevisionConflict(http.StatusConflict, body, requested); ok {
				t.Fatalf("accepted current_source missing %q", missing)
			}
		})
	}
}

func TestExcelPricingRemoteEventsErrorsDoNotExposeSecretOrResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rejected "+excelPricingRemoteTestSecret, http.StatusForbidden)
	}))
	defer server.Close()
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, server.URL), excelPricingRemoteTestSource(),
		excelPricingRemoteEventsOptions{
			MinReconnectBackoff: time.Millisecond,
			MaxReconnectBackoff: 2 * time.Millisecond,
			OnRevision:          func(excelPricingRemoteRevision) error { return nil },
			OnSourceLifecycle:   excelPricingRemoteTestLifecycle,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	runErr := client.Run(ctx)
	if !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v", runErr)
	}
	if strings.Contains(runErr.Error(), excelPricingRemoteTestSecret) || strings.Contains(runErr.Error(), "rejected") {
		t.Fatalf("unsanitized subscriber error = %q", runErr)
	}
}

func TestExcelPricingRemoteEventsConstructorFailsClosedWithoutAcceptanceHook(t *testing.T) {
	cfg := excelPricingRemoteTestConfig(t, "http://127.0.0.1:18080")
	for name, options := range map[string]excelPricingRemoteEventsOptions{
		"both hooks": {},
		"revision hook": {
			OnSourceLifecycle: excelPricingRemoteTestLifecycle,
		},
		"source lifecycle hook": {
			OnRevision: func(excelPricingRemoteRevision) error { return nil },
		},
	} {
		t.Run("missing "+name, func(t *testing.T) {
			if _, err := newExcelPricingRemoteEventsClient(
				cfg, excelPricingRemoteTestSource(), options,
			); !errors.Is(err, errExcelPricingRemoteConfiguration) {
				t.Fatalf("constructor error = %v", err)
			}
		})
	}
}

func TestExcelPricingRemoteReconnectBackoffIsBounded(t *testing.T) {
	minimum, maximum := boundedExcelPricingReconnectBackoff(time.Minute, time.Hour)
	if minimum != excelPricingRemoteMaxBackoff || maximum != excelPricingRemoteMaxBackoff {
		t.Fatalf("bounded backoff = %s..%s", minimum, maximum)
	}
	minimum, maximum = boundedExcelPricingReconnectBackoff(10*time.Millisecond, time.Millisecond)
	if minimum != 10*time.Millisecond || maximum != minimum {
		t.Fatalf("ordered backoff = %s..%s", minimum, maximum)
	}
}

func TestExcelPricingRemoteRevisionURLHasExactBoundedQuery(t *testing.T) {
	source := excelPricingRemoteTestSource()
	parsed, err := url.Parse("https://digitalogic.example/wp-json/digitalogic/pricing/sync/revision")
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("source_id", source.ID)
	query.Set("source_dataset", source.Dataset)
	query.Set("source_revision", source.Revision)
	query.Set("locale", "fa")
	query.Set("page_size", strconv.Itoa(excelPricingSnapshotPageSize))
	query.Set("schema_version", "1")
	parsed.RawQuery = query.Encode()
	if strings.Contains(parsed.RawQuery, "secret") || len(parsed.RawQuery) > 512 {
		t.Fatalf("unsafe revision query = %q", parsed.RawQuery)
	}
}

func TestExcelPricingRemoteEventDedupeRemainsBounded(t *testing.T) {
	source := excelPricingRemoteTestSource()
	client := &excelPricingRemoteEventsClient{
		seen: make(map[string]string),
	}
	for index := 0; index < excelPricingSnapshotEventHistory+20; index++ {
		revision := excelPricingRemoteRevision{
			Source:         source,
			StateRevision:  excelPricingRemoteTestRevision("8"),
			IdempotencyKey: "sha256:" + strings.Repeat(strconv.FormatInt(int64(index%10), 10), 64),
		}
		client.rememberRevision(revision)
	}
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	if len(client.seen) > excelPricingSnapshotEventHistory || len(client.seenOrder) > excelPricingSnapshotEventHistory {
		t.Fatalf("dedupe history grew to map=%d order=%d", len(client.seen), len(client.seenOrder))
	}
}

func TestExcelPricingRemoteCursorCallbackRunsAfterAcceptance(t *testing.T) {
	var mu sync.Mutex
	accepted := false
	callbackSawAccepted := false
	client := &excelPricingRemoteEventsClient{
		seen: make(map[string]string),
		onRevision: func(excelPricingRemoteRevision) error {
			mu.Lock()
			accepted = true
			mu.Unlock()
			return nil
		},
		onCursor: func(uint64) {
			mu.Lock()
			callbackSawAccepted = accepted
			mu.Unlock()
		},
	}
	revision := excelPricingRemoteRevision{
		StateRevision:  excelPricingRemoteTestRevision("9"),
		IdempotencyKey: excelPricingRemoteTestRevision("0"),
	}
	if err := client.deliverRevision(revision); err != nil {
		t.Fatal(err)
	}
	client.advanceCursor(1)
	mu.Lock()
	defer mu.Unlock()
	if !callbackSawAccepted {
		t.Fatal("cursor callback ran before atomic revision acceptance")
	}
}
