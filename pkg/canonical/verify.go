package canonical

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

const (
	// MaxSnapshotBytes matches the receiver boundary for a single product-sync
	// request and keeps verification memory use bounded.
	MaxSnapshotBytes      = 8 * 1024 * 1024
	maxEnvelopeProducts   = 10000
	maxEnvelopeCategories = 10000
	maxIdentityCodeLength = 191
	maxWarningLength      = 255
)

// VerificationSummary is a compact, non-mutating description of a verified
// canonical snapshot.
type VerificationSummary struct {
	Schema           string
	EventType        string
	EventID          string
	SourceID         string
	SourceDataset    string
	SourceRevision   string
	GeneratedAt      string
	Products         int
	Categories       int
	ExcludedCodes    int
	QuarantinedCodes int
	Warnings         int
}

// VerifySnapshotJSON decodes a strict product-sync snapshot and verifies every
// identity that can be derived without receiver state. Unknown fields, malformed
// sparse records, duplicate Codes, and any record/source/event hash mismatch
// fail closed.
func VerifySnapshotJSON(data []byte) (*Envelope, VerificationSummary, error) {
	if len(data) > MaxSnapshotBytes {
		return nil, VerificationSummary{}, fmt.Errorf("canonical snapshot exceeds the %d-byte limit", MaxSnapshotBytes)
	}
	if err := rejectDuplicateJSONFields(data); err != nil {
		return nil, VerificationSummary{}, fmt.Errorf("decode canonical snapshot: %w", err)
	}
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, VerificationSummary{}, fmt.Errorf("decode canonical snapshot: %w", err)
	}
	if err := ValidateEnvelopeIdentity(&envelope); err != nil {
		return nil, VerificationSummary{}, err
	}
	return &envelope, verificationSummary(&envelope), nil
}

func rejectDuplicateJSONFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple top-level JSON values are not allowed")
		}
		return err
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds the verification limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key must be a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("JSON object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("JSON array is not closed")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

// ValidateEnvelopeIdentity verifies a self-contained snapshot. Update events
// require the receiver's previous state to validate their resulting source
// revision, so they are deliberately rejected rather than partially trusted.
func ValidateEnvelopeIdentity(envelope *Envelope) error {
	if envelope == nil {
		return fmt.Errorf("canonical snapshot is nil")
	}
	if envelope.Schema != ContractName {
		return fmt.Errorf("schema must be %q", ContractName)
	}
	if envelope.EventType != "snapshot" {
		return fmt.Errorf("event_type must be snapshot for standalone verification")
	}
	if !validSHA256Identity(envelope.EventID) {
		return fmt.Errorf("event_id must be a lowercase sha256 identity")
	}

	hasCurrency, err := envelopeOptionalStringPresent(envelope, "local_currency", envelope.LocalCurrency)
	if err != nil {
		return err
	}
	hasFormula, err := envelopeOptionalStringPresent(envelope, "formula_id", envelope.FormulaID)
	if err != nil {
		return err
	}
	if hasCurrency != hasFormula {
		return fmt.Errorf("local_currency and formula_id must be present together")
	}
	if hasCurrency && envelope.LocalCurrency != LocalCurrency {
		return fmt.Errorf("local_currency must be %q when present", LocalCurrency)
	}
	if hasFormula && envelope.FormulaID != FormulaID {
		return fmt.Errorf("formula_id must be %q when present", FormulaID)
	}

	if err := validateSourceIdentity(envelope.Source); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339Nano, envelope.GeneratedAt); err != nil {
		return fmt.Errorf("generated_at must be RFC3339: %w", err)
	}
	if envelope.Products == nil {
		return fmt.Errorf("products must be an array")
	}
	if envelope.Categories == nil {
		return fmt.Errorf("categories must be an array")
	}
	if envelope.ExcludedCodes == nil {
		return fmt.Errorf("excluded_codes must be an array")
	}
	if envelope.QuarantinedCodes == nil {
		return fmt.Errorf("quarantined_codes must be an array")
	}
	if envelope.Warnings == nil {
		return fmt.Errorf("warnings must be an array")
	}
	if len(envelope.Products) > maxEnvelopeProducts {
		return fmt.Errorf("products exceeds the %d-record limit", maxEnvelopeProducts)
	}
	if len(envelope.Categories) > maxEnvelopeCategories {
		return fmt.Errorf("categories exceeds the %d-record limit", maxEnvelopeCategories)
	}
	if state, present := envelope.fieldPresence["deleted_codes"]; present && state == fieldNull {
		return fmt.Errorf("deleted_codes must be an array when present")
	}
	if len(envelope.DeletedCodes) != 0 {
		return fmt.Errorf("deleted_codes is only valid on update events")
	}

	if err := validateCanonicalStringSet("excluded_codes", envelope.ExcludedCodes, maxIdentityCodeLength); err != nil {
		return err
	}
	if err := validateCanonicalStringSet("quarantined_codes", envelope.QuarantinedCodes, maxIdentityCodeLength); err != nil {
		return err
	}
	if err := validateCanonicalStringSet("warnings", envelope.Warnings, maxWarningLength); err != nil {
		return err
	}

	previousProductCode := ""
	for index, product := range envelope.Products {
		if err := validateProductIdentity(product, index); err != nil {
			return err
		}
		if index > 0 && product.ProductCode <= previousProductCode {
			return fmt.Errorf("products must be sorted by unique product_code")
		}
		previousProductCode = product.ProductCode
	}

	categoryCodes := make(map[string]Category, len(envelope.Categories))
	previousCategoryCode := ""
	for index, category := range envelope.Categories {
		if err := validateCategoryIdentity(category, index); err != nil {
			return err
		}
		if index > 0 && category.CategoryCode <= previousCategoryCode {
			return fmt.Errorf("categories must be sorted by unique category_code")
		}
		previousCategoryCode = category.CategoryCode
		categoryCodes[category.CategoryCode] = category
	}
	for _, category := range envelope.Categories {
		if category.ParentCode == "" {
			if category.Depth != 1 {
				return fmt.Errorf("category %q depth must be 1 at the root", category.CategoryCode)
			}
			continue
		}
		parent, exists := categoryCodes[category.ParentCode]
		if !exists {
			return fmt.Errorf("category %q references missing parent %q", category.CategoryCode, category.ParentCode)
		}
		if category.Depth != parent.Depth+1 {
			return fmt.Errorf("category %q depth must be exactly one greater than its parent", category.CategoryCode)
		}
	}

	excluded := make(map[string]struct{}, len(envelope.ExcludedCodes))
	for _, code := range envelope.ExcludedCodes {
		excluded[code] = struct{}{}
		if _, exists := categoryCodes[code]; exists {
			return fmt.Errorf("code %q cannot be both a category and excluded", code)
		}
	}
	quarantined := make(map[string]struct{}, len(envelope.QuarantinedCodes))
	for _, code := range envelope.QuarantinedCodes {
		quarantined[code] = struct{}{}
	}
	for _, product := range envelope.Products {
		if _, exists := categoryCodes[product.ProductCode]; exists {
			return fmt.Errorf("code %q cannot be both a product and category", product.ProductCode)
		}
		if _, exists := excluded[product.ProductCode]; exists {
			return fmt.Errorf("code %q cannot be both a product and excluded", product.ProductCode)
		}
		if _, exists := quarantined[product.ProductCode]; exists {
			return fmt.Errorf("code %q cannot be both a product and quarantined", product.ProductCode)
		}
		if product.CategoryCode != "" {
			if _, exists := categoryCodes[product.CategoryCode]; !exists {
				return fmt.Errorf("product %q references missing category %q", product.ProductCode, product.CategoryCode)
			}
		}
	}
	expectedRevision := sourceRevision(envelope.Products, envelope.Categories, envelope.ExcludedCodes, envelope.QuarantinedCodes)
	if envelope.Source.Revision != expectedRevision {
		return fmt.Errorf("source.revision mismatch: expected %s", expectedRevision)
	}
	expectedEventID := eventID(envelope)
	if envelope.EventID != expectedEventID {
		return fmt.Errorf("event_id mismatch: expected %s", expectedEventID)
	}
	return nil
}

func verificationSummary(envelope *Envelope) VerificationSummary {
	return VerificationSummary{
		Schema:           envelope.Schema,
		EventType:        envelope.EventType,
		EventID:          envelope.EventID,
		SourceID:         envelope.Source.ID,
		SourceDataset:    envelope.Source.Dataset,
		SourceRevision:   envelope.Source.Revision,
		GeneratedAt:      envelope.GeneratedAt,
		Products:         len(envelope.Products),
		Categories:       len(envelope.Categories),
		ExcludedCodes:    len(envelope.ExcludedCodes),
		QuarantinedCodes: len(envelope.QuarantinedCodes),
		Warnings:         len(envelope.Warnings),
	}
}

func envelopeOptionalStringPresent(envelope *Envelope, field, value string) (bool, error) {
	if state, tracked := envelope.fieldPresence[field]; tracked {
		if state == fieldNull {
			return false, fmt.Errorf("%s must be a string when present", field)
		}
		return true, nil
	}
	return value != "", nil
}

func validateSourceIdentity(source Source) error {
	for _, item := range []struct {
		field string
		value string
	}{{"id", source.ID}, {"dataset", source.Dataset}} {
		field, value := item.field, item.value
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("source.%s must be a non-empty trimmed string", field)
		}
		if len(value) > maxIdentityCodeLength {
			return fmt.Errorf("source.%s exceeds %d bytes", field, maxIdentityCodeLength)
		}
	}
	if !validSHA256Identity(source.Revision) {
		return fmt.Errorf("source.revision must be a lowercase sha256 identity")
	}
	return nil
}

func validateProductIdentity(product Product, index int) error {
	path := fmt.Sprintf("products[%d]", index)
	if err := validateCode(path+".product_code", product.ProductCode); err != nil {
		return err
	}
	if product.CategoryCode != "" {
		if err := validateCode(path+".category_code", product.CategoryCode); err != nil {
			return err
		}
	}
	if product.Warnings == nil {
		return fmt.Errorf("%s.warnings must be an array", path)
	}
	if err := validateCanonicalStringSet(path+".warnings", product.Warnings, maxWarningLength); err != nil {
		return err
	}
	if (product.ForeignCurrency != "" || product.presence("foreign_currency") == fieldValue) && product.ForeignCurrency != "CNY" {
		return fmt.Errorf("%s.foreign_currency must be CNY or null when present", path)
	}
	if product.ShippingMethodID != "" && (strings.TrimSpace(product.ShippingMethodID) != product.ShippingMethodID || len(product.ShippingMethodID) > maxIdentityCodeLength) {
		return fmt.Errorf("%s.shipping_method_id must be a trimmed string within the code limit or null", path)
	}

	hasShippingPrice := product.ShippingPricePerKg != nil || product.presence("shipping_price_per_kg") != fieldAbsent
	hasShippingCurrency := product.ShippingPricePerKgCurrency != "" || product.presence("shipping_price_per_kg_currency") != fieldAbsent
	if hasShippingPrice != hasShippingCurrency {
		return fmt.Errorf("%s shipping_price_per_kg and currency must be present together", path)
	}
	if (product.ShippingPricePerKgCurrency != "" || product.presence("shipping_price_per_kg_currency") == fieldValue) && product.ShippingPricePerKgCurrency != "CNY" && product.ShippingPricePerKgCurrency != "IRR" {
		return fmt.Errorf("%s.shipping_price_per_kg_currency must be CNY, IRR, or null", path)
	}

	for field, value := range map[string]*float64{
		"sale_price_source":     product.SalePriceSource,
		"purchase_price_source": product.PurchasePriceSource,
		"total_stock":           product.TotalStock,
		"minimum_stock":         product.MinimumStock,
	} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return fmt.Errorf("%s.%s must be finite", path, field)
		}
	}
	for warehouse, stock := range product.WarehouseStock {
		if warehouse == "" || strings.TrimSpace(warehouse) != warehouse || math.IsNaN(stock) || math.IsInf(stock, 0) {
			return fmt.Errorf("%s.warehouse_stock must map non-empty trimmed keys to finite numbers or null", path)
		}
	}
	for warehouse := range product.warehouseNulls {
		if warehouse == "" || strings.TrimSpace(warehouse) != warehouse {
			return fmt.Errorf("%s.warehouse_stock contains an invalid null-valued key", path)
		}
	}
	if err := validatePositiveDecimal(path+".foreign_price", product.ForeignPrice); err != nil {
		return err
	}
	if err := validatePositiveDecimal(path+".weight_grams", product.WeightGrams); err != nil {
		return err
	}
	if err := validatePositiveDecimal(path+".shipping_price_per_kg", product.ShippingPricePerKg); err != nil {
		return err
	}
	if err := validatePositiveDecimal(path+".irt_per_cny", product.IRTPerCNY); err != nil {
		return err
	}
	if product.MarkupPercent != nil {
		value, ok := product.MarkupPercent.Rat()
		if !ok || value.Sign() < 0 {
			return fmt.Errorf("%s.markup_percent must be a non-negative decimal", path)
		}
	}
	roundingDigitsPresent := product.PriceRoundingDigits != nil ||
		product.presence("price_rounding_digits") != fieldAbsent
	roundingModePresent := product.PriceRoundingMode != "" ||
		product.presence("price_rounding_mode") != fieldAbsent
	if product.presence("price_rounding_digits") == fieldNull {
		if roundingModePresent {
			return fmt.Errorf("%s.price_rounding_mode must be omitted when price_rounding_digits is null", path)
		}
	} else if roundingDigitsPresent {
		if product.PriceRoundingDigits == nil ||
			*product.PriceRoundingDigits < pricingcatalog.MinimumRoundDigits ||
			*product.PriceRoundingDigits > pricingcatalog.MaximumRoundDigits {
			return fmt.Errorf("%s.price_rounding_digits must be an integer from %d through %d or explicit null", path, pricingcatalog.MinimumRoundDigits, pricingcatalog.MaximumRoundDigits)
		}
		if product.PriceRoundingMode != pricingcatalog.RoundingModeHalfUp ||
			product.presence("price_rounding_mode") == fieldNull {
			return fmt.Errorf("%s.price_rounding_mode must be nearest_half_up when rounding digits are present", path)
		}
	} else if roundingModePresent {
		return fmt.Errorf("%s.price_rounding_mode requires price_rounding_digits", path)
	}
	if product.presence("final_price") == fieldNull {
		return fmt.Errorf("%s.final_price must be omitted when unavailable, not null", path)
	}
	if product.FinalPrice != nil && *product.FinalPrice < 0 {
		return fmt.Errorf("%s.final_price must be a non-negative integer", path)
	}
	if product.FinalPrice != nil && (product.PriceRoundingDigits == nil ||
		product.PriceRoundingMode != pricingcatalog.RoundingModeHalfUp) {
		return fmt.Errorf("%s.final_price requires rounding provenance", path)
	}
	if product.FinalPrice != nil &&
		product.ForeignPrice != nil &&
		product.WeightGrams != nil &&
		product.ShippingPricePerKg != nil &&
		product.MarkupPercent != nil &&
		product.IRTPerCNY != nil &&
		product.PriceRoundingDigits != nil {
		expected, err := LandedPrice(
			product.WeightGrams.String(),
			product.ShippingPricePerKg.String(),
			product.ShippingPricePerKgCurrency,
			product.ForeignPrice.String(),
			product.MarkupPercent.String(),
			product.IRTPerCNY.String(),
			*product.PriceRoundingDigits,
		)
		if err != nil {
			return fmt.Errorf("%s.final_price cannot be recomputed: %w", path, err)
		}
		if *product.FinalPrice != expected {
			return fmt.Errorf("%s.final_price mismatch: expected %d", path, expected)
		}
	}
	if !validSHA256Identity(product.RecordHash) {
		return fmt.Errorf("%s.record_hash must be a lowercase sha256 identity", path)
	}
	expected := recordHash(product)
	if product.RecordHash != expected {
		return fmt.Errorf("%s.record_hash mismatch: expected %s", path, expected)
	}
	return nil
}

func validateCategoryIdentity(category Category, index int) error {
	path := fmt.Sprintf("categories[%d]", index)
	if err := validateCode(path+".category_code", category.CategoryCode); err != nil {
		return err
	}
	if category.ParentCode != "" {
		if err := validateCode(path+".parent_code", category.ParentCode); err != nil {
			return err
		}
		if category.ParentCode == category.CategoryCode {
			return fmt.Errorf("%s.parent_code must differ from category_code", path)
		}
	}
	if category.Depth <= 0 {
		return fmt.Errorf("%s.depth must be a positive integer", path)
	}
	if category.Name == "" && category.fieldPresence["name"] == fieldAbsent {
		return fmt.Errorf("%s.name must be present as a string or explicit null", path)
	}
	if category.Warnings == nil {
		return fmt.Errorf("%s.warnings must be an array", path)
	}
	if err := validateCanonicalStringSet(path+".warnings", category.Warnings, maxWarningLength); err != nil {
		return err
	}
	if !validSHA256Identity(category.RecordHash) {
		return fmt.Errorf("%s.record_hash must be a lowercase sha256 identity", path)
	}
	expected := categoryRecordHash(category)
	if category.RecordHash != expected {
		return fmt.Errorf("%s.record_hash mismatch: expected %s", path, expected)
	}
	return nil
}

func validateCode(path, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must be a non-empty trimmed string", path)
	}
	if len(value) > maxIdentityCodeLength {
		return fmt.Errorf("%s exceeds %d bytes", path, maxIdentityCodeLength)
	}
	return nil
}

func validateCanonicalStringSet(path string, values []string, maxLength int) error {
	previous := ""
	for index, value := range values {
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("%s[%d] must be a non-empty trimmed string", path, index)
		}
		if len(value) > maxLength {
			return fmt.Errorf("%s[%d] exceeds %d bytes", path, index, maxLength)
		}
		if index > 0 && value <= previous {
			return fmt.Errorf("%s must contain sorted unique strings", path)
		}
		previous = value
	}
	return nil
}

func validSHA256Identity(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	digest := value[len("sha256:"):]
	if digest != strings.ToLower(digest) {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == 32
}

func validatePositiveDecimal(path string, value *pricingcatalog.Decimal) error {
	if value == nil {
		return nil
	}
	decimal, ok := value.Rat()
	if !ok || decimal.Sign() <= 0 {
		return fmt.Errorf("%s must be a positive decimal", path)
	}
	return nil
}
