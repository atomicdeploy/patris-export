package paradox

import (
	"errors"
	"fmt"
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
	pxNew            func() uintptr
	pxOpenFile       func(uintptr, string) int32
	pxClose          func(uintptr)
	pxDelete         func(uintptr)
	pxGetNumFields   func(uintptr) int32
	pxGetNumRecords  func(uintptr) int32
	pxGetField       func(uintptr, int32) uintptr
	pxRetrieveRecord func(uintptr, int32) uintptr
}

type pxVal struct {
	isNull int8
	_      [3]byte
	typ    int32
	value  [16]byte
}

type pxStringValue struct {
	ptr *byte
	len int32
}

// Database represents a Paradox database file.
type Database struct {
	lib   *pxlib
	pxdoc uintptr
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
	if pxdoc == 0 {
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
	if db.pxdoc != 0 {
		db.lib.pxClose(db.pxdoc)
		db.lib.pxDelete(db.pxdoc)
		db.pxdoc = 0
	}
	return nil
}

// GetFields returns the list of fields in the database.
func (db *Database) GetFields() ([]Field, error) {
	if db.pxdoc == 0 {
		return nil, fmt.Errorf("database is not open")
	}

	numFields := int(db.lib.pxGetNumFields(db.pxdoc))
	fields := make([]Field, numFields)

	for i := 0; i < numFields; i++ {
		fieldPtr := db.lib.pxGetField(db.pxdoc, int32(i))
		if fieldPtr == 0 {
			continue
		}
		fields[i] = Field{
			Name: cString(fieldName(fieldPtr)),
			Type: fieldTypeName(fieldType(fieldPtr)),
			Size: int(fieldLen(fieldPtr)),
		}
	}

	return fields, nil
}

// GetRecords returns all records from the database.
func (db *Database) GetRecords() ([]Record, error) {
	if db.pxdoc == 0 {
		return nil, fmt.Errorf("database is not open")
	}

	numRecords := int(db.lib.pxGetNumRecords(db.pxdoc))
	numFields := int(db.lib.pxGetNumFields(db.pxdoc))
	records := make([]Record, 0, numRecords)

	for i := 0; i < numRecords; i++ {
		valuesPtr := db.lib.pxRetrieveRecord(db.pxdoc, int32(i))
		if valuesPtr == 0 {
			continue
		}

		values := unsafe.Slice((*uintptr)(unsafe.Pointer(valuesPtr)), numFields)
		record := make(Record)

		for j := 0; j < numFields; j++ {
			fieldPtr := db.lib.pxGetField(db.pxdoc, int32(j))
			if fieldPtr == 0 || values[j] == 0 {
				continue
			}

			field := cString(fieldName(fieldPtr))
			nativeValue := (*pxVal)(unsafe.Pointer(values[j]))
			if nativeValue.isNull != 0 {
				record[field] = nil
				continue
			}
			value := getFieldValue(nativeValue, fieldType(fieldPtr))
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
	if db.pxdoc == 0 {
		return 0
	}
	return int(db.lib.pxGetNumRecords(db.pxdoc))
}

// GetNumFields returns the number of fields in the database.
func (db *Database) GetNumFields() int {
	if db.pxdoc == 0 {
		return 0
	}
	return int(db.lib.pxGetNumFields(db.pxdoc))
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

func fieldName(fieldPtr uintptr) *byte {
	return *(**byte)(unsafe.Pointer(fieldPtr))
}

func fieldType(fieldPtr uintptr) int8 {
	return *(*int8)(unsafe.Pointer(fieldPtr + unsafe.Sizeof(uintptr(0))))
}

func fieldLen(fieldPtr uintptr) int32 {
	return *(*int32)(unsafe.Pointer(fieldPtr + alignUp(unsafe.Sizeof(uintptr(0))+1, 4)))
}

func alignUp(value, alignment uintptr) uintptr {
	return (value + alignment - 1) &^ (alignment - 1)
}

func getFieldValue(value *pxVal, fieldType int8) interface{} {
	if value == nil || value.isNull != 0 {
		return nil
	}

	switch fieldType {
	case pxfAlpha:
		return pxString(value)
	case pxfShort, pxfLong, pxfAutoInc, pxfDate:
		return int(pxLong(value))
	case pxfNumber, pxfCurrency:
		return *(*float64)(unsafe.Pointer(&value.value[0]))
	case pxfLogical:
		return pxLong(value) != 0
	default:
		if str := pxString(value); str != "" {
			return str
		}
	}
	return nil
}

func pxString(value *pxVal) string {
	str := (*pxStringValue)(unsafe.Pointer(&value.value[0]))
	if str.ptr == nil || str.len <= 0 {
		return ""
	}
	return string(unsafe.Slice(str.ptr, int(str.len)))
}

func cString(ptr *byte) string {
	if ptr == nil {
		return ""
	}
	var n int
	for p := uintptr(unsafe.Pointer(ptr)); ; p++ {
		if *(*byte)(unsafe.Pointer(p)) == 0 {
			break
		}
		n++
	}
	return string(unsafe.Slice(ptr, n))
}
