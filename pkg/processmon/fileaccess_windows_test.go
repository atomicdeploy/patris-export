//go:build windows

package processmon

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/shirou/gopsutil/v4/process"
	"golang.org/x/sys/windows"
)

func TestRestartManagerProcessInfoLayout(t *testing.T) {
	var info restartManagerProcessInfo
	checks := map[string]struct {
		got  uintptr
		want uintptr
	}{
		"size":               {unsafe.Sizeof(info), 668},
		"process":            {unsafe.Offsetof(info.Process), 0},
		"app name":           {unsafe.Offsetof(info.AppName), 12},
		"service short name": {unsafe.Offsetof(info.ServiceShortName), 524},
		"application type":   {unsafe.Offsetof(info.ApplicationType), 652},
		"application status": {unsafe.Offsetof(info.AppStatus), 656},
		"terminal session":   {unsafe.Offsetof(info.TSSessionID), 660},
		"restartable":        {unsafe.Offsetof(info.Restartable), 664},
	}
	for field, check := range checks {
		if check.got != check.want {
			t.Errorf("restartManagerProcessInfo %s = %d, want %d", field, check.got, check.want)
		}
	}
}

func TestRestartManagerProcessesRetriesGrowingList(t *testing.T) {
	manager := &fakeRestartManager{}
	manager.getListFunc = func(_ uint32, needed, count *uint32, processes []restartManagerProcessInfo, _ *uint32) uintptr {
		switch manager.getListCalls {
		case 1:
			*needed = 1
			return uintptr(windows.ERROR_MORE_DATA)
		case 2:
			*needed = 2
			return uintptr(windows.ERROR_MORE_DATA)
		default:
			processes[0].Process.ProcessID = 101
			processes[1].Process.ProcessID = 202
			*count = 2
			return restartManagerSuccess
		}
	}

	processes, err := restartManagerProcesses(context.Background(), manager, 42)
	if err != nil {
		t.Fatalf("restartManagerProcesses: %v", err)
	}
	if len(processes) != 2 || processes[0].Process.ProcessID != 101 || processes[1].Process.ProcessID != 202 {
		t.Fatalf("unexpected process list: %+v", processes)
	}
	if manager.getListCalls != 3 {
		t.Fatalf("getList calls = %d, want 3", manager.getListCalls)
	}
}

func TestRestartManagerProcessesRejectsUnexpectedSuccess(t *testing.T) {
	manager := &fakeRestartManager{
		getListFunc: func(_ uint32, needed, _ *uint32, _ []restartManagerProcessInfo, _ *uint32) uintptr {
			*needed = 1
			return restartManagerSuccess
		},
	}

	_, err := restartManagerProcesses(context.Background(), manager, 42)
	if err == nil || !strings.Contains(err.Error(), "without requesting a buffer") {
		t.Fatalf("error = %v, want unexpected-success error", err)
	}
}

func TestRestartManagerProcessesCapsAllocation(t *testing.T) {
	manager := &fakeRestartManager{
		getListFunc: func(_ uint32, needed, _ *uint32, _ []restartManagerProcessInfo, _ *uint32) uintptr {
			*needed = restartManagerMaxProcesses + 1
			return uintptr(windows.ERROR_MORE_DATA)
		},
	}

	_, err := restartManagerProcesses(context.Background(), manager, 42)
	if err == nil || !strings.Contains(err.Error(), "unreasonable") {
		t.Fatalf("error = %v, want allocation-cap error", err)
	}
	if manager.getListCalls != 1 {
		t.Fatalf("getList calls = %d, want 1", manager.getListCalls)
	}
}

func TestRestartManagerProcessesHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	manager := &fakeRestartManager{
		getListFunc: func(_ uint32, needed, _ *uint32, _ []restartManagerProcessInfo, _ *uint32) uintptr {
			*needed = 1
			cancel()
			return uintptr(windows.ERROR_MORE_DATA)
		},
	}

	_, err := restartManagerProcesses(ctx, manager, 42)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestRestartManagerProcessesRejectsSuccessAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	manager := &fakeRestartManager{
		getListFunc: func(_ uint32, _ *uint32, _ *uint32, _ []restartManagerProcessInfo, _ *uint32) uintptr {
			cancel()
			return restartManagerSuccess
		},
	}

	_, err := restartManagerProcesses(ctx, manager, 42)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestFindFileProcessesWithManagerDeduplicatesAndEndsSession(t *testing.T) {
	const wantCreateTime = int64(1_700_000_000_123)
	processInfo := restartManagerProcessInfo{
		Process: restartManagerUniqueProcess{
			ProcessID:        123,
			ProcessStartTime: windows.NsecToFiletime(wantCreateTime * 1_000_000),
		},
	}
	copy(processInfo.AppName[:], windows.StringToUTF16("Example App"))

	manager := &fakeRestartManager{}
	manager.getListFunc = scriptedRestartManagerList(processInfo, processInfo)

	matches, err := findFileProcessesWithManager(context.Background(), `C:\data\kala.db`, manager)
	if err != nil {
		t.Fatalf("findFileProcessesWithManager: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %+v, want one deduplicated match", matches)
	}
	match := matches[0]
	if match.PID != 123 || match.Name != "Example App" || match.CreateTime != wantCreateTime {
		t.Fatalf("unexpected match: %+v", match)
	}
	if len(match.OpenFiles) != 1 || match.OpenFiles[0] != `C:\data\kala.db` {
		t.Fatalf("open files = %v", match.OpenFiles)
	}
	if manager.endSessionCalls != 1 {
		t.Fatalf("endSession calls = %d, want 1", manager.endSessionCalls)
	}
}

func TestFindFileProcessesWithManagerJoinsCleanupError(t *testing.T) {
	registerErr := errors.New("register failed")
	endErr := errors.New("end failed")
	manager := &fakeRestartManager{registerErr: registerErr, endErr: endErr}

	_, err := findFileProcessesWithManager(context.Background(), `C:\data\kala.db`, manager)
	if !errors.Is(err, registerErr) || !errors.Is(err, endErr) {
		t.Fatalf("error = %v, want both register and end errors", err)
	}
	if manager.endSessionCalls != 1 {
		t.Fatalf("endSession calls = %d, want 1", manager.endSessionCalls)
	}
}

// This test measures operating-system process resources and is intentionally
// opt-in because unrelated system activity can make the deltas noisy.
func TestRestartManagerFileScanIsBoundedIntegration(t *testing.T) {
	if os.Getenv("PATRIS_PROCESSMON_INTEGRATION") != "1" {
		t.Skip("set PATRIS_PROCESSMON_INTEGRATION=1 to run live resource checks")
	}

	file, err := os.CreateTemp("", "processmon_restart_manager_*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()

	scan := func(iteration int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		info, scanErr := FindProcessesWithFileContext(ctx, file.Name())
		cancel()
		if scanErr != nil {
			t.Fatalf("scan %d: %v", iteration, scanErr)
		}
		for _, processInfo := range info.Processes {
			if processInfo.PID == int32(os.Getpid()) {
				return
			}
		}
		t.Fatalf("scan %d did not report the current process holding %s", iteration, file.Name())
	}

	// Load rstrtmgr.dll and let its one-time process resources settle before
	// measuring steady-state scans.
	scan(0)
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	current, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		t.Fatalf("open current process: %v", err)
	}
	beforeHandles, err := current.NumFDs()
	if err != nil {
		t.Fatalf("read initial handle count: %v", err)
	}
	beforeGoroutines := runtime.NumGoroutine()
	started := time.Now()

	for i := 1; i <= 10; i++ {
		scan(i)
	}
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("ten Restart Manager scans took %s", elapsed)
	}
	afterHandles, err := current.NumFDs()
	if err != nil {
		t.Fatalf("read final handle count: %v", err)
	}
	if delta := afterHandles - beforeHandles; delta > 16 {
		t.Fatalf("Restart Manager scans leaked %d process handles", delta)
	}
	if delta := runtime.NumGoroutine() - beforeGoroutines; delta > 2 {
		t.Fatalf("Restart Manager scans leaked %d goroutines", delta)
	}
}

type fakeRestartManager struct {
	startErr        error
	registerErr     error
	endErr          error
	getListFunc     func(uint32, *uint32, *uint32, []restartManagerProcessInfo, *uint32) uintptr
	getListCalls    int
	endSessionCalls int
}

func (manager *fakeRestartManager) startSession() (uint32, error) {
	return 42, manager.startErr
}

func (manager *fakeRestartManager) registerFile(uint32, string) error {
	return manager.registerErr
}

func (manager *fakeRestartManager) getList(
	session uint32,
	needed *uint32,
	count *uint32,
	processes []restartManagerProcessInfo,
	rebootReasons *uint32,
) uintptr {
	manager.getListCalls++
	if manager.getListFunc == nil {
		return restartManagerSuccess
	}
	return manager.getListFunc(session, needed, count, processes, rebootReasons)
}

func (manager *fakeRestartManager) endSession(uint32) error {
	manager.endSessionCalls++
	return manager.endErr
}

func scriptedRestartManagerList(processes ...restartManagerProcessInfo) func(
	uint32,
	*uint32,
	*uint32,
	[]restartManagerProcessInfo,
	*uint32,
) uintptr {
	return func(_ uint32, needed, count *uint32, buffer []restartManagerProcessInfo, _ *uint32) uintptr {
		if buffer == nil {
			*needed = uint32(len(processes))
			if len(processes) == 0 {
				return restartManagerSuccess
			}
			return uintptr(windows.ERROR_MORE_DATA)
		}
		copy(buffer, processes)
		*count = uint32(len(processes))
		return restartManagerSuccess
	}
}
