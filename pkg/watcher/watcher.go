package watcher

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/filecopy"
	"github.com/fsnotify/fsnotify"
)

// ErrClosed reports that Watch or WatchEvents was called after closure.
var ErrClosed = errors.New("file watcher is closed")

// ErrAlreadyRegistered reports that a path already has a watch registration.
// registration. Call Unwatch before registering the same normalized path again.
var ErrAlreadyRegistered = errors.New("path is already registered; call Unwatch before registering it again")

type debounceTimer struct {
	timer                  *time.Timer
	timerGeneration        uint64
	registrationGeneration uint64
	event                  Event
}

// EventReason describes the operating-system signal that caused a source
// observation. Startup and overflow observations deliberately force a callback
// even when the primary file hash is unchanged: a companion/WAL write may be
// the only visible evidence that the logical database changed.
type EventReason string

const (
	EventChange    EventReason = "filesystem_change"
	EventCompanion EventReason = "companion_change"
	EventStartup   EventReason = "startup_catch_up"
	EventOverflow  EventReason = "watch_overflow"
)

// Event is acknowledged only when its callback returns nil. This lets callers
// durably persist work before the watcher advances its file-content baseline.
type Event struct {
	Path       string
	Reason     EventReason
	ObservedAt time.Time
	Retry      bool
}

// FileWatcher watches database files for changes.
type FileWatcher struct {
	watcher         *fsnotify.Watcher
	fileHashes      map[string]string
	mu              sync.RWMutex
	callbacks       map[string]func(string)
	eventCallbacks  map[string]func(Event) error
	inFlight        map[string]bool
	pendingEvents   map[string]Event
	debounce        map[string]time.Duration
	watchedDirs     map[string]int
	pathDirs        map[string]string
	timers          map[string]debounceTimer
	timerSeq        uint64
	registrations   map[string]uint64
	registrationSeq uint64
	hashForPath     func(string) (string, error)
	retryDelay      time.Duration
	startOnce       sync.Once
	closeOnce       sync.Once
	eventWG         sync.WaitGroup
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
		watcher:        watcher,
		fileHashes:     make(map[string]string),
		callbacks:      make(map[string]func(string)),
		eventCallbacks: make(map[string]func(Event) error),
		inFlight:       make(map[string]bool),
		pendingEvents:  make(map[string]Event),
		debounce:       make(map[string]time.Duration),
		watchedDirs:    make(map[string]int),
		pathDirs:       make(map[string]string),
		timers:         make(map[string]debounceTimer),
		registrations:  make(map[string]uint64),
		retryDelay:     time.Second,
	}
	fw.hashForPath = fw.getHashForPath
	return fw, nil
}

// WatchEvents registers a durable-acknowledgement callback for a local source.
// The callback must return nil only after the observed logical revision is
// durably captured. Related Paradox/SQLite companion-file events are mapped to
// the registered primary .db path.
func (fw *FileWatcher) WatchEvents(path string, callback func(Event) error, debounceDuration time.Duration) error {
	if callback == nil {
		return errors.New("watch event callback is required")
	}
	return fw.watchLocal(path, nil, callback, debounceDuration)
}

// Watch starts watching a local file with a configurable debounce duration.
func (fw *FileWatcher) Watch(path string, callback func(string), debounceDuration time.Duration) error {
	return fw.watchLocal(path, callback, nil, debounceDuration)
}

func (fw *FileWatcher) watchLocal(path string, callback func(string), eventCallback func(Event) error, debounceDuration time.Duration) error {
	if filecopy.IsURL(path) {
		return fmt.Errorf("event-driven watch requires a local file path")
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
	if eventCallback != nil {
		fw.eventCallbacks[absPath] = eventCallback
	}
	fw.debounce[absPath] = debounceDuration
	fw.pathDirs[absPath] = dir
	fw.registrations[absPath] = fw.registrationSeq
	fw.watchedDirs[dir]++

	return nil
}

// Trigger queues a forced source observation. It is used for startup catch-up
// and can also be used by a host that receives an explicit overflow signal.
func (fw *FileWatcher) Trigger(path string, reason EventReason) error {
	key, err := fw.normalizePath(path)
	if err != nil {
		return err
	}
	if reason != EventStartup && reason != EventOverflow {
		return fmt.Errorf("unsupported forced event reason %q", reason)
	}
	fw.mu.RLock()
	_, registered := fw.registrations[key]
	fw.mu.RUnlock()
	if !registered {
		return fmt.Errorf("path is not registered: %s", key)
	}
	fw.queueFileChangeReason(key, reason)
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

			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) != 0 {
				path, err := filepath.Abs(event.Name)
				if err != nil {
					continue
				}

				fw.queueRelatedFileChange(path)
			}

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("⚠️  Watcher error: %v", err)
			fw.handleWatchError(err)
		}
	}
}

func (fw *FileWatcher) handleWatchError(err error) {
	if !errors.Is(err, fsnotify.ErrEventOverflow) {
		return
	}
	fw.mu.RLock()
	paths := make([]string, 0, len(fw.eventCallbacks))
	for path := range fw.eventCallbacks {
		paths = append(paths, path)
	}
	fw.mu.RUnlock()
	for _, path := range paths {
		fw.queueFileChangeReason(path, EventOverflow)
	}
}

func (fw *FileWatcher) queueRelatedFileChange(changedPath string) {
	fw.mu.RLock()
	paths := make([]string, 0, len(fw.registrations))
	for path := range fw.registrations {
		if relatedSourcePath(path, changedPath) {
			paths = append(paths, path)
		}
	}
	fw.mu.RUnlock()
	for _, path := range paths {
		reason := EventChange
		if filepath.Clean(path) != filepath.Clean(changedPath) {
			reason = EventCompanion
		}
		fw.queueFileChangeReason(path, reason)
	}
}

func relatedSourcePath(primary, changed string) bool {
	if filepath.Clean(primary) == filepath.Clean(changed) {
		return true
	}
	if !strings.EqualFold(filepath.Dir(primary), filepath.Dir(changed)) || !strings.EqualFold(filepath.Ext(primary), ".db") {
		return false
	}
	stem := strings.TrimSuffix(strings.ToLower(filepath.Base(primary)), strings.ToLower(filepath.Ext(primary)))
	name := strings.ToLower(filepath.Base(changed))
	return strings.HasPrefix(name, stem+".")
}

// queueFileChange snapshots the current registration generation before
// starting immediate or debounced work. Stale work cannot claim a callback
// after Unwatch and a later re-registration of the same path.
func (fw *FileWatcher) queueFileChange(path string) {
	fw.queueFileChangeReason(path, EventChange)
}

func (fw *FileWatcher) queueFileChangeReason(path string, reason EventReason) {
	fw.queueFileEvent(Event{
		Path:       path,
		Reason:     reason,
		ObservedAt: time.Now().UTC(),
	}, 0, 0)
}

// queueFileEvent preserves the first observation time while coalescing newer
// filesystem signals. minimumDelay is used only for autonomous retries so a
// zero-debounce watch cannot spin after a transient read or callback failure.
func (fw *FileWatcher) queueFileEvent(event Event, minimumDelay time.Duration, expectedGeneration uint64) {
	path := event.Path
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now().UTC()
	}
	fw.mu.Lock()
	if fw.closed {
		fw.mu.Unlock()
		return
	}
	registrationGeneration, watched := fw.registrations[path]
	if !watched || (expectedGeneration != 0 && registrationGeneration != expectedGeneration) {
		fw.mu.Unlock()
		return
	}
	if fw.inFlight[path] {
		fw.pendingEvents[path] = mergeEvents(fw.pendingEvents[path], event)
		fw.mu.Unlock()
		return
	}
	if scheduled, exists := fw.timers[path]; exists {
		event = mergeEvents(scheduled.event, event)
		fw.stopDebounceTimerLocked(path)
	}

	debounceDuration := fw.debounce[path]
	if debounceDuration < minimumDelay {
		debounceDuration = minimumDelay
	}
	if debounceDuration <= 0 {
		if fw.eventCallbacks[path] != nil {
			fw.inFlight[path] = true
			fw.eventWG.Add(1)
			fw.mu.Unlock()
			go fw.runReservedFileEvent(path, registrationGeneration, event)
			return
		}
		fw.mu.Unlock()
		go fw.handleFileEvent(path, registrationGeneration, event)
		return
	}

	fw.timerSeq++
	timerGeneration := fw.timerSeq
	timer := time.AfterFunc(debounceDuration, func() {
		fw.fireDebounced(path, timerGeneration, registrationGeneration)
	})
	fw.timers[path] = debounceTimer{
		timer:                  timer,
		timerGeneration:        timerGeneration,
		registrationGeneration: registrationGeneration,
		event:                  event,
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
	if fw.eventCallbacks[path] != nil {
		if fw.inFlight[path] {
			fw.pendingEvents[path] = mergeEvents(fw.pendingEvents[path], scheduled.event)
			fw.mu.Unlock()
			return
		}
		fw.inFlight[path] = true
		fw.eventWG.Add(1)
		fw.mu.Unlock()
		fw.runReservedFileEvent(path, registrationGeneration, scheduled.event)
		return
	}
	fw.mu.Unlock()
	fw.handleFileEvent(path, registrationGeneration, scheduled.event)
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

func (fw *FileWatcher) handleFileChange(path string, registrationGeneration uint64) {
	fw.queueFileEvent(Event{
		Path:       path,
		Reason:     EventChange,
		ObservedAt: time.Now().UTC(),
	}, 0, registrationGeneration)
}

func (fw *FileWatcher) handleFileEvent(path string, registrationGeneration uint64, event Event) {
	event.Path = path
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now().UTC()
	}

	// Direct callers (principally focused tests) enter through the same
	// reservation gate as fsnotify. Reserving before hashing is what makes the
	// first observed timestamp deterministic when a later signal hashes faster.
	fw.mu.Lock()
	if fw.closed || fw.registrations[path] != registrationGeneration {
		fw.mu.Unlock()
		return
	}
	if fw.eventCallbacks[path] != nil {
		if fw.inFlight[path] {
			fw.pendingEvents[path] = mergeEvents(fw.pendingEvents[path], event)
			fw.mu.Unlock()
			return
		}
		fw.inFlight[path] = true
		fw.eventWG.Add(1)
		fw.mu.Unlock()
		fw.runReservedFileEvent(path, registrationGeneration, event)
		return
	}
	fw.mu.Unlock()
	fw.processFileEvent(path, registrationGeneration, event)
}

func (fw *FileWatcher) runReservedFileEvent(path string, registrationGeneration uint64, event Event) {
	defer fw.eventWG.Done()
	fw.processFileEvent(path, registrationGeneration, event)
}

func (fw *FileWatcher) processFileEvent(path string, registrationGeneration uint64, event Event) {
	event.Path = path
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now().UTC()
	}
	newHash, err := fw.hashForPath(path)
	if err != nil {
		if filecopy.IsURL(path) {
			log.Printf("⚠️  Failed to get remote source hash")
			fw.completeEventPreparation(path, registrationGeneration, event, true)
			return
		}
		log.Printf("⚠️  Failed to get hash for %s: %v", path, err)
		fw.completeEventPreparation(path, registrationGeneration, event, true)
		return
	}

	if callback, claimed, skipped := fw.claimEventCallback(path, registrationGeneration, newHash, event); claimed {
		callbackErr := callback(event)
		fw.completeEventCallback(path, registrationGeneration, newHash, event, callbackErr)
		return
	} else if skipped {
		fw.completeEventPreparation(path, registrationGeneration, event, false)
		return
	}
	callback, claimed := fw.claimCallback(path, registrationGeneration, newHash)
	if !claimed {
		return
	}
	callback(path)
}

func (fw *FileWatcher) claimEventCallback(path string, registrationGeneration uint64, newHash string, event Event) (func(Event) error, bool, bool) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.closed || fw.registrations[path] != registrationGeneration {
		return nil, false, false
	}
	callback := fw.eventCallbacks[path]
	if callback == nil {
		return nil, false, false
	}
	if !fw.inFlight[path] {
		return nil, false, false
	}
	if event.Reason == EventChange && !event.Retry && fw.fileHashes[path] == newHash {
		return nil, false, true
	}
	return callback, true, false
}

func (fw *FileWatcher) completeEventCallback(path string, registrationGeneration uint64, newHash string, event Event, callbackErr error) {
	var next Event
	startNext := false
	fw.mu.Lock()
	if fw.registrations[path] != registrationGeneration {
		fw.mu.Unlock()
		return
	}
	if callbackErr == nil {
		fw.fileHashes[path] = newHash
	} else {
		log.Printf("⚠️  Source event was not acknowledged for %s: %v", filepath.Base(path), callbackErr)
	}
	pending, hasPending := fw.pendingEvents[path]
	delete(fw.pendingEvents, path)
	if callbackErr != nil {
		if hasPending {
			event = mergeEvents(event, pending)
		}
		fw.inFlight[path] = false
		fw.scheduleEventRetryLocked(event, registrationGeneration)
		fw.mu.Unlock()
		return
	}
	fw.inFlight[path] = false
	if hasPending {
		fw.inFlight[path] = true
		fw.eventWG.Add(1)
		next = pending
		startNext = true
	}
	fw.mu.Unlock()
	if startNext {
		go fw.runReservedFileEvent(path, registrationGeneration, next)
	}
}

// completeEventPreparation releases the reservation held while hashing. A
// failed preparation retries the entire unacknowledged observation batch; a
// same-hash observation is dropped unless a newer signal arrived meanwhile.
func (fw *FileWatcher) completeEventPreparation(path string, registrationGeneration uint64, event Event, retry bool) {
	var next Event
	startNext := false
	fw.mu.Lock()
	if fw.registrations[path] != registrationGeneration {
		fw.mu.Unlock()
		return
	}
	pending, hasPending := fw.pendingEvents[path]
	delete(fw.pendingEvents, path)
	if retry {
		if hasPending {
			event = mergeEvents(event, pending)
		}
		fw.inFlight[path] = false
		fw.scheduleEventRetryLocked(event, registrationGeneration)
		fw.mu.Unlock()
		return
	}
	fw.inFlight[path] = false
	if hasPending {
		fw.inFlight[path] = true
		fw.eventWG.Add(1)
		next = mergeEvents(event, pending)
		startNext = true
	}
	fw.mu.Unlock()
	if startNext {
		go fw.runReservedFileEvent(path, registrationGeneration, next)
	}
}

func (fw *FileWatcher) scheduleEventRetryLocked(event Event, registrationGeneration uint64) {
	event.Retry = true
	delay := fw.retryDelay
	if delay <= 0 {
		delay = time.Second
	}
	if debounce := fw.debounce[event.Path]; debounce > delay {
		delay = debounce
	}
	if scheduled, exists := fw.timers[event.Path]; exists {
		event = mergeEvents(scheduled.event, event)
	}
	fw.stopDebounceTimerLocked(event.Path)
	fw.timerSeq++
	timerGeneration := fw.timerSeq
	timer := time.AfterFunc(delay, func() {
		fw.fireDebounced(event.Path, timerGeneration, registrationGeneration)
	})
	fw.timers[event.Path] = debounceTimer{
		timer:                  timer,
		timerGeneration:        timerGeneration,
		registrationGeneration: registrationGeneration,
		event:                  event,
	}
}

func mergeEvents(current, candidate Event) Event {
	if current.Path == "" {
		return candidate
	}
	current.Reason = strongerReason(current.Reason, candidate.Reason)
	current.Retry = current.Retry || candidate.Retry
	if current.ObservedAt.IsZero() || (!candidate.ObservedAt.IsZero() && candidate.ObservedAt.Before(current.ObservedAt)) {
		current.ObservedAt = candidate.ObservedAt
	}
	return current
}

// WaitCallbacks waits for durable-acknowledgement callbacks already claimed
// before Close. Call Close first so no new callback can be reserved.
func (fw *FileWatcher) WaitCallbacks() {
	fw.eventWG.Wait()
}

func strongerReason(current, candidate EventReason) EventReason {
	if current == EventOverflow || candidate == EventOverflow {
		return EventOverflow
	}
	if current == EventStartup || candidate == EventStartup {
		return EventStartup
	}
	if current == EventCompanion || candidate == EventCompanion {
		return EventCompanion
	}
	return EventChange
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
	return fw.getFileHash(path)
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

// Close cancels future watcher work and pending debounce timers.
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
	clear(fw.callbacks)
	clear(fw.eventCallbacks)
	clear(fw.inFlight)
	clear(fw.pendingEvents)
	clear(fw.fileHashes)
	clear(fw.debounce)
	clear(fw.pathDirs)
	clear(fw.watchedDirs)
	clear(fw.registrations)
	fw.mu.Unlock()

	return fw.watcher.Close()
}

// Unwatch prevents future callbacks from being claimed for path and cancels
// its pending debounce timer. A callback already claimed before the
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

	fw.stopDebounceTimerLocked(key)

	delete(fw.fileHashes, key)
	delete(fw.callbacks, key)
	delete(fw.eventCallbacks, key)
	delete(fw.inFlight, key)
	delete(fw.pendingEvents, key)
	delete(fw.debounce, key)
	delete(fw.registrations, key)
	dir := fw.pathDirs[key]
	delete(fw.pathDirs, key)

	var removeErr error
	if dir != "" {
		fw.watchedDirs[dir]--
		if fw.watchedDirs[dir] <= 0 {
			delete(fw.watchedDirs, dir)
			removeErr = fw.watcher.Remove(dir)
		}
	}

	fw.mu.Unlock()
	return removeErr
}
