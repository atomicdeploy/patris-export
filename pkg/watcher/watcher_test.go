package watcher

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func registeredGeneration(t *testing.T, fw *FileWatcher, path string) uint64 {
	t.Helper()
	absPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("Failed to resolve test path: %v", err)
	}
	fw.mu.RLock()
	generation := fw.registrations[absPath]
	fw.mu.RUnlock()
	if generation == 0 {
		t.Fatalf("Path has no registration generation: %s", absPath)
	}
	return generation
}

func TestFileWatcher_DebounceZero(t *testing.T) {
	// Create a temporary file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(tmpFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create watcher
	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}
	defer fw.Close()

	// Track callback invocations
	var mu sync.Mutex
	callCount := 0

	// Watch with 0 debounce
	err = fw.Watch(tmpFile, func(path string) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
	}, 0)
	if err != nil {
		t.Fatalf("Failed to watch file: %v", err)
	}

	fw.Start()

	// Wait for watcher to start
	time.Sleep(100 * time.Millisecond)

	// Make multiple rapid changes
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(tmpFile, []byte("change "+strconv.Itoa(i)), 0644); err != nil {
			t.Fatalf("Failed to write to file: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for all callbacks to complete (no debounce means they should all fire)
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	finalCallCount := callCount
	mu.Unlock()

	// With 0 debounce, all changes should trigger callbacks
	if finalCallCount == 0 {
		t.Errorf("Expected at least one callback, got %d", finalCallCount)
	}
}

func TestFileWatcher_DebounceOneSecond(t *testing.T) {
	// Create a temporary file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(tmpFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create watcher
	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}
	defer fw.Close()

	// Track callback invocations
	var mu sync.Mutex
	callCount := 0
	var callTimes []time.Time

	// Watch with 1 second debounce
	err = fw.Watch(tmpFile, func(path string) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
		callTimes = append(callTimes, time.Now())
	}, 1*time.Second)
	if err != nil {
		t.Fatalf("Failed to watch file: %v", err)
	}

	fw.Start()

	// Wait for watcher to start
	time.Sleep(100 * time.Millisecond)

	// Make multiple rapid changes
	startTime := time.Now()
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(tmpFile, []byte("change "+strconv.Itoa(i)), 0644); err != nil {
			t.Fatalf("Failed to write to file: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Wait for debounced callback to fire
	time.Sleep(1500 * time.Millisecond)

	mu.Lock()
	finalCallCount := callCount
	totalTime := time.Since(startTime)
	mu.Unlock()

	// With 1 second debounce, multiple rapid changes should result in only 1 callback
	if finalCallCount != 1 {
		t.Errorf("Expected 1 debounced callback, got %d", finalCallCount)
	}

	// The callback should have been delayed by at least the debounce duration
	if totalTime < 1*time.Second {
		t.Errorf("Expected debounced callback after at least 1 second, got %v", totalTime)
	}
}

func TestFileWatcher_MultipleFiles(t *testing.T) {
	// Create temporary files
	tmpDir := t.TempDir()
	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.txt")

	if err := os.WriteFile(file1, []byte("initial1"), 0644); err != nil {
		t.Fatalf("Failed to create test file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("initial2"), 0644); err != nil {
		t.Fatalf("Failed to create test file2: %v", err)
	}

	// Create watcher
	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}
	defer fw.Close()

	// Track callbacks for each file
	var mu sync.Mutex
	file1Calls := 0
	file2Calls := 0

	// Watch file1 with 0 debounce
	err = fw.Watch(file1, func(path string) {
		mu.Lock()
		defer mu.Unlock()
		file1Calls++
	}, 0)
	if err != nil {
		t.Fatalf("Failed to watch file1: %v", err)
	}

	// Watch file2 with 500ms debounce
	err = fw.Watch(file2, func(path string) {
		mu.Lock()
		defer mu.Unlock()
		file2Calls++
	}, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to watch file2: %v", err)
	}

	fw.Start()

	// Wait for watcher to start
	time.Sleep(100 * time.Millisecond)

	// Change both files
	if err := os.WriteFile(file1, []byte("change1"), 0644); err != nil {
		t.Fatalf("Failed to write to file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("change2"), 0644); err != nil {
		t.Fatalf("Failed to write to file2: %v", err)
	}

	// Wait for callbacks
	time.Sleep(800 * time.Millisecond)

	mu.Lock()
	f1Calls := file1Calls
	f2Calls := file2Calls
	mu.Unlock()

	// Both files should have triggered callbacks
	if f1Calls == 0 {
		t.Errorf("Expected file1 callback, got %d", f1Calls)
	}
	if f2Calls == 0 {
		t.Errorf("Expected file2 callback, got %d", f2Calls)
	}
}

func TestFileWatcher_Unwatch(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "target.txt")
	sentinel := filepath.Join(tmpDir, "sentinel.txt")
	if err := os.WriteFile(target, []byte("initial target"), 0644); err != nil {
		t.Fatalf("Failed to create target file: %v", err)
	}
	if err := os.WriteFile(sentinel, []byte("initial sentinel"), 0644); err != nil {
		t.Fatalf("Failed to create sentinel file: %v", err)
	}

	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}
	defer fw.Close()

	targetEvents := make(chan struct{}, 16)
	sentinelEvents := make(chan struct{}, 16)
	if err := fw.Watch(target, func(string) { targetEvents <- struct{}{} }, 0); err != nil {
		t.Fatalf("Failed to watch target: %v", err)
	}
	if err := fw.Watch(sentinel, func(string) { sentinelEvents <- struct{}{} }, 0); err != nil {
		t.Fatalf("Failed to watch sentinel: %v", err)
	}
	fw.Start()

	if err := os.WriteFile(target, []byte("first target change"), 0644); err != nil {
		t.Fatalf("Failed to write target: %v", err)
	}
	select {
	case <-targetEvents:
	case <-time.After(2 * time.Second):
		t.Fatal("Expected target callback before Unwatch")
	}

	if err := fw.Unwatch(target); err != nil {
		t.Fatalf("Failed to unwatch target: %v", err)
	}
	for {
		select {
		case <-targetEvents:
			continue
		default:
			goto targetDrained
		}
	}

targetDrained:
	if err := os.WriteFile(target, []byte("second target change"), 0644); err != nil {
		t.Fatalf("Failed to rewrite unwatched target: %v", err)
	}
	if err := os.WriteFile(sentinel, []byte("sentinel fence"), 0644); err != nil {
		t.Fatalf("Failed to write sentinel: %v", err)
	}
	select {
	case <-sentinelEvents:
	case <-time.After(2 * time.Second):
		t.Fatal("Sentinel callback did not run; sibling directory watch was lost")
	}
	select {
	case <-targetEvents:
		t.Fatal("Target callback ran after Unwatch and sentinel processing")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestFileWatcher_ClaimsConcurrentHashOnce(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}
	defer fw.Close()

	var calls atomic.Int32
	if err := fw.Watch(tmpFile, func(string) { calls.Add(1) }, 0); err != nil {
		t.Fatalf("Failed to watch file: %v", err)
	}
	absPath, err := filepath.Abs(tmpFile)
	if err != nil {
		t.Fatalf("Failed to resolve test path: %v", err)
	}
	generation := registeredGeneration(t, fw, tmpFile)

	const workers = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			<-start
			callback, claimed := fw.claimCallback(absPath, generation, "same-new-hash")
			if !claimed {
				return
			}
			callback(absPath)
		}()
	}
	close(start)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("Expected one callback claim for a shared hash, got %d", got)
	}
}

func TestFileWatcher_UnwatchAllowsClaimedCallbackToFinish(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}
	defer fw.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	var releaseOnce sync.Once
	releaseCallback := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseCallback)
	if err := fw.Watch(tmpFile, func(string) {
		close(started)
		<-release
	}, 0); err != nil {
		t.Fatalf("Failed to watch file: %v", err)
	}
	absPath, err := filepath.Abs(tmpFile)
	if err != nil {
		t.Fatalf("Failed to resolve test path: %v", err)
	}
	generation := registeredGeneration(t, fw, tmpFile)
	callback, claimed := fw.claimCallback(absPath, generation, "claimed-new-hash")
	if !claimed {
		t.Fatal("Expected callback claim")
	}
	go func() {
		defer close(finished)
		callback(absPath)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("Claimed callback did not start")
	}

	unwatched := make(chan error, 1)
	go func() { unwatched <- fw.Unwatch(tmpFile) }()
	select {
	case err := <-unwatched:
		if err != nil {
			t.Fatalf("Unwatch failed while callback was running: %v", err)
		}
	case <-time.After(2 * time.Second):
		releaseCallback()
		t.Fatal("Unwatch blocked on a callback that was already claimed")
	}

	if _, claimed := fw.claimCallback(absPath, generation, "future-hash"); claimed {
		t.Fatal("Unwatch allowed a future callback claim")
	}

	releaseCallback()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Claimed callback did not finish after release")
	}
}

func TestFileWatcher_CallbackCanUnwatchItself(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}

	unwatched := make(chan error, 1)
	if err := fw.Watch(tmpFile, func(string) {
		unwatched <- fw.Unwatch(tmpFile)
	}, 0); err != nil {
		t.Fatalf("Failed to watch file: %v", err)
	}
	absPath, err := filepath.Abs(tmpFile)
	if err != nil {
		t.Fatalf("Failed to resolve test path: %v", err)
	}
	generation := registeredGeneration(t, fw, tmpFile)

	callback, claimed := fw.claimCallback(absPath, generation, "self-unwatch-hash")
	if !claimed {
		t.Fatal("Expected callback claim")
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		callback(absPath)
	}()

	select {
	case callbackErr := <-unwatched:
		if callbackErr != nil {
			t.Fatalf("Self-unwatch failed: %v", callbackErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Callback deadlocked while unwatching itself")
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Self-unwatching callback did not finish")
	}
	if _, claimed := fw.claimCallback(absPath, generation, "future-hash"); claimed {
		_ = fw.Close()
		t.Fatal("Self-unwatch left the callback claimable")
	}
	closeErr := fw.Close()
	if closeErr != nil {
		t.Fatalf("Close after self-unwatch failed: %v", closeErr)
	}
}

func TestFileWatcher_CallbackCanCloseWatcher(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}

	closedFromCallback := make(chan error, 1)
	if err := fw.Watch(tmpFile, func(string) {
		closedFromCallback <- fw.Close()
	}, 0); err != nil {
		t.Fatalf("Failed to watch file: %v", err)
	}
	absPath, err := filepath.Abs(tmpFile)
	if err != nil {
		t.Fatalf("Failed to resolve test path: %v", err)
	}
	generation := registeredGeneration(t, fw, tmpFile)
	callback, claimed := fw.claimCallback(absPath, generation, "close-from-callback")
	if !claimed {
		t.Fatal("Expected callback claim")
	}
	go callback(absPath)
	select {
	case err := <-closedFromCallback:
		if err != nil {
			t.Fatalf("Close from callback failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Callback deadlocked while closing its watcher")
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("Repeated Close after callback returned a different error: %v", err)
	}
}

func TestFileWatcher_CloseCancelsPendingDebounce(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}
	var calls atomic.Int32
	if err := fw.Watch(tmpFile, func(string) { calls.Add(1) }, time.Hour); err != nil {
		t.Fatalf("Failed to watch file: %v", err)
	}
	absPath, err := filepath.Abs(tmpFile)
	if err != nil {
		t.Fatalf("Failed to resolve test path: %v", err)
	}
	fw.queueFileChange(absPath)

	fw.mu.RLock()
	timerCount := len(fw.timers)
	fw.mu.RUnlock()
	if timerCount != 1 {
		t.Fatalf("Expected one pending debounce timer, got %d", timerCount)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("Close failed with pending debounce timer: %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("Pending debounce callback ran during Close: %d", got)
	}
	fw.mu.RLock()
	timerCount = len(fw.timers)
	fw.mu.RUnlock()
	if timerCount != 0 {
		t.Fatalf("Close left pending debounce timers behind: %d", timerCount)
	}
}

func TestFileWatcher_ConcurrentCloseIsIdempotent(t *testing.T) {
	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}

	const closers = 16
	start := make(chan struct{})
	errorsSeen := make(chan error, closers)
	var wg sync.WaitGroup
	wg.Add(closers)
	for range closers {
		go func() {
			defer wg.Done()
			<-start
			errorsSeen <- fw.Close()
		}()
	}
	close(start)
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("Concurrent Close failed: %v", err)
		}
	}
}

func TestFileWatcher_ClosedWatcherRejectsNewWork(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if err := fw.Watch(tmpFile, func(string) {}, 0); !errors.Is(err, ErrClosed) {
		t.Fatalf("Watch after Close error = %v, want %v", err, ErrClosed)
	}
	if err := fw.Poll(tmpFile, func(string) {}, time.Second); !errors.Is(err, ErrClosed) {
		t.Fatalf("Poll after Close error = %v, want %v", err, ErrClosed)
	}
	if err := fw.Unwatch(tmpFile); err != nil {
		t.Fatalf("Unwatch after Close should be idempotent, got %v", err)
	}
	fw.Start()
}

func TestFileWatcher_DuplicateRegistrationRequiresUnwatch(t *testing.T) {
	tests := []struct {
		name   string
		first  string
		second string
	}{
		{name: "watch then watch", first: "watch", second: "watch"},
		{name: "poll then poll", first: "poll", second: "poll"},
		{name: "watch then poll", first: "watch", second: "poll"},
		{name: "poll then watch", first: "poll", second: "watch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := filepath.Join(t.TempDir(), "test.txt")
			if err := os.WriteFile(tmpFile, []byte("initial"), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}
			fw, err := NewFileWatcher()
			if err != nil {
				t.Fatalf("Failed to create file watcher: %v", err)
			}
			defer fw.Close()

			register := func(kind string) error {
				if kind == "poll" {
					return fw.Poll(tmpFile, func(string) {}, time.Hour)
				}
				return fw.Watch(tmpFile, func(string) {}, 0)
			}
			if err := register(tt.first); err != nil {
				t.Fatalf("Initial %s registration failed: %v", tt.first, err)
			}
			if err := register(tt.second); !errors.Is(err, ErrAlreadyRegistered) {
				t.Fatalf("Duplicate %s registration error = %v, want %v", tt.second, err, ErrAlreadyRegistered)
			}
			if err := fw.Unwatch(tmpFile); err != nil {
				t.Fatalf("Unwatch before re-registration failed: %v", err)
			}
			if err := register(tt.second); err != nil {
				t.Fatalf("%s registration after Unwatch failed: %v", tt.second, err)
			}
		})
	}
}

func TestFileWatcher_ConcurrentMixedRegistrationHasSingleWinner(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}
	defer fw.Close()

	const workers = 32
	start := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := range workers {
		go func() {
			defer wg.Done()
			<-start
			if worker%2 == 0 {
				results <- fw.Watch(tmpFile, func(string) {}, 0)
				return
			}
			results <- fw.Poll(tmpFile, func(string) {}, time.Hour)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	duplicates := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAlreadyRegistered):
			duplicates++
		default:
			t.Fatalf("Unexpected concurrent registration error: %v", err)
		}
	}
	if successes != 1 || duplicates != workers-1 {
		t.Fatalf("Concurrent registration results: successes=%d duplicates=%d", successes, duplicates)
	}
}

func TestFileWatcher_StaleWorkCannotClaimReregisteredCallback(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}
	defer fw.Close()

	var oldCalls atomic.Int32
	if err := fw.Watch(tmpFile, func(string) { oldCalls.Add(1) }, 0); err != nil {
		t.Fatalf("Initial Watch failed: %v", err)
	}
	absPath, err := filepath.Abs(tmpFile)
	if err != nil {
		t.Fatalf("Failed to resolve test path: %v", err)
	}
	oldGeneration := registeredGeneration(t, fw, tmpFile)
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWork := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseWork)
	fw.hashForPath = func(string) (string, error) {
		close(started)
		<-release
		return "stale-work-hash", nil
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		fw.handleFileChange(absPath, oldGeneration)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("Old hash work did not start")
	}

	if err := fw.Unwatch(tmpFile); err != nil {
		t.Fatalf("Unwatch while old work was blocked failed: %v", err)
	}
	var newCalls atomic.Int32
	if err := fw.Watch(tmpFile, func(string) { newCalls.Add(1) }, 0); err != nil {
		t.Fatalf("Re-registering path failed: %v", err)
	}
	newGeneration := registeredGeneration(t, fw, tmpFile)
	if newGeneration == oldGeneration {
		t.Fatal("Re-registration reused the old generation")
	}

	releaseWork()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Old hash work did not finish after release")
	}
	if got := oldCalls.Load(); got != 0 {
		t.Fatalf("Old callback ran after Unwatch: %d", got)
	}
	if got := newCalls.Load(); got != 0 {
		t.Fatalf("Stale work claimed the re-registered callback: %d", got)
	}
	callback, claimed := fw.claimCallback(absPath, newGeneration, "new-work-hash")
	if !claimed {
		t.Fatal("Current registration could not claim its callback")
	}
	callback(absPath)
	if got := newCalls.Load(); got != 1 {
		t.Fatalf("Current registration callback count = %d, want 1", got)
	}
}

func TestFileWatcher_ConcurrentDebounceReplacement(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}
	if err := fw.Watch(tmpFile, func(string) {}, time.Hour); err != nil {
		t.Fatalf("Failed to watch file: %v", err)
	}
	absPath, err := filepath.Abs(tmpFile)
	if err != nil {
		t.Fatalf("Failed to resolve test path: %v", err)
	}

	const workers = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			<-start
			fw.queueFileChange(absPath)
		}()
	}
	close(start)
	wg.Wait()
	if err := fw.Close(); err != nil {
		t.Fatalf("Close after concurrent debounce replacement failed: %v", err)
	}
}

func TestFileWatcher_PollURL(t *testing.T) {
	var mu sync.Mutex
	content := "initial"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Write([]byte(content))
	}))
	defer server.Close()

	fw, err := NewFileWatcher()
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}
	defer fw.Close()

	changed := make(chan string, 1)
	if err := fw.Poll(server.URL+"/kala.db", func(path string) {
		changed <- path
	}, 50*time.Millisecond); err != nil {
		t.Fatalf("Failed to poll URL: %v", err)
	}

	mu.Lock()
	content = "changed content"
	mu.Unlock()

	select {
	case got := <-changed:
		if got != server.URL+"/kala.db" {
			t.Fatalf("unexpected callback path: %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected polling callback after URL content changed")
	}
}
