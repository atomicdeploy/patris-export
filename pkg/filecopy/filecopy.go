package filecopy

import (
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

// CalculateHash calculates the CRC32 hash of a file.
// The file is opened in read-only mode (os.Open uses O_RDONLY by default).
func CalculateHash(filePath string) (string, error) {
	file, err := os.Open(filePath) // Opens in read-only mode
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
// and preserves the modification time. The hash is calculated during the copy operation
// in a single pass for efficiency. Returns information about the copied file.
// The source file is opened in read-only mode (os.Open uses O_RDONLY by default).
func CopyToTemp(sourcePath string) (*FileInfo, error) {
	// Get file info
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat source file: %w", err)
	}

	// Open source file in read-only mode
	source, err := os.Open(sourcePath) // Opens in read-only mode (O_RDONLY)
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

	// Ensure destination is closed properly, handling any write errors
	var closeErr error
	defer func() {
		if cerr := dest.Close(); cerr != nil && closeErr == nil {
			closeErr = cerr
		}
	}()

	// Calculate hash while copying file in chunks (single pass optimization)
	hash := crc32.NewIEEE()
	buffer := make([]byte, ChunkSize)
	for {
		n, readErr := source.Read(buffer)
		if readErr != nil && readErr != io.EOF {
			return nil, fmt.Errorf("failed to read from source: %w", readErr)
		}
		if n == 0 {
			break
		}

		// Update hash
		if _, err := hash.Write(buffer[:n]); err != nil {
			return nil, fmt.Errorf("failed to update hash: %w", err)
		}

		// Write to destination
		if _, err := dest.Write(buffer[:n]); err != nil {
			closeErr = err
			return nil, fmt.Errorf("failed to write to temp file: %w", err)
		}
	}

	// Check for deferred close errors
	if closeErr != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", closeErr)
	}

	// Preserve modification time
	modTime := sourceInfo.ModTime()
	if err := os.Chtimes(tempPath, time.Now(), modTime); err != nil {
		return nil, fmt.Errorf("failed to set modification time: %w", err)
	}

	return &FileInfo{
		SourcePath: sourcePath,
		TempPath:   tempPath,
		Hash:       fmt.Sprintf("%08x", hash.Sum32()),
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

// IsURL checks if a path string is a URL
func IsURL(path string) bool {
	// Quick check for common URL schemes
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return true
	}

	// More thorough check using net/url
	u, err := url.Parse(path)
	if err != nil {
		return false
	}

	// A valid URL should have a scheme and host
	return u.Scheme != "" && u.Host != ""
}

// DownloadToTemp downloads a file from a URL to a temporary location
// The hash is calculated during the download operation in a single pass for efficiency.
// Returns information about the downloaded file.
func DownloadToTemp(urlStr string) (*FileInfo, error) {
	// Parse URL to get filename
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	// Extract filename from URL path
	baseName := filepath.Base(u.Path)
	if baseName == "/" || baseName == "." || baseName == "" {
		baseName = "download.db"
	}

	// Create temp directory
	tempDir := filepath.Join(os.TempDir(), "patris-export")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Create a unique temp filename using URL hash
	urlHash := crc32.ChecksumIEEE([]byte(urlStr))
	tempFileName := fmt.Sprintf("%s.%08x", baseName, urlHash)
	tempPath := filepath.Join(tempDir, tempFileName)

	// Download file
	resp, err := http.Get(urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download file: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	// Create temp file
	dest, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	// Ensure destination is closed properly
	var closeErr error
	defer func() {
		if cerr := dest.Close(); cerr != nil && closeErr == nil {
			closeErr = cerr
		}
	}()

	// Calculate hash while downloading in chunks
	hash := crc32.NewIEEE()
	buffer := make([]byte, ChunkSize)
	var totalSize int64

	for {
		n, readErr := resp.Body.Read(buffer)
		if readErr != nil && readErr != io.EOF {
			return nil, fmt.Errorf("failed to read from URL: %w", readErr)
		}
		if n == 0 {
			break
		}

		totalSize += int64(n)

		// Update hash
		if _, err := hash.Write(buffer[:n]); err != nil {
			return nil, fmt.Errorf("failed to update hash: %w", err)
		}

		// Write to destination
		if _, err := dest.Write(buffer[:n]); err != nil {
			closeErr = err
			return nil, fmt.Errorf("failed to write to temp file: %w", err)
		}
	}

	// Check for deferred close errors
	if closeErr != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", closeErr)
	}

	return &FileInfo{
		SourcePath: urlStr,
		TempPath:   tempPath,
		Hash:       fmt.Sprintf("%08x", hash.Sum32()),
		Size:       totalSize,
		ModTime:    time.Now(),
	}, nil
}
