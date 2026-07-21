package recordpipe

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/recordsink"
)

// XLSXOptions maps the already-built pipeline result to non-secret workbook
// provenance. Keeping this adapter on Result ensures the CLI and HTTP route
// cannot invent a second canonical transformation.
func (result Result) XLSXOptions(dataset string, rightToLeft bool, values ...recordsink.XLSXPreferences) recordsink.XLSXOptions {
	metadata := recordsink.XLSXMetadata{
		Schema:        "patris-export.records",
		SourceDataset: filepath.Base(strings.TrimSpace(dataset)),
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if result.Contract != nil {
		contract := result.Contract
		metadata = recordsink.XLSXMetadata{
			Schema:         contract.Schema,
			FormulaID:      contract.FormulaID,
			LocalCurrency:  contract.LocalCurrency,
			SourceID:       contract.Source.ID,
			SourceDataset:  contract.Source.Dataset,
			SourceRevision: contract.Source.Revision,
			GeneratedAt:    contract.GeneratedAt,
			Warnings:       workbookWarnings(result),
		}
	}
	preferences := recordsink.XLSXPreferences{Language: "en", Mode: "precalculated", ZebraRows: true}
	if len(values) > 0 {
		preferences = values[len(values)-1]
	}
	zebraRows := preferences.ZebraRows
	return recordsink.XLSXOptions{
		RightToLeft:  rightToLeft,
		Language:     preferences.Language,
		Mode:         preferences.Mode,
		ZebraRows:    &zebraRows,
		ColumnLabels: preferences.ColumnLabels,
		Metadata:     metadata,
	}
}

func workbookWarnings(result Result) []string {
	if result.Contract == nil {
		return nil
	}
	seen := make(map[string]struct{})
	for _, warning := range result.Contract.Warnings {
		warning = strings.TrimSpace(warning)
		if warning != "" {
			seen[warning] = struct{}{}
		}
	}
	for _, product := range result.Contract.Products {
		for _, warning := range product.Warnings {
			warning = strings.TrimSpace(warning)
			if warning != "" {
				seen[warning] = struct{}{}
			}
		}
	}
	warnings := make([]string, 0, len(seen))
	for warning := range seen {
		warnings = append(warnings, warning)
	}
	sort.Strings(warnings)
	return warnings
}
