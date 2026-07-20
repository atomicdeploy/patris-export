//go:build cgo && pxlib_cgo && !pxlib_cgo_static

package paradox

import "testing"

func TestCGOSharedBackendIdentity(t *testing.T) {
	if got := NativeBackend(); got != "cgo-shared" {
		t.Fatalf("NativeBackend() = %q, want cgo-shared", got)
	}
}
