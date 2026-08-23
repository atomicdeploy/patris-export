package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

const (
	excelPricingRemoteSnapshotRequestSchema = "digitalogic.pricing-snapshot-request/v1"
	excelPricingRemoteSnapshotBuildSchema   = "digitalogic.pricing-snapshot-build/v1"
	excelPricingRemoteSnapshotPayloadSchema = "digitalogic.pricing-snapshot/v1"
	excelPricingRemoteSnapshotEventSchema   = "digitalogic.pricing-snapshot-build-event/v1"

	excelPricingRemoteSnapshotMaxResponseBytes = 32 << 20
	excelPricingRemoteSnapshotTerminalHistory  = 256

	excelPricingRemoteSnapshotStageRevisionFetch        = "revision_fetch"
	excelPricingRemoteSnapshotStageTerminalSubscription = "terminal_subscription"
	excelPricingRemoteSnapshotStageSnapshotStart        = "snapshot_start"
	excelPricingRemoteSnapshotStageTerminalWait         = "terminal_wait"
	excelPricingRemoteSnapshotStageTerminalMatch        = "terminal_match"
	excelPricingRemoteSnapshotStageRemoteTerminal       = "remote_terminal"
	excelPricingRemoteSnapshotStageSnapshotPayload      = "snapshot_payload"
)

var (
	errExcelPricingRemoteSnapshotConfiguration = errors.New("remote pricing snapshot configuration is invalid")
	errExcelPricingRemoteSnapshotProtocol      = errors.New("remote pricing snapshot protocol violation")
	errExcelPricingRemoteSnapshotIntegrity     = errors.New("remote pricing snapshot integrity validation failed")
	errExcelPricingRemoteSnapshotRejected      = errors.New("remote pricing snapshot request was rejected")
	errExcelPricingRemoteSnapshotUnavailable   = errors.New("remote pricing snapshot is unavailable")

	excelPricingRemoteSnapshotIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)
)

// excelPricingRemoteSnapshotStageError preserves a bounded stage/code pair
// without retaining a URL, response, credential, request/build identifier, or
// raw transport error in the user-visible snapshot job contract.
type excelPricingRemoteSnapshotStageError struct {
	stage string
	code  string
	cause error
}

func (err *excelPricingRemoteSnapshotStageError) Error() string {
	if err == nil {
		return "remote pricing snapshot stage failed"
	}
	return "remote pricing snapshot " + err.stage + " failed: " + err.code
}

func (err *excelPricingRemoteSnapshotStageError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func wrapExcelPricingRemoteSnapshotStage(stage string, cause error) error {
	if cause == nil {
		return nil
	}
	return &excelPricingRemoteSnapshotStageError{
		stage: stage,
		code:  excelPricingRemoteSnapshotStageCode(stage, cause),
		cause: cause,
	}
}

func excelPricingRemoteSnapshotStageCode(stage string, cause error) string {
	switch stage {
	case excelPricingRemoteSnapshotStageRevisionFetch:
		switch {
		case errors.Is(cause, errExcelPricingRemoteSnapshotProtocol),
			errors.Is(cause, errExcelPricingRemoteSnapshotIntegrity):
			return "snapshot_revision_fetch_protocol_failed"
		case errors.Is(cause, errExcelPricingRemoteSnapshotConfiguration):
			return "snapshot_revision_fetch_configuration_failed"
		default:
			return "snapshot_revision_fetch_unavailable"
		}
	case excelPricingRemoteSnapshotStageTerminalSubscription:
		return "snapshot_terminal_subscription_failed"
	case excelPricingRemoteSnapshotStageSnapshotStart:
		switch {
		case errors.Is(cause, errExcelPricingRemoteSnapshotProtocol),
			errors.Is(cause, errExcelPricingRemoteSnapshotIntegrity):
			return "snapshot_start_protocol_failed"
		case errors.Is(cause, errExcelPricingRemoteSnapshotConfiguration):
			return "snapshot_start_configuration_failed"
		case errors.Is(cause, errExcelPricingRemoteSnapshotRejected):
			return "snapshot_start_rejected"
		default:
			return "snapshot_start_unavailable"
		}
	case excelPricingRemoteSnapshotStageTerminalWait:
		return "snapshot_terminal_wait_failed"
	case excelPricingRemoteSnapshotStageTerminalMatch:
		return "snapshot_terminal_match_failed"
	case excelPricingRemoteSnapshotStageRemoteTerminal:
		return "snapshot_remote_terminal_failed"
	case excelPricingRemoteSnapshotStageSnapshotPayload:
		switch {
		case errors.Is(cause, errExcelPricingRemoteSnapshotIntegrity):
			return "snapshot_payload_integrity_failed"
		case errors.Is(cause, errExcelPricingRemoteSnapshotProtocol):
			return "snapshot_payload_protocol_failed"
		case errors.Is(cause, errExcelPricingRemoteSnapshotConfiguration):
			return "snapshot_payload_configuration_failed"
		default:
			return "snapshot_payload_unavailable"
		}
	default:
		return "snapshot_remote_stage_failed"
	}
}

func excelPricingRemoteSnapshotFailureDetails(err error) (string, string, bool) {
	var staged *excelPricingRemoteSnapshotStageError
	if !errors.As(err, &staged) || staged == nil || staged.stage == "" || staged.code == "" {
		return "", "", false
	}
	return staged.stage, staged.code, true
}

var excelPricingRemoteSnapshotExcelV1Fields = []string{
	"sync_key",
	"reconciliation_status",
	"patris_code",
	"woocommerce_id",
	"parent_id",
	"product_type",
	"publication_status",
	"name",
	"part_number",
	"sku",
	"categories",
	"category_ids",
	"currency",
	"regular_price",
	"sale_price",
	"effective_price",
	"patris_final_price",
	"price_status",
	"stock_quantity",
	"stock_status",
	"patris_total_stock",
	"patris_minimum_stock",
	"patris_location",
	"weight_grams",
	"woocommerce_weight",
	"woocommerce_weight_unit",
	"foreign_price",
	"foreign_currency",
	"partner_price_irr",
	"price_source_amount",
	"price_source_currency",
	"price_source_kind",
	"price_rounding_digits",
	"price_rounding_mode",
	"shipping_method_id",
	"shipping_method_name_en",
	"shipping_method_name_fa",
	"shipping_price_per_kg",
	"shipping_price_per_kg_currency",
	"profit_margin_percent",
	"permalink",
	"image_url",
	"updated_at",
	"sync_status",
	"sync_error",
	"record_revision",
}

type excelPricingRemoteSnapshotEndpoints struct {
	baseURL      *url.URL
	revisionPath string
	startPath    string
	snapshotPath string
	buildPath    string
}

type excelPricingRemoteSnapshotClient struct {
	cfg       updateout.Config
	source    canonical.Source
	secret    string
	endpoints excelPricingRemoteSnapshotEndpoints
	client    *http.Client
	terminals excelPricingRemoteSnapshotTerminalSource
}

type excelPricingRemoteSnapshotClientOptions struct {
	HTTPClient *http.Client
	Terminals  excelPricingRemoteSnapshotTerminalSource
}

type excelPricingRemoteSnapshotRevision struct {
	StateRevision         string
	CatalogRevision       string
	PricingStateRevision  string
	PricingPolicyRevision string
	ETag                  string
}

type excelPricingRemoteSnapshotTerminalEvent struct {
	Schema               string           `json:"schema"`
	SchemaVersion        int              `json:"schema_version"`
	BuildID              string           `json:"build_id"`
	RequestID            string           `json:"request_id"`
	Status               string           `json:"status"`
	Source               canonical.Source `json:"source"`
	StateRevision        string           `json:"state_revision"`
	PricingStateRevision string           `json:"pricing_state_revision"`
	CatalogRevision      string           `json:"catalog_revision"`
	SnapshotToken        string           `json:"snapshot_token,omitempty"`
	SnapshotRevision     string           `json:"snapshot_revision,omitempty"`
	Digest               string           `json:"digest,omitempty"`
	SnapshotPath         string           `json:"snapshot_path,omitempty"`
	Code                 string           `json:"code,omitempty"`
	Retryable            bool             `json:"retryable"`
	IdempotencyKey       string           `json:"idempotency_key"`
	EventID              uint64           `json:"-"`
}

type excelPricingRemoteSnapshotTerminalSubscription interface {
	Wait(context.Context) (excelPricingRemoteSnapshotTerminalEvent, error)
	Close()
}

// excelPricingRemoteSnapshotTerminalSource must be fed only after the
// digitalogic.pricing.v1 WebSocket handshake and frame validation succeed.
// Keeping this dependency mandatory makes cold snapshot activation fail closed
// until WordPress publishes a durable, replayable terminal event.
type excelPricingRemoteSnapshotTerminalSource interface {
	Subscribe(
		requestID string,
		source canonical.Source,
		stateRevision string,
	) (excelPricingRemoteSnapshotTerminalSubscription, error)
}

type excelPricingRemoteSnapshotTerminalHub struct {
	mu       sync.Mutex
	nextID   uint64
	waiters  map[string]map[uint64]*excelPricingRemoteSnapshotHubSubscription
	history  map[string]excelPricingRemoteSnapshotTerminalEvent
	order    []string
	lastSeen uint64
}

type excelPricingRemoteSnapshotHubSubscription struct {
	hub       *excelPricingRemoteSnapshotTerminalHub
	requestID string
	id        uint64
	channel   chan excelPricingRemoteSnapshotTerminalEvent
	once      sync.Once
}

type excelPricingRemoteSnapshotStartRequest struct {
	Schema                string           `json:"schema"`
	SchemaVersion         int              `json:"schema_version"`
	Operation             string           `json:"operation"`
	ClientID              string           `json:"client_id"`
	Channel               string           `json:"channel"`
	RequestID             string           `json:"request_id"`
	IdempotencyKey        string           `json:"idempotency_key"`
	Source                canonical.Source `json:"source"`
	Locale                string           `json:"locale"`
	PageSize              int              `json:"page_size"`
	MaxAgeSeconds         int              `json:"max_age_seconds"`
	ExpectedStateRevision string           `json:"expected_state_revision"`
}

type excelPricingRemoteSnapshotBuildResponse struct {
	Schema               string           `json:"schema"`
	SchemaVersion        int              `json:"schema_version"`
	BuildID              string           `json:"build_id"`
	RequestID            string           `json:"request_id"`
	Status               string           `json:"status"`
	Source               canonical.Source `json:"source"`
	Locale               string           `json:"locale"`
	StateRevision        string           `json:"state_revision"`
	PricingStateRevision string           `json:"pricing_state_revision"`
	CatalogRevision      string           `json:"catalog_revision"`
	SnapshotToken        string           `json:"snapshot_token,omitempty"`
	Revision             string           `json:"revision,omitempty"`
	SnapshotRevision     string           `json:"snapshot_revision,omitempty"`
	Digest               string           `json:"digest,omitempty"`
	StatusURL            string           `json:"status_url"`
	CancelURL            string           `json:"cancel_url"`
	SnapshotURL          string           `json:"snapshot_url,omitempty"`
	Code                 string           `json:"code,omitempty"`
	Retryable            bool             `json:"retryable"`
}

type excelPricingRemoteSnapshotMutationGuard struct {
	ExpectedStateRevision string                                `json:"expected_state_revision"`
	Preview               excelPricingSnapshotMutationOperation `json:"preview"`
	Apply                 excelPricingSnapshotMutationOperation `json:"apply"`
}

type excelPricingRemoteSnapshotIntegrity struct {
	Algorithm             string `json:"algorithm"`
	PayloadDigest         string `json:"payload_digest"`
	StateDigest           string `json:"state_digest"`
	CatalogMetadataDigest string `json:"catalog_metadata_digest"`
	PageRevisionsDigest   string `json:"page_revisions_digest"`
	DatasetRevision       string `json:"dataset_revision"`
	RowCount              int    `json:"row_count"`
	DistinctSyncKeys      int    `json:"distinct_sync_keys"`
	RemoteTotal           int    `json:"remote_total"`
	PageCount             int    `json:"page_count"`
	WarningCount          int    `json:"warning_count"`
}

type excelPricingRemoteSnapshotCatalog struct {
	Dataset         string                         `json:"dataset"`
	Locale          string                         `json:"locale"`
	DatasetRevision string                         `json:"dataset_revision"`
	Columns         json.RawMessage                `json:"columns"`
	Rows            []json.RawMessage              `json:"rows"`
	Reconciliation  json.RawMessage                `json:"reconciliation"`
	Pagination      excelPricingSnapshotPagination `json:"pagination"`
}

type excelPricingRemoteSnapshotPayload struct {
	Schema                string                                  `json:"schema"`
	SchemaVersion         int                                     `json:"schema_version"`
	Projection            string                                  `json:"projection"`
	ProjectionSchema      string                                  `json:"projection_schema"`
	SnapshotToken         string                                  `json:"snapshot_token"`
	Revision              string                                  `json:"revision"`
	SnapshotRevision      string                                  `json:"snapshot_revision"`
	Digest                string                                  `json:"digest"`
	StateRevision         string                                  `json:"state_revision"`
	PricingStateRevision  string                                  `json:"pricing_state_revision"`
	PricingPolicyRevision string                                  `json:"pricing_policy_revision"`
	CatalogRevision       string                                  `json:"catalog_revision"`
	DatasetRevision       string                                  `json:"dataset_revision"`
	Source                canonical.Source                        `json:"source"`
	CreatedAt             string                                  `json:"created_at"`
	ExpiresAt             string                                  `json:"expires_at"`
	RowCount              int                                     `json:"row_count"`
	DistinctSyncKeys      int                                     `json:"distinct_sync_keys"`
	RemoteTotal           int                                     `json:"remote_total"`
	PageSize              int                                     `json:"page_size"`
	PageCount             int                                     `json:"page_count"`
	PageDigests           []string                                `json:"page_digests"`
	Integrity             excelPricingRemoteSnapshotIntegrity     `json:"integrity"`
	MutationGuard         excelPricingRemoteSnapshotMutationGuard `json:"mutation_guard"`
	Settings              json.RawMessage                         `json:"settings"`
	Reconciliation        json.RawMessage                         `json:"reconciliation"`
	Catalog               excelPricingRemoteSnapshotCatalog       `json:"catalog"`
}

type excelPricingRemoteSnapshotResult struct {
	Source                 canonical.Source
	CompositeStateRevision string
	PricingStateRevision   string
	PricingPolicyRevision  string
	CatalogRevision        string
	DatasetRevision        string
	SnapshotRevision       string
	ETag                   string
	MutationStateRevision  string
	Rows                   []json.RawMessage
	ProjectedRows          []json.RawMessage
	ProjectedRowFields     []string
	RawPayload             []byte
}

type excelPricingRemoteSnapshotReconciliation struct {
	Status          string                                    `json:"status"`
	IntegrityStatus string                                    `json:"integrity_status"`
	Warnings        []interface{}                             `json:"warnings"`
	Source          canonical.Source                          `json:"source"`
	Counts          excelPricingRemoteSnapshotReconcileCounts `json:"counts"`
}

type excelPricingRemoteSnapshotReconcileCounts struct {
	PatrisProducts          int `json:"patris_products"`
	WooCommerceRaw          int `json:"woocommerce_raw"`
	WooCommerceLeaves       int `json:"woocommerce_leaves"`
	UnionRows               int `json:"union_rows"`
	Matched                 int `json:"matched"`
	SourceOnly              int `json:"source_only"`
	PatrisOnly              int `json:"patris_only"`
	WooOnly                 int `json:"woo_only"`
	AmbiguousCodes          int `json:"ambiguous_codes"`
	VariableParentsExcluded int `json:"variable_parents_excluded"`
}

func newExcelPricingRemoteSnapshotClient(
	cfg updateout.Config,
	source canonical.Source,
	options excelPricingRemoteSnapshotClientOptions,
) (*excelPricingRemoteSnapshotClient, error) {
	cfg, secret, _, err := resolveExcelPricingRemote(cfg, "state")
	if err != nil || !validExcelPricingRemoteSource(source) || options.Terminals == nil {
		return nil, errExcelPricingRemoteSnapshotConfiguration
	}
	endpoints, err := deriveExcelPricingRemoteSnapshotEndpoints(cfg.URL)
	if err != nil {
		return nil, errExcelPricingRemoteSnapshotConfiguration
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: excelPricingRemoteTimeout(cfg.Timeout)}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if copyClient.Timeout <= 0 {
		copyClient.Timeout = excelPricingRemoteTimeout(cfg.Timeout)
	}
	return &excelPricingRemoteSnapshotClient{
		cfg:       cfg,
		source:    source,
		secret:    secret,
		endpoints: endpoints,
		client:    &copyClient,
		terminals: options.Terminals,
	}, nil
}

func deriveExcelPricingRemoteSnapshotEndpoints(productSyncURL string) (excelPricingRemoteSnapshotEndpoints, error) {
	parsed, err := url.Parse(strings.TrimSpace(productSyncURL))
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Scheme != "https" && !hostnameIsLoopback(parsed.Hostname())) {
		return excelPricingRemoteSnapshotEndpoints{}, errExcelPricingRemoteSnapshotConfiguration
	}
	index := strings.Index(parsed.Path, "/wp-json/")
	if index < 0 {
		return excelPricingRemoteSnapshotEndpoints{}, errExcelPricingRemoteSnapshotConfiguration
	}
	prefix := strings.TrimSuffix(parsed.Path[:index], "/") + "/wp-json/digitalogic/pricing/sync"
	base := *parsed
	base.Path = ""
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""
	return excelPricingRemoteSnapshotEndpoints{
		baseURL:      &base,
		revisionPath: prefix + "/revision",
		startPath:    prefix + "/snapshots",
		snapshotPath: prefix + "/snapshots/",
		buildPath:    prefix + "/builds/",
	}, nil
}

func newExcelPricingRemoteSnapshotTerminalHub() *excelPricingRemoteSnapshotTerminalHub {
	return &excelPricingRemoteSnapshotTerminalHub{
		waiters: make(map[string]map[uint64]*excelPricingRemoteSnapshotHubSubscription),
		history: make(map[string]excelPricingRemoteSnapshotTerminalEvent),
	}
}

func (hub *excelPricingRemoteSnapshotTerminalHub) Subscribe(
	requestID string,
	source canonical.Source,
	stateRevision string,
) (excelPricingRemoteSnapshotTerminalSubscription, error) {
	if hub == nil || !excelPricingRemoteSnapshotIdentifierPattern.MatchString(requestID) ||
		!validExcelPricingRemoteSource(source) || !isSHA256Revision(stateRevision) {
		return nil, errExcelPricingRemoteSnapshotConfiguration
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.nextID++
	subscription := &excelPricingRemoteSnapshotHubSubscription{
		hub:       hub,
		requestID: requestID,
		id:        hub.nextID,
		channel:   make(chan excelPricingRemoteSnapshotTerminalEvent, 1),
	}
	if replay, ok := hub.history[requestID]; ok {
		if replay.Source != source || replay.StateRevision != stateRevision {
			return nil, errExcelPricingRemoteSnapshotProtocol
		}
		subscription.channel <- replay
		close(subscription.channel)
		return subscription, nil
	}
	if hub.waiters[requestID] == nil {
		hub.waiters[requestID] = make(map[uint64]*excelPricingRemoteSnapshotHubSubscription)
	}
	hub.waiters[requestID][subscription.id] = subscription
	return subscription, nil
}

// publishAuthenticated accepts only a frame already authenticated by the
// digitalogic.pricing.v1 transport. No raw WebSocket body or credential enters
// the hub, and the monotonically increasing event ID is enforced here.
func (hub *excelPricingRemoteSnapshotTerminalHub) publishAuthenticated(
	event excelPricingRemoteSnapshotTerminalEvent,
) error {
	if hub == nil || validateExcelPricingRemoteSnapshotTerminalEvent(event) != nil {
		return errExcelPricingRemoteSnapshotProtocol
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if event.EventID <= hub.lastSeen {
		if prior, ok := hub.history[event.RequestID]; ok && prior.EventID == event.EventID && prior == event {
			return nil
		}
		return errExcelPricingRemoteSnapshotProtocol
	}
	if prior, exists := hub.history[event.RequestID]; exists && prior != event {
		return errExcelPricingRemoteSnapshotProtocol
	}
	hub.lastSeen = event.EventID
	hub.history[event.RequestID] = event
	hub.order = append(hub.order, event.RequestID)
	if len(hub.order) > excelPricingRemoteSnapshotTerminalHistory {
		oldest := hub.order[0]
		hub.order = hub.order[1:]
		delete(hub.history, oldest)
	}
	for id, subscription := range hub.waiters[event.RequestID] {
		subscription.channel <- event
		close(subscription.channel)
		delete(hub.waiters[event.RequestID], id)
	}
	delete(hub.waiters, event.RequestID)
	return nil
}

func (subscription *excelPricingRemoteSnapshotHubSubscription) Wait(
	ctx context.Context,
) (excelPricingRemoteSnapshotTerminalEvent, error) {
	if subscription == nil || subscription.channel == nil {
		return excelPricingRemoteSnapshotTerminalEvent{}, errExcelPricingRemoteSnapshotConfiguration
	}
	select {
	case <-ctx.Done():
		return excelPricingRemoteSnapshotTerminalEvent{}, ctx.Err()
	case event, ok := <-subscription.channel:
		if !ok {
			return excelPricingRemoteSnapshotTerminalEvent{}, errExcelPricingRemoteSnapshotUnavailable
		}
		return event, nil
	}
}

func (subscription *excelPricingRemoteSnapshotHubSubscription) Close() {
	if subscription == nil {
		return
	}
	subscription.once.Do(func() {
		hub := subscription.hub
		if hub == nil {
			return
		}
		hub.mu.Lock()
		if waiters := hub.waiters[subscription.requestID]; waiters != nil {
			delete(waiters, subscription.id)
			if len(waiters) == 0 {
				delete(hub.waiters, subscription.requestID)
			}
		}
		hub.mu.Unlock()
	})
}

func (client *excelPricingRemoteSnapshotClient) Collect(
	ctx context.Context,
	requestID string,
	maxAgeSeconds int,
) (*excelPricingRemoteSnapshotResult, error) {
	if client == nil || client.terminals == nil ||
		!excelPricingRemoteSnapshotIdentifierPattern.MatchString(requestID) ||
		maxAgeSeconds < 0 || maxAgeSeconds > int(excelPricingSnapshotMaxCacheAge/time.Second) {
		return nil, errExcelPricingRemoteSnapshotConfiguration
	}
	revision, err := client.fetchRevision(ctx)
	if err != nil {
		return nil, wrapExcelPricingRemoteSnapshotStage(
			excelPricingRemoteSnapshotStageRevisionFetch,
			err,
		)
	}
	// Register before POST. A terminal event emitted by an unusually fast build
	// can therefore be queued while the POST response is still in flight.
	subscription, err := client.terminals.Subscribe(requestID, client.source, revision.StateRevision)
	if err != nil {
		return nil, wrapExcelPricingRemoteSnapshotStage(
			excelPricingRemoteSnapshotStageTerminalSubscription,
			errExcelPricingRemoteSnapshotUnavailable,
		)
	}
	defer subscription.Close()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Once the idempotent start POST is dispatched, keep it alive long enough to
	// receive the build receipt even if the local job is cancelled. Otherwise the
	// provider can accept work while cancellation tears down the response before
	// we learn the build ID needed for the compensating DELETE. The original
	// deadline and the HTTP client's timeout still bound this commit window.
	startContext := context.WithoutCancel(ctx)
	var stopStart context.CancelFunc
	if deadline, ok := ctx.Deadline(); ok {
		startContext, stopStart = context.WithDeadline(startContext, deadline)
	} else {
		startContext, stopStart = context.WithCancel(startContext)
	}
	build, status, err := client.startSnapshot(startContext, requestID, maxAgeSeconds, revision)
	stopStart()
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, wrapExcelPricingRemoteSnapshotStage(
				excelPricingRemoteSnapshotStageSnapshotStart,
				contextErr,
			)
		}
		return nil, wrapExcelPricingRemoteSnapshotStage(
			excelPricingRemoteSnapshotStageSnapshotStart,
			err,
		)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		client.cancelSnapshot(build.CancelURL, build.BuildID)
		return nil, wrapExcelPricingRemoteSnapshotStage(
			excelPricingRemoteSnapshotStageSnapshotStart,
			contextErr,
		)
	}
	if status == http.StatusAccepted {
		event, waitErr := subscription.Wait(ctx)
		if waitErr != nil {
			client.cancelSnapshot(build.CancelURL, build.BuildID)
			return nil, wrapExcelPricingRemoteSnapshotStage(
				excelPricingRemoteSnapshotStageTerminalWait,
				waitErr,
			)
		}
		if validateExcelPricingRemoteSnapshotTerminalMatch(event, build, revision) != nil {
			client.cancelSnapshot(build.CancelURL, build.BuildID)
			return nil, wrapExcelPricingRemoteSnapshotStage(
				excelPricingRemoteSnapshotStageTerminalMatch,
				errExcelPricingRemoteSnapshotProtocol,
			)
		}
		if event.Status != "ready" {
			return nil, wrapExcelPricingRemoteSnapshotStage(
				excelPricingRemoteSnapshotStageRemoteTerminal,
				errExcelPricingRemoteSnapshotUnavailable,
			)
		}
		build.Status = event.Status
		build.SnapshotToken = event.SnapshotToken
		build.SnapshotRevision = event.SnapshotRevision
		build.Revision = event.SnapshotRevision
		build.Digest = event.Digest
		build.SnapshotURL = event.SnapshotPath
	}
	if build.Status != "ready" {
		return nil, wrapExcelPricingRemoteSnapshotStage(
			excelPricingRemoteSnapshotStageRemoteTerminal,
			errExcelPricingRemoteSnapshotUnavailable,
		)
	}
	result, err := client.fetchSnapshot(ctx, build, revision)
	if err != nil {
		return nil, wrapExcelPricingRemoteSnapshotStage(
			excelPricingRemoteSnapshotStageSnapshotPayload,
			err,
		)
	}
	return result, nil
}

func (client *excelPricingRemoteSnapshotClient) fetchRevision(
	ctx context.Context,
) (excelPricingRemoteSnapshotRevision, error) {
	requestURL := client.endpointURL(client.endpoints.revisionPath)
	query := requestURL.Query()
	client.setSourceQuery(query)
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return excelPricingRemoteSnapshotRevision{}, errExcelPricingRemoteSnapshotConfiguration
	}
	client.setRemoteHeaders(request, false)
	response, err := client.client.Do(request)
	if err != nil {
		return excelPricingRemoteSnapshotRevision{}, errExcelPricingRemoteSnapshotUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return excelPricingRemoteSnapshotRevision{}, errExcelPricingRemoteSnapshotUnavailable
	}
	if !excelPricingRemoteJSONContentType(response.Header.Get("Content-Type")) {
		return excelPricingRemoteSnapshotRevision{}, errExcelPricingRemoteSnapshotProtocol
	}
	body, err := readExcelPricingRemoteSnapshotBody(response.Body, excelPricingRemoteRevisionMaxBytes)
	if err != nil {
		return excelPricingRemoteSnapshotRevision{}, err
	}
	if bytes.Contains(body, []byte(client.secret)) {
		return excelPricingRemoteSnapshotRevision{}, errExcelPricingRemoteSnapshotProtocol
	}
	var payload excelPricingRemoteRevisionResponse
	if json.Unmarshal(body, &payload) != nil || payload.Schema != excelPricingRemoteRevisionSchema ||
		payload.SchemaVersion != 1 || payload.Projection != excelPricingRemoteProjection ||
		payload.ProjectionSchema != excelPricingRemoteProjectionSchema || payload.Source != client.source ||
		payload.Locale != "fa" || payload.PageSize != excelPricingSnapshotPageSize ||
		!validExcelPricingRemoteRevisionParts(payload.StateRevision, payload.CatalogRevision,
			payload.PricingStateRevision, payload.PricingPolicyRevision) {
		return excelPricingRemoteSnapshotRevision{}, errExcelPricingRemoteSnapshotProtocol
	}
	etag := strings.TrimSpace(response.Header.Get("ETag"))
	if !isStrongExcelPricingRevisionETag(etag, payload.StateRevision) {
		return excelPricingRemoteSnapshotRevision{}, errExcelPricingRemoteSnapshotProtocol
	}
	return excelPricingRemoteSnapshotRevision{
		StateRevision:         payload.StateRevision,
		CatalogRevision:       payload.CatalogRevision,
		PricingStateRevision:  payload.PricingStateRevision,
		PricingPolicyRevision: payload.PricingPolicyRevision,
		ETag:                  etag,
	}, nil
}

func (client *excelPricingRemoteSnapshotClient) startSnapshot(
	ctx context.Context,
	requestID string,
	maxAgeSeconds int,
	revision excelPricingRemoteSnapshotRevision,
) (excelPricingRemoteSnapshotBuildResponse, int, error) {
	payload := excelPricingRemoteSnapshotStartRequest{
		Schema:                excelPricingRemoteSnapshotRequestSchema,
		SchemaVersion:         1,
		Operation:             "snapshot",
		ClientID:              "patris-export",
		Channel:               excelPricingContractChannel,
		RequestID:             requestID,
		IdempotencyKey:        requestID,
		Source:                client.source,
		Locale:                "fa",
		PageSize:              excelPricingSnapshotPageSize,
		MaxAgeSeconds:         maxAgeSeconds,
		ExpectedStateRevision: revision.StateRevision,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return excelPricingRemoteSnapshotBuildResponse{}, 0, errExcelPricingRemoteSnapshotConfiguration
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		client.endpointURL(client.endpoints.startPath).String(), bytes.NewReader(body))
	if err != nil {
		return excelPricingRemoteSnapshotBuildResponse{}, 0, errExcelPricingRemoteSnapshotConfiguration
	}
	client.setRemoteHeaders(request, true)
	request.Header.Set("Idempotency-Key", requestID)
	request.Header.Set("If-Match", revision.ETag)
	response, err := client.client.Do(request)
	if err != nil {
		return excelPricingRemoteSnapshotBuildResponse{}, 0, errExcelPricingRemoteSnapshotUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return excelPricingRemoteSnapshotBuildResponse{}, response.StatusCode, errExcelPricingRemoteSnapshotRejected
	}
	if !excelPricingRemoteJSONContentType(response.Header.Get("Content-Type")) {
		return excelPricingRemoteSnapshotBuildResponse{}, response.StatusCode, errExcelPricingRemoteSnapshotProtocol
	}
	responseBody, err := readExcelPricingRemoteSnapshotBody(response.Body, excelPricingRemoteRevisionMaxBytes)
	if err != nil {
		return excelPricingRemoteSnapshotBuildResponse{}, response.StatusCode, err
	}
	if bytes.Contains(responseBody, []byte(client.secret)) {
		return excelPricingRemoteSnapshotBuildResponse{}, response.StatusCode, errExcelPricingRemoteSnapshotProtocol
	}
	var build excelPricingRemoteSnapshotBuildResponse
	if json.Unmarshal(responseBody, &build) != nil ||
		validateExcelPricingRemoteSnapshotBuild(build, response.StatusCode, requestID, client.source, revision) != nil {
		return excelPricingRemoteSnapshotBuildResponse{}, response.StatusCode, errExcelPricingRemoteSnapshotProtocol
	}
	if _, err := client.allowedRemoteURL(build.StatusURL, client.endpoints.buildPath+url.PathEscape(build.BuildID)); err != nil {
		return excelPricingRemoteSnapshotBuildResponse{}, response.StatusCode, errExcelPricingRemoteSnapshotProtocol
	}
	if _, err := client.allowedRemoteURL(build.CancelURL, client.endpoints.buildPath+url.PathEscape(build.BuildID)); err != nil {
		return excelPricingRemoteSnapshotBuildResponse{}, response.StatusCode, errExcelPricingRemoteSnapshotProtocol
	}
	if response.StatusCode == http.StatusOK {
		if _, err := client.allowedRemoteURL(build.SnapshotURL,
			client.endpoints.snapshotPath+url.PathEscape(build.SnapshotToken)); err != nil {
			return excelPricingRemoteSnapshotBuildResponse{}, response.StatusCode, errExcelPricingRemoteSnapshotProtocol
		}
	}
	return build, response.StatusCode, nil
}

func (client *excelPricingRemoteSnapshotClient) fetchSnapshot(
	ctx context.Context,
	build excelPricingRemoteSnapshotBuildResponse,
	revision excelPricingRemoteSnapshotRevision,
) (*excelPricingRemoteSnapshotResult, error) {
	expectedPath := client.endpoints.snapshotPath + url.PathEscape(build.SnapshotToken)
	requestURL, err := client.allowedRemoteURL(build.SnapshotURL, expectedPath)
	if err != nil {
		return nil, errExcelPricingRemoteSnapshotProtocol
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, errExcelPricingRemoteSnapshotConfiguration
	}
	client.setRemoteHeaders(request, false)
	response, err := client.client.Do(request)
	if err != nil {
		return nil, errExcelPricingRemoteSnapshotUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errExcelPricingRemoteSnapshotUnavailable
	}
	if !excelPricingRemoteJSONContentType(response.Header.Get("Content-Type")) {
		return nil, errExcelPricingRemoteSnapshotProtocol
	}
	body, err := readExcelPricingRemoteSnapshotBody(response.Body, excelPricingRemoteSnapshotMaxResponseBytes)
	if err != nil {
		return nil, err
	}
	if bytes.Contains(body, []byte(client.secret)) {
		return nil, errExcelPricingRemoteSnapshotProtocol
	}
	var payload excelPricingRemoteSnapshotPayload
	if json.Unmarshal(body, &payload) != nil {
		return nil, errExcelPricingRemoteSnapshotProtocol
	}
	etag := strings.TrimSpace(response.Header.Get("ETag"))
	if err := validateExcelPricingRemoteSnapshotPayload(payload, build, revision, client.source, etag); err != nil {
		return nil, err
	}
	projected, fields, err := projectExcelPricingSnapshotRows(payload.Catalog.Rows, excelPricingSnapshotProjectionExcelV1)
	if err != nil {
		return nil, errExcelPricingRemoteSnapshotIntegrity
	}
	return &excelPricingRemoteSnapshotResult{
		Source:                 payload.Source,
		CompositeStateRevision: payload.StateRevision,
		PricingStateRevision:   payload.PricingStateRevision,
		PricingPolicyRevision:  payload.PricingPolicyRevision,
		CatalogRevision:        payload.CatalogRevision,
		DatasetRevision:        payload.DatasetRevision,
		SnapshotRevision:       payload.SnapshotRevision,
		ETag:                   etag,
		MutationStateRevision:  payload.MutationGuard.ExpectedStateRevision,
		Rows:                   append([]json.RawMessage(nil), payload.Catalog.Rows...),
		ProjectedRows:          projected,
		ProjectedRowFields:     fields,
		RawPayload:             append([]byte(nil), body...),
	}, nil
}

func readExcelPricingRemoteSnapshotBody(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, errExcelPricingRemoteSnapshotUnavailable
	}
	if len(body) == 0 || int64(len(body)) > limit {
		return nil, errExcelPricingRemoteSnapshotProtocol
	}
	return body, nil
}

func (client *excelPricingRemoteSnapshotClient) cancelSnapshot(cancelURL, buildID string) {
	requestURL, err := client.allowedRemoteURL(cancelURL,
		client.endpoints.buildPath+url.PathEscape(buildID))
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, requestURL.String(), nil)
	if err != nil {
		return
	}
	client.setRemoteHeaders(request, false)
	response, err := client.client.Do(request)
	if err == nil && response != nil && response.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
	}
}

func (client *excelPricingRemoteSnapshotClient) setRemoteHeaders(request *http.Request, jsonBody bool) {
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "patris-export-excel-companion/1")
	request.Header.Set(excelPricingRemoteSecretHeader, client.secret)
	request.Header.Set(excelPricingRemoteSourceIDHeader, client.source.ID)
	request.Header.Set(excelPricingRemoteDatasetHeader, client.source.Dataset)
	if jsonBody {
		request.Header.Set("Content-Type", "application/json")
	}
}

func (client *excelPricingRemoteSnapshotClient) setSourceQuery(query url.Values) {
	query.Set("source_id", client.source.ID)
	query.Set("source_dataset", client.source.Dataset)
	query.Set("source_revision", client.source.Revision)
	query.Set("locale", "fa")
	query.Set("page_size", strconv.Itoa(excelPricingSnapshotPageSize))
	query.Set("schema_version", "1")
}

func (client *excelPricingRemoteSnapshotClient) endpointURL(path string) *url.URL {
	result := *client.endpoints.baseURL
	result.Path = path
	result.RawPath = ""
	return &result
}

func (client *excelPricingRemoteSnapshotClient) allowedRemoteURL(
	raw, expectedPath string,
) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.Path != expectedPath {
		return nil, errExcelPricingRemoteSnapshotProtocol
	}
	if parsed.IsAbs() {
		if !strings.EqualFold(parsed.Scheme, client.endpoints.baseURL.Scheme) ||
			!strings.EqualFold(parsed.Host, client.endpoints.baseURL.Host) {
			return nil, errExcelPricingRemoteSnapshotProtocol
		}
	} else {
		if parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") {
			return nil, errExcelPricingRemoteSnapshotProtocol
		}
		resolved := *client.endpoints.baseURL
		resolved.Path = parsed.Path
		resolved.RawQuery = parsed.RawQuery
		parsed = &resolved
	}
	query := parsed.Query()
	allowed := map[string]string{
		"source_id":       client.source.ID,
		"source_dataset":  client.source.Dataset,
		"source_revision": client.source.Revision,
		"locale":          "fa",
		"page_size":       strconv.Itoa(excelPricingSnapshotPageSize),
		"schema_version":  "1",
	}
	for key, values := range query {
		expected, ok := allowed[key]
		if !ok || len(values) != 1 || values[0] != expected {
			return nil, errExcelPricingRemoteSnapshotProtocol
		}
	}
	return parsed, nil
}

func validateExcelPricingRemoteSnapshotBuild(
	build excelPricingRemoteSnapshotBuildResponse,
	status int,
	requestID string,
	source canonical.Source,
	revision excelPricingRemoteSnapshotRevision,
) error {
	if build.Schema != excelPricingRemoteSnapshotBuildSchema || build.SchemaVersion != 1 ||
		!excelPricingRemoteSnapshotIdentifierPattern.MatchString(build.BuildID) ||
		build.RequestID != requestID || build.Source != source || build.Locale != "fa" ||
		build.StateRevision != revision.StateRevision ||
		build.PricingStateRevision != revision.PricingStateRevision ||
		build.CatalogRevision != revision.CatalogRevision {
		return errExcelPricingRemoteSnapshotProtocol
	}
	if status == http.StatusAccepted {
		if build.Status != "queued" && build.Status != "running" {
			return errExcelPricingRemoteSnapshotProtocol
		}
		return nil
	}
	if status != http.StatusOK || build.Status != "ready" ||
		!excelPricingRemoteSnapshotIdentifierPattern.MatchString(build.SnapshotToken) ||
		!validExcelPricingRemoteRevisionParts(build.Revision, build.SnapshotRevision, build.Digest) ||
		build.Revision != build.SnapshotRevision || build.Digest != build.SnapshotRevision {
		return errExcelPricingRemoteSnapshotProtocol
	}
	return nil
}

func validateExcelPricingRemoteSnapshotTerminalEvent(
	event excelPricingRemoteSnapshotTerminalEvent,
) error {
	if event.Schema != excelPricingRemoteSnapshotEventSchema || event.SchemaVersion != 1 ||
		event.EventID == 0 || !excelPricingRemoteSnapshotIdentifierPattern.MatchString(event.BuildID) ||
		!excelPricingRemoteSnapshotIdentifierPattern.MatchString(event.RequestID) ||
		!validExcelPricingRemoteSource(event.Source) ||
		!validExcelPricingRemoteRevisionParts(event.StateRevision, event.PricingStateRevision,
			event.CatalogRevision) || !isSHA256Revision(event.IdempotencyKey) {
		return errExcelPricingRemoteSnapshotProtocol
	}
	switch event.Status {
	case "ready":
		if !excelPricingRemoteSnapshotIdentifierPattern.MatchString(event.SnapshotToken) ||
			!validExcelPricingRemoteRevisionParts(event.SnapshotRevision, event.Digest) ||
			event.SnapshotRevision != event.Digest || strings.TrimSpace(event.SnapshotPath) == "" ||
			event.Code != "" || event.Retryable {
			return errExcelPricingRemoteSnapshotProtocol
		}
	case "failed", "cancelled":
		if strings.TrimSpace(event.Code) == "" || event.SnapshotToken != "" ||
			event.SnapshotRevision != "" || event.Digest != "" || event.SnapshotPath != "" {
			return errExcelPricingRemoteSnapshotProtocol
		}
	default:
		return errExcelPricingRemoteSnapshotProtocol
	}
	return nil
}

func validateExcelPricingRemoteSnapshotTerminalMatch(
	event excelPricingRemoteSnapshotTerminalEvent,
	build excelPricingRemoteSnapshotBuildResponse,
	revision excelPricingRemoteSnapshotRevision,
) error {
	if validateExcelPricingRemoteSnapshotTerminalEvent(event) != nil ||
		event.BuildID != build.BuildID || event.RequestID != build.RequestID ||
		event.Source != build.Source || event.StateRevision != revision.StateRevision ||
		event.PricingStateRevision != revision.PricingStateRevision ||
		event.CatalogRevision != revision.CatalogRevision {
		return errExcelPricingRemoteSnapshotProtocol
	}
	return nil
}

func validateExcelPricingRemoteSnapshotPayload(
	payload excelPricingRemoteSnapshotPayload,
	build excelPricingRemoteSnapshotBuildResponse,
	revision excelPricingRemoteSnapshotRevision,
	source canonical.Source,
	etag string,
) error {
	if payload.Schema != excelPricingRemoteSnapshotPayloadSchema || payload.SchemaVersion != 1 ||
		payload.Projection != excelPricingRemoteProjection ||
		payload.ProjectionSchema != excelPricingRemoteProjectionSchema ||
		payload.SnapshotToken != build.SnapshotToken || payload.Source != source ||
		payload.StateRevision != revision.StateRevision ||
		payload.PricingStateRevision != revision.PricingStateRevision ||
		payload.PricingPolicyRevision != revision.PricingPolicyRevision ||
		payload.CatalogRevision != revision.CatalogRevision ||
		!validExcelPricingRemoteRevisionParts(payload.Revision, payload.SnapshotRevision, payload.Digest,
			payload.DatasetRevision) || payload.Revision != payload.SnapshotRevision ||
		payload.Digest != payload.SnapshotRevision || build.SnapshotRevision != payload.SnapshotRevision ||
		!isStrongExcelPricingRevisionETag(etag, payload.Digest) {
		return errExcelPricingRemoteSnapshotProtocol
	}
	createdAt, createdErr := time.Parse(time.RFC3339, payload.CreatedAt)
	expiresAt, expiresErr := time.Parse(time.RFC3339, payload.ExpiresAt)
	now := time.Now().UTC()
	if createdErr != nil || expiresErr != nil || !expiresAt.After(createdAt) ||
		createdAt.After(now.Add(time.Minute)) || !expiresAt.After(now) ||
		!excelPricingRemoteSnapshotJSONObject(payload.Settings) {
		return errExcelPricingRemoteSnapshotProtocol
	}
	if payload.PageSize != excelPricingSnapshotPageSize || payload.RowCount < 0 ||
		payload.RowCount > excelPricingSnapshotPageSize*excelPricingSnapshotMaxPages ||
		payload.DistinctSyncKeys != payload.RowCount || payload.RemoteTotal != payload.RowCount ||
		payload.PageCount < 1 || payload.PageCount > excelPricingSnapshotMaxPages ||
		payload.PageCount != max(1, (payload.RowCount+payload.PageSize-1)/payload.PageSize) ||
		len(payload.PageDigests) != payload.PageCount || len(payload.Catalog.Rows) != payload.RowCount ||
		payload.Catalog.Dataset != "reconciled_products" || payload.Catalog.Locale != "fa" ||
		payload.Catalog.DatasetRevision != payload.DatasetRevision ||
		payload.Catalog.Pagination.Page != 1 || payload.Catalog.Pagination.Limit != payload.PageSize ||
		payload.Catalog.Pagination.Total != payload.RowCount ||
		payload.Catalog.Pagination.Pages != payload.PageCount || payload.Catalog.Pagination.HasMore {
		return errExcelPricingRemoteSnapshotIntegrity
	}
	if payload.MutationGuard.ExpectedStateRevision != payload.PricingStateRevision ||
		payload.MutationGuard.ExpectedStateRevision == payload.StateRevision ||
		!validExcelPricingRemoteSnapshotMutation(payload.MutationGuard) {
		return errExcelPricingRemoteSnapshotIntegrity
	}
	if err := validateExcelPricingRemoteSnapshotColumns(payload.Catalog.Columns); err != nil {
		return err
	}
	reconciliation, err := validateExcelPricingRemoteSnapshotRows(payload.Catalog.Rows,
		payload.Reconciliation, payload.Catalog.Reconciliation, source, payload.RowCount)
	if err != nil {
		return err
	}
	if payload.Integrity.Algorithm != "sha256" ||
		payload.Integrity.PayloadDigest != payload.Digest ||
		payload.Integrity.DatasetRevision != payload.DatasetRevision ||
		payload.Integrity.RowCount != payload.RowCount ||
		payload.Integrity.DistinctSyncKeys != payload.RowCount ||
		payload.Integrity.RemoteTotal != payload.RowCount ||
		payload.Integrity.PageCount != payload.PageCount ||
		payload.Integrity.WarningCount != len(reconciliation.Warnings) ||
		!validExcelPricingRemoteRevisionParts(payload.Integrity.StateDigest,
			payload.Integrity.CatalogMetadataDigest, payload.Integrity.PageRevisionsDigest) {
		return errExcelPricingRemoteSnapshotIntegrity
	}
	if err := verifyExcelPricingRemoteSnapshotDigests(payload); err != nil {
		return err
	}
	return nil
}

func validExcelPricingRemoteSnapshotMutation(guard excelPricingRemoteSnapshotMutationGuard) bool {
	return guard.Preview.Method == http.MethodPost &&
		guard.Preview.Path == "/wp-json/digitalogic/pricing/sync/preview" &&
		guard.Preview.RequiresIdempotencyKey && guard.Preview.RequiresIfMatch &&
		guard.Apply.Method == http.MethodPost &&
		guard.Apply.Path == "/wp-json/digitalogic/pricing/sync/apply" &&
		guard.Apply.RequiresIdempotencyKey && guard.Apply.RequiresIfMatch &&
		guard.Apply.Confirmation == "APPLY"
}

func validateExcelPricingRemoteSnapshotColumns(raw json.RawMessage) error {
	var columns []map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &columns) != nil {
		return errExcelPricingRemoteSnapshotIntegrity
	}
	if len(columns) != len(excelPricingRemoteSnapshotExcelV1Fields) {
		return errExcelPricingRemoteSnapshotIntegrity
	}
	for index, expected := range excelPricingRemoteSnapshotExcelV1Fields {
		if excelPricingSnapshotString(columns[index]["key"]) != expected {
			return errExcelPricingRemoteSnapshotIntegrity
		}
	}
	return nil
}

func validateExcelPricingRemoteSnapshotRows(
	rows []json.RawMessage,
	reconciliationRaw, catalogReconciliationRaw json.RawMessage,
	source canonical.Source,
	rowCount int,
) (excelPricingRemoteSnapshotReconciliation, error) {
	var reconciliation excelPricingRemoteSnapshotReconciliation
	if len(reconciliationRaw) == 0 || len(catalogReconciliationRaw) == 0 ||
		!bytes.Equal(bytes.TrimSpace(reconciliationRaw), bytes.TrimSpace(catalogReconciliationRaw)) ||
		json.Unmarshal(reconciliationRaw, &reconciliation) != nil ||
		reconciliation.Status != "current" || reconciliation.IntegrityStatus != "current" ||
		reconciliation.Source != source {
		return reconciliation, errExcelPricingRemoteSnapshotIntegrity
	}
	counts := reconciliation.Counts
	if counts.PatrisProducts < 0 || counts.WooCommerceRaw < 0 || counts.WooCommerceLeaves < 0 ||
		counts.UnionRows != rowCount || counts.Matched < 0 || counts.SourceOnly < 0 ||
		counts.PatrisOnly != counts.SourceOnly || counts.WooOnly < 0 || counts.AmbiguousCodes != 0 ||
		counts.VariableParentsExcluded < 0 ||
		counts.PatrisProducts != counts.Matched+counts.PatrisOnly ||
		counts.WooCommerceLeaves != counts.Matched+counts.WooOnly ||
		counts.WooCommerceRaw != counts.WooCommerceLeaves+counts.VariableParentsExcluded ||
		counts.UnionRows != counts.Matched+counts.PatrisOnly+counts.WooOnly {
		return reconciliation, errExcelPricingRemoteSnapshotIntegrity
	}
	var warningDocument interface{}
	if json.Unmarshal(reconciliationRaw, &warningDocument) != nil ||
		excelPricingSnapshotScanWarnings(warningDocument) != nil {
		return reconciliation, errExcelPricingRemoteSnapshotIntegrity
	}
	seen := make(map[string]struct{}, len(rows))
	observed := excelPricingRemoteSnapshotReconcileCounts{}
	expectedFields := make(map[string]struct{}, len(excelPricingRemoteSnapshotExcelV1Fields))
	for _, field := range excelPricingRemoteSnapshotExcelV1Fields {
		expectedFields[field] = struct{}{}
	}
	for _, raw := range rows {
		var row map[string]json.RawMessage
		if json.Unmarshal(raw, &row) != nil || len(row) != len(expectedFields) {
			return reconciliation, errExcelPricingRemoteSnapshotIntegrity
		}
		for field := range row {
			if _, ok := expectedFields[field]; !ok {
				return reconciliation, errExcelPricingRemoteSnapshotIntegrity
			}
		}
		key := excelPricingSnapshotString(row["sync_key"])
		if key == "" {
			return reconciliation, errExcelPricingRemoteSnapshotIntegrity
		}
		if _, exists := seen[key]; exists {
			return reconciliation, errExcelPricingRemoteSnapshotIntegrity
		}
		seen[key] = struct{}{}
		switch excelPricingSnapshotString(row["reconciliation_status"]) {
		case "matched":
			observed.Matched++
		case "patris_only":
			observed.PatrisOnly++
		case "woo_only":
			observed.WooOnly++
		default:
			return reconciliation, errExcelPricingRemoteSnapshotIntegrity
		}
	}
	if len(seen) != rowCount || observed.Matched != counts.Matched ||
		observed.PatrisOnly != counts.PatrisOnly || observed.WooOnly != counts.WooOnly {
		return reconciliation, errExcelPricingRemoteSnapshotIntegrity
	}
	return reconciliation, nil
}

func verifyExcelPricingRemoteSnapshotDigests(payload excelPricingRemoteSnapshotPayload) error {
	pageDigests := make([]string, 0, payload.PageCount)
	for page := 1; page <= payload.PageCount; page++ {
		start := (page - 1) * payload.PageSize
		end := start + payload.PageSize
		if end > len(payload.Catalog.Rows) {
			end = len(payload.Catalog.Rows)
		}
		pageBody, err := marshalExcelPricingRemoteSnapshotJSON(struct {
			Page int               `json:"page"`
			Rows []json.RawMessage `json:"rows"`
		}{page, payload.Catalog.Rows[start:end]})
		if err != nil {
			return errExcelPricingRemoteSnapshotIntegrity
		}
		pageDigests = append(pageDigests, excelPricingSnapshotDigest(pageBody))
	}
	if len(pageDigests) != len(payload.PageDigests) {
		return errExcelPricingRemoteSnapshotIntegrity
	}
	for index := range pageDigests {
		if pageDigests[index] != payload.PageDigests[index] {
			return errExcelPricingRemoteSnapshotIntegrity
		}
	}
	pageRevisionBody, _ := marshalExcelPricingRemoteSnapshotJSON(payload.PageDigests)
	if excelPricingSnapshotDigest(pageRevisionBody) != payload.Integrity.PageRevisionsDigest {
		return errExcelPricingRemoteSnapshotIntegrity
	}
	catalogMetadataBody, _ := marshalExcelPricingRemoteSnapshotJSON(struct {
		DatasetRevision string          `json:"dataset_revision"`
		Columns         json.RawMessage `json:"columns"`
		Reconciliation  json.RawMessage `json:"reconciliation"`
		RowCount        int             `json:"row_count"`
	}{payload.DatasetRevision, payload.Catalog.Columns, payload.Reconciliation, payload.RowCount})
	if excelPricingSnapshotDigest(catalogMetadataBody) != payload.Integrity.CatalogMetadataDigest {
		return errExcelPricingRemoteSnapshotIntegrity
	}
	pricingRevision, _ := json.Marshal(payload.PricingStateRevision)
	stateBody, _ := marshalExcelPricingRemoteSnapshotJSON([]json.RawMessage{
		pricingRevision,
		payload.Settings,
		mustMarshalExcelPricingRemoteSnapshotJSON(payload.MutationGuard),
	})
	if excelPricingSnapshotDigest(stateBody) != payload.Integrity.StateDigest {
		return errExcelPricingRemoteSnapshotIntegrity
	}
	snapshotBody, _ := marshalExcelPricingRemoteSnapshotJSON(struct {
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
	if excelPricingSnapshotDigest(snapshotBody) != payload.Digest {
		return errExcelPricingRemoteSnapshotIntegrity
	}
	return nil
}

func marshalExcelPricingRemoteSnapshotJSON(value interface{}) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func excelPricingRemoteSnapshotJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' || !json.Valid(trimmed) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(trimmed, &object) == nil && object != nil
}

func mustMarshalExcelPricingRemoteSnapshotJSON(value interface{}) json.RawMessage {
	body, err := marshalExcelPricingRemoteSnapshotJSON(value)
	if err != nil {
		return nil
	}
	return body
}

func excelPricingRemoteSnapshotRequestID(jobID string) string {
	digest := sha256.Sum256([]byte("pricing-snapshot\x00" + jobID))
	return "snapshot-" + hex.EncodeToString(digest[:16])
}
