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
	ModeNone            = "none"
	ModeStatic          = "static"
	ModeDigitalogic     = "digitalogic"
	CurrencyCNY         = "CNY"
	CurrencyIRR         = "IRR"
	RoundingModeHalfUp  = "nearest_half_up"
	MinimumRoundDigits  = 0
	MaximumRoundDigits  = 9
	defaultFreshFor     = 5 * time.Minute
	defaultMaxStale     = time.Hour
	defaultTimeout      = 60 * time.Second
	minimumTimeout      = 100 * time.Millisecond
	maximumTimeout      = 80 * time.Second
	defaultMaxEntries   = 2048
	defaultMaxBytes     = int64(2 << 20)
	defaultConcurrency  = 8
	defaultBatchSize    = 500
	maximumBatchSize    = 500
	defaultBatchWorkers = 2
	maximumBatchWorkers = 4
)

// Config selects a replaceable pricing-catalog provider. Static is suitable
// for offline use; Digitalogic reads the living integration catalog and the
// exact Code/SKU product assignment endpoints.
type Config struct {
	Mode        string            `json:"mode,omitempty" yaml:"mode,omitempty" toml:"mode,omitempty"`
	Static      StaticConfig      `json:"static,omitempty" yaml:"static,omitempty" toml:"static,omitempty"`
	Digitalogic DigitalogicConfig `json:"digitalogic,omitempty" yaml:"digitalogic,omitempty" toml:"digitalogic,omitempty"`
}

type StaticConfig struct {
	Revision              string                `json:"revision,omitempty" yaml:"revision,omitempty" toml:"revision,omitempty"`
	CNYToIRT              *Decimal              `json:"cny_to_irt,omitempty" yaml:"cny_to_irt,omitempty" toml:"cny_to_irt,omitempty"`
	RoundingDigits        *int                  `json:"rounding_digits,omitempty" yaml:"rounding_digits,omitempty" toml:"rounding_digits,omitempty"`
	CurrencyEffectiveDate string                `json:"currency_effective_date,omitempty" yaml:"currency_effective_date,omitempty" toml:"currency_effective_date,omitempty"`
	SelectedWarehouses    []string              `json:"selected_warehouses,omitempty" yaml:"selected_warehouses,omitempty" toml:"selected_warehouses,omitempty"`
	Methods               []Method              `json:"shipping_methods,omitempty" yaml:"shipping_methods,omitempty" toml:"shipping_methods,omitempty"`
	Assignments           map[string]Assignment `json:"assignments,omitempty" yaml:"assignments,omitempty" toml:"assignments,omitempty"`
	DefaultAssignment     *Assignment           `json:"default_assignment,omitempty" yaml:"default_assignment,omitempty" toml:"default_assignment,omitempty"`

	roundingDigitsPresent bool
	roundingDigitsNull    bool
}

type DigitalogicConfig struct {
	BaseURL             string `json:"base_url,omitempty" yaml:"base_url,omitempty" toml:"base_url,omitempty"`
	CatalogPath         string `json:"catalog_path,omitempty" yaml:"catalog_path,omitempty" toml:"catalog_path,omitempty"`
	AssignmentPath      string `json:"assignment_path,omitempty" yaml:"assignment_path,omitempty" toml:"assignment_path,omitempty"`
	BatchAssignmentPath string `json:"batch_assignment_path,omitempty" yaml:"batch_assignment_path,omitempty" toml:"batch_assignment_path,omitempty"`
	BatchSize           int    `json:"batch_size,omitempty" yaml:"batch_size,omitempty" toml:"batch_size,omitempty"`
	BatchConcurrency    int    `json:"batch_concurrency,omitempty" yaml:"batch_concurrency,omitempty" toml:"batch_concurrency,omitempty"`
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
	ID         string   `json:"id" yaml:"id" toml:"id"`
	Name       string   `json:"name,omitempty" yaml:"name,omitempty" toml:"name,omitempty"`
	Enabled    *bool    `json:"enabled,omitempty" yaml:"enabled,omitempty" toml:"enabled,omitempty"`
	PricePerKg *Decimal `json:"price_per_kg,omitempty" yaml:"price_per_kg,omitempty" toml:"price_per_kg,omitempty"`
	Currency   string   `json:"currency,omitempty" yaml:"currency,omitempty" toml:"currency,omitempty"`

	pricePerKgPresent bool
	pricePerKgNull    bool
	currencyPresent   bool
	currencyNull      bool
}

type Assignment struct {
	MethodID      string   `json:"shipping_method_id,omitempty" yaml:"shipping_method_id,omitempty" toml:"shipping_method_id,omitempty"`
	ProfitPercent *Decimal `json:"profit_percent,omitempty" yaml:"profit_percent,omitempty" toml:"profit_percent,omitempty"`
}

// Resolution is the complete set of external inputs needed by
// landed_price for one immutable product Code.
type Resolution struct {
	CatalogRevision            string
	CatalogStatus              string
	CatalogFetchedAt           time.Time
	CurrencyEffectiveDate      string
	SelectedWarehouses         []string
	MethodID                   string
	ShippingPricePerKg         *Decimal
	ShippingPricePerKgCurrency string
	MarkupPercent              *Decimal
	MarkupPercentSource        string
	IRTPerCNY                  *Decimal
	RoundingDigits             *int
	ExplicitNulls              map[string]bool
	ShippingPricePairPresent   bool
	Warnings                   []string
}

type Provider interface {
	Resolve(context.Context, string) Resolution
}

// Prefetcher is an optional provider capability used by canonical transforms.
// It returns a transform-scoped Provider whose bounded result barrier protects
// the current run from persistent-LRU eviction. Providers that do not implement
// it retain per-record Resolve behavior, keeping static providers simple.
type Prefetcher interface {
	Prefetch(context.Context, []string) Provider
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
	for code, assignment := range cfg.Static.Assignments {
		assignment.MethodID = strings.TrimSpace(assignment.MethodID)
		cfg.Static.Assignments[code] = assignment
	}
	for index := range cfg.Static.Methods {
		cfg.Static.Methods[index].ID = strings.TrimSpace(cfg.Static.Methods[index].ID)
		cfg.Static.Methods[index].Name = strings.TrimSpace(cfg.Static.Methods[index].Name)
		cfg.Static.Methods[index].Currency = normalizeShippingCurrency(cfg.Static.Methods[index].Currency)
	}
	if cfg.Static.DefaultAssignment != nil {
		cfg.Static.DefaultAssignment.MethodID = strings.TrimSpace(cfg.Static.DefaultAssignment.MethodID)
	}

	d := &cfg.Digitalogic
	d.BaseURL = strings.TrimRight(strings.TrimSpace(d.BaseURL), "/")
	if strings.TrimSpace(d.CatalogPath) == "" {
		d.CatalogPath = "integration/catalog"
	}
	if strings.TrimSpace(d.AssignmentPath) == "" {
		d.AssignmentPath = "integration/products/by-code/{code}/pricing"
	}
	if strings.TrimSpace(d.BatchAssignmentPath) == "" {
		d.BatchAssignmentPath = "integration/pricing-assignments/batch"
	}
	if d.BatchSize <= 0 {
		d.BatchSize = defaultBatchSize
	}
	if d.BatchSize > maximumBatchSize {
		d.BatchSize = maximumBatchSize
	}
	if d.BatchConcurrency <= 0 {
		d.BatchConcurrency = defaultBatchWorkers
	}
	if d.BatchConcurrency > maximumBatchWorkers {
		d.BatchConcurrency = maximumBatchWorkers
	}
	if parseDuration(d.FreshFor, 0) <= 0 {
		d.FreshFor = defaultFreshFor.String()
	}
	if parseDuration(d.MaxStale, 0) <= 0 {
		d.MaxStale = defaultMaxStale.String()
	}
	timeout := parseDuration(d.Timeout, defaultTimeout)
	if timeout < minimumTimeout {
		timeout = minimumTimeout
	}
	if timeout > maximumTimeout {
		timeout = maximumTimeout
	}
	d.Timeout = timeout.String()
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

// Configured reports whether pricing/shipping input is intentionally active.
// The default empty static config is standalone mode and must not leak an
// integration-shaped schema into exported rows.
func Configured(cfg Config) bool {
	cfg = Normalize(cfg)
	switch cfg.Mode {
	case ModeDigitalogic:
		return strings.TrimSpace(cfg.Digitalogic.BaseURL) != ""
	case ModeStatic:
		static := cfg.Static
		return static.CNYToIRT != nil || static.RoundingDigits != nil || static.roundingDigitsPresent || strings.TrimSpace(static.CurrencyEffectiveDate) != "" || len(static.SelectedWarehouses) > 0 || len(static.Methods) > 0 || len(static.Assignments) > 0 || static.DefaultAssignment != nil
	default:
		return false
	}
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
		Warnings:      []string{"pricing_catalog_disabled", "shipping_method_missing"},
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
	if p.config.roundingDigitsNull {
		if resolution.ExplicitNulls == nil {
			resolution.ExplicitNulls = make(map[string]bool)
		}
		resolution.ExplicitNulls["price_rounding_digits"] = true
		resolution.Warnings = append(resolution.Warnings, "price_rounding_digits_explicit_null")
	} else if p.config.RoundingDigits != nil {
		digits := *p.config.RoundingDigits
		resolution.RoundingDigits = &digits
	} else {
		digits := MinimumRoundDigits
		resolution.RoundingDigits = &digits
	}
	if !ok {
		resolution.Warnings = append(resolution.Warnings, "pricing_assignment_missing")
	} else {
		resolution.MarkupPercent = cloneDecimal(assignment.ProfitPercent)
	}
	if !ok || strings.TrimSpace(assignment.MethodID) == "" {
		resolution.Warnings = append(resolution.Warnings, "shipping_method_missing")
	} else {
		resolution.MethodID = strings.TrimSpace(assignment.MethodID)
		method, exists := p.methods[resolution.MethodID]
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
			resolution.ShippingPricePairPresent = pricePresent && currencyPresent
			if pricePresent != currencyPresent {
				resolution.Warnings = append(resolution.Warnings, "shipping_price_per_kg_pair_incomplete")
			}
			if method.pricePerKgNull {
				if resolution.ExplicitNulls == nil {
					resolution.ExplicitNulls = make(map[string]bool)
				}
				resolution.ExplicitNulls["shipping_price_per_kg"] = true
			}
			if method.currencyNull {
				if resolution.ExplicitNulls == nil {
					resolution.ExplicitNulls = make(map[string]bool)
				}
				resolution.ExplicitNulls["shipping_price_per_kg_currency"] = true
			}
			if method.Enabled != nil && !*method.Enabled {
				resolution.Warnings = append(resolution.Warnings, "shipping_method_disabled")
			}
		}
	}
	return finishResolution(resolution)
}

func finishResolution(value Resolution) Resolution {
	value.MarkupPercentSource = strings.TrimSpace(value.MarkupPercentSource)
	value.ShippingPricePerKgCurrency = normalizeShippingCurrency(value.ShippingPricePerKgCurrency)
	priceExplicitlyNull := value.ExplicitNulls["shipping_price_per_kg"]
	currencyExplicitlyNull := value.ExplicitNulls["shipping_price_per_kg_currency"]
	priceAvailable := validPositive(value.ShippingPricePerKg)
	currencyAvailable := validShippingCurrency(value.ShippingPricePerKgCurrency)
	if !priceAvailable {
		value.ShippingPricePerKg = nil
		value.Warnings = append(value.Warnings, "shipping_price_per_kg_missing")
	}
	if !currencyAvailable {
		if value.ShippingPricePerKgCurrency != "" && !currencyExplicitlyNull {
			value.Warnings = append(value.Warnings, "shipping_price_per_kg_currency_invalid")
		}
		value.ShippingPricePerKgCurrency = ""
		value.Warnings = append(value.Warnings, "shipping_price_per_kg_currency_missing")
	}
	if (priceAvailable || priceExplicitlyNull) != (currencyAvailable || currencyExplicitlyNull) {
		value.ShippingPricePerKg = nil
		value.ShippingPricePerKgCurrency = ""
		delete(value.ExplicitNulls, "shipping_price_per_kg")
		delete(value.ExplicitNulls, "shipping_price_per_kg_currency")
		value.ShippingPricePairPresent = false
		value.Warnings = append(value.Warnings, "shipping_price_per_kg_pair_incomplete")
	} else if priceAvailable || priceExplicitlyNull {
		value.ShippingPricePairPresent = true
	}
	if !validNonNegative(value.MarkupPercent) {
		value.MarkupPercent = nil
		value.MarkupPercentSource = ""
		value.Warnings = append(value.Warnings, "markup_percent_missing")
	}
	if !validPositive(value.IRTPerCNY) {
		value.IRTPerCNY = nil
		value.Warnings = append(value.Warnings, "fx_rate_missing")
	}
	if value.RoundingDigits != nil {
		if *value.RoundingDigits < MinimumRoundDigits || *value.RoundingDigits > MaximumRoundDigits {
			value.RoundingDigits = nil
			value.Warnings = append(value.Warnings, "price_rounding_digits_invalid")
		}
	}
	value.Warnings = normalizedStrings(value.Warnings)
	return value
}

func normalizeShippingCurrency(value string) string {
	return value
}

func validShippingCurrency(value string) bool {
	switch normalizeShippingCurrency(value) {
	case CurrencyCNY, CurrencyIRR:
		return true
	default:
		return false
	}
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
