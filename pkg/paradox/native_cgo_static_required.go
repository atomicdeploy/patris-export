//go:build pxlib_cgo_static && !pxlib_cgo

package paradox

// The static selector refines the direct-CGO backend; using it alone must not
// silently fall back to the runtime-dynamic loader.
var _ = pxlib_cgo_static_requires_pxlib_cgo
