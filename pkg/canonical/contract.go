package canonical

import (
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
	return newEnvelope(rows, nil, nil, source, sourceID, generatedAt, quarantinedCodes...)
}

func NewEnvelopeWithCategories(rows []Product, categories []Category, source, sourceID string, generatedAt time.Time, quarantinedCodes ...string) *Envelope {
	return newEnvelope(rows, categories, nil, source, sourceID, generatedAt, quarantinedCodes...)
}

func NewCatalogEnvelope(rows []Product, categories []Category, excludedCodes []string, source, sourceID string, generatedAt time.Time, quarantinedCodes ...string) *Envelope {
	return newEnvelope(rows, categories, excludedCodes, source, sourceID, generatedAt, quarantinedCodes...)
}

func newEnvelope(rows []Product, categories []Category, excludedCodes []string, source, sourceID string, generatedAt time.Time, quarantinedCodes ...string) *Envelope {
	if generatedAt.IsZero() {
		generatedAt = time.Now()
	}
	products := cloneProducts(rows)
	sort.SliceStable(products, func(i, j int) bool {
		return products[i].ProductCode < products[j].ProductCode
	})
	categories = cloneCategories(categories)
	sort.SliceStable(categories, func(i, j int) bool {
		return categories[i].CategoryCode < categories[j].CategoryCode
	})
	quarantinedCodes = normalizedWarnings(quarantinedCodes)
	excludedCodes = normalizedWarnings(excludedCodes)
	revision := sourceRevision(products, categories, excludedCodes, quarantinedCodes)
	if strings.TrimSpace(sourceID) == "" {
		sourceID = sourceBaseName(source)
	}
	envelope := &Envelope{
		Schema:           ContractName,
		EventType:        "snapshot",
		LocalCurrency:    LocalCurrency,
		FormulaID:        FormulaID,
		Source:           Source{ID: sourceID, Dataset: sourceBaseName(source), Revision: revision},
		GeneratedAt:      generatedAt.UTC().Format(time.RFC3339Nano),
		Products:         products,
		Categories:       categories,
		ExcludedCodes:    excludedCodes,
		QuarantinedCodes: quarantinedCodes,
		Warnings:         normalizedWarnings(nil),
	}
	for _, code := range quarantinedCodes {
		envelope.Warnings = append(envelope.Warnings, "duplicate_product_code:"+code)
	}
	envelope.EventID = eventID(envelope)
	return envelope
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
	material := make([]string, 0, len(products)+len(categories)+len(excludedCodes))
	for _, product := range products {
		material = append(material, product.ProductCode+"="+product.RecordHash)
	}
	for _, category := range categories {
		material = append(material, "category:"+category.CategoryCode+"="+category.RecordHash)
	}
	for _, code := range excludedCodes {
		material = append(material, "excluded="+code)
	}
	sort.Strings(material)
	for _, code := range quarantinedCodes {
		material = append(material, "quarantined="+code)
	}
	return hashBytes([]byte(strings.Join(material, "\n")))
}

func eventID(envelope *Envelope) string {
	type identity struct {
		Schema           string      `json:"schema"`
		EventType        string      `json:"event_type"`
		LocalCurrency    string      `json:"local_currency"`
		FormulaID        string      `json:"formula_id"`
		Source           Source      `json:"source"`
		GeneratedAt      string      `json:"generated_at"`
		Products         []string    `json:"products"`
		Categories       []string    `json:"categories"`
		ExcludedCodes    []string    `json:"excluded_codes"`
		DeletedCodes     []Tombstone `json:"deleted_codes,omitempty"`
		QuarantinedCodes []string    `json:"quarantined_codes"`
	}
	hashes := make([]string, 0, len(envelope.Products))
	for _, product := range envelope.Products {
		hashes = append(hashes, product.ProductCode+"="+product.RecordHash)
	}
	sort.Strings(hashes)
	categoryHashes := make([]string, 0, len(envelope.Categories))
	for _, category := range envelope.Categories {
		categoryHashes = append(categoryHashes, category.CategoryCode+"="+category.RecordHash)
	}
	sort.Strings(categoryHashes)
	material, _ := json.Marshal(identity{
		Schema: envelope.Schema, EventType: envelope.EventType,
		LocalCurrency: envelope.LocalCurrency, FormulaID: envelope.FormulaID,
		Source: envelope.Source, GeneratedAt: envelope.GeneratedAt, Products: hashes, Categories: categoryHashes, ExcludedCodes: envelope.ExcludedCodes,
		DeletedCodes: envelope.DeletedCodes, QuarantinedCodes: envelope.QuarantinedCodes,
	})
	return hashBytes(material)
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
	copy.ExcludedCodes = append([]string(nil), value.ExcludedCodes...)
	copy.DeletedCodes = append([]Tombstone(nil), value.DeletedCodes...)
	copy.QuarantinedCodes = append([]string(nil), value.QuarantinedCodes...)
	if value.Warnings != nil {
		copy.Warnings = append(make([]string, 0, len(value.Warnings)), value.Warnings...)
	}
	return &copy
}

func cloneCategories(values []Category) []Category {
	result := make([]Category, 0, len(values))
	for _, value := range values {
		copy := value
		copy.fieldPresence = make(map[string]fieldPresence, len(value.fieldPresence))
		for key, state := range value.fieldPresence {
			copy.fieldPresence[key] = state
		}
		if value.Warnings != nil {
			copy.Warnings = append(make([]string, 0, len(value.Warnings)), value.Warnings...)
		}
		result = append(result, copy)
	}
	return result
}

func cloneProducts(values []Product) []Product {
	result := make([]Product, 0, len(values))
	for _, value := range values {
		result = append(result, cloneProduct(value))
	}
	return result
}

func cloneProduct(value Product) Product {
	copy := value
	copy.WarehouseStock = make(map[string]float64, len(value.WarehouseStock))
	for key, stock := range value.WarehouseStock {
		copy.WarehouseStock[key] = stock
	}
	copy.fieldPresence = make(map[string]fieldPresence, len(value.fieldPresence))
	for key, state := range value.fieldPresence {
		copy.fieldPresence[key] = state
	}
	copy.warehouseNulls = make(map[string]bool, len(value.warehouseNulls))
	for key, isNull := range value.warehouseNulls {
		copy.warehouseNulls[key] = isNull
	}
	if value.Warnings != nil {
		copy.Warnings = append(make([]string, 0, len(value.Warnings)), value.Warnings...)
	}
	return copy
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
