package paradox

import (
	"testing"
)

func TestOpen(t *testing.T) {
	// Test opening a valid database file
	db, err := Open("../../testdata/kala.db")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	if db == nil {
		t.Fatal("Expected non-nil database")
	}
}

func TestGetNumRecords(t *testing.T) {
	db, err := Open("../../testdata/kala.db")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	numRecords := db.GetNumRecords()
	if numRecords == 0 {
		t.Error("Expected non-zero record count")
	}

	t.Logf("Number of records: %d", numRecords)
}

func TestGetNumFields(t *testing.T) {
	db, err := Open("../../testdata/kala.db")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	numFields := db.GetNumFields()
	if numFields == 0 {
		t.Error("Expected non-zero field count")
	}

	t.Logf("Number of fields: %d", numFields)
}

func TestGetFields(t *testing.T) {
	db, err := Open("../../testdata/kala.db")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	fields, err := db.GetFields()
	if err != nil {
		t.Fatalf("Failed to get fields: %v", err)
	}

	if len(fields) == 0 {
		t.Error("Expected non-empty fields list")
	}

	// Check first field
	if len(fields) > 0 {
		if fields[0].Name == "" {
			t.Error("Expected non-empty field name")
		}
		t.Logf("First field: %s (%s)", fields[0].Name, fields[0].Type)
	}
}

func TestGetRecords(t *testing.T) {
	db, err := Open("../../testdata/kala.db")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	records, err := db.GetRecords()
	if err != nil {
		t.Fatalf("Failed to get records: %v", err)
	}

	if len(records) == 0 {
		t.Error("Expected non-empty records list")
	}

	// Check first record has some data
	if len(records) > 0 {
		if len(records[0]) == 0 {
			t.Error("Expected non-empty first record")
		}
		t.Logf("First record has %d fields", len(records[0]))
	}
}

func TestReadCompanyInfo(t *testing.T) {
	info, err := ReadCompanyInfo("../../testdata/company.inf", nil)
	if err != nil {
		t.Fatalf("Failed to read company info: %v", err)
	}

	if info.Name == "" {
		t.Error("Expected non-empty company name")
	}

	if info.StartDate == "" {
		t.Error("Expected non-empty start date")
	}

	if info.EndDate == "" {
		t.Error("Expected non-empty end date")
	}

	t.Logf("Company: %s, Start: %s, End: %s", info.Name, info.StartDate, info.EndDate)
}
