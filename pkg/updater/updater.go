package updater

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	githubAPIURL = "https://api.github.com"
	repoOwner    = "atomicdeploy"
	repoName     = "patris-export"
)

// Updater handles auto-update functionality
type Updater struct {
	apiToken     string
	client       *http.Client
	binaryName   string // Base name of the binary (e.g., "patris-export")
}

// WorkflowRun represents a GitHub Actions workflow run
type WorkflowRun struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	HeadBranch string    `json:"head_branch"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	CreatedAt  time.Time `json:"created_at"`
}

// WorkflowRunsResponse represents the API response for workflow runs
type WorkflowRunsResponse struct {
	TotalCount   int           `json:"total_count"`
	WorkflowRuns []WorkflowRun `json:"workflow_runs"`
}

// Artifact represents a GitHub Actions artifact
type Artifact struct {
	ID                 int64     `json:"id"`
	Name               string    `json:"name"`
	SizeInBytes        int64     `json:"size_in_bytes"`
	ArchiveDownloadURL string    `json:"archive_download_url"`
	Expired            bool      `json:"expired"`
	CreatedAt          time.Time `json:"created_at"`
}

// ArtifactsResponse represents the API response for artifacts
type ArtifactsResponse struct {
	TotalCount int        `json:"total_count"`
	Artifacts  []Artifact `json:"artifacts"`
}

// NewUpdater creates a new updater instance
func NewUpdater() *Updater {
	// Derive binary name from the current executable
	binaryName := deriveBinaryName()
	
	return &Updater{
		apiToken:   os.Getenv("GITHUB_TOKEN"),
		binaryName: binaryName,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// deriveBinaryName derives the base binary name from the current executable
func deriveBinaryName() string {
	exe, err := os.Executable()
	if err != nil {
		// Fallback to repo name if we can't get the executable
		return repoName
	}
	
	// Get the base name without path
	baseName := filepath.Base(exe)
	
	// Remove platform suffix and extension if present
	// Examples: "patris-export-linux-amd64" -> "patris-export"
	//           "patris-export-windows-amd64.exe" -> "patris-export"
	//           "patris-export.exe" -> "patris-export"
	//           "patris-export" -> "patris-export"
	
	// Remove .exe extension if present
	baseName = strings.TrimSuffix(baseName, ".exe")
	
	// Remove platform suffixes
	baseName = strings.TrimSuffix(baseName, "-linux-amd64")
	baseName = strings.TrimSuffix(baseName, "-windows-amd64")
	baseName = strings.TrimSuffix(baseName, "-darwin-amd64")
	baseName = strings.TrimSuffix(baseName, "-darwin-arm64")
	
	// If we ended up with an empty string, use repo name
	if baseName == "" {
		return repoName
	}
	
	return baseName
}

// doRequest performs an HTTP request with proper headers
func (u *Updater) doRequest(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add GitHub API headers
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	// Add token if available
	if u.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+u.apiToken)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return resp, nil
}

// GetLatestSuccessfulRun gets the latest successful workflow run for a branch
func (u *Updater) GetLatestSuccessfulRun(branch string) (*WorkflowRun, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/actions/runs?branch=%s&status=success&per_page=100",
		githubAPIURL, repoOwner, repoName, branch)

	resp, err := u.doRequest(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var runsResp WorkflowRunsResponse
	if err := json.NewDecoder(resp.Body).Decode(&runsResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(runsResp.WorkflowRuns) == 0 {
		return nil, fmt.Errorf("no successful workflow runs found for branch '%s'", branch)
	}

	// Find the latest successful build workflow run
	for _, run := range runsResp.WorkflowRuns {
		runNameLower := strings.ToLower(run.Name)
		if run.Conclusion == "success" && (runNameLower == "build" || strings.Contains(runNameLower, "build")) {
			return &run, nil
		}
	}

	return nil, fmt.Errorf("no successful build workflow run found for branch '%s'", branch)
}

// GetArtifactsForRun gets all artifacts for a workflow run
func (u *Updater) GetArtifactsForRun(runID int64) ([]Artifact, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/actions/runs/%d/artifacts",
		githubAPIURL, repoOwner, repoName, runID)

	resp, err := u.doRequest(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var artifactsResp ArtifactsResponse
	if err := json.NewDecoder(resp.Body).Decode(&artifactsResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(artifactsResp.Artifacts) == 0 {
		return nil, fmt.Errorf("no artifacts found for workflow run %d", runID)
	}

	return artifactsResp.Artifacts, nil
}

// DownloadArtifact downloads an artifact and returns the path to the downloaded file
func (u *Updater) DownloadArtifact(artifact *Artifact, destDir string) (string, error) {
	// Ensure destination directory exists
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create destination directory: %w", err)
	}

	destPath := filepath.Join(destDir, artifact.Name+".zip")

	// Create the file
	out, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	// Download the artifact
	req, err := http.NewRequest("GET", artifact.ArchiveDownloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Add GitHub API headers
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	if u.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+u.apiToken)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("download failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Write to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return destPath, nil
}

// ExtractExecutable extracts the executable from a ZIP file
func (u *Updater) ExtractExecutable(zipPath, destDir string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("failed to open zip file: %w", err)
	}
	defer r.Close()

	var executablePath string
	var foundExecutable bool

	// Expected binary name based on platform
	expectedName := u.GetPlatformBinaryName()

	for _, f := range r.File {
		// Skip directories
		if f.FileInfo().IsDir() {
			continue
		}

		baseName := filepath.Base(f.Name)
		
		// Check if this file matches our expected executable name
		isExecutable := baseName == expectedName
		
		if isExecutable {
			rc, err := f.Open()
			if err != nil {
				return "", fmt.Errorf("failed to open file in zip: %w", err)
			}
			defer rc.Close()

			outPath := filepath.Join(destDir, baseName)
			out, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
			if err != nil {
				return "", fmt.Errorf("failed to create output file: %w", err)
			}
			defer out.Close()

			_, err = io.Copy(out, rc)
			if err != nil {
				return "", fmt.Errorf("failed to extract file: %w", err)
			}

			executablePath = outPath
			foundExecutable = true
			break // Use the first match found
		}
	}

	if !foundExecutable {
		return "", fmt.Errorf("no executable found in zip file (expected: %s)", expectedName)
	}

	return executablePath, nil
}

// GetPlatformBinaryName returns the expected binary name for the current platform
func (u *Updater) GetPlatformBinaryName() string {
	switch runtime.GOOS {
	case "windows":
		return fmt.Sprintf("%s-windows-amd64.exe", u.binaryName)
	case "linux":
		return fmt.Sprintf("%s-linux-amd64", u.binaryName)
	default:
		return u.binaryName
	}
}

// ReplaceCurrentExecutable replaces the current executable with a new one
func (u *Updater) ReplaceCurrentExecutable(newExePath string) error {
	// Get current executable path
	currentExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get current executable path: %w", err)
	}

	// Resolve symlinks
	currentExe, err = filepath.EvalSymlinks(currentExe)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	// Create backup of old executable
	backupPath := currentExe + ".old"
	
	// Remove old backup if it exists
	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		// Log warning but continue - old backup removal is not critical
		fmt.Fprintf(os.Stderr, "Warning: failed to remove old backup: %v\n", err)
	}

	// Rename current executable to backup
	if err := os.Rename(currentExe, backupPath); err != nil {
		return fmt.Errorf("failed to backup current executable: %w", err)
	}

	// Copy new executable to current location
	if err := copyFile(newExePath, currentExe); err != nil {
		// Restore backup on failure
		_ = os.Rename(backupPath, currentExe)
		return fmt.Errorf("failed to replace executable: %w", err)
	}

	// Make it executable
	if err := os.Chmod(currentExe, 0755); err != nil {
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}

	// Remove backup
	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		// Log warning but don't fail the update
		fmt.Fprintf(os.Stderr, "Warning: failed to remove backup file: %v\n", err)
	}

	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}

	return out.Close()
}

// GetCurrentPlatformArtifactName returns the artifact name for the current platform
func GetCurrentPlatformArtifactName() string {
	// Derive binary name from the current executable
	binaryName := deriveBinaryName()
	
	switch runtime.GOOS {
	case "windows":
		return fmt.Sprintf("%s-windows-amd64", binaryName)
	case "linux":
		return fmt.Sprintf("%s-linux-amd64", binaryName)
	default:
		// Unsupported platform - return empty string
		// The caller should handle this appropriately
		return ""
	}
}
