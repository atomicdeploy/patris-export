package paradox

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

// PureGoDatabase represents a Paradox database using pure Go (no CGO)
type PureGoDatabase struct {
	file           *os.File
	header         *paradoxHeader
	fields         []Field
	recordSize     int
	dataBlockStart int64
}

type paradoxHeader struct {
	recordSize      uint16
	headerSize      uint16
	fileType        byte
	maxTableSize    byte
	numRecords      uint32
	nextBlock       uint16
	fileBlocks      uint16
	firstBlock      uint16
	lastBlock       uint16
	unknown1        uint16
	modifiedFlags1  byte
	indexFieldNum   byte
	primaryIndexWS  *uint32
	unknown2        [4]byte
	numFields       uint16
	primaryKeyFields uint16
	encryption1     uint32
	sortOrder       byte
	modifiedFlags2  byte
	unknown3        [2]byte
	changeCount1    byte
	changeCount2    byte
	unknown4        byte
	tableNamePtrPtr *uint32
	fldInfoPtr      *uint32
	writeProtected  byte
	fileVersionID   byte
	maxBlocks       uint16
	unknown5        byte
	auxPasswords    byte
	unknown6        [4]byte
	cryptInfoStartPtr *uint32
	cryptInfoEndPtr   *uint32
	unknown7        byte
	autoInc         uint32
	unknown8        [3]byte
	indexUpdateRequired byte
	unknown9        [5]byte
	refIntegrity    byte
}

// OpenPureGo opens a Paradox database file using pure Go implementation
func OpenPureGo(path string) (*PureGoDatabase, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	db := &PureGoDatabase{
		file: file,
	}

	// Read header
	if err := db.readHeader(); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to read header: %w", err)
	}

	// Read field definitions
	if err := db.readFields(); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to read fields: %w", err)
	}

	return db, nil
}

// readHeader reads the Paradox file header
func (db *PureGoDatabase) readHeader() error {
	header := make([]byte, 0x800) // 2KB header
	if _, err := db.file.Read(header); err != nil {
		return err
	}

	db.header = &paradoxHeader{}
	db.header.recordSize = binary.LittleEndian.Uint16(header[0:2])
	db.header.headerSize = binary.LittleEndian.Uint16(header[2:4])
	db.header.fileType = header[4]
	db.header.maxTableSize = header[5]
	db.header.numRecords = binary.LittleEndian.Uint32(header[6:10])
	db.header.nextBlock = binary.LittleEndian.Uint16(header[10:12])
	db.header.fileBlocks = binary.LittleEndian.Uint16(header[12:14])
	db.header.firstBlock = binary.LittleEndian.Uint16(header[14:16])
	db.header.lastBlock = binary.LittleEndian.Uint16(header[16:18])
	db.header.numFields = binary.LittleEndian.Uint16(header[0x21:0x23])

	db.recordSize = int(db.header.recordSize)
	db.dataBlockStart = int64(db.header.headerSize) * 1024

	return nil
}

// readFields reads field definitions from the header
func (db *PureGoDatabase) readFields() error {
	header := make([]byte, 0x800)
	db.file.Seek(0, io.SeekStart)
	db.file.Read(header)

	// Field types start at offset 0x78
	fieldTypeOffset := 0x78
	// Field names start at offset 0x220 (approximately)
	fieldNameOffset := 0x220

	db.fields = make([]Field, db.header.numFields)

	// Read field names
	namePos := fieldNameOffset
	for i := 0; i < int(db.header.numFields); i++ {
		// Read null-terminated field name
		nameEnd := namePos
		for header[nameEnd] != 0 {
			nameEnd++
		}
		db.fields[i].Name = string(header[namePos:nameEnd])
		namePos = nameEnd + 1
	}

	// Read field types and sizes
	for i := 0; i < int(db.header.numFields); i++ {
		fieldType := header[fieldTypeOffset+i]
		
		var typeStr string
		var size int
		
		switch fieldType {
		case 0x01: // Alpha (string)
			typeStr = "alpha"
			// Size is stored elsewhere, we'll calculate from record structure
		case 0x03: // Short
			typeStr = "short"
			size = 2
		case 0x04: // Long/AutoInc
			typeStr = "long"
			size = 4
		case 0x06: // Number/Currency
			typeStr = "number"
			size = 8
		case 0x09: // Logical
			typeStr = "logical"
			size = 1
		default:
			typeStr = fmt.Sprintf("unknown(%d)", fieldType)
		}
		
		db.fields[i].Type = typeStr
		db.fields[i].Size = size
	}

	// Calculate alpha field sizes from record size
	totalSize := 0
	for i := range db.fields {
		if db.fields[i].Type != "alpha" {
			totalSize += db.fields[i].Size
		}
	}
	
	// Distribute remaining space among alpha fields
	alphaCount := 0
	for i := range db.fields {
		if db.fields[i].Type == "alpha" {
			alphaCount++
		}
	}
	
	if alphaCount > 0 {
		remainingSize := db.recordSize - totalSize
		// This is a simple estimation; real field sizes are in another part of header
		avgAlphaSize := remainingSize / alphaCount
		for i := range db.fields {
			if db.fields[i].Type == "alpha" {
				db.fields[i].Size = avgAlphaSize
			}
		}
	}

	return nil
}

// GetFields returns field definitions
func (db *PureGoDatabase) GetFields() ([]Field, error) {
	return db.fields, nil
}

// GetNumRecords returns the number of records
func (db *PureGoDatabase) GetNumRecords() int {
	return int(db.header.numRecords)
}

// GetNumFields returns the number of fields
func (db *PureGoDatabase) GetNumFields() int {
	return int(db.header.numFields)
}

// GetRecords reads all records from the database
func (db *PureGoDatabase) GetRecords() ([]Record, error) {
	records := make([]Record, 0, db.header.numRecords)

	// Seek to data blocks
	db.file.Seek(db.dataBlockStart, io.SeekStart)

	for i := 0; i < int(db.header.numRecords); i++ {
		record, err := db.readRecord()
		if err != nil {
			return nil, fmt.Errorf("failed to read record %d: %w", i, err)
		}
		if record != nil {
			records = append(records, record)
		}
	}

	return records, nil
}

// readRecord reads a single record
func (db *PureGoDatabase) readRecord() (Record, error) {
	data := make([]byte, db.recordSize)
	n, err := db.file.Read(data)
	if err == io.EOF {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if n != db.recordSize {
		return nil, fmt.Errorf("incomplete record read")
	}

	record := make(Record)
	offset := 0

	for _, field := range db.fields {
		if offset+field.Size > len(data) {
			break
		}

		fieldData := data[offset : offset+field.Size]
		
		var value interface{}
		switch field.Type {
		case "alpha":
			// String field - find null terminator
			endPos := 0
			for endPos < len(fieldData) && fieldData[endPos] != 0 {
				endPos++
			}
			value = string(fieldData[:endPos])

		case "short":
			if len(fieldData) >= 2 {
				value = int(int16(binary.LittleEndian.Uint16(fieldData)))
			}

		case "long":
			if len(fieldData) >= 4 {
				value = int(int32(binary.LittleEndian.Uint32(fieldData)))
			}

		case "number":
			if len(fieldData) >= 8 {
				bits := binary.LittleEndian.Uint64(fieldData)
				value = math.Float64frombits(bits)
			}

		case "logical":
			if len(fieldData) >= 1 {
				value = fieldData[0] != 0
			}
		}

		if value != nil {
			record[field.Name] = value
		}

		offset += field.Size
	}

	return record, nil
}

// Close closes the database file
func (db *PureGoDatabase) Close() error {
	if db.file != nil {
		return db.file.Close()
	}
	return nil
}
