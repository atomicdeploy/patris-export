package processmon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// ProcessInfo contains information about a running process
type ProcessInfo struct {
	PID         int32    `json:"pid"`
	Name        string   `json:"name"`
	Exe         string   `json:"exe"`
	Cmdline     string   `json:"cmdline"`
	OpenFiles   []string `json:"open_files,omitempty"`
	CreateTime  int64    `json:"create_time"`
	MemoryUsage uint64   `json:"memory_usage,omitempty"`
}

// FileAccessInfo contains information about processes accessing a file
type FileAccessInfo struct {
	FilePath  string        `json:"file_path"`
	Processes []ProcessInfo `json:"processes"`
}

const (
	openFilesTimeoutWindows = 50 * time.Millisecond
	openFilesTimeoutDefault = 250 * time.Millisecond
	fileScanDeadlineWindows = 3 * time.Second
	fileScanDeadlineDefault = 10 * time.Second
)

// FindProcessByName finds all running processes with the given name.
// Windows uses exact matching. Unix-like systems use substring matching and
// ignore a trailing ".exe" so callers can search for Windows process names.
func FindProcessByName(name string) ([]ProcessInfo, error) {
	processes, err := process.Processes()
	if err != nil {
		return nil, fmt.Errorf("failed to get process list: %w", err)
	}

	found := make([]ProcessInfo, 0)
	for _, p := range processes {
		pName, err := p.Name()
		if err != nil {
			continue // Skip processes we can't access
		}

		if processNameMatches(pName, name) {
			found = append(found, collectProcessInfo(p, pName, nil, false))
		}
	}

	return found, nil
}

// FindProcessesWithFile finds all processes that have the specified file open
func FindProcessesWithFile(filePath string) (*FileAccessInfo, error) {
	// Normalize the file path
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Ensure file exists
	if _, err := os.Stat(absPath); err != nil {
		return nil, fmt.Errorf("file does not exist: %w", err)
	}

	processes, err := process.Processes()
	if err != nil {
		return nil, fmt.Errorf("failed to get process list: %w", err)
	}

	info := &FileAccessInfo{
		FilePath:  absPath,
		Processes: make([]ProcessInfo, 0),
	}

	deadline := time.Now().Add(fileScanDeadline())
	for _, p := range processes {
		if time.Now().After(deadline) {
			break
		}

		openFiles, err := openFilesWithTimeout(p)
		if err != nil {
			continue // Skip processes we can't access
		}

		// Check if this process has our file open
		hasFile := false
		for _, f := range openFiles {
			// Normalize and compare paths
			openPath, err := filepath.Abs(f.Path)
			if err != nil {
				continue
			}

			if openPath == absPath {
				hasFile = true
				break
			}
		}

		if hasFile {
			info.Processes = append(info.Processes, collectProcessInfo(p, "", openFiles, true))
		}
	}

	return info, nil
}

// IsFileInUse checks if a file is currently opened by any process
func IsFileInUse(filePath string) (bool, error) {
	info, err := FindProcessesWithFile(filePath)
	if err != nil {
		return false, err
	}
	return len(info.Processes) > 0, nil
}

// GetProcessInfo retrieves detailed information about a specific process by PID
func GetProcessInfo(pid int32) (*ProcessInfo, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("failed to find process: %w", err)
	}

	info := collectProcessInfo(p, "", nil, true)
	return &info, nil
}

func processNameMatches(processName, target string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(processName, target)
	}

	normalizedProcess := strings.TrimSuffix(strings.ToLower(processName), ".exe")
	normalizedTarget := strings.TrimSuffix(strings.ToLower(target), ".exe")
	return strings.Contains(normalizedProcess, normalizedTarget)
}

func collectProcessInfo(p *process.Process, knownName string, knownOpenFiles []process.OpenFilesStat, includeOpenFiles bool) ProcessInfo {
	info := ProcessInfo{PID: p.Pid, Name: knownName}

	if info.Name == "" {
		if pName, err := p.Name(); err == nil {
			info.Name = pName
		}
	}
	if exe, err := p.Exe(); err == nil {
		info.Exe = exe
	}
	if cmdline, err := p.Cmdline(); err == nil {
		info.Cmdline = cmdline
	}
	if createTime, err := p.CreateTime(); err == nil {
		info.CreateTime = createTime
	}
	if memInfo, err := p.MemoryInfo(); err == nil {
		info.MemoryUsage = memInfo.RSS
	}
	if includeOpenFiles {
		openFiles := knownOpenFiles
		if openFiles == nil {
			if files, err := openFilesWithTimeout(p); err == nil {
				openFiles = files
			}
		}
		for _, f := range openFiles {
			info.OpenFiles = append(info.OpenFiles, f.Path)
		}
	}

	return info
}

func openFilesTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return openFilesTimeoutWindows
	}
	return openFilesTimeoutDefault
}

func fileScanDeadline() time.Duration {
	if runtime.GOOS == "windows" {
		return fileScanDeadlineWindows
	}
	return fileScanDeadlineDefault
}

func openFilesWithTimeout(p *process.Process) ([]process.OpenFilesStat, error) {
	type result struct {
		files []process.OpenFilesStat
		err   error
	}

	ch := make(chan result, 1)
	go func() {
		files, err := p.OpenFiles()
		ch <- result{files: files, err: err}
	}()

	select {
	case res := <-ch:
		return res.files, res.err
	case <-time.After(openFilesTimeout()):
		return nil, fmt.Errorf("timed out reading open files for pid %d", p.Pid)
	}
}
