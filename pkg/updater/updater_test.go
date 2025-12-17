package updater

import (
	"runtime"
	"testing"
)

func TestGetCurrentPlatformArtifactName(t *testing.T) {
	name := GetCurrentPlatformArtifactName()
	
	switch runtime.GOOS {
	case "windows":
		if name != "patris-export-windows-amd64" {
			t.Errorf("Expected 'patris-export-windows-amd64', got '%s'", name)
		}
	case "linux":
		if name != "patris-export-linux-amd64" {
			t.Errorf("Expected 'patris-export-linux-amd64', got '%s'", name)
		}
	default:
		if name != "" {
			t.Errorf("Expected empty string for unsupported platform, got '%s'", name)
		}
	}
}

func TestNewUpdater(t *testing.T) {
	u := NewUpdater()
	if u == nil {
		t.Fatal("Expected updater instance, got nil")
	}
	
	if u.client == nil {
		t.Error("Expected HTTP client to be initialized")
	}
}
