//go:build (darwin || freebsd || linux || netbsd) && !pxlib_cgo

package paradox

import (
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

func openLibrary(name string) (uintptr, error) {
	return purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

func closeLibrary(handle uintptr) error {
	return purego.Dlclose(handle)
}

func lookupSymbol(handle uintptr, name string) (uintptr, error) {
	return purego.Dlsym(handle, name)
}

func prepareLibraryLoad() {
	if runtime.GOOS != "linux" {
		return
	}
	_, _ = purego.Dlopen("libm.so.6", purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

func pxLong(value *pxVal) int64 {
	// C long follows the platform data model: four bytes on ILP32 and eight
	// bytes on LP64. Reading eight bytes on a 32-bit target loses the sign when
	// the adjacent union storage is zero, for example -1 becomes 4294967295.
	if unsafe.Sizeof(uintptr(0)) == 4 {
		return int64(*(*int32)(unsafe.Pointer(&value.value[0])))
	}
	return *(*int64)(unsafe.Pointer(&value.value[0]))
}
