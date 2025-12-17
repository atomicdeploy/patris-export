package updater

import (
	"runtime"
	"strings"
	"testing"
)

func TestGetCurrentPlatformArtifactName(t *testing.T) {
	name := GetCurrentPlatformArtifactName()
	
	// Should not be empty on supported platforms
	if name == "" && (runtime.GOOS == "windows" || runtime.GOOS == "linux") {
		t.Errorf("Expected non-empty artifact name for %s, got empty string", runtime.GOOS)
	}
	
	// Should contain platform suffix
	switch runtime.GOOS {
	case "windows":
		if !strings.HasSuffix(name, "-windows-amd64") {
			t.Errorf("Expected Windows artifact name to end with '-windows-amd64', got '%s'", name)
		}
	case "linux":
		if !strings.HasSuffix(name, "-linux-amd64") {
			t.Errorf("Expected Linux artifact name to end with '-linux-amd64', got '%s'", name)
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
	
	if u.binaryName == "" {
		t.Error("Expected binary name to be initialized")
	}
}

func TestDeriveBinaryName(t *testing.T) {
	name := deriveBinaryName()
	
	// Should never be empty
	if name == "" {
		t.Error("Expected non-empty binary name")
	}
	
	// Should not contain platform suffixes
	if strings.Contains(name, "-linux-") || strings.Contains(name, "-windows-") {
		t.Errorf("Binary name should not contain platform suffix, got '%s'", name)
	}
	
	// Should not have .exe extension
	if strings.HasSuffix(name, ".exe") {
		t.Errorf("Binary name should not have .exe extension, got '%s'", name)
	}
}

func TestGetPlatformBinaryName(t *testing.T) {
	u := NewUpdater()
	name := u.GetPlatformBinaryName()
	
	// Should not be empty
	if name == "" {
		t.Error("Expected non-empty platform binary name")
	}
	
	// Should contain the base binary name
	if !strings.Contains(name, u.binaryName) {
		t.Errorf("Expected platform binary name to contain '%s', got '%s'", u.binaryName, name)
	}
	
	// Check platform-specific expectations
	switch runtime.GOOS {
	case "windows":
		if !strings.HasSuffix(name, ".exe") {
			t.Errorf("Expected Windows binary name to end with '.exe', got '%s'", name)
		}
		if !strings.Contains(name, "-windows-amd64") {
			t.Errorf("Expected Windows binary name to contain '-windows-amd64', got '%s'", name)
		}
	case "linux":
		if strings.HasSuffix(name, ".exe") {
			t.Errorf("Expected Linux binary name to not have '.exe', got '%s'", name)
		}
		if !strings.Contains(name, "-linux-amd64") {
			t.Errorf("Expected Linux binary name to contain '-linux-amd64', got '%s'", name)
		}
	}
}
