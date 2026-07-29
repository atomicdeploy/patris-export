package recordpipe

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/naming"
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
	Rows                []map[string]interface{}
	Payload             interface{}
	KeyField            string
	Raw                 bool
	Contract            *canonical.Envelope
	DisableSyncContract bool
}

func Build(rawRows []map[string]interface{}, source string, options Options) Result {
	return BuildContext(context.Background(), rawRows, source, options)
}

func BuildContext(ctx context.Context, rawRows []map[string]interface{}, source string, options Options) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return Result{}
	}
	if options.Raw {
		rows, err := copyRowsWithoutSortFieldsContext(ctx, rawRows)
		if err != nil {
			return Result{}
		}
		return Result{
			Rows:     rows,
			Payload:  rows,
			KeyField: recordmap.KeyField(options.Mapping, source),
			Raw:      true,
		}
	}

	records, err := mapsToParadoxContext(ctx, rawRows)
	if err != nil {
		return Result{}
	}
	exp := converter.NewExporter(converter.Patris2Fa)
	if _, ok := canonical.ProfileFor(source, options.Canonical); ok {
		converted, err := exp.ConvertRecordsContext(ctx, records)
		if err != nil {
			return Result{}
		}
		rows, err := paradoxToRowsContext(ctx, converted)
		if err != nil {
			return Result{}
		}
		for index, row := range rows {
			if ctx.Err() != nil {
				return Result{}
			}
			warnings := naming.Merge(nil, append(naming.Warnings(rawRows[index]), naming.Warnings(row)...))
			if len(warnings) > 0 {
				row[naming.InternalWarningsField] = warnings
			}
		}
		rows, contract, err := canonical.TransformContext(ctx, rows, source, options.Canonical, options.CatalogProvider, options.GeneratedAt)
		if err != nil {
			return Result{}
		}
		if !options.Canonical.ExposeRecordHashes() {
			rows = rowsWithoutRecordHashes(rows)
		}
		return Result{
			Rows:                rows,
			Payload:             canonical.OutputEnvelope(contract, options.Canonical),
			KeyField:            "product_code",
			Raw:                 false,
			Contract:            contract,
			DisableSyncContract: !options.Canonical.HashesEnabled(),
		}
	}
	converted, err := exp.ConvertRecordsContext(ctx, records)
	if err != nil {
		return Result{}
	}
	namingWarningsByCode := make(map[string][]string, len(converted))
	for index, record := range converted {
		if ctx.Err() != nil {
			return Result{}
		}
		code := fmt.Sprint(record["Code"])
		convertedRow := make(map[string]interface{}, len(record))
		for field, value := range record {
			if ctx.Err() != nil {
				return Result{}
			}
			convertedRow[field] = value
		}
		namingWarningsByCode[code] = naming.Merge(namingWarningsByCode[code], append(naming.Warnings(rawRows[index]), naming.Warnings(convertedRow)...))
	}
	keyed, err := exp.TransformRecordsContext(ctx, converted)
	if err != nil {
		return Result{}
	}
	rows, err := rowsFromKeyedContext(ctx, keyed, "Code")
	if err != nil {
		return Result{}
	}
	namingWarnings := make([][]string, len(rows))
	for index, row := range rows {
		if ctx.Err() != nil {
			return Result{}
		}
		namingWarnings[index] = naming.Merge(namingWarningsByCode[fmt.Sprint(row["Code"])], naming.Warnings(row))
	}
	rows, err = recordmap.ApplyContext(ctx, rows, options.Mapping, source)
	if err != nil {
		return Result{}
	}
	for index, warnings := range namingWarnings {
		if ctx.Err() != nil {
			return Result{}
		}
		if len(warnings) > 0 {
			rows[index]["warnings"] = naming.Merge(rows[index]["warnings"], warnings)
		}
	}
	keyField := recordmap.KeyField(options.Mapping, source)
	payload, err := recordmap.KeyedContext(ctx, rows, keyField, true)
	if err != nil {
		return Result{}
	}
	return Result{
		Rows:     rows,
		Payload:  payload,
		KeyField: keyField,
		Raw:      false,
	}
}

func copyRowsWithoutSortFields(records []map[string]interface{}) []map[string]interface{} {
	rows, _ := copyRowsWithoutSortFieldsContext(context.Background(), records)
	return rows
}

func rowsWithoutRecordHashes(records []map[string]interface{}) []map[string]interface{} {
	rows := recordmap.CopyRows(records)
	for _, row := range rows {
		delete(row, "record_hash")
	}
	return rows
}

func copyRowsWithoutSortFieldsContext(ctx context.Context, records []map[string]interface{}) ([]map[string]interface{}, error) {
	rows := make([]map[string]interface{}, 0, len(records))
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := make(map[string]interface{}, len(record))
		for field, value := range record {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !converter.IsSortField(field) {
				row[field] = value
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func paradoxToRows(records []paradox.Record) []map[string]interface{} {
	rows, _ := paradoxToRowsContext(context.Background(), records)
	return rows
}

func paradoxToRowsContext(ctx context.Context, records []paradox.Record) ([]map[string]interface{}, error) {
	rows := make([]map[string]interface{}, 0, len(records))
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := make(map[string]interface{}, len(record))
		for key, value := range record {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			row[key] = value
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (result Result) SyncEnvelope(changes *recorddiff.ChangeSet) *canonical.Envelope {
	if result.DisableSyncContract {
		return nil
	}
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
	records, _ := mapsToParadoxContext(context.Background(), rows)
	return records
}

func mapsToParadoxContext(ctx context.Context, rows []map[string]interface{}) ([]paradox.Record, error) {
	records := make([]paradox.Record, 0, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record := paradox.Record{}
		for key, value := range row {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			record[key] = value
		}
		records = append(records, record)
	}
	return records, nil
}

func rowsFromKeyed(keyed map[string]interface{}, keyField string) []map[string]interface{} {
	rows, _ := rowsFromKeyedContext(context.Background(), keyed, keyField)
	return rows
}

func rowsFromKeyedContext(ctx context.Context, keyed map[string]interface{}, keyField string) ([]map[string]interface{}, error) {
	if keyField == "" {
		keyField = "Code"
	}
	rows := make([]map[string]interface{}, 0, len(keyed))
	for key, value := range keyed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		row := make(map[string]interface{}, len(record)+1)
		row[keyField] = key
		for field, fieldValue := range record {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			row[field] = fieldValue
		}
		rows = append(rows, row)
	}
	return rows, nil
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
