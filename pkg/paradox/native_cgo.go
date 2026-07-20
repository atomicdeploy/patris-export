//go:build cgo && pxlib_cgo

package paradox

/*
#include <stdint.h>
#include <stdlib.h>
#include <paradox.h>

static long long patris_px_long(pxval_t *value) {
	return value == NULL ? 0 : (long long)value->value.lval;
}
*/
import "C"

import "unsafe"

// NativeBackend identifies the selected pxlib binding strategy and link mode.
func NativeBackend() string { return cgoLinkMode }

func openPxlib() (*pxlib, error) {
	return &pxlib{
		pxBoot:     func() { C.PX_boot() },
		pxShutdown: func() { C.PX_shutdown() },
		pxNew: func() uintptr {
			return uintptr(unsafe.Pointer(C.PX_new()))
		},
		pxOpenFile: func(document uintptr, path string) int32 {
			cPath := C.CString(path)
			defer C.free(unsafe.Pointer(cPath))
			return int32(C.PX_open_file((*C.pxdoc_t)(unsafe.Pointer(document)), cPath))
		},
		pxClose: func(document uintptr) {
			C.PX_close((*C.pxdoc_t)(unsafe.Pointer(document)))
		},
		pxDelete: func(document uintptr) {
			C.PX_delete((*C.pxdoc_t)(unsafe.Pointer(document)))
		},
		pxGetNumFields: func(document uintptr) int32 {
			return int32(C.PX_get_num_fields((*C.pxdoc_t)(unsafe.Pointer(document))))
		},
		pxGetNumRecords: func(document uintptr) int32 {
			return int32(C.PX_get_num_records((*C.pxdoc_t)(unsafe.Pointer(document))))
		},
		pxGetField: func(document uintptr, index int32) uintptr {
			return uintptr(unsafe.Pointer(C.PX_get_field((*C.pxdoc_t)(unsafe.Pointer(document)), C.int(index))))
		},
		pxRetrieveRecord: func(document uintptr, index int32) uintptr {
			return uintptr(unsafe.Pointer(C.PX_retrieve_record((*C.pxdoc_t)(unsafe.Pointer(document)), C.int(index))))
		},
	}, nil
}

func pxLong(value *pxVal) int64 {
	return int64(C.patris_px_long((*C.pxval_t)(unsafe.Pointer(value))))
}
