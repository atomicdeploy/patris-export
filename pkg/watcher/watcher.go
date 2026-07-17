package watcher

import (
	"crypto/sha256"
	"errors"
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

// ErrClosed reports that Watch or Poll was called after the watcher closed.
var ErrClosed = errors.New("file watcher is closed")

// ErrAlreadyRegistered reports that a path already has a Watch or Poll
// registration. Call Unwatch before registering the same normalized path again.
var ErrAlreadyRegistered = errors.New("path is already registered; call Unwatch before registering it again")

type debounceTimer struct {
	timer                  *time.Timer
	timerGeneration        uint64
	registrationGeneration uint64
}

// FileWatcher watches database files for changes.
type FileWatcher struct {
	watcher         *fsnotify.Watcher
	fileHashes      map[string]string
	mu              sync.RWMutex
	callbacks       map[string]func(string)
	debounce        map[string]time.Duration
	watchedDirs     map[string]int
	pathDirs        map[string]string
	stopChans       map[string]chan struct{}
	pollers         map[string]bool
	timers          map[string]debounceTimer
	timerSeq        uint64
	registrations   map[string]uint64
	registrationSeq uint64
	hashForPath     func(string) (string, error)
	startOnce       sync.Once
	closeOnce       sync.Once
	closed          bool
	closeErr        error
}

// NewFileWatcher creates a new file watcher.
func NewFileWatcher() (*FileWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	fw := &FileWatcher{
		watcher:       watcher,
		fileHashes:    make(map[string]string),
		callbacks:     make(map[string]func(string)),
		debounce:      make(map[string]time.Duration),
		watchedDirs:   make(map[string]int),
		pathDirs:      make(map[string]string),
		stopChans:     make(map[string]chan struct{}),
		pollers:       make(map[string]bool),
		timers:        make(map[string]debounceTimer),
		registrations: make(map[string]uint64),
	}
	fw.hashForPath = fw.getHashForPath
	return fw, nil
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
	if err := fw.registrationErrorLocked(absPath); err != nil {
		return err
	}

	hash, err := fw.getFileHash(absPath)
	if err != nil {
		return fmt.Errorf("failed to get initial hash: %w", err)
	}

	dir := filepath.Dir(absPath)
	if fw.watchedDirs[dir] == 0 {
		if err := fw.watcher.Add(dir); err != nil {
			return fmt.Errorf("failed to watch directory: %w", err)
		}
	}

	fw.registrationSeq++
	fw.fileHashes[absPath] = hash
	fw.callbacks[absPath] = callback
	fw.debounce[absPath] = debounceDuration
	fw.pathDirs[absPath] = dir
	fw.registrations[absPath] = fw.registrationSeq
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
	fw.mu.RLock()
	registrationErr := fw.registrationErrorLocked(key)
	fw.mu.RUnlock()
	if registrationErr != nil {
		return registrationErr
	}

	hash, err := fw.getHashForPath(key)
	if err != nil {
		return fmt.Errorf("failed to get initial hash: %w", err)
	}

	stop := make(chan struct{})

	fw.mu.Lock()
	if err := fw.registrationErrorLocked(key); err != nil {
		fw.mu.Unlock()
		return err
	}
	fw.registrationSeq++
	fw.fileHashes[key] = hash
	fw.callbacks[key] = callback
	fw.debounce[key] = 0
	fw.stopChans[key] = stop
	fw.pollers[key] = true
	fw.registrations[key] = fw.registrationSeq
	generation := fw.registrationSeq
	fw.mu.Unlock()

	go fw.pollLoop(key, generation, interval, stop)
	return nil
}

// registrationErrorLocked validates whether path may be registered. The caller
// must hold fw.mu for reading or writing.
func (fw *FileWatcher) registrationErrorLocked(path string) error {
	if fw.closed {
		return ErrClosed
	}
	if _, exists := fw.registrations[path]; exists {
		return fmt.Errorf("%w: %s", ErrAlreadyRegistered, path)
	}
	return nil
}

// Start begins watching for local file changes.
func (fw *FileWatcher) Start() {
	fw.startOnce.Do(func() {
		fw.mu.Lock()
		if fw.closed {
			fw.mu.Unlock()
			return
		}
		fw.mu.Unlock()

		go fw.watchLoop()
	})
}

func (fw *FileWatcher) watchLoop() {
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

				fw.queueFileChange(path)
			}

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("⚠️  Watcher error: %v", err)
		}
	}
}

// queueFileChange snapshots the current registration generation before
// starting immediate or debounced work. Stale work cannot claim a callback
// after Unwatch and a later re-registration of the same path.
func (fw *FileWatcher) queueFileChange(path string) {
	fw.mu.Lock()
	if fw.closed {
		fw.mu.Unlock()
		return
	}
	registrationGeneration, watched := fw.registrations[path]
	if !watched {
		fw.mu.Unlock()
		return
	}

	debounceDuration := fw.debounce[path]
	if debounceDuration <= 0 {
		fw.mu.Unlock()
		go fw.handleFileChange(path, registrationGeneration)
		return
	}

	fw.stopDebounceTimerLocked(path)
	fw.timerSeq++
	timerGeneration := fw.timerSeq
	timer := time.AfterFunc(debounceDuration, func() {
		fw.fireDebounced(path, timerGeneration, registrationGeneration)
	})
	fw.timers[path] = debounceTimer{
		timer:                  timer,
		timerGeneration:        timerGeneration,
		registrationGeneration: registrationGeneration,
	}
	fw.mu.Unlock()
}

func (fw *FileWatcher) fireDebounced(path string, timerGeneration, registrationGeneration uint64) {
	fw.mu.Lock()
	scheduled, exists := fw.timers[path]
	if fw.closed || !exists ||
		scheduled.timerGeneration != timerGeneration ||
		scheduled.registrationGeneration != registrationGeneration ||
		fw.registrations[path] != registrationGeneration {
		fw.mu.Unlock()
		return
	}
	delete(fw.timers, path)
	fw.mu.Unlock()
	fw.handleFileChange(path, registrationGeneration)
}

// stopDebounceTimerLocked cancels one registered timer. The caller must hold
// fw.mu. A callback already released by time.Timer remains harmless because
// fireDebounced and claimCallback both verify the registration generation.
func (fw *FileWatcher) stopDebounceTimerLocked(path string) {
	scheduled, exists := fw.timers[path]
	if !exists {
		return
	}
	delete(fw.timers, path)
	scheduled.timer.Stop()
}

func (fw *FileWatcher) pollLoop(path string, registrationGeneration uint64, interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fw.handleFileChange(path, registrationGeneration)
		case <-stop:
			return
		}
	}
}

func (fw *FileWatcher) handleFileChange(path string, registrationGeneration uint64) {
	newHash, err := fw.hashForPath(path)
	if err != nil {
		log.Printf("⚠️  Failed to get hash for %s: %v", path, err)
		return
	}

	callback, claimed := fw.claimCallback(path, registrationGeneration, newHash)
	if !claimed {
		return
	}
	callback(path)
}

// claimCallback atomically commits a new hash and reserves one callback for it.
// Rechecking under the write lock collapses duplicate fsnotify events that hash
// the same file concurrently.
func (fw *FileWatcher) claimCallback(path string, registrationGeneration uint64, newHash string) (func(string), bool) {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	if fw.closed || fw.registrations[path] != registrationGeneration {
		return nil, false
	}
	oldHash, hasHash := fw.fileHashes[path]
	callback, hasCallback := fw.callbacks[path]
	if !hasHash || !hasCallback || callback == nil || newHash == oldHash {
		return nil, false
	}

	fw.fileHashes[path] = newHash
	return callback, true
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

// Close cancels future watcher work, pending debounce timers, and pollers.
// Repeated and concurrent calls return the same result. A callback that was
// already claimed may finish after Close returns, which keeps Close safe when
// it is called synchronously from inside that callback.
func (fw *FileWatcher) Close() error {
	fw.closeOnce.Do(func() {
		fw.closeErr = fw.close()
	})
	return fw.closeErr
}

func (fw *FileWatcher) close() error {
	fw.mu.Lock()
	fw.closed = true
	for path := range fw.timers {
		fw.stopDebounceTimerLocked(path)
	}
	for path, stopChan := range fw.stopChans {
		close(stopChan)
		delete(fw.stopChans, path)
	}
	clear(fw.callbacks)
	clear(fw.fileHashes)
	clear(fw.debounce)
	clear(fw.pathDirs)
	clear(fw.pollers)
	clear(fw.watchedDirs)
	clear(fw.registrations)
	fw.mu.Unlock()

	return fw.watcher.Close()
}

// Unwatch prevents future callbacks from being claimed for path and cancels
// its poller or pending debounce timer. A callback already claimed before the
// removal may still start or finish after Unwatch returns. This non-blocking
// teardown makes it safe for a callback to unwatch itself.
func (fw *FileWatcher) Unwatch(path string) error {
	key, err := fw.normalizePath(path)
	if err != nil {
		return err
	}
	fw.mu.Lock()
	if fw.closed {
		fw.mu.Unlock()
		return nil
	}

	if stopChan, exists := fw.stopChans[key]; exists {
		close(stopChan)
		delete(fw.stopChans, key)
	}
	isPoller := fw.pollers[key]
	delete(fw.pollers, key)
	fw.stopDebounceTimerLocked(key)

	delete(fw.fileHashes, key)
	delete(fw.callbacks, key)
	delete(fw.debounce, key)
	delete(fw.registrations, key)
	dir := fw.pathDirs[key]
	delete(fw.pathDirs, key)

	var removeErr error
	if dir != "" && !isPoller {
		fw.watchedDirs[dir]--
		if fw.watchedDirs[dir] <= 0 {
			delete(fw.watchedDirs, dir)
			removeErr = fw.watcher.Remove(dir)
		}
	}

	fw.mu.Unlock()
	return removeErr
}
