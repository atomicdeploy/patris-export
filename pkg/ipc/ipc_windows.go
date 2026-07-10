//go:build windows

package ipc

import (
	"context"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
)

func DefaultPath() string {
	return `\\.\pipe\patris-export`
}

func NormalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return DefaultPath()
	}
	lower := strings.ToLower(path)
	if strings.HasPrefix(lower, `\\.\pipe\`) || strings.HasPrefix(lower, `\\?\pipe\`) {
		return path
	}
	path = strings.Trim(path, `\/`)
	if path == "" {
		path = "patris-export"
	}
	return `\\.\pipe\` + strings.ReplaceAll(path, `/`, `-`)
}

func listenLocal(path string) (net.Listener, error) {
	return winio.ListenPipe(path, &winio.PipeConfig{
		InputBufferSize:  16 * 1024 * 1024,
		OutputBufferSize: 16 * 1024 * 1024,
	})
}

func cleanupLocal(path string) {}

func Dial(ctx context.Context, path string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, NormalizePath(path))
}
