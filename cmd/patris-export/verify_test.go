package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyCommandAcceptsExactSnapshotBytesFromStdin(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "patris-product-sync.synthetic.json"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := newVerifyCommand()
	cmd.SetArgs([]string{"-"})
	cmd.SetIn(bytes.NewReader(fixture))
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verify stdin: %v", err)
	}
	text := output.String()
	for _, expected := range []string{"valid snapshot:", "products=2", "categories=2", `source="synthetic-fixture"/"synthetic-kala.db"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("summary %q does not contain %q", text, expected)
		}
	}
}

func TestVerifyCommandReadsRegularFileWithoutMutation(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "patris-product-sync.synthetic.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newVerifyCommand()
	cmd.SetArgs([]string{path})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verify file: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, fixture) {
		t.Fatal("verify modified the snapshot")
	}
}

func TestVerifyCommandFailsClosed(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "patris-product-sync.synthetic.json"))
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"hash mismatch": bytes.Replace(fixture, []byte(`"event_id": "sha256:`), []byte(`"event_id": "sha256:0`), 1),
		"oversized":     bytes.Repeat([]byte("x"), int(maxCanonicalSnapshotBytes+1)),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			cmd := newVerifyCommand()
			cmd.SetArgs([]string{"-"})
			cmd.SetIn(bytes.NewReader(input))
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			if err := cmd.Execute(); err == nil {
				t.Fatal("invalid snapshot was accepted")
			}
			if strings.Contains(output.String(), "valid snapshot:") {
				t.Fatalf("invalid snapshot emitted a success summary: %q", output.String())
			}
		})
	}
}

func TestVerifyCommandRejectsNonRegularPath(t *testing.T) {
	cmd := newVerifyCommand()
	cmd.SetArgs([]string{t.TempDir()})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory error = %v", err)
	}
}
