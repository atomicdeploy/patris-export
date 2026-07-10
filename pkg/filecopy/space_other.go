//go:build !linux

package filecopy

func availableBytes(_ string) (uint64, bool) {
	return 0, false
}
