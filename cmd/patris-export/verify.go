package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/spf13/cobra"
)

const maxCanonicalSnapshotBytes int64 = canonical.MaxSnapshotBytes

func newVerifyCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "verify <snapshot.json|->",
		Short:         "Verify a canonical product-sync snapshot without applying it",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := readVerifyInput(cmd, args[0])
			if err != nil {
				return err
			}
			_, summary, err := canonical.VerifySnapshotJSON(data)
			if err != nil {
				return fmt.Errorf("snapshot verification failed: %w", err)
			}
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"valid snapshot: products=%d categories=%d excluded=%d quarantined=%d warnings=%d source=%q/%q revision=%s event=%s\n",
				summary.Products,
				summary.Categories,
				summary.ExcludedCodes,
				summary.QuarantinedCodes,
				summary.Warnings,
				summary.SourceID,
				summary.SourceDataset,
				summary.SourceRevision,
				summary.EventID,
			)
			return err
		},
	}
}

func readVerifyInput(cmd *cobra.Command, source string) ([]byte, error) {
	if source == "-" {
		return readBoundedSnapshot(cmd.InOrStdin())
	}

	path := filepath.Clean(source)
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect snapshot: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("snapshot path must not be a symbolic link")
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("snapshot path must be a regular file")
	}
	if before.Size() > maxCanonicalSnapshotBytes {
		return nil, fmt.Errorf("snapshot exceeds the %d-byte limit", maxCanonicalSnapshotBytes)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open snapshot: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened snapshot: %w", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("snapshot changed before it could be read")
	}

	data, err := readBoundedSnapshot(file)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect read snapshot: %w", err)
	}
	if !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("snapshot changed while it was being read")
	}
	return data, nil
}

func readBoundedSnapshot(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxCanonicalSnapshotBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	if int64(len(data)) > maxCanonicalSnapshotBytes {
		return nil, fmt.Errorf("snapshot exceeds the %d-byte limit", maxCanonicalSnapshotBytes)
	}
	return data, nil
}
