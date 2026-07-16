package pricingcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"sort"
	"strings"
	"time"
)

const (
	ModeNone           = "none"
	ModeStatic         = "static"
	ModeDigitalogic    = "digitalogic"
	defaultFreshFor    = 5 * time.Minute
	defaultMaxStale    = time.Hour
	defaultTimeout     = 5 * time.Second
	defaultMaxEntries  = 2048
	defaultMaxBytes    = int64(2 << 20)
	defaultConcurrency = 8
	defaultBatchSize   = 500
	maximumBatchSize   = 500
)

// Config selects a replaceable pricing-catalog provider. Static is suitable
// for offline use; Digitalogic reads the versioned integration catalog and the
// exact Code/SKU product assignment endpoints.
type Config struct {
	Mode        string            `json:"mode,omitempty" yaml:"mode,omitempty" toml:"mode,omitempty"`
	Static      StaticConfig      `json:"static,omitempty" yaml:"static,omitempty" toml:"static,omitempty"`
	Digitalogic DigitalogicConfig `json:"digitalogic,omitempty" yaml:"digitalogic,omitempty" toml:"digitalogic,omitempty"`
}

type StaticConfig struct {
	Revision              string                `json:"revision,omitempty" yaml:"revision,omitempty" toml:"revision,omitempty"`
	CNYToIRT              *Decimal              `json:"cny_to_irt,omitempty" yaml:"cny_to_irt,omitempty" toml:"cny_to_irt,omitempty"`
	CurrencyEffectiveDate string                `json:"currency_effective_date,omitempty" yaml:"currency_effective_date,omitempty" toml:"currency_effective_date,omitempty"`
	SelectedWarehouses    []string              `json:"selected_warehouses,omitempty" yaml:"selected_warehouses,omitempty" toml:"selected_warehouses,omitempty"`
	Methods               []Method              `json:"import_freight_methods,omitempty" yaml:"import_freight_methods,omitempty" toml:"import_freight_methods,omitempty"`
	Assignments           map[string]Assignment `json:"assignments,omitempty" yaml:"assignments,omitempty" toml:"assignments,omitempty"`
	DefaultAssignment     *Assignment           `json:"default_assignment,omitempty" yaml:"default_assignment,omitempty" toml:"default_assignment,omitempty"`
}

type DigitalogicConfig struct {
	BaseURL             string `json:"base_url,omitempty" yaml:"base_url,omitempty" toml:"base_url,omitempty"`
	CatalogPath         string `json:"catalog_path,omitempty" yaml:"catalog_path,omitempty" toml:"catalog_path,omitempty"`
	AssignmentPath      string `json:"assignment_path,omitempty" yaml:"assignment_path,omitempty" toml:"assignment_path,omitempty"`
	BatchAssignmentPath string `json:"batch_assignment_path,omitempty" yaml:"batch_assignment_path,omitempty" toml:"batch_assignment_path,omitempty"`
	BatchSize           int    `json:"batch_size,omitempty" yaml:"batch_size,omitempty" toml:"batch_size,omitempty"`
	UsernameEnv         string `json:"username_env,omitempty" yaml:"username_env,omitempty" toml:"username_env,omitempty"`
	PasswordEnv         string `json:"password_env,omitempty" yaml:"password_env,omitempty" toml:"password_env,omitempty"`
	BearerTokenEnv      string `json:"bearer_token_env,omitempty" yaml:"bearer_token_env,omitempty" toml:"bearer_token_env,omitempty"`
	FreshFor            string `json:"fresh_for,omitempty" yaml:"fresh_for,omitempty" toml:"fresh_for,omitempty"`
	MaxStale            string `json:"max_stale,omitempty" yaml:"max_stale,omitempty" toml:"max_stale,omitempty"`
	Timeout             string `json:"timeout,omitempty" yaml:"timeout,omitempty" toml:"timeout,omitempty"`
	MaxEntries          int    `json:"max_entries,omitempty" yaml:"max_entries,omitempty" toml:"max_entries,omitempty"`
	MaxConcurrency      int    `json:"max_concurrency,omitempty" yaml:"max_concurrency,omitempty" toml:"max_concurrency,omitempty"`
	MaxResponseBytes    int64  `json:"max_response_bytes,omitempty" yaml:"max_response_bytes,omitempty" toml:"max_response_bytes,omitempty"`
}

type Method struct {
	ID            string   `json:"id" yaml:"id" toml:"id"`
	Name          string   `json:"name,omitempty" yaml:"name,omitempty" toml:"name,omitempty"`
	Enabled       *bool    `json:"enabled,omitempty" yaml:"enabled,omitempty" toml:"enabled,omitempty"`
	PricePerKgCNY *Decimal `json:"price_per_kg_cny,omitempty" yaml:"price_per_kg_cny,omitempty" toml:"price_per_kg_cny,omitempty"`
}

type Assignment struct {
	MethodID      string   `json:"import_freight_method_id,omitempty" yaml:"import_freight_method_id,omitempty" toml:"import_freight_method_id,omitempty"`
	ProfitPercent *Decimal `json:"profit_percent,omitempty" yaml:"profit_percent,omitempty" toml:"profit_percent,omitempty"`
}

// Resolution is the complete set of external inputs needed by
// landed_price_v1 for one immutable product Code.
type Resolution struct {
	CatalogRevision       string
	CatalogStatus         string
	CatalogFetchedAt      time.Time
	CurrencyEffectiveDate string
	SelectedWarehouses    []string
	MethodID              string
	FreightCNYPerKg       *Decimal
	MarkupPercent         *Decimal
	IRTPerCNY             *Decimal
	Warnings              []string
}

type Provider interface {
	Resolve(context.Context, string) Resolution
}

// Prefetcher is an optional provider capability used by canonical transforms.
// Providers that do not implement it retain the existing per-record Resolve
// behavior, which keeps static and independently replaceable providers simple.
type Prefetcher interface {
	Prefetch(context.Context, []string)
}

func DefaultConfig() Config {
	return Config{Mode: ModeStatic}
}

func Normalize(cfg Config) Config {
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch cfg.Mode {
	case ModeNone, ModeStatic, ModeDigitalogic:
	default:
		cfg.Mode = ModeStatic
	}
	cfg.Static.Revision = strings.TrimSpace(cfg.Static.Revision)
	cfg.Static.CurrencyEffectiveDate = strings.TrimSpace(cfg.Static.CurrencyEffectiveDate)
	cfg.Static.SelectedWarehouses = normalizedStrings(cfg.Static.SelectedWarehouses)
	if cfg.Static.Assignments == nil {
		cfg.Static.Assignments = map[string]Assignment{}
	}

	d := &cfg.Digitalogic
	d.BaseURL = strings.TrimRight(strings.TrimSpace(d.BaseURL), "/")
	if strings.TrimSpace(d.CatalogPath) == "" {
		d.CatalogPath = "integration/catalog"
	}
	if strings.TrimSpace(d.AssignmentPath) == "" {
		d.AssignmentPath = "products/by-code/{code}/import-pricing"
	}
	if strings.TrimSpace(d.BatchAssignmentPath) == "" {
		d.BatchAssignmentPath = "pricing-assignments/batch"
	}
	if d.BatchSize <= 0 {
		d.BatchSize = defaultBatchSize
	}
	if d.BatchSize > maximumBatchSize {
		d.BatchSize = maximumBatchSize
	}
	if parseDuration(d.FreshFor, 0) <= 0 {
		d.FreshFor = defaultFreshFor.String()
	}
	if parseDuration(d.MaxStale, 0) <= 0 {
		d.MaxStale = defaultMaxStale.String()
	}
	if parseDuration(d.Timeout, 0) <= 0 {
		d.Timeout = defaultTimeout.String()
	}
	if d.MaxEntries <= 0 {
		d.MaxEntries = defaultMaxEntries
	}
	if d.MaxConcurrency <= 0 {
		d.MaxConcurrency = defaultConcurrency
	}
	if d.MaxResponseBytes <= 0 {
		d.MaxResponseBytes = defaultMaxBytes
	}
	return cfg
}

func NewProvider(cfg Config) Provider {
	cfg = Normalize(cfg)
	switch cfg.Mode {
	case ModeDigitalogic:
		return newHTTPProvider(cfg.Digitalogic, nil, time.Now)
	case ModeNone:
		return disabledProvider{}
	default:
		return newStaticProvider(cfg.Static)
	}
}

type disabledProvider struct{}

func (disabledProvider) Resolve(context.Context, string) Resolution {
	return finishResolution(Resolution{
		CatalogStatus: "disabled",
		Warnings:      []string{"pricing_catalog_disabled", "import_freight_method_missing"},
	})
}

type staticProvider struct {
	config   StaticConfig
	methods  map[string]Method
	revision string
}

func newStaticProvider(cfg StaticConfig) Provider {
	methods := make(map[string]Method, len(cfg.Methods))
	for _, method := range cfg.Methods {
		method.ID = strings.TrimSpace(method.ID)
		if method.ID != "" {
			methods[method.ID] = method
		}
	}
	revision := strings.TrimSpace(cfg.Revision)
	if revision == "" {
		material, _ := json.Marshal(cfg)
		digest := sha256.Sum256(material)
		revision = "sha256:" + hex.EncodeToString(digest[:])
	}
	return &staticProvider{config: cfg, methods: methods, revision: revision}
}

func (p *staticProvider) Resolve(_ context.Context, code string) Resolution {
	assignment, ok := p.config.Assignments[strings.TrimSpace(code)]
	if !ok && p.config.DefaultAssignment != nil {
		assignment = *p.config.DefaultAssignment
		ok = true
	}
	resolution := Resolution{
		CatalogRevision:       p.revision,
		CatalogStatus:         "static",
		CurrencyEffectiveDate: p.config.CurrencyEffectiveDate,
		SelectedWarehouses:    append([]string(nil), p.config.SelectedWarehouses...),
		IRTPerCNY:             cloneDecimal(p.config.CNYToIRT),
	}
	if !ok || strings.TrimSpace(assignment.MethodID) == "" {
		resolution.Warnings = append(resolution.Warnings, "import_freight_method_missing")
	} else {
		resolution.MethodID = strings.TrimSpace(assignment.MethodID)
		resolution.MarkupPercent = cloneDecimal(assignment.ProfitPercent)
		method, exists := p.methods[resolution.MethodID]
		if !exists {
			resolution.Warnings = append(resolution.Warnings, "import_freight_method_unknown")
		} else {
			resolution.FreightCNYPerKg = cloneDecimal(method.PricePerKgCNY)
			if method.Enabled != nil && !*method.Enabled {
				resolution.Warnings = append(resolution.Warnings, "import_freight_method_disabled")
			}
		}
	}
	return finishResolution(resolution)
}

func finishResolution(value Resolution) Resolution {
	if !validPositive(value.FreightCNYPerKg) {
		value.FreightCNYPerKg = nil
		value.Warnings = append(value.Warnings, "freight_rate_missing")
	}
	if !validNonNegative(value.MarkupPercent) {
		value.MarkupPercent = nil
		value.Warnings = append(value.Warnings, "markup_percent_missing")
	}
	if !validPositive(value.IRTPerCNY) {
		value.IRTPerCNY = nil
		value.Warnings = append(value.Warnings, "fx_rate_missing")
	}
	value.Warnings = normalizedStrings(value.Warnings)
	return value
}

func validPositive(value *Decimal) bool {
	parsed, ok := decimalRat(value)
	return ok && parsed.Sign() > 0
}

func validNonNegative(value *Decimal) bool {
	parsed, ok := decimalRat(value)
	return ok && parsed.Sign() >= 0
}

func decimalRat(value *Decimal) (*big.Rat, bool) {
	if value == nil {
		return nil, false
	}
	return value.Rat()
}

func normalizedStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func parseDuration(value string, fallback time.Duration) time.Duration {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}
