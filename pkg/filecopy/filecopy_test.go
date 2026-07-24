package filecopy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

	// Verify temp filename starts with source basename and includes path hash
	baseName := filepath.Base(fileInfo.TempPath)
	if !strings.HasPrefix(baseName, "test-database.db.") {
		t.Errorf("Expected basename to start with 'test-database.db.', got '%s'", baseName)
	}

	// Verify temp file is in the selected patris-export temp directory.
	expectedDir := TempRootForSize(4)
	actualDir := filepath.Dir(fileInfo.TempPath)
	if actualDir != expectedDir {
		t.Errorf("Expected temp dir %s, got %s", expectedDir, actualDir)
	}

	// Verify the same source file always gets the same temp path (for watch mode)
	fileInfo2, err := CopyToTemp(srcPath)
	if err != nil {
		t.Fatalf("Failed to copy again: %v", err)
	}
	defer CleanupTemp(fileInfo2.TempPath)

	if fileInfo.TempPath != fileInfo2.TempPath {
		t.Errorf("Expected same temp path for same source file, got %s and %s", fileInfo.TempPath, fileInfo2.TempPath)
	}
}

func TestTempRootExplicitOverrideWins(t *testing.T) {
	override := filepath.Join(t.TempDir(), "override")
	SetTempDir(override)
	SetTempPolicy(TempStrategyMemory, DefaultMemoryTempLimitBytes)
	defer SetTempDir("")
	defer SetTempPolicy(TempStrategyAuto, DefaultMemoryTempLimitBytes)

	if got := TempRootForSize(1); got != override {
		t.Fatalf("explicit temp dir should win: got %s want %s", got, override)
	}
}

func TestTempRootSystemStrategy(t *testing.T) {
	SetTempDir("")
	SetTempPolicy(TempStrategySystem, DefaultMemoryTempLimitBytes)
	defer SetTempPolicy(TempStrategyAuto, DefaultMemoryTempLimitBytes)

	expected := filepath.Join(os.TempDir(), "patris-export")
	if got := TempRootForSize(1); got != expected {
		t.Fatalf("system strategy temp root = %s, want %s", got, expected)
	}
}

func TestTempRootLargeOrUnknownUsesSystem(t *testing.T) {
	SetTempDir("")
	SetTempPolicy(TempStrategyAuto, 10)
	defer SetTempPolicy(TempStrategyAuto, DefaultMemoryTempLimitBytes)

	expected := filepath.Join(os.TempDir(), "patris-export")
	if got := TempRootForSize(-1); got != expected {
		t.Fatalf("unknown size temp root = %s, want %s", got, expected)
	}
	if got := TempRootForSize(11); got != expected {
		t.Fatalf("oversized temp root = %s, want %s", got, expected)
	}
}

func TestIsURL(t *testing.T) {
	if !IsURL("https://example.com/kala.db") {
		t.Fatal("expected HTTPS URL to be recognized")
	}
	if !IsURL("http://127.0.0.1:8080/kala.db") {
		t.Fatal("expected HTTP URL to be recognized")
	}
	if IsURL("file:///tmp/kala.db") {
		t.Fatal("file URL should not be treated as a remote source")
	}
	if IsURL("C:/Patris/data4/kala.db") {
		t.Fatal("local path should not be treated as a URL")
	}
}

func TestDownloadToTemp(t *testing.T) {
	content := []byte("remote database content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Write(content)
	}))
	defer server.Close()

	info, err := DownloadToTemp(server.URL + "/kala.db")
	if err != nil {
		t.Fatalf("DownloadToTemp returned error: %v", err)
	}
	defer CleanupTemp(info.TempPath)

	if info.SourcePath != server.URL+"/kala.db" {
		t.Fatalf("unexpected source path: %s", info.SourcePath)
	}
	if info.Size != int64(len(content)) {
		t.Fatalf("expected size %d, got %d", len(content), info.Size)
	}
	got, err := os.ReadFile(info.TempPath)
	if err != nil {
		t.Fatalf("failed to read temp download: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("downloaded content mismatch: %q", got)
	}
}

func TestDownloadToTempContextCancelsWithoutPartialFile(t *testing.T) {
	tempRoot := t.TempDir()
	SetTempDir(tempRoot)
	defer SetTempDir("")

	requestStarted := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = response.Write([]byte("last-known-good"))
			return
		}
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte("partial-replacement"))
		if flusher, ok := response.(http.Flusher); ok {
			flusher.Flush()
		}
		close(requestStarted)
		<-request.Context().Done()
	}))
	defer server.Close()

	stable, err := DownloadToTemp(server.URL + "/delayed.db")
	if err != nil {
		t.Fatalf("create last-known-good download: %v", err)
	}
	defer CleanupTemp(stable.TempPath)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := DownloadToTempContext(ctx, server.URL+"/delayed.db")
		done <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("delayed download did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cancelled download error=%v, want context deadline", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled download did not return promptly")
	}
	content, err := os.ReadFile(stable.TempPath)
	if err != nil {
		t.Fatalf("read stable download after cancellation: %v", err)
	}
	if string(content) != "last-known-good" {
		t.Fatalf("cancelled replacement changed stable download: %q", content)
	}
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		t.Fatalf("read temp root: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(stable.TempPath) {
		t.Fatalf("cancelled download left partial files: %#v", entries)
	}
}

func TestCopyToTempContextRejectsPreCancelledRequest(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.db")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CopyToTempContext(ctx, source); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled copy error=%v, want context.Canceled", err)
	}
}
