package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPricingCheckpointFailureDiagnostics(t *testing.T) {
	dir := t.TempDir()
	// A directory cannot be replaced by the checkpoint file on any supported OS.
	a := &pricingActuator{path: dir, state: pricingActuationStatus{Phase: "complete", EventID: "accepted-event"}}
	if err := a.saveLocked(); err == nil {
		t.Fatal("expected replacement failure")
	}
	failure := a.state.LastCheckpointFailure
	if failure == nil || failure.Stage != "replace" || failure.SystemCode == 0 || failure.At.IsZero() {
		t.Fatalf("missing classified failure: %+v", failure)
	}
	encoded, err := json.Marshal(failure)
	if err != nil || strings.Contains(string(encoded), dir) {
		t.Fatal("diagnostic must not contain a filesystem path")
	}
	// A later ordinary local save retains the diagnostic and accepted identity.
	a.path = filepath.Join(dir, "checkpoint.json")
	if err := a.saveLocked(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(a.path)
	if err != nil {
		t.Fatal(err)
	}
	var restored pricingActuationStatus
	if err := json.Unmarshal(data, &restored); err != nil || restored.LastCheckpointFailure == nil || restored.EventID != "accepted-event" || restored.Phase != "complete" {
		t.Fatalf("lost diagnostic or accepted identity: %s", data)
	}
}
