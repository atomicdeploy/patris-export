//go:build windows
// +build windows

package main

// getDefaultDirectAccess returns the default value for direct access flag on Windows.
// On Windows, temp file copying is the default to avoid write-lock conflicts with
// Borland Database Engine (BDE) and other applications.
func getDefaultDirectAccess() bool {
	return false
}
