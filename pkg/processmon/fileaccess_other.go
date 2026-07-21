//go:build !windows

package processmon

import (
	"context"
	"path/filepath"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

const (
	openFilesTimeoutDefault = 250 * time.Millisecond
	fileScanDeadlineDefault = 10 * time.Second
)

func findFileProcesses(ctx context.Context, filePath string) ([]fileProcessMatch, error) {
	scanCtx, cancel := context.WithTimeout(ctx, fileScanDeadlineDefault)
	defer cancel()

	processes, err := process.ProcessesWithContext(scanCtx)
	if err != nil {
		return nil, err
	}

	matches := make([]fileProcessMatch, 0)
	for _, p := range processes {
		if err := scanCtx.Err(); err != nil {
			return nil, err
		}

		files, err := p.OpenFilesWithContext(scanCtx)
		if err != nil {
			continue
		}

		paths := make([]string, 0, len(files))
		hasFile := false
		for _, file := range files {
			openPath, err := filepath.Abs(file.Path)
			if err != nil {
				continue
			}
			paths = append(paths, file.Path)
			if openPath == filePath {
				hasFile = true
			}
		}
		if hasFile {
			matches = append(matches, fileProcessMatch{PID: p.Pid, OpenFiles: paths})
		}
	}
	if err := scanCtx.Err(); err != nil {
		return nil, err
	}

	return matches, nil
}

func openFilesForProcess(ctx context.Context, p *process.Process) ([]string, error) {
	openCtx, cancel := context.WithTimeout(ctx, openFilesTimeoutDefault)
	defer cancel()

	files, err := p.OpenFilesWithContext(openCtx)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths, nil
}
