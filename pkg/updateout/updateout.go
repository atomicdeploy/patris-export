package updateout

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/recorddiff"
	"github.com/atomicdeploy/patris-export/pkg/recordsink"
)

type Config struct {
	Enabled              bool              `json:"enabled" yaml:"enabled" toml:"enabled"`
	URL                  string            `json:"url,omitempty" yaml:"url,omitempty" toml:"url,omitempty"`
	Method               string            `json:"method,omitempty" yaml:"method,omitempty" toml:"method,omitempty"`
	Format               string            `json:"format,omitempty" yaml:"format,omitempty" toml:"format,omitempty"`
	Mode                 string            `json:"mode,omitempty" yaml:"mode,omitempty" toml:"mode,omitempty"`
	Initial              bool              `json:"initial" yaml:"initial" toml:"initial"`
	AllowRaw             bool              `json:"allow_raw,omitempty" yaml:"allow_raw,omitempty" toml:"allow_raw,omitempty"`
	RequireContract      bool              `json:"require_contract,omitempty" yaml:"require_contract,omitempty" toml:"require_contract,omitempty"`
	Timeout              string            `json:"timeout,omitempty" yaml:"timeout,omitempty" toml:"timeout,omitempty"`
	RetryAttempts        int               `json:"retry_attempts,omitempty" yaml:"retry_attempts,omitempty" toml:"retry_attempts,omitempty"`
	RetryBackoff         string            `json:"retry_backoff,omitempty" yaml:"retry_backoff,omitempty" toml:"retry_backoff,omitempty"`
	ProductSyncSecretEnv string            `json:"product_sync_secret_env,omitempty" yaml:"product_sync_secret_env,omitempty" toml:"product_sync_secret_env,omitempty"`
	Headers              map[string]string `json:"headers,omitempty" yaml:"headers,omitempty" toml:"headers,omitempty"`
	Command              []string          `json:"command,omitempty" yaml:"command,omitempty" toml:"command,omitempty"`
}

const productSyncSecretHeader = "X-Digitalogic-Product-Sync-Secret"

var (
	errReceiverRejected         = errors.New("receiver rejected request")
	errReceiverHTTPStatus       = errors.New("receiver returned non-success HTTP status")
	errReceiverIdentityMismatch = errors.New("receiver event identity mismatch")
	errReceiverStateMissing     = errors.New("receiver response is missing product-sync delivery state")
	errReceiverStateInvalid     = errors.New("receiver response has inconsistent product-sync delivery state")
	errInvalidDestination       = errors.New("invalid destination URL")
	errRequestFailed            = errors.New("request failed")
	errResponseReadFailed       = errors.New("response read failed")
)

// DeliveryResult exposes the receiver's durable-apply state without retaining
// its response body or any credential material. Generic webhooks leave Status
// and EventID empty.
type DeliveryResult struct {
	HTTPStatus      int
	Status          string
	EventID         string
	Retryable       bool
	PendingProducts int
	Attempts        int
}

// DeliveryError is safe to print. Endpoint query strings, response bodies,
// request headers, and transport error strings are deliberately excluded.
type DeliveryError struct {
	Endpoint        string
	HTTPStatus      int
	Status          string
	Attempts        int
	PendingProducts int
	Retryable       bool
	Reason          string
}

func (e *DeliveryError) Error() string {
	parts := []string{"send update"}
	if e.Endpoint != "" {
		parts = append(parts, "to "+e.Endpoint)
	}
	if e.HTTPStatus != 0 {
		parts = append(parts, fmt.Sprintf("returned HTTP %d", e.HTTPStatus))
	}
	if e.Status != "" {
		parts = append(parts, "status="+e.Status)
	}
	if e.PendingProducts > 0 {
		parts = append(parts, fmt.Sprintf("pending_products=%d", e.PendingProducts))
	}
	if e.Attempts > 0 {
		parts = append(parts, fmt.Sprintf("attempts=%d", e.Attempts))
	}
	if e.Retryable {
		parts = append(parts, "retryable=true")
	}
	if e.Reason != "" {
		parts = append(parts, e.Reason)
	}
	return strings.Join(parts, " ")
}

type Event struct {
	Type             string                   `json:"type"`
	Timestamp        string                   `json:"timestamp"`
	Source           string                   `json:"source,omitempty"`
	Raw              bool                     `json:"raw,omitempty"`
	Records          []map[string]interface{} `json:"records,omitempty"`
	Changes          *recorddiff.ChangeSet    `json:"changes,omitempty"`
	KeyField         string                   `json:"key_field,omitempty"`
	Contract         *canonical.Envelope      `json:"contract,omitempty"`
	SnapshotContract *canonical.Envelope      `json:"snapshot_contract,omitempty"`
}

func Normalize(cfg Config) Config {
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.Method = strings.ToUpper(strings.TrimSpace(cfg.Method))
	if cfg.Method == "" {
		cfg.Method = http.MethodPost
	}
	cfg.Format = strings.ToLower(strings.TrimSpace(cfg.Format))
	if cfg.Format == "" {
		cfg.Format = "json"
	}
	if cfg.Format != "csv" {
		cfg.Format = "json"
	}
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch cfg.Mode {
	case "", "changes", "change", "diff":
		cfg.Mode = "changes"
	case "full", "snapshot", "records":
		cfg.Mode = "full"
	default:
		cfg.Mode = "changes"
	}
	if cfg.Timeout == "" {
		cfg.Timeout = "10s"
	}
	if cfg.RetryAttempts <= 0 {
		cfg.RetryAttempts = 1
	}
	if cfg.RetryAttempts > 10 {
		cfg.RetryAttempts = 10
	}
	if cfg.RetryBackoff == "" {
		cfg.RetryBackoff = "1s"
	}
	cfg.ProductSyncSecretEnv = strings.TrimSpace(cfg.ProductSyncSecretEnv)
	return cfg
}

func Dispatch(ctx context.Context, cfg Config, event Event) error {
	_, err := DispatchWithResult(ctx, cfg, event)
	return err
}

// DispatchWithResult sends through the shared HTTP/command update path and
// returns Digitalogic receiver state when the destination supplies it.
func DispatchWithResult(ctx context.Context, cfg Config, event Event) (DeliveryResult, error) {
	cfg = Normalize(cfg)
	if !cfg.Enabled {
		return DeliveryResult{}, nil
	}
	if event.Raw && !cfg.AllowRaw {
		return DeliveryResult{}, fmt.Errorf("raw outbound updates are disabled; enable allow_raw explicitly only for a trusted non-integration destination")
	}
	if cfg.RequireContract {
		if cfg.Format != "json" {
			return DeliveryResult{}, fmt.Errorf("require_contract requires JSON delivery")
		}
		if selectedContract(cfg, event) == nil {
			return DeliveryResult{}, fmt.Errorf("outbound destination requires a canonical contract")
		}
	}
	if err := validateSecretConfig(cfg, event); err != nil {
		return DeliveryResult{}, err
	}
	if event.Timestamp == "" {
		event.Timestamp = time.Now().Format(time.RFC3339)
	}
	var errs []error
	var result DeliveryResult
	if cfg.URL != "" {
		var err error
		result, err = sendHTTP(ctx, cfg, event)
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(cfg.Command) > 0 {
		if err := runCommand(ctx, cfg, event); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return result, errors.Join(errs...)
	}
	return result, nil
}

func sendHTTP(ctx context.Context, cfg Config, event Event) (DeliveryResult, error) {
	body, contentType, err := encode(cfg, event)
	if err != nil {
		return DeliveryResult{}, err
	}
	timeout, _ := time.ParseDuration(cfg.Timeout)
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	backoff, backoffErr := time.ParseDuration(cfg.RetryBackoff)
	if backoffErr != nil {
		backoff = time.Second
	}
	if backoff < 0 {
		backoff = 0
	}
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	secret, err := resolveProductSyncSecret(cfg)
	if err != nil {
		return DeliveryResult{}, err
	}
	contract := selectedContract(cfg, event)
	endpoint := safeEndpoint(cfg.URL)
	client := http.DefaultClient
	if secret != "" {
		client = &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	var result DeliveryResult
	for attempt := 1; attempt <= cfg.RetryAttempts; attempt++ {
		result = DeliveryResult{Attempts: attempt}
		result, err = sendHTTPAttempt(ctx, client, timeout, cfg, event, contract, body, contentType, secret, result)
		if err == nil && !result.Retryable {
			return result, nil
		}
		if result.Retryable && attempt < cfg.RetryAttempts {
			if err := waitForRetry(ctx, backoff); err != nil {
				return result, deliveryError(endpoint, result, true, "delivery cancelled")
			}
			continue
		}
		reason := deliveryReason(err, result)
		return result, deliveryError(endpoint, result, result.Retryable, reason)
	}
	return result, deliveryError(endpoint, result, result.Retryable, "delivery failed")
}

func sendHTTPAttempt(
	ctx context.Context,
	client *http.Client,
	timeout time.Duration,
	cfg Config,
	event Event,
	contract *canonical.Envelope,
	body []byte,
	contentType string,
	secret string,
	result DeliveryResult,
) (DeliveryResult, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, cfg.Method, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return result, errInvalidDestination
	}
	applyHeaders(req, cfg, event, contract, contentType, secret)
	resp, err := client.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		result.Retryable = true
		return result, errRequestFailed
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	resp.Body.Close()
	result.HTTPStatus = resp.StatusCode
	if readErr != nil {
		result.Retryable = true
		return result, errResponseReadFailed
	}
	return classifyHTTPResponse(result, responseBody, contract, cfg.ProductSyncSecretEnv != "")
}

func applyHeaders(req *http.Request, cfg Config, event Event, contract *canonical.Envelope, contentType, secret string) {
	for key, value := range cfg.Headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "patris-export")
	req.Header.Set("X-Patris-Event", event.Type)
	source := event.Source
	if contract != nil && strings.TrimSpace(contract.Source.ID) != "" {
		source = contract.Source.ID
	}
	req.Header.Set("X-Patris-Source", source)
	if contract != nil {
		req.Header.Set("X-Patris-Contract", contract.Schema)
		req.Header.Set("X-Patris-Contract-Version", contract.SchemaVersion)
		req.Header.Set("X-Patris-Event-ID", contract.EventID)
	}
	if secret != "" {
		req.Header.Set(productSyncSecretHeader, secret)
	}
}

func validateSecretConfig(cfg Config, event Event) error {
	for key := range cfg.Headers {
		if strings.EqualFold(strings.TrimSpace(key), productSyncSecretHeader) {
			return fmt.Errorf("%s is reserved; set product_sync_secret_env to an environment-variable name instead of storing the secret in headers", productSyncSecretHeader)
		}
	}
	if cfg.ProductSyncSecretEnv == "" {
		return nil
	}
	if !validEnvName(cfg.ProductSyncSecretEnv) {
		return fmt.Errorf("product_sync_secret_env must be a portable environment-variable name")
	}
	contract := selectedContract(cfg, event)
	if cfg.URL == "" || cfg.Format != "json" || contract == nil || contract.Schema != canonical.ContractName {
		return fmt.Errorf("product_sync_secret_env requires an HTTP JSON digitalogic.product-sync contract destination")
	}
	endpoint, err := url.Parse(cfg.URL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return fmt.Errorf("product_sync_secret_env requires a valid HTTP or HTTPS destination")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" {
		return fmt.Errorf("product-sync destination must not contain user info or query parameters; authentication is header-only")
	}
	if endpoint.Scheme != "https" && !isLoopbackHost(endpoint.Hostname()) {
		return fmt.Errorf("product-sync destination requires HTTPS except for loopback test or development hosts")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	address := net.ParseIP(strings.TrimSpace(host))
	return address != nil && address.IsLoopback()
}

func validEnvName(value string) bool {
	for index, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' || (index > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return value != ""
}

func resolveProductSyncSecret(cfg Config) (string, error) {
	if cfg.ProductSyncSecretEnv == "" {
		return "", nil
	}
	secret, exists := os.LookupEnv(cfg.ProductSyncSecretEnv)
	secret = strings.TrimSpace(secret)
	if !exists || secret == "" {
		return "", fmt.Errorf("product-sync secret environment variable %q is missing or empty", cfg.ProductSyncSecretEnv)
	}
	return secret, nil
}

type receiverResponse struct {
	Success *bool  `json:"success"`
	Code    string `json:"code"`
	Data    struct {
		Status          string `json:"status"`
		EventID         string `json:"event_id"`
		Retryable       bool   `json:"retryable"`
		PendingProducts int    `json:"pending_products"`
	} `json:"data"`
	Details struct {
		Retryable bool `json:"retryable"`
	} `json:"details"`
}

func classifyHTTPResponse(result DeliveryResult, body []byte, contract *canonical.Envelope, requireReceiverResponse bool) (DeliveryResult, error) {
	var decoded receiverResponse
	isProductSync := contract != nil && contract.Schema == canonical.ContractName
	decodedOK := isProductSync && len(bytes.TrimSpace(body)) > 0 && json.Unmarshal(body, &decoded) == nil && decoded.Success != nil
	receiverRejected := false
	if decodedOK {
		result.Status = safeToken(decoded.Data.Status)
		result.EventID = safeToken(decoded.Data.EventID)
		result.PendingProducts = decoded.Data.PendingProducts
		result.Retryable = decoded.Data.Retryable
		if contract != nil && result.EventID != "" && result.EventID != contract.EventID {
			result.Retryable = false
			return result, errReceiverIdentityMismatch
		}
		if *decoded.Success {
			if err := validateReceiverState(result); err != nil {
				result.Retryable = false
				return result, err
			}
		} else {
			receiverRejected = true
			result.Retryable = decoded.Data.Retryable || decoded.Details.Retryable
			if result.Status == "" {
				result.Status = safeToken(decoded.Code)
			}
		}
	}
	if result.HTTPStatus < 200 || result.HTTPStatus >= 300 {
		if retryableHTTPStatus(result.HTTPStatus) {
			result.Retryable = true
		}
		return result, errReceiverHTTPStatus
	}
	if receiverRejected {
		return result, errReceiverRejected
	}
	if requireReceiverResponse {
		if !decodedOK || result.Status == "" || result.EventID == "" || contract == nil || result.EventID != contract.EventID {
			result.Retryable = false
			return result, errReceiverStateMissing
		}
	}
	return result, nil
}

func validateReceiverState(result DeliveryResult) error {
	switch result.Status {
	case "accepted", "already_current", "replayed", "recovered":
		if result.Retryable || result.PendingProducts != 0 {
			return errReceiverStateInvalid
		}
	case "partially_applied", "retry_pending":
		if !result.Retryable || result.PendingProducts <= 0 {
			return errReceiverStateInvalid
		}
	default:
		return errReceiverStateInvalid
	}
	return nil
}

func deliveryReason(err error, result DeliveryResult) string {
	if err == nil && result.Retryable {
		return "receiver has pending work"
	}
	switch {
	case errors.Is(err, errReceiverIdentityMismatch):
		return errReceiverIdentityMismatch.Error()
	case errors.Is(err, errReceiverStateMissing):
		return errReceiverStateMissing.Error()
	case errors.Is(err, errReceiverStateInvalid):
		return errReceiverStateInvalid.Error()
	case errors.Is(err, errReceiverHTTPStatus):
		return errReceiverHTTPStatus.Error()
	case errors.Is(err, errInvalidDestination):
		return errInvalidDestination.Error()
	case errors.Is(err, errRequestFailed):
		return errRequestFailed.Error()
	case errors.Is(err, errResponseReadFailed):
		return errResponseReadFailed.Error()
	default:
		return errReceiverRejected.Error()
	}
}

func retryableHTTPStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func deliveryError(endpoint string, result DeliveryResult, retryable bool, reason string) error {
	return &DeliveryError{
		Endpoint: endpoint, HTTPStatus: result.HTTPStatus, Status: result.Status,
		Attempts: result.Attempts, PendingProducts: result.PendingProducts,
		Retryable: retryable, Reason: reason,
	}
}

func safeEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "configured endpoint"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func safeToken(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 96 {
		value = value[:96]
	}
	var result strings.Builder
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune("._:-", r) {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func runCommand(ctx context.Context, cfg Config, event Event) error {
	timeout, _ := time.ParseDuration(cfg.Timeout)
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(reqCtx, cfg.Command[0], cfg.Command[1:]...)
	body, _, err := encode(cfg, event)
	if err != nil {
		return err
	}
	cmd.Stdin = bytes.NewReader(body)
	cmd.Env = append(environmentWithout(os.Environ(), cfg.ProductSyncSecretEnv),
		"PATRIS_EXPORT_EVENT_TYPE="+event.Type,
		"PATRIS_EXPORT_EVENT_SOURCE="+event.Source,
		"PATRIS_EXPORT_EVENT_TIMESTAMP="+event.Timestamp,
		"PATRIS_EXPORT_EVENT_KEY_FIELD="+event.KeyField,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("send update command failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func environmentWithout(environ []string, key string) []string {
	if strings.TrimSpace(key) == "" {
		return environ
	}
	filtered := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(name, key) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func encode(cfg Config, event Event) ([]byte, string, error) {
	if contract := selectedContract(cfg, event); cfg.Format == "json" && contract != nil {
		data, err := json.MarshalIndent(contract, "", "  ")
		if err != nil {
			return nil, "", err
		}
		return append(data, '\n'), "application/json; charset=utf-8", nil
	}
	if cfg.Mode == "full" || event.Type == "initial" {
		event.Changes = nil
	} else {
		event.Records = nil
	}
	if cfg.Format == "csv" {
		rows := event.Records
		if len(rows) == 0 && event.Changes != nil {
			rows = rowsFromChanges(event.Changes, event.KeyField)
		}
		data, err := recordsink.CSVBytes(rows, event.KeyField)
		return data, "text/csv; charset=utf-8", err
	}
	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return nil, "", err
	}
	return append(data, '\n'), "application/json; charset=utf-8", nil
}

func selectedContract(cfg Config, event Event) *canonical.Envelope {
	if (cfg.Mode == "full" || event.Type == "initial") && event.SnapshotContract != nil {
		return event.SnapshotContract
	}
	return event.Contract
}

func rowsFromChanges(changes *recorddiff.ChangeSet, keyField string) []map[string]interface{} {
	if changes == nil {
		return nil
	}
	if strings.TrimSpace(keyField) == "" {
		keyField = changes.KeyField
	}
	if strings.TrimSpace(keyField) == "" {
		keyField = "Code"
	}

	rows := []map[string]interface{}{}
	for _, added := range changes.Added {
		row := copyRow(added)
		row["_change_type"] = "added"
		rows = append(rows, row)
	}
	for _, modified := range changes.Modified {
		row := copyRow(modified.Record)
		if len(row) == 0 {
			row = copyRow(modified.NewValues)
		}
		if _, exists := row[keyField]; !exists {
			row[keyField] = modified.Code
		}
		row["_change_type"] = "modified"
		if len(modified.ChangedFields) > 0 {
			row["_changed_fields"] = strings.Join(modified.ChangedFields, ",")
		}
		rows = append(rows, row)
	}
	for _, deleted := range changes.Deleted {
		rows = append(rows, map[string]interface{}{
			keyField:       deleted,
			"_change_type": "deleted",
		})
	}
	return rows
}

func copyRow(row map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(row)+2)
	for key, value := range row {
		copy[key] = value
	}
	return copy
}
