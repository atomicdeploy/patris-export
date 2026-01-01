package watcher

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

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
	var lastCallTime time.Time

	// Watch with 0 debounce
	err = fw.Watch(tmpFile, func(path string) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
		lastCallTime = time.Now()
	}, 0)
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
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for all callbacks to complete (no debounce means they should all fire)
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	finalCallCount := callCount
	timeSinceStart := lastCallTime.Sub(startTime)
	mu.Unlock()

	// With 0 debounce, all changes should trigger callbacks
	if finalCallCount == 0 {
		t.Errorf("Expected at least one callback, got %d", finalCallCount)
	}

	// The last callback should have happened relatively quickly (not delayed by debounce)
	if timeSinceStart > 500*time.Millisecond {
		t.Errorf("With 0 debounce, callbacks should be immediate, but took %v", timeSinceStart)
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

	// Watch file
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

	// Make a change
	if err := os.WriteFile(tmpFile, []byte("change1"), 0644); err != nil {
		t.Fatalf("Failed to write to file: %v", err)
	}

	// Wait for callback
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	callsBeforeUnwatch := callCount
	mu.Unlock()

	if callsBeforeUnwatch == 0 {
		t.Fatal("Expected callback before unwatch")
	}

	// Unwatch the file
	if err := fw.Unwatch(tmpFile); err != nil {
		t.Fatalf("Failed to unwatch file: %v", err)
	}

	// Make another change
	if err := os.WriteFile(tmpFile, []byte("change2"), 0644); err != nil {
		t.Fatalf("Failed to write to file: %v", err)
	}

	// Wait to ensure no callback fires
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	callsAfterUnwatch := callCount
	mu.Unlock()

	// Call count should not have increased after unwatch
	if callsAfterUnwatch != callsBeforeUnwatch {
		t.Errorf("Expected no callbacks after unwatch, but got %d total calls (was %d before unwatch)", callsAfterUnwatch, callsBeforeUnwatch)
	}
}
