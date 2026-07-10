package appconfig

import (
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
	if err := os.WriteFile(yamlPath, []byte("server:\n  port: 19090\nruntime:\n  temp_dir: tmp/patris\n"), 0644); err != nil {
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
	if !containsAll(text, "[server]", "port = 18181", "[runtime]") {
		t.Fatalf("saved TOML did not contain expected sections:\n%s", text)
	}
}

func TestApplyEnvNotificationOptions(t *testing.T) {
	t.Setenv("PATRIS_NOTIFICATIONS", "true")
	t.Setenv("PATRIS_NOTIFY_CLIENT_CONNECTED", "true")
	t.Setenv("PATRIS_NOTIFY_FILE_UPDATED", "true")
	t.Setenv("PATRIS_NOTIFY_ROW_UPDATED", "true")
	t.Setenv("PATRIS_NOTIFY_INCLUDE_ROW_VALUES", "true")
	t.Setenv("PATRIS_NOTIFY_MAX_ROWS", "7")

	cfg := Default()
	ApplyEnv(&cfg)
	if !cfg.Notifications.Enabled || !cfg.Notifications.ClientConnected || !cfg.Notifications.FileUpdated || !cfg.Notifications.RowUpdated {
		t.Fatalf("notification booleans were not applied: %+v", cfg.Notifications)
	}
	if !cfg.Notifications.IncludeRowValues || cfg.Notifications.MaxRows != 7 {
		t.Fatalf("notification details were not applied: %+v", cfg.Notifications)
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
