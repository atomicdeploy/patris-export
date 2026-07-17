package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/atomicdeploy/patris-export/pkg/licensing"
)

func TestLicenseStatusCommandWorksWithoutEngineEnforcement(t *testing.T) {
	cmd := newLicenseCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("license status returned error: %v", err)
	}
	var status licensing.Status
	if err := json.Unmarshal(output.Bytes(), &status); err != nil {
		t.Fatalf("license status output is not JSON: %v\n%s", err, output.String())
	}
	if status.Mode == "" || status.State == "" {
		t.Fatalf("license status omits mode/state: %+v", status)
	}
}

func TestLicenseSubcommandsBypassEnforcement(t *testing.T) {
	cmd := newLicenseCommand()
	status, _, err := cmd.Find([]string{"status"})
	if err != nil {
		t.Fatal(err)
	}
	if !licenseCommandBypassesEnforcement(status) {
		t.Fatal("license status did not inherit the management bypass")
	}
}
