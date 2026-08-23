package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestVerifyWorkbookFontsCommandFailsClosedForRealWorkbookWithoutNamedPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unconfigured.xlsx")
	book := excelize.NewFile()
	if err := book.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := newVerifyWorkbookFontsCommand()
	cmd.SetArgs([]string{path})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "font verification failed") {
		t.Fatalf("unconfigured workbook error = %v", err)
	}
	if strings.Contains(output.String(), "valid workbook fonts") {
		t.Fatalf("unconfigured workbook emitted success: %q", output.String())
	}
}
