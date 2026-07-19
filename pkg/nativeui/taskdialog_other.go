//go:build !windows

package nativeui

func ShowNativeDependencyError(error) bool {
	return false
}
