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
	excelPricingRemoteWebSocketProtocol = "digitalogic.pricing"
	excelPricingRemoteStateEventSchema  = "digitalogic.pricing-state-change"
	excelPricingRemoteSourceEventSchema = "digitalogic.pricing-source-change"
	excelPricingRemoteStreamResetSchema = "digitalogic.pricing-stream-reset"
	excelPricingRemoteRevisionSchema    = "digitalogic.pricing-sync-revision"
	excelPricingRemoteProjection        = "excel"
	excelPricingRemoteProjectionSchema  = "digitalogic.pricing-projection/excel"
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
	errExcelPricingRemoteSourceChanged = errors.New("pricing event subscriber source changed")
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

type excelPricingRemoteEventsOptions struct {
	InitialCursor       uint64
	InitialETag         string
	OnRevision          func(excelPricingRemoteRevision) error
	OnSourceTransition  func(excelPricingRemoteSourceTransition) error
	OnSnapshotTerminal  func(excelPricingRemoteSnapshotTerminalEvent) error
	OnApplyTerminal     func(excelPricingRemoteApplyTerminalEvent) error
	OnConnected         func(context.Context) error
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
	onSource     func(excelPricingRemoteSourceTransition) error
	onTerminal   func(excelPricingRemoteSnapshotTerminalEvent) error
	onApply      func(excelPricingRemoteApplyTerminalEvent) error
	onConnected  func(context.Context) error
	onCursor     func(uint64)
	minBackoff   time.Duration
	maxBackoff   time.Duration

	stateMu       sync.Mutex
	cursor        uint64
	etag          string
	stateRevision string
	seen          map[string]struct{}
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
	Reason                     string `json:"reason"`
	Cursor                     uint64 `json:"cursor"`
	OldestEventID              uint64 `json:"oldest_event_id"`
	LatestEventID              uint64 `json:"latest_event_id"`
	RevisionValidationRequired bool   `json:"revision_validation_required"`
	RevisionPath               string `json:"revision_path"`
}

type excelPricingRemoteSourceTransition struct {
	Schema                     string           `json:"schema"`
	Projection                 string           `json:"projection"`
	Change                     string           `json:"change"`
	Source                     canonical.Source `json:"source"`
	PreviousSourceRevision     *string          `json:"previous_source_revision"`
	IdempotencyKey             string           `json:"idempotency_key"`
	RevisionValidationRequired bool             `json:"revision_validation_required"`
	RevisionPath               string           `json:"revision_path"`
	Audience                   struct {
		Services []string `json:"services"`
	} `json:"audience"`
	Name    string `json:"-"`
	EventID uint64 `json:"-"`
}

type excelPricingRemoteStateEventData struct {
	Schema                string           `json:"schema"`
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

type excelPricingRemoteRevisionResponse struct {
	Schema                string           `json:"schema"`
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

func newExcelPricingRemoteEventsClient(
	cfg updateout.Config,
	source canonical.Source,
	options excelPricingRemoteEventsOptions,
) (*excelPricingRemoteEventsClient, error) {
	cfg, secret, _, err := resolveExcelPricingRemote(cfg, "state")
	if err != nil || !validExcelPricingRemoteSource(source) || options.OnRevision == nil {
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
	return &excelPricingRemoteEventsClient{
		cfg:          cfg,
		source:       source,
		secret:       secret,
		webSocketURL: webSocketURL,
		revisionURL:  revisionURL,
		revisionPath: revisionPath,
		dialer:       dialer,
		httpClient:   httpClient,
		onRevision:   options.OnRevision,
		onSource:     options.OnSourceTransition,
		onTerminal:   options.OnSnapshotTerminal,
		onApply:      options.OnApplyTerminal,
		onConnected:  options.OnConnected,
		onCursor:     options.OnCursor,
		minBackoff:   minBackoff,
		maxBackoff:   maxBackoff,
		cursor:       options.InitialCursor,
		etag:         initialETag,
		seen:         make(map[string]struct{}),
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
		connected, err := client.runConnection(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, errExcelPricingRemoteSourceChanged) {
			return err
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
		if err := client.validateExcelPricingRemoteRevision(ctx, "connection_validation", data.Cursor); err != nil {
			return false, err
		}
		if data.CursorResetRequired {
			client.replaceCursor(data.Cursor)
		} else {
			client.advanceCursor(data.Cursor)
		}
		if client.onConnected != nil {
			if err := client.onConnected(ctx); err != nil {
				return false, errExcelPricingRemoteRevision
			}
		}
		return true, nil
	case "pricing.stream.reset":
		var data excelPricingRemoteStreamResetData
		if frame.ID != 0 || json.Unmarshal(frame.Data, &data) != nil ||
			data.Schema != excelPricingRemoteStreamResetSchema ||
			(data.Reason != "cursor_gap" && data.Reason != "invalid_event") ||
			!data.RevisionValidationRequired ||
			data.RevisionPath != client.revisionPath ||
			!validExcelPricingRemoteWindow(data.Cursor, data.OldestEventID, data.LatestEventID) {
			return false, errExcelPricingRemoteProtocol
		}
		if err := client.validateExcelPricingRemoteRevision(ctx, "cursor_reset", data.Cursor); err != nil {
			return false, err
		}
		client.replaceCursor(data.Cursor)
		return false, nil
	case "pricing.source.changed", "pricing.source.removed":
		if frame.ID == 0 || client.onSource == nil {
			return false, errExcelPricingRemoteProtocol
		}
		if frame.ID <= client.currentCursor() {
			return false, nil
		}
		var transition excelPricingRemoteSourceTransition
		var fields map[string]json.RawMessage
		if json.Unmarshal(frame.Data, &transition) != nil ||
			json.Unmarshal(frame.Data, &fields) != nil ||
			fields["previous_source_revision"] == nil ||
			!validExcelPricingRemoteSourceTransition(eventName, client.source, transition) {
			return false, errExcelPricingRemoteProtocol
		}
		transition.Name = eventName
		transition.EventID = frame.ID
		if err := client.onSource(transition); err != nil {
			return false, errExcelPricingRemoteRevision
		}
		// The source-transition hook durably accepts both the invalidation and
		// this cursor before it fences the old subscriber generation. Updating
		// the client copy without invoking OnCursor avoids losing that accepted
		// cursor after the generation fence has made ordinary callbacks stale.
		client.replaceCursorLocally(frame.ID)
		return false, errExcelPricingRemoteSourceChanged
	case "pricing.state.changed":
		if frame.ID == 0 {
			return false, errExcelPricingRemoteProtocol
		}
		if frame.ID <= client.currentCursor() {
			return false, nil
		}
		var data excelPricingRemoteStateEventData
		if json.Unmarshal(frame.Data, &data) != nil ||
			data.Schema != excelPricingRemoteStateEventSchema ||
			data.Projection != excelPricingRemoteProjection || data.Source != client.source ||
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
		if !client.seenRevision(revision) {
			if err := client.deliverRevision(revision); err != nil {
				return false, err
			}
			client.rememberRevision(revision)
		}
		client.setValidatedRevision(revision.StateRevision, revision.ETag)
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
		if event.Source != client.source || validateExcelPricingRemoteSnapshotTerminalEvent(event) != nil {
			return false, errExcelPricingRemoteProtocol
		}
		if err := client.onTerminal(event); err != nil {
			return false, errExcelPricingRemoteProtocol
		}
		// The cursor is acknowledged only after the request waiter durably retains
		// the terminal event. A disconnect before this point therefore replays it.
		client.advanceCursor(frame.ID)
		return false, nil
	case excelPricingApplyEventName:
		if frame.ID == 0 || client.onApply == nil {
			return false, errExcelPricingRemoteProtocol
		}
		if frame.ID <= client.currentCursor() {
			return false, nil
		}
		var event excelPricingRemoteApplyTerminalEvent
		decoder := json.NewDecoder(bytes.NewReader(frame.Data))
		if decoder.Decode(&event) != nil {
			return false, errExcelPricingRemoteProtocol
		}
		var extra interface{}
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return false, errExcelPricingRemoteProtocol
		}
		event.EventID = frame.ID
		if event.Source != client.source || validateExcelPricingRemoteApplyTerminalEvent(event) != nil {
			return false, errExcelPricingRemoteProtocol
		}
		if err := client.onApply(event); err != nil {
			return false, errExcelPricingRemoteProtocol
		}
		// Durable local acceptance happens inside onApply. Only that successful
		// commit permits Last-Event-ID to advance.
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
	case event == "pricing.state.changed" && (name == "" || name == event):
		return event, true
	case (event == "pricing.source.changed" || event == "pricing.source.removed") &&
		(name == "" || name == event):
		return event, true
	case event == "pricing.snapshot.build.terminal" && (name == "" || name == event):
		return event, true
	case event == excelPricingApplyEventName && (name == "" || name == event):
		return event, true
	case event == "pricing_state_changed" && name == "pricing.state.changed":
		// This is the exact event/name mapping emitted by the WordPress panel
		// bridge. Other unequal aliases remain protocol violations.
		return name, true
	default:
		return "", false
	}
}

func validExcelPricingRemoteSourceTransition(
	name string,
	current canonical.Source,
	transition excelPricingRemoteSourceTransition,
) bool {
	if transition.Schema != excelPricingRemoteSourceEventSchema ||
		transition.Projection != excelPricingRemoteProjection ||
		!validExcelPricingRemoteSource(transition.Source) ||
		transition.Source.ID != current.ID || transition.Source.Dataset != current.Dataset ||
		!isSHA256Revision(transition.IdempotencyKey) ||
		!transition.RevisionValidationRequired || transition.RevisionPath == "" ||
		len(transition.Audience.Services) != 1 ||
		transition.Audience.Services[0] != "patris_pricing" {
		return false
	}
	if transition.RevisionPath != "/wp-json/digitalogic/pricing/sync/revision" {
		return false
	}

	switch name {
	case "pricing.source.changed":
		switch transition.Change {
		case "added":
			return transition.PreviousSourceRevision == nil
		case "changed":
			return transition.PreviousSourceRevision != nil &&
				*transition.PreviousSourceRevision == current.Revision &&
				transition.Source.Revision != current.Revision
		default:
			return false
		}
	case "pricing.source.removed":
		return transition.Change == "removed" && transition.Source == current &&
			transition.PreviousSourceRevision != nil &&
			*transition.PreviousSourceRevision == current.Revision
	default:
		return false
	}
}

func validExcelPricingRemoteWindow(cursor, oldest, latest uint64) bool {
	if oldest > latest || cursor > latest {
		return false
	}
	return oldest == 0 || cursor >= oldest-1
}

func (client *excelPricingRemoteEventsClient) validateExcelPricingRemoteRevision(
	ctx context.Context,
	origin string,
	eventID uint64,
) error {
	requestURL, err := url.Parse(client.revisionURL)
	if err != nil {
		return errExcelPricingRemoteRevision
	}
	query := requestURL.Query()
	query.Set("source_id", client.source.ID)
	query.Set("source_dataset", client.source.Dataset)
	query.Set("source_revision", client.source.Revision)
	query.Set("locale", "fa")
	query.Set("page_size", strconv.Itoa(excelPricingSnapshotPageSize))
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return errExcelPricingRemoteRevision
	}
	request.Header.Set("Accept", "application/json")
	// Revision ETags bind the identity JSON bytes to state_revision. Selecting
	// the identity representation prevents an intermediary from replacing that
	// validator with a gzip-representation ETag before the fail-closed check.
	request.Header.Set("Accept-Encoding", excelPricingRemoteIdentityEncoding)
	request.Header.Set(excelPricingRemoteSecretHeader, client.secret)
	request.Header.Set(excelPricingRemoteSourceIDHeader, client.source.ID)
	request.Header.Set(excelPricingRemoteDatasetHeader, client.source.Dataset)
	if etag := client.currentETag(); etag != "" {
		request.Header.Set("If-None-Match", etag)
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return errExcelPricingRemoteRevision
	}
	defer response.Body.Close()
	responseETag := strings.TrimSpace(response.Header.Get("ETag"))
	if response.StatusCode == http.StatusNotModified {
		if client.currentETag() == "" || responseETag != client.currentETag() {
			return errExcelPricingRemoteRevision
		}
		return nil
	}
	if response.StatusCode != http.StatusOK || !excelPricingRemoteJSONContentType(response.Header.Get("Content-Type")) {
		return errExcelPricingRemoteRevision
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, excelPricingRemoteRevisionMaxBytes+1))
	if err != nil || len(body) == 0 || len(body) > excelPricingRemoteRevisionMaxBytes {
		return errExcelPricingRemoteRevision
	}
	var payload excelPricingRemoteRevisionResponse
	if json.Unmarshal(body, &payload) != nil ||
		payload.Schema != excelPricingRemoteRevisionSchema ||
		payload.Projection != excelPricingRemoteProjection ||
		payload.ProjectionSchema != excelPricingRemoteProjectionSchema ||
		payload.Source != client.source || payload.Locale != "fa" ||
		payload.PageSize != excelPricingSnapshotPageSize ||
		!validExcelPricingRemoteRevisionParts(payload.StateRevision, payload.CatalogRevision,
			payload.PricingStateRevision, payload.PricingPolicyRevision) ||
		!isStrongExcelPricingRevisionETag(responseETag, payload.StateRevision) {
		return errExcelPricingRemoteRevision
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
	if err := client.deliverRevision(revision); err != nil {
		return err
	}
	client.setValidatedRevision(revision.StateRevision, revision.ETag)
	return nil
}

func excelPricingRemoteJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	return err == nil && mediaType == "application/json"
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

func (client *excelPricingRemoteEventsClient) replaceCursorLocally(cursor uint64) {
	client.stateMu.Lock()
	client.cursor = cursor
	client.stateMu.Unlock()
}

func (client *excelPricingRemoteEventsClient) currentETag() string {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	return client.etag
}

func (client *excelPricingRemoteEventsClient) setValidatedRevision(stateRevision, etag string) {
	client.stateMu.Lock()
	client.stateRevision = stateRevision
	client.etag = etag
	client.stateMu.Unlock()
}

func (client *excelPricingRemoteEventsClient) seenRevision(revision excelPricingRemoteRevision) bool {
	key := revision.IdempotencyKey + "\x00" + revision.StateRevision
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	_, seen := client.seen[key]
	return seen
}

func (client *excelPricingRemoteEventsClient) rememberRevision(revision excelPricingRemoteRevision) {
	key := revision.IdempotencyKey + "\x00" + revision.StateRevision
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	if _, exists := client.seen[key]; exists {
		return
	}
	client.seen[key] = struct{}{}
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
	onSourceTransition func(excelPricingRemoteSourceTransition) error,
	onSnapshotTerminal func(excelPricingRemoteSnapshotTerminalEvent) error,
	onApplyTerminal func(excelPricingRemoteApplyTerminalEvent) error,
	onConnected func(context.Context) error,
) error {
	client, err := newExcelPricingRemoteEventsClient(cfg, source, excelPricingRemoteEventsOptions{
		InitialCursor:      initialCursor,
		OnCursor:           onCursor,
		OnRevision:         onRevision,
		OnSourceTransition: onSourceTransition,
		OnSnapshotTerminal: onSnapshotTerminal,
		OnApplyTerminal:    onApplyTerminal,
		OnConnected:        onConnected,
	})
	if err != nil {
		return err
	}
	return client.Run(ctx)
}
