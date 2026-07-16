package appconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestCanonicalKalaProfileAndPricingProviderDefaults(t *testing.T) {
	cfg := Default()
	profile, exists := cfg.Canonical.Profiles["kala.db"]
	if !cfg.Canonical.Enabled || !exists || profile.Type != "kala_v1" {
		t.Fatalf("kala canonical profile is not enabled by default: %+v", cfg.Canonical)
	}
	if cfg.Canonical.Pricing.Mode != "static" {
		t.Fatalf("offline static pricing must be the standalone default, got %q", cfg.Canonical.Pricing.Mode)
	}
}

func TestApplyEnvConfiguresDigitalogicWithoutStoringCredentials(t *testing.T) {
	t.Setenv("PATRIS_EXPORT_DIGITALOGIC_URL", "https://digitalogic.example/wp-json/digitalogic/v1/")
	t.Setenv("PATRIS_EXPORT_DIGITALOGIC_USERNAME_ENV", "DIGITALOGIC_KEY")
	t.Setenv("PATRIS_EXPORT_DIGITALOGIC_PASSWORD_ENV", "DIGITALOGIC_SECRET")
	t.Setenv("PATRIS_EXPORT_DIGITALOGIC_BEARER_ENV", "DIGITALOGIC_PRICING_READ_TOKEN")
	t.Setenv("PATRIS_EXPORT_PRICING_FRESH_FOR", "2m")
	t.Setenv("PATRIS_EXPORT_PRICING_MAX_STALE", "30m")
	t.Setenv("DIGITALOGIC_KEY", "must-not-be-copied")
	t.Setenv("DIGITALOGIC_SECRET", "must-not-be-copied")
	t.Setenv("DIGITALOGIC_PRICING_READ_TOKEN", "must-not-be-copied-bearer")

	cfg := Default()
	ApplyEnv(&cfg)
	digitalogic := cfg.Canonical.Pricing.Digitalogic
	if cfg.Canonical.Pricing.Mode != "digitalogic" || digitalogic.BaseURL != "https://digitalogic.example/wp-json/digitalogic/v1" {
		t.Fatalf("Digitalogic pricing provider was not selected: %+v", cfg.Canonical.Pricing)
	}
	if digitalogic.UsernameEnv != "DIGITALOGIC_KEY" || digitalogic.PasswordEnv != "DIGITALOGIC_SECRET" || digitalogic.BearerTokenEnv != "DIGITALOGIC_PRICING_READ_TOKEN" || digitalogic.FreshFor != "2m" || digitalogic.MaxStale != "30m" {
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

func TestEnvironmentCanRequireCanonicalOutboundContract(t *testing.T) {
	t.Setenv("PATRIS_EXPORT_SEND_REQUIRE_CONTRACT", "true")
	t.Setenv("PATRIS_EXPORT_SEND_ALLOW_RAW", "false")
	t.Setenv("PATRIS_EXPORT_SEND_RETRY_ATTEMPTS", "3")
	t.Setenv("PATRIS_EXPORT_SEND_RETRY_BACKOFF", "250ms")
	t.Setenv("PATRIS_EXPORT_SEND_PRODUCT_SYNC_SECRET_ENV", "DIGITALOGIC_PRODUCT_SYNC_SECRET")
	t.Setenv("DIGITALOGIC_PRODUCT_SYNC_SECRET", "must-not-enter-config")
	cfg := Default()
	ApplyEnv(&cfg)
	if !cfg.SendUpdates.RequireContract || cfg.SendUpdates.AllowRaw || cfg.SendUpdates.RetryAttempts != 3 || cfg.SendUpdates.RetryBackoff != "250ms" {
		t.Fatalf("outbound safety environment overrides were not applied: %+v", cfg.SendUpdates)
	}
	if cfg.SendUpdates.ProductSyncSecretEnv != "DIGITALOGIC_PRODUCT_SYNC_SECRET" {
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
