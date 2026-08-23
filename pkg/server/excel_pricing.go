package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

const (
	excelPricingLocalRequestSchema  = "patris.excel-pricing-companion-request/v1"
	excelPricingSessionSchema       = "patris.excel-pricing-companion-session/v1"
	excelPricingRemoteRequestSchema = "digitalogic.pricing-sync-request/v1"
	excelPricingStateSchema         = "digitalogic.pricing-sync-state/v1"
	excelPricingPreviewSchema       = "digitalogic.pricing-sync-preview/v1"
	excelPricingApplySchema         = "digitalogic.pricing-sync-apply/v1"
	excelPricingClientHeader        = "X-Patris-Excel-Client"
	excelPricingClientID            = "digitalogic-price-calculator/v1"
	excelPricingContractClientID    = "digitalogic-price-calculator"
	excelPricingContractChannel     = "excel-workbook"
	excelPricingCSRFHeader          = "X-Patris-Excel-CSRF-Token"
	excelPricingSessionTTL          = 10 * time.Minute
	excelPricingOperationTimeout    = 8 * time.Minute
	excelPricingMaxSessions         = 128
	excelPricingMaxRequestBytes     = 64 * 1024
	excelPricingMaxResponseBytes    = 4 * 1024 * 1024
	excelPricingMaxStatePageSize    = 250
)

var (
	excelPricingIdempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)
	excelPricingProfitPattern      = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,3})(?:\.[0-9]{1,12})?$`)
	excelPricingShippingPattern    = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,17})(?:\.[0-9]{1,12})?$`)
)

type excelPricingSettings struct {
	DollarPrice             int64       `json:"dollar_price"`
	YuanPrice               int64       `json:"yuan_price"`
	EffectiveDate           string      `json:"effective_date"`
	USDEffectiveDate        string      `json:"usd_effective_date"`
	CNYEffectiveDate        string      `json:"cny_effective_date"`
	ProfitMarginPercent     json.Number `json:"profit_margin_percent"`
	AirExpressPricePerKG    json.Number `json:"air_express_price_per_kg"`
	AirExpressCurrency      string      `json:"air_express_currency"`
	ShippingCatalogRevision string      `json:"shipping_catalog_revision"`
	PriceRoundingDigits     json.Number `json:"price_rounding_digits"`
	PriceRoundingMode       string      `json:"price_rounding_mode"`
}

type excelPricingLocalRequest struct {
	Schema                string                `json:"schema"`
	SchemaVersion         int                   `json:"schema_version"`
	Operation             string                `json:"operation"`
	ClientID              string                `json:"client_id"`
	Channel               string                `json:"channel"`
	RequestID             string                `json:"request_id"`
	Source                *canonical.Source     `json:"source,omitempty"`
	Page                  int                   `json:"page,omitempty"`
	Limit                 int                   `json:"limit,omitempty"`
	Locale                string                `json:"locale,omitempty"`
	IdempotencyKey        string                `json:"idempotency_key,omitempty"`
	ExpectedStateRevision string                `json:"expected_state_revision,omitempty"`
	Settings              *excelPricingSettings `json:"settings,omitempty"`
	ProductChanges        json.RawMessage       `json:"product_changes,omitempty"`
	PreviewDigest         string                `json:"preview_digest,omitempty"`
	Confirmation          string                `json:"confirmation,omitempty"`
}

type excelPricingRemoteRequest struct {
	Schema                string                `json:"schema"`
	SchemaVersion         int                   `json:"schema_version"`
	Operation             string                `json:"operation"`
	ClientID              string                `json:"client_id"`
	Channel               string                `json:"channel"`
	RequestID             string                `json:"request_id"`
	Source                canonical.Source      `json:"source"`
	Page                  int                   `json:"page,omitempty"`
	Limit                 int                   `json:"limit,omitempty"`
	Locale                string                `json:"locale,omitempty"`
	IdempotencyKey        string                `json:"idempotency_key,omitempty"`
	ExpectedStateRevision string                `json:"expected_state_revision,omitempty"`
	Settings              *excelPricingSettings `json:"settings,omitempty"`
	ProductChanges        *[]interface{}        `json:"product_changes,omitempty"`
	PreviewDigest         string                `json:"preview_digest,omitempty"`
	Confirmation          string                `json:"confirmation,omitempty"`
}

type excelPricingSession struct {
	tokenHash [sha256.Size]byte
	expiresAt time.Time
}

type excelPricingState struct {
	mu        sync.Mutex
	sessions  map[[sha256.Size]byte]excelPricingSession
	now       func() time.Time
	permit    chan struct{}
	client    *http.Client
	snapshots *excelPricingSnapshotStore
	mutations *excelPricingMutationLedger

	canonical func(context.Context) (recordpipe.Result, error)
	dispatch  func(context.Context, updateout.Config, updateout.Event) (updateout.DeliveryResult, error)

	snapshotCollector       excelPricingSnapshotCollector
	snapshotRevisionCurrent func(canonical.Source, string, string) bool
}

type excelPricingRemoteResponse struct {
	body          []byte
	status        int
	schema        string
	stateRevision string
}

func newExcelPricingState() *excelPricingState {
	state := &excelPricingState{
		sessions: make(map[[sha256.Size]byte]excelPricingSession),
		now:      time.Now,
		permit:   make(chan struct{}, 1),
		client: &http.Client{
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		dispatch: updateout.DispatchWithResult,
	}
	state.snapshots = newExcelPricingSnapshotStore(state.now)
	state.mutations = newExcelPricingMutationLedger(state.now)
	return state
}

func (s *Server) handlePostExcelPricingSession(w http.ResponseWriter, r *http.Request) {
	setExcelPricingResponseHeaders(w)
	if !excelPricingLocalRequestAllowed(r) || !singleHeaderEquals(r, excelPricingClientHeader, excelPricingClientID) {
		writeExcelPricingError(w, http.StatusForbidden, "local_request_required")
		return
	}
	if !singleJSONContentType(r) {
		writeExcelPricingError(w, http.StatusUnsupportedMediaType, "json_required")
		return
	}
	var empty map[string]json.RawMessage
	if err := decodeBoundedJSON(w, r, 1024, &empty); err != nil {
		writeExcelPricingError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if empty == nil || len(empty) != 0 {
		writeExcelPricingError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	token, err := randomExcelPricingToken()
	if err != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "session_unavailable")
		return
	}
	state := s.excelPricing
	now := state.now().UTC()
	state.mu.Lock()
	for key, session := range state.sessions {
		if !session.expiresAt.After(now) {
			delete(state.sessions, key)
		}
	}
	if len(state.sessions) >= excelPricingMaxSessions {
		state.mu.Unlock()
		writeExcelPricingError(w, http.StatusTooManyRequests, "too_many_sessions")
		return
	}
	tokenHash := sha256.Sum256([]byte(token))
	state.sessions[tokenHash] = excelPricingSession{
		tokenHash: tokenHash,
		expiresAt: now.Add(excelPricingSessionTTL),
	}
	state.mu.Unlock()
	writeExcelPricingJSON(w, http.StatusOK, map[string]interface{}{
		"schema":     excelPricingSessionSchema,
		"csrf_token": token,
		"expires_at": now.Add(excelPricingSessionTTL).Format(time.RFC3339),
	})
}

func (s *Server) handlePostExcelPricingState(w http.ResponseWriter, r *http.Request) {
	s.handleExcelPricingOperation(w, r, "state")
}

func (s *Server) handlePostExcelPricingPreview(w http.ResponseWriter, r *http.Request) {
	s.handleExcelPricingOperation(w, r, "preview")
}

func (s *Server) handlePostExcelPricingApply(w http.ResponseWriter, r *http.Request) {
	s.handleExcelPricingOperation(w, r, "apply")
}

func (s *Server) handleExcelPricingOperation(w http.ResponseWriter, r *http.Request, operation string) {
	setExcelPricingResponseHeaders(w)
	if !excelPricingLocalRequestAllowed(r) ||
		!singleHeaderEquals(r, excelPricingClientHeader, excelPricingClientID) ||
		!s.excelPricing.authorizedSession(r) {
		writeExcelPricingError(w, http.StatusForbidden, "local_session_required")
		return
	}
	if !singleJSONContentType(r) {
		writeExcelPricingError(w, http.StatusUnsupportedMediaType, "json_required")
		return
	}
	var local excelPricingLocalRequest
	if err := decodeBoundedJSON(w, r, excelPricingMaxRequestBytes, &local); err != nil {
		writeExcelPricingError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := validateExcelPricingLocalRequest(r, operation, local); err != nil {
		writeExcelPricingError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	mutationFingerprint := ""
	mutationReserved := false
	if operation == "preview" || operation == "apply" {
		mutationFingerprint = excelPricingMutationFingerprint(local)
		begin, replayStatus, replayBody := s.excelPricing.mutations.begin(
			operation,
			local.IdempotencyKey,
			mutationFingerprint,
		)
		switch begin {
		case excelPricingMutationBeginReplay:
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(replayStatus)
			_, _ = w.Write(replayBody)
			return
		case excelPricingMutationBeginRunning:
			writeExcelPricingBusy(w, "pricing_busy")
			return
		case excelPricingMutationBeginConflict:
			writeExcelPricingError(w, http.StatusConflict, "idempotency_conflict")
			return
		case excelPricingMutationBeginNew:
			mutationReserved = true
		}
		defer func() {
			if mutationReserved {
				s.excelPricing.mutations.abort(operation, local.IdempotencyKey, mutationFingerprint)
			}
		}()
		if operation == "apply" {
			exists, matches := s.excelPricing.mutations.previewMatch(
				local.PreviewDigest,
				excelPricingPreviewBindingFingerprint(local),
			)
			if !exists {
				writeExcelPricingError(w, http.StatusConflict, "preview_required")
				return
			}
			if !matches {
				writeExcelPricingError(w, http.StatusConflict, "preview_binding_conflict")
				return
			}
		}
	}

	operationContext, cancel := context.WithTimeout(r.Context(), excelPricingOperationTimeout)
	defer cancel()
	select {
	case s.excelPricing.permit <- struct{}{}:
		defer func() { <-s.excelPricing.permit }()
	default:
		writeExcelPricingBusy(w, "pricing_busy")
		return
	}

	cfg := s.Config()
	if _, _, _, err := resolveExcelPricingRemote(cfg.SendUpdates, operation); err != nil {
		writeExcelPricingError(w, http.StatusServiceUnavailable, "remote_not_configured")
		return
	}
	var source canonical.Source
	if operation == "state" {
		matches, err := s.excelPricingStateSourceMatches(operationContext, local.Source, cfg)
		if err != nil {
			writeExcelPricingError(w, http.StatusServiceUnavailable, "canonical_source_unavailable")
			return
		}
		if !matches {
			writeExcelPricingError(w, http.StatusConflict, "canonical_source_mismatch")
			return
		}
		source = *local.Source
	} else {
		contract, err := s.excelPricingCanonical(operationContext, cfg)
		if err != nil {
			writeExcelPricingError(w, http.StatusServiceUnavailable, "canonical_source_unavailable")
			return
		}
		source = contract.Source
	}
	remoteRequest := buildExcelPricingRemoteRequest(operation, local, source)
	remote, err := s.forwardExcelPricing(operationContext, cfg.SendUpdates, operation, remoteRequest, local)
	if err != nil {
		var remoteError *excelPricingRemoteError
		if errors.As(err, &remoteError) {
			writeExcelPricingError(w, remoteError.status, remoteError.code)
			return
		}
		writeExcelPricingError(w, http.StatusBadGateway, "remote_unavailable")
		return
	}

	if operation == "apply" {
		// The remote mutation may already be committed even if local canonical
		// delivery or readback fails. Shared serialization guarantees there is no
		// concurrent snapshot build, so invalidate warm reuse immediately after
		// the successful remote apply response.
		s.invalidateCanonicalProjection(true)
		s.excelPricing.snapshots.publishPricingStateInvalidated(remote.stateRevision)
		if err := s.completeExcelPricingApply(operationContext, cfg, remote); err != nil {
			writeExcelPricingError(w, http.StatusBadGateway, "post_apply_verification_failed")
			return
		}
		s.excelPricing.snapshots.publishPricingStateVerified(remote.stateRevision)
	} else if operation == "preview" {
		previewDigest, present, err := excelPricingPreviewDigest(remote.body)
		if err != nil {
			writeExcelPricingError(w, http.StatusBadGateway, "remote_contract_invalid")
			return
		}
		if present && !s.excelPricing.mutations.bindPreview(
			previewDigest,
			excelPricingPreviewBindingFingerprint(local),
		) {
			writeExcelPricingError(w, http.StatusConflict, "preview_digest_conflict")
			return
		}
	}
	if mutationReserved {
		s.excelPricing.mutations.complete(
			operation,
			local.IdempotencyKey,
			mutationFingerprint,
			remote.status,
			remote.body,
		)
		mutationReserved = false
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(remote.status)
	_, _ = w.Write(remote.body)
}

func (state *excelPricingState) authorizedSession(r *http.Request) bool {
	values := r.Header.Values(excelPricingCSRFHeader)
	if len(values) != 1 {
		return false
	}
	token := strings.TrimSpace(values[0])
	if len(token) != 43 {
		return false
	}
	hash := sha256.Sum256([]byte(token))
	now := state.now().UTC()
	state.mu.Lock()
	defer state.mu.Unlock()
	session, ok := state.sessions[hash]
	if !ok || !session.expiresAt.After(now) {
		delete(state.sessions, hash)
		return false
	}
	return subtle.ConstantTimeCompare(hash[:], session.tokenHash[:]) == 1
}

func (s *Server) excelPricingCanonical(ctx context.Context, cfg appconfig.Config) (*canonical.Envelope, error) {
	timeout := canonicalRequestTimeout(cfg)
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	projection := s.excelPricing.canonical
	if projection == nil {
		projection = s.canonicalRecordResultContext
	}
	result, err := projection(bounded)
	if err != nil {
		return nil, err
	}
	if result.Contract == nil || result.Contract.Schema != canonical.ContractName ||
		strings.TrimSpace(result.Contract.Source.ID) == "" ||
		strings.TrimSpace(result.Contract.Source.Dataset) == "" ||
		!isSHA256Revision(result.Contract.Source.Revision) {
		return nil, errors.New("canonical source identity is unavailable")
	}
	return result.SyncEnvelope(nil), nil
}

func (s *Server) excelPricingStateSourceMatches(
	ctx context.Context,
	source *canonical.Source,
	cfg appconfig.Config,
) (bool, error) {
	if source == nil || !isSHA256Revision(source.Revision) {
		return false, nil
	}
	contract, err := s.excelPricingCanonical(ctx, cfg)
	if err != nil {
		return false, err
	}
	return *source == contract.Source, nil
}

func buildExcelPricingRemoteRequest(operation string, local excelPricingLocalRequest, source canonical.Source) excelPricingRemoteRequest {
	request := excelPricingRemoteRequest{
		Schema:                excelPricingRemoteRequestSchema,
		SchemaVersion:         1,
		Operation:             operation,
		ClientID:              local.ClientID,
		Channel:               local.Channel,
		RequestID:             local.RequestID,
		Source:                source,
		Page:                  local.Page,
		Limit:                 local.Limit,
		Locale:                local.Locale,
		IdempotencyKey:        local.IdempotencyKey,
		ExpectedStateRevision: local.ExpectedStateRevision,
		Settings:              local.Settings,
		PreviewDigest:         local.PreviewDigest,
		Confirmation:          local.Confirmation,
	}
	if operation == "preview" || operation == "apply" {
		changes := []interface{}{}
		request.ProductChanges = &changes
	}
	return request
}

func (s *Server) forwardExcelPricing(
	ctx context.Context,
	cfg updateout.Config,
	operation string,
	payload excelPricingRemoteRequest,
	local excelPricingLocalRequest,
) (excelPricingRemoteResponse, error) {
	cfg, secret, endpoint, err := resolveExcelPricingRemote(cfg, operation)
	if err != nil {
		return excelPricingRemoteResponse{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > excelPricingMaxRequestBytes {
		return excelPricingRemoteResponse{}, errors.New("pricing request is invalid")
	}
	timeout := excelPricingRemoteTimeout(cfg.Timeout)
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(bounded, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return excelPricingRemoteResponse{}, errors.New("pricing request is invalid")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "patris-export-excel-companion/1")
	request.Header.Set(updateout.ProductSyncSecretHeader, secret)
	if operation == "preview" || operation == "apply" {
		request.Header.Set("Idempotency-Key", local.IdempotencyKey)
		request.Header.Set("If-Match", `"`+local.ExpectedStateRevision+`"`)
	}
	client := s.excelPricing.client
	if client == nil {
		client = newExcelPricingState().client
	}
	copyClient := *client
	copyClient.Timeout = timeout
	copyClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := copyClient.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return excelPricingRemoteResponse{}, errors.New("remote pricing request failed")
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, excelPricingMaxResponseBytes+1))
	responseContentTypes := response.Header.Values("Content-Type")
	response.Body.Close()
	if readErr != nil || len(responseBody) > excelPricingMaxResponseBytes {
		return excelPricingRemoteResponse{}, errors.New("remote pricing response is unavailable")
	}
	if bytes.Contains(responseBody, []byte(secret)) {
		return excelPricingRemoteResponse{}, errors.New("remote pricing response contained protected material")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return excelPricingRemoteResponse{}, safeExcelPricingRemoteError(response.StatusCode, responseBody)
	}
	if len(responseContentTypes) != 1 {
		return excelPricingRemoteResponse{}, errors.New("remote pricing response content type is invalid")
	}
	mediaType, _, mediaErr := mime.ParseMediaType(responseContentTypes[0])
	if mediaErr != nil || (mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json")) {
		return excelPricingRemoteResponse{}, errors.New("remote pricing response content type is invalid")
	}
	expectedSchema := excelPricingExpectedSchema(operation)
	var metadata struct {
		Schema        string `json:"schema"`
		StateRevision string `json:"state_revision"`
	}
	if json.Unmarshal(responseBody, &metadata) != nil ||
		metadata.Schema != expectedSchema ||
		!isSHA256Revision(metadata.StateRevision) {
		return excelPricingRemoteResponse{}, errors.New("remote pricing response contract is invalid")
	}
	return excelPricingRemoteResponse{
		body:          responseBody,
		status:        response.StatusCode,
		schema:        metadata.Schema,
		stateRevision: metadata.StateRevision,
	}, nil
}

func (s *Server) completeExcelPricingApply(
	ctx context.Context,
	cfg appconfig.Config,
	applied excelPricingRemoteResponse,
) error {
	contract, err := s.excelPricingCanonical(ctx, cfg)
	if err != nil {
		return err
	}
	event := updateout.Event{
		Type:             "update",
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		Source:           s.currentDBPath(),
		Raw:              false,
		Contract:         contract,
		SnapshotContract: contract,
	}
	dispatch := s.excelPricing.dispatch
	if dispatch == nil {
		dispatch = updateout.DispatchWithResult
	}
	deliveryConfig := cfg.SendUpdates
	if deliveryConfig.RetryAttempts < 10 {
		deliveryConfig.RetryAttempts = 10
	}
	if backoff, err := time.ParseDuration(deliveryConfig.RetryBackoff); err != nil || backoff < 2*time.Second {
		deliveryConfig.RetryBackoff = "2s"
	}
	delivery, err := dispatch(ctx, deliveryConfig, event)
	if err != nil || !excelPricingDeliveryComplete(delivery, contract.EventID) {
		return errors.New("canonical product-sync delivery failed")
	}

	stateRequest := excelPricingLocalRequest{
		Schema:        excelPricingLocalRequestSchema,
		SchemaVersion: 1,
		Operation:     "state",
		ClientID:      excelPricingContractClientID,
		Channel:       excelPricingContractChannel,
		RequestID: "excel-state-readback-" +
			strings.TrimPrefix(applied.stateRevision, "sha256:")[:32],
		Page:   1,
		Limit:  1,
		Locale: "fa",
	}
	remotePayload := buildExcelPricingRemoteRequest("state", stateRequest, contract.Source)
	state, err := s.forwardExcelPricing(ctx, cfg.SendUpdates, "state", remotePayload, stateRequest)
	if err != nil {
		return err
	}
	if state.schema != excelPricingStateSchema || state.stateRevision != applied.stateRevision {
		return errors.New("post-apply pricing state did not match apply readback")
	}
	return nil
}

func excelPricingDeliveryComplete(result updateout.DeliveryResult, eventID string) bool {
	if result.HTTPStatus < 200 || result.HTTPStatus >= 300 ||
		result.Retryable || result.PendingProducts != 0 ||
		result.DeferredAmbiguous != 0 ||
		result.DeferredProducts != result.DeferredMissing ||
		result.EventID != eventID || result.Attempts < 1 {
		return false
	}
	switch result.Status {
	case "accepted", "already_current", "replayed", "recovered":
		return true
	default:
		return false
	}
}

func validateExcelPricingLocalRequest(r *http.Request, operation string, request excelPricingLocalRequest) error {
	if request.Schema != excelPricingLocalRequestSchema ||
		request.SchemaVersion != 1 ||
		request.Operation != operation ||
		request.ClientID != excelPricingContractClientID ||
		request.Channel != excelPricingContractChannel ||
		!excelPricingIdempotencyPattern.MatchString(request.RequestID) {
		return errors.New("request identity is invalid")
	}
	switch operation {
	case "state":
		if len(request.ProductChanges) != 0 {
			return errors.New("state request has product changes")
		}
		if request.Source == nil || strings.TrimSpace(request.Source.ID) == "" ||
			strings.TrimSpace(request.Source.Dataset) == "" ||
			!isSHA256Revision(request.Source.Revision) {
			return errors.New("state source identity is invalid")
		}
		if request.Page < 1 || request.Page > 1_000_000 ||
			request.Limit < 1 || request.Limit > excelPricingMaxStatePageSize {
			return errors.New("state pagination is invalid")
		}
		if request.Locale != "fa" && request.Locale != "fa_IR" {
			return errors.New("state locale is invalid")
		}
		if request.IdempotencyKey != "" || request.ExpectedStateRevision != "" ||
			request.Settings != nil || request.PreviewDigest != "" || request.Confirmation != "" {
			return errors.New("state request has mutation fields")
		}
	case "preview", "apply":
		if request.Source != nil {
			return errors.New("mutation request has caller-supplied source")
		}
		if !emptyJSONArray(request.ProductChanges) {
			return errors.New("product changes must be an empty array")
		}
		if !excelPricingIdempotencyPattern.MatchString(request.IdempotencyKey) ||
			request.RequestID != request.IdempotencyKey ||
			!singleHeaderEquals(r, "Idempotency-Key", request.IdempotencyKey) ||
			!isSHA256Revision(request.ExpectedStateRevision) ||
			!singleHeaderEquals(r, "If-Match", `"`+request.ExpectedStateRevision+`"`) ||
			request.Settings == nil ||
			validateExcelPricingSettings(*request.Settings) != nil ||
			request.Page != 0 || request.Limit != 0 || request.Locale != "" {
			return errors.New("mutation request is invalid")
		}
		if operation == "preview" {
			if request.PreviewDigest != "" || request.Confirmation != "" {
				return errors.New("preview request has apply fields")
			}
		} else if !isSHA256Revision(request.PreviewDigest) || request.Confirmation != "APPLY" {
			return errors.New("apply confirmation is invalid")
		}
	default:
		return errors.New("operation is invalid")
	}
	return nil
}

func validateExcelPricingSettings(settings excelPricingSettings) error {
	if settings.DollarPrice < 1 || settings.DollarPrice > 1_000_000_000 ||
		settings.YuanPrice < 1 || settings.YuanPrice > 1_000_000_000 {
		return errors.New("currency rate is invalid")
	}
	if !validExcelPricingDate(settings.EffectiveDate) ||
		!validExcelPricingDate(settings.USDEffectiveDate) ||
		!validExcelPricingDate(settings.CNYEffectiveDate) ||
		settings.EffectiveDate != settings.CNYEffectiveDate {
		return errors.New("effective date is invalid")
	}
	profitText := settings.ProfitMarginPercent.String()
	if !excelPricingProfitPattern.MatchString(profitText) {
		return errors.New("profit is invalid")
	}
	profit, err := strconv.ParseFloat(profitText, 64)
	if err != nil || profit < 0 || profit > 1000 {
		return errors.New("profit is invalid")
	}
	shippingText := settings.AirExpressPricePerKG.String()
	if !excelPricingShippingPattern.MatchString(shippingText) {
		return errors.New("shipping price is invalid")
	}
	shipping, err := strconv.ParseFloat(shippingText, 64)
	if err != nil || shipping <= 0 {
		return errors.New("shipping price is invalid")
	}
	if settings.AirExpressCurrency != "CNY" &&
		settings.AirExpressCurrency != "IRR" {
		return errors.New("shipping currency is invalid")
	}
	if !isSHA256Revision(settings.ShippingCatalogRevision) {
		return errors.New("shipping catalog revision is invalid")
	}
	roundingText := settings.PriceRoundingDigits.String()
	roundingDigits, err := strconv.Atoi(roundingText)
	if err != nil || strconv.Itoa(roundingDigits) != roundingText ||
		roundingDigits < 0 || roundingDigits > 9 {
		return errors.New("price rounding digits are invalid")
	}
	if settings.PriceRoundingMode != "nearest_half_up" {
		return errors.New("price rounding mode is invalid")
	}
	return nil
}

func validExcelPricingDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func excelPricingLocalRequestAllowed(r *http.Request) bool {
	if !remoteAddressIsLoopback(r.RemoteAddr) || requestHasProxyEvidence(r) {
		return false
	}
	_, requestHost, ok := effectiveRequestOrigin(r)
	if !ok || !hostnameIsLoopback(requestHost) {
		return false
	}
	if len(r.Header.Values("Origin")) == 0 {
		return true
	}
	_, originHost, ok := strictSameOrigin(r)
	return ok && hostnameIsLoopback(originHost)
}

func excelPricingRemoteURL(productSyncURL, operation string) (string, error) {
	if operation != "state" && operation != "preview" && operation != "apply" {
		return "", errors.New("pricing operation is invalid")
	}
	parsed, err := url.Parse(strings.TrimSpace(productSyncURL))
	if err != nil || parsed.User != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("product-sync destination is invalid")
	}
	if parsed.Scheme != "https" && !hostnameIsLoopback(parsed.Hostname()) {
		return "", errors.New("pricing destination requires HTTPS")
	}
	index := strings.Index(parsed.Path, "/wp-json/")
	if index < 0 {
		return "", errors.New("product-sync destination is not a WordPress REST route")
	}
	prefix := strings.TrimSuffix(parsed.Path[:index], "/")
	parsed.Path = prefix + "/wp-json/digitalogic/pricing/sync/" + operation
	parsed.RawPath = ""
	return parsed.String(), nil
}

func resolveExcelPricingRemote(
	cfg updateout.Config,
	operation string,
) (updateout.Config, string, string, error) {
	cfg = updateout.Normalize(cfg)
	if !cfg.Enabled || cfg.Format != "json" || cfg.Method != http.MethodPost ||
		strings.TrimSpace(cfg.URL) == "" || strings.TrimSpace(cfg.ProductSyncSecretEnv) == "" {
		return cfg, "", "", errors.New("product-sync delivery is not configured")
	}
	endpoint, err := excelPricingRemoteURL(cfg.URL, operation)
	if err != nil {
		return cfg, "", "", err
	}
	secret, err := updateout.ResolveProductSyncSecret(cfg)
	if err != nil || len(secret) < 16 {
		return cfg, "", "", errors.New("product-sync credential is unavailable")
	}
	return cfg, secret, endpoint, nil
}

func excelPricingRemoteTimeout(value string) time.Duration {
	timeout, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || timeout <= 0 {
		timeout = 10 * time.Second
	}
	if timeout < time.Second {
		timeout = time.Second
	}
	if timeout > 2*time.Minute {
		timeout = 2 * time.Minute
	}
	return timeout
}

func excelPricingExpectedSchema(operation string) string {
	switch operation {
	case "state":
		return excelPricingStateSchema
	case "preview":
		return excelPricingPreviewSchema
	case "apply":
		return excelPricingApplySchema
	default:
		return ""
	}
}

type excelPricingRemoteError struct {
	status int
	code   string
}

func (error *excelPricingRemoteError) Error() string {
	return error.code
}

func safeExcelPricingRemoteError(status int, body []byte) error {
	code := "remote_rejected"
	var payload struct {
		Code string `json:"code"`
	}
	if len(body) <= excelPricingMaxResponseBytes && json.Unmarshal(body, &payload) == nil {
		candidate := strings.TrimSpace(payload.Code)
		if candidate != "" && len(candidate) <= 96 {
			valid := true
			for _, character := range candidate {
				if (character >= 'a' && character <= 'z') ||
					(character >= '0' && character <= '9') ||
					character == '_' || character == '-' {
					continue
				}
				valid = false
				break
			}
			if valid {
				code = candidate
			}
		}
	}
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	return &excelPricingRemoteError{status: status, code: code}
}

func decodeBoundedJSON(w http.ResponseWriter, r *http.Request, maximum int64, target interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, maximum)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request contains trailing JSON")
	}
	return nil
}

func jsonContentType(value string) bool {
	value = strings.TrimSpace(strings.Split(value, ";")[0])
	return strings.EqualFold(value, "application/json")
}

func singleJSONContentType(r *http.Request) bool {
	values := r.Header.Values("Content-Type")
	return len(values) == 1 && jsonContentType(values[0])
}

func emptyJSONArray(value json.RawMessage) bool {
	if len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return false
	}
	var items []json.RawMessage
	return json.Unmarshal(value, &items) == nil && items != nil && len(items) == 0
}

func singleHeaderEquals(r *http.Request, name, expected string) bool {
	values := r.Header.Values(name)
	return len(values) == 1 && subtle.ConstantTimeCompare(
		sha256Bytes(strings.TrimSpace(values[0])),
		sha256Bytes(expected),
	) == 1
}

func sha256Bytes(value string) []byte {
	hash := sha256.Sum256([]byte(value))
	return hash[:]
}

func isSHA256Revision(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	if value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
}

func randomExcelPricingToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func setExcelPricingResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Vary", "Origin")
}

func writeExcelPricingError(w http.ResponseWriter, status int, code string) {
	writeExcelPricingJSON(w, status, map[string]interface{}{
		"success": false,
		"code":    code,
		"message": "درخواست همگام‌سازی قیمت به‌صورت ایمن انجام نشد.",
	})
}

func writeExcelPricingJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
