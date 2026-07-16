package pricingcatalog

import (
	"container/list"
	"context"
	"encoding/json"
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

type httpProvider struct {
	config   DigitalogicConfig
	client   *http.Client
	now      func() time.Time
	freshFor time.Duration
	maxStale time.Duration

	mu             sync.Mutex
	catalogFetchMu sync.Mutex
	catalog        *catalogSnapshot
	assignments    map[string]*list.Element
	lru            *list.List
	configError    string
}

func newHTTPProvider(cfg DigitalogicConfig, client *http.Client, now func() time.Time) Provider {
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
	p.mu.Lock()
	if element, ok := p.assignments[code]; ok {
		p.lru.MoveToFront(element)
		cached := element.Value.(*assignmentEntry).snapshot
		if withinAge(now, cached.fetchedAt, p.freshFor) {
			p.mu.Unlock()
			copy := cached
			return &copy, "fresh", append([]string(nil), cached.warnings...)
		}
	}
	p.mu.Unlock()

	fetched, err := p.fetchAssignment(ctx, code)
	if err == nil {
		fetched.fetchedAt = now
		p.storeAssignment(code, fetched)
		copy := fetched
		return &copy, "fresh", append([]string(nil), fetched.warnings...)
	}

	p.mu.Lock()
	element := p.assignments[code]
	if element != nil {
		cached := element.Value.(*assignmentEntry).snapshot
		if withinAge(now, cached.fetchedAt, p.maxStale) {
			p.lru.MoveToFront(element)
			p.mu.Unlock()
			copy := cached
			return &copy, "stale", append(append([]string(nil), cached.warnings...), "product_pricing_assignment_fetch_failed")
		}
	}
	p.mu.Unlock()
	return nil, "unavailable", []string{"product_pricing_assignment_fetch_failed"}
}

func (p *httpProvider) storeAssignment(code string, snapshot assignmentSnapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
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
	var wire struct {
		MethodID        string   `json:"import_freight_method_id"`
		ProfitPercent   *Decimal `json:"profit_percent"`
		PricingWarnings []string `json:"pricing_warnings"`
		Markup          struct {
			ProfitPercent *Decimal `json:"profit_percent"`
			Warning       string   `json:"warning"`
		} `json:"markup"`
	}
	if err := p.getJSON(ctx, path, &wire); err != nil {
		return assignmentSnapshot{}, err
	}
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
	}, nil
}

func (p *httpProvider) getJSON(ctx context.Context, path string, target interface{}) error {
	endpoint, err := joinURL(p.config.BaseURL, path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "patris-export")
	if token := strings.TrimSpace(os.Getenv(p.config.BearerTokenEnv)); p.config.BearerTokenEnv != "" && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if p.config.UsernameEnv != "" || p.config.PasswordEnv != "" {
		req.SetBasicAuth(os.Getenv(p.config.UsernameEnv), os.Getenv(p.config.PasswordEnv))
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("HTTP status %d", resp.StatusCode)
	}
	reader := io.LimitReader(resp.Body, p.config.MaxResponseBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if int64(len(data)) > p.config.MaxResponseBytes {
		return fmt.Errorf("response exceeds configured limit")
	}
	var envelope struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && len(envelope.Data) > 0 {
		if envelope.Success != nil && !*envelope.Success {
			return fmt.Errorf("remote API reported failure")
		}
		data = envelope.Data
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("invalid JSON contract: %w", err)
	}
	return nil
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
