//go:build windows && !pxlib_cgo

package paradox

import (
	"syscall"
	"unsafe"
)

func openLibrary(name string) (uintptr, error) {
	handle, err := syscall.LoadLibrary(name)
	return uintptr(handle), err
}

func closeLibrary(handle uintptr) error {
	return syscall.FreeLibrary(syscall.Handle(handle))
}

func lookupSymbol(handle uintptr, name string) (uintptr, error) {
	return syscall.GetProcAddress(syscall.Handle(handle), name)
}

func prepareLibraryLoad() {}

func pxLong(value *pxVal) int64 {
	return int64(*(*int32)(unsafe.Pointer(&value.value[0])))
}
