package server

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/datasource"
)

// TestRealDatabaseChanges tests change detection using actual Paradox database files.
// These files (kala-before.db and kala-after.db) contain real inventory data from a 
// production database. The test validates that the change detection logic properly
// identifies added, modified, and deleted records, and tracks field-level changes.
func TestRealDatabaseChanges(t *testing.T) {
	beforeDB := filepath.Join("..", "..", "testdata", "kala-before.db")
	afterDB := filepath.Join("..", "..", "testdata", "kala-after.db")
	
	// Check test files exist
	if _, err := os.Stat(beforeDB); os.IsNotExist(err) {
		t.Skip("Test database files not found, skipping real database test")
	}
	
	// Read records from both databases
	beforeRecords, err := readDatabaseToMap(beforeDB)
	if err != nil {
		t.Fatalf("Failed to read before database: %v", err)
	}
	
	afterRecords, err := readDatabaseToMap(afterDB)
	if err != nil {
		t.Fatalf("Failed to read after database: %v", err)
	}
	
	t.Logf("Loaded %d records from before DB and %d records from after DB", 
		len(beforeRecords), len(afterRecords))
	
	// Compute changes
	added, modified, deleted := compareRecords(beforeRecords, afterRecords)
	
	t.Logf("Total changes: %d added, %d modified, %d deleted",
		len(added), len(modified), len(deleted))
	
	// Log all changes for debugging
	if len(added) > 0 {
		t.Logf("Added records: %v", added)
	}
	
	if len(deleted) > 0 {
		t.Logf("Deleted records: %v", deleted)
	}
	
	// Verify modified records and their field-level changes
	if len(modified) > 0 {
		t.Logf("\nDetailed field-level changes:")
		for code, changes := range modified {
			t.Logf("  Modified record: Code=%v", code)
			t.Logf("    Changed %d field(s): %v", len(changes), getChangedFieldNames(changes))
			for field, change := range changes {
				t.Logf("      %s: %v → %v", field, change.OldValue, change.NewValue)
			}
		}
	}
	
	// Basic sanity checks - the database files should have the same number of records
	// (we're testing modifications, not additions/deletions)
	if len(beforeRecords) != len(afterRecords) {
		t.Logf("Warning: Before DB has %d records, After DB has %d records", 
			len(beforeRecords), len(afterRecords))
	}
	
	// Validate the expected change: Code 116005, FOROSH: 8888 → 9999
	if len(added) != 0 {
		t.Errorf("Expected 0 added records, got %d", len(added))
	}
	if len(deleted) != 0 {
		t.Errorf("Expected 0 deleted records, got %d", len(deleted))
	}
	if len(modified) != 1 {
		t.Errorf("Expected exactly 1 modified record, got %d", len(modified))
		return
	}
	
	// Check the specific modified record
	expectedCode := "116005"
	changes, ok := modified[expectedCode]
	if !ok {
		t.Errorf("Expected modified record with Code=%s, but it wasn't found", expectedCode)
		t.Logf("Modified records: %v", getModifiedCodes(modified))
		return
	}
	
	// Verify the FOROSH field changed
	if len(changes) != 1 {
		t.Errorf("Expected exactly 1 changed field in record %s, got %d", expectedCode, len(changes))
	}
	
	foreshChange, ok := changes["FOROSH"]
	if !ok {
		t.Errorf("Expected field 'FOROSH' to be changed in record %s", expectedCode)
		t.Logf("Changed fields: %v", getChangedFieldNames(changes))
		return
	}
	
	// Verify the specific values (allow both int and float64 since JSON uses float64 for numbers)
	oldVal := fmt.Sprintf("%v", foreshChange.OldValue)
	newVal := fmt.Sprintf("%v", foreshChange.NewValue)
	
	if oldVal != "8888" {
		t.Errorf("Expected old FOROSH value '8888', got '%s'", oldVal)
	}
	if newVal != "9999" {
		t.Errorf("Expected new FOROSH value '9999', got '%s'", newVal)
	}
	
	t.Logf("\n✅ Real database change detection test passed")
	t.Logf("   Successfully detected expected change: Code=%s, FOROSH: %s → %s", 
		expectedCode, oldVal, newVal)
	t.Logf("   This validates that field-level change tracking works with production data")
}

// getModifiedCodes returns a list of codes from the modified map
func getModifiedCodes(modified map[string]map[string]FieldChange) []string {
	codes := make([]string, 0, len(modified))
	for code := range modified {
		codes = append(codes, code)
	}
	return codes
}

// copyFile copies a file from src to dst
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	
	srcFile, err := os.Open(src)
	if err != nil {
		t.Fatalf("Failed to open source file: %v", err)
	}
	defer srcFile.Close()
	
	dstFile, err := os.Create(dst)
	if err != nil {
		t.Fatalf("Failed to create destination file: %v", err)
	}
	defer dstFile.Close()
	
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		t.Fatalf("Failed to copy file: %v", err)
	}
}

// FieldChange represents a change to a specific field
type FieldChange struct {
	OldValue interface{}
	NewValue interface{}
}

// readDatabaseToMap reads a Paradox database and returns records as a map with Code as keys
func readDatabaseToMap(dbPath string) (map[string]map[string]interface{}, error) {
	// Create a data source from the database (use nil for default Patris81 mapping)
	ds, err := datasource.NewDataSource(dbPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create data source: %w", err)
	}
	defer ds.Close()
	
	// Get records (already transformed by datasource)
	records, err := ds.GetRecords()
	if err != nil {
		return nil, fmt.Errorf("failed to get records: %w", err)
	}
	
	// Convert array of records to map keyed by Code
	result := make(map[string]map[string]interface{})
	for _, record := range records {
		if code, ok := record["Code"]; ok {
			codeStr := fmt.Sprintf("%v", code)
			result[codeStr] = record
		}
	}
	
	return result, nil
}

// compareRecords compares two sets of records and returns the differences
func compareRecords(oldRecords, newRecords map[string]map[string]interface{}) (
	added []string,
	modified map[string]map[string]FieldChange,
	deleted []string,
) {
	added = []string{}
	deleted = []string{}
	modified = make(map[string]map[string]FieldChange)
	
	// Find added records
	for code := range newRecords {
		if _, exists := oldRecords[code]; !exists {
			added = append(added, code)
		}
	}
	
	// Find deleted records
	for code := range oldRecords {
		if _, exists := newRecords[code]; !exists {
			deleted = append(deleted, code)
		}
	}
	
	// Find modified records
	for code, newRecord := range newRecords {
		if oldRecord, exists := oldRecords[code]; exists {
			changes := make(map[string]FieldChange)
			
			// Compare each field
			for key, newVal := range newRecord {
				if key == "Code" {
					continue // Skip the key field
				}
				oldVal := oldRecord[key]
				
				// Serialize values to JSON for comparison
				oldJSON := fmt.Sprintf("%v", oldVal)
				newJSON := fmt.Sprintf("%v", newVal)
				
				if oldJSON != newJSON {
					changes[key] = FieldChange{
						OldValue: oldVal,
						NewValue: newVal,
					}
				}
			}
			
			// Check for fields that existed in old but not in new
			for key, oldVal := range oldRecord {
				if key == "Code" {
					continue
				}
				if _, exists := newRecord[key]; !exists {
					changes[key] = FieldChange{
						OldValue: oldVal,
						NewValue: nil,
					}
				}
			}
			
			if len(changes) > 0 {
				modified[code] = changes
			}
		}
	}
	
	return added, modified, deleted
}

// getChangedFieldNames returns a list of field names that changed
func getChangedFieldNames(changes map[string]FieldChange) []string {
	names := make([]string, 0, len(changes))
	for name := range changes {
		names = append(names, name)
	}
	return names
}
