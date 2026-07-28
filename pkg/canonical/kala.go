package canonical

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/naming"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"golang.org/x/text/unicode/norm"
)

var (
	numberPattern       = regexp.MustCompile(`[-+]?[0-9]+(?:[.,٫][0-9]+)?`)
	weightValueUnit     = regexp.MustCompile(`(?i)([-+]?[0-9]+(?:[.,٫][0-9]+)?)\s*(میلی\s*(?:گرم|غرام)|کیلو(?:\s*(?:گرم|غرام))?|(?:گرم|غرام)|milligrams?|kilograms?|grams?|mg|kg|g)`)
	weightUnitValue     = regexp.MustCompile(`(?i)(میلی\s*(?:گرم|غرام)|کیلو(?:\s*(?:گرم|غرام))?|(?:گرم|غرام)|milligrams?|kilograms?|grams?|mg|kg|g)\s*([-+]?[0-9]+(?:[.,٫][0-9]+)?)`)
	weightUnitPresent   = regexp.MustCompile(`(?i)(میلی\s*(?:گرم|غرام)|کیلو(?:\s*(?:گرم|غرام))?|(?:گرم|غرام)|\b(?:milligrams?|kilograms?|grams?|mg|kg|g)\b)`)
	perPackagePattern   = regexp.MustCompile(`(?:هر|بسته)\s*([0-9]+(?:[.,٫][0-9]+)?)\s*(?:عدد|عددی)`)
	halfKilogramPattern = regexp.MustCompile(`نیم\s*کیلو(?:\s*(?:گرم|غرام))?`)
	standaloneZero      = regexp.MustCompile(`(^|[\s|;,_-])0+(?:[.,]0+)?($|[\s|;,_-])`)
)

var reservedNonMerchandiseCodes = map[string]struct{}{
	"117001": {}, // Yuan remittance accounting row.
	"999010": {}, // Freight charge.
	"999222": {}, // Decimal remainder.
	"999332": {}, // Service.
	"999888": {}, // Purchasing-service overhead.
	"999991": {}, // Tax and duties.
	"999993": {}, // Consolidated duties.
	"999994": {}, // Legacy placeholder.
}

const (
	PriceSourceKindForeign    = "foreign_price"
	PriceSourceKindPartner    = "partner_price"
	PriceSourceKindSaleDirect = "sale_price_direct"
)

type Product struct {
	ProductCode                string                  `json:"product_code"`
	CategoryCode               string                  `json:"category_code"`
	Name                       string                  `json:"name"`
	Serial                     string                  `json:"serial"`
	Unit                       string                  `json:"unit"`
	SalePriceSource            *float64                `json:"sale_price_source"`
	PartnerPriceSource         *pricingcatalog.Decimal `json:"partner_price_source"`
	PurchasePriceSource        *float64                `json:"purchase_price_source"`
	WarehouseStock             map[string]float64      `json:"warehouse_stock"`
	TotalStock                 *float64                `json:"total_stock"`
	MinimumStock               *float64                `json:"minimum_stock"`
	ForeignCurrency            string                  `json:"foreign_currency"`
	ForeignPrice               *pricingcatalog.Decimal `json:"foreign_price"`
	WeightGrams                *pricingcatalog.Decimal `json:"weight_grams"`
	Location                   string                  `json:"location"`
	ShippingMethodID           string                  `json:"shipping_method_id,omitempty"`
	ShippingPricePerKg         *pricingcatalog.Decimal `json:"shipping_price_per_kg,omitempty"`
	ShippingPricePerKgCurrency string                  `json:"shipping_price_per_kg_currency,omitempty"`
	MarkupPercent              *pricingcatalog.Decimal `json:"markup_percent"`
	IRTPerCNY                  *pricingcatalog.Decimal `json:"irt_per_cny"`
	PricingCatalogRevision     string                  `json:"pricing_catalog_revision"`
	PricingCatalogStatus       string                  `json:"pricing_catalog_status"`
	CurrencyEffectiveDate      string                  `json:"currency_effective_date"`
	PriceSourceAmount          *pricingcatalog.Decimal `json:"price_source_amount"`
	PriceSourceCurrency        string                  `json:"price_source_currency"`
	PriceSourceKind            string                  `json:"price_source_kind"`
	PriceRoundingDigits        *int                    `json:"price_rounding_digits"`
	PriceRoundingMode          string                  `json:"price_rounding_mode"`
	FinalPrice                 *int64                  `json:"final_price"`
	SourceUpdatedAt            string                  `json:"source_updated_at"`
	Warnings                   []string                `json:"warnings"`
	RecordHash                 string                  `json:"record_hash,omitempty"`
	fieldPresence              map[string]fieldPresence
	warehouseNulls             map[string]bool
	integrationActive          bool
}

type fieldPresence uint8

const (
	fieldAbsent fieldPresence = iota
	fieldValue
	fieldNull
)

// Category is the transformed catalog hierarchy carried beside products.
// Patris category/header rows are structural records, not sellable products;
// keeping a separate typed shape prevents zero-stock headers from crossing the
// product boundary while preserving their names and hierarchy for consumers.
type Category struct {
	CategoryCode  string   `json:"category_code"`
	Name          string   `json:"name"`
	ParentCode    string   `json:"parent_code"`
	Depth         int      `json:"depth"`
	Warnings      []string `json:"warnings"`
	RecordHash    string   `json:"record_hash,omitempty"`
	fieldPresence map[string]fieldPresence
}

func Transform(ctx context.Context, rows []map[string]interface{}, source string, cfg Config, provider pricingcatalog.Provider, generatedAt time.Time) ([]map[string]interface{}, *Envelope) {
	products, envelope, _ := TransformContext(ctx, rows, source, cfg, provider, generatedAt)
	return products, envelope
}

// TransformContext builds the canonical snapshot while cooperatively honoring
// cancellation throughout classification, category construction, pricing
// dispatch, hashing, and row materialization. Transform remains the compatible
// wrapper for existing unbounded callers.
func TransformContext(ctx context.Context, rows []map[string]interface{}, source string, cfg Config, provider pricingcatalog.Provider, generatedAt time.Time) ([]map[string]interface{}, *Envelope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	normalizedPricing := pricingcatalog.Normalize(cfg.Pricing)
	integrationActive := pricingcatalog.Configured(normalizedPricing)
	if provider == nil {
		provider = pricingcatalog.NewProvider(normalizedPricing)
	}
	products := make([]Product, 0, len(rows))
	codeCounts := make(map[string]int, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if code := codeString(firstValue(row, "product_code", "code", "Code")); code != "" {
			codeCounts[code]++
		}
	}
	duplicateCodes := make([]string, 0)
	for code, count := range codeCounts {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if count > 1 {
			duplicateCodes = append(duplicateCodes, code)
		}
	}
	sort.Strings(duplicateCodes)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	categoryCodes, excludedCodes, ambiguousCodes, err := classifyKalaRowsContext(ctx, rows, codeCounts)
	if err != nil {
		return nil, nil, err
	}
	quarantined := normalizedWarnings(append(append([]string{}, duplicateCodes...), ambiguousCodes...))
	categories, err := parseKalaCategoriesContext(ctx, rows, codeCounts, categoryCodes)
	if err != nil {
		return nil, nil, err
	}
	categoryByCode := make(map[string]Category, len(categories))
	for _, category := range categories {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		categoryByCode[category.CategoryCode] = category
	}
	quarantineSet := stringSet(quarantined)
	excludedSet := stringSet(excludedCodes)
	eligible := make([]int, 0, len(rows))
	for index, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		code := codeString(firstValue(row, "product_code", "code", "Code"))
		if code != "" && codeCounts[code] == 1 && !categoryCodes[code] && !quarantineSet[code] && !excludedSet[code] {
			eligible = append(eligible, index)
		}
	}
	if prefetcher, ok := provider.(pricingcatalog.Prefetcher); ok && len(eligible) > 0 {
		codes := make([]string, 0, len(eligible))
		for _, index := range eligible {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			codes = append(codes, codeString(firstValue(rows[index], "product_code", "code", "Code")))
		}
		provider = prefetcher.Prefetch(ctx, codes)
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
	}
	parsedProducts := make([]Product, len(rows))
	workers := 1
	if normalizedPricing.Mode == pricingcatalog.ModeDigitalogic {
		workers = normalizedPricing.Digitalogic.MaxConcurrency
		if workers > len(eligible) {
			workers = len(eligible)
		}
	}
	if workers <= 1 {
		for _, index := range eligible {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			parsedProducts[index] = parseKalaProduct(ctx, rows[index], provider, integrationActive, normalizedPricing.UseSalePriceDirectFallback)
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
	} else {
		jobs := make(chan int)
		var wait sync.WaitGroup
		wait.Add(workers)
		for worker := 0; worker < workers; worker++ {
			go func() {
				defer wait.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case index, ok := <-jobs:
						if !ok {
							return
						}
						product := parseKalaProduct(ctx, rows[index], provider, integrationActive, normalizedPricing.UseSalePriceDirectFallback)
						if ctx.Err() != nil {
							return
						}
						parsedProducts[index] = product
					}
				}
			}()
		}
	dispatch:
		for _, index := range eligible {
			select {
			case <-ctx.Done():
				break dispatch
			case jobs <- index:
			}
		}
		close(jobs)
		wait.Wait()
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
	}
	for _, product := range parsedProducts {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if product.ProductCode == "" {
			continue
		}
		if codeCounts[product.ProductCode] > 1 {
			continue
		}
		product.CategoryCode, err = longestCategoryPrefixContext(ctx, product.ProductCode, categoryByCode)
		if err != nil {
			return nil, nil, err
		}
		product.RecordHash = recordHash(product)
		products = append(products, product)
	}
	sort.SliceStable(products, func(i, j int) bool {
		return products[i].ProductCode < products[j].ProductCode
	})
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	envelope, err := newEnvelopeBaseContext(ctx, products, categories, excludedCodes, source, cfg.SourceID, generatedAt, quarantined...)
	if err != nil {
		return nil, nil, err
	}
	if !integrationActive {
		envelope.LocalCurrency = ""
		envelope.FormulaID = ""
	}
	envelope.Warnings = nil
	for _, code := range duplicateCodes {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		envelope.Warnings = append(envelope.Warnings, "duplicate_product_code:"+code)
	}
	for _, code := range ambiguousCodes {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		envelope.Warnings = append(envelope.Warnings, "ambiguous_catalog_record:"+code)
	}
	envelope.Warnings, err = normalizedWarningsContext(ctx, envelope.Warnings)
	if err != nil {
		return nil, nil, err
	}
	envelope.EventID, err = eventIDContext(ctx, envelope)
	if err != nil {
		return nil, nil, err
	}
	productRows, err := ProductsToRowsContext(ctx, products)
	if err != nil {
		return nil, nil, err
	}
	return productRows, envelope, nil
}

// classifyKalaRows applies the verified Patris hierarchy to the complete
// snapshot. It deliberately fails closed for ambiguous numeric rows: category
// headers and accounting/service entries must never become Woo products.
func classifyKalaRows(rows []map[string]interface{}, codeCounts map[string]int) (map[string]bool, []string, []string) {
	categories, excluded, quarantined, _ := classifyKalaRowsContext(context.Background(), rows, codeCounts)
	return categories, excluded, quarantined
}

func classifyKalaRowsContext(ctx context.Context, rows []map[string]interface{}, codeCounts map[string]int) (map[string]bool, []string, []string, error) {
	codes := make([]string, 0, len(codeCounts))
	rowByCode := make(map[string]map[string]interface{}, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		code := codeString(firstValue(row, "product_code", "code", "Code"))
		if code != "" && codeCounts[code] == 1 {
			rowByCode[code] = row
		}
	}
	for code := range codeCounts {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		codes = append(codes, code)
	}
	sort.Strings(codes)
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}

	categories := make(map[string]bool)
	excluded := make(map[string]bool)
	quarantined := make(map[string]bool)
	hasDescendant := make(map[string]bool)
	for _, code := range codes {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		if codeCounts[code] != 1 || len(code) != 6 || !asciiDigits(code) {
			continue
		}
		for _, candidate := range codes {
			if err := ctx.Err(); err != nil {
				return nil, nil, nil, err
			}
			if codeCounts[candidate] == 1 && len(candidate) == 9 && strings.HasPrefix(candidate, code) {
				hasDescendant[code] = true
				break
			}
		}
	}

	for _, code := range codes {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		if codeCounts[code] != 1 || !asciiDigits(code) {
			continue
		}
		if _, reserved := reservedNonMerchandiseCodes[code]; reserved {
			excluded[code] = true
			continue
		}
		row := rowByCode[code]
		if len(code) == 3 {
			if categoryHasStrongProductSignals(row) {
				quarantined[code] = true
			} else {
				categories[code] = true
			}
			continue
		}
		if len(code) != 6 {
			continue
		}
		if hasDescendant[code] {
			if categoryHasStrongProductSignals(row) {
				quarantined[code] = true
			} else {
				categories[code] = true
			}
			continue
		}
		if emptyCategoryHeader(code, row) {
			categories[code] = true
			continue
		}
		if !rowHasMerchandiseSignals(code, row) {
			quarantined[code] = true
		}
	}

	// Numeric Patris codes outside the documented 3/6/9 shapes are ambiguous.
	// Do not require parents to be present here: filtered/partial extracts are a
	// supported input, and their valid leaf products must remain exportable.
	for _, code := range codes {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		if codeCounts[code] != 1 || !asciiDigits(code) || categories[code] || excluded[code] || quarantined[code] {
			continue
		}
		if len(code) != 6 && len(code) != 9 {
			quarantined[code] = true
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	return categories, sortedSet(excluded), sortedSet(quarantined), nil
}

func asciiDigits(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func sortedSet(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value, included := range values {
		if included {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func parseKalaCategories(rows []map[string]interface{}, codeCounts map[string]int, categoryCodes map[string]bool) []Category {
	categories, _ := parseKalaCategoriesContext(context.Background(), rows, codeCounts, categoryCodes)
	return categories
}

func parseKalaCategoriesContext(ctx context.Context, rows []map[string]interface{}, codeCounts map[string]int, categoryCodes map[string]bool) ([]Category, error) {
	byCode := make(map[string]Category, len(categoryCodes))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		code := codeString(firstValue(row, "product_code", "code", "Code"))
		if code == "" || codeCounts[code] != 1 || !categoryCodes[code] {
			continue
		}
		presence := map[string]fieldPresence{}
		markPresence(presence, "name", row, "name", "Name", "part_number")
		byCode[code] = Category{
			CategoryCode:  code,
			Name:          normalizeText(firstValue(row, "name", "Name", "part_number")),
			Warnings:      naming.Merge(row[naming.InternalWarningsField], naming.Warnings(row)),
			fieldPresence: presence,
		}
	}

	codes := make([]string, 0, len(byCode))
	for code := range byCode {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	sort.SliceStable(codes, func(i, j int) bool {
		if len(codes[i]) != len(codes[j]) {
			return len(codes[i]) < len(codes[j])
		}
		return codes[i] < codes[j]
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for _, code := range codes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		category := byCode[code]
		parent, err := longestCategoryPrefixContext(ctx, code, byCode)
		if err != nil {
			return nil, err
		}
		category.ParentCode = parent
		category.Depth = 1
		for parent := category.ParentCode; parent != ""; {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			category.Depth++
			ancestor, exists := byCode[parent]
			if !exists || ancestor.ParentCode == parent {
				break
			}
			parent = ancestor.ParentCode
		}
		category.RecordHash = categoryRecordHash(category)
		byCode[code] = category
	}

	categories := make([]Category, 0, len(codes))
	for _, code := range codes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		categories = append(categories, byCode[code])
	}
	sort.SliceStable(categories, func(i, j int) bool {
		return categories[i].CategoryCode < categories[j].CategoryCode
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return categories, nil
}

func longestCategoryPrefix(code string, categories map[string]Category) string {
	parent, _ := longestCategoryPrefixContext(context.Background(), code, categories)
	return parent
}

func longestCategoryPrefixContext(ctx context.Context, code string, categories map[string]Category) (string, error) {
	parent := ""
	for candidate := range categories {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if len(candidate) >= len(code) || len(candidate) <= len(parent) {
			continue
		}
		if strings.HasPrefix(code, candidate) {
			parent = candidate
		}
	}
	return parent, nil
}

func categoryHasStrongProductSignals(row map[string]interface{}) bool {
	stocks, _ := warehouseStock(row)
	for _, stock := range stocks {
		if stock != 0 {
			return true
		}
	}
	for _, fields := range [][]string{
		{"sale_price_source", "FOROSH", "fee_kol"},
		{"purchase_price_source", "KHARYD", "purchase_price"},
		{"minimum_stock", "Sefaresh", "minimum"},
	} {
		if value := nullableNumber(firstValue(row, fields...)); value != nil && *value != 0 {
			return true
		}
	}
	for _, field := range []string{"foreign_price", "yuan_price", "weight_grams"} {
		if value, exists := row[field]; exists && positiveDecimal(value) != nil {
			return true
		}
	}
	sourceWarnings := []string{}
	if partner, _ := extractPartnerPrice(row, &sourceWarnings); decimalStrictlyPositive(partner) {
		return true
	}
	if strings.TrimSpace(normalizeText(firstValue(row, "location", "Location"))) != "" {
		return true
	}
	warnings := []string{}
	if decimalStrictlyPositive(extractForeignPrice(row, &warnings)) {
		return true
	}
	warnings = nil
	weight, location := extractWeightAndLocation(row, &warnings)
	return weight != nil || strings.TrimSpace(location) != ""
}

func rowHasMerchandiseSignals(code string, row map[string]interface{}) bool {
	if categoryHasStrongProductSignals(row) {
		return true
	}
	serial := normalizeText(firstValue(row, "serial", "Serial"))
	return serial != "" && serial != code
}

func emptyCategoryHeader(code string, row map[string]interface{}) bool {
	if len(code) != 6 || !asciiDigits(code) || categoryHasStrongProductSignals(row) {
		return false
	}
	name := normalizeText(firstValue(row, "name", "Name", "part_number"))
	serial := normalizeText(firstValue(row, "serial", "Serial"))
	return name != "" && serial == code
}

func parseKalaProduct(ctx context.Context, row map[string]interface{}, provider pricingcatalog.Provider, integrationActive bool, directSaleFallback ...bool) Product {
	if ctx != nil && ctx.Err() != nil {
		return Product{}
	}
	warnings := naming.Merge(row[naming.InternalWarningsField], naming.Warnings(row))
	code := codeString(firstValue(row, "product_code", "code", "Code"))
	foreignPrice := extractForeignPrice(row, &warnings)
	partnerPrice, partnerPricePresence := extractPartnerPrice(row, &warnings)
	salePrice, salePricePresence := exactSourceDecimal(row, "sale_price_source", "FOROSH", "fee_kol")
	weight, location := extractWeightAndLocation(row, &warnings)
	resolution := pricingcatalog.Resolution{}
	if integrationActive {
		resolution = provider.Resolve(ctx, code)
		if ctx != nil && ctx.Err() != nil {
			return Product{}
		}
	}
	warehouseStock, warehouseNulls := warehouseStock(row)
	totalStock := totalStock(row, warehouseStock, resolution.SelectedWarehouses, &warnings)

	var (
		priceSourceAmount   *pricingcatalog.Decimal
		priceSourceCurrency string
		priceSourceKind     string
		finalPrice          *int64
		shippingMethodID    = resolution.MethodID
		shippingPricePerKg  = clonePricingDecimal(resolution.ShippingPricePerKg)
		shippingCurrency    = resolution.ShippingPricePerKgCurrency
		markupPercent       = clonePricingDecimal(resolution.MarkupPercent)
		irtPerCNY           = clonePricingDecimal(resolution.IRTPerCNY)
		currencyDate        = resolution.CurrencyEffectiveDate
		roundingDigits      = cloneRoundingDigits(resolution.RoundingDigits)
		roundingMode        string
		explicitNulls       = cloneExplicitNulls(resolution.ExplicitNulls)
	)
	if roundingDigits != nil {
		roundingMode = pricingcatalog.RoundingModeHalfUp
	}
	if integrationActive {
		foreignPositive := decimalStrictlyPositive(foreignPrice)
		foreignReady := foreignPositive &&
			decimalStrictlyPositive(weight) &&
			strings.TrimSpace(resolution.MethodID) != "" &&
			resolution.MethodID != pricingcatalog.MethodDomestic &&
			resolution.ShippingPricePerKg != nil &&
			resolution.ShippingPricePerKgCurrency != "" &&
			resolution.MarkupPercent != nil &&
			resolution.IRTPerCNY != nil &&
			resolution.RoundingDigits != nil &&
			!hasAny(resolution.Warnings, "shipping_method_disabled", "shipping_method_unknown")
		if foreignReady {
			value, err := LandedPrice(
				weight.String(), resolution.ShippingPricePerKg.String(),
				resolution.ShippingPricePerKgCurrency, foreignPrice.String(),
				resolution.MarkupPercent.String(), resolution.IRTPerCNY.String(),
				*resolution.RoundingDigits,
			)
			if err != nil {
				warnings = append(warnings, "landed_price_calculation_failed")
			} else {
				priceSourceAmount = clonePricingDecimal(foreignPrice)
				priceSourceCurrency = pricingcatalog.CurrencyCNY
				priceSourceKind = PriceSourceKindForeign
				finalPrice = &value
				warnings = append(warnings, resolution.Warnings...)
			}
		}
		if foreignPositive && priceSourceKind == "" {
			warnings = append(warnings, "foreign_price_path_unavailable")
			if weight != nil && !decimalStrictlyPositive(weight) {
				warnings = append(warnings, "weight_non_positive_for_foreign_price")
			}
		} else if foreignPrice != nil && !foreignPositive {
			warnings = append(warnings, "foreign_price_non_positive")
		}

		partnerPositive := decimalStrictlyPositive(partnerPrice)
		if priceSourceKind == "" {
			switch partnerPricePresence {
			case fieldAbsent:
				warnings = append(warnings, "partner_price_missing")
			case fieldNull:
				warnings = append(warnings, "partner_price_explicit_null")
			case fieldValue:
				if partnerPrice == nil {
					warnings = append(warnings, "partner_price_invalid")
				} else if !partnerPositive {
					warnings = append(warnings, "partner_price_non_positive")
				}
			}
		}

		if priceSourceKind == "" && partnerPositive &&
			resolution.MarkupPercent != nil && resolution.RoundingDigits != nil {
			value, err := PartnerPrice(partnerPrice.String(), resolution.MarkupPercent.String(), *resolution.RoundingDigits)
			if err != nil {
				warnings = append(warnings, "partner_price_calculation_failed")
			} else {
				priceSourceAmount = clonePricingDecimal(partnerPrice)
				priceSourceCurrency = pricingcatalog.CurrencyIRR
				priceSourceKind = PriceSourceKindPartner
				finalPrice = &value
				shippingMethodID, shippingPricePerKg, shippingCurrency = domesticShipping()
				irtPerCNY = nil
				currencyDate = ""
				delete(explicitNulls, "shipping_price_per_kg")
				delete(explicitNulls, "shipping_price_per_kg_currency")
				warnings = append(warnings, warningsForPartnerPrice(resolution.Warnings)...)
				warnings = append(warnings, "domestic_shipping_method_applied", "partner_price_fallback_used", "freight_not_applied_for_partner_price")
			}
		}
		if priceSourceKind == "" && partnerPositive {
			warnings = append(warnings, "partner_price_path_unavailable")
		}

		useDirectSale := len(directSaleFallback) > 0 && directSaleFallback[0]
		salePositive := decimalStrictlyPositive(salePrice)
		if priceSourceKind == "" && useDirectSale {
			switch salePricePresence {
			case fieldAbsent:
				warnings = append(warnings, "sale_price_direct_missing")
			case fieldNull:
				warnings = append(warnings, "sale_price_direct_explicit_null")
			case fieldValue:
				if salePrice == nil {
					warnings = append(warnings, "sale_price_direct_invalid")
				} else if !salePositive {
					warnings = append(warnings, "sale_price_direct_non_positive")
				}
			}
		}
		if priceSourceKind == "" && salePositive && useDirectSale {
			value, err := DirectSalePrice(salePrice.String())
			if err != nil {
				warnings = append(warnings, "sale_price_direct_calculation_failed")
			} else {
				priceSourceAmount = clonePricingDecimal(salePrice)
				priceSourceCurrency = pricingcatalog.CurrencyIRR
				priceSourceKind = PriceSourceKindSaleDirect
				finalPrice = &value
				shippingMethodID, shippingPricePerKg, shippingCurrency = domesticShipping()
				markupPercent = nil
				irtPerCNY = nil
				currencyDate = ""
				roundingDigits = nil
				roundingMode = ""
				explicitNulls = map[string]bool{}
				warnings = append(warnings, warningsForPartnerPrice(resolution.Warnings)...)
				warnings = append(warnings, "domestic_shipping_method_applied", "freight_not_applied_for_sale_price_direct", "sale_price_direct_fallback_used")
			}
		}
		if priceSourceKind == "" {
			warnings = append(warnings, resolution.Warnings...)
		}
		if finalPrice == nil {
			warnings = append(warnings, "final_price_unavailable")
		}
	}
	presence := map[string]fieldPresence{}
	markPresence(presence, "name", row, "name", "Name", "part_number")
	markPresence(presence, "serial", row, "serial", "Serial")
	markPresence(presence, "unit", row, "unit", "Vahed")
	markPresence(presence, "sale_price_source", row, "sale_price_source", "FOROSH", "fee_kol")
	markPresence(presence, "partner_price_source", row, "partner_price_source", "Sharh1", "priceinfo", "Sharh")
	markPresence(presence, "purchase_price_source", row, "purchase_price_source", "KHARYD", "purchase_price")
	markPresence(presence, "minimum_stock", row, "minimum_stock", "Sefaresh", "minimum")
	markPresence(presence, "foreign_price", row, "foreign_price", "yuan_price", "Sharh1", "priceinfo", "Sharh")
	markPresence(presence, "weight_grams", row, "weight_grams", "Weight", "Sharh2")
	markPresence(presence, "location", row, "location", "Location")
	markPresence(presence, "source_updated_at", row, "source_updated_at", "updated_at", "Dates")
	markPresence(presence, "total_stock", row, "total_stock", "stock", "ALLANBAR")
	if warehouseFieldPresent(row) {
		presence["warehouse_stock"] = fieldValue
	}
	if rowNull(row, "warehouse_stock", "ANBAR") && len(warehouseStock) == 0 && len(warehouseNulls) == 0 {
		presence["warehouse_stock"] = fieldNull
	}
	if finalPrice != nil {
		presence["final_price"] = fieldValue
	}
	if priceSourceAmount != nil {
		presence["price_source_amount"] = fieldValue
		presence["price_source_currency"] = fieldValue
		presence["price_source_kind"] = fieldValue
	}
	if roundingDigits != nil {
		presence["price_rounding_digits"] = fieldValue
		presence["price_rounding_mode"] = fieldValue
	}
	for field, isNull := range explicitNulls {
		if isNull {
			presence[field] = fieldNull
		}
	}

	return Product{
		ProductCode:                code,
		Name:                       normalizeText(firstValue(row, "name", "Name", "part_number")),
		Serial:                     normalizeText(firstValue(row, "serial", "Serial")),
		Unit:                       normalizeText(firstValue(row, "unit", "Vahed")),
		SalePriceSource:            nullableNumber(firstValue(row, "sale_price_source", "FOROSH", "fee_kol")),
		PartnerPriceSource:         partnerPrice,
		PurchasePriceSource:        nullableNumber(firstValue(row, "purchase_price_source", "KHARYD", "purchase_price")),
		WarehouseStock:             warehouseStock,
		TotalStock:                 totalStock,
		MinimumStock:               nullableNumber(firstValue(row, "minimum_stock", "Sefaresh", "minimum")),
		ForeignCurrency:            "CNY",
		ForeignPrice:               foreignPrice,
		WeightGrams:                weight,
		Location:                   location,
		ShippingMethodID:           shippingMethodID,
		ShippingPricePerKg:         shippingPricePerKg,
		ShippingPricePerKgCurrency: shippingCurrency,
		MarkupPercent:              markupPercent,
		IRTPerCNY:                  irtPerCNY,
		PricingCatalogRevision:     resolution.CatalogRevision,
		PricingCatalogStatus:       resolution.CatalogStatus,
		CurrencyEffectiveDate:      currencyDate,
		PriceSourceAmount:          priceSourceAmount,
		PriceSourceCurrency:        priceSourceCurrency,
		PriceSourceKind:            priceSourceKind,
		PriceRoundingDigits:        roundingDigits,
		PriceRoundingMode:          roundingMode,
		FinalPrice:                 finalPrice,
		SourceUpdatedAt:            normalizeText(firstValue(row, "source_updated_at", "updated_at", "Dates")),
		Warnings:                   normalizedWarnings(warnings),
		fieldPresence:              presence,
		warehouseNulls:             warehouseNulls,
		integrationActive:          integrationActive,
	}
}

// Map converts the typed wire contract into the generic row representation
// required by the existing spreadsheet and SQL sink boundary.
func (product Product) Map() map[string]interface{} {
	row := map[string]interface{}{"product_code": product.ProductCode}
	putString(row, "category_code", product.CategoryCode, product.presence("category_code"))
	putString(row, "name", product.Name, product.presence("name"))
	putString(row, "serial", product.Serial, product.presence("serial"))
	putString(row, "unit", product.Unit, product.presence("unit"))
	putPointer(row, "sale_price_source", pointerFloatValue(product.SalePriceSource), product.presence("sale_price_source"))
	putPointer(row, "partner_price_source", pointerDecimalValue(product.PartnerPriceSource), product.presence("partner_price_source"))
	putPointer(row, "purchase_price_source", pointerFloatValue(product.PurchasePriceSource), product.presence("purchase_price_source"))
	putWarehouses(row, product)
	putPointer(row, "total_stock", pointerFloatValue(product.TotalStock), product.presence("total_stock"))
	putPointer(row, "minimum_stock", pointerFloatValue(product.MinimumStock), product.presence("minimum_stock"))
	if product.ForeignPrice != nil || product.presence("foreign_currency") != fieldAbsent {
		putString(row, "foreign_currency", product.ForeignCurrency, product.presence("foreign_currency"))
	}
	putPointer(row, "foreign_price", pointerDecimalValue(product.ForeignPrice), product.presence("foreign_price"))
	putPointer(row, "weight_grams", pointerDecimalValue(product.WeightGrams), product.presence("weight_grams"))
	putString(row, "location", product.Location, product.presence("location"))
	if product.integrationActive {
		putString(row, "shipping_method_id", product.ShippingMethodID, product.presence("shipping_method_id"))
		putPointer(row, "shipping_price_per_kg", pointerDecimalValue(product.ShippingPricePerKg), product.presence("shipping_price_per_kg"))
		putString(row, "shipping_price_per_kg_currency", product.ShippingPricePerKgCurrency, product.presence("shipping_price_per_kg_currency"))
		putPointer(row, "markup_percent", pointerDecimalValue(product.MarkupPercent), product.presence("markup_percent"))
		putPointer(row, "irt_per_cny", pointerDecimalValue(product.IRTPerCNY), product.presence("irt_per_cny"))
		putString(row, "pricing_catalog_revision", product.PricingCatalogRevision, product.presence("pricing_catalog_revision"))
		putString(row, "pricing_catalog_status", product.PricingCatalogStatus, product.presence("pricing_catalog_status"))
		putString(row, "currency_effective_date", product.CurrencyEffectiveDate, product.presence("currency_effective_date"))
		putPointer(row, "price_source_amount", pointerDecimalValue(product.PriceSourceAmount), product.presence("price_source_amount"))
		putString(row, "price_source_currency", product.PriceSourceCurrency, product.presence("price_source_currency"))
		putString(row, "price_source_kind", product.PriceSourceKind, product.presence("price_source_kind"))
		putPointer(row, "price_rounding_digits", pointerIntValueFromInt(product.PriceRoundingDigits), product.presence("price_rounding_digits"))
		putString(row, "price_rounding_mode", product.PriceRoundingMode, product.presence("price_rounding_mode"))
		putPointer(row, "final_price", pointerIntValue(product.FinalPrice), product.presence("final_price"))
	}
	putString(row, "source_updated_at", product.SourceUpdatedAt, product.presence("source_updated_at"))
	row["warnings"] = append(make([]string, 0, len(product.Warnings)), product.Warnings...)
	putString(row, "record_hash", product.RecordHash, fieldAbsent)
	return row
}

// MarshalJSON keeps JSON and row-based sinks on one sparse boundary. Fields
// absent from the source are omitted, while explicitly received nulls remain
// JSON null instead of being confused with missing data.
func (product Product) MarshalJSON() ([]byte, error) {
	return json.Marshal(product.Map())
}

func (product *Product) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := rejectUnknownJSONFields(raw, "product", []string{
		"product_code", "category_code", "name", "serial", "unit", "sale_price_source", "partner_price_source",
		"purchase_price_source", "warehouse_stock", "total_stock", "minimum_stock",
		"foreign_currency", "foreign_price", "weight_grams", "location", "shipping_method_id",
		"shipping_price_per_kg", "shipping_price_per_kg_currency", "markup_percent", "irt_per_cny", "pricing_catalog_revision",
		"pricing_catalog_status", "currency_effective_date", "price_source_amount", "price_source_currency",
		"price_source_kind", "price_rounding_digits", "price_rounding_mode", "final_price", "source_updated_at",
		"warnings", "record_hash",
	}); err != nil {
		return err
	}
	type productAlias Product
	var decoded productAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*product = Product(decoded)

	product.fieldPresence = map[string]fieldPresence{}
	for _, field := range []string{
		"category_code", "name", "serial", "unit", "sale_price_source", "partner_price_source", "purchase_price_source",
		"warehouse_stock", "total_stock", "minimum_stock", "foreign_currency", "foreign_price",
		"weight_grams", "location", "source_updated_at", "shipping_method_id",
		"shipping_price_per_kg", "shipping_price_per_kg_currency", "markup_percent", "irt_per_cny",
		"pricing_catalog_revision", "pricing_catalog_status", "currency_effective_date",
		"price_source_amount", "price_source_currency", "price_source_kind",
		"price_rounding_digits", "price_rounding_mode", "final_price",
	} {
		if value, exists := raw[field]; exists {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				product.fieldPresence[field] = fieldNull
			} else {
				product.fieldPresence[field] = fieldValue
			}
		}
	}
	_, shippingPricePresent := raw["shipping_price_per_kg"]
	_, shippingCurrencyPresent := raw["shipping_price_per_kg_currency"]
	if shippingPricePresent != shippingCurrencyPresent {
		return fmt.Errorf("product shipping_price_per_kg and shipping_price_per_kg_currency must be present together")
	}
	if shippingCurrencyPresent && product.presence("shipping_price_per_kg_currency") != fieldNull {
		if product.ShippingPricePerKgCurrency != pricingcatalog.CurrencyCNY && product.ShippingPricePerKgCurrency != pricingcatalog.CurrencyIRR {
			return fmt.Errorf("product shipping_price_per_kg_currency must be CNY or IRR")
		}
	}
	priceSourceFields := []string{"price_source_amount", "price_source_currency", "price_source_kind"}
	priceSourcePresent := 0
	for _, field := range priceSourceFields {
		if _, exists := raw[field]; exists {
			priceSourcePresent++
		}
	}
	if priceSourcePresent != 0 && priceSourcePresent != len(priceSourceFields) {
		return fmt.Errorf("product price_source_amount, price_source_currency, and price_source_kind must be present together")
	}
	if priceSourcePresent == len(priceSourceFields) {
		for _, field := range priceSourceFields {
			if product.presence(field) == fieldNull {
				return fmt.Errorf("product %s must be omitted rather than null", field)
			}
		}
	}
	_, roundingDigitsPresent := raw["price_rounding_digits"]
	_, roundingModePresent := raw["price_rounding_mode"]
	if product.presence("price_rounding_digits") == fieldNull {
		if roundingModePresent {
			return fmt.Errorf("product price_rounding_mode must be omitted when price_rounding_digits is null")
		}
	} else if roundingDigitsPresent != roundingModePresent {
		return fmt.Errorf("product price_rounding_digits and price_rounding_mode must be present together")
	}
	for _, field := range []string{"partner_price_source", "foreign_price", "weight_grams", "shipping_price_per_kg", "markup_percent", "irt_per_cny", "price_source_amount"} {
		if value, exists := raw[field]; exists {
			value = bytes.TrimSpace(value)
			if len(value) > 0 && value[0] == '"' {
				return fmt.Errorf("product %s must be a JSON number or null", field)
			}
		}
	}
	product.warehouseNulls = map[string]bool{}
	if value, exists := raw["warehouse_stock"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		var warehouses map[string]json.RawMessage
		if err := json.Unmarshal(value, &warehouses); err == nil {
			for warehouse, stock := range warehouses {
				if bytes.Equal(bytes.TrimSpace(stock), []byte("null")) {
					product.warehouseNulls[warehouse] = true
					delete(product.WarehouseStock, warehouse)
				}
			}
		}
	}

	for _, field := range []string{
		"shipping_method_id", "shipping_price_per_kg", "shipping_price_per_kg_currency", "markup_percent", "irt_per_cny",
		"pricing_catalog_revision", "pricing_catalog_status", "currency_effective_date",
		"price_source_amount", "price_source_currency", "price_source_kind",
		"price_rounding_digits", "price_rounding_mode", "final_price",
	} {
		if _, exists := raw[field]; exists {
			product.integrationActive = true
			break
		}
	}
	return nil
}

func (product Product) presence(field string) fieldPresence {
	if state, ok := product.fieldPresence[field]; ok {
		return state
	}
	return fieldAbsent
}

func putString(row map[string]interface{}, field, value string, state fieldPresence) {
	if state == fieldNull {
		row[field] = nil
		return
	}
	if state == fieldValue {
		row[field] = value
		return
	}
	if strings.TrimSpace(value) != "" {
		row[field] = value
	}
}

func putPointer(row map[string]interface{}, field string, value interface{}, state fieldPresence) {
	if value != nil {
		row[field] = value
		return
	}
	if state == fieldNull {
		row[field] = nil
	}
}

func putWarehouses(row map[string]interface{}, product Product) {
	state := product.presence("warehouse_stock")
	if state == fieldNull {
		row["warehouse_stock"] = nil
		return
	}
	if len(product.WarehouseStock) == 0 && len(product.warehouseNulls) == 0 && state == fieldAbsent {
		return
	}
	stock := make(map[string]interface{}, len(product.WarehouseStock)+len(product.warehouseNulls))
	for warehouse, value := range product.WarehouseStock {
		stock[warehouse] = value
	}
	for warehouse := range product.warehouseNulls {
		if _, exists := stock[warehouse]; !exists {
			stock[warehouse] = nil
		}
	}
	row["warehouse_stock"] = stock
}

func recordHash(product Product) string {
	product.RecordHash = ""
	material, _ := json.Marshal(product)
	return hashBytes(material)
}

func ProductsToRows(products []Product) []map[string]interface{} {
	rows, _ := ProductsToRowsContext(context.Background(), products)
	return rows
}

// ProductsToRowsContext materializes canonical row maps with cooperative
// cancellation between products.
func ProductsToRowsContext(ctx context.Context, products []Product) ([]map[string]interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rows := make([]map[string]interface{}, 0, len(products))
	for _, product := range products {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows = append(rows, product.Map())
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func (category Category) Map() map[string]interface{} {
	row := map[string]interface{}{
		"category_code": category.CategoryCode,
		"parent_code":   category.ParentCode,
		"depth":         category.Depth,
		"warnings":      append(make([]string, 0, len(category.Warnings)), category.Warnings...),
	}
	putString(row, "name", category.Name, category.fieldPresence["name"])
	putString(row, "record_hash", category.RecordHash, fieldAbsent)
	return row
}

func (category Category) MarshalJSON() ([]byte, error) {
	return json.Marshal(category.Map())
}

func (category *Category) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := rejectUnknownJSONFields(raw, "category", []string{
		"category_code", "name", "parent_code", "depth", "warnings", "record_hash",
	}); err != nil {
		return err
	}
	type categoryAlias Category
	var decoded categoryAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*category = Category(decoded)
	category.fieldPresence = map[string]fieldPresence{}
	if value, exists := raw["name"]; exists {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			category.fieldPresence["name"] = fieldNull
		} else {
			category.fieldPresence["name"] = fieldValue
		}
	}
	return nil
}

func CategoriesToRows(categories []Category) []map[string]interface{} {
	rows := make([]map[string]interface{}, 0, len(categories))
	for _, category := range categories {
		rows = append(rows, category.Map())
	}
	return rows
}

func categoryRecordHash(category Category) string {
	category.RecordHash = ""
	material, _ := json.Marshal(category)
	return hashBytes(material)
}

// extractPartnerPrice maps Patris' first Sharh1 numeric slot ("قیمت همکار")
// independently from FOROSH. FOROSH is the distinct sale-price fact and must
// never be substituted here.
func extractPartnerPrice(row map[string]interface{}, warnings *[]string) (*pricingcatalog.Decimal, fieldPresence) {
	sources := map[string]pricingcatalog.Decimal{}
	presence := fieldAbsent

	if value, exists := row["partner_price_source"]; exists {
		if value == nil {
			return nil, fieldNull
		}
		return decimalNumber(value), fieldValue
	}
	for _, field := range []string{"Sharh1", "priceinfo", "Sharh"} {
		value, exists := row[field]
		if !exists {
			continue
		}
		if presence == fieldAbsent {
			if value == nil {
				presence = fieldNull
			} else {
				presence = fieldValue
			}
		}
		if value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if parsed := partnerPriceFromDescription(value, warnings); parsed != nil {
			sources[field] = *parsed
		}
	}
	if len(sources) == 0 {
		return nil, presence
	}
	distinct := map[string]pricingcatalog.Decimal{}
	for _, value := range sources {
		distinct[decimalIdentity(value)] = value
	}
	if len(distinct) != 1 {
		*warnings = append(*warnings, "partner_price_source_conflict")
		return nil, fieldValue
	}
	for _, value := range distinct {
		copy := value
		return &copy, fieldValue
	}
	return nil, presence
}

func partnerPriceFromDescription(value interface{}, warnings *[]string) *pricingcatalog.Decimal {
	text := normalizeDigits(normalizeText(value))
	matches := numberPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	if len(matches) > 4 {
		*warnings = append(*warnings, "partner_price_extra_slots", "partner_price_ambiguous")
		return nil
	}
	parsed := decimalNumber(matches[0])
	if parsed == nil {
		*warnings = append(*warnings, "partner_price_invalid")
	}
	return parsed
}

func extractForeignPrice(row map[string]interface{}, warnings *[]string) *pricingcatalog.Decimal {
	sources := map[string]pricingcatalog.Decimal{}
	for _, field := range []string{"foreign_price", "yuan_price"} {
		if value, ok := row[field]; ok {
			if parsed := nonNegativeDecimal(value); parsed != nil {
				sources[field] = *parsed
			}
		}
	}
	for _, field := range []string{"Sharh1", "priceinfo", "Sharh"} {
		value, ok := row[field]
		if !ok || strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if parsed := foreignPriceFromDescription(value, warnings); parsed != nil {
			sources[field] = *parsed
		}
	}
	if len(sources) == 0 {
		*warnings = append(*warnings, "foreign_price_missing")
		return nil
	}
	distinct := map[string]pricingcatalog.Decimal{}
	for _, value := range sources {
		distinct[decimalIdentity(value)] = value
	}
	if len(distinct) != 1 {
		*warnings = append(*warnings, "foreign_price_source_conflict")
		return nil
	}
	for _, value := range distinct {
		copy := value
		return &copy
	}
	return nil
}

func foreignPriceFromDescription(value interface{}, warnings *[]string) *pricingcatalog.Decimal {
	text := normalizeDigits(normalizeText(value))
	matches := numberPattern.FindAllString(text, -1)
	numbers := make([]pricingcatalog.Decimal, 0, len(matches))
	for _, match := range matches {
		if parsed := decimalNumber(match); parsed != nil {
			numbers = append(numbers, *parsed)
		}
	}
	if len(numbers) > 4 {
		*warnings = append(*warnings, "foreign_price_extra_slots", "foreign_price_ambiguous")
		return nil
	}
	if len(numbers) == 4 {
		value, ok := numbers[3].Rat()
		if !ok || value.Sign() < 0 {
			*warnings = append(*warnings, "foreign_price_invalid")
			return nil
		}
		copy := numbers[3]
		return &copy
	}
	positive := []pricingcatalog.Decimal{}
	for _, number := range numbers {
		if decimalPositive(number) {
			positive = append(positive, number)
		}
	}
	if len(positive) == 1 {
		*warnings = append(*warnings, "foreign_price_inferred_without_slot")
		copy := positive[0]
		return &copy
	}
	if len(positive) > 1 {
		*warnings = append(*warnings, "foreign_price_ambiguous")
	}
	return nil
}

func extractWeightAndLocation(row map[string]interface{}, warnings *[]string) (*pricingcatalog.Decimal, string) {
	weights := map[string]pricingcatalog.Decimal{}
	locations := map[string]string{}
	for _, field := range []string{"weight_grams", "Weight"} {
		if value, ok := row[field]; ok {
			if parsed := nonNegativeDecimal(value); parsed != nil {
				weights[field] = *parsed
			}
		}
	}
	if value := normalizeText(firstValue(row, "location")); value != "" {
		locations["location"] = value
	}
	for _, field := range []string{"Sharh2", "short_desc"} {
		value, ok := row[field]
		if !ok || strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		weight, location, fieldWarnings := weightAndLocationFromDescription(value)
		*warnings = append(*warnings, fieldWarnings...)
		if weight != nil {
			weights[field] = *weight
		}
		if location != "" {
			locations[field] = location
		}
	}

	var weight *pricingcatalog.Decimal
	distinctWeights := map[string]pricingcatalog.Decimal{}
	for _, value := range weights {
		distinctWeights[decimalIdentity(value)] = value
	}
	if len(distinctWeights) == 1 {
		for _, value := range distinctWeights {
			copy := value
			weight = &copy
		}
	} else if len(distinctWeights) > 1 {
		*warnings = append(*warnings, "weight_source_conflict")
	} else if !hasAny(*warnings, "weight_ambiguous", "weight_unparsed", "weight_source_conflict") {
		*warnings = append(*warnings, "weight_missing")
	}

	location := ""
	distinctLocations := map[string]string{}
	for _, value := range locations {
		distinctLocations[normalizeText(value)] = value
	}
	if len(distinctLocations) > 0 {
		keys := make([]string, 0, len(distinctLocations))
		for key := range distinctLocations {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		location = distinctLocations[keys[0]]
		if len(distinctLocations) > 1 {
			*warnings = append(*warnings, "location_source_conflict")
		}
	}
	return weight, location
}

func weightAndLocationFromDescription(value interface{}) (*pricingcatalog.Decimal, string, []string) {
	text := normalizeDigits(normalizeText(value))
	warnings := []string{}
	per := pricingcatalog.Decimal("1")
	if match := perPackagePattern.FindStringSubmatch(text); len(match) > 1 {
		if parsed := positiveDecimal(match[1]); parsed != nil {
			per = *parsed
		}
	}
	candidates := []pricingcatalog.Decimal{}
	location := text
	for _, match := range weightValueUnit.FindAllStringSubmatch(text, -1) {
		if parsed := weightInGrams(match[1], match[2], &per); parsed != nil {
			candidates = append(candidates, *parsed)
		}
	}
	for _, match := range weightUnitValue.FindAllStringSubmatch(text, -1) {
		if parsed := weightInGrams(match[2], match[1], &per); parsed != nil {
			candidates = append(candidates, *parsed)
		}
	}
	location = weightValueUnit.ReplaceAllString(location, " ")
	location = weightUnitValue.ReplaceAllString(location, " ")
	if halfKilogramPattern.MatchString(text) {
		if parsed := weightInGrams("0.5", "kg", &per); parsed != nil {
			candidates = append(candidates, *parsed)
		}
		location = halfKilogramPattern.ReplaceAllString(location, " ")
	}
	distinct := map[string]pricingcatalog.Decimal{}
	for _, candidate := range candidates {
		distinct[decimalIdentity(candidate)] = candidate
	}
	var weight *pricingcatalog.Decimal
	if len(distinct) == 1 {
		for _, value := range distinct {
			copy := value
			weight = &copy
		}
	} else if len(distinct) > 1 {
		warnings = append(warnings, "weight_ambiguous")
	} else if weightUnitPresent.MatchString(text) {
		warnings = append(warnings, "weight_unparsed")
	}

	location = perPackagePattern.ReplaceAllString(location, " ")
	for standaloneZero.MatchString(location) {
		location = standaloneZero.ReplaceAllString(location, "$1$2")
	}
	location = strings.NewReplacer("\r", " - ", "\n", " - ", "|", " - ", ";", " - ", "_", " - ").Replace(location)
	location = regexp.MustCompile(`\s*-\s*`).ReplaceAllString(location, " - ")
	location = regexp.MustCompile(`\s+`).ReplaceAllString(location, " ")
	location = strings.Trim(normalizeText(location), " \t\n\r-_.,")
	return weight, location, warnings
}

func weightInGrams(value, unit string, per *pricingcatalog.Decimal) *pricingcatalog.Decimal {
	parsed := nonNegativeDecimal(value)
	if parsed == nil || per == nil || !decimalPositive(*per) {
		return nil
	}
	grams, ok := parsed.Rat()
	perRat, perOK := per.Rat()
	if !ok || !perOK {
		return nil
	}
	unit = strings.ToLower(normalizeText(unit))
	if strings.Contains(unit, "میلی") || unit == "mg" || strings.HasPrefix(unit, "milligram") {
		grams.Quo(grams, big.NewRat(1000, 1))
	} else if strings.Contains(unit, "کیلو") || unit == "kg" || strings.HasPrefix(unit, "kilogram") {
		grams.Mul(grams, big.NewRat(1000, 1))
	}
	grams.Quo(grams, perRat)
	if grams.Sign() < 0 {
		return nil
	}
	return finiteDecimal(grams)
}

func finiteDecimal(value *big.Rat) *pricingcatalog.Decimal {
	if value == nil {
		return nil
	}
	denominator := new(big.Int).Set(value.Denom())
	two, five := big.NewInt(2), big.NewInt(5)
	counts := [2]int{}
	for index, factor := range []*big.Int{two, five} {
		for new(big.Int).Mod(denominator, factor).Sign() == 0 {
			denominator.Quo(denominator, factor)
			counts[index]++
		}
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return nil
	}
	scale := counts[0]
	if counts[1] > scale {
		scale = counts[1]
	}
	numerator := new(big.Int).Set(value.Num())
	if scale > counts[0] {
		numerator.Mul(numerator, new(big.Int).Exp(two, big.NewInt(int64(scale-counts[0])), nil))
	}
	if scale > counts[1] {
		numerator.Mul(numerator, new(big.Int).Exp(five, big.NewInt(int64(scale-counts[1])), nil))
	}
	negative := numerator.Sign() < 0
	digits := new(big.Int).Abs(numerator).String()
	if scale > 0 {
		for len(digits) <= scale {
			digits = "0" + digits
		}
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
		digits = strings.TrimRight(strings.TrimRight(digits, "0"), ".")
	}
	if negative {
		digits = "-" + digits
	}
	parsed, err := pricingcatalog.NewDecimal(digits)
	if err != nil {
		return nil
	}
	return parsed
}

func warehouseStock(row map[string]interface{}) (map[string]float64, map[string]bool) {
	result := map[string]float64{}
	nulls := map[string]bool{}
	if existing, ok := row["warehouse_stock"].(map[string]interface{}); ok {
		for key, value := range existing {
			key = strings.TrimSpace(key)
			if value == nil {
				nulls[key] = true
				continue
			}
			if number := nullableNumber(value); number != nil {
				result[key] = *number
			}
		}
	}
	if value, ok := row["ANBAR"]; ok {
		reflected := reflect.ValueOf(value)
		if reflected.IsValid() && (reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array) {
			for index := 0; index < reflected.Len(); index++ {
				value := reflected.Index(index).Interface()
				key := strconv.Itoa(index + 1)
				if value == nil {
					nulls[key] = true
				} else if number := nullableNumber(value); number != nil {
					result[key] = *number
				}
			}
		}
	}
	for key, value := range row {
		upper := strings.ToUpper(key)
		if !strings.HasPrefix(upper, "ANBAR") || upper == "ANBAR" {
			continue
		}
		index := strings.TrimPrefix(upper, "ANBAR")
		if _, err := strconv.Atoi(index); err == nil {
			if value == nil {
				nulls[index] = true
			} else if number := nullableNumber(value); number != nil {
				result[index] = *number
			}
		}
	}
	return result, nulls
}

func markPresence(target map[string]fieldPresence, output string, row map[string]interface{}, fields ...string) {
	for _, field := range fields {
		value, exists := row[field]
		if !exists {
			continue
		}
		if value == nil {
			target[output] = fieldNull
		} else {
			target[output] = fieldValue
		}
		return
	}
}

func rowNull(row map[string]interface{}, fields ...string) bool {
	for _, field := range fields {
		if value, exists := row[field]; exists {
			return value == nil
		}
	}
	return false
}

func warehouseFieldPresent(row map[string]interface{}) bool {
	for key := range row {
		upper := strings.ToUpper(strings.TrimSpace(key))
		if upper == "WAREHOUSE_STOCK" || upper == "ANBAR" || (strings.HasPrefix(upper, "ANBAR") && len(upper) > len("ANBAR")) {
			return true
		}
	}
	return false
}

func totalStock(row map[string]interface{}, warehouses map[string]float64, selected []string, warnings *[]string) *float64 {
	if len(selected) > 0 {
		total := 0.0
		matched := 0
		for _, warehouse := range selected {
			warehouse = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(warehouse)), "ANBAR")
			if value, ok := warehouses[warehouse]; ok {
				total += value
				matched++
			}
		}
		if matched > 0 {
			if total < 0 {
				*warnings = append(*warnings, "negative_total_stock")
			}
			return floatPointer(total)
		}
		*warnings = append(*warnings, "selected_warehouses_unavailable")
		return nil
	}
	value := nullableNumber(firstValue(row, "total_stock", "stock", "ALLANBAR"))
	if value == nil {
		*warnings = append(*warnings, "total_stock_missing")
		return nil
	}
	if *value < 0 {
		*warnings = append(*warnings, "negative_total_stock")
	}
	return value
}

func nullableNumber(value interface{}) *float64 {
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil
		}
		return floatPointer(typed)
	case float32:
		return nullableNumber(float64(typed))
	case int:
		return floatPointer(float64(typed))
	case int8:
		return floatPointer(float64(typed))
	case int16:
		return floatPointer(float64(typed))
	case int32:
		return floatPointer(float64(typed))
	case int64:
		return floatPointer(float64(typed))
	case uint:
		return floatPointer(float64(typed))
	case uint8:
		return floatPointer(float64(typed))
	case uint16:
		return floatPointer(float64(typed))
	case uint32:
		return floatPointer(float64(typed))
	case uint64:
		return floatPointer(float64(typed))
	case json.Number:
		return nullableNumber(typed.String())
	}
	text := normalizeDigits(fmt.Sprint(value))
	text = strings.TrimSpace(strings.NewReplacer(" ", "", "٬", "", "،", "", "٫", ".").Replace(text))
	if strings.Count(text, ",") == 1 && !strings.Contains(text, ".") {
		parts := strings.SplitN(text, ",", 2)
		if len(parts[1]) == 3 && strings.TrimLeft(parts[0], "+-") != "" {
			text = parts[0] + parts[1]
		} else {
			text = parts[0] + "." + parts[1]
		}
	} else {
		text = strings.ReplaceAll(text, ",", "")
	}
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil
	}
	return floatPointer(parsed)
}

func positiveNumber(value interface{}) *float64 {
	parsed := nullableNumber(value)
	if parsed == nil || *parsed <= 0 {
		return nil
	}
	return parsed
}

func decimalNumber(value interface{}) *pricingcatalog.Decimal {
	if value == nil {
		return nil
	}
	if typed, ok := value.(pricingcatalog.Decimal); ok {
		copy := typed
		return &copy
	}
	if typed, ok := value.(*pricingcatalog.Decimal); ok {
		if typed == nil {
			return nil
		}
		copy := *typed
		return &copy
	}
	var text string
	switch typed := value.(type) {
	case json.Number:
		text = typed.String()
	case float64:
		text = strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		text = strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		text = strconv.FormatInt(int64(typed), 10)
	case int8:
		text = strconv.FormatInt(int64(typed), 10)
	case int16:
		text = strconv.FormatInt(int64(typed), 10)
	case int32:
		text = strconv.FormatInt(int64(typed), 10)
	case int64:
		text = strconv.FormatInt(typed, 10)
	case uint:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint8:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint16:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint32:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint64:
		text = strconv.FormatUint(typed, 10)
	default:
		text = normalizeDigits(fmt.Sprint(value))
	}
	text = strings.TrimSpace(strings.NewReplacer(" ", "", "٬", "", "،", "", "٫", ".").Replace(text))
	if strings.Count(text, ",") == 1 && !strings.Contains(text, ".") {
		parts := strings.SplitN(text, ",", 2)
		if len(parts[1]) == 3 && strings.TrimLeft(parts[0], "+-") != "" {
			text = parts[0] + parts[1]
		} else {
			text = parts[0] + "." + parts[1]
		}
	} else {
		text = strings.ReplaceAll(text, ",", "")
	}
	parsed, err := pricingcatalog.NewDecimal(text)
	if err != nil {
		return nil
	}
	return parsed
}

func nonNegativeDecimal(value interface{}) *pricingcatalog.Decimal {
	parsed := decimalNumber(value)
	if parsed == nil {
		return nil
	}
	rational, ok := parsed.Rat()
	if !ok || rational.Sign() < 0 {
		return nil
	}
	return parsed
}

func positiveDecimal(value interface{}) *pricingcatalog.Decimal {
	parsed := decimalNumber(value)
	if parsed == nil || !decimalPositive(*parsed) {
		return nil
	}
	return parsed
}

func decimalPositive(value pricingcatalog.Decimal) bool {
	parsed, ok := value.Rat()
	return ok && parsed.Sign() > 0
}

func decimalStrictlyPositive(value *pricingcatalog.Decimal) bool {
	if value == nil {
		return false
	}
	return decimalPositive(*value)
}

func exactSourceDecimal(row map[string]interface{}, fields ...string) (*pricingcatalog.Decimal, fieldPresence) {
	for _, field := range fields {
		value, exists := row[field]
		if !exists {
			continue
		}
		if value == nil {
			return nil, fieldNull
		}
		return decimalNumber(value), fieldValue
	}
	return nil, fieldAbsent
}

func clonePricingDecimal(value *pricingcatalog.Decimal) *pricingcatalog.Decimal {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneRoundingDigits(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneExplicitNulls(value map[string]bool) map[string]bool {
	result := make(map[string]bool, len(value))
	for field, isNull := range value {
		if isNull {
			result[field] = true
		}
	}
	return result
}

func domesticShipping() (string, *pricingcatalog.Decimal, string) {
	zero := pricingcatalog.Decimal("0")
	return pricingcatalog.MethodDomestic, &zero, pricingcatalog.CurrencyIRR
}

func warningsForPartnerPrice(values []string) []string {
	irrelevant := map[string]struct{}{
		"fx_rate_missing":                        {},
		"pricing_cny_to_irt_missing_or_invalid":  {},
		"shipping_method_disabled":               {},
		"shipping_method_missing":                {},
		"shipping_method_unknown":                {},
		"shipping_price_per_kg_currency_invalid": {},
		"shipping_price_per_kg_currency_missing": {},
		"shipping_price_per_kg_missing":          {},
		"shipping_price_per_kg_pair_incomplete":  {},
		"domestic_shipping_price_must_be_zero":   {},
		"domestic_shipping_currency_must_be_irr": {},
	}
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if _, skip := irrelevant[value]; !skip {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func decimalIdentity(value pricingcatalog.Decimal) string {
	parsed, ok := value.Rat()
	if !ok {
		return value.String()
	}
	return parsed.RatString()
}

func normalizeText(value interface{}) string {
	if value == nil {
		return ""
	}
	text := fmt.Sprint(value)
	if strings.Contains(text, "%") {
		if decoded, err := url.PathUnescape(text); err == nil && decoded != text {
			if utf8.ValidString(decoded) {
				text = decoded
			} else {
				text = converter.Patris2Fa(decoded)
			}
		}
	}
	if !utf8.ValidString(text) {
		text = converter.Patris2Fa(text)
	}
	text = strings.NewReplacer(
		"ك", "ک", "ي", "ی", "ى", "ی", "ـ", "", "\u0081", "",
		"\u00a0", " ", "\u200c", " ", "\u200d", " ",
	).Replace(text)
	text = norm.NFC.String(text)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for index, line := range lines {
		lines[index] = strings.Join(strings.Fields(line), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func normalizeDigits(value string) string {
	return strings.NewReplacer(
		"۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4",
		"۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9",
		"٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4",
		"٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9",
	).Replace(value)
}

func codeString(value interface{}) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(normalizeDigits(typed))
	case json.Number:
		return strings.TrimSpace(typed.String())
	case float64:
		if math.Trunc(typed) == typed {
			return strconv.FormatFloat(typed, 'f', 0, 64)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return codeString(float64(typed))
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstValue(row map[string]interface{}, fields ...string) interface{} {
	for _, field := range fields {
		if value, exists := row[field]; exists {
			return value
		}
	}
	return nil
}

func pointerFloatValue(value *float64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func pointerDecimalValue(value *pricingcatalog.Decimal) interface{} {
	if value == nil {
		return nil
	}
	return json.Number(value.String())
}

func pointerIntValue(value *int64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func pointerIntValueFromInt(value *int) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func floatPointer(value float64) *float64 { return &value }

func normalizedWarnings(values []string) []string {
	result, _ := normalizedWarningsContext(context.Background(), values)
	return result
}

func normalizedWarningsContext(ctx context.Context, values []string) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	set := map[string]struct{}{}
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	if err := stableSortContext(ctx, result, func(left, right string) bool { return left < right }); err != nil {
		return nil, err
	}
	return result, nil
}

func hasAny(values []string, expected ...string) bool {
	for _, value := range values {
		for _, candidate := range expected {
			if value == candidate {
				return true
			}
		}
	}
	return false
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
