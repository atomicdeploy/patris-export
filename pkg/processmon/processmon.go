package processmon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

// ProcessInfo contains information about a running process
type ProcessInfo struct {
	PID     int32  `json:"pid"`
	Name    string `json:"name"`
	Exe     string `json:"exe"`
	Cmdline string `json:"cmdline"`
	// OpenFiles contains files known to the operation that produced this
	// ProcessInfo. On Windows, targeted file scans contain only the requested
	// file, while generic process-info queries leave this field empty.
	OpenFiles   []string `json:"open_files,omitempty"`
	CreateTime  int64    `json:"create_time"`
	MemoryUsage uint64   `json:"memory_usage,omitempty"`
}

// FileAccessInfo contains information about processes accessing a file
type FileAccessInfo struct {
	FilePath  string        `json:"file_path"`
	Processes []ProcessInfo `json:"processes"`
}

type fileProcessMatch struct {
	PID        int32
	Name       string
	OpenFiles  []string
	CreateTime int64
}

// FindProcessByName finds all running processes with the given name.
// Windows uses exact matching. Unix-like systems use substring matching and
// ignore a trailing ".exe" so callers can search for Windows process names.
func FindProcessByName(name string) ([]ProcessInfo, error) {
	return FindProcessByNameContext(context.Background(), name)
}

// FindProcessByNameContext finds all running processes with the given name
// and stops between process inspections when ctx is cancelled.
func FindProcessByNameContext(ctx context.Context, name string) ([]ProcessInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	processes, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get process list: %w", err)
	}

	found := make([]ProcessInfo, 0)
	for _, p := range processes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		pName, err := p.NameWithContext(ctx)
		if err != nil {
			continue // Skip processes we can't access
		}

		if processNameMatches(pName, name) {
			found = append(found, collectProcessInfo(ctx, p, pName, nil, false))
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return found, nil
}

// FindProcessesWithFile finds all processes that have the specified file open
func FindProcessesWithFile(filePath string) (*FileAccessInfo, error) {
	return FindProcessesWithFileContext(context.Background(), filePath)
}

// FindProcessesWithFileContext finds processes using filePath without
// abandoning background scans when ctx expires.
func FindProcessesWithFileContext(ctx context.Context, filePath string) (*FileAccessInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Normalize the file path
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Ensure file exists and is a file. Restart Manager accepts files, not
	// directories, and otherwise reports a fairly opaque registration error.
	fileInfo, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("file does not exist: %w", err)
	}
	if fileInfo.IsDir() {
		return nil, fmt.Errorf("path is a directory, not a file: %s", absPath)
	}

	matches, err := findFileProcesses(ctx, absPath)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	info := &FileAccessInfo{
		FilePath:  absPath,
		Processes: make([]ProcessInfo, 0),
	}

	for _, match := range matches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		fallback := ProcessInfo{
			PID:        match.PID,
			Name:       match.Name,
			OpenFiles:  append([]string(nil), match.OpenFiles...),
			CreateTime: match.CreateTime,
		}

		p, err := process.NewProcessWithContext(ctx, match.PID)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			// Restart Manager already identified the lock owner. Preserve that
			// useful result even if the process is protected or exits before we
			// can enrich it with optional details.
			info.Processes = append(info.Processes, fallback)
			continue
		}

		knownName := match.Name
		if processName, nameErr := p.NameWithContext(ctx); nameErr == nil && processName != "" {
			knownName = processName
		} else if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}

		if match.CreateTime != 0 {
			createTime, createErr := p.CreateTimeWithContext(ctx)
			if createErr != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, ctxErr
				}
				info.Processes = append(info.Processes, fallback)
				continue
			}
			if createTime != match.CreateTime {
				// The PID was reused after Restart Manager took its snapshot.
				// Preserve the point-in-time lock owner without enriching it
				// with details from the unrelated replacement process.
				info.Processes = append(info.Processes, fallback)
				continue
			}
		}

		info.Processes = append(info.Processes, collectProcessInfo(ctx, p, knownName, match.OpenFiles, true))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return info, nil
}

// IsFileInUse checks if a file is currently opened by any process
func IsFileInUse(filePath string) (bool, error) {
	return IsFileInUseContext(context.Background(), filePath)
}

// IsFileInUseContext checks whether filePath is currently opened by any
// process while honoring ctx between OS queries.
func IsFileInUseContext(ctx context.Context, filePath string) (bool, error) {
	info, err := FindProcessesWithFileContext(ctx, filePath)
	if err != nil {
		return false, err
	}
	return len(info.Processes) > 0, nil
}

// GetProcessInfo retrieves detailed information about a specific process by PID.
// On Windows, OpenFiles is intentionally empty because enumerating a process's
// handles requires the system-wide snapshot that targeted Restart Manager
// queries avoid. Use FindProcessesWithFile to query ownership of a known file.
func GetProcessInfo(pid int32) (*ProcessInfo, error) {
	return GetProcessInfoContext(context.Background(), pid)
}

// GetProcessInfoContext retrieves detailed information about a process. Its
// Windows OpenFiles behavior matches GetProcessInfo.
func GetProcessInfoContext(ctx context.Context, pid int32) (*ProcessInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return nil, fmt.Errorf("failed to find process: %w", err)
	}

	info := collectProcessInfo(ctx, p, "", nil, true)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

func collectProcessInfo(ctx context.Context, p *process.Process, knownName string, knownOpenFiles []string, includeOpenFiles bool) ProcessInfo {
	info := ProcessInfo{PID: p.Pid, Name: knownName}

	if info.Name == "" {
		if pName, err := p.NameWithContext(ctx); err == nil {
			info.Name = pName
		}
	}
	if exe, err := p.ExeWithContext(ctx); err == nil {
		info.Exe = exe
	}
	if cmdline, err := p.CmdlineWithContext(ctx); err == nil {
		info.Cmdline = cmdline
	}
	if createTime, err := p.CreateTimeWithContext(ctx); err == nil {
		info.CreateTime = createTime
	}
	if memInfo, err := p.MemoryInfoWithContext(ctx); err == nil {
		info.MemoryUsage = memInfo.RSS
	}
	if includeOpenFiles {
		openFiles := knownOpenFiles
		if openFiles == nil {
			if files, err := openFilesForProcess(ctx, p); err == nil {
				openFiles = files
			}
		}
		info.OpenFiles = append(info.OpenFiles, openFiles...)
	}

	return info
}
