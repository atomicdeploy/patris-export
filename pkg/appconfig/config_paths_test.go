package appconfig

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestResolvePathsDiscoversSupportedUserConfigNames(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("APPDATA", configRoot)
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("HOME", configRoot)
	t.Setenv("PATRIS_EXPORT_CONFIG_FILES", "")
	t.Setenv("PATRIS_EXPORT_CONFIG_PATHS", "")
	t.Setenv("PATRIS_EXPORT_CONFIG", "")
	t.Setenv("PATRIS_EXPORT_CONFIG_FILE", "")

	paths := ResolvePaths(nil)
	want := filepath.Join(filepath.Dir(DefaultPath()), "patris-export.toml")
	if !slices.Contains(paths, want) {
		t.Fatalf("ResolvePaths did not include installer-supported user config %q: %#v", want, paths)
	}
}
