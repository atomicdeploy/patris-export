package watcher

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
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
	newHash, err := fw.getFileHash(path)
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
	return fw.watcher.Close()
}

// Unwatch stops watching a specific file
func (fw *FileWatcher) Unwatch(path string) error {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	delete(fw.fileHashes, path)
	delete(fw.callbacks, path)
	delete(fw.debounce, path)

	return fw.watcher.Remove(path)
}
