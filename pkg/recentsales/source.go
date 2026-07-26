package recentsales

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/datasource"
)

var ErrSourceChanged = errors.New("recent-sales source changed while it was being read")

type saleEvent struct {
	ProductCode string
	Quantity    float64
	SoldAt      time.Time
}

// Load reads a separately configured sales source through the existing
// datasource abstraction. It explicitly rejects the primary product database:
// this contract is not an excuse to infer sales from kala.db.
func Load(ctx context.Context, cfg Config, query Query, primaryDatabase string) (Envelope, error) {
	cfg = NormalizeConfig(cfg)
	if err := validateQuery(query, cfg); err != nil {
		return Envelope{}, err
	}
	if err := validateSource(cfg.Source, primaryDatabase); err != nil {
		return Envelope{}, err
	}
	info, err := os.Stat(cfg.Source)
	if err != nil {
		return Envelope{}, fmt.Errorf("stat recent-sales source: %w", err)
	}
	maxSourceBytes := cfg.MaxSourceMB * 1024 * 1024
	if info.Size() > maxSourceBytes {
		return Envelope{}, fmt.Errorf("recent-sales source is %d bytes; maximum is %d", info.Size(), maxSourceBytes)
	}
	before, err := sourceRevision(ctx, cfg.Source)
	if err != nil {
		return Envelope{}, err
	}
	ds, err := datasource.NewDataSource(cfg.Source, converter.DefaultCharMapping(), true)
	if err != nil {
		return Envelope{}, fmt.Errorf("open recent-sales source: %w", err)
	}
	defer ds.Close()
	contextSource, ok := ds.(datasource.ContextDataSource)
	if !ok {
		return Envelope{}, fmt.Errorf("recent-sales source does not support bounded reads")
	}
	rows, err := contextSource.GetRawRecordsContext(ctx)
	if err != nil {
		return Envelope{}, fmt.Errorf("read recent-sales source: %w", err)
	}
	if len(rows) > cfg.MaxSourceRows {
		return Envelope{}, fmt.Errorf("recent-sales source has %d rows; maximum is %d", len(rows), cfg.MaxSourceRows)
	}
	after, err := sourceRevision(ctx, cfg.Source)
	if err != nil {
		return Envelope{}, err
	}
	if before != after {
		return Envelope{}, ErrSourceChanged
	}
	aggregates, err := aggregateRows(ctx, cfg, query, rows)
	if err != nil {
		return Envelope{}, err
	}
	totalItems := len(aggregates)
	totalPages := 0
	if totalItems > 0 {
		totalPages = (totalItems + query.PageSize - 1) / query.PageSize
	}
	start := (query.Page - 1) * query.PageSize
	if start > totalItems {
		start = totalItems
	}
	end := min(start+query.PageSize, totalItems)
	pageRows := append([]Aggregate(nil), aggregates[start:end]...)
	if pageRows == nil {
		pageRows = []Aggregate{}
	}
	return Envelope{
		Schema:  SchemaName,
		Version: SchemaVersion,
		Source: Source{
			ID:       cfg.SourceID,
			Dataset:  filepath.Base(cfg.Source),
			Revision: before,
		},
		Window: Window{From: query.From.UTC(), To: query.To.UTC()},
		Page: Page{
			Number:     query.Page,
			Size:       query.PageSize,
			TotalItems: totalItems,
			TotalPages: totalPages,
		},
		Sales: pageRows,
	}, nil
}

func validateQuery(query Query, cfg Config) error {
	if query.Page <= 0 || query.Page > maxPageNumber {
		return fmt.Errorf("page must be between 1 and %d", maxPageNumber)
	}
	if query.PageSize <= 0 || query.PageSize > cfg.MaxPageSize {
		return fmt.Errorf("page_size must be between 1 and %d", cfg.MaxPageSize)
	}
	if query.From.IsZero() || query.To.IsZero() || !query.From.Before(query.To) {
		return fmt.Errorf("from must be before to")
	}
	maxWindow, _ := time.ParseDuration(cfg.MaxWindow)
	if query.To.Sub(query.From) > maxWindow {
		return fmt.Errorf("requested window exceeds maximum %s", cfg.MaxWindow)
	}
	return nil
}

func validateSource(source, primaryDatabase string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return fmt.Errorf("recent-sales source is not configured")
	}
	if strings.Contains(source, "://") {
		return fmt.Errorf("recent-sales source must be a local supported datasource")
	}
	if strings.EqualFold(filepath.Base(source), "kala.db") {
		return fmt.Errorf("kala.db cannot be used as a recent-sales source")
	}
	sourceAbs, sourceErr := filepath.Abs(source)
	primaryAbs, primaryErr := filepath.Abs(strings.TrimSpace(primaryDatabase))
	if sourceErr == nil && primaryErr == nil && strings.EqualFold(filepath.Clean(sourceAbs), filepath.Clean(primaryAbs)) {
		return fmt.Errorf("the primary product database cannot be used as a recent-sales source")
	}
	return nil
}

func sourceRevision(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open recent-sales source revision: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	reader := bufio.NewReaderSize(file, 64*1024)
	buffer := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		read, readErr := reader.Read(buffer)
		if read > 0 {
			if _, err := hash.Write(buffer[:read]); err != nil {
				return "", err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return "", fmt.Errorf("hash recent-sales source: %w", readErr)
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func aggregateRows(ctx context.Context, cfg Config, query Query, rows []map[string]interface{}) ([]Aggregate, error) {
	events := make(map[string]saleEvent, len(rows))
	for index, row := range rows {
		if index%128 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		eventID, err := exactString(row[cfg.EventIDField])
		if err != nil || eventID == "" {
			return nil, fmt.Errorf("recent-sales row %d has invalid %s", index+1, cfg.EventIDField)
		}
		productCode, err := exactString(row[cfg.ProductCodeField])
		if err != nil || productCode == "" {
			return nil, fmt.Errorf("recent-sales row %d has invalid %s", index+1, cfg.ProductCodeField)
		}
		quantity, err := positiveNumber(row[cfg.QuantityField])
		if err != nil {
			return nil, fmt.Errorf("recent-sales row %d has invalid %s", index+1, cfg.QuantityField)
		}
		soldAt, err := timestamp(row[cfg.SoldAtField])
		if err != nil {
			return nil, fmt.Errorf("recent-sales row %d has invalid %s", index+1, cfg.SoldAtField)
		}
		event := saleEvent{ProductCode: productCode, Quantity: quantity, SoldAt: soldAt}
		if existing, exists := events[eventID]; exists {
			if existing != event {
				return nil, fmt.Errorf("recent-sales source has conflicting duplicate event identifiers")
			}
			continue
		}
		events[eventID] = event
	}

	byProduct := make(map[string]Aggregate)
	for _, event := range events {
		if event.SoldAt.Before(query.From) || !event.SoldAt.Before(query.To) {
			continue
		}
		aggregate := byProduct[event.ProductCode]
		aggregate.ProductCode = event.ProductCode
		aggregate.SoldQuantity += event.Quantity
		aggregate.SaleFrequency++
		if aggregate.LastSoldAt.IsZero() || event.SoldAt.After(aggregate.LastSoldAt) {
			aggregate.LastSoldAt = event.SoldAt
		}
		byProduct[event.ProductCode] = aggregate
	}
	result := make([]Aggregate, 0, len(byProduct))
	for _, aggregate := range byProduct {
		result = append(result, aggregate)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ProductCode < result[j].ProductCode
	})
	return result, nil
}

func exactString(value interface{}) (string, error) {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed), nil
	case json.Number:
		return strings.TrimSpace(typed.String()), nil
	case int:
		return strconv.Itoa(typed), nil
	case int8:
		return strconv.FormatInt(int64(typed), 10), nil
	case int16:
		return strconv.FormatInt(int64(typed), 10), nil
	case int32:
		return strconv.FormatInt(int64(typed), 10), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case uint:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint64:
		return strconv.FormatUint(typed, 10), nil
	default:
		return "", fmt.Errorf("unsupported exact identifier type %T", value)
	}
}

func positiveNumber(value interface{}) (float64, error) {
	var number float64
	var err error
	switch typed := value.(type) {
	case json.Number:
		number, err = typed.Float64()
	case string:
		number, err = strconv.ParseFloat(strings.TrimSpace(typed), 64)
	case int:
		number = float64(typed)
	case int8:
		number = float64(typed)
	case int16:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint:
		number = float64(typed)
	case uint8:
		number = float64(typed)
	case uint16:
		number = float64(typed)
	case uint32:
		number = float64(typed)
	case uint64:
		number = float64(typed)
	case float32:
		number = float64(typed)
	case float64:
		number = typed
	default:
		return 0, fmt.Errorf("unsupported quantity type %T", value)
	}
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number <= 0 {
		return 0, fmt.Errorf("quantity must be a positive finite number")
	}
	return number, nil
}

func timestamp(value interface{}) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		if typed.IsZero() {
			return time.Time{}, fmt.Errorf("timestamp is empty")
		}
		return typed.UTC(), nil
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(typed))
		if err != nil {
			return time.Time{}, err
		}
		return parsed.UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported timestamp type %T", value)
	}
}
