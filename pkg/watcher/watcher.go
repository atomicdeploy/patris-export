package watcher

import (
	"crypto/sha256"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/filecopy"
	"github.com/fsnotify/fsnotify"
)

var pollHTTPClient = &http.Client{Timeout: 30 * time.Second}

// FileWatcher watches database files for changes.
type FileWatcher struct {
	watcher     *fsnotify.Watcher
	fileHashes  map[string]string
	mu          sync.RWMutex
	callbacks   map[string]func(string)
	debounce    map[string]time.Duration
	watchedDirs map[string]int
	pathDirs    map[string]string
	stopChans   map[string]chan struct{}
	pollers     map[string]bool
}

// NewFileWatcher creates a new file watcher.
func NewFileWatcher() (*FileWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	return &FileWatcher{
		watcher:     watcher,
		fileHashes:  make(map[string]string),
		callbacks:   make(map[string]func(string)),
		debounce:    make(map[string]time.Duration),
		watchedDirs: make(map[string]int),
		pathDirs:    make(map[string]string),
		stopChans:   make(map[string]chan struct{}),
		pollers:     make(map[string]bool),
	}, nil
}

// Watch starts watching a local file with a configurable debounce duration.
func (fw *FileWatcher) Watch(path string, callback func(string), debounceDuration time.Duration) error {
	if filecopy.IsURL(path) {
		return fmt.Errorf("watch requires a local file path; use Poll for URL sources")
	}
	absPath, err := fw.normalizePath(path)
	if err != nil {
		return err
	}

	fw.mu.Lock()
	defer fw.mu.Unlock()

	hash, err := fw.getFileHash(absPath)
	if err != nil {
		return fmt.Errorf("failed to get initial hash: %w", err)
	}

	fw.fileHashes[absPath] = hash
	fw.callbacks[absPath] = callback
	fw.debounce[absPath] = debounceDuration

	dir := filepath.Dir(absPath)
	fw.pathDirs[absPath] = dir
	if fw.watchedDirs[dir] == 0 {
		if err := fw.watcher.Add(dir); err != nil {
			return fmt.Errorf("failed to watch directory: %w", err)
		}
	}
	fw.watchedDirs[dir]++

	return nil
}

// Poll checks a local file or URL on an interval and invokes callback after
// content changes. It is primarily used for remote database sources.
func (fw *FileWatcher) Poll(path string, callback func(string), interval time.Duration) error {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	key, err := fw.normalizePath(path)
	if err != nil {
		return err
	}

	hash, err := fw.getHashForPath(key)
	if err != nil {
		return fmt.Errorf("failed to get initial hash: %w", err)
	}

	stop := make(chan struct{})

	fw.mu.Lock()
	fw.fileHashes[key] = hash
	fw.callbacks[key] = callback
	fw.debounce[key] = 0
	fw.stopChans[key] = stop
	fw.pollers[key] = true
	fw.mu.Unlock()

	go fw.pollLoop(key, interval, stop)
	return nil
}

// Start begins watching for local file changes.
func (fw *FileWatcher) Start() {
	go fw.watchLoop()
}

func (fw *FileWatcher) watchLoop() {
	debounceTimers := make(map[string]*time.Timer)

	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}

			if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
				path, err := filepath.Abs(event.Name)
				if err != nil {
					continue
				}

				fw.mu.RLock()
				_, watched := fw.callbacks[path]
				debounceDuration := fw.debounce[path]
				fw.mu.RUnlock()
				if !watched {
					continue
				}

				if debounceDuration == 0 {
					go fw.handleFileChange(path)
				} else {
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

func (fw *FileWatcher) pollLoop(path string, interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fw.handleFileChange(path)
		case <-stop:
			return
		}
	}
}

func (fw *FileWatcher) handleFileChange(path string) {
	fw.mu.RLock()
	oldHash := fw.fileHashes[path]
	fw.mu.RUnlock()

	newHash, err := fw.getHashForPath(path)
	if err != nil {
		log.Printf("⚠️  Failed to get hash for %s: %v", path, err)
		return
	}

	if newHash != oldHash {
		fw.mu.Lock()
		callback, hasCallback := fw.callbacks[path]
		if !hasCallback {
			fw.mu.Unlock()
			return
		}
		fw.fileHashes[path] = newHash
		fw.mu.Unlock()

		callback(path)
	}
}

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

func (fw *FileWatcher) getHashForPath(path string) (string, error) {
	if filecopy.IsURL(path) {
		return fw.getURLHash(path)
	}
	return fw.getFileHash(path)
}

func (fw *FileWatcher) getURLHash(sourceURL string) (string, error) {
	req, err := http.NewRequest(http.MethodHead, sourceURL, nil)
	if err == nil {
		req.Header.Set("User-Agent", "patris-export")
		if resp, err := pollHTTPClient.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				fingerprint := resp.Header.Get("ETag") + "|" + resp.Header.Get("Last-Modified") + "|" + resp.Header.Get("Content-Length")
				if fingerprint != "||" {
					return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(fingerprint))), nil
				}
			}
		}
	}

	fileInfo, err := filecopy.DownloadToTemp(sourceURL)
	if err != nil {
		return "", err
	}
	defer filecopy.CleanupTemp(fileInfo.TempPath)
	return fileInfo.Hash, nil
}

func (fw *FileWatcher) normalizePath(path string) (string, error) {
	if filecopy.IsURL(path) {
		return path, nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}
	return absPath, nil
}

// Close stops the file watcher.
func (fw *FileWatcher) Close() error {
	fw.mu.Lock()
	for path, stopChan := range fw.stopChans {
		close(stopChan)
		delete(fw.stopChans, path)
	}
	fw.mu.Unlock()
	return fw.watcher.Close()
}

// Unwatch stops watching or polling a specific path.
func (fw *FileWatcher) Unwatch(path string) error {
	key, err := fw.normalizePath(path)
	if err != nil {
		return err
	}
	fw.mu.Lock()
	defer fw.mu.Unlock()

	if stopChan, exists := fw.stopChans[key]; exists {
		close(stopChan)
		delete(fw.stopChans, key)
	}
	isPoller := fw.pollers[key]
	delete(fw.pollers, key)

	delete(fw.fileHashes, key)
	delete(fw.callbacks, key)
	delete(fw.debounce, key)
	dir := fw.pathDirs[key]
	delete(fw.pathDirs, key)

	if dir != "" && !isPoller {
		fw.watchedDirs[dir]--
		if fw.watchedDirs[dir] <= 0 {
			delete(fw.watchedDirs, dir)
			return fw.watcher.Remove(dir)
		}
	}
	return nil
}
