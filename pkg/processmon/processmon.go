package processmon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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

// FindProcessByName finds all running processes with the given name
// For Windows, it looks for exact matches (e.g., "patris81.exe")
// For Unix/Linux, it looks for processes containing the name
func FindProcessByName(name string) ([]ProcessInfo, error) {
	processes, err := process.Processes()
	if err != nil {
		return nil, fmt.Errorf("failed to get process list: %w", err)
	}

	var found []ProcessInfo
	for _, p := range processes {
		pName, err := p.Name()
		if err != nil {
			continue // Skip processes we can't access
		}

		// Case-insensitive matching
		// On Windows, match exact name; on Unix/Linux, match substring
		matches := false
		if runtime.GOOS == "windows" {
			matches = strings.EqualFold(pName, name)
		} else {
			matches = strings.Contains(strings.ToLower(pName), strings.ToLower(name))
		}

		if matches {
			info := ProcessInfo{
				PID:  p.Pid,
				Name: pName,
			}

			// Get executable path
			if exe, err := p.Exe(); err == nil {
				info.Exe = exe
			}

			// Get command line
			if cmdline, err := p.Cmdline(); err == nil {
				info.Cmdline = cmdline
			}

			// Get create time
			if createTime, err := p.CreateTime(); err == nil {
				info.CreateTime = createTime
			}

			// Get memory usage
			if memInfo, err := p.MemoryInfo(); err == nil {
				info.MemoryUsage = memInfo.RSS
			}

			// Get open files
			if openFiles, err := p.OpenFiles(); err == nil {
				for _, f := range openFiles {
					info.OpenFiles = append(info.OpenFiles, f.Path)
				}
			}

			found = append(found, info)
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

	for _, p := range processes {
		// Get open files for this process
		openFiles, err := p.OpenFiles()
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
			pInfo := ProcessInfo{
				PID: p.Pid,
			}

			// Get process name
			if pName, err := p.Name(); err == nil {
				pInfo.Name = pName
			}

			// Get executable path
			if exe, err := p.Exe(); err == nil {
				pInfo.Exe = exe
			}

			// Get command line
			if cmdline, err := p.Cmdline(); err == nil {
				pInfo.Cmdline = cmdline
			}

			// Get create time
			if createTime, err := p.CreateTime(); err == nil {
				pInfo.CreateTime = createTime
			}

			// Get memory usage
			if memInfo, err := p.MemoryInfo(); err == nil {
				pInfo.MemoryUsage = memInfo.RSS
			}

			// Add the list of open files for this process
			for _, f := range openFiles {
				pInfo.OpenFiles = append(pInfo.OpenFiles, f.Path)
			}

			info.Processes = append(info.Processes, pInfo)
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

	info := &ProcessInfo{
		PID: pid,
	}

	// Get process name
	if pName, err := p.Name(); err == nil {
		info.Name = pName
	}

	// Get executable path
	if exe, err := p.Exe(); err == nil {
		info.Exe = exe
	}

	// Get command line
	if cmdline, err := p.Cmdline(); err == nil {
		info.Cmdline = cmdline
	}

	// Get create time
	if createTime, err := p.CreateTime(); err == nil {
		info.CreateTime = createTime
	}

	// Get memory usage
	if memInfo, err := p.MemoryInfo(); err == nil {
		info.MemoryUsage = memInfo.RSS
	}

	// Get open files
	if openFiles, err := p.OpenFiles(); err == nil {
		for _, f := range openFiles {
			info.OpenFiles = append(info.OpenFiles, f.Path)
		}
	}

	return info, nil
}
