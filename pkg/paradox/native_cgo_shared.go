//go:build cgo && pxlib_cgo && !pxlib_cgo_static

package paradox

/*
#cgo windows LDFLAGS: -lpxlib
#cgo linux freebsd netbsd LDFLAGS: -lpx
#cgo darwin LDFLAGS: -lpx
*/
import "C"

const cgoLinkMode = "cgo-shared"
