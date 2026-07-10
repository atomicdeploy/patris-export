//go:build !windows

package ipc

import (
	"context"
	"net"
	"os"
	"strings"
)

func DefaultPath() string {
	return "/tmp/patris-export.sock"
}

func NormalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return DefaultPath()
	}
	return path
}

func listenLocal(path string) (net.Listener, error) {
	_ = os.Remove(path)
	return net.Listen("unix", path)
}

func cleanupLocal(path string) {
	_ = os.Remove(path)
}

func Dial(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", NormalizePath(path))
}
