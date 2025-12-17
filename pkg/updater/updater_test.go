package updater

import (
	"fmt"
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

func TestFindPlatformArtifact(t *testing.T) {
	u := NewUpdater("testowner", "testrepo")
	
	// Test with artifacts matching the current platform
	// Use the actual binaryName from the updater
	currentPlatform := runtime.GOOS
	currentArch := runtime.GOARCH
	
	var platformArtifactName string
	switch currentPlatform {
	case "linux":
		platformArtifactName = fmt.Sprintf("%s-linux-amd64", u.binaryName)
	case "windows":
		// Test with mingw variant since that's what we support
		platformArtifactName = fmt.Sprintf("%s-windows-mingw-amd64", u.binaryName)
	case "darwin":
		if currentArch == "arm64" {
			platformArtifactName = fmt.Sprintf("%s-darwin-arm64", u.binaryName)
		} else {
			platformArtifactName = fmt.Sprintf("%s-darwin-amd64", u.binaryName)
		}
	}
	
	t.Run("Current platform exact match", func(t *testing.T) {
		artifacts := []Artifact{
			{Name: platformArtifactName},
			{Name: "other-platform"},
		}
		
		result := u.FindPlatformArtifact(artifacts)
		if result == nil {
			t.Errorf("Expected to find artifact for current platform, but got nil (looking for %s)", platformArtifactName)
		} else if result.Name != platformArtifactName {
			t.Errorf("Expected artifact name %s, got %s", platformArtifactName, result.Name)
		}
	})
	
	t.Run("No matching artifact for current platform", func(t *testing.T) {
		// Create artifacts that don't match current platform
		var otherArtifacts []Artifact
		if currentPlatform != "windows" {
			otherArtifacts = append(otherArtifacts, Artifact{Name: fmt.Sprintf("%s-windows-mingw-amd64", u.binaryName)})
		}
		if currentPlatform != "linux" {
			otherArtifacts = append(otherArtifacts, Artifact{Name: fmt.Sprintf("%s-linux-amd64", u.binaryName)})
		}
		if currentPlatform != "darwin" {
			otherArtifacts = append(otherArtifacts, Artifact{Name: fmt.Sprintf("%s-darwin-amd64", u.binaryName)})
		}
		
		if len(otherArtifacts) > 0 {
			result := u.FindPlatformArtifact(otherArtifacts)
			if result != nil {
				t.Errorf("Expected no artifact match, but got %s", result.Name)
			}
		}
	})
	
	// Test Windows-specific flexible matching (only meaningful on Windows)
	if currentPlatform == "windows" {
		t.Run("Windows flexible matching", func(t *testing.T) {
			testCases := []struct {
				name         string
				artifactName string
			}{
				{"mingw variant", fmt.Sprintf("%s-windows-mingw-amd64", u.binaryName)},
				{"mingw-cross variant", fmt.Sprintf("%s-windows-mingw-cross-amd64", u.binaryName)},
				{"exact match", fmt.Sprintf("%s-windows-amd64", u.binaryName)},
			}
			
			for _, tc := range testCases {
				t.Run(tc.name, func(t *testing.T) {
					artifacts := []Artifact{
						{Name: tc.artifactName},
						{Name: fmt.Sprintf("%s-linux-amd64", u.binaryName)},
					}
					
					result := u.FindPlatformArtifact(artifacts)
					if result == nil {
						t.Errorf("Expected to find Windows artifact, but got nil")
					} else if result.Name != tc.artifactName {
						t.Errorf("Expected artifact name %s, got %s", tc.artifactName, result.Name)
					}
				})
			}
		})
	} else {
		t.Logf("Skipping Windows-specific flexible matching tests (not running on Windows)")
	}
}

