//go:build !windows

package server

import "os"

func replaceSourceDeliveryOutboxFile(source, destination string) error {
	return os.Rename(source, destination)
}
