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
func (result Result) XLSXOptions(dataset string, rightToLeft bool) recordsink.XLSXOptions {
	metadata := recordsink.XLSXMetadata{
		Schema:        "patris-export.records",
		SchemaVersion: "1",
		SourceDataset: filepath.Base(strings.TrimSpace(dataset)),
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if result.Contract != nil {
		contract := result.Contract
		metadata = recordsink.XLSXMetadata{
			Schema:          contract.Schema,
			SchemaVersion:   contract.SchemaVersion,
			FormulaID:       contract.FormulaID,
			FormulaRevision: contract.FormulaRevision,
			FormulaVersion:  contract.FormulaVersion,
			LocalCurrency:   contract.LocalCurrency,
			SourceID:        contract.Source.ID,
			SourceDataset:   contract.Source.Dataset,
			SourceRevision:  contract.Source.Revision,
			GeneratedAt:     contract.GeneratedAt,
			Warnings:        workbookWarnings(result),
		}
	}
	return recordsink.XLSXOptions{RightToLeft: rightToLeft, Metadata: metadata}
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
