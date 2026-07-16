package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/recorddiff"
)

const (
	ContractName    = "digitalogic.product-sync"
	ContractVersion = "1.0"
	FormulaVersion  = "landed_price_v1"
	FormulaRevision = "1.0.0"
	LocalCurrency   = "IRT"
)

type Source struct {
	ID       string `json:"id"`
	Dataset  string `json:"dataset"`
	Revision string `json:"revision"`
}

type Tombstone struct {
	ProductCode string `json:"product_code"`
	Deleted     bool   `json:"deleted"`
}

type Envelope struct {
	Schema           string      `json:"schema"`
	SchemaVersion    string      `json:"schema_version"`
	Event            string      `json:"event"`
	EventType        string      `json:"event_type"`
	EventID          string      `json:"event_id"`
	LocalCurrency    string      `json:"local_currency"`
	FormulaID        string      `json:"formula_id"`
	FormulaRevision  string      `json:"formula_revision"`
	FormulaVersion   string      `json:"formula_version"`
	Source           Source      `json:"source"`
	GeneratedAt      string      `json:"generated_at"`
	Products         []Product   `json:"products"`
	DeletedCodes     []Tombstone `json:"deleted_codes,omitempty"`
	QuarantinedCodes []string    `json:"quarantined_codes,omitempty"`
	Warnings         []string    `json:"warnings,omitempty"`
}

func NewEnvelope(rows []Product, source, sourceID string, generatedAt time.Time, quarantinedCodes ...string) *Envelope {
	if generatedAt.IsZero() {
		generatedAt = time.Now()
	}
	products := cloneProducts(rows)
	sort.SliceStable(products, func(i, j int) bool {
		return products[i].ProductCode < products[j].ProductCode
	})
	quarantinedCodes = normalizedWarnings(quarantinedCodes)
	revision := sourceRevision(products, quarantinedCodes)
	if strings.TrimSpace(sourceID) == "" {
		sourceID = sourceBaseName(source)
	}
	envelope := &Envelope{
		Schema:           ContractName,
		SchemaVersion:    ContractVersion,
		Event:            ContractName,
		EventType:        "snapshot",
		LocalCurrency:    LocalCurrency,
		FormulaID:        FormulaVersion,
		FormulaRevision:  FormulaRevision,
		FormulaVersion:   FormulaVersion,
		Source:           Source{ID: sourceID, Dataset: sourceBaseName(source), Revision: revision},
		GeneratedAt:      generatedAt.UTC().Format(time.RFC3339Nano),
		Products:         products,
		QuarantinedCodes: quarantinedCodes,
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
		SchemaVersion:    snapshot.SchemaVersion,
		Event:            snapshot.Event,
		EventType:        "update",
		LocalCurrency:    snapshot.LocalCurrency,
		FormulaID:        snapshot.FormulaID,
		FormulaRevision:  snapshot.FormulaRevision,
		FormulaVersion:   snapshot.FormulaVersion,
		Source:           snapshot.Source,
		GeneratedAt:      snapshot.GeneratedAt,
		Products:         products,
		DeletedCodes:     deleted,
		QuarantinedCodes: append([]string(nil), snapshot.QuarantinedCodes...),
		Warnings:         append([]string(nil), snapshot.Warnings...),
	}
	envelope.EventID = eventID(envelope)
	return envelope
}

func sourceRevision(products []Product, quarantinedCodes []string) string {
	material := make([]string, 0, len(products))
	for _, product := range products {
		material = append(material, product.ProductCode+"="+product.RecordHash)
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
		SchemaVersion    string      `json:"schema_version"`
		EventType        string      `json:"event_type"`
		LocalCurrency    string      `json:"local_currency"`
		FormulaID        string      `json:"formula_id"`
		FormulaRevision  string      `json:"formula_revision"`
		Source           Source      `json:"source"`
		Products         []string    `json:"products"`
		DeletedCodes     []Tombstone `json:"deleted_codes,omitempty"`
		QuarantinedCodes []string    `json:"quarantined_codes,omitempty"`
	}
	hashes := make([]string, 0, len(envelope.Products))
	for _, product := range envelope.Products {
		hashes = append(hashes, product.ProductCode+"="+product.RecordHash)
	}
	sort.Strings(hashes)
	material, _ := json.Marshal(identity{
		Schema: envelope.Schema, SchemaVersion: envelope.SchemaVersion, EventType: envelope.EventType,
		LocalCurrency: envelope.LocalCurrency, FormulaID: envelope.FormulaID, FormulaRevision: envelope.FormulaRevision,
		Source: envelope.Source, Products: hashes, DeletedCodes: envelope.DeletedCodes, QuarantinedCodes: envelope.QuarantinedCodes,
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
	copy.DeletedCodes = append([]Tombstone(nil), value.DeletedCodes...)
	copy.QuarantinedCodes = append([]string(nil), value.QuarantinedCodes...)
	if value.Warnings != nil {
		copy.Warnings = append(make([]string, 0, len(value.Warnings)), value.Warnings...)
	}
	return &copy
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
