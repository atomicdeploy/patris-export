package filecopy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCalculateHash(t *testing.T) {
	// Create a temporary test file
	content := []byte("test content for hashing")
	tmpFile, err := os.CreateTemp("", "test-hash-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(content); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	// Calculate hash
	hash, err := CalculateHash(tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to calculate hash: %v", err)
	}

	// Verify hash is not empty and has expected format (8 hex characters)
	if hash == "" {
		t.Error("Expected non-empty hash")
	}
	if len(hash) != 8 {
		t.Errorf("Expected hash length of 8, got %d", len(hash))
	}

	// Calculate again to verify consistency
	hash2, err := CalculateHash(tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to calculate hash again: %v", err)
	}
	if hash != hash2 {
		t.Errorf("Hash mismatch: %s != %s", hash, hash2)
	}

	t.Logf("Hash: %s", hash)
}

func TestCopyToTemp(t *testing.T) {
	// Create a temporary source file
	content := []byte("test content for copying")
	srcFile, err := os.CreateTemp("", "test-src-*.db")
	if err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}
	srcPath := srcFile.Name()
	defer os.Remove(srcPath)

	if _, err := srcFile.Write(content); err != nil {
		t.Fatalf("Failed to write to source file: %v", err)
	}

	// Set a specific modification time
	modTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(srcPath, time.Now(), modTime); err != nil {
		t.Fatalf("Failed to set mod time: %v", err)
	}
	srcFile.Close()

	// Copy to temp
	fileInfo, err := CopyToTemp(srcPath)
	if err != nil {
		t.Fatalf("Failed to copy to temp: %v", err)
	}
	defer CleanupTemp(fileInfo.TempPath)

	// Verify file info
	if fileInfo.SourcePath != srcPath {
		t.Errorf("Expected source path %s, got %s", srcPath, fileInfo.SourcePath)
	}
	if fileInfo.TempPath == "" {
		t.Error("Expected non-empty temp path")
	}
	if fileInfo.Hash == "" {
		t.Error("Expected non-empty hash")
	}
	if fileInfo.Size != int64(len(content)) {
		t.Errorf("Expected size %d, got %d", len(content), fileInfo.Size)
	}

	// Verify temp file exists
	if _, err := os.Stat(fileInfo.TempPath); os.IsNotExist(err) {
		t.Error("Temp file does not exist")
	}

	// Verify temp file content
	tempContent, err := os.ReadFile(fileInfo.TempPath)
	if err != nil {
		t.Fatalf("Failed to read temp file: %v", err)
	}
	if string(tempContent) != string(content) {
		t.Error("Temp file content does not match source")
	}

	// Verify modification time is preserved (with some tolerance for filesystem precision)
	tempInfo, err := os.Stat(fileInfo.TempPath)
	if err != nil {
		t.Fatalf("Failed to stat temp file: %v", err)
	}
	timeDiff := tempInfo.ModTime().Sub(modTime)
	if timeDiff < 0 {
		timeDiff = -timeDiff
	}
	if timeDiff > time.Second {
		t.Errorf("Modification time not preserved: expected %v, got %v", modTime, tempInfo.ModTime())
	}

	t.Logf("Source: %s", srcPath)
	t.Logf("Temp: %s", fileInfo.TempPath)
	t.Logf("Hash: %s", fileInfo.Hash)
	t.Logf("Size: %d bytes", fileInfo.Size)
}

func TestCopyToTempWithLargeFile(t *testing.T) {
	// Create a file larger than chunk size
	srcFile, err := os.CreateTemp("", "test-large-*.db")
	if err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}
	srcPath := srcFile.Name()
	defer os.Remove(srcPath)

	// Write 15MB of data (larger than 10MB chunk)
	chunk := make([]byte, 1024*1024) // 1MB
	for i := 0; i < 15; i++ {
		if _, err := srcFile.Write(chunk); err != nil {
			t.Fatalf("Failed to write chunk: %v", err)
		}
	}
	srcFile.Close()

	// Get source file size
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		t.Fatalf("Failed to stat source: %v", err)
	}

	// Copy to temp
	fileInfo, err := CopyToTemp(srcPath)
	if err != nil {
		t.Fatalf("Failed to copy large file: %v", err)
	}
	defer CleanupTemp(fileInfo.TempPath)

	// Verify size matches
	tempInfo, err := os.Stat(fileInfo.TempPath)
	if err != nil {
		t.Fatalf("Failed to stat temp file: %v", err)
	}
	if tempInfo.Size() != srcInfo.Size() {
		t.Errorf("Size mismatch: expected %d, got %d", srcInfo.Size(), tempInfo.Size())
	}

	t.Logf("Large file copied successfully: %d bytes", fileInfo.Size)
}

func TestCleanupTemp(t *testing.T) {
	// Create a temp file
	tmpFile, err := os.CreateTemp("", "test-cleanup-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	// Verify file exists
	if _, err := os.Stat(tmpPath); os.IsNotExist(err) {
		t.Fatal("Temp file does not exist")
	}

	// Cleanup
	if err := CleanupTemp(tmpPath); err != nil {
		t.Fatalf("Failed to cleanup: %v", err)
	}

	// Verify file is removed
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("Temp file still exists after cleanup")
	}

	// Cleanup again should not error
	if err := CleanupTemp(tmpPath); err != nil {
		t.Errorf("Cleanup of non-existent file should not error: %v", err)
	}

	// Cleanup empty path should not error
	if err := CleanupTemp(""); err != nil {
		t.Errorf("Cleanup of empty path should not error: %v", err)
	}
}

func TestCopyToTempNonExistentFile(t *testing.T) {
	_, err := CopyToTemp("/nonexistent/file.db")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestCalculateHashNonExistentFile(t *testing.T) {
	_, err := CalculateHash("/nonexistent/file.db")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestCopyToTempBasename(t *testing.T) {
	// Create a source file with a specific name
	srcDir, err := os.MkdirTemp("", "test-dir-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(srcDir)

	srcPath := filepath.Join(srcDir, "test-database.db")
	if err := os.WriteFile(srcPath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to write source file: %v", err)
	}

	// Copy to temp
	fileInfo, err := CopyToTemp(srcPath)
	if err != nil {
		t.Fatalf("Failed to copy: %v", err)
	}
	defer CleanupTemp(fileInfo.TempPath)

	// Verify temp file has same basename
	if filepath.Base(fileInfo.TempPath) != "test-database.db" {
		t.Errorf("Expected basename 'test-database.db', got '%s'", filepath.Base(fileInfo.TempPath))
	}

	// Verify temp file is in system temp directory under patris-export subdirectory
	expectedDir := filepath.Join(os.TempDir(), "patris-export")
	actualDir := filepath.Dir(fileInfo.TempPath)
	if actualDir != expectedDir {
		t.Errorf("Expected temp dir %s, got %s", expectedDir, actualDir)
	}
}
