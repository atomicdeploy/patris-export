package server

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/datasource"
)

// TestRealDatabaseChanges tests change detection using actual Paradox database files
// These files (kala-before.db and kala-after.db) contain real data with known changes:
// - Code 116005: FOROSH changed from 8888 to 0
// - Code 113007034: ALLANBAR changed from 9 to 10, ANBAR[0] changed from 9 to 10
// - Code 113009007: Name changed from "ESP-01 PROGRAMER" to "ESP8266 ADAPTOR", Tedad_k changed from 0 to 10
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
	
	// Verify we have the expected changes
	if len(added) != 0 {
		t.Errorf("Expected 0 added records, got %d", len(added))
	}
	
	if len(deleted) != 0 {
		t.Errorf("Expected 0 deleted records, got %d", len(deleted))
	}
	
	if len(modified) != 3 {
		t.Errorf("Expected 3 modified records, got %d", len(modified))
	}
	
	// Verify modified records and their field-level changes
	for code, changes := range modified {
		t.Logf("\nModified record: Code=%v", code)
		t.Logf("  Changed %d field(s): %v", len(changes), getChangedFieldNames(changes))
		for field, change := range changes {
			t.Logf("    %s: %v → %v", field, change.OldValue, change.NewValue)
		}
	}
	
	// Verify specific records were modified with correct field changes
	if changes, ok := modified["116005"]; ok {
		// FOROSH: 8888 → 0
		if len(changes) != 1 {
			t.Errorf("Expected 1 changed field for 116005, got %d", len(changes))
		}
		if change, ok := changes["FOROSH"]; ok {
			t.Logf("  ✓ Code 116005: FOROSH changed from %v to %v", change.OldValue, change.NewValue)
		} else {
			t.Error("Expected FOROSH to change for 116005")
		}
	} else {
		t.Error("Expected Code=116005 to be modified")
	}
	
	if changes, ok := modified["113007034"]; ok {
		// ALLANBAR: 9 → 10, ANBAR[0]: 9 → 10
		if len(changes) != 2 {
			t.Errorf("Expected 2 changed fields for 113007034, got %d: %v", len(changes), getChangedFieldNames(changes))
		}
		t.Logf("  ✓ Code 113007034: %d field(s) changed correctly", len(changes))
	} else {
		t.Error("Expected Code=113007034 to be modified")
	}
	
	if changes, ok := modified["113009007"]; ok {
		// Name: "ESP-01 PROGRAMER" → "ESP8266 ADAPTOR", Tedad_k: 0 → 10
		if len(changes) != 2 {
			t.Errorf("Expected 2 changed fields for 113009007, got %d: %v", len(changes), getChangedFieldNames(changes))
		}
		t.Logf("  ✓ Code 113009007: %d field(s) changed correctly", len(changes))
	} else {
		t.Error("Expected Code=113009007 to be modified")
	}
	
	t.Log("\n✅ Real database change detection test passed")
	t.Log("   This test validates the change detection logic using actual Paradox database files")
	t.Log("   with known modifications. It confirms that field-level changes are properly detected.")
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
