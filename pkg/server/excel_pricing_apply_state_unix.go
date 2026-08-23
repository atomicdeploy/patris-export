//go:build !windows

package server

import (
	"os"
	"path/filepath"
)

func replaceExcelPricingApplyStateFile(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
