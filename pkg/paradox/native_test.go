package paradox

import (
	"errors"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"unsafe"
)

func TestPxlibABILayout(t *testing.T) {
	pointerSize := unsafe.Sizeof(uintptr(0))
	fieldLengthOffset := alignUp(pointerSize+1, unsafe.Alignof(int32(0)))
	fieldSize := alignUp(fieldLengthOffset+2*unsafe.Sizeof(int32(0)), pointerSize)
	valueSize := uintptr(8) + 2*pointerSize
	stringSize := alignUp(pointerSize+unsafe.Sizeof(int32(0)), pointerSize)

	if got := unsafe.Offsetof(pxField{}.name); got != 0 {
		t.Fatalf("pxField.name offset = %d, want 0", got)
	}
	if got := unsafe.Offsetof(pxField{}.typ); got != pointerSize {
		t.Fatalf("pxField.typ offset = %d, want %d", got, pointerSize)
	}
	if got := unsafe.Offsetof(pxField{}.length); got != fieldLengthOffset {
		t.Fatalf("pxField.length offset = %d, want %d", got, fieldLengthOffset)
	}
	if got := unsafe.Offsetof(pxField{}.decimalCount); got != fieldLengthOffset+4 {
		t.Fatalf("pxField.decimalCount offset = %d, want %d", got, fieldLengthOffset+4)
	}
	if got := unsafe.Sizeof(pxField{}); got != fieldSize {
		t.Fatalf("pxField size = %d, want %d", got, fieldSize)
	}

	if got := unsafe.Offsetof(pxVal{}.typ); got != 4 {
		t.Fatalf("pxVal.typ offset = %d, want 4", got)
	}
	if got := unsafe.Offsetof(pxVal{}.value); got != 8 {
		t.Fatalf("pxVal.value offset = %d, want 8", got)
	}
	if got := unsafe.Sizeof(pxVal{}); got != valueSize {
		t.Fatalf("pxVal size = %d, want %d", got, valueSize)
	}

	if got := unsafe.Offsetof(pxStringValue{}.len); got != pointerSize {
		t.Fatalf("pxStringValue.len offset = %d, want %d", got, pointerSize)
	}
	if got := unsafe.Sizeof(pxStringValue{}); got != stringSize {
		t.Fatalf("pxStringValue size = %d, want %d", got, stringSize)
	}
}

func TestCStringIsBounded(t *testing.T) {
	value := []byte("Code\x00ignored")
	got, err := cString(&value[0])
	if err != nil {
		t.Fatalf("cString(valid) error = %v", err)
	}
	if got != "Code" {
		t.Fatalf("cString(valid) = %q, want Code", got)
	}

	got, err = cString(nil)
	if err != nil || got != "" {
		t.Fatalf("cString(nil) = %q, %v; want empty, nil", got, err)
	}

	unterminated := make([]byte, maxNativeFieldNameBytes)
	for i := range unterminated {
		unterminated[i] = 'x'
	}
	if _, err := cString(&unterminated[0]); err == nil {
		t.Fatal("cString accepted a field name without a bounded terminator")
	}
}

func TestNativeRecordValuesValidatesArray(t *testing.T) {
	first := &pxVal{}
	third := &pxVal{}
	native := []*pxVal{first, nil, third}

	got, err := nativeRecordValues(&native[0], len(native))
	if err != nil {
		t.Fatalf("nativeRecordValues(valid) error = %v", err)
	}
	if len(got) != len(native) || got[0] != first || got[1] != nil || got[2] != third {
		t.Fatalf("nativeRecordValues(valid) = %#v, want %#v", got, native)
	}
	if got, err := nativeRecordValues(nil, 0); err != nil || got != nil {
		t.Fatalf("nativeRecordValues(nil, 0) = %#v, %v; want nil, nil", got, err)
	}
	if _, err := nativeRecordValues(nil, 1); err == nil {
		t.Fatal("nativeRecordValues accepted a null non-empty array")
	}
	if _, err := nativeRecordValues(&native[0], -1); err == nil {
		t.Fatal("nativeRecordValues accepted a negative field count")
	}
	if _, err := nativeRecordValues(&native[0], maxNativeFields+1); err == nil {
		t.Fatal("nativeRecordValues accepted a field count above the Paradox limit")
	}
}

func TestPxStringValidatesDescriptor(t *testing.T) {
	value := &pxVal{}
	setPxStringForTest(value, nil, 3)
	if _, err := pxString(value); err == nil {
		t.Fatal("pxString accepted null data with a positive length")
	}

	setPxStringForTest(value, nil, -1)
	if _, err := pxString(value); err == nil {
		t.Fatal("pxString accepted a negative length")
	}

	data := []byte("Patris")
	setPxStringForTest(value, &data[0], int32(len(data)))
	got, err := pxString(value)
	runtime.KeepAlive(data)
	if err != nil {
		t.Fatalf("pxString(valid) error = %v", err)
	}
	if got != "Patris" {
		t.Fatalf("pxString(valid) = %q, want Patris", got)
	}
}

func TestGetFieldValueUsesAuditedUnionMember(t *testing.T) {
	value := &pxVal{}
	*(*float64)(unsafe.Pointer(&value.value[0])) = 123.5
	got, err := getFieldValue(value, pxfTimestamp)
	if err != nil {
		t.Fatalf("getFieldValue(timestamp) error = %v", err)
	}
	if got != 123.5 {
		t.Fatalf("getFieldValue(timestamp) = %#v, want 123.5", got)
	}

	got, err = getFieldValue(value, 0x7f)
	if err != nil || got != nil {
		t.Fatalf("getFieldValue(unknown) = %#v, %v; want nil, nil", got, err)
	}

	value.isNull = 1
	got, err = getFieldValue(value, pxfAlpha)
	if err != nil || got != nil {
		t.Fatalf("getFieldValue(null) = %#v, %v; want nil, nil", got, err)
	}
}

func TestPxLongSignExtendsNativeNegativeValue(t *testing.T) {
	value := &pxVal{}
	if runtime.GOOS == "windows" || unsafe.Sizeof(uintptr(0)) == 4 {
		*(*int32)(unsafe.Pointer(&value.value[0])) = -1
	} else {
		*(*int64)(unsafe.Pointer(&value.value[0])) = -1
	}

	if got := pxLong(value); got != -1 {
		t.Fatalf("pxLong(native -1) = %d, want -1", got)
	}
}

func TestNativeCountsRejectMalformedValues(t *testing.T) {
	if _, err := nonNegativeNativeCount("record", -1); err == nil {
		t.Fatal("nonNegativeNativeCount accepted a negative record count")
	}
	if _, err := nativeFieldCount(-1); err == nil {
		t.Fatal("nativeFieldCount accepted a negative field count")
	}
}

func TestNativeFieldMetadataRejectsInvalidValues(t *testing.T) {
	if _, err := nativeFieldName(nil); err == nil {
		t.Fatal("nativeFieldName accepted null metadata")
	}

	name := []byte("Code\x00")
	field := &pxField{name: &name[0], length: 4}
	got, err := nativeFieldName(field)
	if err != nil || got != "Code" {
		t.Fatalf("nativeFieldName(valid) = %q, %v; want Code, nil", got, err)
	}

	field.length = -1
	if _, err := nativeFieldName(field); err == nil {
		t.Fatal("nativeFieldName accepted a negative field length")
	}
	field.length = maxNativeFieldBytes + 1
	if _, err := nativeFieldName(field); err == nil {
		t.Fatal("nativeFieldName accepted a field length above TFldInfoRec.fSize")
	}
}

func setPxStringForTest(value *pxVal, ptr *byte, length int32) {
	descriptor := (*pxStringValue)(unsafe.Pointer(&value.value[0]))
	descriptor.ptr = ptr
	descriptor.len = length
}

func TestOpenReportsMissingNativeRuntime(t *testing.T) {
	t.Setenv("PATRIS_EXPORT_PXLIB_LIBRARY", filepath.Join(t.TempDir(), "missing-pxlib-runtime.dll"))
	resetNativeLoaderForTest()
	t.Cleanup(resetNativeLoaderForTest)

	_, err := Open(filepath.Join(t.TempDir(), "missing.db"))
	if err == nil {
		t.Fatal("Open returned nil error for a missing native runtime")
	}

	var depErr *NativeDependencyError
	if !errors.As(err, &depErr) {
		t.Fatalf("Open error type = %T, want NativeDependencyError: %v", err, err)
	}
	if depErr.Library != "pxlib" {
		t.Fatalf("Library = %q, want pxlib", depErr.Library)
	}
	if len(depErr.Attempts) != 1 {
		t.Fatalf("Attempts = %v, want exactly the explicit override", depErr.Attempts)
	}
}

func resetNativeLoaderForTest() {
	loadOnce = sync.Once{}
	loaded = nil
	loadErr = nil
}
