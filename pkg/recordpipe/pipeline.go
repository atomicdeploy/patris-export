package recordpipe

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/paradox"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/recorddiff"
	"github.com/atomicdeploy/patris-export/pkg/recordmap"
)

type Options struct {
	Raw             bool
	Mapping         recordmap.Config
	Canonical       canonical.Config
	CatalogProvider pricingcatalog.Provider
	GeneratedAt     time.Time
}

type Result struct {
	Rows     []map[string]interface{}
	Payload  interface{}
	KeyField string
	Raw      bool
	Contract *canonical.Envelope
}

func Build(rawRows []map[string]interface{}, source string, options Options) Result {
	return BuildContext(context.Background(), rawRows, source, options)
}

func BuildContext(ctx context.Context, rawRows []map[string]interface{}, source string, options Options) Result {
	if options.Raw {
		rows := copyRowsWithoutSortFields(rawRows)
		return Result{
			Rows:     rows,
			Payload:  rows,
			KeyField: recordmap.KeyField(options.Mapping, source),
			Raw:      true,
		}
	}

	records := mapsToParadox(rawRows)
	exp := converter.NewExporter(converter.Patris2Fa)
	if _, ok := canonical.ProfileFor(source, options.Canonical); ok {
		rows := paradoxToRows(exp.ConvertRecords(records))
		rows, contract := canonical.Transform(ctx, rows, source, options.Canonical, options.CatalogProvider, options.GeneratedAt)
		return Result{
			Rows:     rows,
			Payload:  contract,
			KeyField: "product_code",
			Raw:      false,
			Contract: contract,
		}
	}
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

func copyRowsWithoutSortFields(records []map[string]interface{}) []map[string]interface{} {
	rows := make([]map[string]interface{}, 0, len(records))
	for _, record := range records {
		row := make(map[string]interface{}, len(record))
		for field, value := range record {
			if !converter.IsSortField(field) {
				row[field] = value
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func paradoxToRows(records []paradox.Record) []map[string]interface{} {
	rows := make([]map[string]interface{}, 0, len(records))
	for _, record := range records {
		row := make(map[string]interface{}, len(record))
		for key, value := range record {
			row[key] = value
		}
		rows = append(rows, row)
	}
	return rows
}

func (result Result) SyncEnvelope(changes *recorddiff.ChangeSet) *canonical.Envelope {
	if changes == nil {
		return canonical.ChangeEnvelope(result.Contract, nil)
	}
	safe := result.FilterChanges(*changes)
	return canonical.ChangeEnvelope(result.Contract, &safe)
}

// FilterChanges prevents a duplicate-Code quarantine from being interpreted
// as a deletion. Quarantine means preserve the last known good downstream
// value until the source ambiguity is resolved; it is never a tombstone.
func (result Result) FilterChanges(changes recorddiff.ChangeSet) recorddiff.ChangeSet {
	if result.Contract == nil || len(result.Contract.QuarantinedCodes) == 0 || len(changes.Deleted) == 0 {
		return changes
	}
	protected := make(map[string]struct{}, len(result.Contract.QuarantinedCodes))
	for _, code := range result.Contract.QuarantinedCodes {
		protected[code] = struct{}{}
	}
	deleted := make([]string, 0, len(changes.Deleted))
	for _, code := range changes.Deleted {
		if _, quarantined := protected[code]; !quarantined {
			deleted = append(deleted, code)
		}
	}
	changes.Deleted = deleted
	return changes
}

func PayloadToRows(payload interface{}, keyField string) []map[string]interface{} {
	switch value := payload.(type) {
	case []map[string]interface{}:
		return recordmap.CopyRows(value)
	case map[string]interface{}:
		return rowsFromKeyed(value, keyField)
	case *canonical.Envelope:
		if value == nil {
			return nil
		}
		return canonical.ProductsToRows(value.Products)
	case canonical.Envelope:
		return canonical.ProductsToRows(value.Products)
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
