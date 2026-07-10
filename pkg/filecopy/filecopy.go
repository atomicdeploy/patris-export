package filecopy

import (
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// ChunkSize defines the size of chunks for file copying (10MB)
	ChunkSize = 10 * 1024 * 1024

	// DefaultMemoryTempLimitBytes is the default cap for tmpfs-backed copies.
	DefaultMemoryTempLimitBytes = 100 * 1024 * 1024

	TempStrategyAuto   = "auto"
	TempStrategySystem = "system"
	TempStrategyMemory = "memory"
)

var (
	httpClient               = &http.Client{Timeout: 2 * time.Minute}
	tempDirOverride          string
	tempStrategy                   = TempStrategyAuto
	tempMemoryLimitBytes     int64 = DefaultMemoryTempLimitBytes
	tempDirMu                sync.RWMutex
	memoryTempFreeSpaceSlack int64 = 8 * 1024 * 1024
)

// SetTempDir configures the base temp directory used by copy/download helpers.
// An empty value resets the helpers to the system temp directory.
func SetTempDir(path string) {
	tempDirMu.Lock()
	tempDirOverride = strings.TrimSpace(path)
	tempDirMu.Unlock()
}

// SetTempPolicy configures automatic temp storage selection. The system
// strategy always uses os.TempDir, memory tries /dev/shm when safe, and auto
// prefers memory only for small known-size files on supported platforms.
func SetTempPolicy(strategy string, memoryLimitBytes int64) {
	tempDirMu.Lock()
	tempStrategy = normalizeTempStrategy(strategy)
	if memoryLimitBytes > 0 {
		tempMemoryLimitBytes = memoryLimitBytes
	} else {
		tempMemoryLimitBytes = DefaultMemoryTempLimitBytes
	}
	tempDirMu.Unlock()
}

// TempRootForSize returns the directory used for temporary files of sizeHint.
// Explicit SetTempDir overrides always win. A negative size means unknown.
func TempRootForSize(sizeHint int64) string {
	tempDirMu.RLock()
	override := tempDirOverride
	strategy := tempStrategy
	limit := tempMemoryLimitBytes
	tempDirMu.RUnlock()

	if override != "" {
		return override
	}
	if shouldUseMemoryTemp(strategy, sizeHint, limit) {
		return filepath.Join(memoryTempBaseDir(), "patris-export")
	}
	return systemTempRoot()
}

func tempRootForSize(sizeHint int64) string {
	return TempRootForSize(sizeHint)
}

func tempRoot() string {
	return tempRootForSize(-1)
}

func systemTempRoot() string {
	return filepath.Join(os.TempDir(), "patris-export")
}

func normalizeTempStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "", TempStrategyAuto:
		return TempStrategyAuto
	case TempStrategySystem, "disk", "tmp":
		return TempStrategySystem
	case TempStrategyMemory, "shm", "tmpfs", "ram":
		return TempStrategyMemory
	default:
		return TempStrategyAuto
	}
}

func shouldUseMemoryTemp(strategy string, sizeHint int64, limit int64) bool {
	strategy = normalizeTempStrategy(strategy)
	if strategy == TempStrategySystem {
		return false
	}
	if sizeHint < 0 || limit <= 0 || sizeHint > limit {
		return false
	}
	if runtime.GOOS != "linux" {
		return false
	}

	base := memoryTempBaseDir()
	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		return false
	}
	if available, ok := availableBytes(base); ok {
		required := uint64(sizeHint)
		if memoryTempFreeSpaceSlack > 0 {
			required += uint64(memoryTempFreeSpaceSlack)
		}
		if available < required {
			return false
		}
	}
	return true
}

func memoryTempBaseDir() string {
	return "/dev/shm"
}

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

	// Create temp file in the selected temp directory. Use a subdirectory to
	// avoid conflicts with source files that might already be in temp storage.
	// Include a hash of the absolute path to handle multiple files with same name
	tempDir := tempRootForSize(sourceInfo.Size())
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

// IsURL reports whether path is an HTTP or HTTPS URL.
func IsURL(path string) bool {
	u, err := url.Parse(strings.TrimSpace(path))
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// DownloadToTemp downloads an HTTP/HTTPS file into the patris-export temp
// directory. The temp filename is stable for the URL so polling and repeated
// reads reuse the same location while still refreshing the content.
func DownloadToTemp(sourceURL string) (*FileInfo, error) {
	u, err := url.Parse(strings.TrimSpace(sourceURL))
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}
	if !IsURL(sourceURL) {
		return nil, fmt.Errorf("unsupported URL scheme: %s", u.Scheme)
	}

	baseName := filepath.Base(u.Path)
	if baseName == "." || baseName == "/" || baseName == "" {
		baseName = "download.db"
	}

	urlHash := crc32.ChecksumIEEE([]byte(sourceURL))

	req, err := http.NewRequest(http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "patris-export")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("failed to download URL: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	tempDir := tempRootForSize(resp.ContentLength)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	tempPath := filepath.Join(tempDir, fmt.Sprintf("%s.%08x", baseName, urlHash))

	dest, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	var closeErr error
	defer func() {
		if cerr := dest.Close(); cerr != nil && closeErr == nil {
			closeErr = cerr
		}
	}()

	hash := crc32.NewIEEE()
	written, err := io.CopyBuffer(io.MultiWriter(dest, hash), resp.Body, make([]byte, ChunkSize))
	if err != nil {
		closeErr = err
		return nil, fmt.Errorf("failed to write downloaded file: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", closeErr)
	}

	modTime := time.Now()
	if lastModified := resp.Header.Get("Last-Modified"); lastModified != "" {
		if parsed, err := http.ParseTime(lastModified); err == nil {
			modTime = parsed
			_ = os.Chtimes(tempPath, time.Now(), modTime)
		}
	}

	return &FileInfo{
		SourcePath: sourceURL,
		TempPath:   tempPath,
		Hash:       fmt.Sprintf("%08x", hash.Sum32()),
		Size:       written,
		ModTime:    modTime,
	}, nil
}
