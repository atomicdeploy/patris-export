//go:build cgo && pxlib_cgo && pxlib_cgo_static

package paradox

import "testing"

func TestCGOStaticBackendIdentity(t *testing.T) {
	if got := NativeBackend(); got != "cgo-static" {
		t.Fatalf("NativeBackend() = %q, want cgo-static", got)
	}
}
