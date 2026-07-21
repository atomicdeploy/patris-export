//go:build windows

package processmon

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/shirou/gopsutil/v4/process"
	"golang.org/x/sys/windows"
)

const (
	restartManagerSessionKeyChars = 32
	restartManagerMaxListAttempts = 8
	restartManagerMaxProcesses    = 1 << 16
	restartManagerSuccess         = uintptr(0)
)

var (
	restartManagerDLL               = windows.NewLazySystemDLL("rstrtmgr.dll")
	procRestartManagerStartSession  = restartManagerDLL.NewProc("RmStartSession")
	procRestartManagerRegisterFiles = restartManagerDLL.NewProc("RmRegisterResources")
	procRestartManagerGetList       = restartManagerDLL.NewProc("RmGetList")
	procRestartManagerEndSession    = restartManagerDLL.NewProc("RmEndSession")
	restartManagerScanGate          = make(chan struct{}, 1)
	restartManagerLoadOnce          sync.Once
	restartManagerLoadErr           error
)

type restartManagerUniqueProcess struct {
	ProcessID        uint32
	ProcessStartTime windows.Filetime
}

type restartManagerProcessInfo struct {
	Process          restartManagerUniqueProcess
	AppName          [256]uint16
	ServiceShortName [64]uint16
	ApplicationType  int32
	AppStatus        uint32
	TSSessionID      uint32
	Restartable      int32
}

type restartManagerAPI interface {
	startSession() (uint32, error)
	registerFile(uint32, string) error
	getList(uint32, *uint32, *uint32, []restartManagerProcessInfo, *uint32) uintptr
	endSession(uint32) error
}

type nativeRestartManager struct{}

func (nativeRestartManager) startSession() (uint32, error) {
	if err := ensureRestartManagerAvailable(); err != nil {
		return 0, err
	}

	var session uint32
	sessionKey := make([]uint16, restartManagerSessionKeyChars+1)
	result, _, _ := procRestartManagerStartSession.Call(
		uintptr(unsafe.Pointer(&session)),
		0,
		uintptr(unsafe.Pointer(&sessionKey[0])),
	)
	if err := restartManagerError("start session", result); err != nil {
		return 0, err
	}
	return session, nil
}

func (nativeRestartManager) registerFile(session uint32, filePath string) error {
	pathPtr, err := windows.UTF16PtrFromString(filePath)
	if err != nil {
		return fmt.Errorf("encode file path for Restart Manager: %w", err)
	}
	filePaths := []*uint16{pathPtr}
	result, _, _ := procRestartManagerRegisterFiles.Call(
		uintptr(session),
		uintptr(len(filePaths)),
		uintptr(unsafe.Pointer(&filePaths[0])),
		0,
		0,
		0,
		0,
	)
	runtime.KeepAlive(filePaths)
	return restartManagerError("register file", result)
}

func (nativeRestartManager) getList(
	session uint32,
	needed *uint32,
	count *uint32,
	processes []restartManagerProcessInfo,
	rebootReasons *uint32,
) uintptr {
	var processBuffer uintptr
	if len(processes) > 0 {
		processBuffer = uintptr(unsafe.Pointer(&processes[0]))
	}
	result, _, _ := procRestartManagerGetList.Call(
		uintptr(session),
		uintptr(unsafe.Pointer(needed)),
		uintptr(unsafe.Pointer(count)),
		processBuffer,
		uintptr(unsafe.Pointer(rebootReasons)),
	)
	return result
}

func (nativeRestartManager) endSession(session uint32) error {
	result, _, _ := procRestartManagerEndSession.Call(uintptr(session))
	return restartManagerError("end session", result)
}

func findFileProcesses(ctx context.Context, filePath string) ([]fileProcessMatch, error) {
	if err := acquireRestartManagerScan(ctx); err != nil {
		return nil, err
	}
	defer releaseRestartManagerScan()

	return findFileProcessesWithManager(ctx, filePath, nativeRestartManager{})
}

func findFileProcessesWithManager(
	ctx context.Context,
	filePath string,
	manager restartManagerAPI,
) (matches []fileProcessMatch, returnErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	session, err := manager.startSession()
	if err != nil {
		return nil, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, manager.endSession(session), ctx.Err())
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := manager.registerFile(session, filePath); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	processes, err := restartManagerProcesses(ctx, manager, session)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	matches = make([]fileProcessMatch, 0, len(processes))
	seen := make(map[uint32]struct{}, len(processes))
	for _, processInfo := range processes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid := processInfo.Process.ProcessID
		if pid == 0 || pid > uint32(^uint32(0)>>1) {
			continue
		}
		if _, exists := seen[pid]; exists {
			continue
		}
		seen[pid] = struct{}{}

		name := windows.UTF16ToString(processInfo.AppName[:])
		if name == "" {
			name = windows.UTF16ToString(processInfo.ServiceShortName[:])
		}
		matches = append(matches, fileProcessMatch{
			PID:        int32(pid),
			Name:       name,
			OpenFiles:  []string{filePath},
			CreateTime: restartManagerCreateTimeMillis(processInfo.Process.ProcessStartTime),
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return matches, nil
}

func restartManagerProcesses(
	ctx context.Context,
	manager restartManagerAPI,
	session uint32,
) ([]restartManagerProcessInfo, error) {
	var needed uint32
	var count uint32
	var rebootReasons uint32

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := manager.getList(session, &needed, &count, nil, &rebootReasons)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if result == restartManagerSuccess {
		if needed == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("Restart Manager reported %d processes without requesting a buffer", needed)
	}
	if syscall.Errno(result) != windows.ERROR_MORE_DATA {
		return nil, restartManagerError("query process count", result)
	}

	for attempt := 0; attempt < restartManagerMaxListAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if needed == 0 {
			return nil, nil
		}
		if needed > restartManagerMaxProcesses {
			return nil, fmt.Errorf("Restart Manager requested an unreasonable %d-process buffer", needed)
		}

		processes := make([]restartManagerProcessInfo, needed)
		count = uint32(len(processes))
		result = manager.getList(session, &needed, &count, processes, &rebootReasons)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if syscall.Errno(result) == windows.ERROR_MORE_DATA {
			currentSize := uint32(len(processes))
			if needed <= currentSize {
				if currentSize >= restartManagerMaxProcesses {
					return nil, fmt.Errorf("Restart Manager process list exceeds the %d-process safety limit", restartManagerMaxProcesses)
				}
				needed = currentSize * 2
				if needed > restartManagerMaxProcesses {
					needed = restartManagerMaxProcesses
				}
			}
			continue
		}
		if err := restartManagerError("query process list", result); err != nil {
			return nil, err
		}
		if count > uint32(len(processes)) {
			return nil, fmt.Errorf("Restart Manager returned %d processes into a %d-entry buffer", count, len(processes))
		}
		return processes[:count], nil
	}

	return nil, fmt.Errorf("Restart Manager process list changed during %d retries", restartManagerMaxListAttempts)
}

func acquireRestartManagerScan(ctx context.Context) error {
	select {
	case restartManagerScanGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseRestartManagerScan() {
	<-restartManagerScanGate
}

func restartManagerCreateTimeMillis(fileTime windows.Filetime) int64 {
	if fileTime.LowDateTime == 0 && fileTime.HighDateTime == 0 {
		return 0
	}
	return fileTime.Nanoseconds() / 1_000_000
}

func ensureRestartManagerAvailable() error {
	restartManagerLoadOnce.Do(func() {
		procedures := []struct {
			name string
			proc *windows.LazyProc
		}{
			{"RmStartSession", procRestartManagerStartSession},
			{"RmRegisterResources", procRestartManagerRegisterFiles},
			{"RmGetList", procRestartManagerGetList},
			{"RmEndSession", procRestartManagerEndSession},
		}
		for _, procedure := range procedures {
			if err := procedure.proc.Find(); err != nil {
				restartManagerLoadErr = fmt.Errorf("load Restart Manager procedure %s: %w", procedure.name, err)
				return
			}
		}
	})
	return restartManagerLoadErr
}

func restartManagerError(operation string, result uintptr) error {
	if result == restartManagerSuccess {
		return nil
	}
	return fmt.Errorf("Restart Manager %s: %w", operation, syscall.Errno(result))
}

// gopsutil's Windows OpenFiles implementation snapshots the complete system
// handle table for every process. Listing every file for one process is
// intentionally disabled here; targeted file queries use Restart Manager.
func openFilesForProcess(context.Context, *process.Process) ([]string, error) {
	return nil, nil
}
