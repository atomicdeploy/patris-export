package recordpipe

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/paradox"
	"github.com/atomicdeploy/patris-export/pkg/recordmap"
)

type Options struct {
	Raw     bool
	Mapping recordmap.Config
}

type Result struct {
	Rows     []map[string]interface{}
	Payload  interface{}
	KeyField string
	Raw      bool
}

func Build(rawRows []map[string]interface{}, source string, options Options) Result {
	if options.Raw {
		rows := recordmap.CopyRows(rawRows)
		return Result{
			Rows:     rows,
			Payload:  rows,
			KeyField: recordmap.KeyField(options.Mapping, source),
			Raw:      true,
		}
	}

	records := mapsToParadox(rawRows)
	exp := converter.NewExporter(converter.Patris2Fa)
	keyed := exp.ConvertAndTransformRecords(records)
	rows := rowsFromKeyed(keyed, "Code")
	rows = recordmap.Apply(rows, options.Mapping, source)
	keyField := recordmap.KeyField(options.Mapping, source)
	payload := recordmap.Keyed(rows, keyField, true)
	return Result{
		Rows:     rows,
		Payload:  payload,
		KeyField: keyField,
		Raw:      false,
	}
}

func PayloadToRows(payload interface{}, keyField string) []map[string]interface{} {
	switch value := payload.(type) {
	case []map[string]interface{}:
		return recordmap.CopyRows(value)
	case map[string]interface{}:
		return rowsFromKeyed(value, keyField)
	default:
		return nil
	}
}

func SourceTableName(source string) string {
	base := filepath.Base(strings.TrimSpace(source))
	ext := filepath.Ext(base)
	if ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" || base == "." {
		return "patris_export"
	}
	return sanitizeIdentifier(base)
}

func mapsToParadox(rows []map[string]interface{}) []paradox.Record {
	records := make([]paradox.Record, 0, len(rows))
	for _, row := range rows {
		record := paradox.Record{}
		for key, value := range row {
			record[key] = value
		}
		records = append(records, record)
	}
	return records
}

func rowsFromKeyed(keyed map[string]interface{}, keyField string) []map[string]interface{} {
	if keyField == "" {
		keyField = "Code"
	}
	rows := make([]map[string]interface{}, 0, len(keyed))
	for key, value := range keyed {
		record, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		row := make(map[string]interface{}, len(record)+1)
		row[keyField] = key
		for field, fieldValue := range record {
			row[field] = fieldValue
		}
		rows = append(rows, row)
	}
	return rows
}

func sanitizeIdentifier(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "patris_export"
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = fmt.Sprintf("t_%s", out)
	}
	return out
}
