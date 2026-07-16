package pricingcatalog

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type catalogSnapshot struct {
	revision              string
	currencyEffectiveDate string
	selectedWarehouses    []string
	irtPerCNY             *Decimal
	methods               map[string]Method
	fetchedAt             time.Time
	warnings              []string
}

type assignmentSnapshot struct {
	assignment Assignment
	warnings   []string
	fetchedAt  time.Time
}

type assignmentEntry struct {
	code     string
	snapshot assignmentSnapshot
}

type prefetchDiagnostic struct {
	warnings    []string
	fetchedAt   time.Time
	allowSingle bool
}

type assignmentWire struct {
	Code            string   `json:"code"`
	MethodID        string   `json:"import_freight_method_id"`
	ProfitPercent   *Decimal `json:"profit_percent"`
	PricingWarnings []string `json:"pricing_warnings"`
	Markup          struct {
		ProfitPercent *Decimal `json:"profit_percent"`
		Warning       string   `json:"warning"`
	} `json:"markup"`
}

type batchAssignmentResult struct {
	code     string
	snapshot *assignmentSnapshot
	warnings []string
}

type remoteStatusError struct {
	status int
}

func (e *remoteStatusError) Error() string {
	return fmt.Sprintf("HTTP status %d", e.status)
}

type contractError struct {
	reason string
}

func (e *contractError) Error() string {
	return "invalid JSON contract: " + e.reason
}

var (
	errRequestTooLarge  = errors.New("request exceeds configured limit")
	errResponseTooLarge = errors.New("response exceeds configured limit")
)

type httpProvider struct {
	config   DigitalogicConfig
	client   *http.Client
	now      func() time.Time
	freshFor time.Duration
	maxStale time.Duration

	mu             sync.Mutex
	catalogFetchMu sync.Mutex
	prefetchMu     sync.Mutex
	catalog        *catalogSnapshot
	assignments    map[string]*list.Element
	diagnostics    map[string]prefetchDiagnostic
	lru            *list.List
	configError    string
}

func newHTTPProvider(cfg DigitalogicConfig, client *http.Client, now func() time.Time) *httpProvider {
	normalized := Normalize(Config{Mode: ModeDigitalogic, Digitalogic: cfg}).Digitalogic
	if now == nil {
		now = time.Now
	}
	if client == nil {
		client = &http.Client{
			Timeout: parseDuration(normalized.Timeout, defaultTimeout),
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	provider := &httpProvider{
		config:      normalized,
		client:      client,
		now:         now,
		freshFor:    parseDuration(normalized.FreshFor, defaultFreshFor),
		maxStale:    parseDuration(normalized.MaxStale, defaultMaxStale),
		assignments: make(map[string]*list.Element),
		diagnostics: make(map[string]prefetchDiagnostic),
		lru:         list.New(),
	}
	provider.configError = validateBaseURL(normalized.BaseURL)
	if provider.configError == "" {
		if _, err := joinURL(normalized.BaseURL, normalized.CatalogPath); err != nil {
			provider.configError = "pricing_catalog_path_invalid"
		} else if !strings.Contains(normalized.AssignmentPath, "{code}") {
			provider.configError = "pricing_assignment_path_invalid"
		} else if _, err := joinURL(normalized.BaseURL, strings.ReplaceAll(normalized.AssignmentPath, "{code}", "probe")); err != nil {
			provider.configError = "pricing_assignment_path_invalid"
		} else if _, err := joinURL(normalized.BaseURL, normalized.BatchAssignmentPath); err != nil {
			provider.configError = "pricing_assignment_batch_path_invalid"
		}
	}
	return provider
}

func validateBaseURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "pricing_catalog_url_invalid"
	}
	host := strings.ToLower(parsed.Hostname())
	if !strings.EqualFold(parsed.Scheme, "https") && host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return "pricing_catalog_https_required"
	}
	return ""
}

// Prefetch resolves uncached assignments in bounded request-order chunks. A
// server that does not yet expose the versioned endpoint falls back to the
// existing per-Code resolver. Authentication, transport, response-limit, and
// malformed-contract failures are retained as per-Code diagnostics and block
// an unsafe second request path for the current freshness window.
func (p *httpProvider) Prefetch(ctx context.Context, codes []string) {
	if p.configError != "" || len(codes) == 0 {
		return
	}
	p.prefetchMu.Lock()
	defer p.prefetchMu.Unlock()
	if catalog, _, _ := p.resolveCatalog(ctx); catalog == nil {
		return
	}

	now := p.now().UTC()
	seen := make(map[string]struct{}, len(codes))
	pending := make([]string, 0, len(codes))
	p.mu.Lock()
	for _, code := range codes {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		if element := p.assignments[code]; element != nil {
			cached := element.Value.(*assignmentEntry).snapshot
			if withinAge(now, cached.fetchedAt, p.freshFor) {
				p.lru.MoveToFront(element)
				continue
			}
		}
		pending = append(pending, code)
	}
	p.mu.Unlock()

	for offset := 0; offset < len(pending); offset += p.config.BatchSize {
		end := offset + p.config.BatchSize
		if end > len(pending) {
			end = len(pending)
		}
		chunk := pending[offset:end]
		results, err := p.fetchAssignmentBatch(ctx, chunk)
		if err != nil {
			remaining := pending[offset:]
			if isUnsupportedBatchEndpoint(err) {
				p.storePrefetchDiagnostic(remaining, []string{"pricing_assignment_batch_unsupported"}, now, true)
				return
			}
			p.storePrefetchDiagnostic(remaining, []string{batchFailureWarning(err)}, now, false)
			return
		}

		for _, result := range results {
			if result.snapshot != nil {
				snapshot := *result.snapshot
				snapshot.fetchedAt = now
				p.storeAssignment(result.code, snapshot)
				continue
			}
			p.storePrefetchDiagnostic([]string{result.code}, result.warnings, now, false)
		}
	}
}

func (p *httpProvider) Resolve(ctx context.Context, code string) Resolution {
	code = strings.TrimSpace(code)
	if p.configError != "" {
		return finishResolution(Resolution{CatalogStatus: "unavailable", Warnings: []string{p.configError}})
	}

	catalog, catalogStatus, catalogWarnings := p.resolveCatalog(ctx)
	resolution := Resolution{CatalogStatus: catalogStatus, Warnings: append([]string(nil), catalogWarnings...)}
	if catalog == nil {
		resolution.Warnings = append(resolution.Warnings, "pricing_catalog_unavailable")
		return finishResolution(resolution)
	}
	resolution.CatalogRevision = catalog.revision
	resolution.CatalogFetchedAt = catalog.fetchedAt
	resolution.CurrencyEffectiveDate = catalog.currencyEffectiveDate
	resolution.SelectedWarehouses = append([]string(nil), catalog.selectedWarehouses...)
	resolution.IRTPerCNY = cloneDecimal(catalog.irtPerCNY)
	resolution.Warnings = append(resolution.Warnings, catalog.warnings...)

	assignment, assignmentStatus, assignmentWarnings := p.resolveAssignment(ctx, code)
	resolution.Warnings = append(resolution.Warnings, assignmentWarnings...)
	if assignmentStatus == "stale" {
		resolution.Warnings = append(resolution.Warnings, "product_pricing_assignment_stale")
	}
	if assignment == nil || strings.TrimSpace(assignment.assignment.MethodID) == "" {
		resolution.Warnings = append(resolution.Warnings, "import_freight_method_missing")
		return finishResolution(resolution)
	}

	resolution.MethodID = strings.TrimSpace(assignment.assignment.MethodID)
	resolution.MarkupPercent = cloneDecimal(assignment.assignment.ProfitPercent)
	method, exists := catalog.methods[resolution.MethodID]
	if !exists {
		resolution.Warnings = append(resolution.Warnings, "import_freight_method_unknown")
	} else {
		resolution.FreightCNYPerKg = cloneDecimal(method.PricePerKgCNY)
		if method.Enabled != nil && !*method.Enabled {
			resolution.Warnings = append(resolution.Warnings, "import_freight_method_disabled")
		}
	}
	return finishResolution(resolution)
}

func (p *httpProvider) resolveCatalog(ctx context.Context) (*catalogSnapshot, string, []string) {
	now := p.now().UTC()
	p.mu.Lock()
	cached := p.catalog
	if cached != nil && withinAge(now, cached.fetchedAt, p.freshFor) {
		copy := cloneCatalog(cached)
		p.mu.Unlock()
		return copy, "fresh", nil
	}
	p.mu.Unlock()
	p.catalogFetchMu.Lock()
	defer p.catalogFetchMu.Unlock()

	// Another resolver may have refreshed the catalog while this caller waited.
	now = p.now().UTC()
	p.mu.Lock()
	cached = p.catalog
	if cached != nil && withinAge(now, cached.fetchedAt, p.freshFor) {
		copy := cloneCatalog(cached)
		p.mu.Unlock()
		return copy, "fresh", nil
	}
	p.mu.Unlock()

	fetched, err := p.fetchCatalog(ctx)
	if err == nil {
		fetched.fetchedAt = now
		p.mu.Lock()
		p.catalog = cloneCatalog(fetched)
		p.mu.Unlock()
		return fetched, "fresh", nil
	}

	if cached != nil && withinAge(now, cached.fetchedAt, p.maxStale) {
		return cloneCatalog(cached), "stale", []string{"pricing_catalog_stale"}
	}
	return nil, "unavailable", []string{"pricing_catalog_fetch_failed"}
}

func (p *httpProvider) resolveAssignment(ctx context.Context, code string) (*assignmentSnapshot, string, []string) {
	now := p.now().UTC()
	prefetchWarnings := []string(nil)
	p.mu.Lock()
	if diagnostic, ok := p.diagnostics[code]; ok {
		if withinAge(now, diagnostic.fetchedAt, p.freshFor) {
			prefetchWarnings = append(prefetchWarnings, diagnostic.warnings...)
			if !diagnostic.allowSingle {
				if element := p.assignments[code]; element != nil {
					cached := element.Value.(*assignmentEntry).snapshot
					if withinAge(now, cached.fetchedAt, p.maxStale) {
						p.lru.MoveToFront(element)
						p.mu.Unlock()
						copy := cached
						warnings := append(append([]string(nil), cached.warnings...), prefetchWarnings...)
						return &copy, "stale", normalizedStrings(warnings)
					}
				}
				p.mu.Unlock()
				return nil, "unavailable", normalizedStrings(prefetchWarnings)
			}
		} else {
			delete(p.diagnostics, code)
		}
	}
	if element, ok := p.assignments[code]; ok {
		p.lru.MoveToFront(element)
		cached := element.Value.(*assignmentEntry).snapshot
		if withinAge(now, cached.fetchedAt, p.freshFor) {
			p.mu.Unlock()
			copy := cached
			warnings := append(append([]string(nil), cached.warnings...), prefetchWarnings...)
			return &copy, "fresh", normalizedStrings(warnings)
		}
	}
	p.mu.Unlock()

	fetched, err := p.fetchAssignment(ctx, code)
	if err == nil {
		fetched.fetchedAt = now
		p.storeAssignment(code, fetched)
		copy := fetched
		warnings := append(append([]string(nil), fetched.warnings...), prefetchWarnings...)
		return &copy, "fresh", normalizedStrings(warnings)
	}

	p.mu.Lock()
	element := p.assignments[code]
	if element != nil {
		cached := element.Value.(*assignmentEntry).snapshot
		if withinAge(now, cached.fetchedAt, p.maxStale) {
			p.lru.MoveToFront(element)
			p.mu.Unlock()
			copy := cached
			warnings := append(append([]string(nil), cached.warnings...), prefetchWarnings...)
			warnings = append(warnings, "product_pricing_assignment_fetch_failed")
			return &copy, "stale", normalizedStrings(warnings)
		}
	}
	p.mu.Unlock()
	prefetchWarnings = append(prefetchWarnings, "product_pricing_assignment_fetch_failed")
	return nil, "unavailable", normalizedStrings(prefetchWarnings)
}

func (p *httpProvider) storeAssignment(code string, snapshot assignmentSnapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.diagnostics, code)
	if element, exists := p.assignments[code]; exists {
		element.Value.(*assignmentEntry).snapshot = snapshot
		p.lru.MoveToFront(element)
		return
	}
	element := p.lru.PushFront(&assignmentEntry{code: code, snapshot: snapshot})
	p.assignments[code] = element
	for p.lru.Len() > p.config.MaxEntries {
		oldest := p.lru.Back()
		if oldest == nil {
			break
		}
		delete(p.assignments, oldest.Value.(*assignmentEntry).code)
		p.lru.Remove(oldest)
	}
}

func (p *httpProvider) storePrefetchDiagnostic(codes, warnings []string, fetchedAt time.Time, allowSingle bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	warnings = normalizedStrings(warnings)
	for _, code := range codes {
		p.diagnostics[code] = prefetchDiagnostic{
			warnings:    append([]string(nil), warnings...),
			fetchedAt:   fetchedAt,
			allowSingle: allowSingle,
		}
	}
}

func (p *httpProvider) fetchCatalog(ctx context.Context) (*catalogSnapshot, error) {
	var wire struct {
		Schema             string   `json:"schema"`
		SchemaVersion      string   `json:"schema_version"`
		Revision           string   `json:"revision"`
		SelectedWarehouses []string `json:"selected_warehouses"`
		Currency           struct {
			Local         string   `json:"local"`
			CNYToLocal    *Decimal `json:"cny_to_local"`
			CNYToIRT      *Decimal `json:"cny_to_irt"`
			EffectiveDate string   `json:"effective_date"`
			Warnings      []string `json:"warnings"`
		} `json:"currency"`
		Pricing struct {
			FormulaID       string `json:"formula_id"`
			FormulaRevision string `json:"formula_revision"`
		} `json:"pricing"`
		Methods []Method `json:"import_freight_methods"`
	}
	if err := p.getJSON(ctx, p.config.CatalogPath, &wire); err != nil {
		return nil, err
	}
	warnings := append([]string(nil), wire.Currency.Warnings...)
	schemaCompatible := wire.Schema == "digitalogic.integration-catalog" && majorVersion(wire.SchemaVersion) == "1"
	if !schemaCompatible {
		warnings = append(warnings, "pricing_catalog_schema_incompatible")
	}
	revisionCompatible := strings.TrimSpace(wire.Revision) != ""
	if !revisionCompatible {
		warnings = append(warnings, "pricing_catalog_revision_missing")
	}
	formulaCompatible := strings.TrimSpace(wire.Pricing.FormulaID) == "landed_price_v1" && majorVersion(wire.Pricing.FormulaRevision) == "1"
	if !formulaCompatible {
		warnings = append(warnings, "pricing_formula_incompatible")
	}
	localIsIRT := strings.EqualFold(strings.TrimSpace(wire.Currency.Local), "IRT")
	if !localIsIRT {
		warnings = append(warnings, "pricing_local_currency_not_irt")
	}
	irtPerCNY := cloneDecimal(wire.Currency.CNYToIRT)
	if !validPositive(irtPerCNY) {
		warnings = append(warnings, "pricing_cny_to_irt_missing_or_invalid")
	}
	fxContractCompatible := true
	if validPositive(wire.Currency.CNYToLocal) && validPositive(irtPerCNY) && !decimalEqual(wire.Currency.CNYToLocal, irtPerCNY) {
		warnings = append(warnings, "pricing_fx_contract_conflict")
		fxContractCompatible = false
	}
	if !schemaCompatible || !revisionCompatible || !formulaCompatible || !localIsIRT || !validPositive(irtPerCNY) || !fxContractCompatible {
		irtPerCNY = nil
	}
	methods := make(map[string]Method, len(wire.Methods))
	for _, method := range wire.Methods {
		method.ID = strings.TrimSpace(method.ID)
		if method.ID != "" {
			methods[method.ID] = method
		}
	}
	return &catalogSnapshot{
		revision:              strings.TrimSpace(wire.Revision),
		currencyEffectiveDate: strings.TrimSpace(wire.Currency.EffectiveDate),
		selectedWarehouses:    normalizedStrings(wire.SelectedWarehouses),
		irtPerCNY:             irtPerCNY,
		methods:               methods,
		warnings:              normalizedStrings(warnings),
	}, nil
}

func majorVersion(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(value), "v"))
	if index := strings.IndexByte(value, '.'); index >= 0 {
		value = value[:index]
	}
	return value
}

func withinAge(now, fetchedAt time.Time, limit time.Duration) bool {
	if fetchedAt.IsZero() || limit <= 0 {
		return false
	}
	age := now.Sub(fetchedAt)
	return age >= 0 && age <= limit
}

func (p *httpProvider) fetchAssignment(ctx context.Context, code string) (assignmentSnapshot, error) {
	path := strings.ReplaceAll(p.config.AssignmentPath, "{code}", url.PathEscape(code))
	var wire assignmentWire
	if err := p.getJSON(ctx, path, &wire); err != nil {
		return assignmentSnapshot{}, err
	}
	return assignmentSnapshotFromWire(wire), nil
}

func assignmentSnapshotFromWire(wire assignmentWire) assignmentSnapshot {
	profit := wire.ProfitPercent
	if wire.Markup.ProfitPercent != nil {
		profit = wire.Markup.ProfitPercent
	}
	warnings := append([]string(nil), wire.PricingWarnings...)
	if strings.TrimSpace(wire.Markup.Warning) != "" {
		warnings = append(warnings, wire.Markup.Warning)
	}
	return assignmentSnapshot{
		assignment: Assignment{MethodID: strings.TrimSpace(wire.MethodID), ProfitPercent: cloneDecimal(profit)},
		warnings:   normalizedStrings(warnings),
	}
}

func (p *httpProvider) fetchAssignmentBatch(ctx context.Context, codes []string) ([]batchAssignmentResult, error) {
	var wire struct {
		Schema         string `json:"schema"`
		SchemaVersion  string `json:"schema_version"`
		RequestedCount int    `json:"requested_count"`
		ResolvedCount  int    `json:"resolved_count"`
		ErrorCount     int    `json:"error_count"`
		MaximumCodes   int    `json:"maximum_codes"`
		DefaultMarkup  struct {
			Schema        string   `json:"schema"`
			SchemaVersion string   `json:"schema_version"`
			Configured    bool     `json:"configured"`
			ProfitPercent *Decimal `json:"profit_percent"`
		} `json:"default_percentage_markup"`
		Results []struct {
			Code       string          `json:"code"`
			Status     string          `json:"status"`
			Assignment json.RawMessage `json:"assignment"`
			Error      struct {
				Code       string `json:"code"`
				HTTPStatus int    `json:"http_status"`
				Retryable  bool   `json:"retryable"`
			} `json:"error"`
		} `json:"results"`
	}
	if err := p.postJSON(ctx, p.config.BatchAssignmentPath, struct {
		Codes []string `json:"codes"`
	}{Codes: codes}, &wire); err != nil {
		return nil, err
	}
	if wire.Schema != "digitalogic.pricing-assignment-batch" || majorVersion(wire.SchemaVersion) != "1" {
		return nil, &contractError{reason: "incompatible batch schema"}
	}
	if wire.DefaultMarkup.Schema != "digitalogic.default-percentage-markup" || majorVersion(wire.DefaultMarkup.SchemaVersion) != "1" {
		return nil, &contractError{reason: "incompatible default-markup schema"}
	}
	if wire.DefaultMarkup.Configured && wire.DefaultMarkup.ProfitPercent == nil {
		return nil, &contractError{reason: "configured default markup is missing its decimal"}
	}
	if wire.RequestedCount != len(codes) || len(wire.Results) != len(codes) || wire.MaximumCodes < len(codes) || wire.MaximumCodes > maximumBatchSize {
		return nil, &contractError{reason: "batch cardinality does not match the request"}
	}
	if wire.ResolvedCount < 0 || wire.ErrorCount < 0 || wire.ResolvedCount+wire.ErrorCount != wire.RequestedCount {
		return nil, &contractError{reason: "batch result counts are inconsistent"}
	}

	resolvedCount := 0
	errorCount := 0
	results := make([]batchAssignmentResult, 0, len(codes))
	for index, result := range wire.Results {
		if result.Code != codes[index] {
			return nil, &contractError{reason: "batch result order or Code changed"}
		}
		switch result.Status {
		case "ok":
			resolvedCount++
			if len(result.Assignment) == 0 || bytes.Equal(bytes.TrimSpace(result.Assignment), []byte("null")) {
				return nil, &contractError{reason: "successful batch result omitted its assignment"}
			}
			var assignment assignmentWire
			if err := json.Unmarshal(result.Assignment, &assignment); err != nil {
				return nil, &contractError{reason: "successful batch assignment is malformed"}
			}
			if strings.TrimSpace(assignment.Code) != result.Code {
				return nil, &contractError{reason: "successful batch assignment Code mismatch"}
			}
			snapshot := assignmentSnapshotFromWire(assignment)
			results = append(results, batchAssignmentResult{code: result.Code, snapshot: &snapshot})
		case "error":
			errorCount++
			warning := "product_pricing_assignment_batch_result_failed"
			switch result.Error.Code {
			case "digitalogic_product_code_not_found":
				warning = "product_pricing_assignment_not_found"
			case "digitalogic_product_code_ambiguous":
				warning = "product_pricing_assignment_ambiguous"
			case "digitalogic_invalid_product_code":
				warning = "product_pricing_assignment_invalid"
			default:
				results = append(results, batchAssignmentResult{code: result.Code, warnings: []string{warning}})
				continue
			}
			snapshot := assignmentSnapshot{warnings: []string{warning}}
			results = append(results, batchAssignmentResult{code: result.Code, snapshot: &snapshot})
		default:
			return nil, &contractError{reason: "batch result status is invalid"}
		}
	}
	if resolvedCount != wire.ResolvedCount || errorCount != wire.ErrorCount {
		return nil, &contractError{reason: "batch status counts do not match the envelope"}
	}
	return results, nil
}

func (p *httpProvider) getJSON(ctx context.Context, path string, target interface{}) error {
	return p.doJSON(ctx, http.MethodGet, path, nil, target)
}

func (p *httpProvider) postJSON(ctx context.Context, path string, payload, target interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if int64(len(body)) > p.config.MaxResponseBytes {
		return errRequestTooLarge
	}
	return p.doJSON(ctx, http.MethodPost, path, body, target)
}

func (p *httpProvider) doJSON(ctx context.Context, method, path string, body []byte, target interface{}) error {
	endpoint, err := joinURL(p.config.BaseURL, path)
	if err != nil {
		return err
	}
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "patris-export")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.config.BearerTokenEnv != "" {
		token := strings.TrimSpace(os.Getenv(p.config.BearerTokenEnv))
		if token == "" {
			return errors.New("configured bearer credential is unavailable")
		}
		req.Header.Set("Authorization", "Bearer "+token)
	} else if p.config.UsernameEnv != "" || p.config.PasswordEnv != "" {
		username := os.Getenv(p.config.UsernameEnv)
		password := os.Getenv(p.config.PasswordEnv)
		if p.config.UsernameEnv == "" || p.config.PasswordEnv == "" || username == "" || password == "" {
			return errors.New("configured basic credential is unavailable")
		}
		req.SetBasicAuth(username, password)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return &remoteStatusError{status: resp.StatusCode}
	}
	responseReader := io.LimitReader(resp.Body, p.config.MaxResponseBytes+1)
	data, err := io.ReadAll(responseReader)
	if err != nil {
		return err
	}
	if int64(len(data)) > p.config.MaxResponseBytes {
		return errResponseTooLarge
	}
	var envelope struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && len(envelope.Data) > 0 {
		if envelope.Success != nil && !*envelope.Success {
			return &contractError{reason: "remote API reported failure"}
		}
		data = envelope.Data
	}
	if err := json.Unmarshal(data, target); err != nil {
		return &contractError{reason: "JSON decode failed"}
	}
	return nil
}

func isUnsupportedBatchEndpoint(err error) bool {
	var status *remoteStatusError
	return errors.As(err, &status) && (status.status == http.StatusNotFound || status.status == http.StatusMethodNotAllowed || status.status == http.StatusNotImplemented)
}

func batchFailureWarning(err error) string {
	var status *remoteStatusError
	var contract *contractError
	switch {
	case errors.As(err, &status) && (status.status == http.StatusUnauthorized || status.status == http.StatusForbidden):
		return "pricing_assignment_batch_auth_failed"
	case errors.As(err, &status):
		return "pricing_assignment_batch_http_failed"
	case errors.Is(err, errRequestTooLarge):
		return "pricing_assignment_batch_request_too_large"
	case errors.Is(err, errResponseTooLarge):
		return "pricing_assignment_batch_response_too_large"
	case errors.As(err, &contract):
		return "pricing_assignment_batch_contract_invalid"
	default:
		return "pricing_assignment_batch_transport_failed"
	}
}

func joinURL(base, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(base, "/") + "/")
	if err != nil {
		return "", err
	}
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) {
		return "", fmt.Errorf("integration path must be relative")
	}
	reference, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	if reference.IsAbs() || reference.Host != "" || reference.Scheme != "" || reference.User != nil {
		return "", fmt.Errorf("integration path must not change origin")
	}
	resolved := parsed.ResolveReference(reference)
	if !strings.EqualFold(resolved.Scheme, parsed.Scheme) || !strings.EqualFold(resolved.Host, parsed.Host) || resolved.User != nil {
		return "", fmt.Errorf("integration path escaped configured origin")
	}
	return resolved.String(), nil
}

func cloneCatalog(value *catalogSnapshot) *catalogSnapshot {
	if value == nil {
		return nil
	}
	copy := *value
	copy.selectedWarehouses = append([]string(nil), value.selectedWarehouses...)
	copy.irtPerCNY = cloneDecimal(value.irtPerCNY)
	copy.warnings = append([]string(nil), value.warnings...)
	copy.methods = make(map[string]Method, len(value.methods))
	for key, method := range value.methods {
		method.PricePerKgCNY = cloneDecimal(method.PricePerKgCNY)
		if method.Enabled != nil {
			enabled := *method.Enabled
			method.Enabled = &enabled
		}
		copy.methods[key] = method
	}
	return &copy
}

func decimalEqual(left, right *Decimal) bool {
	l, lok := decimalRat(left)
	r, rok := decimalRat(right)
	return lok && rok && l.Cmp(r) == 0
}
