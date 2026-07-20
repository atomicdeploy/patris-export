package paradox

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestNativeBackendIsExplicit(t *testing.T) {
	backend := NativeBackend()
	if backend != "runtime-dynamic" && !strings.HasPrefix(backend, "cgo-") {
		t.Fatalf("unexpected native backend %q", backend)
	}
}

func TestOpenReportsMissingNativeRuntime(t *testing.T) {
	if NativeBackend() != "runtime-dynamic" {
		t.Skip("missing-runtime discovery belongs to the runtime-dynamic backend")
	}
	t.Setenv("PATRIS_EXPORT_PXLIB_LIBRARY", filepath.Join(t.TempDir(), "missing-pxlib-runtime.dll"))
	resetNativeLoaderForTest()
	t.Cleanup(resetNativeLoaderForTest)

	_, err := Open(filepath.Join(t.TempDir(), "missing.db"))
	if err == nil {
		t.Fatal("Open returned nil error for a missing native runtime")
	}

	var depErr *NativeDependencyError
	if !errors.As(err, &depErr) {
		t.Fatalf("Open error type = %T, want NativeDependencyError: %v", err, err)
	}
	if depErr.Library != "pxlib" {
		t.Fatalf("Library = %q, want pxlib", depErr.Library)
	}
	if len(depErr.Attempts) != 1 {
		t.Fatalf("Attempts = %v, want exactly the explicit override", depErr.Attempts)
	}
}

func resetNativeLoaderForTest() {
	loadOnce = sync.Once{}
	loaded = nil
	loadErr = nil
}
