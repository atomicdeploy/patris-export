//go:build !pxlib_cgo

package paradox

import "testing"

func TestRuntimeDynamicBackendIdentity(t *testing.T) {
	if got := NativeBackend(); got != "runtime-dynamic" {
		t.Fatalf("NativeBackend() = %q, want runtime-dynamic", got)
	}
}
