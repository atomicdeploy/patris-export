package recordsink

import (
	"sort"
	"strings"
	"unicode"
)

type xlsxLocalizedLabel struct {
	English string
	Persian string
}

// These labels are the workbook presentation vocabulary for the canonical
// schema. Machine keys remain in the JSON/CSV/API contract; Excel receives
// readable headings without changing or duplicating the transformed values.
var xlsxColumnLabels = map[string]xlsxLocalizedLabel{
	"product_code":                   {English: "Code", Persian: "کد"},
	"name":                           {English: "Name", Persian: "نام"},
	"category_code":                  {English: "Category Code", Persian: "کد دسته‌بندی"},
	"part_number":                    {English: "Part Number", Persian: "پارت نامبر"},
	"serial":                         {English: "Serial", Persian: "سریال"},
	"unit":                           {English: "Unit", Persian: "واحد"},
	"sale_price_source":              {English: "Sale Price (Source)", Persian: "قیمت فروش (منبع)"},
	"purchase_price_source":          {English: "Purchase Price (Source)", Persian: "قیمت خرید (منبع)"},
	"warehouse_stock":                {English: "Warehouse Stock", Persian: "موجودی انبار"},
	"total_stock":                    {English: "Total Stock", Persian: "موجودی کل"},
	"minimum_stock":                  {English: "Minimum Stock", Persian: "حداقل موجودی"},
	"foreign_currency":               {English: "Foreign Currency", Persian: "ارز خارجی"},
	"foreign_price":                  {English: "Foreign Price", Persian: "قیمت ارزی"},
	"weight_grams":                   {English: "Weight (g)", Persian: "وزن (گرم)"},
	"location":                       {English: "Location", Persian: "محل"},
	"shipping_method_id":             {English: "Shipping Method", Persian: "روش حمل"},
	"shipping_price_per_kg":          {English: "Shipping Price/kg", Persian: "هزینه حمل/کیلوگرم"},
	"shipping_price_per_kg_currency": {English: "Shipping Currency", Persian: "ارز هزینه حمل"},
	"markup_percent":                 {English: "Profit Margin (%)", Persian: "حاشیه سود (%)"},
	"irt_per_cny":                    {English: "IRT per CNY", Persian: "نرخ ریال به یوان"},
	"pricing_catalog_revision":       {English: "Pricing Revision", Persian: "نسخه قیمت‌گذاری"},
	"pricing_catalog_status":         {English: "Pricing Status", Persian: "وضعیت قیمت‌گذاری"},
	"currency_effective_date":        {English: "Currency Date", Persian: "تاریخ نرخ ارز"},
	"final_price":                    {English: "Final Price (IRT)", Persian: "قیمت نهایی (ریال)"},
	"formula_version":                {English: "Formula Version", Persian: "نسخه فرمول"},
	"source_updated_at":              {English: "Source Updated", Persian: "آخرین به‌روزرسانی منبع"},
	"warnings":                       {English: "Warnings", Persian: "هشدارها"},
	"record_hash":                    {English: "Record Hash", Persian: "هش رکورد"},
}

func xlsxHeaderLabel(field, warehouseID, language string, configured map[string]string) string {
	label, ok := xlsxColumnLabels[field]
	if configuredLabel := configuredXLSXLabel(field, language, configured); configuredLabel != "" {
		return appendXLSXWarehouseID(configuredLabel, warehouseID, language)
	}
	if !ok {
		return humanizeXLSXField(field)
	}
	base := label.English
	if language == "fa" {
		base = label.Persian
	}
	return appendXLSXWarehouseID(base, warehouseID, language)
}

func appendXLSXWarehouseID(base, warehouseID, language string) string {
	if warehouseID == "" {
		return base
	}
	if language == "fa" {
		return base + " " + toPersianDigits(warehouseID)
	}
	return base + " " + warehouseID
}

func configuredXLSXLabel(field, language string, configured map[string]string) string {
	aliases := []string{field}
	switch field {
	case "product_code":
		aliases = append(aliases, "Code")
	case "name":
		aliases = append(aliases, "Name")
	case "serial":
		aliases = append(aliases, "Serial")
	case "warehouse_stock":
		aliases = append(aliases, "ANBAR", "Warehouse")
	}
	for _, alias := range aliases {
		configuredValue, matched := configuredXLSXAliasValue(configured, alias)
		if !matched {
			continue
		}
		value := strings.TrimSpace(configuredValue)
		if value == "" || strings.EqualFold(value, field) {
			return ""
		}
		// Default English UI labels must not override the built-in Persian
		// workbook vocabulary when xlsx_language=fa/auto+fa.
		if language == "fa" {
			if localized, exists := xlsxColumnLabels[field]; exists && strings.EqualFold(value, localized.English) {
				return ""
			}
			if field == "warehouse_stock" && (strings.EqualFold(value, "Warehouse") || strings.EqualFold(value, "Stock")) {
				return ""
			}
		}
		return value
	}
	return ""
}

// configuredXLSXAliasValue gives the canonical machine key deterministic
// precedence over legacy UI aliases. Go intentionally randomizes map iteration,
// and layered configs commonly retain both forms (for example
// warehouse_stock and ANBAR), so ranging over the map can select the wrong
// custom heading. Exact spelling wins within one alias; equivalent
// case/whitespace variants use a stable lexical tie-breaker.
func configuredXLSXAliasValue(configured map[string]string, alias string) (string, bool) {
	if value, exists := configured[alias]; exists {
		return value, true
	}
	matchingKeys := make([]string, 0, 1)
	for key := range configured {
		if strings.EqualFold(strings.TrimSpace(key), alias) {
			matchingKeys = append(matchingKeys, key)
		}
	}
	if len(matchingKeys) == 0 {
		return "", false
	}
	sort.Strings(matchingKeys)
	return configured[matchingKeys[0]], true
}

func humanizeXLSXField(field string) string {
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(strings.TrimSpace(field)))
	for index, word := range words {
		runes := []rune(strings.ToLower(word))
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
		}
		words[index] = string(runes)
	}
	return strings.Join(words, " ")
}

func toPersianDigits(value string) string {
	return strings.NewReplacer(
		"0", "۰", "1", "۱", "2", "۲", "3", "۳", "4", "۴",
		"5", "۵", "6", "۶", "7", "۷", "8", "۸", "9", "۹",
	).Replace(value)
}
