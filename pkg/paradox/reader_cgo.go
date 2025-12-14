//go:build !windows && cgo
// +build !windows,cgo

package paradox

/*
#cgo LDFLAGS: -lpx
#include <stdlib.h>
#include <paradox.h>
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// Database represents a Paradox database file
type Database struct {
	pxdoc  *C.pxdoc_t
	path   string
	pureDB *PureGoDatabase // for compatibility
}

// Open opens a Paradox database file
func Open(path string) (*Database, error) {
	// Initialize pxlib
	C.PX_boot()

	// Create pxdoc structure
	pxdoc := C.PX_new()
	if pxdoc == nil {
		return nil, fmt.Errorf("failed to create pxdoc structure")
	}

	// Open the file
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	if C.PX_open_file(pxdoc, cPath) < 0 {
		C.PX_delete(pxdoc)
		return nil, fmt.Errorf("failed to open Paradox file: %s", path)
	}

	return &Database{
		pxdoc: pxdoc,
		path:  path,
	}, nil
}

// Close closes the database
func (db *Database) Close() error {
	if db.pxdoc != nil {
		C.PX_close(db.pxdoc)
		C.PX_delete(db.pxdoc)
		db.pxdoc = nil
	}
	return nil
}

// GetFields returns the list of fields in the database
func (db *Database) GetFields() ([]Field, error) {
	if db.pxdoc == nil {
		return nil, fmt.Errorf("database is not open")
	}

	numFields := int(C.PX_get_num_fields(db.pxdoc))
	fields := make([]Field, numFields)

	for i := 0; i < numFields; i++ {
		field := C.PX_get_field(db.pxdoc, C.int(i))
		if field == nil {
			continue
		}

		fieldType := ""
		switch field.px_ftype {
		case C.pxfAlpha:
			fieldType = "alpha"
		case C.pxfDate:
			fieldType = "date"
		case C.pxfShort:
			fieldType = "short"
		case C.pxfLong:
			fieldType = "long"
		case C.pxfCurrency:
			fieldType = "currency"
		case C.pxfNumber:
			fieldType = "number"
		case C.pxfLogical:
			fieldType = "logical"
		case C.pxfMemoBLOb:
			fieldType = "memo"
		case C.pxfBLOb:
			fieldType = "blob"
		case C.pxfFmtMemoBLOb:
			fieldType = "fmtmemo"
		case C.pxfOLE:
			fieldType = "ole"
		case C.pxfGraphic:
			fieldType = "graphic"
		case C.pxfTime:
			fieldType = "time"
		case C.pxfTimestamp:
			fieldType = "timestamp"
		case C.pxfAutoInc:
			fieldType = "autoinc"
		case C.pxfBCD:
			fieldType = "bcd"
		case C.pxfBytes:
			fieldType = "bytes"
		default:
			fieldType = "unknown"
		}

		fields[i] = Field{
			Name: C.GoString(field.px_fname),
			Type: fieldType,
			Size: int(field.px_flen),
		}
	}

	return fields, nil
}

// GetRecords returns all records from the database
func (db *Database) GetRecords() ([]Record, error) {
	if db.pxdoc == nil {
		return nil, fmt.Errorf("database is not open")
	}

	numRecords := int(C.PX_get_num_records(db.pxdoc))
	numFields := int(C.PX_get_num_fields(db.pxdoc))

	records := make([]Record, 0, numRecords)

	for i := 0; i < numRecords; i++ {
		pxvals := C.PX_retrieve_record(db.pxdoc, C.int(i))
		if pxvals == nil {
			continue
		}

		record := make(Record)
		
		for j := 0; j < numFields; j++ {
			field := C.PX_get_field(db.pxdoc, C.int(j))
			if field == nil {
				continue
			}

			fieldName := C.GoString(field.px_fname)
			
			// Get the pxval_t pointer for this field
			pxvalPtr := (**C.pxval_t)(unsafe.Pointer(uintptr(unsafe.Pointer(pxvals)) + uintptr(j)*unsafe.Sizeof(*pxvals)))
			pxval := *pxvalPtr
			
			if pxval == nil {
				continue
			}
			
			value := db.getFieldValue(pxval, field.px_ftype)
			
			if value != nil {
				record[fieldName] = value
			}
		}

		records = append(records, record)
	}

	return records, nil
}

// getFieldValue extracts a field value from a pxval_t
func (db *Database) getFieldValue(pxval *C.pxval_t, fieldType C.char) interface{} {
	if pxval.isnull != 0 {
		return nil
	}

	var result interface{}

	// Access union members through unsafe pointer casting
	valuePtr := unsafe.Pointer(&pxval.value)

	switch fieldType {
	case C.pxfAlpha:
		// String field - union contains a struct with char* and int
		type strStruct struct {
			val *C.char
			len C.int
		}
		str := (*strStruct)(valuePtr)
		if str.val != nil {
			result = C.GoString(str.val)
		}

	case C.pxfShort:
		// Short integer - union contains long
		lval := (*C.long)(valuePtr)
		result = int(*lval)

	case C.pxfLong, C.pxfAutoInc:
		// Long integer - union contains long
		lval := (*C.long)(valuePtr)
		result = int(*lval)

	case C.pxfNumber, C.pxfCurrency:
		// Double/Float - union contains double
		dval := (*C.double)(valuePtr)
		result = float64(*dval)

	case C.pxfDate:
		// Date field (stored as long, days since 1/1/0001)
		lval := (*C.long)(valuePtr)
		result = int(*lval)

	case C.pxfLogical:
		// Boolean field - union contains long
		lval := (*C.long)(valuePtr)
		result = *lval != 0

	default:
		// For unsupported types, try to get as string
		type strStruct struct {
			val *C.char
			len C.int
		}
		str := (*strStruct)(valuePtr)
		if str.val != nil {
			result = C.GoString(str.val)
		}
	}

	return result
}

// GetNumRecords returns the number of records in the database
func (db *Database) GetNumRecords() int {
	if db.pxdoc == nil {
		return 0
	}
	return int(C.PX_get_num_records(db.pxdoc))
}

// GetNumFields returns the number of fields in the database
func (db *Database) GetNumFields() int {
	if db.pxdoc == nil {
		return 0
	}
	return int(C.PX_get_num_fields(db.pxdoc))
}

// Shutdown shuts down pxlib
func Shutdown() {
	C.PX_shutdown()
}
