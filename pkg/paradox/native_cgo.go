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
		pxNew: func() unsafe.Pointer {
			return unsafe.Pointer(C.PX_new())
		},
		pxOpenFile: func(document unsafe.Pointer, path string) int32 {
			cPath := C.CString(path)
			defer C.free(unsafe.Pointer(cPath))
			return int32(C.PX_open_file((*C.pxdoc_t)(document), cPath))
		},
		pxClose: func(document unsafe.Pointer) {
			C.PX_close((*C.pxdoc_t)(document))
		},
		pxDelete: func(document unsafe.Pointer) {
			C.PX_delete((*C.pxdoc_t)(document))
		},
		pxGetNumFields: func(document unsafe.Pointer) int32 {
			return int32(C.PX_get_num_fields((*C.pxdoc_t)(document)))
		},
		pxGetNumRecords: func(document unsafe.Pointer) int32 {
			return int32(C.PX_get_num_records((*C.pxdoc_t)(document)))
		},
		pxGetField: func(document unsafe.Pointer, index int32) *pxField {
			return (*pxField)(unsafe.Pointer(C.PX_get_field((*C.pxdoc_t)(document), C.int(index))))
		},
		pxRetrieveRecord: func(document unsafe.Pointer, index int32) **pxVal {
			return (**pxVal)(unsafe.Pointer(C.PX_retrieve_record((*C.pxdoc_t)(document), C.int(index))))
		},
	}, nil
}

func pxLong(value *pxVal) int64 {
	return int64(C.patris_px_long((*C.pxval_t)(unsafe.Pointer(value))))
}
