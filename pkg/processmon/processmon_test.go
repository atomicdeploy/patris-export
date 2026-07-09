package processmon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindProcessByName(t *testing.T) {
	// Try to find the current test process (go test)
	// This should always find at least one process
	processes, err := FindProcessByName("go")
	if err != nil {
		t.Fatalf("FindProcessByName failed: %v", err)
	}

	// We should find at least one process (the current test process)
	if len(processes) == 0 {
		t.Logf("Warning: No 'go' processes found, might be expected in some environments")
	}

	// Verify that processes have basic information
	for _, p := range processes {
		if p.PID == 0 {
			t.Errorf("Process has invalid PID: 0")
		}
		if p.Name == "" {
			t.Errorf("Process has empty name")
		}
		t.Logf("Found process: PID=%d, Name=%s, Exe=%s", p.PID, p.Name, p.Exe)
	}
}

func TestFindProcessByName_NotFound(t *testing.T) {
	// Try to find a process that doesn't exist
	processes, err := FindProcessByName("nonexistent_process_xyz123")
	if err != nil {
		t.Fatalf("FindProcessByName failed: %v", err)
	}

	// Should return empty list, not error
	if len(processes) != 0 {
		t.Errorf("Expected 0 processes, got %d", len(processes))
	}
}

func TestFindProcessesWithFile(t *testing.T) {
	// Create a temporary file
	tmpFile, err := os.CreateTemp("", "processmon_test_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write some data to keep the file open
	if _, err := tmpFile.WriteString("test data"); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}

	// Find processes with this file open
	// This test process should have the file open
	info, err := FindProcessesWithFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("FindProcessesWithFile failed: %v", err)
	}

	if info.FilePath == "" {
		t.Errorf("FileAccessInfo has empty FilePath")
	}

	// The test process should have this file open
	// Note: On some systems, we might not have permission to see our own file handles
	t.Logf("Found %d processes with file %s open", len(info.Processes), tmpFile.Name())
	for _, p := range info.Processes {
		t.Logf("Process: PID=%d, Name=%s", p.PID, p.Name)
	}

	// Close the file
	tmpFile.Close()
}

func TestFindProcessesWithFile_NotExists(t *testing.T) {
	// Try to find processes with a non-existent file
	_, err := FindProcessesWithFile("/nonexistent/path/to/file.txt")
	if err == nil {
		t.Errorf("Expected error for non-existent file, got nil")
	}
}

func TestIsFileInUse(t *testing.T) {
	// Create a temporary file
	tmpFile, err := os.CreateTemp("", "processmon_test_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Check if file is in use while open
	inUse, err := IsFileInUse(tmpFile.Name())
	if err != nil {
		t.Fatalf("IsFileInUse failed: %v", err)
	}
	t.Logf("File in use while open: %v", inUse)

	// Close the file
	tmpFile.Close()

	// Check if file is in use after closing
	inUse, err = IsFileInUse(tmpFile.Name())
	if err != nil {
		t.Fatalf("IsFileInUse failed after close: %v", err)
	}
	t.Logf("File in use after close: %v", inUse)
}

func TestGetProcessInfo(t *testing.T) {
	// Get info about the current process
	pid := int32(os.Getpid())
	info, err := GetProcessInfo(pid)
	if err != nil {
		t.Fatalf("GetProcessInfo failed: %v", err)
	}

	if info.PID != pid {
		t.Errorf("Expected PID %d, got %d", pid, info.PID)
	}

	if info.Name == "" {
		t.Errorf("Process name is empty")
	}

	t.Logf("Current process: PID=%d, Name=%s, Exe=%s", info.PID, info.Name, info.Exe)
}

func TestGetProcessInfo_InvalidPID(t *testing.T) {
	// Try to get info for an invalid PID
	_, err := GetProcessInfo(999999)
	if err == nil {
		t.Errorf("Expected error for invalid PID, got nil")
	}
}

// TestIntegration tests the complete workflow
func TestIntegration(t *testing.T) {
	// Create a test file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.db")
	
	f, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer f.Close()
	defer os.Remove(testFile)

	// Write some data
	if _, err := f.WriteString("test database content"); err != nil {
		t.Fatalf("Failed to write to test file: %v", err)
	}

	// Check if file is in use
	inUse, err := IsFileInUse(testFile)
	if err != nil {
		t.Fatalf("Failed to check if file is in use: %v", err)
	}
	t.Logf("Test file in use: %v", inUse)

	// Find processes with file open
	fileInfo, err := FindProcessesWithFile(testFile)
	if err != nil {
		t.Fatalf("Failed to find processes with file: %v", err)
	}
	t.Logf("Found %d processes with test file open", len(fileInfo.Processes))

	// Log details about processes
	for _, p := range fileInfo.Processes {
		t.Logf("Process accessing file: PID=%d, Name=%s, Exe=%s", p.PID, p.Name, p.Exe)
	}
}
