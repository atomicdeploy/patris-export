package pricingcatalog

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type catalogSnapshot struct {
	revision              string
	currencyEffectiveDate string
	selectedWarehouses    []string
	irtPerCNY             *Decimal
	roundingDigits        *int
	methods               map[string]Method
	explicitNulls         map[string]bool
	methodPriceNulls      map[string]bool
	methodCurrencyNulls   map[string]bool
	methodPairPresent     map[string]bool
	methodPairIncomplete  map[string]bool
	fetchedAt             time.Time
	warnings              []string
}

type assignmentSnapshot struct {
	assignment    Assignment
	profitSource  string
	warnings      []string
	explicitNulls map[string]bool
	fetchedAt     time.Time
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

type prefetchDiagnosticEntry struct {
	code       string
	diagnostic prefetchDiagnostic
}

// prefetchRun is a transform-scoped result barrier. It may temporarily hold
// more entries than the persistent LRU, but is bounded by the caller's unique
// Code slice and releases each outcome as that Code is resolved.
type prefetchRun struct {
	mu       sync.Mutex
	outcomes map[string]prefetchOutcome
}

type prefetchOutcome struct {
	snapshot   *assignmentSnapshot
	diagnostic *prefetchDiagnostic
}

type prefetchedProvider struct {
	parent *httpProvider
	run    *prefetchRun
}

type assignmentWire struct {
	Code            string   `json:"code"`
	MethodID        string   `json:"shipping_method_id"`
	ProfitPercent   *Decimal `json:"profit_percent"`
	ProfitSource    string   `json:"profit_percent_source"`
	PricingWarnings []string `json:"pricing_warnings"`
}

type batchAssignmentWire struct {
	Code            string        `json:"code"`
	MethodID        string        `json:"shipping_method_id"`
	ProfitPercent   *batchDecimal `json:"profit_percent"`
	ProfitSource    string        `json:"profit_percent_source"`
	PricingWarnings []string      `json:"pricing_warnings"`
}

type batchDecimal struct {
	value Decimal
}

type batchDefaultMarkup struct {
	schema     string
	configured bool
	typeName   string
	profit     *Decimal
	source     string
	revision   string
}

type batchAssignmentPage struct {
	defaultMarkup batchDefaultMarkup
	results       []batchAssignmentResult
}

type batchAssignmentResult struct {
	code        string
	snapshot    *assignmentSnapshot
	warnings    []string
	allowSingle bool
}

type batchAssignmentFetch struct {
	page      batchAssignmentPage
	err       error
	completed bool
}

type remoteStatusError struct {
	status int
}

// contextGate serializes remote cache fills without making a queued caller
// outlive its own cancellation or deadline. A sync.Mutex cannot be abandoned
// while Lock is waiting, which allowed one slow pricing projection to hold
// later canonical requests past their documented request ceiling.
type contextGate struct {
	ready chan struct{}
}

func newContextGate() *contextGate {
	gate := &contextGate{ready: make(chan struct{}, 1)}
	gate.ready <- struct{}{}
	return gate
}

func (gate *contextGate) lock(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-gate.ready:
		return nil
	}
}

func (gate *contextGate) unlock() {
	gate.ready <- struct{}{}
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
	errCredentialUnavailable = errors.New("configured credential is unavailable")
	errRequestTooLarge       = errors.New("request exceeds configured limit")
	errResponseTooLarge      = errors.New("response exceeds configured limit")
	errBatchPrefetchStopped  = errors.New("pricing assignment batch prefetch stopped before completion")
)

func (d *batchDecimal) UnmarshalJSON(data []byte) error {
	if d == nil {
		return errors.New("nil batch decimal receiver")
	}
	data = bytes.TrimSpace(data)
	if len(data) < 2 || data[0] != '"' {
		return errors.New("batch decimal must be a quoted canonical string")
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if strings.TrimSpace(value) != value {
		return errors.New("batch decimal must not contain surrounding whitespace")
	}
	parsed, err := NewDecimal(value)
	if err != nil {
		return err
	}
	if parsed.String() != value {
		return errors.New("batch decimal must use canonical base-10 text")
	}
	if point := strings.IndexByte(value, '.'); point >= 0 && len(value)-point-1 > 12 {
		return errors.New("batch decimal exceeds 12 fractional digits")
	}
	d.value = *parsed
	return nil
}

func (d *batchDecimal) decimal() *Decimal {
	if d == nil {
		return nil
	}
	value := d.value
	return &value
}

func (r *prefetchRun) take(code string) (prefetchOutcome, bool) {
	if r == nil {
		return prefetchOutcome{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	outcome, ok := r.outcomes[code]
	if ok {
		delete(r.outcomes, code)
	}
	return clonePrefetchOutcome(outcome), ok
}

type httpProvider struct {
	config   DigitalogicConfig
	client   *http.Client
	now      func() time.Time
	freshFor time.Duration
	maxStale time.Duration

	mu               sync.Mutex
	catalogFetchGate *contextGate
	prefetchGate     *contextGate
	catalog          *catalogSnapshot
	catalogFailure   time.Time
	batchFailure     *prefetchDiagnostic
	assignments      map[string]*list.Element
	diagnostics      map[string]*list.Element
	lru              *list.List
	diagnosticLRU    *list.List
	configError      string
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
		config:           normalized,
		client:           client,
		now:              now,
		freshFor:         parseDuration(normalized.FreshFor, defaultFreshFor),
		maxStale:         parseDuration(normalized.MaxStale, defaultMaxStale),
		catalogFetchGate: newContextGate(),
		prefetchGate:     newContextGate(),
		assignments:      make(map[string]*list.Element),
		diagnostics:      make(map[string]*list.Element),
		lru:              list.New(),
		diagnosticLRU:    list.New(),
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

// Prefetch stages uncached assignments in bounded request-order chunks and
// returns a transform-scoped result barrier. Pages become visible only after
// their shared default-markup contract agrees. Fresh failure backoff blocks an
// unsafe repeated request path for the current freshness window.
func (p *httpProvider) Prefetch(ctx context.Context, codes []string) Provider {
	if p.configError != "" || len(codes) == 0 {
		return p
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := p.prefetchGate.lock(ctx); err != nil {
		return p
	}
	defer p.prefetchGate.unlock()
	if err := ctx.Err(); err != nil {
		return p
	}
	if catalog, _, _ := p.resolveCatalog(ctx); catalog == nil {
		return p
	}

	now := p.now().UTC()
	seen := make(map[string]struct{}, len(codes))
	pending := make([]string, 0, len(codes))
	outcomes := make(map[string]prefetchOutcome, len(codes))
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
		if diagnostic, ok := p.freshPrefetchDiagnosticLocked(code, now); ok {
			outcomes[code] = prefetchOutcome{diagnostic: &diagnostic}
			continue
		}
		if element := p.assignments[code]; element != nil {
			cached := element.Value.(*assignmentEntry).snapshot
			if withinAge(now, cached.fetchedAt, p.freshFor) {
				p.lru.MoveToFront(element)
				copy := cloneAssignmentSnapshot(cached)
				outcomes[code] = prefetchOutcome{snapshot: &copy}
				continue
			}
		}
		pending = append(pending, code)
	}
	p.mu.Unlock()

	pendingOutcomes := make(map[string]prefetchOutcome, len(pending))
	var expectedDefault *batchDefaultMarkup
	pages, batchErr := p.fetchAssignmentPages(ctx, pending)
	if batchErr == nil {
		// Pages may complete out of order, but contract agreement and result
		// materialization always follow original request-page order.
		for _, page := range pages {
			if expectedDefault == nil {
				copy := cloneBatchDefaultMarkup(page.defaultMarkup)
				expectedDefault = &copy
			} else if !equalBatchDefaultMarkup(*expectedDefault, page.defaultMarkup) {
				batchErr = &contractError{reason: "default-markup contract changed between batch chunks"}
				break
			}
			for _, result := range page.results {
				if result.snapshot != nil {
					snapshot := cloneAssignmentSnapshot(*result.snapshot)
					snapshot.fetchedAt = now
					pendingOutcomes[result.code] = prefetchOutcome{snapshot: &snapshot}
					continue
				}
				diagnostic := prefetchDiagnostic{
					warnings: normalizedStrings(result.warnings), fetchedAt: now, allowSingle: result.allowSingle,
				}
				pendingOutcomes[result.code] = prefetchOutcome{diagnostic: &diagnostic}
			}
		}
	}

	var batchBackoff *prefetchDiagnostic
	if batchErr != nil {
		pendingOutcomes = make(map[string]prefetchOutcome, len(pending))
		warning := batchFailureWarning(batchErr)
		backoff := prefetchDiagnostic{warnings: []string{warning}, fetchedAt: now}
		batchBackoff = &backoff
		for _, code := range pending {
			diagnostic := clonePrefetchDiagnostic(backoff)
			pendingOutcomes[code] = prefetchOutcome{diagnostic: &diagnostic}
		}
	}

	p.commitPrefetchOutcomes(pendingOutcomes, batchBackoff, batchErr == nil && len(pending) > 0)
	for code, outcome := range pendingOutcomes {
		outcomes[code] = clonePrefetchOutcome(outcome)
	}
	if len(outcomes) == 0 {
		return p
	}
	return &prefetchedProvider{parent: p, run: &prefetchRun{outcomes: outcomes}}
}

// fetchAssignmentPages bounds concurrent page requests independently from
// per-product materialization workers. A terminal page failure cancels sibling
// requests, while the caller's context remains the authoritative cancellation
// signal returned to the canonical transform.
func (p *httpProvider) fetchAssignmentPages(ctx context.Context, codes []string) ([]batchAssignmentPage, error) {
	if len(codes) == 0 {
		return nil, nil
	}
	pageCount := (len(codes) + p.config.BatchSize - 1) / p.config.BatchSize
	fetches := make([]batchAssignmentFetch, pageCount)
	if pageCount == 1 {
		page, err := p.fetchAssignmentBatch(ctx, codes)
		if err != nil {
			return nil, err
		}
		return []batchAssignmentPage{page}, nil
	}

	workers := p.config.BatchConcurrency
	if workers > pageCount {
		workers = pageCount
	}
	if workers < 1 {
		workers = 1
	}

	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int, pageCount)
	for index := 0; index < pageCount; index++ {
		jobs <- index
	}
	close(jobs)

	var wait sync.WaitGroup
	var failureOnce sync.Once
	var firstFailure error
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wait.Done()
			for {
				select {
				case <-batchCtx.Done():
					return
				case index, ok := <-jobs:
					if !ok {
						return
					}
					offset := index * p.config.BatchSize
					end := offset + p.config.BatchSize
					if end > len(codes) {
						end = len(codes)
					}
					page, err := p.fetchAssignmentBatch(batchCtx, codes[offset:end])
					fetches[index] = batchAssignmentFetch{page: page, err: err, completed: true}
					if err != nil {
						failureOnce.Do(func() {
							firstFailure = err
							cancel()
						})
					}
				}
			}
		}()
	}
	wait.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Prefer the lowest request-page terminal error when multiple pages finish
	// together. Sibling context-cancellation errors never mask that cause.
	for _, fetch := range fetches {
		if fetch.completed && fetch.err != nil &&
			!errors.Is(fetch.err, context.Canceled) &&
			!errors.Is(fetch.err, context.DeadlineExceeded) {
			return nil, fetch.err
		}
	}
	if firstFailure != nil {
		return nil, firstFailure
	}

	pages := make([]batchAssignmentPage, pageCount)
	for index, fetch := range fetches {
		if !fetch.completed {
			return nil, errBatchPrefetchStopped
		}
		pages[index] = fetch.page
	}
	return pages, nil
}

func (p *httpProvider) Resolve(ctx context.Context, code string) Resolution {
	return p.resolve(ctx, code, nil)
}

func (p *prefetchedProvider) Resolve(ctx context.Context, code string) Resolution {
	return p.parent.resolve(ctx, code, p.run)
}

func (p *httpProvider) resolve(ctx context.Context, code string, run *prefetchRun) Resolution {
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
	if catalog.roundingDigits != nil {
		digits := *catalog.roundingDigits
		resolution.RoundingDigits = &digits
	}
	resolution.ExplicitNulls = cloneBoolMap(catalog.explicitNulls)
	resolution.Warnings = append(resolution.Warnings, catalog.warnings...)

	assignment, assignmentStatus, assignmentWarnings := p.resolveAssignment(ctx, code, run)
	resolution.Warnings = append(resolution.Warnings, assignmentWarnings...)
	if assignmentStatus == "stale" {
		resolution.Warnings = append(resolution.Warnings, "product_pricing_assignment_stale")
	}
	if assignment != nil {
		for field, isNull := range assignment.explicitNulls {
			if isNull {
				resolution.ExplicitNulls[field] = true
			}
		}
	}
	if assignment == nil {
		resolution.Warnings = append(resolution.Warnings, "pricing_assignment_missing", "shipping_method_missing")
		return finishResolution(resolution)
	}
	resolution.MarkupPercent = cloneDecimal(assignment.assignment.ProfitPercent)
	resolution.MarkupPercentSource = assignment.profitSource
	if strings.TrimSpace(assignment.assignment.MethodID) == "" {
		resolution.Warnings = append(resolution.Warnings, "shipping_method_missing")
		return finishResolution(resolution)
	}

	resolution.MethodID = strings.TrimSpace(assignment.assignment.MethodID)
	method, exists := catalog.methods[resolution.MethodID]
	if !exists {
		resolution.Warnings = append(resolution.Warnings, "shipping_method_unknown")
	} else {
		pricePresent, currencyPresent := method.shippingPairPresence()
		if pricePresent {
			resolution.ShippingPricePerKg = cloneDecimal(method.PricePerKg)
		}
		if currencyPresent {
			resolution.ShippingPricePerKgCurrency = method.Currency
		}
		resolution.ShippingPricePairPresent = catalog.methodPairPresent[resolution.MethodID]
		if catalog.methodPairIncomplete[resolution.MethodID] {
			resolution.Warnings = append(resolution.Warnings, "shipping_price_per_kg_pair_incomplete")
		}
		if catalog.methodPriceNulls[resolution.MethodID] {
			resolution.ExplicitNulls["shipping_price_per_kg"] = true
		}
		if catalog.methodCurrencyNulls[resolution.MethodID] {
			resolution.ExplicitNulls["shipping_price_per_kg_currency"] = true
		}
		if method.Enabled != nil && !*method.Enabled {
			resolution.Warnings = append(resolution.Warnings, "shipping_method_disabled")
		}
	}
	return finishResolution(resolution)
}

func (p *httpProvider) resolveCatalog(ctx context.Context) (*catalogSnapshot, string, []string) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := p.now().UTC()
	p.mu.Lock()
	cached := p.catalog
	if cached != nil && withinAge(now, cached.fetchedAt, p.freshFor) {
		copy := cloneCatalog(cached)
		p.mu.Unlock()
		return copy, "fresh", nil
	}
	if withinAge(now, p.catalogFailure, p.freshFor) {
		if cached != nil && withinAge(now, cached.fetchedAt, p.maxStale) {
			copy := cloneCatalog(cached)
			p.mu.Unlock()
			return copy, "stale", []string{"pricing_catalog_stale"}
		}
		p.mu.Unlock()
		return nil, "unavailable", []string{"pricing_catalog_fetch_failed"}
	}
	p.mu.Unlock()
	if err := p.catalogFetchGate.lock(ctx); err != nil {
		return nil, "unavailable", []string{"pricing_catalog_fetch_failed"}
	}
	defer p.catalogFetchGate.unlock()
	if err := ctx.Err(); err != nil {
		return nil, "unavailable", []string{"pricing_catalog_fetch_failed"}
	}

	// Another resolver may have refreshed the catalog while this caller waited.
	now = p.now().UTC()
	p.mu.Lock()
	cached = p.catalog
	if cached != nil && withinAge(now, cached.fetchedAt, p.freshFor) {
		copy := cloneCatalog(cached)
		p.mu.Unlock()
		return copy, "fresh", nil
	}
	if withinAge(now, p.catalogFailure, p.freshFor) {
		if cached != nil && withinAge(now, cached.fetchedAt, p.maxStale) {
			copy := cloneCatalog(cached)
			p.mu.Unlock()
			return copy, "stale", []string{"pricing_catalog_stale"}
		}
		p.mu.Unlock()
		return nil, "unavailable", []string{"pricing_catalog_fetch_failed"}
	}
	p.mu.Unlock()

	fetched, err := p.fetchCatalog(ctx)
	if err == nil {
		fetched.fetchedAt = now
		p.mu.Lock()
		p.catalog = cloneCatalog(fetched)
		p.catalogFailure = time.Time{}
		p.mu.Unlock()
		return fetched, "fresh", nil
	}

	p.mu.Lock()
	p.catalogFailure = now
	cached = p.catalog
	p.mu.Unlock()
	if cached != nil && withinAge(now, cached.fetchedAt, p.maxStale) {
		return cloneCatalog(cached), "stale", []string{"pricing_catalog_stale"}
	}
	return nil, "unavailable", []string{"pricing_catalog_fetch_failed"}
}

func (p *httpProvider) resolveAssignment(ctx context.Context, code string, run *prefetchRun) (*assignmentSnapshot, string, []string) {
	now := p.now().UTC()
	prefetchWarnings := []string(nil)
	skipPersistentDiagnostic := false
	if outcome, ok := run.take(code); ok {
		if outcome.snapshot != nil {
			copy := cloneAssignmentSnapshot(*outcome.snapshot)
			return &copy, "fresh", append([]string(nil), copy.warnings...)
		}
		if outcome.diagnostic != nil {
			diagnostic := clonePrefetchDiagnostic(*outcome.diagnostic)
			prefetchWarnings = append(prefetchWarnings, diagnostic.warnings...)
			skipPersistentDiagnostic = true
			if !diagnostic.allowSingle {
				p.mu.Lock()
				if element := p.assignments[code]; element != nil {
					cached := element.Value.(*assignmentEntry).snapshot
					if withinAge(now, cached.fetchedAt, p.maxStale) {
						p.lru.MoveToFront(element)
						p.mu.Unlock()
						copy := cloneAssignmentSnapshot(cached)
						warnings := append(append([]string(nil), cached.warnings...), prefetchWarnings...)
						return &copy, "stale", normalizedStrings(warnings)
					}
				}
				p.mu.Unlock()
				return nil, "unavailable", normalizedStrings(prefetchWarnings)
			}
		}
	}
	p.mu.Lock()
	if !skipPersistentDiagnostic {
		if diagnostic, ok := p.freshPrefetchDiagnosticLocked(code, now); ok {
			prefetchWarnings = append(prefetchWarnings, diagnostic.warnings...)
			if !diagnostic.allowSingle {
				if element := p.assignments[code]; element != nil {
					cached := element.Value.(*assignmentEntry).snapshot
					if withinAge(now, cached.fetchedAt, p.maxStale) {
						p.lru.MoveToFront(element)
						p.mu.Unlock()
						copy := cloneAssignmentSnapshot(cached)
						warnings := append(append([]string(nil), cached.warnings...), prefetchWarnings...)
						return &copy, "stale", normalizedStrings(warnings)
					}
				}
				p.mu.Unlock()
				return nil, "unavailable", normalizedStrings(prefetchWarnings)
			}
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
	p.storeAssignmentLocked(code, snapshot)
}

func (p *httpProvider) storeAssignmentLocked(code string, snapshot assignmentSnapshot) {
	p.removeDiagnosticLocked(code)
	if element, exists := p.assignments[code]; exists {
		element.Value.(*assignmentEntry).snapshot = cloneAssignmentSnapshot(snapshot)
		p.lru.MoveToFront(element)
		return
	}
	element := p.lru.PushFront(&assignmentEntry{code: code, snapshot: cloneAssignmentSnapshot(snapshot)})
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
		diagnostic := prefetchDiagnostic{
			warnings:    append([]string(nil), warnings...),
			fetchedAt:   fetchedAt,
			allowSingle: allowSingle,
		}
		p.storePrefetchDiagnosticLocked(code, diagnostic)
	}
}

func (p *httpProvider) storePrefetchDiagnosticLocked(code string, diagnostic prefetchDiagnostic) {
	diagnostic = clonePrefetchDiagnostic(diagnostic)
	if element := p.diagnostics[code]; element != nil {
		element.Value.(*prefetchDiagnosticEntry).diagnostic = diagnostic
		p.diagnosticLRU.MoveToFront(element)
		return
	}
	element := p.diagnosticLRU.PushFront(&prefetchDiagnosticEntry{code: code, diagnostic: diagnostic})
	p.diagnostics[code] = element
	for p.diagnosticLRU.Len() > p.config.MaxEntries {
		oldest := p.diagnosticLRU.Back()
		if oldest == nil {
			break
		}
		delete(p.diagnostics, oldest.Value.(*prefetchDiagnosticEntry).code)
		p.diagnosticLRU.Remove(oldest)
	}
}

func (p *httpProvider) commitPrefetchOutcomes(outcomes map[string]prefetchOutcome, batchFailure *prefetchDiagnostic, clearBatchFailure bool) {
	if len(outcomes) == 0 && batchFailure == nil && !clearBatchFailure {
		return
	}
	keys := make([]string, 0, len(outcomes))
	for code := range outcomes {
		keys = append(keys, code)
	}
	sort.Strings(keys)
	p.mu.Lock()
	defer p.mu.Unlock()
	if clearBatchFailure {
		p.batchFailure = nil
	}
	if batchFailure != nil {
		copy := clonePrefetchDiagnostic(*batchFailure)
		p.batchFailure = &copy
	}
	for _, code := range keys {
		outcome := outcomes[code]
		if outcome.snapshot != nil {
			p.storeAssignmentLocked(code, *outcome.snapshot)
			continue
		}
		if outcome.diagnostic != nil {
			p.storePrefetchDiagnosticLocked(code, *outcome.diagnostic)
		}
	}
}

func (p *httpProvider) removeDiagnosticLocked(code string) {
	element := p.diagnostics[code]
	if element == nil {
		return
	}
	delete(p.diagnostics, code)
	p.diagnosticLRU.Remove(element)
}

func (p *httpProvider) freshPrefetchDiagnosticLocked(code string, now time.Time) (prefetchDiagnostic, bool) {
	if p.batchFailure != nil {
		if withinAge(now, p.batchFailure.fetchedAt, p.freshFor) {
			return clonePrefetchDiagnostic(*p.batchFailure), true
		}
		p.batchFailure = nil
	}
	if element := p.diagnostics[code]; element != nil {
		diagnostic := element.Value.(*prefetchDiagnosticEntry).diagnostic
		if withinAge(now, diagnostic.fetchedAt, p.freshFor) {
			p.diagnosticLRU.MoveToFront(element)
			return clonePrefetchDiagnostic(diagnostic), true
		}
		p.removeDiagnosticLocked(code)
	}
	return prefetchDiagnostic{}, false
}

func cloneAssignmentSnapshot(value assignmentSnapshot) assignmentSnapshot {
	value.assignment.ProfitPercent = cloneDecimal(value.assignment.ProfitPercent)
	value.warnings = append([]string(nil), value.warnings...)
	value.explicitNulls = cloneBoolMap(value.explicitNulls)
	return value
}

func clonePrefetchDiagnostic(value prefetchDiagnostic) prefetchDiagnostic {
	value.warnings = append([]string(nil), value.warnings...)
	return value
}

func clonePrefetchOutcome(value prefetchOutcome) prefetchOutcome {
	copy := prefetchOutcome{}
	if value.snapshot != nil {
		snapshot := cloneAssignmentSnapshot(*value.snapshot)
		copy.snapshot = &snapshot
	}
	if value.diagnostic != nil {
		diagnostic := clonePrefetchDiagnostic(*value.diagnostic)
		copy.diagnostic = &diagnostic
	}
	return copy
}

func cloneBatchDefaultMarkup(value batchDefaultMarkup) batchDefaultMarkup {
	value.profit = cloneDecimal(value.profit)
	return value
}

func equalBatchDefaultMarkup(left, right batchDefaultMarkup) bool {
	if left.schema != right.schema || left.configured != right.configured || left.typeName != right.typeName || left.source != right.source || left.revision != right.revision {
		return false
	}
	if left.profit == nil || right.profit == nil {
		return left.profit == nil && right.profit == nil
	}
	return left.profit.String() == right.profit.String()
}

func (p *httpProvider) fetchCatalog(ctx context.Context) (*catalogSnapshot, error) {
	var wire struct {
		Schema             string   `json:"schema"`
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
			FormulaID      string `json:"formula_id"`
			RoundingDigits *int   `json:"rounding_digits"`
			RoundingMode   string `json:"rounding_mode"`
		} `json:"pricing"`
		Methods []Method `json:"shipping_methods"`
	}
	raw, err := p.getJSON(ctx, p.config.CatalogPath, &wire)
	if err != nil {
		return nil, err
	}
	explicitNulls := explicitJSONNulls(raw, "revision")
	if explicitNulls["revision"] {
		delete(explicitNulls, "revision")
		explicitNulls["pricing_catalog_revision"] = true
	}
	var rawCatalog struct {
		Currency json.RawMessage   `json:"currency"`
		Pricing  json.RawMessage   `json:"pricing"`
		Methods  []json.RawMessage `json:"shipping_methods"`
	}
	_ = json.Unmarshal(raw, &rawCatalog)
	for field, isNull := range explicitJSONNulls(rawCatalog.Currency, "cny_to_irt", "effective_date") {
		if isNull {
			switch field {
			case "cny_to_irt":
				explicitNulls["irt_per_cny"] = true
			case "effective_date":
				explicitNulls["currency_effective_date"] = true
			}
		}
	}
	pricingNulls := explicitJSONNulls(rawCatalog.Pricing, "rounding_digits")
	if pricingNulls["rounding_digits"] {
		explicitNulls["price_rounding_digits"] = true
	}
	warnings := append([]string(nil), wire.Currency.Warnings...)
	schemaCompatible := wire.Schema == "digitalogic.integration-catalog"
	if !schemaCompatible {
		warnings = append(warnings, "pricing_catalog_schema_incompatible")
	}
	revisionCompatible := strings.TrimSpace(wire.Revision) != ""
	if !revisionCompatible {
		warnings = append(warnings, "pricing_catalog_revision_missing")
	}
	formulaCompatible := strings.TrimSpace(wire.Pricing.FormulaID) == "landed_price"
	if !formulaCompatible {
		warnings = append(warnings, "pricing_formula_incompatible")
	}
	roundingMode := strings.TrimSpace(wire.Pricing.RoundingMode)
	roundingModeCompatible := roundingMode == "" || roundingMode == RoundingModeHalfUp
	if !roundingModeCompatible {
		warnings = append(warnings, "price_rounding_mode_invalid")
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
	baseContractCompatible := schemaCompatible && revisionCompatible && formulaCompatible && localIsIRT
	if !baseContractCompatible || !validPositive(irtPerCNY) || !fxContractCompatible {
		irtPerCNY = nil
	}
	var roundingDigits *int
	if pricingNulls["rounding_digits"] {
		warnings = append(warnings, "price_rounding_digits_explicit_null")
	} else if !baseContractCompatible || !roundingModeCompatible {
		// Withhold shared calculation provenance when the catalog document
		// itself is incompatible. The IRR partner path remains independent of
		// the optional CNY FX input but not of the catalog contract.
	} else if wire.Pricing.RoundingDigits == nil {
		digits := MinimumRoundDigits
		roundingDigits = &digits
	} else if *wire.Pricing.RoundingDigits < MinimumRoundDigits || *wire.Pricing.RoundingDigits > MaximumRoundDigits {
		warnings = append(warnings, "price_rounding_digits_invalid")
	} else {
		digits := *wire.Pricing.RoundingDigits
		roundingDigits = &digits
	}
	methods := make(map[string]Method, len(wire.Methods))
	methodPriceNulls := make(map[string]bool)
	methodCurrencyNulls := make(map[string]bool)
	methodPairPresent := make(map[string]bool)
	methodPairIncomplete := make(map[string]bool)
	for index, method := range wire.Methods {
		method.ID = strings.TrimSpace(method.ID)
		method.Currency = normalizeShippingCurrency(method.Currency)
		if method.ID != "" {
			methods[method.ID] = method
			if index < len(rawCatalog.Methods) {
				var rawMethod map[string]json.RawMessage
				_ = json.Unmarshal(rawCatalog.Methods[index], &rawMethod)
				_, pricePresent := rawMethod["price_per_kg"]
				_, currencyPresent := rawMethod["currency"]
				methodPairPresent[method.ID] = pricePresent && currencyPresent
				methodPairIncomplete[method.ID] = pricePresent != currencyPresent
				nulls := explicitJSONNulls(rawCatalog.Methods[index], "price_per_kg", "currency")
				methodPriceNulls[method.ID] = nulls["price_per_kg"]
				methodCurrencyNulls[method.ID] = nulls["currency"]
			}
		}
	}
	return &catalogSnapshot{
		revision:              strings.TrimSpace(wire.Revision),
		currencyEffectiveDate: strings.TrimSpace(wire.Currency.EffectiveDate),
		selectedWarehouses:    normalizedStrings(wire.SelectedWarehouses),
		irtPerCNY:             irtPerCNY,
		roundingDigits:        roundingDigits,
		methods:               methods,
		explicitNulls:         explicitNulls,
		methodPriceNulls:      methodPriceNulls,
		methodCurrencyNulls:   methodCurrencyNulls,
		methodPairPresent:     methodPairPresent,
		methodPairIncomplete:  methodPairIncomplete,
		warnings:              normalizedStrings(warnings),
	}, nil
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
	raw, err := p.getJSON(ctx, path, &wire)
	if err != nil {
		return assignmentSnapshot{}, err
	}
	return assignmentSnapshotFromWire(wire, raw), nil
}

func assignmentSnapshotFromWire(wire assignmentWire, raw json.RawMessage) assignmentSnapshot {
	profit := wire.ProfitPercent
	warnings := append([]string(nil), wire.PricingWarnings...)
	return assignmentSnapshot{
		assignment:    Assignment{MethodID: strings.TrimSpace(wire.MethodID), ProfitPercent: cloneDecimal(profit)},
		profitSource:  strings.TrimSpace(wire.ProfitSource),
		warnings:      normalizedStrings(warnings),
		explicitNulls: mapAssignmentNulls(explicitJSONNulls(raw, "shipping_method_id", "profit_percent")),
	}
}

func explicitJSONNulls(raw json.RawMessage, fields ...string) map[string]bool {
	result := make(map[string]bool)
	if len(raw) == 0 {
		return result
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return result
	}
	for _, field := range fields {
		if value, exists := object[field]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			result[field] = true
		}
	}
	return result
}

func mapAssignmentNulls(values map[string]bool) map[string]bool {
	result := make(map[string]bool)
	if values["shipping_method_id"] {
		result["shipping_method_id"] = true
	}
	if values["profit_percent"] {
		result["markup_percent"] = true
	}
	return result
}

func (p *httpProvider) fetchAssignmentBatch(ctx context.Context, codes []string) (batchAssignmentPage, error) {
	var wire struct {
		Schema         string `json:"schema"`
		RequestedCount int    `json:"requested_count"`
		ResolvedCount  int    `json:"resolved_count"`
		ErrorCount     int    `json:"error_count"`
		MaximumCodes   int    `json:"maximum_codes"`
		DefaultMarkup  struct {
			Schema        string        `json:"schema"`
			Configured    bool          `json:"configured"`
			Type          string        `json:"type"`
			ProfitPercent *batchDecimal `json:"profit_percent"`
			Source        string        `json:"source"`
			Revision      string        `json:"revision"`
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
	if _, err := p.postJSON(ctx, p.config.BatchAssignmentPath, struct {
		Codes []string `json:"codes"`
	}{Codes: codes}, &wire); err != nil {
		return batchAssignmentPage{}, err
	}
	if wire.Schema != "digitalogic.pricing-assignment-batch" {
		return batchAssignmentPage{}, &contractError{reason: "incompatible batch schema"}
	}
	if wire.DefaultMarkup.Schema != "digitalogic.default-percentage-markup" {
		return batchAssignmentPage{}, &contractError{reason: "incompatible default-markup schema"}
	}
	defaultMarkup := batchDefaultMarkup{
		schema:     wire.DefaultMarkup.Schema,
		configured: wire.DefaultMarkup.Configured,
		typeName:   wire.DefaultMarkup.Type,
		profit:     wire.DefaultMarkup.ProfitPercent.decimal(),
		source:     wire.DefaultMarkup.Source,
		revision:   wire.DefaultMarkup.Revision,
	}
	if err := validateBatchDefaultMarkup(defaultMarkup); err != nil {
		return batchAssignmentPage{}, err
	}
	if wire.RequestedCount != len(codes) || len(wire.Results) != len(codes) || wire.MaximumCodes < len(codes) || wire.MaximumCodes > maximumBatchSize {
		return batchAssignmentPage{}, &contractError{reason: "batch cardinality does not match the request"}
	}
	if wire.ResolvedCount < 0 || wire.ErrorCount < 0 || wire.ResolvedCount+wire.ErrorCount != wire.RequestedCount {
		return batchAssignmentPage{}, &contractError{reason: "batch result counts are inconsistent"}
	}

	resolvedCount := 0
	errorCount := 0
	results := make([]batchAssignmentResult, 0, len(codes))
	for index, result := range wire.Results {
		if result.Code != codes[index] {
			return batchAssignmentPage{}, &contractError{reason: "batch result order or Code changed"}
		}
		switch result.Status {
		case "ok":
			resolvedCount++
			if len(result.Assignment) == 0 || bytes.Equal(bytes.TrimSpace(result.Assignment), []byte("null")) {
				return batchAssignmentPage{}, &contractError{reason: "successful batch result omitted its assignment"}
			}
			var assignment batchAssignmentWire
			if err := decodeStrictJSON(result.Assignment, &assignment); err != nil {
				return batchAssignmentPage{}, &contractError{reason: "successful batch assignment is malformed"}
			}
			if assignment.Code != result.Code {
				return batchAssignmentPage{}, &contractError{reason: "successful batch assignment Code mismatch"}
			}
			if err := validateBatchAssignmentMarkup(assignment, defaultMarkup); err != nil {
				return batchAssignmentPage{}, err
			}
			profit := assignment.ProfitPercent.decimal()
			snapshot := assignmentSnapshot{
				assignment: Assignment{
					MethodID:      strings.TrimSpace(assignment.MethodID),
					ProfitPercent: cloneDecimal(profit),
				},
				profitSource:  strings.TrimSpace(assignment.ProfitSource),
				warnings:      normalizedStrings(assignment.PricingWarnings),
				explicitNulls: mapAssignmentNulls(explicitJSONNulls(result.Assignment, "shipping_method_id", "profit_percent")),
			}
			results = append(results, batchAssignmentResult{code: result.Code, snapshot: &snapshot})
		case "error":
			errorCount++
			if len(result.Assignment) > 0 && !bytes.Equal(bytes.TrimSpace(result.Assignment), []byte("null")) {
				return batchAssignmentPage{}, &contractError{reason: "failed batch result included an assignment"}
			}
			result.Error.Code = strings.TrimSpace(result.Error.Code)
			if result.Error.Code == "" || result.Error.HTTPStatus < 100 || result.Error.HTTPStatus > 599 {
				return batchAssignmentPage{}, &contractError{reason: "failed batch result omitted its typed error"}
			}
			warning := "product_pricing_assignment_batch_result_failed"
			switch result.Error.Code {
			case "digitalogic_product_code_not_found":
				if result.Error.Retryable || result.Error.HTTPStatus != http.StatusNotFound {
					return batchAssignmentPage{}, &contractError{reason: "not-found batch result changed its error semantics"}
				}
				warning = "product_pricing_assignment_not_found"
			case "digitalogic_product_code_ambiguous":
				if result.Error.Retryable || result.Error.HTTPStatus != http.StatusConflict {
					return batchAssignmentPage{}, &contractError{reason: "ambiguous batch result changed its error semantics"}
				}
				warning = "product_pricing_assignment_ambiguous"
			case "digitalogic_invalid_product_code":
				if result.Error.Retryable || result.Error.HTTPStatus != http.StatusBadRequest {
					return batchAssignmentPage{}, &contractError{reason: "invalid-Code batch result changed its error semantics"}
				}
				warning = "product_pricing_assignment_invalid"
			default:
				if result.Error.Retryable {
					if result.Error.HTTPStatus < 500 {
						return batchAssignmentPage{}, &contractError{reason: "retryable batch result used a non-server error status"}
					}
					warning = "product_pricing_assignment_batch_result_retryable"
				}
				results = append(results, batchAssignmentResult{
					code: result.Code, warnings: []string{warning}, allowSingle: result.Error.Retryable,
				})
				continue
			}
			snapshot := assignmentSnapshot{warnings: []string{warning}}
			results = append(results, batchAssignmentResult{code: result.Code, snapshot: &snapshot})
		default:
			return batchAssignmentPage{}, &contractError{reason: "batch result status is invalid"}
		}
	}
	if resolvedCount != wire.ResolvedCount || errorCount != wire.ErrorCount {
		return batchAssignmentPage{}, &contractError{reason: "batch status counts do not match the envelope"}
	}
	return batchAssignmentPage{defaultMarkup: defaultMarkup, results: results}, nil
}

func validateBatchDefaultMarkup(markup batchDefaultMarkup) error {
	if markup.typeName != strings.TrimSpace(markup.typeName) || markup.source != strings.TrimSpace(markup.source) || markup.revision != strings.TrimSpace(markup.revision) {
		return &contractError{reason: "default markup fields are not canonical strings"}
	}
	if markup.typeName != "percentage" || markup.revision == "" {
		return &contractError{reason: "default markup omitted its type or revision"}
	}
	if markup.configured {
		if markup.source != "global_default" || !validBatchPercentage(markup.profit) {
			return &contractError{reason: "configured default markup has inconsistent decimal/source semantics"}
		}
		return nil
	}
	if markup.profit != nil || (markup.source != "unset" && markup.source != "invalid_storage") {
		return &contractError{reason: "unconfigured default markup has inconsistent decimal/source semantics"}
	}
	return nil
}

func validateBatchAssignmentMarkup(assignment batchAssignmentWire, defaultMarkup batchDefaultMarkup) error {
	source := strings.TrimSpace(assignment.ProfitSource)
	if source != assignment.ProfitSource {
		return &contractError{reason: "assignment markup source is not canonical"}
	}
	warnings := normalizedStrings(assignment.PricingWarnings)
	profit := assignment.ProfitPercent.decimal()
	switch source {
	case "global_default":
		if !defaultMarkup.configured || !validBatchPercentage(profit) || !decimalEqual(profit, defaultMarkup.profit) {
			return &contractError{reason: "global-default assignment does not match the shared default decimal"}
		}
	case "product_override":
		if !validBatchPercentage(profit) {
			return &contractError{reason: "product override has an invalid exact percentage"}
		}
	case "unset":
		if profit != nil || len(warnings) == 0 {
			return &contractError{reason: "unset assignment omitted its diagnostic or included a decimal"}
		}
	case "":
		if profit != nil || len(warnings) == 0 {
			return &contractError{reason: "invalid assignment markup omitted its diagnostic or included a decimal"}
		}
	default:
		return &contractError{reason: "assignment markup source is unsupported"}
	}
	return nil
}

func validBatchPercentage(value *Decimal) bool {
	parsed, ok := decimalRat(value)
	return ok && parsed.Sign() >= 0 && parsed.Cmp(big.NewRat(1000, 1)) <= 0
}

func (p *httpProvider) getJSON(ctx context.Context, path string, target interface{}) (json.RawMessage, error) {
	return p.doJSON(ctx, http.MethodGet, path, nil, target)
}

func (p *httpProvider) postJSON(ctx context.Context, path string, payload, target interface{}) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > p.config.MaxResponseBytes {
		return nil, errRequestTooLarge
	}
	return p.doJSON(ctx, http.MethodPost, path, body, target)
}

func (p *httpProvider) doJSON(ctx context.Context, method, path string, body []byte, target interface{}) (json.RawMessage, error) {
	endpoint, err := joinURL(p.config.BaseURL, path)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "patris-export")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.config.BearerTokenEnv != "" {
		token := strings.TrimSpace(os.Getenv(p.config.BearerTokenEnv))
		if token == "" {
			return nil, errCredentialUnavailable
		}
		req.Header.Set("Authorization", "Bearer "+token)
	} else if p.config.UsernameEnv != "" || p.config.PasswordEnv != "" {
		username := os.Getenv(p.config.UsernameEnv)
		password := os.Getenv(p.config.PasswordEnv)
		if p.config.UsernameEnv == "" || p.config.PasswordEnv == "" || username == "" || password == "" {
			return nil, errCredentialUnavailable
		}
		req.SetBasicAuth(username, password)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return nil, &remoteStatusError{status: resp.StatusCode}
	}
	responseReader := io.LimitReader(resp.Body, p.config.MaxResponseBytes+1)
	data, err := io.ReadAll(responseReader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > p.config.MaxResponseBytes {
		return nil, errResponseTooLarge
	}
	var envelope struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(data, &outer); err == nil {
		_, wrapped := outer["data"]
		if wrapped {
			if err := decodeStrictJSON(data, &envelope); err != nil {
				return nil, &contractError{reason: "JSON wrapper decode failed"}
			}
			if len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
				return nil, &contractError{reason: "remote API omitted response data"}
			}
			if envelope.Success != nil && !*envelope.Success {
				return nil, &contractError{reason: "remote API reported failure"}
			}
			data = envelope.Data
		}
	}
	if err := decodeStrictJSON(data, target); err != nil {
		return nil, &contractError{reason: "JSON decode failed"}
	}
	return json.RawMessage(append([]byte(nil), data...)), nil
}

func decodeStrictJSON(data []byte, target interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("JSON document has trailing values")
	}
	return nil
}

func batchFailureWarning(err error) string {
	var status *remoteStatusError
	var contract *contractError
	switch {
	case errors.As(err, &status) && (status.status == http.StatusUnauthorized || status.status == http.StatusForbidden):
		return "pricing_assignment_batch_auth_failed"
	case errors.Is(err, errCredentialUnavailable):
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
	if value.roundingDigits != nil {
		digits := *value.roundingDigits
		copy.roundingDigits = &digits
	}
	copy.warnings = append([]string(nil), value.warnings...)
	copy.explicitNulls = cloneBoolMap(value.explicitNulls)
	copy.methodPriceNulls = cloneBoolMap(value.methodPriceNulls)
	copy.methodCurrencyNulls = cloneBoolMap(value.methodCurrencyNulls)
	copy.methodPairPresent = cloneBoolMap(value.methodPairPresent)
	copy.methodPairIncomplete = cloneBoolMap(value.methodPairIncomplete)
	copy.methods = make(map[string]Method, len(value.methods))
	for key, method := range value.methods {
		method.PricePerKg = cloneDecimal(method.PricePerKg)
		if method.Enabled != nil {
			enabled := *method.Enabled
			method.Enabled = &enabled
		}
		copy.methods[key] = method
	}
	return &copy
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func decimalEqual(left, right *Decimal) bool {
	l, lok := decimalRat(left)
	r, rok := decimalRat(right)
	return lok && rok && l.Cmp(r) == 0
}
