package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
	"github.com/gorilla/websocket"
)

const (
	excelPricingRemoteWebSocketProtocol = "digitalogic.pricing.v1"
	excelPricingRemoteSourceEventSchema = "digitalogic.pricing-source-change/v1"
	excelPricingRemoteStateEventSchema  = "digitalogic.pricing-state-change/v1"
	excelPricingRemoteStreamResetSchema = "digitalogic.pricing-stream-reset/v1"
	excelPricingRemoteRevisionSchema    = "digitalogic.pricing-sync-revision/v1"
	excelPricingRemoteProjection        = "excel-v1"
	excelPricingRemoteProjectionSchema  = "digitalogic.pricing-projection/excel-v1"
	excelPricingRemoteIdentityEncoding  = "identity"

	excelPricingRemoteEventsMaxFrameBytes = 64 << 10
	excelPricingRemoteRevisionMaxBytes    = 64 << 10
	excelPricingRemotePingInterval        = 20 * time.Second
	excelPricingRemotePongTimeout         = 60 * time.Second
	excelPricingRemoteMinBackoff          = 250 * time.Millisecond
	excelPricingRemoteMaxBackoff          = 30 * time.Second
)

const (
	excelPricingRemoteSecretHeader   = updateout.ProductSyncSecretHeader
	excelPricingRemoteSourceIDHeader = "X-Patris-Source-Id"
	excelPricingRemoteDatasetHeader  = "X-Patris-Source-Dataset"
)

var (
	errExcelPricingRemoteConfiguration = errors.New("pricing event subscriber configuration is invalid")
	errExcelPricingRemoteHandshake     = errors.New("pricing event subscriber handshake failed")
	errExcelPricingRemoteProtocol      = errors.New("pricing event subscriber protocol violation")
	errExcelPricingRemoteRevision      = errors.New("pricing revision validation failed")
)

// excelPricingRemoteRevision is the only state allowed to cross from the
// authenticated WordPress stream into the local snapshot store. It contains
// no credential, endpoint, response body, or product data.
type excelPricingRemoteRevision struct {
	Source                canonical.Source
	StateRevision         string
	CatalogRevision       string
	PricingStateRevision  string
	PricingPolicyRevision string
	ETag                  string
	Cause                 string
	IdempotencyKey        string
	EventID               uint64
	ValidationOrigin      string
}

type excelPricingRemoteSourceValidationOutcome string

const (
	excelPricingRemoteSourceCurrent    excelPricingRemoteSourceValidationOutcome = "current"
	excelPricingRemoteSourceSuperseded excelPricingRemoteSourceValidationOutcome = "superseded"
	excelPricingRemoteSourceAbsent     excelPricingRemoteSourceValidationOutcome = "absent"
)

// excelPricingRemoteSourceLifecycle is the only source-identity transition
// allowed to cross from the authenticated WordPress stream into the local
// snapshot store. Synthetic modes reconcile a connection/reset without
// inventing a durable producer idempotency key.
type excelPricingRemoteSourceLifecycle struct {
	Mode                   string
	Name                   string
	Change                 string
	Source                 canonical.Source
	PreviousSourceRevision string
	IdempotencyKey         string
	EventID                uint64
	ValidationOrigin       string
	ValidationOutcome      excelPricingRemoteSourceValidationOutcome
	CurrentSourceRevision  string
	Revision               *excelPricingRemoteRevision
}

type excelPricingRemoteEventsOptions struct {
	InitialCursor       uint64
	InitialETag         string
	OnRevision          func(excelPricingRemoteRevision) error
	OnSourceLifecycle   func(excelPricingRemoteSourceLifecycle) error
	OnSnapshotTerminal  func(excelPricingRemoteSnapshotTerminalEvent) error
	OnCursor            func(uint64)
	WebSocketDialer     *websocket.Dialer
	HTTPClient          *http.Client
	MinReconnectBackoff time.Duration
	MaxReconnectBackoff time.Duration
}

type excelPricingRemoteEventsClient struct {
	cfg          updateout.Config
	source       canonical.Source
	secret       string
	webSocketURL string
	revisionURL  string
	revisionPath string
	dialer       *websocket.Dialer
	httpClient   *http.Client
	onRevision   func(excelPricingRemoteRevision) error
	onLifecycle  func(excelPricingRemoteSourceLifecycle) error
	onTerminal   func(excelPricingRemoteSnapshotTerminalEvent) error
	onCursor     func(uint64)
	minBackoff   time.Duration
	maxBackoff   time.Duration

	stateMu       sync.Mutex
	cursor        uint64
	etag          string
	stateRevision string
	streamSource  canonical.Source
	streamPresent bool
	seen          map[string]string
	seenOrder     []string
}

type excelPricingRemoteWireFrame struct {
	Event   string          `json:"event"`
	Name    string          `json:"name"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Time    string          `json:"time"`
	ID      uint64          `json:"id"`
}

type excelPricingRemoteConnectedData struct {
	Principal                  string `json:"principal"`
	Cursor                     uint64 `json:"cursor"`
	OldestEventID              uint64 `json:"oldest_event_id"`
	LatestEventID              uint64 `json:"latest_event_id"`
	CursorResetRequired        bool   `json:"cursor_reset_required"`
	RevisionValidationRequired bool   `json:"revision_validation_required"`
	RevisionPath               string `json:"revision_path"`
}

type excelPricingRemoteStreamResetData struct {
	Schema                     string `json:"schema"`
	SchemaVersion              int    `json:"schema_version"`
	Reason                     string `json:"reason"`
	Cursor                     uint64 `json:"cursor"`
	OldestEventID              uint64 `json:"oldest_event_id"`
	LatestEventID              uint64 `json:"latest_event_id"`
	RevisionValidationRequired bool   `json:"revision_validation_required"`
	RevisionPath               string `json:"revision_path"`
}

type excelPricingRemoteStateEventData struct {
	Schema                string           `json:"schema"`
	SchemaVersion         int              `json:"schema_version"`
	Projection            string           `json:"projection"`
	Source                canonical.Source `json:"source"`
	StateRevision         string           `json:"state_revision"`
	ETag                  string           `json:"etag"`
	CatalogRevision       string           `json:"catalog_revision"`
	PricingStateRevision  string           `json:"pricing_state_revision"`
	PricingPolicyRevision string           `json:"pricing_policy_revision"`
	Cause                 string           `json:"cause"`
	IdempotencyKey        string           `json:"idempotency_key"`
	RevisionPath          string           `json:"revision_path"`
}

type excelPricingRemoteSourceAudience struct {
	Services []string `json:"services"`
}

type excelPricingRemoteSourceEventData struct {
	Schema                     string                           `json:"schema"`
	SchemaVersion              int                              `json:"schema_version"`
	Projection                 string                           `json:"projection"`
	Change                     string                           `json:"change"`
	Source                     canonical.Source                 `json:"source"`
	PreviousSourceRevision     json.RawMessage                  `json:"previous_source_revision"`
	IdempotencyKey             string                           `json:"idempotency_key"`
	RevisionValidationRequired bool                             `json:"revision_validation_required"`
	RevisionPath               string                           `json:"revision_path"`
	Audience                   excelPricingRemoteSourceAudience `json:"audience"`
}

type excelPricingRemoteErrorSource struct {
	ID       *string `json:"id"`
	Dataset  *string `json:"dataset"`
	Revision *string `json:"revision"`
}

type excelPricingRemoteErrorDetails struct {
	CurrentSource           *excelPricingRemoteErrorSource `json:"current_source"`
	SubmittedSourceRevision *string                        `json:"submitted_source_revision"`
	CurrentSourceRevision   *string                        `json:"current_source_revision"`
	Retryable               *bool                          `json:"retryable"`
}

type excelPricingRemoteErrorResponse struct {
	Success   *bool                          `json:"success"`
	Code      string                         `json:"code"`
	Message   string                         `json:"message"`
	Retryable *bool                          `json:"retryable"`
	Details   excelPricingRemoteErrorDetails `json:"details"`
}

type excelPricingRemoteRevisionResponse struct {
	Schema                string           `json:"schema"`
	SchemaVersion         int              `json:"schema_version"`
	Projection            string           `json:"projection"`
	ProjectionSchema      string           `json:"projection_schema"`
	StateRevision         string           `json:"state_revision"`
	Source                canonical.Source `json:"source"`
	CatalogRevision       string           `json:"catalog_revision"`
	PricingStateRevision  string           `json:"pricing_state_revision"`
	PricingPolicyRevision string           `json:"pricing_policy_revision"`
	Locale                string           `json:"locale"`
	PageSize              int              `json:"page_size"`
}

type excelPricingRemoteRevisionValidation struct {
	Outcome               excelPricingRemoteSourceValidationOutcome
	Revision              *excelPricingRemoteRevision
	CurrentSourceRevision string
	NotModified           bool
}

func newExcelPricingRemoteEventsClient(
	cfg updateout.Config,
	source canonical.Source,
	options excelPricingRemoteEventsOptions,
) (*excelPricingRemoteEventsClient, error) {
	cfg, secret, _, err := resolveExcelPricingRemote(cfg, "state")
	if err != nil || !validExcelPricingRemoteSource(source) || options.OnRevision == nil ||
		options.OnSourceLifecycle == nil {
		return nil, errExcelPricingRemoteConfiguration
	}
	webSocketURL, revisionURL, revisionPath, err := excelPricingRemoteEventEndpoints(cfg.URL)
	if err != nil {
		return nil, errExcelPricingRemoteConfiguration
	}

	dialer := options.WebSocketDialer
	if dialer == nil {
		copyDialer := *websocket.DefaultDialer
		copyDialer.HandshakeTimeout = excelPricingRemotePongTimeout
		copyDialer.Subprotocols = []string{excelPricingRemoteWebSocketProtocol}
		dialer = &copyDialer
	} else {
		copyDialer := *dialer
		copyDialer.Subprotocols = []string{excelPricingRemoteWebSocketProtocol}
		if copyDialer.HandshakeTimeout <= 0 {
			copyDialer.HandshakeTimeout = excelPricingRemotePongTimeout
		}
		dialer = &copyDialer
	}

	httpClient := options.HTTPClient
	if httpClient == nil {
		timeout := excelPricingRemoteTimeout(cfg.Timeout)
		httpClient = &http.Client{
			Timeout: timeout,
		}
	}
	copyHTTPClient := *httpClient
	// Never forward the machine credential to a redirected host or path.
	copyHTTPClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	httpClient = &copyHTTPClient

	minBackoff, maxBackoff := boundedExcelPricingReconnectBackoff(
		options.MinReconnectBackoff,
		options.MaxReconnectBackoff,
	)
	initialETag := strings.TrimSpace(options.InitialETag)
	if initialETag != "" && !isStrongExcelPricingRevisionETag(initialETag, "") {
		return nil, errExcelPricingRemoteConfiguration
	}
	initialStateRevision := ""
	if initialETag != "" {
		initialStateRevision = initialETag[1 : len(initialETag)-1]
	}
	return &excelPricingRemoteEventsClient{
		cfg:           cfg,
		source:        source,
		secret:        secret,
		webSocketURL:  webSocketURL,
		revisionURL:   revisionURL,
		revisionPath:  revisionPath,
		dialer:        dialer,
		httpClient:    httpClient,
		onRevision:    options.OnRevision,
		onLifecycle:   options.OnSourceLifecycle,
		onTerminal:    options.OnSnapshotTerminal,
		onCursor:      options.OnCursor,
		minBackoff:    minBackoff,
		maxBackoff:    maxBackoff,
		cursor:        options.InitialCursor,
		etag:          initialETag,
		stateRevision: initialStateRevision,
		streamSource:  source,
		streamPresent: true,
		seen:          make(map[string]string),
	}, nil
}

func validExcelPricingRemoteSource(source canonical.Source) bool {
	return validExcelPricingRemoteHeaderValue(source.ID) &&
		validExcelPricingRemoteHeaderValue(source.Dataset) &&
		isSHA256Revision(source.Revision)
}

func validExcelPricingRemoteHeaderValue(value string) bool {
	return value != "" && len(value) <= 256 && strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, "\r\n")
}

func excelPricingRemoteEventEndpoints(productSyncURL string) (string, string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(productSyncURL))
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Scheme != "https" && !hostnameIsLoopback(parsed.Hostname())) {
		return "", "", "", errExcelPricingRemoteConfiguration
	}
	index := strings.Index(parsed.Path, "/wp-json/")
	if index < 0 {
		return "", "", "", errExcelPricingRemoteConfiguration
	}
	prefix := strings.TrimSuffix(parsed.Path[:index], "/")

	webSocket := *parsed
	if webSocket.Scheme == "https" {
		webSocket.Scheme = "wss"
	} else {
		webSocket.Scheme = "ws"
	}
	webSocket.Path = "/wordpress-ws"
	webSocket.RawPath = ""
	webSocket.RawQuery = ""
	webSocket.Fragment = ""

	revision := *parsed
	revision.Path = prefix + "/wp-json/digitalogic/pricing/sync/revision"
	revision.RawPath = ""
	revision.RawQuery = ""
	revision.Fragment = ""
	return webSocket.String(), revision.String(), revision.Path, nil
}

func boundedExcelPricingReconnectBackoff(minimum, maximum time.Duration) (time.Duration, time.Duration) {
	if minimum <= 0 {
		minimum = excelPricingRemoteMinBackoff
	}
	if minimum > excelPricingRemoteMaxBackoff {
		minimum = excelPricingRemoteMaxBackoff
	}
	if maximum <= 0 {
		maximum = excelPricingRemoteMaxBackoff
	}
	if maximum > excelPricingRemoteMaxBackoff {
		maximum = excelPricingRemoteMaxBackoff
	}
	if maximum < minimum {
		maximum = minimum
	}
	return minimum, maximum
}

// Run maintains one outbound event-driven subscription until ctx is cancelled.
// Transport and protocol errors are intentionally sanitized and retried with a
// bounded backoff; no endpoint response or credential material is returned.
func (client *excelPricingRemoteEventsClient) Run(ctx context.Context) error {
	if client == nil {
		return errExcelPricingRemoteConfiguration
	}
	backoff := client.minBackoff
	for {
		connected, _ := client.runConnection(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if connected {
			backoff = client.minBackoff
		}
		if err := waitExcelPricingRemoteReconnect(ctx, backoff); err != nil {
			return err
		}
		if backoff < client.maxBackoff {
			backoff *= 2
			if backoff > client.maxBackoff {
				backoff = client.maxBackoff
			}
		}
	}
}

func waitExcelPricingRemoteReconnect(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (client *excelPricingRemoteEventsClient) runConnection(ctx context.Context) (bool, error) {
	headers := make(http.Header)
	headers.Set(excelPricingRemoteSecretHeader, client.secret)
	headers.Set(excelPricingRemoteSourceIDHeader, client.source.ID)
	headers.Set(excelPricingRemoteDatasetHeader, client.source.Dataset)
	if cursor := client.currentCursor(); cursor > 0 {
		headers.Set("Last-Event-ID", strconv.FormatUint(cursor, 10))
	}
	connection, response, err := client.dialer.DialContext(ctx, client.webSocketURL, headers)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return false, errExcelPricingRemoteHandshake
	}
	defer connection.Close()
	if connection.Subprotocol() != excelPricingRemoteWebSocketProtocol {
		return false, errExcelPricingRemoteHandshake
	}
	connection.SetReadLimit(excelPricingRemoteEventsMaxFrameBytes)
	_ = connection.SetReadDeadline(time.Now().Add(excelPricingRemotePongTimeout))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(excelPricingRemotePongTimeout))
	})

	connectionDone := make(chan struct{})
	go client.maintainExcelPricingRemoteConnection(ctx, connection, connectionDone)
	defer close(connectionDone)

	connected := false
	for {
		messageType, body, err := connection.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return connected, ctx.Err()
			}
			return connected, errExcelPricingRemoteHandshake
		}
		_ = connection.SetReadDeadline(time.Now().Add(excelPricingRemotePongTimeout))
		if messageType != websocket.TextMessage || len(body) == 0 || len(body) > excelPricingRemoteEventsMaxFrameBytes {
			return connected, errExcelPricingRemoteProtocol
		}
		frameConnected, err := client.handleExcelPricingRemoteFrame(ctx, body, connected)
		if err != nil {
			return connected, err
		}
		connected = connected || frameConnected
	}
}

func (client *excelPricingRemoteEventsClient) maintainExcelPricingRemoteConnection(
	ctx context.Context,
	connection *websocket.Conn,
	done <-chan struct{},
) {
	ticker := time.NewTicker(excelPricingRemotePingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = connection.Close()
			return
		case <-done:
			return
		case <-ticker.C:
			deadline := time.Now().Add(5 * time.Second)
			if err := connection.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				_ = connection.Close()
				return
			}
		}
	}
}

func (client *excelPricingRemoteEventsClient) handleExcelPricingRemoteFrame(
	ctx context.Context,
	body []byte,
	connected bool,
) (bool, error) {
	var frame excelPricingRemoteWireFrame
	if json.Unmarshal(body, &frame) != nil || !frame.Success || len(frame.Data) == 0 {
		return false, errExcelPricingRemoteProtocol
	}
	eventName, ok := normalizedExcelPricingRemoteEventName(frame.Event, frame.Name)
	if !ok {
		return false, errExcelPricingRemoteProtocol
	}
	if !connected && eventName != "connected" {
		return false, errExcelPricingRemoteProtocol
	}

	switch eventName {
	case "connected":
		if connected || frame.ID != 0 {
			return false, errExcelPricingRemoteProtocol
		}
		var data excelPricingRemoteConnectedData
		if json.Unmarshal(frame.Data, &data) != nil || data.Principal != "patris_pricing" ||
			!data.RevisionValidationRequired || data.RevisionPath != client.revisionPath ||
			!validExcelPricingRemoteWindow(data.Cursor, data.OldestEventID, data.LatestEventID) {
			return false, errExcelPricingRemoteProtocol
		}
		requestedCursor := client.currentCursor()
		if !data.CursorResetRequired &&
			((requestedCursor > 0 && data.Cursor != requestedCursor) ||
				(requestedCursor == 0 && data.Cursor != data.LatestEventID)) {
			return false, errExcelPricingRemoteProtocol
		}
		replayPending := requestedCursor > 0 && !data.CursorResetRequired &&
			data.Cursor == requestedCursor && data.Cursor < data.LatestEventID
		if err := client.reconcileExcelPricingRemoteRevision(
			ctx, "connection_validation", data.Cursor, replayPending,
		); err != nil {
			return false, err
		}
		if data.CursorResetRequired {
			client.replaceCursor(data.LatestEventID)
		} else {
			client.advanceCursor(data.Cursor)
		}
		return true, nil
	case "pricing.stream.reset":
		var data excelPricingRemoteStreamResetData
		if frame.ID != 0 || json.Unmarshal(frame.Data, &data) != nil ||
			data.Schema != excelPricingRemoteStreamResetSchema || data.SchemaVersion != 1 ||
			data.Reason != "cursor_gap" || !data.RevisionValidationRequired ||
			data.RevisionPath != client.revisionPath ||
			!validExcelPricingRemoteWindow(data.Cursor, data.OldestEventID, data.LatestEventID) ||
			data.Cursor != data.LatestEventID {
			return false, errExcelPricingRemoteProtocol
		}
		if err := client.reconcileExcelPricingRemoteRevision(ctx, "cursor_reset", data.Cursor, false); err != nil {
			return false, err
		}
		client.replaceCursor(data.LatestEventID)
		return false, nil
	case "pricing.source.changed", "pricing.source.removed":
		if frame.ID == 0 {
			return false, errExcelPricingRemoteProtocol
		}
		if frame.ID <= client.currentCursor() {
			return false, nil
		}
		var data excelPricingRemoteSourceEventData
		if !decodeExactExcelPricingRemoteJSON(frame.Data, &data) ||
			!validExcelPricingRemoteSourceEventShape(eventName, data, client.source, client.revisionPath) {
			return false, errExcelPricingRemoteProtocol
		}
		previous, ok := excelPricingRemoteSourceEventPrevious(data.PreviousSourceRevision)
		if !ok {
			return false, errExcelPricingRemoteProtocol
		}
		dedupeKey := excelPricingRemoteSourceEventDedupeKey(eventName, data.IdempotencyKey)
		fingerprint := excelPricingRemoteSourceEventFingerprint(eventName, data, previous)
		seen, conflict := client.seenEvent(dedupeKey, fingerprint)
		if conflict {
			return false, errExcelPricingRemoteProtocol
		}
		if seen {
			client.advanceCursor(frame.ID)
			return false, nil
		}
		streamSource, streamPresent := client.currentStreamSource()
		if !validExcelPricingRemoteSourceTransition(
			eventName, data.Change, data.Source, previous, streamSource, streamPresent,
		) {
			return false, errExcelPricingRemoteProtocol
		}
		validation, err := client.probeExcelPricingRemoteRevision(
			ctx, data.Source, false, "source_event", frame.ID,
		)
		if err != nil {
			return false, err
		}
		lifecycle := excelPricingRemoteSourceLifecycle{
			Mode:                   "ordered",
			Name:                   eventName,
			Change:                 data.Change,
			Source:                 data.Source,
			PreviousSourceRevision: previous,
			IdempotencyKey:         data.IdempotencyKey,
			EventID:                frame.ID,
			ValidationOrigin:       "source_event",
			ValidationOutcome:      validation.Outcome,
			CurrentSourceRevision:  validation.CurrentSourceRevision,
		}
		if eventName != "pricing.source.removed" && validation.Outcome == excelPricingRemoteSourceCurrent {
			lifecycle.Revision = validation.Revision
		}
		if err := client.deliverSourceLifecycle(lifecycle); err != nil {
			return false, err
		}
		client.applySourceLifecycle(lifecycle)
		client.rememberEvent(dedupeKey, fingerprint)
		client.advanceCursor(frame.ID)
		return false, nil
	case "pricing.state.changed":
		if frame.ID == 0 {
			return false, errExcelPricingRemoteProtocol
		}
		if frame.ID <= client.currentCursor() {
			return false, nil
		}
		streamSource, streamPresent := client.currentStreamSource()
		if !streamPresent {
			return false, errExcelPricingRemoteRevision
		}
		var data excelPricingRemoteStateEventData
		if json.Unmarshal(frame.Data, &data) != nil ||
			data.Schema != excelPricingRemoteStateEventSchema || data.SchemaVersion != 1 ||
			data.Projection != excelPricingRemoteProjection || data.Source != streamSource ||
			!validExcelPricingRemoteRevisionParts(data.StateRevision, data.CatalogRevision,
				data.PricingStateRevision, data.PricingPolicyRevision) ||
			!isStrongExcelPricingRevisionETag(data.ETag, data.StateRevision) ||
			!isSHA256Revision(data.IdempotencyKey) || strings.TrimSpace(data.Cause) == "" ||
			data.RevisionPath != client.revisionPath {
			return false, errExcelPricingRemoteProtocol
		}
		revision := excelPricingRemoteRevision{
			Source:                data.Source,
			StateRevision:         data.StateRevision,
			CatalogRevision:       data.CatalogRevision,
			PricingStateRevision:  data.PricingStateRevision,
			PricingPolicyRevision: data.PricingPolicyRevision,
			ETag:                  data.ETag,
			Cause:                 data.Cause,
			IdempotencyKey:        data.IdempotencyKey,
			EventID:               frame.ID,
			ValidationOrigin:      "stream_event",
		}
		seen, conflict := client.seenRevision(revision)
		if conflict {
			return false, errExcelPricingRemoteProtocol
		}
		if !seen {
			if err := client.deliverRevision(revision); err != nil {
				return false, err
			}
			client.rememberRevision(revision)
		}
		client.setValidatedRevision(revision)
		client.advanceCursor(frame.ID)
		return false, nil
	case "pricing.snapshot.build.terminal":
		if frame.ID == 0 || client.onTerminal == nil {
			return false, errExcelPricingRemoteProtocol
		}
		if frame.ID <= client.currentCursor() {
			return false, nil
		}
		var event excelPricingRemoteSnapshotTerminalEvent
		if json.Unmarshal(frame.Data, &event) != nil {
			return false, errExcelPricingRemoteProtocol
		}
		event.EventID = frame.ID
		if !sameExcelPricingRemoteSourceScope(event.Source, client.source) ||
			validateExcelPricingRemoteSnapshotTerminalEvent(event) != nil {
			return false, errExcelPricingRemoteProtocol
		}
		if err := client.onTerminal(event); err != nil {
			return false, errExcelPricingRemoteProtocol
		}
		// The cursor is acknowledged only after the request waiter durably retains
		// the terminal event. A disconnect before this point therefore replays it.
		client.advanceCursor(frame.ID)
		return false, nil
	default:
		return false, errExcelPricingRemoteProtocol
	}
}

func normalizedExcelPricingRemoteEventName(event, name string) (string, bool) {
	switch {
	case event == "connected" && name == "":
		return event, true
	case event == "pricing.stream.reset" && name == "":
		return event, true
	case event == "pricing.source.changed" && (name == "" || name == event):
		return event, true
	case event == "pricing.source.removed" && (name == "" || name == event):
		return event, true
	case event == "pricing.state.changed" && (name == "" || name == event):
		return event, true
	case event == "pricing.snapshot.build.terminal" && (name == "" || name == event):
		return event, true
	case event == "pricing_state_changed" && name == "pricing.state.changed":
		// This is the exact event/name mapping emitted by the WordPress panel
		// bridge. Other unequal aliases remain protocol violations.
		return name, true
	default:
		return "", false
	}
}

func decodeExactExcelPricingRemoteJSON(data []byte, target interface{}) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

func validExcelPricingRemoteSourceEventShape(
	name string,
	data excelPricingRemoteSourceEventData,
	fixedScope canonical.Source,
	revisionPath string,
) bool {
	if data.Schema != excelPricingRemoteSourceEventSchema || data.SchemaVersion != 1 ||
		data.Projection != excelPricingRemoteProjection || !validExcelPricingRemoteSource(data.Source) ||
		!sameExcelPricingRemoteSourceScope(data.Source, fixedScope) || !isSHA256Revision(data.IdempotencyKey) ||
		!data.RevisionValidationRequired || data.RevisionPath != revisionPath ||
		len(data.Audience.Services) != 1 || data.Audience.Services[0] != "patris_pricing" ||
		len(data.PreviousSourceRevision) == 0 {
		return false
	}
	switch data.Change {
	case "added":
		return name == "pricing.source.changed"
	case "changed":
		return name == "pricing.source.changed"
	case "removed":
		return name == "pricing.source.removed"
	default:
		return false
	}
}

func excelPricingRemoteSourceEventPrevious(raw json.RawMessage) (string, bool) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", true
	}
	var previous string
	if json.Unmarshal(raw, &previous) != nil || !isSHA256Revision(previous) {
		return "", false
	}
	return previous, true
}

func validExcelPricingRemoteSourceTransition(
	name string,
	change string,
	source canonical.Source,
	previous string,
	current canonical.Source,
	present bool,
) bool {
	switch change {
	case "added":
		return name == "pricing.source.changed" && !present && previous == ""
	case "changed":
		return name == "pricing.source.changed" && present && previous == current.Revision &&
			sameExcelPricingRemoteSourceScope(source, current) && source.Revision != current.Revision
	case "removed":
		return name == "pricing.source.removed" && present && source == current && previous == current.Revision
	default:
		return false
	}
}

func sameExcelPricingRemoteSourceScope(left, right canonical.Source) bool {
	return left.ID == right.ID && left.Dataset == right.Dataset
}

func excelPricingRemoteSourceEventDedupeKey(_ string, idempotencyKey string) string {
	return "source\x00" + idempotencyKey
}

func excelPricingRemoteSourceEventFingerprint(
	name string,
	data excelPricingRemoteSourceEventData,
	previous string,
) string {
	return strings.Join([]string{
		name,
		data.Change,
		data.Source.ID,
		data.Source.Dataset,
		data.Source.Revision,
		previous,
	}, "\x00")
}

func validExcelPricingRemoteWindow(cursor, oldest, latest uint64) bool {
	if oldest > latest || cursor > latest {
		return false
	}
	return oldest == 0 || cursor >= oldest-1
}

func (client *excelPricingRemoteEventsClient) reconcileExcelPricingRemoteRevision(
	ctx context.Context,
	origin string,
	eventID uint64,
	replayPending bool,
) error {
	streamSource, streamPresent := client.currentStreamSource()
	validation, err := client.probeExcelPricingRemoteRevision(
		ctx, streamSource, client.canConditionallyValidate(streamSource), origin, eventID,
	)
	if err != nil {
		return err
	}
	if replayPending {
		if validation.Outcome == excelPricingRemoteSourceCurrent && streamPresent {
			if validation.Revision != nil {
				if err := client.deliverRevision(*validation.Revision); err != nil {
					return err
				}
				client.setValidatedRevision(*validation.Revision)
			}
			return nil
		}
		gap := excelPricingRemoteSourceLifecycle{
			Mode:                  "validation_gap",
			Source:                streamSource,
			EventID:               eventID,
			ValidationOrigin:      origin,
			ValidationOutcome:     validation.Outcome,
			CurrentSourceRevision: validation.CurrentSourceRevision,
		}
		if err := client.deliverSourceLifecycle(gap); err != nil {
			return err
		}
		client.clearValidatedRevision()
		return nil
	}

	switch validation.Outcome {
	case excelPricingRemoteSourceCurrent:
		if streamPresent {
			if validation.Revision != nil {
				if err := client.deliverRevision(*validation.Revision); err != nil {
					return err
				}
				client.setValidatedRevision(*validation.Revision)
			}
			return nil
		}
		if validation.Revision == nil {
			return errExcelPricingRemoteRevision
		}
		return client.reconcileExcelPricingRemoteSource(
			origin, eventID, streamSource, *validation.Revision,
		)
	case excelPricingRemoteSourceSuperseded:
		currentSource := canonical.Source{
			ID:       streamSource.ID,
			Dataset:  streamSource.Dataset,
			Revision: validation.CurrentSourceRevision,
		}
		current, probeErr := client.probeExcelPricingRemoteRevision(
			ctx, currentSource, false, origin, eventID,
		)
		if probeErr != nil || current.Outcome != excelPricingRemoteSourceCurrent || current.Revision == nil {
			return errExcelPricingRemoteRevision
		}
		return client.reconcileExcelPricingRemoteSource(
			origin, eventID, streamSource, *current.Revision,
		)
	case excelPricingRemoteSourceAbsent:
		if !streamPresent {
			client.clearValidatedRevision()
			return nil
		}
		lifecycle := excelPricingRemoteSourceLifecycle{
			Mode:                   "reconcile_absent",
			Name:                   "pricing.source.removed",
			Change:                 "removed",
			Source:                 streamSource,
			PreviousSourceRevision: streamSource.Revision,
			EventID:                eventID,
			ValidationOrigin:       origin,
			ValidationOutcome:      validation.Outcome,
		}
		if err := client.deliverSourceLifecycle(lifecycle); err != nil {
			return err
		}
		client.applySourceLifecycle(lifecycle)
		return nil
	default:
		return errExcelPricingRemoteRevision
	}
}

func (client *excelPricingRemoteEventsClient) reconcileExcelPricingRemoteSource(
	origin string,
	eventID uint64,
	previous canonical.Source,
	revision excelPricingRemoteRevision,
) error {
	lifecycle := excelPricingRemoteSourceLifecycle{
		Mode:                   "reconcile_present",
		Name:                   "pricing.source.changed",
		Change:                 "reconciled",
		Source:                 revision.Source,
		PreviousSourceRevision: previous.Revision,
		EventID:                eventID,
		ValidationOrigin:       origin,
		ValidationOutcome:      excelPricingRemoteSourceCurrent,
		Revision:               &revision,
	}
	if err := client.deliverSourceLifecycle(lifecycle); err != nil {
		return err
	}
	client.applySourceLifecycle(lifecycle)
	return nil
}

func (client *excelPricingRemoteEventsClient) probeExcelPricingRemoteRevision(
	ctx context.Context,
	source canonical.Source,
	conditional bool,
	origin string,
	eventID uint64,
) (excelPricingRemoteRevisionValidation, error) {
	invalid := excelPricingRemoteRevisionValidation{}
	if !validExcelPricingRemoteSource(source) || !sameExcelPricingRemoteSourceScope(source, client.source) {
		return invalid, errExcelPricingRemoteRevision
	}
	requestURL, err := url.Parse(client.revisionURL)
	if err != nil {
		return invalid, errExcelPricingRemoteRevision
	}
	query := requestURL.Query()
	query.Set("source_id", source.ID)
	query.Set("source_dataset", source.Dataset)
	query.Set("source_revision", source.Revision)
	query.Set("locale", "fa")
	query.Set("page_size", strconv.Itoa(excelPricingSnapshotPageSize))
	query.Set("schema_version", "1")
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return invalid, errExcelPricingRemoteRevision
	}
	request.Header.Set("Accept", "application/json")
	// Revision ETags bind the identity JSON bytes to state_revision. Selecting
	// the identity representation prevents an intermediary from replacing that
	// validator with a gzip-representation ETag before the fail-closed check.
	request.Header.Set("Accept-Encoding", excelPricingRemoteIdentityEncoding)
	request.Header.Set(excelPricingRemoteSecretHeader, client.secret)
	request.Header.Set(excelPricingRemoteSourceIDHeader, source.ID)
	request.Header.Set(excelPricingRemoteDatasetHeader, source.Dataset)
	if conditional {
		streamSource, present := client.currentStreamSource()
		stateRevision, etag := client.currentValidatedRevision()
		if !present || streamSource != source || stateRevision == "" || etag == "" {
			return invalid, errExcelPricingRemoteRevision
		}
		request.Header.Set("If-None-Match", etag)
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return invalid, errExcelPricingRemoteRevision
	}
	defer response.Body.Close()
	if !excelPricingRemoteIdentityResponseEncoding(response.Header) {
		return invalid, errExcelPricingRemoteRevision
	}
	responseETag, singleResponseETag := excelPricingRemoteSingleETag(response.Header)
	if response.StatusCode == http.StatusNotModified {
		stateRevision, currentETag := client.currentValidatedRevision()
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1))
		if !singleResponseETag || !conditional || readErr != nil || len(body) != 0 || stateRevision == "" || currentETag == "" ||
			responseETag != currentETag || !isStrongExcelPricingRevisionETag(responseETag, stateRevision) {
			return invalid, errExcelPricingRemoteRevision
		}
		return excelPricingRemoteRevisionValidation{
			Outcome:     excelPricingRemoteSourceCurrent,
			NotModified: true,
		}, nil
	}
	if response.StatusCode != http.StatusOK {
		if !excelPricingRemoteJSONContentType(response.Header.Get("Content-Type")) ||
			len(response.Header.Values("ETag")) != 0 {
			return invalid, errExcelPricingRemoteRevision
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, excelPricingRemoteRevisionMaxBytes+1))
		if readErr != nil || len(body) == 0 || len(body) > excelPricingRemoteRevisionMaxBytes {
			return invalid, errExcelPricingRemoteRevision
		}
		validation, ok := excelPricingRemoteRevisionConflict(response.StatusCode, body, source)
		if !ok {
			return invalid, errExcelPricingRemoteRevision
		}
		return validation, nil
	}
	if !singleResponseETag || !excelPricingRemoteJSONContentType(response.Header.Get("Content-Type")) {
		return invalid, errExcelPricingRemoteRevision
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, excelPricingRemoteRevisionMaxBytes+1))
	if err != nil || len(body) == 0 || len(body) > excelPricingRemoteRevisionMaxBytes {
		return invalid, errExcelPricingRemoteRevision
	}
	var payload excelPricingRemoteRevisionResponse
	if !decodeExactExcelPricingRemoteJSON(body, &payload) ||
		payload.Schema != excelPricingRemoteRevisionSchema || payload.SchemaVersion != 1 ||
		payload.Projection != excelPricingRemoteProjection ||
		payload.ProjectionSchema != excelPricingRemoteProjectionSchema ||
		payload.Source != source || payload.Locale != "fa" ||
		payload.PageSize != excelPricingSnapshotPageSize ||
		!validExcelPricingRemoteRevisionParts(payload.StateRevision, payload.CatalogRevision,
			payload.PricingStateRevision, payload.PricingPolicyRevision) ||
		!isStrongExcelPricingRevisionETag(responseETag, payload.StateRevision) {
		return invalid, errExcelPricingRemoteRevision
	}
	revision := excelPricingRemoteRevision{
		Source:                payload.Source,
		StateRevision:         payload.StateRevision,
		CatalogRevision:       payload.CatalogRevision,
		PricingStateRevision:  payload.PricingStateRevision,
		PricingPolicyRevision: payload.PricingPolicyRevision,
		ETag:                  responseETag,
		EventID:               eventID,
		ValidationOrigin:      origin,
	}
	return excelPricingRemoteRevisionValidation{
		Outcome:  excelPricingRemoteSourceCurrent,
		Revision: &revision,
	}, nil
}

func excelPricingRemoteRevisionConflict(
	status int,
	body []byte,
	requested canonical.Source,
) (excelPricingRemoteRevisionValidation, bool) {
	invalid := excelPricingRemoteRevisionValidation{}
	if status != http.StatusConflict {
		return invalid, false
	}
	var payload excelPricingRemoteErrorResponse
	if !decodeExactExcelPricingRemoteJSON(body, &payload) || payload.Success == nil || *payload.Success ||
		payload.Retryable == nil || *payload.Retryable ||
		strings.TrimSpace(payload.Message) == "" {
		return invalid, false
	}
	switch payload.Code {
	case "digitalogic_excel_sync_source_scope_conflict":
		if payload.Details.CurrentSource == nil ||
			payload.Details.CurrentSource.ID == nil || *payload.Details.CurrentSource.ID != "" ||
			payload.Details.CurrentSource.Dataset == nil || *payload.Details.CurrentSource.Dataset != "" ||
			payload.Details.CurrentSource.Revision == nil || *payload.Details.CurrentSource.Revision != "" ||
			payload.Details.SubmittedSourceRevision != nil ||
			payload.Details.CurrentSourceRevision != nil || payload.Details.Retryable != nil {
			return invalid, false
		}
		return excelPricingRemoteRevisionValidation{Outcome: excelPricingRemoteSourceAbsent}, true
	case "digitalogic_pricing_snapshot_source_revision_conflict":
		if payload.Details.CurrentSource != nil || payload.Details.SubmittedSourceRevision == nil ||
			payload.Details.CurrentSourceRevision == nil || payload.Details.Retryable == nil ||
			*payload.Details.Retryable || *payload.Details.SubmittedSourceRevision != requested.Revision ||
			!isSHA256Revision(*payload.Details.CurrentSourceRevision) ||
			*payload.Details.CurrentSourceRevision == requested.Revision {
			return invalid, false
		}
		return excelPricingRemoteRevisionValidation{
			Outcome:               excelPricingRemoteSourceSuperseded,
			CurrentSourceRevision: *payload.Details.CurrentSourceRevision,
		}, true
	default:
		return invalid, false
	}
}

func excelPricingRemoteJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	return err == nil && mediaType == "application/json"
}

func excelPricingRemoteSingleETag(header http.Header) (string, bool) {
	values := header.Values("ETag")
	if len(values) != 1 {
		return "", false
	}
	etag := strings.TrimSpace(values[0])
	return etag, etag != ""
}

func excelPricingRemoteIdentityResponseEncoding(header http.Header) bool {
	values := header.Values("Content-Encoding")
	return len(values) == 0 ||
		(len(values) == 1 && strings.EqualFold(strings.TrimSpace(values[0]), excelPricingRemoteIdentityEncoding))
}

func validExcelPricingRemoteRevisionParts(revisions ...string) bool {
	for _, revision := range revisions {
		if !isSHA256Revision(revision) {
			return false
		}
	}
	return true
}

func isStrongExcelPricingRevisionETag(etag, revision string) bool {
	if len(etag) < 3 || strings.HasPrefix(etag, "W/") || etag[0] != '"' || etag[len(etag)-1] != '"' {
		return false
	}
	value := etag[1 : len(etag)-1]
	if !isSHA256Revision(value) {
		return false
	}
	return revision == "" || value == revision
}

func (client *excelPricingRemoteEventsClient) deliverRevision(revision excelPricingRemoteRevision) error {
	if client.onRevision == nil {
		return errExcelPricingRemoteConfiguration
	}
	if err := client.onRevision(revision); err != nil {
		return errExcelPricingRemoteRevision
	}
	return nil
}

func (client *excelPricingRemoteEventsClient) deliverSourceLifecycle(
	lifecycle excelPricingRemoteSourceLifecycle,
) error {
	if client.onLifecycle == nil {
		return errExcelPricingRemoteConfiguration
	}
	if err := client.onLifecycle(lifecycle); err != nil {
		return errExcelPricingRemoteRevision
	}
	return nil
}

func (client *excelPricingRemoteEventsClient) currentCursor() uint64 {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	return client.cursor
}

func (client *excelPricingRemoteEventsClient) advanceCursor(cursor uint64) {
	client.stateMu.Lock()
	changed := cursor > client.cursor
	if changed {
		client.cursor = cursor
	}
	callback := client.onCursor
	client.stateMu.Unlock()
	if changed && callback != nil {
		callback(cursor)
	}
}

func (client *excelPricingRemoteEventsClient) replaceCursor(cursor uint64) {
	client.stateMu.Lock()
	changed := cursor != client.cursor
	client.cursor = cursor
	callback := client.onCursor
	client.stateMu.Unlock()
	if changed && callback != nil {
		callback(cursor)
	}
}

func (client *excelPricingRemoteEventsClient) currentETag() string {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	return client.etag
}

func (client *excelPricingRemoteEventsClient) currentStreamSource() (canonical.Source, bool) {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	return client.streamSource, client.streamPresent
}

func (client *excelPricingRemoteEventsClient) currentValidatedRevision() (string, string) {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	return client.stateRevision, client.etag
}

func (client *excelPricingRemoteEventsClient) canConditionallyValidate(source canonical.Source) bool {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	return client.streamPresent && client.streamSource == source &&
		client.stateRevision != "" && client.etag != ""
}

func (client *excelPricingRemoteEventsClient) setValidatedRevision(revision excelPricingRemoteRevision) {
	client.stateMu.Lock()
	client.streamSource = revision.Source
	client.streamPresent = true
	client.stateRevision = revision.StateRevision
	client.etag = revision.ETag
	client.stateMu.Unlock()
}

func (client *excelPricingRemoteEventsClient) clearValidatedRevision() {
	client.stateMu.Lock()
	client.stateRevision = ""
	client.etag = ""
	client.stateMu.Unlock()
}

func (client *excelPricingRemoteEventsClient) applySourceLifecycle(
	lifecycle excelPricingRemoteSourceLifecycle,
) {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	switch lifecycle.Mode {
	case "ordered":
		client.streamSource = lifecycle.Source
		client.streamPresent = lifecycle.Change != "removed"
	case "reconcile_present":
		client.streamSource = lifecycle.Source
		client.streamPresent = true
	case "reconcile_absent":
		client.streamSource = lifecycle.Source
		client.streamPresent = false
	default:
		return
	}
	if lifecycle.Revision != nil && client.streamPresent {
		client.stateRevision = lifecycle.Revision.StateRevision
		client.etag = lifecycle.Revision.ETag
		return
	}
	client.stateRevision = ""
	client.etag = ""
}

func (client *excelPricingRemoteEventsClient) seenRevision(
	revision excelPricingRemoteRevision,
) (bool, bool) {
	return client.seenEvent(
		"revision\x00"+revision.IdempotencyKey,
		excelPricingRemoteRevisionFingerprint(revision),
	)
}

func excelPricingRemoteRevisionFingerprint(revision excelPricingRemoteRevision) string {
	return strings.Join([]string{
		revision.Source.ID,
		revision.Source.Dataset,
		revision.Source.Revision,
		revision.StateRevision,
		revision.CatalogRevision,
		revision.PricingStateRevision,
		revision.PricingPolicyRevision,
		revision.ETag,
		revision.Cause,
	}, "\x00")
}

func (client *excelPricingRemoteEventsClient) seenEvent(key, fingerprint string) (bool, bool) {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	stored, seen := client.seen[key]
	if !seen {
		return false, false
	}
	return stored == fingerprint, stored != fingerprint
}

func (client *excelPricingRemoteEventsClient) rememberRevision(revision excelPricingRemoteRevision) {
	client.rememberEvent(
		"revision\x00"+revision.IdempotencyKey,
		excelPricingRemoteRevisionFingerprint(revision),
	)
}

func (client *excelPricingRemoteEventsClient) rememberEvent(key, fingerprint string) {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	if _, exists := client.seen[key]; exists {
		return
	}
	client.seen[key] = fingerprint
	client.seenOrder = append(client.seenOrder, key)
	if len(client.seenOrder) > excelPricingSnapshotEventHistory {
		delete(client.seen, client.seenOrder[0])
		client.seenOrder = client.seenOrder[1:]
	}
}

// runExcelPricingRemoteEvents is the compile-isolated lifecycle entrypoint.
// The Server owner must run it under backgroundCtx/backgroundWG with a freshly
// materialized canonical source, restart it whenever that local source changes,
// and supply synchronous revision and snapshot-terminal hooks. The revision
// hook invalidates the old snapshot generation; the terminal hook durably
// retains a waiter event. Only a nil acknowledgement permits the durable
// remote cursor to advance.
func runExcelPricingRemoteEvents(
	ctx context.Context,
	cfg updateout.Config,
	source canonical.Source,
	initialCursor uint64,
	onCursor func(uint64),
	onRevision func(excelPricingRemoteRevision) error,
	onSourceLifecycle func(excelPricingRemoteSourceLifecycle) error,
	onSnapshotTerminal func(excelPricingRemoteSnapshotTerminalEvent) error,
) error {
	client, err := newExcelPricingRemoteEventsClient(cfg, source, excelPricingRemoteEventsOptions{
		InitialCursor:      initialCursor,
		OnCursor:           onCursor,
		OnRevision:         onRevision,
		OnSourceLifecycle:  onSourceLifecycle,
		OnSnapshotTerminal: onSnapshotTerminal,
	})
	if err != nil {
		return err
	}
	return client.Run(ctx)
}
