package updater

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGetCurrentPlatformArtifactName(t *testing.T) {
	u := NewUpdater("testowner", "testrepo")
	name := u.GetCurrentPlatformArtifactName()

	// Should not be empty on supported platforms
	if name == "" && (runtime.GOOS == "windows" || runtime.GOOS == "linux" || runtime.GOOS == "darwin") {
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
	case "darwin":
		expectedSuffix := "-darwin-amd64"
		if runtime.GOARCH == "arm64" {
			expectedSuffix = "-darwin-arm64"
		}
		if !strings.HasSuffix(name, expectedSuffix) {
			t.Errorf("Expected Darwin artifact name to end with '%s', got '%s'", expectedSuffix, name)
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

func TestNormalizeBinaryName(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain name", "patris-export", "patris-export"},
		{"windows executable", "patris-export.exe", "patris-export"},
		{"linux build suffix", "patris-export-linux-amd64", "patris-export"},
		{"windows build suffix", "patris-export-windows-amd64.exe", "patris-export"},
		{"darwin amd64 suffix", "patris-export-darwin-amd64", "patris-export"},
		{"darwin arm64 suffix", "patris-export-darwin-arm64", "patris-export"},
		{"empty fallback", "", "fallback-name"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeBinaryName(tc.input, "fallback-name"); got != tc.expected {
				t.Fatalf("normalizeBinaryName(%q) = %q, expected %q", tc.input, got, tc.expected)
			}
		})
	}
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
	case "darwin":
		expectedSuffix := "-darwin-amd64"
		if runtime.GOARCH == "arm64" {
			expectedSuffix = "-darwin-arm64"
		}
		if !strings.Contains(name, expectedSuffix) {
			t.Errorf("Expected Darwin binary name to contain '%s', got '%s'", expectedSuffix, name)
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

func TestGetLatestSuccessfulRun(t *testing.T) {
	u, server := newTestUpdater(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/testowner/testrepo/actions/runs" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("branch"); got != "main" {
			t.Fatalf("branch query = %q, expected main", got)
		}
		fmt.Fprint(w, `{"total_count":2,"workflow_runs":[{"id":41,"name":"Lint","status":"completed","conclusion":"success","created_at":"2026-07-10T10:00:00Z"},{"id":42,"name":"Windows Build","status":"completed","conclusion":"success","created_at":"2026-07-10T11:00:00Z"}]}`)
	}))
	defer server.Close()

	run, err := u.GetLatestSuccessfulRun("main")
	if err != nil {
		t.Fatalf("GetLatestSuccessfulRun returned error: %v", err)
	}
	if run.ID != 42 {
		t.Fatalf("run ID = %d, expected 42", run.ID)
	}
}

func TestGetArtifactsForRun(t *testing.T) {
	u, server := newTestUpdater(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/testowner/testrepo/actions/runs/42/artifacts" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"total_count":1,"artifacts":[{"id":7,"name":"patris-export-linux-amd64","size_in_bytes":123,"archive_download_url":"https://example.test/archive.zip","digest":"sha256:abcd","expired":false,"created_at":"2026-07-10T11:01:00Z"}]}`)
	}))
	defer server.Close()

	artifacts, err := u.GetArtifactsForRun(42)
	if err != nil {
		t.Fatalf("GetArtifactsForRun returned error: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("artifact count = %d, expected 1", len(artifacts))
	}
	if artifacts[0].Digest != "sha256:abcd" {
		t.Fatalf("artifact digest = %q, expected sha256:abcd", artifacts[0].Digest)
	}
}

func TestDownloadArtifactVerifiesDigest(t *testing.T) {
	payload := []byte("zip bytes")
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-GitHub-Api-Version"); got != githubAPIVersion {
			t.Fatalf("GitHub API version header = %q, expected %q", got, githubAPIVersion)
		}
		w.Write(payload)
	}))
	defer server.Close()

	u := NewUpdater("testowner", "testrepo")
	u.client = server.Client()
	artifact := &Artifact{
		Name:               "patris-export-linux-amd64",
		ArchiveDownloadURL: server.URL + "/artifact.zip",
		Digest:             fmt.Sprintf("sha256:%x", sum),
	}

	destPath, err := u.DownloadArtifact(artifact, t.TempDir())
	if err != nil {
		t.Fatalf("DownloadArtifact returned error: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read downloaded artifact: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("downloaded payload = %q, expected %q", got, payload)
	}
}

func TestDownloadArtifactRejectsDigestMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("unexpected bytes"))
	}))
	defer server.Close()

	u := NewUpdater("testowner", "testrepo")
	u.client = server.Client()
	artifact := &Artifact{
		Name:               "patris-export-linux-amd64",
		ArchiveDownloadURL: server.URL + "/artifact.zip",
		Digest:             "sha256:0000000000000000000000000000000000000000000000000000000000000000",
	}

	if _, err := u.DownloadArtifact(artifact, t.TempDir()); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected digest mismatch error, got %v", err)
	}
}

func TestReplaceExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("direct replacement helper is only used on non-Windows platforms")
	}

	tmpDir := t.TempDir()
	currentPath := filepath.Join(tmpDir, "patris-export")
	newPath := filepath.Join(tmpDir, "patris-export-new")
	if err := os.WriteFile(currentPath, []byte("old"), 0755); err != nil {
		t.Fatalf("failed to write current executable: %v", err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0755); err != nil {
		t.Fatalf("failed to write replacement executable: %v", err)
	}

	if err := replaceExecutable(currentPath, newPath); err != nil {
		t.Fatalf("replaceExecutable returned error: %v", err)
	}

	content, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("failed to read replaced executable: %v", err)
	}
	if string(content) != "new" {
		t.Fatalf("replacement content = %q, expected new", content)
	}
	if _, err := os.Stat(currentPath + ".old"); !os.IsNotExist(err) {
		t.Fatalf("backup should have been removed, stat error: %v", err)
	}
}

func newTestUpdater(t *testing.T, handler http.Handler) (*Updater, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(handler)
	u := NewUpdater("testowner", "testrepo")
	u.apiBaseURL = server.URL
	u.client = server.Client()
	return u, server
}
