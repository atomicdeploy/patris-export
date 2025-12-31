//go:build !windows
// +build !windows

package main

// getDefaultDirectAccess returns the default value for direct access flag on Unix systems.
// On Linux and other Unix systems, direct access is the default for better performance
// since file locking conflicts are less common.
func getDefaultDirectAccess() bool {
	return true
}
