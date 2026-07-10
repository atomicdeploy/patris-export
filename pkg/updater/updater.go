package updater

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	githubAPIURL       = "https://api.github.com"
	githubAPIVersion   = "2022-11-28"
	defaultHTTPTimeout = 5 * time.Minute
)

// Updater handles auto-update functionality
type Updater struct {
	apiToken   string
	client     *http.Client
	binaryName string // Base name of the binary (e.g., "patris-export")
	repoOwner  string // GitHub repository owner
	repoName   string // GitHub repository name
	apiBaseURL string // GitHub API base URL; overridden by tests
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
	Digest             string    `json:"digest,omitempty"`
	Expired            bool      `json:"expired"`
	CreatedAt          time.Time `json:"created_at"`
}

// ArtifactsResponse represents the API response for artifacts
type ArtifactsResponse struct {
	TotalCount int        `json:"total_count"`
	Artifacts  []Artifact `json:"artifacts"`
}

// ExecutableManifest describes a directly downloadable patris-export binary.
// It is served by the app itself and consumed by API-based self-updates.
type ExecutableManifest struct {
	Name         string       `json:"name"`
	Filename     string       `json:"filename"`
	Version      VersionShape `json:"version"`
	Platform     string       `json:"platform"`
	Size         int64        `json:"size"`
	SHA256       string       `json:"sha256"`
	LastModified time.Time    `json:"last_modified"`
	DownloadURL  string       `json:"download_url"`
	GeneratedAt  time.Time    `json:"generated_at"`
}

// VersionShape mirrors pkg/version.Info without making update metadata depend
// on that package.
type VersionShape struct {
	Version   string `json:"version"`
	BuildDate string `json:"build_date"`
	Commit    string `json:"commit"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// NewUpdater creates a new updater instance
func NewUpdater(repoOwner, repoName string) *Updater {
	// Derive binary name from the current executable
	binaryName := deriveBinaryName(repoName)

	return &Updater{
		apiToken:   os.Getenv("GITHUB_TOKEN"),
		binaryName: binaryName,
		repoOwner:  repoOwner,
		repoName:   repoName,
		apiBaseURL: githubAPIURL,
		client: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// NewAPIUpdater creates an updater for manifest-based HTTP APIs. It
// intentionally does not reuse GITHUB_TOKEN so repository credentials are not
// sent to arbitrary update servers. Set PATRIS_UPDATE_TOKEN when the remote
// manifest/executable endpoints require bearer authentication.
func NewAPIUpdater() *Updater {
	u := NewUpdater("", "patris-export")
	u.apiToken = strings.TrimSpace(os.Getenv("PATRIS_UPDATE_TOKEN"))
	return u
}

// CurrentPlatform returns the runtime platform string used in manifests.
func CurrentPlatform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// FileSHA256 calculates a hex-encoded SHA-256 digest for path.
func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file for sha256: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to calculate sha256: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// deriveBinaryName derives the base binary name from the current executable
func deriveBinaryName(fallbackName string) string {
	exe, err := os.Executable()
	if err != nil {
		// Fallback to provided name if we can't get the executable
		return fallbackName
	}

	// Get the base name without path
	baseName := filepath.Base(exe)

	return normalizeBinaryName(baseName, fallbackName)
}

func normalizeBinaryName(baseName, fallbackName string) string {
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

	// If we ended up with an empty string, use fallback name
	if baseName == "" {
		return fallbackName
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
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)

	// Add token if available
	if u.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+u.apiToken)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("API request failed with status %d and failed to read error body: %w", resp.StatusCode, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("API request failed with status %d and failed to close error body: %w", resp.StatusCode, closeErr)
		}
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return resp, nil
}

// GetLatestSuccessfulRun gets the latest successful workflow run for a branch
func (u *Updater) GetLatestSuccessfulRun(branch string) (*WorkflowRun, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/actions/runs?branch=%s&status=success&per_page=100",
		u.apiBaseURL, u.repoOwner, u.repoName, branch)

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
		u.apiBaseURL, u.repoOwner, u.repoName, runID)

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

// DownloadArtifact downloads an artifact, verifies its GitHub-provided digest when
// available, and returns the path to the downloaded file.
func (u *Updater) DownloadArtifact(artifact *Artifact, destDir string) (destPath string, err error) {
	// Ensure destination directory exists
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create destination directory: %w", err)
	}

	destPath = filepath.Join(destDir, artifact.Name+".zip")

	// Create the file
	out, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}

	// Download the artifact
	req, err := http.NewRequest("GET", artifact.ArchiveDownloadURL, nil)
	if err != nil {
		_ = out.Close()
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Add GitHub API headers
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)

	if u.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+u.apiToken)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		_ = out.Close()
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close download response body: %w", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = out.Close()
		return "", fmt.Errorf("download failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Write to file
	hash := sha256.New()
	if _, err = io.Copy(io.MultiWriter(out, hash), resp.Body); err != nil {
		_ = out.Close()
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	if err = out.Close(); err != nil {
		return "", fmt.Errorf("failed to close downloaded file: %w", err)
	}

	if err = verifyArtifactDigest(artifact.Digest, hash.Sum(nil)); err != nil {
		return "", err
	}

	return destPath, nil
}

func verifyArtifactDigest(expected string, actual []byte) error {
	if expected == "" {
		return nil
	}

	algorithm, encoded, ok := strings.Cut(expected, ":")
	if !ok {
		return fmt.Errorf("artifact digest %q is not in algorithm:hex format", expected)
	}
	if !strings.EqualFold(algorithm, "sha256") {
		return fmt.Errorf("unsupported artifact digest algorithm %q", algorithm)
	}

	actualHex := hex.EncodeToString(actual)
	if !strings.EqualFold(encoded, actualHex) {
		return fmt.Errorf("artifact digest mismatch: expected %s, got sha256:%s", expected, actualHex)
	}

	return nil
}

// ManifestURLFromAPIBase returns the standard manifest endpoint for a Patris
// Export API base URL.
func ManifestURLFromAPIBase(apiBase string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(apiBase))
	if err != nil {
		return "", fmt.Errorf("failed to parse API URL: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("API URL must include scheme and host")
	}
	return base.JoinPath("api", "update", "manifest").String(), nil
}

// FetchExecutableManifest downloads and validates a remote executable manifest.
func (u *Updater) FetchExecutableManifest(manifestURL string) (*ExecutableManifest, error) {
	manifestURL = strings.TrimSpace(manifestURL)
	if manifestURL == "" {
		return nil, fmt.Errorf("manifest URL is required")
	}
	req, err := http.NewRequest(http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create manifest request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if u.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+u.apiToken)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("manifest request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("manifest request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var manifest ExecutableManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("failed to decode manifest: %w", err)
	}
	if err := validateManifest(&manifest); err != nil {
		return nil, err
	}

	downloadURL, err := resolveManifestURL(manifestURL, manifest.DownloadURL)
	if err != nil {
		return nil, err
	}
	manifest.DownloadURL = downloadURL
	return &manifest, nil
}

func validateManifest(manifest *ExecutableManifest) error {
	if manifest == nil {
		return fmt.Errorf("manifest is nil")
	}
	if strings.TrimSpace(manifest.Filename) == "" {
		return fmt.Errorf("manifest filename is required")
	}
	if strings.TrimSpace(manifest.Platform) == "" {
		return fmt.Errorf("manifest platform is required")
	}
	if manifest.Platform != CurrentPlatform() {
		return fmt.Errorf("manifest platform %q does not match current platform %q", manifest.Platform, CurrentPlatform())
	}
	if manifest.Size <= 0 {
		return fmt.Errorf("manifest size must be positive")
	}
	if _, err := decodeSHA256(manifest.SHA256); err != nil {
		return fmt.Errorf("manifest sha256 is invalid: %w", err)
	}
	if strings.TrimSpace(manifest.DownloadURL) == "" {
		return fmt.Errorf("manifest download_url is required")
	}
	return nil
}

func decodeSHA256(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "sha256:")
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, err
	}
	if len(decoded) != sha256.Size {
		return nil, fmt.Errorf("expected %d bytes, got %d", sha256.Size, len(decoded))
	}
	return decoded, nil
}

func resolveManifestURL(manifestURL, rawDownloadURL string) (string, error) {
	base, err := url.Parse(manifestURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse manifest URL: %w", err)
	}
	ref, err := url.Parse(strings.TrimSpace(rawDownloadURL))
	if err != nil {
		return "", fmt.Errorf("failed to parse download URL: %w", err)
	}
	return base.ResolveReference(ref).String(), nil
}

// DownloadExecutableFromManifest streams the manifest executable to destDir and
// verifies the downloaded size and SHA-256 digest before returning the path.
func (u *Updater) DownloadExecutableFromManifest(manifest *ExecutableManifest, destDir string) (destPath string, err error) {
	if err := validateManifest(manifest); err != nil {
		return "", err
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create destination directory: %w", err)
	}

	destPath = filepath.Join(destDir, filepath.Base(manifest.Filename))
	tmpPath := destPath + ".download"
	out, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create downloaded executable: %w", err)
	}
	defer func() {
		if err != nil {
			_ = out.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	req, err := http.NewRequest(http.MethodGet, manifest.DownloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create executable request: %w", err)
	}
	req.Header.Set("Accept", "application/octet-stream")
	if u.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+u.apiToken)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("executable download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("executable download failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if resp.ContentLength >= 0 && resp.ContentLength != manifest.Size {
		return "", fmt.Errorf("download content length mismatch: manifest=%d response=%d", manifest.Size, resp.ContentLength)
	}
	if headerHash := strings.TrimSpace(resp.Header.Get("X-Checksum-SHA256")); headerHash != "" && !strings.EqualFold(strings.TrimPrefix(headerHash, "sha256:"), strings.TrimPrefix(manifest.SHA256, "sha256:")) {
		return "", fmt.Errorf("download checksum header mismatch: manifest=%s response=%s", manifest.SHA256, headerHash)
	}

	hash := sha256.New()
	written, err := io.CopyBuffer(io.MultiWriter(out, hash), resp.Body, make([]byte, 1024*1024))
	if err != nil {
		return "", fmt.Errorf("failed to write downloaded executable: %w", err)
	}
	if err := out.Close(); err != nil {
		return "", fmt.Errorf("failed to close downloaded executable: %w", err)
	}
	if written != manifest.Size {
		return "", fmt.Errorf("download size mismatch: manifest=%d downloaded=%d", manifest.Size, written)
	}
	actualHex := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(strings.TrimPrefix(manifest.SHA256, "sha256:"), actualHex) {
		return "", fmt.Errorf("download sha256 mismatch: manifest=%s downloaded=sha256:%s", manifest.SHA256, actualHex)
	}
	if !manifest.LastModified.IsZero() {
		_ = os.Chtimes(tmpPath, time.Now(), manifest.LastModified)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", fmt.Errorf("failed to finalize downloaded executable: %w", err)
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

		if !isSafeZipPath(f.Name) {
			continue
		}

		baseName := path.Base(strings.ReplaceAll(f.Name, "\\", "/"))

		// Check if this file matches our expected executable name
		isExecutable := baseName == expectedName

		if isExecutable {
			// Extract this specific file
			executablePath, err = extractSingleFile(f, destDir, baseName)
			if err != nil {
				return "", err
			}
			foundExecutable = true
			break // Use the first match found
		}
	}

	if !foundExecutable {
		return "", fmt.Errorf("no executable found in zip file (expected: %s)", expectedName)
	}

	return executablePath, nil
}

func isSafeZipPath(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}

	normalized := strings.ReplaceAll(name, "\\", "/")
	if path.IsAbs(normalized) || filepath.IsAbs(name) {
		return false
	}

	cleaned := path.Clean(normalized)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return false
	}

	for _, part := range strings.Split(cleaned, "/") {
		if part == ".." {
			return false
		}
	}

	return true
}

// extractSingleFile extracts a single file from a ZIP archive
// This is a helper function to ensure proper resource cleanup
func extractSingleFile(f *zip.File, destDir, baseName string) (string, error) {
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

	_, err = io.Copy(out, rc)
	if err != nil {
		// Attempt to close the file even if the copy failed; prefer the copy error.
		_ = out.Close()
		return "", fmt.Errorf("failed to extract file: %w", err)
	}

	if err := out.Close(); err != nil {
		return "", fmt.Errorf("failed to close output file: %w", err)
	}

	return outPath, nil
}

// GetPlatformBinaryName returns the expected binary name for the current platform
func (u *Updater) GetPlatformBinaryName() string {
	switch runtime.GOOS {
	case "windows":
		return fmt.Sprintf("%s-windows-amd64.exe", u.binaryName)
	case "linux":
		return fmt.Sprintf("%s-linux-amd64", u.binaryName)
	case "darwin":
		// macOS support - amd64 (Intel) and arm64 (Apple Silicon)
		if runtime.GOARCH == "arm64" {
			return fmt.Sprintf("%s-darwin-arm64", u.binaryName)
		}
		return fmt.Sprintf("%s-darwin-amd64", u.binaryName)
	default:
		// For unsupported platforms, return just the binary name
		// This will likely fail, but provides a reasonable fallback
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

	if runtime.GOOS == "windows" {
		return scheduleWindowsReplacement(currentExe, newExePath)
	}

	return replaceExecutable(currentExe, newExePath)
}

func replaceExecutable(currentExe, newExePath string) error {
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
		if restoreErr := os.Rename(backupPath, currentExe); restoreErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to restore backup after copy failure: %v\n", restoreErr)
		}
		return fmt.Errorf("failed to replace executable: %w", err)
	}

	// Make it executable
	if err := os.Chmod(currentExe, 0755); err != nil {
		if restoreErr := restoreBackup(currentExe, backupPath); restoreErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to restore backup after chmod failure: %v\n", restoreErr)
		}
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}

	// Remove backup
	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		// Log warning but don't fail the update
		fmt.Fprintf(os.Stderr, "Warning: failed to remove backup file: %v\n", err)
	}

	return nil
}

func restoreBackup(currentExe, backupPath string) error {
	if err := os.Remove(currentExe); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(backupPath, currentExe)
}

func scheduleWindowsReplacement(currentExe, newExePath string) error {
	scriptPath := currentExe + ".update.cmd"
	backupPath := currentExe + ".old"
	stagedPath := currentExe + ".new"

	if err := copyFile(newExePath, stagedPath); err != nil {
		return fmt.Errorf("failed to stage replacement executable: %w", err)
	}

	script := fmt.Sprintf(`@echo off
setlocal
set "NEW_EXE=%s"
set "CURRENT_EXE=%s"
set "BACKUP_EXE=%s"
set "SELF=%%~f0"
ping 127.0.0.1 -n 3 >NUL
if exist "%%BACKUP_EXE%%" del /f /q "%%BACKUP_EXE%%" >NUL 2>NUL
if exist "%%CURRENT_EXE%%" ren "%%CURRENT_EXE%%" "%s" >NUL 2>NUL
copy /y "%%NEW_EXE%%" "%%CURRENT_EXE%%" >NUL
if errorlevel 1 (
  if exist "%%BACKUP_EXE%%" ren "%%BACKUP_EXE%%" "%s" >NUL 2>NUL
  exit /b 1
)
del /f /q "%%BACKUP_EXE%%" >NUL 2>NUL
del /f /q "%%NEW_EXE%%" >NUL 2>NUL
del /f /q "%%SELF%%" >NUL 2>NUL
`, stagedPath, currentExe, backupPath, filepath.Base(backupPath), filepath.Base(currentExe))

	if err := os.WriteFile(scriptPath, []byte(script), 0600); err != nil {
		return fmt.Errorf("failed to write deferred Windows update script: %w", err)
	}

	cmd := exec.Command("cmd", "/C", "start", "", "/MIN", scriptPath)
	if err := cmd.Start(); err != nil {
		_ = os.Remove(stagedPath)
		return fmt.Errorf("failed to start deferred Windows update script: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Windows update scheduled; the executable will be replaced after this process exits.\n")
	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := in.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err = out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}

	return nil
}

// GetCurrentPlatformArtifactName returns the artifact name for the current platform
func (u *Updater) GetCurrentPlatformArtifactName() string {
	switch runtime.GOOS {
	case "windows":
		return fmt.Sprintf("%s-windows-amd64", u.binaryName)
	case "linux":
		return fmt.Sprintf("%s-linux-amd64", u.binaryName)
	case "darwin":
		// macOS support - amd64 (Intel) and arm64 (Apple Silicon)
		if runtime.GOARCH == "arm64" {
			return fmt.Sprintf("%s-darwin-arm64", u.binaryName)
		}
		return fmt.Sprintf("%s-darwin-amd64", u.binaryName)
	default:
		// Unsupported platform - return empty string
		// The caller should handle this appropriately
		return ""
	}
}

// FindPlatformArtifact finds the best matching artifact for the current platform
// from a list of available artifacts. It handles different naming conventions
// (e.g., windows-mingw, windows-mingw-cross) and returns the best match.
func (u *Updater) FindPlatformArtifact(artifacts []Artifact) *Artifact {
	// Get the basic platform identifier
	var platformIdentifier string
	switch runtime.GOOS {
	case "windows":
		platformIdentifier = fmt.Sprintf("%s-windows", u.binaryName)
	case "linux":
		platformIdentifier = fmt.Sprintf("%s-linux", u.binaryName)
	case "darwin":
		if runtime.GOARCH == "arm64" {
			platformIdentifier = fmt.Sprintf("%s-darwin-arm64", u.binaryName)
		} else {
			platformIdentifier = fmt.Sprintf("%s-darwin-amd64", u.binaryName)
		}
	default:
		return nil
	}

	// Try exact match first
	exactMatch := u.GetCurrentPlatformArtifactName()
	for i := range artifacts {
		if artifacts[i].Name == exactMatch {
			return &artifacts[i]
		}
	}

	// For Windows, try flexible matching to handle mingw variants
	// This allows finding "patris-export-windows-mingw-amd64" or
	// "patris-export-windows-mingw-cross-amd64" when looking for
	// "patris-export-windows-amd64"
	if runtime.GOOS == "windows" {
		for i := range artifacts {
			name := artifacts[i].Name
			// Match artifacts that start with the platform identifier and contain "amd64"
			if strings.HasPrefix(name, platformIdentifier) && strings.Contains(name, "amd64") {
				return &artifacts[i]
			}
		}
	}

	// For other platforms, try prefix matching
	for i := range artifacts {
		if strings.HasPrefix(artifacts[i].Name, platformIdentifier) {
			return &artifacts[i]
		}
	}

	return nil
}

// DeriveRepoInfoFromModule attempts to derive repository owner and name from go.mod.
// It looks for go.mod starting from the current working directory (os.Getwd()) and then parent directories,
// i.e., relative to where the process is run, not necessarily the executable's directory.
// Returns (owner, name, error)
//
// TODO: Once a settings/configuration store (using YAML format) is implemented,
// repository information should be stored there for easy access and updates.
func DeriveRepoInfoFromModule() (string, string, error) {
	// First, try environment variables
	repoOwner := os.Getenv("PATRIS_REPO_OWNER")
	repoName := os.Getenv("PATRIS_REPO_NAME")

	if repoOwner != "" && repoName != "" {
		return repoOwner, repoName, nil
	}

	// Fallback: try to find go.mod file
	// Start from current directory and walk up
	dir, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("failed to get working directory: %w", err)
	}

	// Try to find go.mod file
	for {
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			// Found go.mod, parse it
			owner, name, err := parseGoMod(goModPath)
			if err != nil {
				return "", "", err
			}
			return owner, name, nil
		}

		// Move to parent directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root without finding go.mod
			break
		}
		dir = parent
	}

	return "", "", fmt.Errorf("repository info not found: set PATRIS_REPO_OWNER and PATRIS_REPO_NAME environment variables, or run from within the project directory")
}

// parseGoMod parses go.mod file and extracts GitHub repository owner and name
func parseGoMod(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", fmt.Errorf("failed to open go.mod: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Look for module line
		if strings.HasPrefix(line, "module ") {
			modulePath := strings.TrimPrefix(line, "module ")
			modulePath = strings.TrimSpace(modulePath)

			// Extract owner and repo from module path
			// Expected format: github.com/owner/repo
			parts := strings.Split(modulePath, "/")
			if len(parts) >= 3 && parts[0] == "github.com" {
				return parts[1], parts[2], nil
			}

			return "", "", fmt.Errorf("module path '%s' is not a GitHub module", modulePath)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", "", fmt.Errorf("error reading go.mod: %w", err)
	}

	return "", "", fmt.Errorf("module declaration not found in go.mod")
}
