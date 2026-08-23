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

func excelPricingRemoteTestSource() canonical.Source {
	return canonical.Source{
		ID:       "patris-office",
		Dataset:  "kala.db",
		Revision: excelPricingRemoteTestRevision("a"),
	}
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
			InitialCursor: 99,
			OnRevision:    func(excelPricingRemoteRevision) error { return nil },
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

func TestExcelPricingRemoteEventCursorAdvancesOnlyAfterAtomicAcceptance(t *testing.T) {
	source := excelPricingRemoteTestSource()
	state := excelPricingRemoteTestRevision("5")
	cfg := excelPricingRemoteTestConfig(t, "http://127.0.0.1:18080")
	client, err := newExcelPricingRemoteEventsClient(cfg, source, excelPricingRemoteEventsOptions{
		InitialCursor: 5,
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

func TestExcelPricingRemoteEventsRejectWrongSourceWeakETagAndUnmappedAlias(t *testing.T) {
	source := excelPricingRemoteTestSource()
	client, err := newExcelPricingRemoteEventsClient(
		excelPricingRemoteTestConfig(t, "http://127.0.0.1:18080"), source,
		excelPricingRemoteEventsOptions{OnRevision: func(excelPricingRemoteRevision) error { return nil }},
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
		excelPricingRemoteEventsOptions{OnRevision: func(excelPricingRemoteRevision) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.validateExcelPricingRemoteRevision(t.Context(), "connection_validation", 0); !errors.Is(err, errExcelPricingRemoteRevision) {
		t.Fatalf("revision error = %v", err)
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
	if _, err := newExcelPricingRemoteEventsClient(cfg, excelPricingRemoteTestSource(), excelPricingRemoteEventsOptions{}); !errors.Is(err, errExcelPricingRemoteConfiguration) {
		t.Fatalf("constructor error = %v", err)
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
		seen: make(map[string]struct{}),
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
		seen: make(map[string]struct{}),
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
