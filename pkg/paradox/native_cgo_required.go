//go:build pxlib_cgo && !cgo

package paradox

import "errors"

// This deliberately named unresolved identifier makes an accidental
// CGO_ENABLED=0 build fail at compile time with an actionable message.
var _ = pxlib_cgo_requires_CGO_ENABLED_1

func NativeBackend() string { return "pxlib_cgo requires CGO_ENABLED=1" }

func openPxlib() (*pxlib, error) {
	return nil, &NativeDependencyError{Library: "pxlib", Err: errors.New("pxlib_cgo build tag requires CGO_ENABLED=1")}
}

func pxLong(*pxVal) int64 { return 0 }
