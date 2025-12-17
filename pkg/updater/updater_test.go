package updater

import (
	"runtime"
	"strings"
	"testing"
)

func TestGetCurrentPlatformArtifactName(t *testing.T) {
	u := NewUpdater("testowner", "testrepo")
	name := u.GetCurrentPlatformArtifactName()
	
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
	u := NewUpdater("testowner", "testrepo")
	if u == nil {
		t.Fatal("Expected updater instance, got nil")
	}
	
	if u.client == nil {
		t.Error("Expected HTTP client to be initialized")
	}
	
	if u.binaryName == "" {
		t.Error("Expected binary name to be initialized")
	}
	
	if u.repoOwner != "testowner" {
		t.Errorf("Expected repoOwner to be 'testowner', got '%s'", u.repoOwner)
	}
	
	if u.repoName != "testrepo" {
		t.Errorf("Expected repoName to be 'testrepo', got '%s'", u.repoName)
	}
}

func TestDeriveBinaryName(t *testing.T) {
	fallbackName := "test-fallback"
	name := deriveBinaryName(fallbackName)
	
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
	
	// Test the fallback behavior specifically
	// When os.Executable() works, the name should be derived from the test binary
	// Otherwise, it should use the fallback
	// We can't predict which path will be taken, so we just ensure
	// the result is valid (non-empty, no platform suffixes, no .exe)
	t.Logf("Derived binary name: %s (fallback would be: %s)", name, fallbackName)
}

func TestGetPlatformBinaryName(t *testing.T) {
	u := NewUpdater("testowner", "testrepo")
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

func TestDeriveRepoInfoFromModule(t *testing.T) {
	// This test assumes we're running from within the project directory
	owner, name, err := DeriveRepoInfoFromModule()
	if err != nil {
		t.Skipf("Skipping test - not in a Go module directory: %v", err)
		return
	}
	
	// Should have parsed successfully
	if owner == "" {
		t.Error("Expected non-empty repository owner")
	}
	
	if name == "" {
		t.Error("Expected non-empty repository name")
	}
}
