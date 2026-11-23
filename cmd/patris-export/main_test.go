package main

import (
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
	"github.com/spf13/cobra"
)

func TestWatchChangeStateBuildsChangesAfterInitialBaseline(t *testing.T) {
	state := &watchChangeState{}
	initial := recordpipe.Result{
		KeyField: "sku",
		Rows: []map[string]interface{}{
			{"sku": "100", "title": "Bolt", "price": 10},
		},
	}
	if changes := state.Next(initial, "initial", time.Unix(1, 0)); changes != nil {
		t.Fatalf("initial event must carry the full records payload, not a changeset: %#v", changes)
	}

	update := recordpipe.Result{
		KeyField: "sku",
		Rows: []map[string]interface{}{
			{"sku": "100", "title": "Bolt", "price": 12},
			{"sku": "200", "title": "Nut", "price": 5},
		},
	}
	changes := state.Next(update, "update", time.Unix(2, 0))
	if changes == nil {
		t.Fatal("watch update returned a nil changeset")
	}
	added, modified, deleted := changes.Counts()
	if added != 1 || modified != 1 || deleted != 0 {
		t.Fatalf("unexpected watch changes: added=%d modified=%d deleted=%d", added, modified, deleted)
	}
	if changes.Modified[0].Record["sku"] != "100" || changes.Modified[0].Record["title"] != "Bolt" {
		t.Fatalf("modified watch row is incomplete: %#v", changes.Modified[0].Record)
	}
}

func TestConvertXLSXFlagOverrides(t *testing.T) {
	cmd := &cobra.Command{Use: "convert"}
	cmd.Flags().StringVar(&xlsxLanguage, "xlsx-language", "", "")
	cmd.Flags().StringVar(&xlsxMode, "xlsx-mode", "", "")
	cmd.Flags().BoolVar(&xlsxZebraRows, "xlsx-zebra", true, "")
	if err := cmd.Flags().Set("xlsx-language", "fa"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("xlsx-mode", "formula"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("xlsx-zebra", "false"); err != nil {
		t.Fatal(err)
	}
	cfg := appconfig.Default()
	applyConvertFlagOverrides(cmd, &cfg)
	if cfg.Export.XLSXLanguage != "fa" || cfg.Export.XLSXMode != "formula" || cfg.Export.XLSXZebraRows {
		t.Fatalf("XLSX CLI overrides = %+v", cfg.Export)
	}
}

func TestSummarizeNamingWarningsIncludesCanonicalCategoryOnlyRows(t *testing.T) {
	result := recordpipe.Result{
		Rows: []map[string]interface{}{},
		Contract: &canonical.Envelope{Categories: []canonical.Category{{
			CategoryCode: "100",
			Name:         "Category",
			Warnings:     []string{"naming_multiple_spaces:name"},
		}}},
	}

	summary := summarizeNamingWarnings(result)
	if summary.Rows != 1 || summary.Violations != 1 {
		t.Fatalf("category-only naming summary = %+v, want one row and one violation", summary)
	}
}

func TestValidateServeEventDeliveryRejectsSilentCanonicalDisable(t *testing.T) {
	cfg := appconfig.Default()
	cfg.SendUpdates.Enabled = true
	cfg.SendUpdates.RequireContract = true
	if err := validateServeEventDelivery(false, cfg); err == nil {
		t.Fatal("canonical delivery was silently accepted with watching disabled")
	}
	if err := validateServeEventDelivery(true, cfg); err != nil {
		t.Fatalf("canonical delivery with event observation was rejected: %v", err)
	}
	hashesDisabled := false
	cfg.Canonical.Hashes.Enabled = &hashesDisabled
	if err := validateServeEventDelivery(true, cfg); err == nil {
		t.Fatal("canonical delivery without durable identity hashes was accepted")
	}
	hashesEnabled := true
	cfg.Canonical.Hashes.Enabled = &hashesEnabled

	cfg.SendUpdates.RequireContract = false
	cfg.SendUpdates.ProductSyncSecretEnv = "PATRIS_TEST_PRODUCT_SYNC_SECRET"
	if err := validateServeEventDelivery(false, cfg); err == nil {
		t.Fatal("authenticated canonical delivery was accepted with watching disabled")
	}
	cfg.SendUpdates.ProductSyncSecretEnv = ""
	if err := validateServeEventDelivery(false, cfg); err != nil {
		t.Fatalf("non-contract/manual delivery unexpectedly required watching: %v", err)
	}
}
