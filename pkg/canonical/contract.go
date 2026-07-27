package canonical

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/recorddiff"
)

const (
	ContractName  = "patris.product-sync"
	FormulaID     = "landed_price"
	LocalCurrency = "IRT"
)

type Source struct {
	ID       string `json:"id"`
	Dataset  string `json:"dataset"`
	Revision string `json:"revision"`
}

// SourceIdentity derives the same stable source ID and dataset used by
// canonical envelopes without materializing the full product catalog.
func SourceIdentity(source, sourceID, revision string) Source {
	if strings.TrimSpace(sourceID) == "" {
		sourceID = sourceBaseName(source)
	}
	return Source{
		ID:       sourceID,
		Dataset:  sourceBaseName(source),
		Revision: revision,
	}
}

func (source *Source) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := rejectUnknownJSONFields(raw, "product-sync source", []string{"id", "dataset", "revision"}); err != nil {
		return err
	}
	type sourceAlias Source
	var decoded sourceAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*source = Source(decoded)
	return nil
}

type Tombstone struct {
	ProductCode string `json:"product_code"`
	Deleted     bool   `json:"deleted"`
}

func (tombstone *Tombstone) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := rejectUnknownJSONFields(raw, "product tombstone", []string{"product_code", "deleted"}); err != nil {
		return err
	}
	type tombstoneAlias Tombstone
	var decoded tombstoneAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*tombstone = Tombstone(decoded)
	return nil
}

type Envelope struct {
	Schema           string      `json:"schema"`
	EventType        string      `json:"event_type"`
	EventID          string      `json:"event_id"`
	LocalCurrency    string      `json:"local_currency,omitempty"`
	FormulaID        string      `json:"formula_id,omitempty"`
	Source           Source      `json:"source"`
	GeneratedAt      string      `json:"generated_at"`
	Products         []Product   `json:"products"`
	Categories       []Category  `json:"categories"`
	ExcludedCodes    []string    `json:"excluded_codes"`
	DeletedCodes     []Tombstone `json:"deleted_codes,omitempty"`
	QuarantinedCodes []string    `json:"quarantined_codes"`
	Warnings         []string    `json:"warnings"`
	fieldPresence    map[string]fieldPresence
}

func (envelope *Envelope) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := rejectUnknownJSONFields(raw, "product-sync envelope", []string{
		"schema", "event_type", "event_id", "local_currency", "formula_id", "source",
		"generated_at", "products", "categories", "excluded_codes", "deleted_codes",
		"quarantined_codes", "warnings",
	}); err != nil {
		return err
	}
	type envelopeAlias Envelope
	var decoded envelopeAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*envelope = Envelope(decoded)
	envelope.fieldPresence = make(map[string]fieldPresence, 3)
	for _, field := range []string{"local_currency", "formula_id", "deleted_codes"} {
		if value, exists := raw[field]; exists {
			if strings.TrimSpace(string(value)) == "null" {
				envelope.fieldPresence[field] = fieldNull
			} else {
				envelope.fieldPresence[field] = fieldValue
			}
		}
	}
	return nil
}

func rejectUnknownJSONFields(raw map[string]json.RawMessage, kind string, allowed []string) error {
	known := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		known[field] = struct{}{}
	}
	for field := range raw {
		if _, exists := known[field]; !exists {
			return fmt.Errorf("%s contains unknown field %q", kind, field)
		}
	}
	return nil
}

func NewEnvelope(rows []Product, source, sourceID string, generatedAt time.Time, quarantinedCodes ...string) *Envelope {
	envelope, _ := newEnvelopeContext(context.Background(), rows, nil, nil, source, sourceID, generatedAt, quarantinedCodes...)
	return envelope
}

func NewEnvelopeWithCategories(rows []Product, categories []Category, source, sourceID string, generatedAt time.Time, quarantinedCodes ...string) *Envelope {
	envelope, _ := newEnvelopeContext(context.Background(), rows, categories, nil, source, sourceID, generatedAt, quarantinedCodes...)
	return envelope
}

func NewCatalogEnvelope(rows []Product, categories []Category, excludedCodes []string, source, sourceID string, generatedAt time.Time, quarantinedCodes ...string) *Envelope {
	envelope, _ := NewCatalogEnvelopeContext(context.Background(), rows, categories, excludedCodes, source, sourceID, generatedAt, quarantinedCodes...)
	return envelope
}

// NewCatalogEnvelopeContext builds a deterministic snapshot while allowing
// request cancellation to interrupt cloning, sorting, revision hashing, and
// event identity materialization.
func NewCatalogEnvelopeContext(ctx context.Context, rows []Product, categories []Category, excludedCodes []string, source, sourceID string, generatedAt time.Time, quarantinedCodes ...string) (*Envelope, error) {
	return newEnvelopeContext(ctx, rows, categories, excludedCodes, source, sourceID, generatedAt, quarantinedCodes...)
}

func newEnvelopeContext(ctx context.Context, rows []Product, categories []Category, excludedCodes []string, source, sourceID string, generatedAt time.Time, quarantinedCodes ...string) (*Envelope, error) {
	envelope, err := newEnvelopeBaseContext(ctx, rows, categories, excludedCodes, source, sourceID, generatedAt, quarantinedCodes...)
	if err != nil {
		return nil, err
	}
	envelope.EventID, err = eventIDContext(ctx, envelope)
	if err != nil {
		return nil, err
	}
	return envelope, nil
}

func newEnvelopeBaseContext(ctx context.Context, rows []Product, categories []Category, excludedCodes []string, source, sourceID string, generatedAt time.Time, quarantinedCodes ...string) (*Envelope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if generatedAt.IsZero() {
		generatedAt = time.Now()
	}
	products, err := cloneProductsContext(ctx, rows)
	if err != nil {
		return nil, err
	}
	if err := stableSortContext(ctx, products, func(left, right Product) bool {
		return left.ProductCode < right.ProductCode
	}); err != nil {
		return nil, err
	}
	categories, err = cloneCategoriesContext(ctx, categories)
	if err != nil {
		return nil, err
	}
	if err := stableSortContext(ctx, categories, func(left, right Category) bool {
		return left.CategoryCode < right.CategoryCode
	}); err != nil {
		return nil, err
	}
	quarantinedCodes, err = normalizedWarningsContext(ctx, quarantinedCodes)
	if err != nil {
		return nil, err
	}
	excludedCodes, err = normalizedWarningsContext(ctx, excludedCodes)
	if err != nil {
		return nil, err
	}
	revision, err := sourceRevisionContext(ctx, products, categories, excludedCodes, quarantinedCodes)
	if err != nil {
		return nil, err
	}
	envelope := &Envelope{
		Schema:           ContractName,
		EventType:        "snapshot",
		LocalCurrency:    LocalCurrency,
		FormulaID:        FormulaID,
		Source:           SourceIdentity(source, sourceID, revision),
		GeneratedAt:      generatedAt.UTC().Format(time.RFC3339Nano),
		Products:         products,
		Categories:       categories,
		ExcludedCodes:    excludedCodes,
		QuarantinedCodes: quarantinedCodes,
		Warnings:         normalizedWarnings(nil),
	}
	for _, code := range quarantinedCodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		envelope.Warnings = append(envelope.Warnings, "duplicate_product_code:"+code)
	}
	return envelope, nil
}

func ChangeEnvelope(snapshot *Envelope, changes *recorddiff.ChangeSet) *Envelope {
	if snapshot == nil {
		return nil
	}
	if changes == nil {
		copy := cloneEnvelope(snapshot)
		return copy
	}
	byCode := make(map[string]Product, len(snapshot.Products))
	for _, product := range snapshot.Products {
		byCode[product.ProductCode] = product
	}
	codes := make(map[string]struct{})
	keyField := strings.TrimSpace(changes.KeyField)
	if keyField == "" {
		keyField = "product_code"
	}
	for _, added := range changes.Added {
		codes[stringValue(added[keyField])] = struct{}{}
	}
	for _, modified := range changes.Modified {
		codes[modified.Code] = struct{}{}
	}
	products := make([]Product, 0, len(codes))
	for code := range codes {
		if product, ok := byCode[code]; ok {
			products = append(products, cloneProduct(product))
		}
	}
	sort.SliceStable(products, func(i, j int) bool {
		return products[i].ProductCode < products[j].ProductCode
	})
	deleted := make([]Tombstone, 0, len(changes.Deleted))
	quarantined := make(map[string]struct{}, len(snapshot.QuarantinedCodes))
	for _, code := range snapshot.QuarantinedCodes {
		quarantined[code] = struct{}{}
	}
	for _, code := range changes.Deleted {
		if _, protected := quarantined[code]; protected {
			continue
		}
		deleted = append(deleted, Tombstone{ProductCode: code, Deleted: true})
	}
	sort.SliceStable(deleted, func(i, j int) bool { return deleted[i].ProductCode < deleted[j].ProductCode })

	envelope := &Envelope{
		Schema:           snapshot.Schema,
		EventType:        "update",
		LocalCurrency:    snapshot.LocalCurrency,
		FormulaID:        snapshot.FormulaID,
		Source:           snapshot.Source,
		GeneratedAt:      snapshot.GeneratedAt,
		Products:         products,
		Categories:       cloneCategories(snapshot.Categories),
		ExcludedCodes:    append([]string(nil), snapshot.ExcludedCodes...),
		DeletedCodes:     deleted,
		QuarantinedCodes: append([]string(nil), snapshot.QuarantinedCodes...),
		Warnings:         normalizedWarnings(snapshot.Warnings),
	}
	envelope.EventID = eventID(envelope)
	return envelope
}

func sourceRevision(products []Product, categories []Category, excludedCodes, quarantinedCodes []string) string {
	revision, _ := sourceRevisionContext(context.Background(), products, categories, excludedCodes, quarantinedCodes)
	return revision
}

func sourceRevisionContext(ctx context.Context, products []Product, categories []Category, excludedCodes, quarantinedCodes []string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	material := make([]string, 0, len(products)+len(categories)+len(excludedCodes))
	for _, product := range products {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		material = append(material, product.ProductCode+"="+product.RecordHash)
	}
	for _, category := range categories {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		material = append(material, "category:"+category.CategoryCode+"="+category.RecordHash)
	}
	for _, code := range excludedCodes {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		material = append(material, "excluded="+code)
	}
	if err := stableSortContext(ctx, material, func(left, right string) bool { return left < right }); err != nil {
		return "", err
	}
	for _, code := range quarantinedCodes {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		material = append(material, "quarantined="+code)
	}
	digest := sha256.New()
	for index, value := range material {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if index > 0 {
			_, _ = digest.Write([]byte{'\n'})
		}
		_, _ = digest.Write([]byte(value))
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func eventID(envelope *Envelope) string {
	value, _ := eventIDContext(context.Background(), envelope)
	return value
}

func eventIDContext(ctx context.Context, envelope *Envelope) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	hashes := make([]string, 0, len(envelope.Products))
	for _, product := range envelope.Products {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		hashes = append(hashes, product.ProductCode+"="+product.RecordHash)
	}
	if err := stableSortContext(ctx, hashes, func(left, right string) bool { return left < right }); err != nil {
		return "", err
	}
	categoryHashes := make([]string, 0, len(envelope.Categories))
	for _, category := range envelope.Categories {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		categoryHashes = append(categoryHashes, category.CategoryCode+"="+category.RecordHash)
	}
	if err := stableSortContext(ctx, categoryHashes, func(left, right string) bool { return left < right }); err != nil {
		return "", err
	}

	digest := sha256.New()
	write := func(value string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, _ = digest.Write([]byte(value))
		return nil
	}
	writeJSONString := func(value string) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return write(string(encoded))
	}
	writeStringField := func(prefix, value string) error {
		if err := write(prefix); err != nil {
			return err
		}
		return writeJSONString(value)
	}
	writeStringArray := func(prefix string, values []string) error {
		if err := write(prefix); err != nil {
			return err
		}
		if values == nil {
			return write("null")
		}
		if err := write("["); err != nil {
			return err
		}
		for index, value := range values {
			if index > 0 {
				if err := write(","); err != nil {
					return err
				}
			}
			if err := writeJSONString(value); err != nil {
				return err
			}
		}
		return write("]")
	}

	if err := writeStringField(`{"schema":`, envelope.Schema); err != nil {
		return "", err
	}
	if err := writeStringField(`,"event_type":`, envelope.EventType); err != nil {
		return "", err
	}
	if envelope.LocalCurrency != "" {
		if err := writeStringField(`,"local_currency":`, envelope.LocalCurrency); err != nil {
			return "", err
		}
	}
	if envelope.FormulaID != "" {
		if err := writeStringField(`,"formula_id":`, envelope.FormulaID); err != nil {
			return "", err
		}
	}
	if err := write(`,"source":{"id":`); err != nil {
		return "", err
	}
	if err := writeJSONString(envelope.Source.ID); err != nil {
		return "", err
	}
	if err := writeStringField(`,"dataset":`, envelope.Source.Dataset); err != nil {
		return "", err
	}
	if err := writeStringField(`,"revision":`, envelope.Source.Revision); err != nil {
		return "", err
	}
	if err := writeStringField(`},"generated_at":`, envelope.GeneratedAt); err != nil {
		return "", err
	}
	if err := writeStringArray(`,"products":`, hashes); err != nil {
		return "", err
	}
	if err := writeStringArray(`,"categories":`, categoryHashes); err != nil {
		return "", err
	}
	if err := writeStringArray(`,"excluded_codes":`, envelope.ExcludedCodes); err != nil {
		return "", err
	}
	if len(envelope.DeletedCodes) > 0 {
		if err := write(`,"deleted_codes":[`); err != nil {
			return "", err
		}
		for index, tombstone := range envelope.DeletedCodes {
			if index > 0 {
				if err := write(","); err != nil {
					return "", err
				}
			}
			if err := write(`{"product_code":`); err != nil {
				return "", err
			}
			if err := writeJSONString(tombstone.ProductCode); err != nil {
				return "", err
			}
			if tombstone.Deleted {
				if err := write(`,"deleted":true}`); err != nil {
					return "", err
				}
			} else if err := write(`,"deleted":false}`); err != nil {
				return "", err
			}
		}
		if err := write("]"); err != nil {
			return "", err
		}
	}
	if err := writeStringArray(`,"quarantined_codes":`, envelope.QuarantinedCodes); err != nil {
		return "", err
	}
	if err := write("}"); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func hashBytes(material []byte) string {
	digest := sha256.Sum256(material)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func cloneEnvelope(value *Envelope) *Envelope {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Products = cloneProducts(value.Products)
	copy.Categories = cloneCategories(value.Categories)
	if value.ExcludedCodes != nil {
		copy.ExcludedCodes = append(make([]string, 0, len(value.ExcludedCodes)), value.ExcludedCodes...)
	}
	copy.DeletedCodes = append([]Tombstone(nil), value.DeletedCodes...)
	if value.QuarantinedCodes != nil {
		copy.QuarantinedCodes = append(make([]string, 0, len(value.QuarantinedCodes)), value.QuarantinedCodes...)
	}
	if value.fieldPresence != nil {
		copy.fieldPresence = make(map[string]fieldPresence, len(value.fieldPresence))
		for field, state := range value.fieldPresence {
			copy.fieldPresence[field] = state
		}
	}
	if value.Warnings != nil {
		copy.Warnings = append(make([]string, 0, len(value.Warnings)), value.Warnings...)
	}
	return &copy
}

func cloneCategories(values []Category) []Category {
	result, _ := cloneCategoriesContext(context.Background(), values)
	return result
}

func cloneCategoriesContext(ctx context.Context, values []Category) ([]Category, error) {
	result := make([]Category, 0, len(values))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		copy := value
		copy.fieldPresence = make(map[string]fieldPresence, len(value.fieldPresence))
		for key, state := range value.fieldPresence {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			copy.fieldPresence[key] = state
		}
		if value.Warnings != nil {
			copy.Warnings = make([]string, 0, len(value.Warnings))
			for _, warning := range value.Warnings {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				copy.Warnings = append(copy.Warnings, warning)
			}
		}
		result = append(result, copy)
	}
	return result, nil
}

func cloneProducts(values []Product) []Product {
	result, _ := cloneProductsContext(context.Background(), values)
	return result
}

func cloneProductsContext(ctx context.Context, values []Product) ([]Product, error) {
	result := make([]Product, 0, len(values))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		copy, err := cloneProductContext(ctx, value)
		if err != nil {
			return nil, err
		}
		result = append(result, copy)
	}
	return result, nil
}

func cloneProduct(value Product) Product {
	copy, _ := cloneProductContext(context.Background(), value)
	return copy
}

func cloneProductContext(ctx context.Context, value Product) (Product, error) {
	copy := value
	copy.WarehouseStock = make(map[string]float64, len(value.WarehouseStock))
	for key, stock := range value.WarehouseStock {
		if err := ctx.Err(); err != nil {
			return Product{}, err
		}
		copy.WarehouseStock[key] = stock
	}
	copy.fieldPresence = make(map[string]fieldPresence, len(value.fieldPresence))
	for key, state := range value.fieldPresence {
		if err := ctx.Err(); err != nil {
			return Product{}, err
		}
		copy.fieldPresence[key] = state
	}
	copy.warehouseNulls = make(map[string]bool, len(value.warehouseNulls))
	for key, isNull := range value.warehouseNulls {
		if err := ctx.Err(); err != nil {
			return Product{}, err
		}
		copy.warehouseNulls[key] = isNull
	}
	if value.Warnings != nil {
		copy.Warnings = make([]string, 0, len(value.Warnings))
		for _, warning := range value.Warnings {
			if err := ctx.Err(); err != nil {
				return Product{}, err
			}
			copy.Warnings = append(copy.Warnings, warning)
		}
	}
	return copy, nil
}

// stableSortContext is an iterative stable merge sort with cancellation checks
// inside both merge and copy passes. The standard sort helpers cannot abort an
// in-progress O(N log N) catalog sort after a request is cancelled.
func stableSortContext[T any](ctx context.Context, values []T, less func(left, right T) bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(values) < 2 {
		return nil
	}

	buffer := make([]T, len(values))
	source := values
	target := buffer
	for width := 1; width < len(values); {
		for start := 0; start < len(values); start += 2 * width {
			if err := ctx.Err(); err != nil {
				return err
			}
			middle := min(start+width, len(values))
			end := min(start+2*width, len(values))
			left, right, output := start, middle, start
			for left < middle && right < end {
				if err := ctx.Err(); err != nil {
					return err
				}
				if less(source[right], source[left]) {
					target[output] = source[right]
					right++
				} else {
					target[output] = source[left]
					left++
				}
				output++
			}
			for left < middle {
				if err := ctx.Err(); err != nil {
					return err
				}
				target[output] = source[left]
				left++
				output++
			}
			for right < end {
				if err := ctx.Err(); err != nil {
					return err
				}
				target[output] = source[right]
				right++
				output++
			}
		}
		source, target = target, source
		if width > len(values)/2 {
			width = len(values)
		} else {
			width *= 2
		}
	}
	if len(source) > 0 && &source[0] != &values[0] {
		for index := range source {
			if err := ctx.Err(); err != nil {
				return err
			}
			values[index] = source[index]
		}
	}
	return ctx.Err()
}

func copyRows(rows []map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		result = append(result, copyRow(row))
	}
	return result
}

func copyRow(row map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(row))
	for key, value := range row {
		result[key] = copyValue(value)
	}
	return result
}

func copyValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return copyRow(typed)
	case map[string]float64:
		copy := make(map[string]float64, len(typed))
		for key, item := range typed {
			copy[key] = item
		}
		return copy
	case []interface{}:
		copy := make([]interface{}, len(typed))
		for index, item := range typed {
			copy[index] = copyValue(item)
		}
		return copy
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}
