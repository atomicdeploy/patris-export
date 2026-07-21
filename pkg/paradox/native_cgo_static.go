//go:build cgo && pxlib_cgo && pxlib_cgo_static

package paradox

/*
#cgo windows LDFLAGS: -lpxlib_static
#cgo linux freebsd netbsd LDFLAGS: -lpx_static -lm
#cgo darwin LDFLAGS: -lpx_static
*/
import "C"

const cgoLinkMode = "cgo-static"
