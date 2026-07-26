package recentsales

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	SchemaName           = "patris.recent-sales-aggregate"
	SchemaVersion        = 1
	MediaType            = "application/vnd.patris.recent-sales-aggregate+json"
	DefaultTokenEnv      = "PATRIS_EXPORT_RECENT_SALES_TOKEN"
	defaultMaxWindow     = 90 * 24 * time.Hour
	maxAllowedWindow     = 365 * 24 * time.Hour
	defaultMaxPageSize   = 500
	defaultMaxSourceRows = 1_000_000
	defaultMaxSourceMB   = int64(256)
	maxAllowedSourceMB   = int64(1024)
	maxPageNumber        = 1_000_000
)

// Config defines a fail-closed source profile for the recent-sales aggregate
// feed. TokenEnv names an environment variable; credential values are never
// stored in application configuration or returned by the API.
type Config struct {
	Enabled          bool   `json:"enabled" yaml:"enabled" toml:"enabled"`
	Source           string `json:"source" yaml:"source" toml:"source"`
	SourceID         string `json:"source_id" yaml:"source_id" toml:"source_id"`
	TokenEnv         string `json:"token_env" yaml:"token_env" toml:"token_env"`
	ProductCodeField string `json:"product_code_field" yaml:"product_code_field" toml:"product_code_field"`
	QuantityField    string `json:"quantity_field" yaml:"quantity_field" toml:"quantity_field"`
	SoldAtField      string `json:"sold_at_field" yaml:"sold_at_field" toml:"sold_at_field"`
	EventIDField     string `json:"event_id_field" yaml:"event_id_field" toml:"event_id_field"`
	MaxWindow        string `json:"max_window" yaml:"max_window" toml:"max_window"`
	MaxPageSize      int    `json:"max_page_size" yaml:"max_page_size" toml:"max_page_size"`
	MaxSourceRows    int    `json:"max_source_rows" yaml:"max_source_rows" toml:"max_source_rows"`
	MaxSourceMB      int64  `json:"max_source_mb" yaml:"max_source_mb" toml:"max_source_mb"`
}

func DefaultConfig() Config {
	return Config{
		SourceID:         "sales",
		TokenEnv:         DefaultTokenEnv,
		ProductCodeField: "product_code",
		QuantityField:    "quantity",
		SoldAtField:      "sold_at",
		EventIDField:     "sale_event_id",
		MaxWindow:        defaultMaxWindow.String(),
		MaxPageSize:      defaultMaxPageSize,
		MaxSourceRows:    defaultMaxSourceRows,
		MaxSourceMB:      defaultMaxSourceMB,
	}
}

func NormalizeConfig(cfg Config) Config {
	defaults := DefaultConfig()
	cfg.Source = strings.TrimSpace(cfg.Source)
	cfg.SourceID = strings.TrimSpace(cfg.SourceID)
	if cfg.SourceID == "" {
		cfg.SourceID = defaults.SourceID
	}
	cfg.TokenEnv = strings.TrimSpace(cfg.TokenEnv)
	if cfg.TokenEnv == "" {
		cfg.TokenEnv = defaults.TokenEnv
	}
	cfg.ProductCodeField = normalizedField(cfg.ProductCodeField, defaults.ProductCodeField)
	cfg.QuantityField = normalizedField(cfg.QuantityField, defaults.QuantityField)
	cfg.SoldAtField = normalizedField(cfg.SoldAtField, defaults.SoldAtField)
	cfg.EventIDField = normalizedField(cfg.EventIDField, defaults.EventIDField)
	cfg.MaxWindow = normalizeDuration(cfg.MaxWindow, defaultMaxWindow, time.Hour, maxAllowedWindow).String()
	if cfg.MaxPageSize <= 0 {
		cfg.MaxPageSize = defaults.MaxPageSize
	} else if cfg.MaxPageSize > defaultMaxPageSize {
		cfg.MaxPageSize = defaultMaxPageSize
	}
	if cfg.MaxSourceRows <= 0 {
		cfg.MaxSourceRows = defaults.MaxSourceRows
	} else if cfg.MaxSourceRows > defaultMaxSourceRows {
		cfg.MaxSourceRows = defaultMaxSourceRows
	}
	if cfg.MaxSourceMB <= 0 {
		cfg.MaxSourceMB = defaults.MaxSourceMB
	} else if cfg.MaxSourceMB > maxAllowedSourceMB {
		cfg.MaxSourceMB = maxAllowedSourceMB
	}
	return cfg
}

func normalizedField(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func normalizeDuration(value string, fallback, minimum, maximum time.Duration) time.Duration {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		parsed = fallback
	}
	if parsed < minimum {
		parsed = minimum
	}
	if parsed > maximum {
		parsed = maximum
	}
	return parsed
}

type Query struct {
	From     time.Time
	To       time.Time
	Page     int
	PageSize int
}

// ParseQuery validates an explicit, deterministic [from,to) window. Requiring
// both bounds prevents a changing server clock from silently changing a
// nightly operator's source window.
func ParseQuery(values url.Values, cfg Config) (Query, error) {
	cfg = NormalizeConfig(cfg)
	for key, supplied := range values {
		switch key {
		case "from", "to", "page", "page_size":
		default:
			return Query{}, fmt.Errorf("unsupported query parameter %q", key)
		}
		if len(supplied) != 1 {
			return Query{}, fmt.Errorf("query parameter %q must be supplied exactly once", key)
		}
	}
	from, err := parseRequiredTimestamp("from", values.Get("from"))
	if err != nil {
		return Query{}, err
	}
	to, err := parseRequiredTimestamp("to", values.Get("to"))
	if err != nil {
		return Query{}, err
	}
	if !from.Before(to) {
		return Query{}, fmt.Errorf("from must be before to")
	}
	maxWindow, _ := time.ParseDuration(cfg.MaxWindow)
	if to.Sub(from) > maxWindow {
		return Query{}, fmt.Errorf("requested window exceeds maximum %s", cfg.MaxWindow)
	}
	page, err := boundedPositiveInt("page", values.Get("page"), 1, maxPageNumber)
	if err != nil {
		return Query{}, err
	}
	pageSize, err := boundedPositiveInt("page_size", values.Get("page_size"), min(100, cfg.MaxPageSize), cfg.MaxPageSize)
	if err != nil {
		return Query{}, err
	}
	return Query{From: from.UTC(), To: to.UTC(), Page: page, PageSize: pageSize}, nil
}

func parseRequiredTimestamp(name, value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("%s is required", name)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be an RFC3339 timestamp", name)
	}
	return parsed.UTC(), nil
}

func boundedPositiveInt(name, value string, fallback, maximum int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 || parsed > maximum {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, maximum)
	}
	return parsed, nil
}

type Envelope struct {
	Schema  string      `json:"schema"`
	Version int         `json:"version"`
	Source  Source      `json:"source"`
	Window  Window      `json:"window"`
	Page    Page        `json:"page"`
	Sales   []Aggregate `json:"sales"`
}

type Source struct {
	ID       string `json:"id"`
	Dataset  string `json:"dataset"`
	Revision string `json:"revision"`
}

type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type Page struct {
	Number     int `json:"number"`
	Size       int `json:"size"`
	TotalItems int `json:"total_items"`
	TotalPages int `json:"total_pages"`
}

// Aggregate is deliberately closed to the four allowed product-level facts.
type Aggregate struct {
	ProductCode   string    `json:"product_code"`
	SoldQuantity  float64   `json:"sold_quantity"`
	SaleFrequency int       `json:"sale_frequency"`
	LastSoldAt    time.Time `json:"last_sold_at"`
}
