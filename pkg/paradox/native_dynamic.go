//go:build !pxlib_cgo

package paradox

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ebitengine/purego"
)

// NativeBackend identifies the selected pxlib binding strategy.
func NativeBackend() string { return "runtime-dynamic" }

func openPxlib() (*pxlib, error) {
	prepareLibraryLoad()
	candidates := pxlibCandidates()
	var attempts []string
	var lastErr error
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			if _, err := os.Stat(candidate); err != nil {
				lastErr = err
				attempts = append(attempts, candidate)
				continue
			}
		}
		handle, err := openLibrary(candidate)
		attempts = append(attempts, candidate)
		if err != nil {
			lastErr = err
			continue
		}
		lib := &pxlib{handle: handle}
		if err := lib.bind(); err != nil {
			_ = closeLibrary(handle)
			return nil, &NativeDependencyError{Library: "pxlib", Attempts: attempts, Err: err}
		}
		return lib, nil
	}
	return nil, &NativeDependencyError{Library: "pxlib", Attempts: attempts, Err: lastErr}
}

func (lib *pxlib) bind() error {
	bindings := []struct {
		name string
		fn   any
	}{
		{"PX_boot", &lib.pxBoot},
		{"PX_shutdown", &lib.pxShutdown},
		{"PX_new", &lib.pxNew},
		{"PX_open_file", &lib.pxOpenFile},
		{"PX_close", &lib.pxClose},
		{"PX_delete", &lib.pxDelete},
		{"PX_get_num_fields", &lib.pxGetNumFields},
		{"PX_get_num_records", &lib.pxGetNumRecords},
		{"PX_get_field", &lib.pxGetField},
		{"PX_retrieve_record", &lib.pxRetrieveRecord},
	}
	for _, binding := range bindings {
		if err := bindSymbol(lib.handle, binding.name, binding.fn); err != nil {
			return err
		}
	}
	return nil
}

func bindSymbol(handle uintptr, name string, fn any) (err error) {
	sym, err := lookupSymbol(handle, name)
	if err != nil {
		return fmt.Errorf("missing pxlib symbol %s: %w", name, err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("bind pxlib symbol %s: %v", name, recovered)
		}
	}()
	purego.RegisterFunc(fn, sym)
	return nil
}

func pxlibCandidates() []string {
	if exact := firstNonEmpty(os.Getenv("PATRIS_EXPORT_PXLIB_LIBRARY"), os.Getenv("PXLIB_LIBRARY")); exact != "" {
		return []string{exact}
	}

	seen := map[string]bool{}
	var candidates []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		candidates = append(candidates, path)
	}

	if root := firstNonEmpty(os.Getenv("PATRIS_EXPORT_PXLIB_ROOT"), os.Getenv("PXLIB_ROOT")); root != "" {
		for _, name := range platformLibraryNames() {
			add(filepath.Join(root, "bin", name))
			add(filepath.Join(root, "lib", name))
			add(filepath.Join(root, name))
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, name := range platformLibraryNames() {
			add(filepath.Join(dir, name))
			add(filepath.Join(dir, "lib", name))
		}
	}
	for _, name := range platformLibraryNames() {
		add(name)
	}
	return candidates
}

func platformLibraryNames() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"libpxlib.dll", "pxlib.dll"}
	case "linux":
		return []string{"libpx.so", "libpx.so.0", "libpxlib.so"}
	case "darwin":
		return []string{"libpx.dylib", "libpxlib.dylib"}
	default:
		return []string{"libpx.so", "libpxlib.so"}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
