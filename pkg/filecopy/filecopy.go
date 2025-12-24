package filecopy

import (
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	// ChunkSize defines the size of chunks for file copying (10MB)
	ChunkSize = 10 * 1024 * 1024
)

// FileInfo contains information about a file copy operation
type FileInfo struct {
	SourcePath string
	TempPath   string
	Hash       string
	Size       int64
	ModTime    time.Time
}

// CalculateHash calculates the CRC32 hash of a file
func CalculateHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file for hashing: %w", err)
	}
	defer file.Close()

	hash := crc32.NewIEEE()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to calculate hash: %w", err)
	}

	return fmt.Sprintf("%08x", hash.Sum32()), nil
}

// CopyToTemp copies a database file to a temporary location with chunked reading
// and preserves the modification time. Returns information about the copied file.
func CopyToTemp(sourcePath string) (*FileInfo, error) {
	// Get file info
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat source file: %w", err)
	}

	// Calculate hash of source file
	hash, err := CalculateHash(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate hash: %w", err)
	}

	// Open source file
	source, err := os.Open(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open source file: %w", err)
	}
	defer source.Close()

	// Create temp file in system temp directory
	// Use a subdirectory to avoid conflicts with source files that might be in /tmp
	// Include a hash of the absolute path to handle multiple files with same name
	tempDir := filepath.Join(os.TempDir(), "patris-export")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Get absolute path for consistent hashing
	absPath, err := filepath.Abs(sourcePath)
	if err != nil {
		absPath = sourcePath // Fallback to original path
	}

	// Create a unique temp filename using source filename + hash of absolute path
	baseName := filepath.Base(sourcePath)
	pathHash := crc32.ChecksumIEEE([]byte(absPath))
	tempFileName := fmt.Sprintf("%s.%08x", baseName, pathHash)
	tempPath := filepath.Join(tempDir, tempFileName)

	// Open/create destination file
	dest, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer dest.Close()

	// Copy file in chunks
	buffer := make([]byte, ChunkSize)
	for {
		n, err := source.Read(buffer)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read from source: %w", err)
		}
		if n == 0 {
			break
		}

		if _, err := dest.Write(buffer[:n]); err != nil {
			return nil, fmt.Errorf("failed to write to temp file: %w", err)
		}
	}

	// Preserve modification time
	modTime := sourceInfo.ModTime()
	if err := os.Chtimes(tempPath, time.Now(), modTime); err != nil {
		return nil, fmt.Errorf("failed to set modification time: %w", err)
	}

	return &FileInfo{
		SourcePath: sourcePath,
		TempPath:   tempPath,
		Hash:       hash,
		Size:       sourceInfo.Size(),
		ModTime:    modTime,
	}, nil
}

// CleanupTemp removes a temporary file if it exists
func CleanupTemp(tempPath string) error {
	if tempPath == "" {
		return nil
	}

	if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove temp file: %w", err)
	}

	return nil
}
