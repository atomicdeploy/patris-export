package watcher

import (
	"crypto/sha256"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileWatcher watches database files for changes
type FileWatcher struct {
	watcher    *fsnotify.Watcher
	fileHashes map[string]string
	mu         sync.RWMutex
	callbacks  map[string]func(string)
	debounce   map[string]time.Duration
	stopChans  map[string]chan struct{}
	pollers    map[string]bool
}

// NewFileWatcher creates a new file watcher
func NewFileWatcher() (*FileWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	return &FileWatcher{
		watcher:    watcher,
		fileHashes: make(map[string]string),
		callbacks:  make(map[string]func(string)),
		debounce:   make(map[string]time.Duration),
		stopChans:  make(map[string]chan struct{}),
		pollers:    make(map[string]bool),
	}, nil
}

// Watch starts watching a file or directory with a configurable debounce duration
func (fw *FileWatcher) Watch(path string, callback func(string), debounceDuration time.Duration) error {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	// Get initial hash
	hash, err := fw.getFileHash(path)
	if err != nil {
		return fmt.Errorf("failed to get initial hash: %w", err)
	}

	fw.fileHashes[path] = hash
	fw.callbacks[path] = callback
	fw.debounce[path] = debounceDuration

	// Add to watcher
	if err := fw.watcher.Add(path); err != nil {
		return fmt.Errorf("failed to watch file: %w", err)
	}

	return nil
}

// Start begins watching for file changes
func (fw *FileWatcher) Start() {
	go fw.watchLoop()
}

// watchLoop is the main event loop for file watching
func (fw *FileWatcher) watchLoop() {
	// Debounce timer to avoid multiple rapid events
	debounceTimers := make(map[string]*time.Timer)

	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}

			// Only process write and create events
			if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
				path := event.Name

				// Get debounce duration for this path
				fw.mu.RLock()
				debounceDuration := fw.debounce[path]
				fw.mu.RUnlock()

				// If debounce is 0, process immediately
				if debounceDuration == 0 {
					go fw.handleFileChange(path)
				} else {
					// Debounce: wait specified duration before processing
					if timer, exists := debounceTimers[path]; exists {
						timer.Stop()
					}

					debounceTimers[path] = time.AfterFunc(debounceDuration, func() {
						fw.handleFileChange(path)
						delete(debounceTimers, path)
					})
				}
			}

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("⚠️  Watcher error: %v", err)
		}
	}
}

// handleFileChange checks if file has actually changed and calls callback
func (fw *FileWatcher) handleFileChange(path string) {
	fw.mu.RLock()
	callback, hasCallback := fw.callbacks[path]
	oldHash := fw.fileHashes[path]
	fw.mu.RUnlock()

	if !hasCallback {
		return
	}

	// Calculate new hash
	newHash, err := fw.getHashForPath(path)
	if err != nil {
		log.Printf("⚠️  Failed to get hash for %s: %v", path, err)
		return
	}

	// Only trigger callback if hash changed
	if newHash != oldHash {
		fw.mu.Lock()
		fw.fileHashes[path] = newHash
		fw.mu.Unlock()

		callback(path)
	}
}

// getFileHash calculates SHA-256 hash of a file
func (fw *FileWatcher) getFileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// Close stops the file watcher
func (fw *FileWatcher) Close() error {
	// Stop all polling goroutines
	fw.mu.Lock()
	for _, stopChan := range fw.stopChans {
		close(stopChan)
	}
	fw.stopChans = make(map[string]chan struct{})
	fw.mu.Unlock()

	if fw.watcher != nil {
		return fw.watcher.Close()
	}
	return nil
}

// Unwatch stops watching a specific file
func (fw *FileWatcher) Unwatch(path string) error {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	// Stop polling if active
	if stopChan, exists := fw.stopChans[path]; exists {
		close(stopChan)
		delete(fw.stopChans, path)
	}

	delete(fw.fileHashes, path)
	delete(fw.callbacks, path)
	delete(fw.debounce, path)
	delete(fw.pollers, path)

	// Only try to remove from watcher if it's not a poller
	if fw.watcher != nil && !fw.pollers[path] {
		return fw.watcher.Remove(path)
	}

	return nil
}

// isURL checks if a path string is a URL
func isURL(path string) bool {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return true
	}
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	return u.Scheme != "" && u.Host != ""
}

// Poll starts polling a URL or local file at the specified interval
// This is used when fsnotify cannot be used (e.g., for URLs)
func (fw *FileWatcher) Poll(path string, callback func(string), pollInterval time.Duration) error {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	// Get initial hash
	hash, err := fw.getHashForPath(path)
	if err != nil {
		return fmt.Errorf("failed to get initial hash: %w", err)
	}

	fw.fileHashes[path] = hash
	fw.callbacks[path] = callback
	fw.debounce[path] = 0 // No debounce for polling
	fw.pollers[path] = true

	// Create stop channel
	stopChan := make(chan struct{})
	fw.stopChans[path] = stopChan

	// Start polling goroutine
	go fw.pollLoop(path, pollInterval, stopChan)

	return nil
}

// pollLoop continuously polls a path for changes
func (fw *FileWatcher) pollLoop(path string, interval time.Duration, stopChan chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fw.handleFileChange(path)
		case <-stopChan:
			return
		}
	}
}

// getHashForPath calculates hash for either a local file or URL
func (fw *FileWatcher) getHashForPath(path string) (string, error) {
	if isURL(path) {
		return fw.getURLHash(path)
	}
	return fw.getFileHash(path)
}

// getURLHash calculates CRC32 hash of a URL's ETag or content
func (fw *FileWatcher) getURLHash(urlStr string) (string, error) {
	// First try to get ETag from HEAD request
	resp, err := http.Head(urlStr)
	if err == nil {
		defer resp.Body.Close()
		etag := resp.Header.Get("ETag")
		lastModified := resp.Header.Get("Last-Modified")
		if etag != "" || lastModified != "" {
			// Use ETag and Last-Modified as a quick hash
			combined := etag + lastModified
			hash := crc32.ChecksumIEEE([]byte(combined))
			return fmt.Sprintf("%08x", hash), nil
		}
	}

	// If HEAD doesn't work or no ETag, fall back to content hash (expensive)
	resp, err = http.Get(urlStr)
	if err != nil {
		return "", fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP error: %d %s", resp.StatusCode, resp.Status)
	}

	hash := crc32.NewIEEE()
	if _, err := io.Copy(hash, resp.Body); err != nil {
		return "", fmt.Errorf("failed to calculate hash: %w", err)
	}

	return fmt.Sprintf("%08x", hash.Sum32()), nil
}
