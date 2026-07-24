//go:build !windows

package filecopy

import "os"

func replaceFileAtomic(source, destination string) error {
	return os.Rename(source, destination)
}
