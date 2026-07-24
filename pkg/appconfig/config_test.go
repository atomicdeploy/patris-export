package appconfig

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

func TestLoadFilesLayersJSONYAMLTOML(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "base.json")
	yamlPath := filepath.Join(dir, "override.yaml")
	tomlPath := filepath.Join(dir, "local.toml")

	if err := os.WriteFile(jsonPath, []byte(`{"server":{"host":"127.0.0.1","port":18080},"database":{"path":"base.db"},"convert":{"format":"csv"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yamlPath, []byte("server:\n  port: 19090\nruntime:\n  temp_dir: tmp/patris\n  temp_strategy: memory\n  temp_memory_limit_mb: 64\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tomlPath, []byte("[ui]\npage_size = 250\nrtl_text_direction = true\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mgr, err := LoadFiles([]string{jsonPath, yamlPath, tomlPath})
	if err != nil {
		t.Fatalf("LoadFiles failed: %v", err)
	}
	cfg := mgr.Get()
	if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 19090 {
		t.Fatalf("server config was not layered correctly: %+v", cfg.Server)
	}
	if cfg.Database.Path != "base.db" {
		t.Fatalf("database path = %q", cfg.Database.Path)
	}
	if cfg.Convert.Format != "csv" {
		t.Fatalf("convert format = %q", cfg.Convert.Format)
	}
	if cfg.Runtime.TempDir != "tmp/patris" {
		t.Fatalf("temp dir = %q", cfg.Runtime.TempDir)
	}
	if cfg.Runtime.TempStrategy != "memory" || cfg.Runtime.TempMemoryLimitMB != 64 {
		t.Fatalf("runtime temp policy was not layered correctly: %+v", cfg.Runtime)
	}
	if cfg.UI.PageSize != 250 || !cfg.UI.RTLTextDirection {
		t.Fatalf("ui config was not layered correctly: %+v", cfg.UI)
	}
	if mgr.Path() != tomlPath {
		t.Fatalf("write path = %q, want %q", mgr.Path(), tomlPath)
	}
}

func TestTOMLStaticShippingPairUsesPresenceAwareDecoder(t *testing.T) {
	cfg := Default()
	data := []byte("[canonical.pricing]\nmode = 'static'\n[canonical.pricing.static.assignments.A]\nshipping_method_id = 'air'\nprofit_percent = 30\n[[canonical.pricing.static.shipping_methods]]\nid = 'air'\nprice_per_kg = 120\ncurrency = 'IRR'\n")
	if err := decodeConfig("patris-export.toml", data, &cfg); err != nil {
		t.Fatal(err)
	}
	resolution := pricingcatalog.NewProvider(cfg.Canonical.Pricing).Resolve(context.Background(), "A")
	if !resolution.ShippingPricePairPresent || resolution.ShippingPricePerKg == nil || resolution.ShippingPricePerKg.String() != "120" || resolution.ShippingPricePerKgCurrency != pricingcatalog.CurrencyIRR {
		t.Fatalf("TOML shipping pair presence was lost: %+v", resolution)
	}
}

func TestManagerSavesNativeFormat(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "patris-export.toml")
	mgr, err := LoadFiles([]string{tomlPath})
	if err != nil {
		t.Fatalf("LoadFiles failed: %v", err)
	}
	if err := mgr.Update(func(cfg *Config) {
		cfg.Server.Port = 18181
		cfg.Runtime.TempDir = "system"
	}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	data, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !containsAll(text, "[server]", "port = 18181") {
		t.Fatalf("saved TOML did not contain expected sections:\n%s", text)
	}
	if contains(text, "temp_dir") || contains(text, "[runtime]") {
		t.Fatalf("saved TOML should omit unchanged default runtime values:\n%s", text)
	}
}

func TestManagerSavesOnlyChangedValues(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "patris-export.json")
	mgr, err := LoadFiles([]string{jsonPath})
	if err != nil {
		t.Fatalf("LoadFiles failed: %v", err)
	}
	if err := mgr.Update(func(cfg *Config) {
		cfg.UI.Theme = "dark"
		cfg.UI.PageSize = 100
		cfg.Notifications.Native = true
		cfg.Notifications.MaxRows = 3
		cfg.Database.Path = "C:/Patris/data4/kala.db"
	}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !containsAll(text, `"ui"`, `"theme": "dark"`, `"database"`, `"path": "C:/Patris/data4/kala.db"`) {
		t.Fatalf("saved JSON did not contain changed values:\n%s", text)
	}
	for _, needle := range []string{`"page_size"`, `"notification_sound_source"`, `"notifications"`, `"column_labels"`, `"schema_version"`} {
		if contains(text, needle) {
			t.Fatalf("saved JSON should omit default key %s:\n%s", needle, text)
		}
	}
}

func TestUITableUXPersistsThroughConfigPath(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "patris-export.json")
	mgr, err := LoadFiles([]string{jsonPath})
	if err != nil {
		t.Fatalf("LoadFiles failed: %v", err)
	}
	if err := mgr.Update(func(cfg *Config) {
		cfg.UI.Language = "fa"
		cfg.UI.RTLTextDirection = true
		cfg.UI.EnableRowColoring = false
		cfg.UI.FreezeFirstColumn = false
		cfg.UI.ColumnWidths = map[string]int{"Code": 333, "product_code ": 444, "product_code": 211, "Name": 180, "name": 999}
		cfg.UI.HiddenColumns = []string{" warehouse_stock::North%20hub ", "warnings", "warnings", ""}
		cfg.UI.ColumnOrder = []string{
			"product_code", "name", "shipping_price_per_kg", "shipping_price_per_kg_currency",
			"warehouse_stock::North%20hub", "warehouse_stock::North%20hub",
		}
		cfg.UI.RowIconRules = []RowIconRule{{
			ID: "missing-price", Field: "final_price", Operator: "empty",
			Icon: "price", Color: "#dc2626", Label: "Price unavailable",
		}}
		cfg.UI.RowIconFallback = RowIconAppearance{Icon: "package", Color: "#64748b", Label: "Product"}
	}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	reloaded, err := LoadFiles([]string{jsonPath})
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	ui := reloaded.Get().UI
	if ui.Language != "fa" || !ui.RTLTextDirection || ui.EnableRowColoring || ui.FreezeFirstColumn {
		t.Fatalf("language, direction, coloring, or freeze toggle was not preserved: %+v", ui)
	}
	if ui.ColumnWidths["product_code"] != 211 || ui.ColumnWidths["name"] != 480 {
		t.Fatalf("canonical column widths were not persisted and clamped: %+v", ui.ColumnWidths)
	}
	if _, exists := ui.ColumnWidths["Code"]; exists {
		t.Fatalf("legacy Code width was not migrated: %+v", ui.ColumnWidths)
	}
	if got, want := strings.Join(ui.HiddenColumns, "|"), "warehouse_stock::North%20hub|warnings"; got != want {
		t.Fatalf("hidden columns were not persisted and normalized: got %q want %q", got, want)
	}
	if got, want := strings.Join(ui.ColumnOrder, "|"), "product_code|name|shipping_price_per_kg|shipping_price_per_kg_currency|warehouse_stock::North%20hub"; got != want {
		t.Fatalf("column order was not persisted and normalized: got %q want %q", got, want)
	}
	if len(ui.RowIconRules) != 1 || ui.RowIconRules[0].Field != "final_price" || ui.RowIconRules[0].Icon != "price" {
		t.Fatalf("ordered row icon rules were not preserved: %+v", ui.RowIconRules)
	}
	if ui.RowIconFallback.Icon != "package" || ui.RowIconFallback.Color != "#64748b" {
		t.Fatalf("row icon fallback was not preserved: %+v", ui.RowIconFallback)
	}
}

func TestUIColumnPreferenceClearsSurviveSparseRestart(t *testing.T) {
	for _, extension := range []string{"json", "yaml", "toml"} {
		t.Run(extension, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "patris-export."+extension)
			mgr, err := LoadFiles([]string{configPath})
			if err != nil {
				t.Fatalf("LoadFiles failed: %v", err)
			}
			if err := mgr.Update(func(cfg *Config) {
				cfg.UI.HiddenColumns = []string{}
				cfg.UI.ColumnOrder = []string{}
			}); err != nil {
				t.Fatalf("clear column preferences failed: %v", err)
			}

			data, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if text := string(data); !containsAll(text, "hidden_columns", "column_order") {
				t.Fatalf("explicit empty column preferences were dropped from sparse config:\n%s", text)
			}

			reloaded, err := LoadFiles([]string{configPath})
			if err != nil {
				t.Fatalf("restart reload failed: %v", err)
			}
			ui := reloaded.Get().UI
			if ui.HiddenColumns == nil || ui.ColumnOrder == nil || len(ui.HiddenColumns) != 0 || len(ui.ColumnOrder) != 0 {
				t.Fatalf("explicit cleared preferences did not survive restart: %+v", ui)
			}
			if err := reloaded.Save(); err != nil {
				t.Fatalf("second sparse save failed: %v", err)
			}
			secondRestart, err := LoadFiles([]string{configPath})
			if err != nil {
				t.Fatalf("second restart reload failed: %v", err)
			}
			ui = secondRestart.Get().UI
			if ui.HiddenColumns == nil || ui.ColumnOrder == nil {
				t.Fatalf("explicit cleared preferences were dropped after restart and save: %+v", ui)
			}
		})
	}
}

func TestApplyEnvNotificationOptions(t *testing.T) {
	t.Setenv("PATRIS_EXPORT_NOTIFICATIONS", "true")
	t.Setenv("PATRIS_EXPORT_NOTIFY_CLIENT_CONNECTED", "true")
	t.Setenv("PATRIS_EXPORT_NOTIFY_FILE_UPDATED", "true")
	t.Setenv("PATRIS_EXPORT_NOTIFY_ROW_UPDATED", "true")
	t.Setenv("PATRIS_EXPORT_NOTIFY_INCLUDE_ROW_VALUES", "true")
	t.Setenv("PATRIS_EXPORT_NOTIFY_MAX_ROWS", "7")

	cfg := Default()
	ApplyEnv(&cfg)
	if !cfg.Notifications.Enabled || !cfg.Notifications.ClientConnected || !cfg.Notifications.FileUpdated || !cfg.Notifications.RowUpdated {
		t.Fatalf("notification booleans were not applied: %+v", cfg.Notifications)
	}
	if !cfg.Notifications.IncludeRowValues || cfg.Notifications.MaxRows != 7 {
		t.Fatalf("notification details were not applied: %+v", cfg.Notifications)
	}
}

func TestApplyEnvRuntimeTempPolicy(t *testing.T) {
	t.Setenv("PATRIS_EXPORT_TEMP_STRATEGY", "tmpfs")
	t.Setenv("PATRIS_EXPORT_TEMP_MEMORY_LIMIT_MB", "42")

	cfg := Default()
	ApplyEnv(&cfg)
	if cfg.Runtime.TempStrategy != "memory" {
		t.Fatalf("temp strategy = %q", cfg.Runtime.TempStrategy)
	}
	if cfg.Runtime.TempMemoryLimitMB != 42 {
		t.Fatalf("temp memory limit = %d", cfg.Runtime.TempMemoryLimitMB)
	}
	if TempMemoryLimitBytes(cfg.Runtime.TempMemoryLimitMB) != 42*1024*1024 {
		t.Fatalf("unexpected temp memory limit bytes")
	}
}

func TestSQLExportDefaultsAndEnvironmentOverrides(t *testing.T) {
	cfg := Default()
	if cfg.Export.BatchSize != 500 || cfg.Export.Reconciliation != "upsert_only" || cfg.Export.DryRun || cfg.Export.MySQLConnectTimeout != "10s" {
		t.Fatalf("unsafe SQL export defaults: %+v", cfg.Export)
	}

	t.Setenv("PATRIS_EXPORT_BATCH_SIZE", "17")
	t.Setenv("PATRIS_EXPORT_SQL_RECONCILIATION", "DELETE_MISSING")
	t.Setenv("PATRIS_EXPORT_SQL_DRY_RUN", "true")
	t.Setenv("PATRIS_EXPORT_MYSQL_TLS_CA_FILE", " C:/protected/mysql/ca.pem ")
	t.Setenv("PATRIS_EXPORT_MYSQL_TLS_SERVER_NAME", " db.internal.example ")
	t.Setenv("PATRIS_EXPORT_MYSQL_CONNECT_TIMEOUT", "250ms")
	ApplyEnv(&cfg)
	if cfg.Export.BatchSize != 17 || cfg.Export.Reconciliation != "delete_missing" || !cfg.Export.DryRun ||
		cfg.Export.MySQLTLSCAFile != "C:/protected/mysql/ca.pem" || cfg.Export.MySQLTLSServerName != "db.internal.example" ||
		cfg.Export.MySQLConnectTimeout != "250ms" {
		t.Fatalf("SQL export environment was not applied: %+v", cfg.Export)
	}

	cfg.Export.Reconciliation = "typo_that_must_not_delete"
	cfg.Export.MySQLConnectTimeout = "24h"
	normalize(&cfg)
	if cfg.Export.Reconciliation != "upsert_only" {
		t.Fatalf("invalid reconciliation did not fail safe: %q", cfg.Export.Reconciliation)
	}
	if cfg.Export.MySQLConnectTimeout != "2m0s" {
		t.Fatalf("SQL connection timeout was not bounded: %q", cfg.Export.MySQLConnectTimeout)
	}
	cfg.Export.MySQLConnectTimeout = "not-a-duration"
	normalize(&cfg)
	if cfg.Export.MySQLConnectTimeout != "10s" {
		t.Fatalf("invalid SQL connection timeout did not use the safe default: %q", cfg.Export.MySQLConnectTimeout)
	}
	cfg.Export.Reconciliation = "SOFT_DELETE_MISSING"
	normalize(&cfg)
	if cfg.Export.Reconciliation != "soft_delete_missing" {
		t.Fatalf("soft-delete reconciliation was not preserved: %q", cfg.Export.Reconciliation)
	}
}

func TestXLSXExportDefaultsConfigAndEnvironment(t *testing.T) {
	cfg := Default()
	if cfg.Export.XLSXLanguage != "auto" || cfg.Export.XLSXMode != "precalculated" || !cfg.Export.XLSXZebraRows {
		t.Fatalf("unexpected XLSX defaults: %+v", cfg.Export)
	}

	t.Setenv("PATRIS_EXPORT_XLSX_LANGUAGE", "FA")
	t.Setenv("PATRIS_EXPORT_XLSX_MODE", "FORMULAS")
	t.Setenv("PATRIS_EXPORT_XLSX_ZEBRA_ROWS", "false")
	ApplyEnv(&cfg)
	if cfg.Export.XLSXLanguage != "fa" || cfg.Export.XLSXMode != "formula" || cfg.Export.XLSXZebraRows {
		t.Fatalf("XLSX environment was not normalized: %+v", cfg.Export)
	}

	cfg.Export.XLSXLanguage = "unsupported"
	cfg.Export.XLSXMode = "unsupported"
	normalize(&cfg)
	if cfg.Export.XLSXLanguage != "auto" || cfg.Export.XLSXMode != "precalculated" {
		t.Fatalf("invalid XLSX options did not fall back safely: %+v", cfg.Export)
	}
}

func TestCanonicalKalaProfileAndPricingProviderDefaults(t *testing.T) {
	cfg := Default()
	profile, exists := cfg.Canonical.Profiles["kala.db"]
	if !cfg.Canonical.Enabled || !exists || profile.Type != "kala" {
		t.Fatalf("kala canonical profile is not enabled by default: %+v", cfg.Canonical)
	}
	if cfg.Canonical.Pricing.Mode != "static" {
		t.Fatalf("offline static pricing must be the standalone default, got %q", cfg.Canonical.Pricing.Mode)
	}
}

func TestApplyEnvConfiguresDigitalogicWithoutStoringCredentials(t *testing.T) {
	t.Setenv("PATRIS_EXPORT_DIGITALOGIC_URL", "https://digitalogic.example/wp-json/digitalogic/")
	t.Setenv("PATRIS_EXPORT_DIGITALOGIC_USERNAME_ENV", "DIGITALOGIC_KEY")
	t.Setenv("PATRIS_EXPORT_DIGITALOGIC_PASSWORD_ENV", "DIGITALOGIC_SECRET")
	t.Setenv("PATRIS_EXPORT_DIGITALOGIC_BEARER_ENV", "DIGITALOGIC_PRICING_READ_TOKEN")
	t.Setenv("PATRIS_EXPORT_PRICING_FRESH_FOR", "2m")
	t.Setenv("PATRIS_EXPORT_PRICING_MAX_STALE", "30m")
	t.Setenv("PATRIS_EXPORT_PRICING_TIMEOUT", "20s")
	t.Setenv("PATRIS_EXPORT_PRICING_BATCH_SIZE", "250")
	t.Setenv("DIGITALOGIC_KEY", "must-not-be-copied")
	t.Setenv("DIGITALOGIC_SECRET", "must-not-be-copied")
	t.Setenv("DIGITALOGIC_PRICING_READ_TOKEN", "must-not-be-copied-bearer")

	cfg := Default()
	ApplyEnv(&cfg)
	digitalogic := cfg.Canonical.Pricing.Digitalogic
	if cfg.Canonical.Pricing.Mode != "digitalogic" || digitalogic.BaseURL != "https://digitalogic.example/wp-json/digitalogic" {
		t.Fatalf("Digitalogic pricing provider was not selected: %+v", cfg.Canonical.Pricing)
	}
	if digitalogic.UsernameEnv != "DIGITALOGIC_KEY" || digitalogic.PasswordEnv != "DIGITALOGIC_SECRET" || digitalogic.BearerTokenEnv != "DIGITALOGIC_PRICING_READ_TOKEN" || digitalogic.FreshFor != "2m" || digitalogic.MaxStale != "30m" || digitalogic.Timeout != "20s" || digitalogic.BatchSize != 250 {
		t.Fatalf("Digitalogic provider environment references were not normalized: %+v", digitalogic)
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "must-not-be-copied") {
		t.Fatalf("credential values were persisted in config: %s", encoded)
	}
}

func TestApplyEnvBoundsDigitalogicPricingBatchSizeAndTimeout(t *testing.T) {
	t.Setenv("PATRIS_EXPORT_DIGITALOGIC_URL", "https://digitalogic.example/wp-json/digitalogic/")
	t.Setenv("PATRIS_EXPORT_PRICING_TIMEOUT", "not-a-duration")
	t.Setenv("PATRIS_EXPORT_PRICING_BATCH_SIZE", "501")

	cfg := Default()
	ApplyEnv(&cfg)
	digitalogic := cfg.Canonical.Pricing.Digitalogic
	if digitalogic.Timeout != "15s" {
		t.Fatalf("invalid timeout did not normalize to the safe default: %+v", digitalogic)
	}
	if digitalogic.BatchSize != 500 {
		t.Fatalf("batch size was not bounded to the remote contract maximum: %+v", digitalogic)
	}
}

func TestEnvironmentCanRequireCanonicalOutboundContract(t *testing.T) {
	t.Setenv("PATRIS_EXPORT_SEND_REQUIRE_CONTRACT", "true")
	t.Setenv("PATRIS_EXPORT_SEND_ALLOW_RAW", "false")
	t.Setenv("PATRIS_EXPORT_SEND_RETRY_ATTEMPTS", "3")
	t.Setenv("PATRIS_EXPORT_SEND_RETRY_BACKOFF", "250ms")
	t.Setenv("PATRIS_EXPORT_SEND_PRODUCT_SYNC_SECRET_ENV", "PATRIS_PRODUCT_SYNC_SECRET")
	t.Setenv("PATRIS_PRODUCT_SYNC_SECRET", "must-not-enter-config")
	cfg := Default()
	ApplyEnv(&cfg)
	if !cfg.SendUpdates.RequireContract || cfg.SendUpdates.AllowRaw || cfg.SendUpdates.RetryAttempts != 3 || cfg.SendUpdates.RetryBackoff != "250ms" {
		t.Fatalf("outbound safety environment overrides were not applied: %+v", cfg.SendUpdates)
	}
	if cfg.SendUpdates.ProductSyncSecretEnv != "PATRIS_PRODUCT_SYNC_SECRET" {
		t.Fatalf("product-sync secret environment reference was not applied: %+v", cfg.SendUpdates)
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "must-not-enter-config") {
		t.Fatalf("product-sync secret value entered config: %s", encoded)
	}
}

func containsAll(text string, needles ...string) bool {
	for _, needle := range needles {
		if !contains(text, needle) {
			return false
		}
	}
	return true
}

func contains(text, needle string) bool {
	return strings.Contains(text, needle)
}
