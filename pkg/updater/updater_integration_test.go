package updater

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractExecutable_Linux(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "updater-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test ZIP file with a Linux executable
	zipPath := filepath.Join(tmpDir, "test.zip")
	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("Failed to create zip file: %v", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)

	// Test extraction
	u := NewUpdater("testowner", "testrepo")
	expectedName := u.GetPlatformBinaryName()
	
	// Add an executable with the expected name to the ZIP
	exeWriter, err := zipWriter.Create(expectedName)
	if err != nil {
		t.Fatalf("Failed to create file in zip: %v", err)
	}
	
	testContent := []byte("#!/bin/bash\necho 'test'")
	if _, err := exeWriter.Write(testContent); err != nil {
		t.Fatalf("Failed to write to zip: %v", err)
	}

	if err := zipWriter.Close(); err != nil {
		t.Fatalf("Failed to close zip: %v", err)
	}

	extractDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		t.Fatalf("Failed to create extract dir: %v", err)
	}

	execPath, err := u.ExtractExecutable(zipPath, extractDir)
	if err != nil {
		t.Fatalf("Failed to extract executable: %v", err)
	}

	// Verify the file exists and has correct permissions
	info, err := os.Stat(execPath)
	if err != nil {
		t.Fatalf("Failed to stat extracted file: %v", err)
	}

	// Check exact permissions (should be 0755)
	expectedPerms := os.FileMode(0755)
	if info.Mode().Perm() != expectedPerms {
		t.Errorf("Extracted file has permissions %o, expected %o", info.Mode().Perm(), expectedPerms)
	}

	// Verify content
	content, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("Failed to read extracted file: %v", err)
	}

	if string(content) != string(testContent) {
		t.Errorf("Expected content %q, got %q", string(testContent), string(content))
	}
}

func TestExtractExecutable_NoExecutable(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "updater-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test ZIP file without any executable
	zipPath := filepath.Join(tmpDir, "test.zip")
	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("Failed to create zip file: %v", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)

	// Add a non-executable file
	fileWriter, err := zipWriter.Create("readme.txt")
	if err != nil {
		t.Fatalf("Failed to create file in zip: %v", err)
	}
	
	if _, err := fileWriter.Write([]byte("test")); err != nil {
		t.Fatalf("Failed to write to zip: %v", err)
	}

	if err := zipWriter.Close(); err != nil {
		t.Fatalf("Failed to close zip: %v", err)
	}

	// Test extraction - should fail
	u := NewUpdater("testowner", "testrepo")
	extractDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		t.Fatalf("Failed to create extract dir: %v", err)
	}

	_, err = u.ExtractExecutable(zipPath, extractDir)
	if err == nil {
		t.Error("Expected error when no executable found, got nil")
	}

	// Check that error message contains the key phrase about no executable found
	if !strings.Contains(err.Error(), "no executable found in zip file") {
		t.Errorf("Expected error to contain 'no executable found in zip file', got %q", err.Error())
	}
}

func TestCopyFile(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "updater-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source file
	srcPath := filepath.Join(tmpDir, "source.txt")
	testContent := []byte("test content")
	if err := os.WriteFile(srcPath, testContent, 0644); err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}

	// Test copy
	dstPath := filepath.Join(tmpDir, "dest.txt")
	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("Failed to copy file: %v", err)
	}

	// Verify destination exists and has correct content
	content, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("Failed to read destination file: %v", err)
	}

	if string(content) != string(testContent) {
		t.Errorf("Expected content %q, got %q", string(testContent), string(content))
	}
}
