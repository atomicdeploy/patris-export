package canonical

import (
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

type Product struct {
	ProductCode            string                  `json:"product_code"`
	Name                   string                  `json:"name"`
	Serial                 string                  `json:"serial"`
	Unit                   string                  `json:"unit"`
	SalePriceSource        *float64                `json:"sale_price_source"`
	PurchasePriceSource    *float64                `json:"purchase_price_source"`
	WarehouseStock         map[string]float64      `json:"warehouse_stock"`
	TotalStock             *float64                `json:"total_stock"`
	MinimumStock           *float64                `json:"minimum_stock"`
	ForeignCurrency        string                  `json:"foreign_currency"`
	ForeignPrice           *pricingcatalog.Decimal `json:"foreign_price"`
	WeightGrams            *pricingcatalog.Decimal `json:"weight_grams"`
	Location               string                  `json:"location"`
	ImportFreightMethodID  string                  `json:"import_freight_method_id"`
	FreightCNYPerKg        *pricingcatalog.Decimal `json:"freight_cny_per_kg"`
	MarkupPercent          *pricingcatalog.Decimal `json:"markup_percent"`
	IRTPerCNY              *pricingcatalog.Decimal `json:"irt_per_cny"`
	PricingCatalogRevision string                  `json:"pricing_catalog_revision"`
	PricingCatalogStatus   string                  `json:"pricing_catalog_status"`
	CurrencyEffectiveDate  string                  `json:"currency_effective_date"`
	FinalPrice             *int64                  `json:"final_price"`
	FormulaVersion         string                  `json:"formula_version"`
	SourceUpdatedAt        string                  `json:"source_updated_at"`
	Warnings               []string                `json:"warnings"`
	RecordHash             string                  `json:"record_hash,omitempty"`
}

func Transform(ctx context.Context, rows []map[string]interface{}, source string, cfg Config, provider pricingcatalog.Provider, generatedAt time.Time) ([]map[string]interface{}, *Envelope) {
	if provider == nil {
		provider = pricingcatalog.NewProvider(cfg.Pricing)
	}
	products := make([]Product, 0, len(rows))
	codeCounts := make(map[string]int, len(rows))
	for _, row := range rows {
		if code := codeString(firstValue(row, "product_code", "code", "Code")); code != "" {
			codeCounts[code]++
		}
	}
	quarantined := make([]string, 0)
	for code, count := range codeCounts {
		if count > 1 {
			quarantined = append(quarantined, code)
		}
	}
	sort.Strings(quarantined)
	eligible := make([]int, 0, len(rows))
	for index, row := range rows {
		code := codeString(firstValue(row, "product_code", "code", "Code"))
		if code != "" && codeCounts[code] == 1 {
			eligible = append(eligible, index)
		}
	}
	if prefetcher, ok := provider.(pricingcatalog.Prefetcher); ok && len(eligible) > 0 {
		codes := make([]string, 0, len(eligible))
		for _, index := range eligible {
			codes = append(codes, codeString(firstValue(rows[index], "product_code", "code", "Code")))
		}
		provider = prefetcher.Prefetch(ctx, codes)
	}
	parsedProducts := make([]Product, len(rows))
	workers := 1
	normalizedPricing := pricingcatalog.Normalize(cfg.Pricing)
	if normalizedPricing.Mode == pricingcatalog.ModeDigitalogic {
		workers = normalizedPricing.Digitalogic.MaxConcurrency
		if workers > len(eligible) {
			workers = len(eligible)
		}
	}
	if workers <= 1 {
		for _, index := range eligible {
			parsedProducts[index] = parseKalaProduct(ctx, rows[index], provider)
		}
	} else {
		jobs := make(chan int)
		var wait sync.WaitGroup
		wait.Add(workers)
		for worker := 0; worker < workers; worker++ {
			go func() {
				defer wait.Done()
				for index := range jobs {
					parsedProducts[index] = parseKalaProduct(ctx, rows[index], provider)
				}
			}()
		}
		for _, index := range eligible {
			jobs <- index
		}
		close(jobs)
		wait.Wait()
	}
	for _, product := range parsedProducts {
		if product.ProductCode == "" {
			continue
		}
		if codeCounts[product.ProductCode] > 1 {
			continue
		}
		product.RecordHash = recordHash(product)
		products = append(products, product)
	}
	sort.SliceStable(products, func(i, j int) bool {
		return products[i].ProductCode < products[j].ProductCode
	})
	return ProductsToRows(products), NewEnvelope(products, source, cfg.SourceID, generatedAt, quarantined...)
}

func parseKalaProduct(ctx context.Context, row map[string]interface{}, provider pricingcatalog.Provider) Product {
	warnings := []string{}
	code := codeString(firstValue(row, "product_code", "code", "Code"))
	foreignPrice := extractForeignPrice(row, &warnings)
	weight, location := extractWeightAndLocation(row, &warnings)
	resolution := provider.Resolve(ctx, code)
	warnings = append(warnings, resolution.Warnings...)
	warehouseStock := warehouseStock(row)
	totalStock := totalStock(row, warehouseStock, resolution.SelectedWarehouses, &warnings)

	var finalPrice *int64
	if foreignPrice != nil && weight != nil && resolution.FreightCNYPerKg != nil && resolution.MarkupPercent != nil && resolution.IRTPerCNY != nil {
		value, err := LandedPriceV1(weight.String(), resolution.FreightCNYPerKg.String(), foreignPrice.String(), resolution.MarkupPercent.String(), resolution.IRTPerCNY.String())
		if err != nil {
			warnings = append(warnings, "landed_price_calculation_failed")
		} else {
			finalPrice = &value
		}
	}
	if finalPrice == nil {
		warnings = append(warnings, "final_price_unavailable")
	}

	return Product{
		ProductCode:            code,
		Name:                   normalizeText(firstValue(row, "name", "Name", "part_number")),
		Serial:                 normalizeText(firstValue(row, "serial", "Serial")),
		Unit:                   normalizeText(firstValue(row, "unit", "Vahed")),
		SalePriceSource:        nullableNumber(firstValue(row, "sale_price_source", "FOROSH", "fee_kol")),
		PurchasePriceSource:    nullableNumber(firstValue(row, "purchase_price_source", "KHARYD", "purchase_price")),
		WarehouseStock:         warehouseStock,
		TotalStock:             totalStock,
		MinimumStock:           nullableNumber(firstValue(row, "minimum_stock", "Sefaresh", "minimum")),
		ForeignCurrency:        "CNY",
		ForeignPrice:           foreignPrice,
		WeightGrams:            weight,
		Location:               location,
		ImportFreightMethodID:  resolution.MethodID,
		FreightCNYPerKg:        resolution.FreightCNYPerKg,
		MarkupPercent:          resolution.MarkupPercent,
		IRTPerCNY:              resolution.IRTPerCNY,
		PricingCatalogRevision: resolution.CatalogRevision,
		PricingCatalogStatus:   resolution.CatalogStatus,
		CurrencyEffectiveDate:  resolution.CurrencyEffectiveDate,
		FinalPrice:             finalPrice,
		FormulaVersion:         FormulaVersion,
		SourceUpdatedAt:        normalizeText(firstValue(row, "source_updated_at", "updated_at", "Dates")),
		Warnings:               normalizedWarnings(warnings),
	}
}

// Map converts the typed wire contract into the generic row representation
// required by the existing spreadsheet and SQL sink boundary.
func (product Product) Map() map[string]interface{} {
	return map[string]interface{}{
		"product_code":             product.ProductCode,
		"name":                     product.Name,
		"serial":                   product.Serial,
		"unit":                     product.Unit,
		"sale_price_source":        pointerFloatValue(product.SalePriceSource),
		"purchase_price_source":    pointerFloatValue(product.PurchasePriceSource),
		"warehouse_stock":          product.WarehouseStock,
		"total_stock":              pointerFloatValue(product.TotalStock),
		"minimum_stock":            pointerFloatValue(product.MinimumStock),
		"foreign_currency":         product.ForeignCurrency,
		"foreign_price":            pointerDecimalValue(product.ForeignPrice),
		"weight_grams":             pointerDecimalValue(product.WeightGrams),
		"location":                 product.Location,
		"import_freight_method_id": product.ImportFreightMethodID,
		"freight_cny_per_kg":       pointerDecimalValue(product.FreightCNYPerKg),
		"markup_percent":           pointerDecimalValue(product.MarkupPercent),
		"irt_per_cny":              pointerDecimalValue(product.IRTPerCNY),
		"pricing_catalog_revision": product.PricingCatalogRevision,
		"pricing_catalog_status":   product.PricingCatalogStatus,
		"currency_effective_date":  product.CurrencyEffectiveDate,
		"final_price":              pointerIntValue(product.FinalPrice),
		"formula_version":          product.FormulaVersion,
		"source_updated_at":        product.SourceUpdatedAt,
		"warnings":                 append(make([]string, 0, len(product.Warnings)), product.Warnings...),
		"record_hash":              product.RecordHash,
	}
}

func recordHash(product Product) string {
	product.RecordHash = ""
	material, _ := json.Marshal(product)
	return hashBytes(material)
}

func ProductsToRows(products []Product) []map[string]interface{} {
	rows := make([]map[string]interface{}, 0, len(products))
	for _, product := range products {
		rows = append(rows, product.Map())
	}
	return rows
}

func extractForeignPrice(row map[string]interface{}, warnings *[]string) *pricingcatalog.Decimal {
	sources := map[string]pricingcatalog.Decimal{}
	for _, field := range []string{"foreign_price", "yuan_price"} {
		if value, ok := row[field]; ok {
			if parsed := positiveDecimal(value); parsed != nil {
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
		if decimalPositive(numbers[3]) {
			copy := numbers[3]
			return &copy
		}
		return nil
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
			if parsed := positiveDecimal(value); parsed != nil {
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
	parsed := positiveDecimal(value)
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
	if grams.Sign() <= 0 {
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

func warehouseStock(row map[string]interface{}) map[string]float64 {
	result := map[string]float64{}
	if existing, ok := row["warehouse_stock"].(map[string]interface{}); ok {
		for key, value := range existing {
			if number := nullableNumber(value); number != nil {
				result[strings.TrimSpace(key)] = *number
			}
		}
	}
	if value, ok := row["ANBAR"]; ok {
		reflected := reflect.ValueOf(value)
		if reflected.IsValid() && (reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array) {
			for index := 0; index < reflected.Len(); index++ {
				if number := nullableNumber(reflected.Index(index).Interface()); number != nil {
					result[strconv.Itoa(index+1)] = *number
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
			if number := nullableNumber(value); number != nil {
				result[index] = *number
			}
		}
	}
	return result
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
	parsed, err := pricingcatalog.NewDecimal(text)
	if err != nil {
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

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func floatPointer(value float64) *float64 { return &value }

func normalizedWarnings(values []string) []string {
	set := map[string]struct{}{}
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
