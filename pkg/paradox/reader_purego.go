//go:build !cgo
// +build !cgo

package paradox

// Open opens a Paradox database file (pure Go version for non-CGO builds)
func Open(path string) (*Database, error) {
	pureDB, err := OpenPureGo(path)
	if err != nil {
		return nil, err
	}

	// Wrap it in the standard Database interface
	return &Database{
		pureDB: pureDB,
		path:   path,
	}, nil
}

// Database represents a Paradox database file (wrapper)
type Database struct {
	pureDB *PureGoDatabase
	path   string
}

// Close closes the database
func (db *Database) Close() error {
	if db.pureDB != nil {
		return db.pureDB.Close()
	}
	return nil
}

// GetFields returns the list of fields in the database
func (db *Database) GetFields() ([]Field, error) {
	return db.pureDB.GetFields()
}

// GetRecords returns all records from the database
func (db *Database) GetRecords() ([]Record, error) {
	return db.pureDB.GetRecords()
}

// GetNumRecords returns the number of records in the database
func (db *Database) GetNumRecords() int {
	return db.pureDB.GetNumRecords()
}

// GetNumFields returns the number of fields in the database
func (db *Database) GetNumFields() int {
	return db.pureDB.GetNumFields()
}

// Shutdown shuts down (no-op for pure Go version)
func Shutdown() {
	// No-op for pure Go version
}
