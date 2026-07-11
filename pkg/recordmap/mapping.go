package recordmap

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Config describes optional key/value mapping and table-specific field rules.
// It is intentionally data-only so it can be embedded in JSON/YAML/TOML config
// files and reused by CLI, HTTP, WebSocket, and outbound sync paths.
type Config struct {
	Enabled     bool                   `json:"enabled" yaml:"enabled" toml:"enabled"`
	MappingFile string                 `json:"mapping_file,omitempty" yaml:"mapping_file,omitempty" toml:"mapping_file,omitempty"`
	KeyField    string                 `json:"key_field,omitempty" yaml:"key_field,omitempty" toml:"key_field,omitempty"`
	Fields      map[string]string      `json:"fields,omitempty" yaml:"fields,omitempty" toml:"fields,omitempty"`
	Values      map[string]ValueMap    `json:"values,omitempty" yaml:"values,omitempty" toml:"values,omitempty"`
	Defaults    map[string]interface{} `json:"defaults,omitempty" yaml:"defaults,omitempty" toml:"defaults,omitempty"`
	Include     []string               `json:"include,omitempty" yaml:"include,omitempty" toml:"include,omitempty"`
	Drop        []string               `json:"drop,omitempty" yaml:"drop,omitempty" toml:"drop,omitempty"`
	Numeric     map[string]NumericRule `json:"numeric,omitempty" yaml:"numeric,omitempty" toml:"numeric,omitempty"`
	Tables      map[string]TableConfig `json:"tables,omitempty" yaml:"tables,omitempty" toml:"tables,omitempty"`
}

type TableConfig struct {
	Enabled  *bool                  `json:"enabled,omitempty" yaml:"enabled,omitempty" toml:"enabled,omitempty"`
	KeyField string                 `json:"key_field,omitempty" yaml:"key_field,omitempty" toml:"key_field,omitempty"`
	Fields   map[string]string      `json:"fields,omitempty" yaml:"fields,omitempty" toml:"fields,omitempty"`
	Values   map[string]ValueMap    `json:"values,omitempty" yaml:"values,omitempty" toml:"values,omitempty"`
	Defaults map[string]interface{} `json:"defaults,omitempty" yaml:"defaults,omitempty" toml:"defaults,omitempty"`
	Include  []string               `json:"include,omitempty" yaml:"include,omitempty" toml:"include,omitempty"`
	Drop     []string               `json:"drop,omitempty" yaml:"drop,omitempty" toml:"drop,omitempty"`
	Numeric  map[string]NumericRule `json:"numeric,omitempty" yaml:"numeric,omitempty" toml:"numeric,omitempty"`
}

type ValueMap map[string]interface{}

type NumericRule struct {
	Multiplier float64 `json:"multiplier,omitempty" yaml:"multiplier,omitempty" toml:"multiplier,omitempty"`
	Add        float64 `json:"add,omitempty" yaml:"add,omitempty" toml:"add,omitempty"`
	Round      *int    `json:"round,omitempty" yaml:"round,omitempty" toml:"round,omitempty"`
}

// LoadFile reads a mapping config from JSON. The main application config still
// supports YAML/TOML, but standalone mapping files are JSON so users can copy
// examples directly into other systems without format ambiguity.
func LoadFile(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse mapping file %s: %w", path, err)
	}
	if !cfg.Enabled {
		cfg.Enabled = true
	}
	return cfg, nil
}

func Merge(base, overlay Config) Config {
	out := base
	if overlay.Enabled {
		out.Enabled = true
	}
	if overlay.MappingFile != "" {
		out.MappingFile = overlay.MappingFile
	}
	if overlay.KeyField != "" {
		out.KeyField = overlay.KeyField
	}
	out.Fields = mergeStringMap(out.Fields, overlay.Fields)
	out.Values = mergeValueMaps(out.Values, overlay.Values)
	out.Defaults = mergeInterfaceMap(out.Defaults, overlay.Defaults)
	out.Numeric = mergeNumericMap(out.Numeric, overlay.Numeric)
	if len(overlay.Include) > 0 {
		out.Include = append([]string(nil), overlay.Include...)
	}
	if len(overlay.Drop) > 0 {
		out.Drop = append([]string(nil), overlay.Drop...)
	}
	if len(overlay.Tables) > 0 {
		if out.Tables == nil {
			out.Tables = map[string]TableConfig{}
		}
		for key, table := range overlay.Tables {
			out.Tables[strings.ToLower(strings.TrimSpace(key))] = table
		}
	}
	return out
}

func Effective(cfg Config, source string) Config {
	source = strings.ToLower(filepath.Base(strings.TrimSpace(source)))
	out := cfg
	if cfg.KeyField == "" {
		out.KeyField = "Code"
	}
	if cfg.Tables == nil {
		return out
	}
	table, ok := cfg.Tables[source]
	if !ok {
		table, ok = cfg.Tables["*"]
	}
	if !ok {
		return out
	}
	if table.Enabled != nil {
		out.Enabled = *table.Enabled
	}
	if table.KeyField != "" {
		out.KeyField = table.KeyField
	}
	out.Fields = mergeStringMap(out.Fields, table.Fields)
	out.Values = mergeValueMaps(out.Values, table.Values)
	out.Defaults = mergeInterfaceMap(out.Defaults, table.Defaults)
	out.Numeric = mergeNumericMap(out.Numeric, table.Numeric)
	if len(table.Include) > 0 {
		out.Include = append([]string(nil), table.Include...)
	}
	if len(table.Drop) > 0 {
		out.Drop = append([]string(nil), table.Drop...)
	}
	if mappedKey := out.Fields["Code"]; out.KeyField == "Code" && mappedKey != "" {
		out.KeyField = mappedKey
	}
	return out
}

func Apply(records []map[string]interface{}, cfg Config, source string) []map[string]interface{} {
	effective := Effective(cfg, source)
	if !effective.Enabled {
		return CopyRows(records)
	}
	include := stringSet(effective.Include)
	drop := stringSet(effective.Drop)
	out := make([]map[string]interface{}, 0, len(records))
	for _, record := range records {
		next := map[string]interface{}{}
		for key, value := range record {
			if len(include) > 0 && !include[key] {
				continue
			}
			if drop[key] {
				continue
			}
			value = mapValue(value, effective.Values[key])
			if rule, ok := effective.Numeric[key]; ok {
				value = applyNumeric(value, rule)
			}
			dest := key
			if renamed := strings.TrimSpace(effective.Fields[key]); renamed != "" {
				dest = renamed
			}
			next[dest] = value
		}
		for key, value := range effective.Defaults {
			if _, exists := next[key]; !exists {
				next[key] = value
			}
		}
		out = append(out, next)
	}
	return out
}

func KeyField(cfg Config, source string) string {
	effective := Effective(cfg, source)
	if effective.KeyField == "" {
		return "Code"
	}
	return effective.KeyField
}

func Keyed(records []map[string]interface{}, keyField string, omitKey bool) map[string]interface{} {
	if strings.TrimSpace(keyField) == "" {
		keyField = "Code"
	}
	result := make(map[string]interface{}, len(records))
	for index, record := range records {
		key := strings.TrimSpace(fmt.Sprintf("%v", record[keyField]))
		if key == "" || key == "<nil>" {
			key = fmt.Sprintf("row-%d", index+1)
		}
		value := map[string]interface{}{}
		for field, fieldValue := range record {
			if omitKey && field == keyField {
				continue
			}
			value[field] = fieldValue
		}
		result[key] = value
	}
	return result
}

func CopyRows(records []map[string]interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(records))
	for _, record := range records {
		next := make(map[string]interface{}, len(record))
		for key, value := range record {
			next[key] = value
		}
		out = append(out, next)
	}
	return out
}

func Fields(records []map[string]interface{}, keyField string) []string {
	seen := map[string]bool{}
	fields := []string{}
	if keyField != "" {
		seen[keyField] = true
		fields = append(fields, keyField)
	}
	for _, record := range records {
		for field := range record {
			if !seen[field] {
				seen[field] = true
				fields = append(fields, field)
			}
		}
	}
	if len(fields) <= 1 {
		return fields
	}
	prefix := []string{}
	rest := fields
	if keyField != "" && fields[0] == keyField {
		prefix = fields[:1]
		rest = fields[1:]
	}
	sort.Strings(rest)
	return append(prefix, rest...)
}

func mapValue(value interface{}, valueMap ValueMap) interface{} {
	if len(valueMap) == 0 {
		return value
	}
	key := fmt.Sprintf("%v", value)
	if mapped, ok := valueMap[key]; ok {
		return mapped
	}
	return value
}

func applyNumeric(value interface{}, rule NumericRule) interface{} {
	number, ok := toFloat(value)
	if !ok {
		return value
	}
	if rule.Multiplier != 0 {
		number *= rule.Multiplier
	}
	number += rule.Add
	if rule.Round != nil {
		scale := math.Pow(10, float64(*rule.Round))
		number = math.Round(number*scale) / scale
		if *rule.Round == 0 {
			return int64(number)
		}
	}
	return number
}

func toFloat(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func stringSet(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func mergeStringMap(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func mergeInterfaceMap(a, b map[string]interface{}) map[string]interface{} {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := map[string]interface{}{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func mergeValueMaps(a, b map[string]ValueMap) map[string]ValueMap {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := map[string]ValueMap{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func mergeNumericMap(a, b map[string]NumericRule) map[string]NumericRule {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := map[string]NumericRule{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
