package paradox

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unsafe"
)

const (
	pxfAlpha       = 0x01
	pxfDate        = 0x02
	pxfShort       = 0x03
	pxfLong        = 0x04
	pxfCurrency    = 0x05
	pxfNumber      = 0x06
	pxfLogical     = 0x09
	pxfMemoBLOb    = 0x0C
	pxfBLOb        = 0x0D
	pxfFmtMemoBLOb = 0x0E
	pxfOLE         = 0x0F
	pxfGraphic     = 0x10
	pxfTime        = 0x14
	pxfTimestamp   = 0x15
	pxfAutoInc     = 0x16
	pxfBCD         = 0x17
	pxfBytes       = 0x18
)

var (
	loadOnce sync.Once
	loaded   *pxlib
	loadErr  error
)

// NativeDependencyError reports a missing or unusable native Paradox reader
// runtime. It is returned at database-open time instead of letting the OS loader
// abort the process before the application can show a useful message.
type NativeDependencyError struct {
	Library  string
	Attempts []string
	Err      error
}

func (e *NativeDependencyError) Error() string {
	if e == nil {
		return ""
	}
	name := e.Library
	if name == "" {
		name = "pxlib"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "native %s runtime is not available", name)
	if e.Err != nil {
		fmt.Fprintf(&b, ": %v", e.Err)
	}
	if len(e.Attempts) > 0 {
		fmt.Fprintf(&b, " (checked: %s)", strings.Join(e.Attempts, ", "))
	}
	return b.String()
}

func (e *NativeDependencyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func IsNativeDependencyError(err error) bool {
	var depErr *NativeDependencyError
	return errors.As(err, &depErr)
}

type pxlib struct {
	handle uintptr

	pxBoot           func()
	pxShutdown       func()
	pxNew            func() unsafe.Pointer
	pxOpenFile       func(unsafe.Pointer, string) int32
	pxClose          func(unsafe.Pointer)
	pxDelete         func(unsafe.Pointer)
	pxGetNumFields   func(unsafe.Pointer) int32
	pxGetNumRecords  func(unsafe.Pointer) int32
	pxGetField       func(unsafe.Pointer, int32) *pxField
	pxRetrieveRecord func(unsafe.Pointer, int32) **pxVal
}

// pxField mirrors pxlib's pxfield_t. The explicit padding keeps px_flen at
// offset 8 on 32-bit targets and offset 12 on 64-bit targets, matching the C
// layouts audited for ILP32, LP64, and LLP64. Runtime support remains limited
// to the platform/backend combinations documented in docs/PXLIB-FFI.md.
type pxField struct {
	name         *byte
	typ          int8
	_            [3]byte
	length       int32
	decimalCount int32
}

// pxVal mirrors pxlib's pxval_t. The union is two pointer-width words: 8 bytes
// on 32-bit targets and 16 bytes on 64-bit targets. That is the size of the
// largest C union member (the pointer/length string descriptor) on both ABIs.
type pxVal struct {
	isNull int8
	_      [3]byte
	typ    int32
	value  [2]uintptr
}

type pxStringValue struct {
	ptr *byte
	len int32
}

// Database represents a Paradox database file.
type Database struct {
	mu    sync.RWMutex
	lib   *pxlib
	pxdoc unsafe.Pointer
	path  string
}

// Open opens a Paradox database file.
func Open(path string) (*Database, error) {
	lib, err := loadPxlib()
	if err != nil {
		return nil, err
	}

	lib.pxBoot()

	pxdoc := lib.pxNew()
	if pxdoc == nil {
		return nil, fmt.Errorf("failed to create pxdoc structure")
	}

	if lib.pxOpenFile(pxdoc, path) < 0 {
		lib.pxDelete(pxdoc)
		return nil, fmt.Errorf("failed to open Paradox file: %s", path)
	}

	return &Database{
		lib:   lib,
		pxdoc: pxdoc,
		path:  path,
	}, nil
}

// Close closes the database.
func (db *Database) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.pxdoc != nil {
		db.lib.pxClose(db.pxdoc)
		db.lib.pxDelete(db.pxdoc)
		db.pxdoc = nil
	}
	return nil
}

// GetFields returns the list of fields in the database.
func (db *Database) GetFields() ([]Field, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	defer runtime.KeepAlive(db)

	if db.pxdoc == nil {
		return nil, fmt.Errorf("database is not open")
	}

	numFields, err := nativeFieldCount(db.lib.pxGetNumFields(db.pxdoc))
	if err != nil {
		return nil, err
	}
	fields := make([]Field, numFields)

	for i := 0; i < numFields; i++ {
		field := db.lib.pxGetField(db.pxdoc, int32(i))
		name, err := nativeFieldName(field)
		if err != nil {
			return nil, fmt.Errorf("decode field %d metadata: %w", i, err)
		}
		fields[i] = Field{
			Name: name,
			Type: fieldTypeName(field.typ),
			Size: int(field.length),
		}
	}

	return fields, nil
}

// GetRecords returns all records from the database.
func (db *Database) GetRecords() ([]Record, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	defer runtime.KeepAlive(db)

	if db.pxdoc == nil {
		return nil, fmt.Errorf("database is not open")
	}

	numRecords, err := nonNegativeNativeCount("record", db.lib.pxGetNumRecords(db.pxdoc))
	if err != nil {
		return nil, err
	}
	numFields, err := nativeFieldCount(db.lib.pxGetNumFields(db.pxdoc))
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, numRecords)

	for i := 0; i < numRecords; i++ {
		valuesPtr := db.lib.pxRetrieveRecord(db.pxdoc, int32(i))
		if valuesPtr == nil {
			continue
		}

		values, err := nativeRecordValues(valuesPtr, numFields)
		if err != nil {
			return nil, fmt.Errorf("decode record %d values: %w", i, err)
		}
		record := make(Record)

		for j := 0; j < numFields; j++ {
			fieldMeta := db.lib.pxGetField(db.pxdoc, int32(j))
			if values[j] == nil {
				continue
			}

			field, err := nativeFieldName(fieldMeta)
			if err != nil {
				return nil, fmt.Errorf("decode record %d field %d metadata: %w", i, j, err)
			}
			nativeValue := values[j]
			if nativeValue.isNull != 0 {
				record[field] = nil
				continue
			}
			value, err := getFieldValue(nativeValue, fieldMeta.typ)
			if err != nil {
				return nil, fmt.Errorf("decode record %d field %q: %w", i, field, err)
			}
			if value != nil {
				record[field] = value
			}
		}

		records = append(records, record)
	}

	return records, nil
}

// GetNumRecords returns the number of records in the database.
func (db *Database) GetNumRecords() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	defer runtime.KeepAlive(db)

	if db.pxdoc == nil {
		return 0
	}
	count, err := nonNegativeNativeCount("record", db.lib.pxGetNumRecords(db.pxdoc))
	if err != nil {
		return 0
	}
	return count
}

// GetNumFields returns the number of fields in the database.
func (db *Database) GetNumFields() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	defer runtime.KeepAlive(db)

	if db.pxdoc == nil {
		return 0
	}
	count, err := nativeFieldCount(db.lib.pxGetNumFields(db.pxdoc))
	if err != nil {
		return 0
	}
	return count
}

// Shutdown shuts down pxlib.
func Shutdown() {
	if lib, err := loadPxlib(); err == nil {
		lib.pxShutdown()
	}
}

func loadPxlib() (*pxlib, error) {
	loadOnce.Do(func() {
		loaded, loadErr = openPxlib()
	})
	return loaded, loadErr
}

func fieldTypeName(fieldType int8) string {
	switch fieldType {
	case pxfAlpha:
		return "alpha"
	case pxfDate:
		return "date"
	case pxfShort:
		return "short"
	case pxfLong:
		return "long"
	case pxfCurrency:
		return "currency"
	case pxfNumber:
		return "number"
	case pxfLogical:
		return "logical"
	case pxfMemoBLOb:
		return "memo"
	case pxfBLOb:
		return "blob"
	case pxfFmtMemoBLOb:
		return "fmtmemo"
	case pxfOLE:
		return "ole"
	case pxfGraphic:
		return "graphic"
	case pxfTime:
		return "time"
	case pxfTimestamp:
		return "timestamp"
	case pxfAutoInc:
		return "autoinc"
	case pxfBCD:
		return "bcd"
	case pxfBytes:
		return "bytes"
	default:
		return "unknown"
	}
}

func alignUp(value, alignment uintptr) uintptr {
	return (value + alignment - 1) &^ (alignment - 1)
}

const (
	// pxlib's pinned parser reads field names into TMPBUFFSIZE and always adds
	// a terminator. Scanning beyond this contract would turn malformed native
	// output into an unbounded read.
	maxNativeFieldNameBytes = 300
	// Paradox stores the field count in an unsigned 16-bit header value. This
	// cap also bounds the pxval_t** slice returned by PX_retrieve_record.
	maxNativeFields = 1<<16 - 1
	// Field length is stored in the file's one-byte TFldInfoRec.fSize member.
	maxNativeFieldBytes = 1<<8 - 1
)

func nonNegativeNativeCount(kind string, count int32) (int, error) {
	if count < 0 {
		return 0, fmt.Errorf("pxlib returned negative %s count %d", kind, count)
	}
	return int(count), nil
}

func nativeFieldCount(count int32) (int, error) {
	value, err := nonNegativeNativeCount("field", count)
	if err != nil {
		return 0, err
	}
	if value > maxNativeFields {
		return 0, fmt.Errorf("pxlib returned field count %d above Paradox limit %d", value, maxNativeFields)
	}
	return value, nil
}

func nativeRecordValues(values **pxVal, count int) ([]*pxVal, error) {
	if count < 0 || count > maxNativeFields {
		return nil, fmt.Errorf("invalid record field count %d", count)
	}
	if count == 0 {
		return nil, nil
	}
	if values == nil {
		return nil, fmt.Errorf("pxlib returned a null record array for %d fields", count)
	}
	return unsafe.Slice(values, count), nil
}

func nativeFieldName(field *pxField) (string, error) {
	if field == nil {
		return "", fmt.Errorf("pxlib returned null field metadata")
	}
	if field.length < 0 || field.length > maxNativeFieldBytes {
		return "", fmt.Errorf("pxlib returned invalid field length %d", field.length)
	}
	return cString(field.name)
}

func getFieldValue(value *pxVal, fieldType int8) (interface{}, error) {
	if value == nil || value.isNull != 0 {
		return nil, nil
	}

	switch fieldType {
	case pxfAlpha, pxfMemoBLOb, pxfBLOb, pxfFmtMemoBLOb, pxfOLE, pxfGraphic, pxfBCD, pxfBytes:
		return pxString(value)
	case pxfShort, pxfLong, pxfAutoInc, pxfDate, pxfTime:
		return int(pxLong(value)), nil
	case pxfNumber, pxfCurrency, pxfTimestamp:
		return *(*float64)(unsafe.Pointer(&value.value[0])), nil
	case pxfLogical:
		return pxLong(value) != 0, nil
	}
	return nil, nil
}

func pxString(value *pxVal) (string, error) {
	if value == nil {
		return "", nil
	}
	str := (*pxStringValue)(unsafe.Pointer(&value.value[0]))
	if str.len < 0 {
		return "", fmt.Errorf("pxlib returned negative string length %d", str.len)
	}
	if str.ptr == nil {
		if str.len != 0 {
			return "", fmt.Errorf("pxlib returned null string data with length %d", str.len)
		}
		return "", nil
	}
	if str.len == 0 {
		return "", nil
	}
	return string(unsafe.Slice(str.ptr, int(str.len))), nil
}

func cString(ptr *byte) (string, error) {
	if ptr == nil {
		return "", nil
	}
	for n := 0; n < maxNativeFieldNameBytes; n++ {
		if *(*byte)(unsafe.Add(unsafe.Pointer(ptr), n)) == 0 {
			return string(unsafe.Slice(ptr, n)), nil
		}
	}
	return "", fmt.Errorf("pxlib field name is not NUL-terminated within %d bytes", maxNativeFieldNameBytes)
}
