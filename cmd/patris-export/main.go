package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/datasource"
	edgepkg "github.com/atomicdeploy/patris-export/pkg/edge"
	"github.com/atomicdeploy/patris-export/pkg/embedded"
	"github.com/atomicdeploy/patris-export/pkg/filecopy"
	"github.com/atomicdeploy/patris-export/pkg/ipc"
	"github.com/atomicdeploy/patris-export/pkg/licensing"
	"github.com/atomicdeploy/patris-export/pkg/naming"
	"github.com/atomicdeploy/patris-export/pkg/nativeui"
	"github.com/atomicdeploy/patris-export/pkg/oneshot"
	"github.com/atomicdeploy/patris-export/pkg/paradox"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/processmon"
	"github.com/atomicdeploy/patris-export/pkg/recorddiff"
	"github.com/atomicdeploy/patris-export/pkg/recordmap"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
	"github.com/atomicdeploy/patris-export/pkg/recordsink"
	"github.com/atomicdeploy/patris-export/pkg/server"
	"github.com/atomicdeploy/patris-export/pkg/tui"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
	"github.com/atomicdeploy/patris-export/pkg/updater"
	"github.com/atomicdeploy/patris-export/pkg/version"
	"github.com/atomicdeploy/patris-export/pkg/watcher"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	// Global flags
	charMapFile     string
	configFiles     []string
	tempDir         string
	tempStrategy    string
	tempLimitMB     int64
	outputDir       string
	outputFormat    string
	dbFileFlag      string
	viewHTMLOut     string
	viewNoOpen      bool
	viewTitle       string
	watchMode       bool
	verbose         bool
	directAccess    bool
	rtlMode         bool
	rawMode         bool
	mappingFile     string
	exportTable     string
	sqlitePath      string
	sqliteTable     string
	mysqlDSN        string
	mysqlTable      string
	exportBatchSize int
	exportReconcile string
	exportDryRun    bool
	xlsxLanguage    string
	xlsxMode        string
	xlsxZebraRows   bool
	sendURL         string
	sendFormat      string
	sendMode        string
	sendCommand     string
	sendInitial     bool
	sendSecretEnv   string
	sendAttempts    int
	sendBackoff     string
	edgeTargetURL   string
	edgeToken       string
	edgeSourceID    string
	edgeDebounce    string
	edgeOnce        bool
	edgeInitial     bool
	edgeMaxUploadMB int64

	// Color definitions
	successColor = color.New(color.FgGreen, color.Bold)
	errorColor   = color.New(color.FgRed, color.Bold)
	infoColor    = color.New(color.FgCyan)
	warningColor = color.New(color.FgYellow)
)

const (
	defaultRepoOwner = "atomicdeploy"
	defaultRepoName  = "patris-export"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "patris-export [database-file]",
		Short: "📊 Paradox/BDE database file converter for Patris81",
		Long: `
╔═══════════════════════════════════════════════════════════╗
║           🎯 Patris Export - Database Converter           ║
║   Fast and smooth Paradox/BDE database file converter    ║
║         Designed for Patris81 software databases         ║
╚═══════════════════════════════════════════════════════════╝

Reads Paradox .db files and converts them to JSON or CSV format.
Supports Persian/Farsi encoding conversion and file watching.
`,
		Version: version.String(),
		Args:    cobra.MaximumNArgs(1),
		Run:     runRoot,
	}
	rootCmd.Short = "📦 Paradox/BDE database export service for Patris81"
	rootCmd.Long = cliIntro()
	rootCmd.SetVersionTemplate(version.Detailed() + "\n")
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if licenseCommandBypassesEnforcement(cmd) {
			return nil
		}
		return licensing.Enforce(cmd.Context())
	}

	// Global flags
	rootCmd.PersistentFlags().StringArrayVarP(&configFiles, "config", "c", nil, "Path to patris-export config file; repeat to layer JSON/YAML/TOML files")
	rootCmd.PersistentFlags().StringVar(&dbFileFlag, "db", "", "Open a database file in one-shot native viewer mode when no subcommand is used")
	rootCmd.PersistentFlags().StringVarP(&charMapFile, "charmap", "m", "", "Optional custom character mapping file; embedded Patris81 mapping is used by default")
	rootCmd.PersistentFlags().StringVarP(&outputDir, "output", "o", ".", "Output directory for converted files (use '-' for stdout)")
	rootCmd.PersistentFlags().StringVar(&tempDir, "temp-dir", "", "Temp directory for copied/downloaded database files (default: system temp)")
	rootCmd.PersistentFlags().StringVar(&tempStrategy, "temp-strategy", "", "Temp storage strategy: auto, system, or memory (auto prefers /dev/shm on Linux for small files)")
	rootCmd.PersistentFlags().Int64Var(&tempLimitMB, "temp-memory-limit-mb", 0, "Maximum file size in MiB for /dev/shm temp copies when temp strategy allows memory")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
	rootCmd.PersistentFlags().BoolVarP(&directAccess, "direct-access", "d", false, "Access database file directly without temp copy (may conflict with BDE writes)")
	rootCmd.PersistentFlags().BoolVarP(&rtlMode, "rtl", "r", false, "Opt in to RTL logical text conversion for mixed Persian/Latin output")
	rootCmd.PersistentFlags().BoolVar(&rawMode, "raw", false, "Export/serve raw pxlib rows without character conversion, ANBAR compaction, keying, RTL conversion, or mapping")
	rootCmd.PersistentFlags().StringVar(&mappingFile, "mapping", "", "Optional JSON transform mapping file for key/value/table-specific output rules")

	// Convert command
	convertCmd := &cobra.Command{
		Use:   "convert [database-file-or-url]",
		Short: "🔄 Convert a Paradox database file to JSON or CSV",
		Args:  cobra.ExactArgs(1),
		Run:   runConvert,
	}
	convertCmd.Flags().StringVarP(&outputFormat, "format", "f", "json", "Output format (json, csv, xlsx, sqlite, or mysql)")
	convertCmd.Flags().BoolVarP(&watchMode, "watch", "w", false, "Watch file or URL for changes and auto-convert")
	convertCmd.Flags().String("debounce", "1s", "Debounce duration for local files; polling interval for URLs (e.g., 0s, 500ms, 1s, 5m)")
	convertCmd.Flags().StringVar(&exportTable, "table", "", "Destination table name for SQLite/MySQL exports")
	convertCmd.Flags().StringVar(&sqlitePath, "sqlite-path", "", "SQLite database path for --format sqlite")
	convertCmd.Flags().StringVar(&sqliteTable, "sqlite-table", "", "SQLite table name for --format sqlite")
	convertCmd.Flags().StringVar(&mysqlDSN, "mysql-dsn", "", "MySQL DSN for --format mysql (or PATRIS_EXPORT_MYSQL_DSN)")
	convertCmd.Flags().StringVar(&mysqlTable, "mysql-table", "", "MySQL table name for --format mysql")
	convertCmd.Flags().IntVar(&exportBatchSize, "batch-size", 0, "Maximum rows per prepared SQL batch")
	convertCmd.Flags().StringVar(&exportReconcile, "reconciliation", "", "SQL reconciliation mode: upsert_only (safe default) or delete_missing")
	convertCmd.Flags().BoolVar(&exportDryRun, "dry-run", false, "Preview SQL insert/update/delete counts without changing the destination")
	convertCmd.Flags().StringVar(&xlsxLanguage, "xlsx-language", "", "Excel header language: auto, en, or fa (auto follows the configured UI language)")
	convertCmd.Flags().StringVar(&xlsxMode, "xlsx-mode", "", "Excel price output mode: precalculated or formula")
	convertCmd.Flags().BoolVar(&xlsxZebraRows, "xlsx-zebra", true, "Use alternating zebra rows in Excel output")
	convertCmd.Flags().StringVar(&sendURL, "send-url", "", "Webhook/API URL that receives initial and watch update payloads")
	convertCmd.Flags().StringVar(&sendFormat, "send-format", "", "Send update format: json or csv")
	convertCmd.Flags().StringVar(&sendMode, "send-mode", "", "Send update mode: changes or full")
	convertCmd.Flags().StringVar(&sendCommand, "send-command", "", "Command to run for initial/watch updates; payload is written to stdin")
	convertCmd.Flags().BoolVar(&sendInitial, "send-initial", true, "Send a full initial payload before watch updates")
	convertCmd.Flags().StringVar(&sendSecretEnv, "send-product-sync-secret-env", "", "Environment-variable name containing the header-only product-sync secret")
	convertCmd.Flags().IntVar(&sendAttempts, "send-retry-attempts", 0, "Total HTTP delivery attempts (default 1; use retries only with idempotent receivers)")
	convertCmd.Flags().StringVar(&sendBackoff, "send-retry-backoff", "", "Delay between retryable HTTP delivery attempts (for example 1s)")

	// Info command
	infoCmd := &cobra.Command{
		Use:   "info [database-file]",
		Short: "ℹ️  Show information about a Paradox database file",
		Args:  cobra.ExactArgs(1),
		Run:   runInfo,
	}

	// Company command
	companyCmd := &cobra.Command{
		Use:   "company [company.inf]",
		Short: "🏢 Parse company.inf file",
		Args:  cobra.ExactArgs(1),
		Run:   runCompany,
	}

	viewCmd := &cobra.Command{
		Use:   "view [database-file-or-url]",
		Short: "Open a one-shot native database viewer",
		Args:  cobra.MaximumNArgs(1),
		Run:   runView,
	}
	viewCmd.Flags().StringVar(&viewHTMLOut, "html-output", "", "Write the generated one-shot HTML snapshot to this file or directory")
	viewCmd.Flags().BoolVar(&viewNoOpen, "no-open", false, "Generate the HTML snapshot without opening a native viewer window")
	viewCmd.Flags().StringVar(&viewTitle, "title", "", "Native viewer window title")

	// Serve command
	serveCmd := &cobra.Command{
		Use:   "serve [database-file-or-url]",
		Short: "🌐 Start REST API and WebSocket server",
		Args:  cobra.ExactArgs(1),
		Run:   runServe,
	}
	serveCmd.Short = "🌐 Start REST API and WebSocket server"
	serveCmd.Args = cobra.MaximumNArgs(1)
	serveCmd.Flags().StringP("addr", "a", "", "Server address override (e.g., 127.0.0.1:8080 or :8080)")
	serveCmd.Flags().String("host", "", "Host to bind: 127.0.0.1, 0.0.0.0, or an explicit interface")
	serveCmd.Flags().Int("port", 0, "Port to listen on")
	serveCmd.Flags().BoolP("watch", "w", true, "Watch file or URL for changes and broadcast updates")
	serveCmd.Flags().String("debounce", "0s", "Debounce duration for local files; polling interval for URLs (e.g., 0s, 500ms, 1s, 5m)")
	serveCmd.Flags().Bool("http", true, "Enable the HTTP REST/WebSocket/Web UI listener")
	serveCmd.Flags().Bool("ipc", false, "Enable local IPC listener (Windows named pipe, Unix socket)")
	serveCmd.Flags().String("ipc-path", "", "IPC path/name (default: platform-specific patris-export endpoint)")

	stubCmd := &cobra.Command{
		Use:     "stub [database-file]",
		Aliases: []string{"edge"},
		Short:   "📡 Watch a local database and push snapshots to a remote patris-export server",
		Long: `📡 Edge stub mode watches a local Patris .db file and uploads the raw file
to a remote patris-export instance for conversion, transformation, REST, Web UI,
WebSocket, IPC, and notification processing.

This is intended for lightweight edge machines where Patris writes the database,
while a stronger server does the expensive processing and serves clients.`,
		Args: cobra.MaximumNArgs(1),
		Run:  runStub,
	}
	stubCmd.Flags().StringVar(&edgeTargetURL, "target-url", "", "Remote patris-export base URL or /api/edge/upload endpoint")
	stubCmd.Flags().StringVar(&edgeToken, "token", "", "Optional bearer token for the remote edge upload endpoint")
	stubCmd.Flags().StringVar(&edgeSourceID, "source-id", "", "Stable edge/source identifier included with uploads")
	stubCmd.Flags().StringVar(&edgeDebounce, "debounce", "", "Debounce duration before upload after local file changes")
	stubCmd.Flags().BoolVar(&edgeOnce, "once", false, "Upload once and exit instead of watching")
	stubCmd.Flags().BoolVar(&edgeInitial, "initial", true, "Upload the current file once before watching")
	stubCmd.Flags().Int64Var(&edgeMaxUploadMB, "max-upload-mb", 0, "Maximum local file size to upload in MiB")

	ipcCmd := &cobra.Command{
		Use:   "ipc [method] [json-params]",
		Short: "Call a local patris-export IPC endpoint",
		Args:  cobra.RangeArgs(1, 2),
		Run:   runIPC,
	}
	ipcCmd.Flags().String("ipc-path", "", "IPC path/name (default: platform-specific patris-export endpoint)")

	tuiCmd := &cobra.Command{
		Use:   "tui [database-file]",
		Short: "🖥️ Open the terminal dashboard",
		Args:  cobra.MaximumNArgs(1),
		Run:   runTUI,
	}

	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "🚀 Update patris-export from GitHub Actions artifacts",
		Long: `🚀 Update patris-export from GitHub Actions artifacts.

Downloads the latest successful build artifact for your platform and replaces
the current executable. Use --branch to update from a branch other than main.

Examples:
  patris-export update
  patris-export update --branch develop
  patris-export update --api-url http://127.0.0.1:18080
  patris-export update --manifest-url http://127.0.0.1:18080/api/update/manifest

Set GITHUB_TOKEN for private repositories and higher API rate limits.`,
		Run: runUpdate,
	}
	updateCmd.Flags().StringP("branch", "b", "main", "Branch to download from")
	updateCmd.Flags().String("api-url", "", "Update from a Patris Export API base URL using /api/update/manifest")
	updateCmd.Flags().String("manifest-url", "", "Update from an explicit Patris Export executable manifest URL")

	rootCmd.AddCommand(convertCmd, infoCmd, companyCmd, viewCmd, serveCmd, stubCmd, ipcCmd, tuiCmd, updateCmd, newVerifyCommand(), newLicenseCommand())

	if err := rootCmd.Execute(); err != nil {
		nativeui.ShowNativeDependencyError(err)
		errorColor.Fprintf(os.Stderr, "❌ Error: %v\n", err)
		os.Exit(1)
	}
}

func exitWithError(err error, format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	nativeui.ShowNativeDependencyError(err)
	errorColor.Println(message)
	os.Exit(1)
}

func getenvDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func runRoot(cmd *cobra.Command, args []string) {
	dbFile := strings.TrimSpace(dbFileFlag)
	if dbFile == "" && len(args) > 0 {
		dbFile = args[0]
	}
	if dbFile == "" {
		_ = cmd.Help()
		return
	}
	if !isViewableSource(dbFile) {
		errorColor.Printf("Unsupported one-shot viewer source: %s\n", dbFile)
		errorColor.Println("Use a .db or .json file, or run a subcommand such as convert, serve, info, or tui.")
		os.Exit(1)
	}
	runViewSource(cmd, dbFile, "", false, "")
}

func runUpdate(cmd *cobra.Command, args []string) {
	apiURL, err := cmd.Flags().GetString("api-url")
	if err != nil {
		errorColor.Printf("❌ Failed to read 'api-url' flag: %v\n", err)
		os.Exit(1)
	}
	manifestURL, err := cmd.Flags().GetString("manifest-url")
	if err != nil {
		errorColor.Printf("❌ Failed to read 'manifest-url' flag: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(apiURL) != "" || strings.TrimSpace(manifestURL) != "" {
		runAPIUpdate(apiURL, manifestURL)
		return
	}

	branch, err := cmd.Flags().GetString("branch")
	if err != nil {
		errorColor.Printf("❌ Failed to read 'branch' flag: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	successColor.Println("🚀 Patris Export Auto-Update")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	repoOwner := getenvDefault("PATRIS_EXPORT_REPO_OWNER", defaultRepoOwner)
	repoName := getenvDefault("PATRIS_EXPORT_REPO_NAME", defaultRepoName)
	u := updater.NewUpdater(repoOwner, repoName)

	platformName := u.GetCurrentPlatformArtifactName()
	if platformName == "" {
		errorColor.Printf("❌ Auto-update is not supported on %s/%s\n", runtime.GOOS, runtime.GOARCH)
		errorColor.Println("💡 Supported platforms: linux/amd64, windows/amd64, darwin/amd64, darwin/arm64")
		os.Exit(1)
	}

	infoColor.Printf("📦 Repository: %s/%s\n", repoOwner, repoName)
	infoColor.Printf("📦 Current version: %s (built: %s)\n", version.Version, version.BuildDate)
	infoColor.Printf("🌿 Target branch: %s\n", branch)
	infoColor.Printf("💻 Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println()

	if os.Getenv("GITHUB_TOKEN") == "" {
		warningColor.Println("⚠️  GITHUB_TOKEN not set - using anonymous API access (lower rate limits)")
		warningColor.Println("💡 Set GITHUB_TOKEN for private repositories and higher rate limits")
		fmt.Println()
	}

	infoColor.Println("🔍 Searching for latest successful build...")
	run, err := u.GetLatestSuccessfulRun(branch)
	if err != nil {
		errorColor.Printf("❌ Failed to find latest build: %v\n", err)
		os.Exit(1)
	}
	successColor.Printf("✅ Found build #%d from %s\n", run.ID, run.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Println()

	infoColor.Println("📦 Fetching build artifacts...")
	artifacts, err := u.GetArtifactsForRun(run.ID)
	if err != nil {
		errorColor.Printf("❌ Failed to get artifacts: %v\n", err)
		os.Exit(1)
	}

	targetArtifact := u.FindPlatformArtifact(artifacts)
	if targetArtifact == nil {
		errorColor.Printf("❌ No artifact found for platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		errorColor.Println("💡 Available artifacts:")
		for _, artifact := range artifacts {
			fmt.Printf("   • %s\n", artifact.Name)
		}
		os.Exit(1)
	}
	if targetArtifact.Expired {
		errorColor.Println("❌ Artifact has expired - cannot download")
		os.Exit(1)
	}
	successColor.Printf("✅ Found artifact: %s (%.2f MB)\n", targetArtifact.Name, float64(targetArtifact.SizeInBytes)/(1024*1024))
	fmt.Println()

	infoColor.Println("⬇️  Downloading artifact...")
	tempDir, err := os.MkdirTemp("", "patris-update-*")
	if err != nil {
		errorColor.Printf("❌ Failed to create temp directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tempDir)

	zipPath, err := u.DownloadArtifact(targetArtifact, tempDir)
	if err != nil {
		errorColor.Printf("❌ Failed to download artifact: %v\n", err)
		fmt.Println()
		warningColor.Println("💡 GitHub Actions artifacts may require authentication")
		warningColor.Println("   Set GITHUB_TOKEN to a token that can read this repository and its Actions artifacts.")
		warningColor.Println("   Public repositories can use anonymous API access subject to rate limits.")
		warningColor.Println("   Token settings: https://github.com/settings/tokens")
		fmt.Println()
		os.Exit(1)
	}
	successColor.Printf("✅ Downloaded to: %s\n", filepath.Base(zipPath))
	fmt.Println()

	infoColor.Println("📂 Extracting executable...")
	extractedExe, err := u.ExtractExecutable(zipPath, tempDir)
	if err != nil {
		errorColor.Printf("❌ Failed to extract executable: %v\n", err)
		os.Exit(1)
	}
	successColor.Printf("✅ Extracted: %s\n", filepath.Base(extractedExe))
	fmt.Println()

	infoColor.Println("🔄 Replacing current executable...")
	if err := u.ReplaceCurrentExecutable(extractedExe); err != nil {
		errorColor.Printf("❌ Failed to replace executable: %v\n", err)
		errorColor.Println("💡 You may need elevated permissions to update the executable")
		os.Exit(1)
	}

	fmt.Println()
	successColor.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	successColor.Println("✨ Update completed successfully! ✨")
	successColor.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
	infoColor.Println("🎉 Patris Export has been updated to the latest version")
	infoColor.Printf("🌿 Branch: %s\n", branch)
	infoColor.Printf("📅 Build date: %s\n", run.CreatedAt.Format("2006-01-02 15:04:05"))
	infoColor.Println("💡 Run 'patris-export --version' to verify the update")
	fmt.Println()
}

func runAPIUpdate(apiURL, manifestURL string) {
	fmt.Println()
	successColor.Println("🚀 Patris Export API Update")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	manifestURL = strings.TrimSpace(manifestURL)
	if manifestURL == "" {
		var err error
		manifestURL, err = updater.ManifestURLFromAPIBase(apiURL)
		if err != nil {
			errorColor.Printf("❌ Invalid API URL: %v\n", err)
			os.Exit(1)
		}
	}

	infoColor.Printf("🔎 Manifest: %s\n", manifestURL)
	u := updater.NewAPIUpdater()

	manifest, err := u.FetchExecutableManifest(manifestURL)
	if err != nil {
		errorColor.Printf("❌ Failed to fetch manifest: %v\n", err)
		os.Exit(1)
	}
	successColor.Printf("✅ Remote version: %s (commit %s)\n", manifest.Version.Version, shortCLIHash(manifest.Version.Commit))
	infoColor.Printf("💻 Platform: %s\n", manifest.Platform)
	infoColor.Printf("📦 File: %s (%.2f MB)\n", manifest.Filename, float64(manifest.Size)/(1024*1024))
	infoColor.Printf("🔐 SHA-256: %s\n", shortCLIHash(manifest.SHA256))
	infoColor.Printf("🕒 Modified: %s\n", manifest.LastModified.Local().Format("2006-01-02 15:04:05"))
	fmt.Println()

	tempDir, err := os.MkdirTemp("", "patris-api-update-*")
	if err != nil {
		errorColor.Printf("❌ Failed to create temp directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tempDir)

	infoColor.Println("⬇️  Downloading and verifying executable...")
	exePath, err := u.DownloadExecutableFromManifest(manifest, tempDir)
	if err != nil {
		errorColor.Printf("❌ Failed to download executable: %v\n", err)
		os.Exit(1)
	}
	successColor.Printf("✅ Verified download: %s\n", filepath.Base(exePath))
	fmt.Println()

	infoColor.Println("🔄 Replacing current executable...")
	if err := u.ReplaceCurrentExecutable(exePath); err != nil {
		errorColor.Printf("❌ Failed to replace executable: %v\n", err)
		errorColor.Println("💡 You may need elevated permissions to update the executable")
		os.Exit(1)
	}

	fmt.Println()
	successColor.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	successColor.Println("✨ API update completed successfully! ✨")
	successColor.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	infoColor.Println("💡 Restart patris-export, then run 'patris-export --version' to verify.")
	fmt.Println()
}

func shortCLIHash(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "sha256:")
	if value == "" {
		return "unknown"
	}
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func cliIntro() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57")).Padding(0, 1).Render("Patris Export")
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("63"))).
		Headers("Feature", "Status").
		Rows(
			[]string{"Paradox/BDE reader", "pxlib-backed native reader"},
			[]string{"Web UI", "REST API + WebSocket live updates"},
			[]string{"Config", "JSON file + env + CLI overrides"},
			[]string{"Build", version.String()},
		)
	return title + "\n\n" + t.String() + "\n\nReads Patris81 Paradox .db files and serves them as JSON, CSV, REST, WebSocket, Web UI, and TUI views."
}

func effectiveConfig(cmd *cobra.Command) (*appconfig.Manager, appconfig.Config) {
	mgr, err := appconfig.LoadFiles(configFiles)
	if err != nil {
		errorColor.Printf("❌ Failed to load config: %v\n", err)
		os.Exit(1)
	}
	cfg := mgr.Get()
	appconfig.ApplyEnv(&cfg)

	rootFlags := cmd.Root().PersistentFlags()
	if !rootFlags.Changed("charmap") && charMapFile == "" {
		charMapFile = cfg.Database.Charmap
	}
	if !rootFlags.Changed("direct-access") {
		directAccess = cfg.Database.DirectAccess
	}
	if !rootFlags.Changed("rtl") {
		rtlMode = cfg.Database.RTLConversion
	}
	if rootFlags.Changed("raw") {
		cfg.Database.Raw = rawMode
		cfg.Convert.Raw = rawMode
	} else {
		rawMode = cfg.Database.Raw || cfg.Convert.Raw
		cfg.Database.Raw = rawMode
		cfg.Convert.Raw = rawMode
	}
	if rootFlags.Changed("mapping") {
		cfg.Transform.MappingFile = mappingFile
		cfg.Transform.Enabled = strings.TrimSpace(mappingFile) != ""
	} else {
		mappingFile = cfg.Transform.MappingFile
	}
	if strings.TrimSpace(cfg.Transform.MappingFile) != "" {
		loaded, err := recordmap.LoadFile(cfg.Transform.MappingFile)
		if err != nil {
			errorColor.Printf("âŒ Failed to load transform mapping: %v\n", err)
			os.Exit(1)
		}
		cfg.Transform = recordmap.Merge(cfg.Transform, loaded)
	}
	if !rootFlags.Changed("output") {
		outputDir = cfg.Convert.Output
	}
	if !rootFlags.Changed("temp-dir") {
		tempDir = cfg.Runtime.TempDir
	}
	if !rootFlags.Changed("temp-strategy") {
		tempStrategy = cfg.Runtime.TempStrategy
	}
	if !rootFlags.Changed("temp-memory-limit-mb") {
		tempLimitMB = cfg.Runtime.TempMemoryLimitMB
	}
	filecopy.SetTempDir(appconfig.ResolveTempDir(tempDir))
	filecopy.SetTempPolicy(tempStrategy, appconfig.TempMemoryLimitBytes(tempLimitMB))
	converter.SetRTLConversion(rtlMode)
	return mgr, cfg
}

func runTUI(cmd *cobra.Command, args []string) {
	mgr, cfg := effectiveConfig(cmd)
	if len(args) > 0 {
		cfg.Database.Path = args[0]
	}
	if err := tui.Run(cfg, mgr.Path(), version.Current()); err != nil {
		exitWithError(err, "❌ TUI error: %v", err)
	}
}

func runConvert(cmd *cobra.Command, args []string) {
	_, cfg := effectiveConfig(cmd)
	dbFile := args[0]
	if !cmd.Flags().Changed("format") {
		outputFormat = cfg.Convert.Format
	}
	outputFormat = strings.ToLower(strings.TrimSpace(outputFormat))
	if outputFormat == "excel" {
		outputFormat = "xlsx"
	}
	if outputFormat != "json" && outputFormat != "csv" && outputFormat != "xlsx" && outputFormat != "sqlite" && outputFormat != "mysql" {
		errorColor.Printf("❌ Unsupported output format: %s\n", outputFormat)
		errorColor.Println("💡 Use --format json, csv, xlsx, sqlite, or mysql")
		os.Exit(1)
	}
	if !cmd.Flags().Changed("watch") {
		watchMode = cfg.Convert.Watch
	}
	applyConvertFlagOverrides(cmd, &cfg)

	useStdout := outputDir == "-"
	if watchMode && useStdout {
		errorColor.Println("❌ Watch mode cannot be used with stdout output")
		errorColor.Println("💡 Remove -w flag or specify a file/directory for output")
		os.Exit(1)
	}

	var charMap converter.CharMapping
	var err error
	if charMapFile != "" {
		charMap, err = converter.LoadCharMapping(charMapFile)
		if err != nil {
			errorColor.Printf("❌ Failed to load character mapping: %v\n", err)
			os.Exit(1)
		}
		converter.SetDefaultMapping(charMap)
		if !useStdout {
			successColor.Println("✅ Custom character mapping loaded from file")
		}
	} else if !useStdout {
		infoColor.Println("ℹ️  Using embedded character mapping (Patris81 default)")
	}

	if !useStdout && outputFormat != "mysql" && !(outputFormat == "sqlite" && cfg.Export.DryRun) {
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			errorColor.Printf("❌ Failed to create output directory: %v\n", err)
			os.Exit(1)
		}
	}

	if !useStdout {
		displayFileStatus(dbFile)
		checkProcessConflicts(dbFile)
	}

	var convertMu sync.Mutex
	var updateState watchChangeState
	catalogProvider := pricingcatalog.NewProvider(cfg.Canonical.Pricing)
	convertAndSend := func(path, eventType string) {
		convertMu.Lock()
		defer convertMu.Unlock()

		result, err := convertFile(path, charMap, useStdout, cfg, catalogProvider)
		if err != nil {
			return
		}

		changes := updateState.Next(result, eventType, time.Now())
		sendConvertUpdate(cfg, path, result, eventType, changes)
	}

	if watchMode {
		debounceStr, _ := cmd.Flags().GetString("debounce")
		if !cmd.Flags().Changed("debounce") {
			debounceStr = cfg.Convert.Debounce
		}
		debounceDuration := parseDebounceDuration(debounceStr)

		infoColor.Printf("👀 Watching file: %s\n", dbFile)
		infoColor.Println("📝 Press Ctrl+C to stop watching")
		convertAndSend(dbFile, "initial")

		fw, err := watcher.NewFileWatcher()
		if err != nil {
			errorColor.Printf("❌ Failed to create file watcher: %v\n", err)
			os.Exit(1)
		}
		defer fw.Close()

		if filecopy.IsURL(dbFile) {
			pollInterval := debounceDuration
			if pollInterval <= 0 {
				pollInterval = 5 * time.Minute
			}
			infoColor.Printf("🔄 Polling URL every %v\n", pollInterval)
			if err := fw.Poll(dbFile, func(path string) {
				infoColor.Printf("🔄 Remote source changed: %s\n", path)
				convertAndSend(path, "update")
			}, pollInterval); err != nil {
				errorColor.Printf("❌ Failed to poll URL: %v\n", err)
				os.Exit(1)
			}
			select {}
		}

		if err := fw.Watch(dbFile, func(path string) {
			infoColor.Printf("🔄 File changed: %s\n", filepath.Base(path))
			convertAndSend(path, "update")
		}, debounceDuration); err != nil {
			errorColor.Printf("❌ Failed to watch file: %v\n", err)
			os.Exit(1)
		}
		fw.Start()
		select {}
	}

	if _, err := convertFile(dbFile, charMap, useStdout, cfg, catalogProvider); err != nil {
		os.Exit(1)
	}
}

func applyConvertFlagOverrides(cmd *cobra.Command, cfg *appconfig.Config) {
	if cmd.Flags().Changed("table") {
		cfg.Convert.Table = exportTable
	}
	if cmd.Flags().Changed("sqlite-path") {
		cfg.Export.SQLitePath = sqlitePath
	}
	if cmd.Flags().Changed("sqlite-table") {
		cfg.Export.SQLiteTable = sqliteTable
	}
	if cmd.Flags().Changed("mysql-dsn") {
		cfg.Export.MySQLDSN = mysqlDSN
	}
	if cmd.Flags().Changed("mysql-table") {
		cfg.Export.MySQLTable = mysqlTable
	}
	if cmd.Flags().Changed("batch-size") {
		cfg.Export.BatchSize = exportBatchSize
	}
	if cmd.Flags().Changed("reconciliation") {
		cfg.Export.Reconciliation = exportReconcile
	}
	if cmd.Flags().Changed("dry-run") {
		cfg.Export.DryRun = exportDryRun
	}
	if cmd.Flags().Changed("xlsx-language") {
		cfg.Export.XLSXLanguage = xlsxLanguage
	}
	if cmd.Flags().Changed("xlsx-mode") {
		cfg.Export.XLSXMode = xlsxMode
	}
	if cmd.Flags().Changed("xlsx-zebra") {
		cfg.Export.XLSXZebraRows = xlsxZebraRows
	}
	if cmd.Flags().Changed("send-url") {
		cfg.SendUpdates.URL = sendURL
		cfg.SendUpdates.Enabled = strings.TrimSpace(sendURL) != ""
	}
	if cmd.Flags().Changed("send-format") {
		cfg.SendUpdates.Format = sendFormat
	}
	if cmd.Flags().Changed("send-mode") {
		cfg.SendUpdates.Mode = sendMode
	}
	if cmd.Flags().Changed("send-command") {
		cfg.SendUpdates.Command = strings.Fields(sendCommand)
		cfg.SendUpdates.Enabled = len(cfg.SendUpdates.Command) > 0
	}
	if cmd.Flags().Changed("send-initial") {
		cfg.SendUpdates.Initial = sendInitial
	}
	if cmd.Flags().Changed("send-product-sync-secret-env") {
		cfg.SendUpdates.ProductSyncSecretEnv = sendSecretEnv
	}
	if cmd.Flags().Changed("send-retry-attempts") {
		cfg.SendUpdates.RetryAttempts = sendAttempts
	}
	if cmd.Flags().Changed("send-retry-backoff") {
		cfg.SendUpdates.RetryBackoff = sendBackoff
	}
	cfg.SendUpdates = updateout.Normalize(cfg.SendUpdates)
}

func convertFile(dbFile string, charMap converter.CharMapping, useStdout bool, cfg appconfig.Config, catalogProvider pricingcatalog.Provider) (recordpipe.Result, error) {
	if !useStdout {
		infoColor.Printf("📂 Opening database: %s\n", filepath.Base(dbFile))
	}
	ds, err := datasource.NewDataSource(dbFile, charMap, !directAccess && !useStdout)
	if err != nil {
		errorColor.Printf("❌ Failed to create data source: %v\n", err)
		return recordpipe.Result{}, err
	}
	defer ds.Close()
	rawRows, err := ds.GetRawRecords()
	if err != nil {
		nativeui.ShowNativeDependencyError(err)
		errorColor.Printf("❌ Failed to read records: %v\n", err)
		return recordpipe.Result{}, err
	}
	result := recordpipe.Build(rawRows, dbFile, recordpipe.Options{
		Raw:             cfg.Convert.Raw || cfg.Database.Raw,
		Mapping:         cfg.Transform,
		Canonical:       cfg.Canonical,
		CatalogProvider: catalogProvider,
		GeneratedAt:     time.Now(),
	})
	if summary := naming.Summarize(result.Rows); summary.Violations > 0 {
		warningColor.Fprintf(os.Stderr, "Warning: %d naming-convention violation(s) across %d row(s); inspect the warnings field for rule and field IDs.\n", summary.Violations, summary.Rows)
	}
	if !useStdout {
		infoColor.Printf("📊 Found %d records\n", len(result.Rows))
	}
	if err := writeConvertOutput(dbFile, result, useStdout, cfg); err != nil {
		errorColor.Printf("❌ Failed to export: %v\n", err)
		return result, err
	}
	return result, nil
}

func writeConvertOutput(dbFile string, result recordpipe.Result, useStdout bool, cfg appconfig.Config) error {
	if useStdout {
		switch outputFormat {
		case "csv":
			return recordsink.WriteCSV(os.Stdout, result.Rows, result.KeyField)
		case "json":
			return recordsink.WriteJSON(os.Stdout, result.Payload)
		default:
			return fmt.Errorf("stdout output supports json and csv only")
		}
	}

	sourceName := sourceBaseName(dbFile)
	baseName := strings.TrimSuffix(sourceName, filepath.Ext(sourceName))
	if strings.TrimSpace(baseName) == "" {
		baseName = "patris-export"
	}
	tableName := firstNonEmpty(cfg.Convert.Table, cfg.Export.SQLiteTable, cfg.Export.MySQLTable, recordpipe.SourceTableName(dbFile))
	var outputFile string
	var sqlResult *recordsink.SQLResult
	switch outputFormat {
	case "json":
		outputFile = filepath.Join(outputDir, baseName+".json")
		file, err := os.Create(outputFile)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := recordsink.WriteJSON(file, result.Payload); err != nil {
			return err
		}
	case "csv":
		outputFile = filepath.Join(outputDir, baseName+".csv")
		file, err := os.Create(outputFile)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := recordsink.WriteCSV(file, result.Rows, result.KeyField); err != nil {
			return err
		}
	case "xlsx":
		outputFile = filepath.Join(outputDir, baseName+".xlsx")
		language := recordsink.ResolveXLSXLanguage(cfg.Export.XLSXLanguage, cfg.UI.Language)
		xlsxOptions := result.XLSXOptions(sourceName, cfg.UI.RTLTextDirection || language == "fa", recordsink.XLSXPreferences{
			Language:     language,
			Mode:         cfg.Export.XLSXMode,
			ZebraRows:    cfg.Export.XLSXZebraRows,
			ColumnLabels: cfg.ColumnLabels,
		})
		if err := recordsink.WriteXLSX(outputFile, result.Rows, result.KeyField, xlsxOptions); err != nil {
			return err
		}
	case "sqlite":
		outputFile = firstNonEmpty(cfg.Export.SQLitePath, sqlitePath)
		if outputFile == "" {
			outputFile = filepath.Join(outputDir, baseName+".sqlite")
		}
		syncResult, err := recordsink.SyncSQLite(context.Background(), outputFile, recordsink.SQLOptions{
			Table:          tableName,
			KeyField:       result.KeyField,
			Batch:          cfg.Export.BatchSize,
			Reconciliation: recordsink.ReconciliationMode(cfg.Export.Reconciliation),
			DryRun:         cfg.Export.DryRun,
			ProtectedKeys:  quarantinedCodes(result),
		}, result.Rows)
		if err != nil {
			return err
		}
		sqlResult = &syncResult
	case "mysql":
		dsn := firstNonEmpty(cfg.Export.MySQLDSN, mysqlDSN)
		if dsn == "" {
			return fmt.Errorf("mysql export requires --mysql-dsn or PATRIS_EXPORT_MYSQL_DSN")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		syncResult, err := recordsink.SyncSQLWithResult(ctx, recordsink.SQLOptions{
			Driver:         "mysql",
			DSN:            dsn,
			Table:          tableName,
			KeyField:       result.KeyField,
			Batch:          cfg.Export.BatchSize,
			Reconciliation: recordsink.ReconciliationMode(cfg.Export.Reconciliation),
			DryRun:         cfg.Export.DryRun,
			ProtectedKeys:  quarantinedCodes(result),
		}, result.Rows)
		if err != nil {
			return err
		}
		sqlResult = &syncResult
		outputFile = "mysql:" + tableName
	}
	if sqlResult != nil {
		infoColor.Printf(
			"SQL result: inserted=%d updated=%d unchanged=%d deleted=%d failed=%d elapsed=%dms reconciliation=%s dry_run=%t\n",
			sqlResult.Inserted,
			sqlResult.Updated,
			sqlResult.Unchanged,
			sqlResult.Deleted,
			sqlResult.Failed,
			sqlResult.ElapsedMS,
			sqlResult.Reconciliation,
			sqlResult.DryRun,
		)
		if sqlResult.DryRun {
			successColor.Printf("SQL dry-run preview completed for: %s\n", outputFile)
			return nil
		}
	}
	successColor.Printf("✅ Successfully exported to: %s\n", outputFile)
	return nil
}

func quarantinedCodes(result recordpipe.Result) []string {
	if result.Contract == nil {
		return nil
	}
	return append([]string(nil), result.Contract.QuarantinedCodes...)
}

func sendConvertUpdate(cfg appconfig.Config, source string, result recordpipe.Result, eventType string, changes *recorddiff.ChangeSet) {
	if !cfg.SendUpdates.Enabled {
		return
	}
	if eventType == "initial" && !cfg.SendUpdates.Initial {
		return
	}
	timestamp := time.Now().Format(time.RFC3339)
	if changes != nil && changes.Timestamp != "" {
		timestamp = changes.Timestamp
	}
	event := updateout.Event{
		Type:             eventType,
		Timestamp:        timestamp,
		Source:           source,
		Raw:              result.Raw,
		Records:          result.Rows,
		Changes:          changes,
		KeyField:         result.KeyField,
		Contract:         result.SyncEnvelope(changes),
		SnapshotContract: result.SyncEnvelope(nil),
	}
	delivery, err := updateout.DispatchWithResult(context.Background(), cfg.SendUpdates, event)
	if err != nil {
		warningColor.Printf("Send update failed: %v\n", err)
		return
	}
	if delivery.Status != "" {
		infoColor.Printf("Send update delivered: %s\n", delivery.DiagnosticSummary())
	}
}

type watchChangeState struct {
	previousRows []map[string]interface{}
}

func (state *watchChangeState) Next(result recordpipe.Result, eventType string, at time.Time) *recorddiff.ChangeSet {
	var changes *recorddiff.ChangeSet
	if eventType != "initial" {
		changeSet := recorddiff.Between(state.previousRows, result.Rows, result.KeyField, at)
		changeSet.Raw = result.Raw
		changeSet = result.FilterChanges(changeSet)
		changes = &changeSet
	}
	state.previousRows = recordmap.CopyRows(result.Rows)
	return changes
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func runInfo(cmd *cobra.Command, args []string) {
	effectiveConfig(cmd)
	dbFile := args[0]

	// Check for process conflicts
	checkProcessConflicts(dbFile)

	fileToOpen, cleanup, err := prepareFileForReading(dbFile)
	if err != nil {
		errorColor.Printf("❌ %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	infoColor.Printf("🔍 Reading database: %s\n", filepath.Base(dbFile))

	db, err := paradox.Open(fileToOpen)
	if err != nil {
		exitWithError(err, "❌ Failed to open database: %v", err)
	}
	defer db.Close()

	fields, err := db.GetFields()
	if err != nil {
		errorColor.Printf("❌ Failed to get fields: %v\n", err)
		os.Exit(1)
	}

	numRecords := db.GetNumRecords()

	fmt.Println()
	successColor.Println("📋 Database Information")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	infoColor.Printf("📁 File: %s\n", filepath.Base(dbFile))
	infoColor.Printf("📊 Records: %d\n", numRecords)
	infoColor.Printf("📝 Fields: %d\n", len(fields))
	fmt.Println()

	successColor.Println("🗂️  Field Definitions")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	for i, field := range fields {
		fmt.Printf("%2d. %-20s %-12s (size: %d)\n", i+1, field.Name, field.Type, field.Size)
	}
	fmt.Println()
}

func runCompany(cmd *cobra.Command, args []string) {
	effectiveConfig(cmd)
	companyFile := args[0]

	// Load character mapping if provided, otherwise use embedded default
	var charMap converter.CharMapping
	var err error

	if charMapFile != "" {
		charMap, err = converter.LoadCharMapping(charMapFile)
		if err != nil {
			errorColor.Printf("❌ Failed to load character mapping: %v\n", err)
			os.Exit(1)
		}
		converter.SetDefaultMapping(charMap)
		infoColor.Println("ℹ️  Using custom character mapping from file")
	} else {
		infoColor.Println("ℹ️  Using embedded character mapping (Patris81 default)")
	}

	infoColor.Printf("🔍 Reading company info: %s\n", filepath.Base(companyFile))

	info, err := paradox.ReadCompanyInfo(companyFile, converter.Patris2Fa)
	if err != nil {
		errorColor.Printf("❌ Failed to read company info: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	successColor.Println("🏢 Company Information")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("📛 Name:       %s\n", info.Name)
	fmt.Printf("📅 Start Date: %s\n", info.StartDate)
	fmt.Printf("📅 End Date:   %s\n", info.EndDate)
	fmt.Println()
}

func runView(cmd *cobra.Command, args []string) {
	_, cfg := effectiveConfig(cmd)
	dbFile := cfg.Database.Path
	if strings.TrimSpace(dbFileFlag) != "" {
		dbFile = strings.TrimSpace(dbFileFlag)
	}
	if len(args) > 0 {
		dbFile = args[0]
	}
	if strings.TrimSpace(dbFile) == "" {
		errorColor.Println("No database file specified. Pass one to view, set --db, or set database.path in config.")
		os.Exit(1)
	}
	if !isViewableSource(dbFile) {
		errorColor.Printf("Unsupported one-shot viewer source: %s\n", dbFile)
		errorColor.Println("Use a .db or .json file.")
		os.Exit(1)
	}
	runViewSource(cmd, dbFile, viewHTMLOut, viewNoOpen, viewTitle)
}

func runViewSource(cmd *cobra.Command, dbFile, outputPath string, noOpen bool, title string) {
	effectiveConfig(cmd)
	var charMap converter.CharMapping
	if charMapFile != "" {
		var err error
		charMap, err = converter.LoadCharMapping(charMapFile)
		if err != nil {
			errorColor.Printf("Failed to load character mapping: %v\n", err)
			os.Exit(1)
		}
		converter.SetDefaultMapping(charMap)
		successColor.Println("Custom character mapping loaded from file")
	} else {
		infoColor.Println("Using embedded character mapping (Patris81 default)")
	}

	displayFileStatus(dbFile)
	checkProcessConflicts(dbFile)

	if strings.TrimSpace(title) == "" {
		title = fmt.Sprintf("Patris Export - %s", sourceBaseName(dbFile))
	}
	result, err := oneshot.Run(oneshot.Options{
		Path:         dbFile,
		CharMap:      charMap,
		UseTempFile:  !directAccess,
		OutputPath:   outputPath,
		Open:         !noOpen,
		Title:        title,
		BuildVersion: version.Current(),
	})
	if err != nil {
		exitWithError(err, "One-shot viewer failed: %v", err)
	}
	successColor.Printf("Generated one-shot viewer: %s\n", result.HTMLPath)
	infoColor.Printf("Rows: %d  Columns: %d\n", result.Records, result.Fields)
	if result.Opened {
		successColor.Println("Opened native viewer window")
	}
}

// parseDebounceDuration parses and validates a debounce duration string
func parseDebounceDuration(durationStr string) time.Duration {
	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		errorColor.Printf("❌ Invalid debounce duration '%s': %v\n", durationStr, err)
		errorColor.Println("💡 Valid examples: 0s, 500ms, 1s, 5s, 1m")
		os.Exit(1)
	}
	return duration
}

// displayFileStatus shows the file access mode status message
func displayFileStatus(filePath string) {
	if filecopy.IsURL(filePath) {
		infoColor.Printf("🌐 URL source detected: %s\n", filePath)
		infoColor.Println("📋 Will download and read a temporary file copy")
		return
	}
	if directAccess {
		warningColor.Printf("⚠️  Direct file access mode for: %s (may conflict with BDE writes)\n", filepath.Base(filePath))
	} else {
		infoColor.Printf("📋 Using temporary file copy for: %s\n", filepath.Base(filePath))
	}
}

// checkProcessConflicts checks for potential conflicts with running processes
func checkProcessConflicts(dbFile string) {
	if filecopy.IsURL(dbFile) {
		return
	}

	// Check for running patris81.exe processes
	patris81Processes, err := processmon.FindProcessByName("patris81.exe")
	if err != nil {
		warningColor.Printf("⚠️  Could not inspect running patris81.exe processes: %v\n", err)
	} else if len(patris81Processes) > 0 {
		warningColor.Printf("⚠️  Warning: Found %d running patris81.exe process(es)\n", len(patris81Processes))
		for _, p := range patris81Processes {
			infoColor.Printf("   - PID %d: %s\n", p.PID, p.Exe)
		}
		if directAccess {
			warningColor.Println("   ⚠️  Direct access mode may cause conflicts. Consider using --direct-access=false")
		}
	}

	// Check if the file is currently open by any process
	if directAccess {
		fileInfo, err := processmon.FindProcessesWithFile(dbFile)
		if err != nil {
			warningColor.Printf("⚠️  Could not inspect database file locks: %v\n", err)
		} else if len(fileInfo.Processes) > 0 {
			warningColor.Printf("⚠️  Warning: File is currently open by %d process(es)\n", len(fileInfo.Processes))
			for _, p := range fileInfo.Processes {
				infoColor.Printf("   - PID %d: %s\n", p.PID, p.Name)
			}
			warningColor.Println("   ⚠️  This may cause conflicts in direct access mode. Consider using --direct-access=false")
		}
	}
}

// prepareFileForReading prepares a database file for reading, optionally copying to temp
// Returns the file path to open and a cleanup function
func prepareFileForReading(dbFile string, logStatus ...bool) (fileToOpen string, cleanup func(), err error) {
	shouldLog := true
	if len(logStatus) > 0 {
		shouldLog = logStatus[0]
	}

	if filecopy.IsURL(dbFile) {
		if shouldLog {
			infoColor.Printf("🌐 Downloading database from URL: %s\n", dbFile)
		}

		tempFileInfo, err := filecopy.DownloadToTemp(dbFile)
		if err != nil {
			return "", nil, fmt.Errorf("failed to download file to temp: %w", err)
		}

		if shouldLog {
			successColor.Printf("✅ Source URL checksum: %s\n", tempFileInfo.Hash)
		}
		if shouldLog && verbose {
			infoColor.Printf("   Size: %d bytes\n", tempFileInfo.Size)
			infoColor.Printf("   Temp path: %s\n", tempFileInfo.TempPath)
		}

		cleanup = func() {
			filecopy.CleanupTemp(tempFileInfo.TempPath)
		}

		return tempFileInfo.TempPath, cleanup, nil
	}

	if !directAccess {
		if shouldLog {
			infoColor.Printf("📋 Copying database to temp location: %s\n", filepath.Base(dbFile))
		}

		tempFileInfo, err := filecopy.CopyToTemp(dbFile)
		if err != nil {
			return "", nil, fmt.Errorf("failed to copy file to temp: %w", err)
		}

		if shouldLog {
			successColor.Printf("✅ Source file checksum: %s\n", tempFileInfo.Hash)
		}
		if shouldLog && verbose {
			infoColor.Printf("   Size: %d bytes\n", tempFileInfo.Size)
			infoColor.Printf("   Temp path: %s\n", tempFileInfo.TempPath)
		}

		cleanup = func() {
			filecopy.CleanupTemp(tempFileInfo.TempPath)
		}

		return tempFileInfo.TempPath, cleanup, nil
	}

	// Direct access mode - no cleanup needed
	return dbFile, func() {}, nil
}

func sourceBaseName(path string) string {
	if filecopy.IsURL(path) {
		if u, err := url.Parse(path); err == nil {
			if base := filepath.Base(u.Path); base != "." && base != "/" && base != "" {
				return base
			}
			return u.Host
		}
	}
	return filepath.Base(path)
}

func isViewableSource(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if filecopy.IsURL(path) {
		if u, err := url.Parse(path); err == nil {
			ext = strings.ToLower(filepath.Ext(u.Path))
		}
	}
	return ext == ".db" || ext == ".json"
}

func runStub(cmd *cobra.Command, args []string) {
	_, cfg := effectiveConfig(cmd)
	dbFile := cfg.Database.Path
	if strings.TrimSpace(dbFileFlag) != "" {
		dbFile = strings.TrimSpace(dbFileFlag)
	}
	if len(args) > 0 {
		dbFile = args[0]
	}
	if strings.TrimSpace(dbFile) == "" {
		errorColor.Println("No database file specified. Pass one to stub, set --db, or set database.path in config.")
		os.Exit(1)
	}
	if filecopy.IsURL(dbFile) {
		errorColor.Println("Edge stub mode watches local files only. Use a local .db path.")
		os.Exit(1)
	}

	targetURL := edgeTargetURL
	if !cmd.Flags().Changed("target-url") {
		targetURL = cfg.Edge.TargetURL
	}
	token := edgeToken
	if !cmd.Flags().Changed("token") {
		token = cfg.Edge.Token
	}
	sourceID := edgeSourceID
	if !cmd.Flags().Changed("source-id") {
		sourceID = cfg.Edge.SourceID
	}
	debounceStr := edgeDebounce
	if !cmd.Flags().Changed("debounce") {
		debounceStr = cfg.Edge.Debounce
	}
	maxUploadMB := edgeMaxUploadMB
	if !cmd.Flags().Changed("max-upload-mb") {
		maxUploadMB = cfg.Edge.MaxUploadMB
	}
	if maxUploadMB <= 0 {
		maxUploadMB = 512
	}
	debounceDuration := parseDebounceDuration(debounceStr)

	client := edgepkg.Client{
		TargetURL: targetURL,
		Token:     token,
		SourceID:  sourceID,
		MaxBytes:  maxUploadMB * 1024 * 1024,
	}
	if _, err := edgepkg.UploadURL(targetURL); err != nil {
		errorColor.Printf("Invalid edge target URL: %v\n", err)
		os.Exit(1)
	}

	displayFileStatus(dbFile)
	checkProcessConflicts(dbFile)

	upload := func(path string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		infoColor.Printf("📤 Uploading %s to %s\n", filepath.Base(path), targetURL)
		result, err := client.UploadFile(ctx, path)
		if err != nil {
			errorColor.Printf("Edge upload failed: %v\n", err)
			return false
		}
		successColor.Printf("✅ Edge upload accepted: %d records, %d bytes, hash=%s\n", result.Records, result.Size, result.Hash)
		return true
	}

	if edgeInitial || edgeOnce {
		if ok := upload(dbFile); !ok && edgeOnce {
			os.Exit(1)
		}
	}
	if edgeOnce {
		return
	}

	fw, err := watcher.NewFileWatcher()
	if err != nil {
		errorColor.Printf("Failed to create file watcher: %v\n", err)
		os.Exit(1)
	}
	defer fw.Close()

	if err := fw.Watch(dbFile, func(path string) {
		infoColor.Printf("🔄 Local database changed: %s\n", filepath.Base(path))
		upload(path)
	}, debounceDuration); err != nil {
		errorColor.Printf("Failed to watch database file: %v\n", err)
		os.Exit(1)
	}
	fw.Start()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	successColor.Printf("📡 Edge stub watching %s\n", filepath.Base(dbFile))
	infoColor.Printf("🎯 Remote target: %s\n", targetURL)
	infoColor.Printf("⏱️ Debounce: %s | Max upload: %d MiB\n", debounceDuration, maxUploadMB)
	infoColor.Println("Press Ctrl+C to stop the edge stub")
	<-ctx.Done()
}

func runIPC(cmd *cobra.Command, args []string) {
	path, _ := cmd.Flags().GetString("ipc-path")
	conn, err := ipc.Dial(context.Background(), path)
	if err != nil {
		errorColor.Printf("Failed to connect to IPC endpoint: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	req := ipc.Request{
		ID:     1,
		Method: args[0],
	}
	if len(args) > 1 {
		params := []byte(args[1])
		if !json.Valid(params) {
			errorColor.Println("IPC params must be valid JSON")
			os.Exit(1)
		}
		req.Params = json.RawMessage(params)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		errorColor.Printf("Failed to write IPC request: %v\n", err)
		os.Exit(1)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
		if !strings.EqualFold(args[0], "subscribe") {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		errorColor.Printf("Failed to read IPC response: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	// Set up logging
	log.SetFlags(0)
	log.SetOutput(os.Stdout)
}

func runServe(cmd *cobra.Command, args []string) {
	configManager, cfg := effectiveConfig(cmd)
	if len(args) > 0 {
		cfg.Database.Path = args[0]
	}
	if cfg.Database.Path == "" {
		errorColor.Println("❌ No database file specified. Pass one to serve or set database.path in the config.")
		os.Exit(1)
	}
	dbFile := cfg.Database.Path
	addr, _ := cmd.Flags().GetString("addr")
	watchFile, _ := cmd.Flags().GetBool("watch")
	debounceStr, _ := cmd.Flags().GetString("debounce")
	host, _ := cmd.Flags().GetString("host")
	port, _ := cmd.Flags().GetInt("port")
	httpEnabled, _ := cmd.Flags().GetBool("http")
	ipcEnabled, _ := cmd.Flags().GetBool("ipc")
	ipcPath, _ := cmd.Flags().GetString("ipc-path")
	if cmd.Flags().Changed("addr") && addr != "" {
		appconfig.ApplyAddr(&cfg, addr)
	}
	if cmd.Flags().Changed("host") && host != "" {
		cfg.Server.Host = host
	}
	if cmd.Flags().Changed("port") && port > 0 {
		cfg.Server.Port = port
	}
	if !cmd.Flags().Changed("watch") {
		watchFile = cfg.Server.Watch
	}
	if !cmd.Flags().Changed("debounce") {
		debounceStr = cfg.Server.Debounce
	}
	if !cmd.Flags().Changed("http") {
		httpEnabled = cfg.Server.HTTP
	}
	if cmd.Flags().Changed("ipc") {
		cfg.Server.IPC.Enabled = ipcEnabled
	}
	if cmd.Flags().Changed("ipc-path") && ipcPath != "" {
		cfg.Server.IPC.Path = ipcPath
	}
	if !httpEnabled && !cfg.Server.IPC.Enabled {
		errorColor.Println("At least one transport must be enabled. Use --http=true or --ipc.")
		os.Exit(1)
	}
	addr = cfg.Addr()
	if err := configManager.Replace(cfg); err != nil {
		errorColor.Printf("âŒ Failed to save effective config: %v\n", err)
		os.Exit(1)
	}

	// Load character mapping if provided, otherwise use embedded default
	var charMap converter.CharMapping
	var err error

	if charMapFile != "" {
		charMap, err = converter.LoadCharMapping(charMapFile)
		if err != nil {
			errorColor.Printf("❌ Failed to load character mapping: %v\n", err)
			os.Exit(1)
		}
		converter.SetDefaultMapping(charMap)
		successColor.Println("✅ Custom character mapping loaded from file")
	} else {
		infoColor.Println("ℹ️  Using embedded character mapping (Patris81 default)")
	}

	// Create server
	srv, err := server.NewServerWithOptions(dbFile, charMap, server.Options{
		Config:  configManager,
		Version: version.Current(),
	}, !directAccess)
	if err != nil {
		errorColor.Printf("❌ Failed to create server: %v\n", err)
		os.Exit(1)
	}
	defer srv.Close()

	// Display temp file setting
	displayFileStatus(dbFile)

	// Check for process conflicts
	checkProcessConflicts(dbFile)

	// Start file watching if enabled
	if watchFile {
		// Parse debounce duration
		debounceDuration := parseDebounceDuration(debounceStr)

		if err := srv.StartWatching(debounceDuration); err != nil {
			errorColor.Printf("❌ Failed to start file watching: %v\n", err)
			os.Exit(1)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if cfg.Server.IPC.Enabled {
		ipcServer := ipc.NewServer(cfg.Server.IPC.Path, embedded.NewServerHandler(srv))
		go func() {
			if err := ipcServer.Serve(ctx); err != nil {
				errorColor.Printf("IPC server error: %v\n", err)
				stop()
			}
		}()
		successColor.Printf("IPC endpoint running at %s\n", ipcServer.Path())
	}

	if !httpEnabled {
		infoColor.Printf("⚙️ Config: %s\n", configManager.Path())
		infoColor.Println("Press Ctrl+C to stop the IPC server")
		<-ctx.Done()
		return
	}

	// Start server
	successColor.Printf("🌐 Server running at http://%s\n", addr)
	infoColor.Printf("⚙️ Config: %s\n", configManager.Path())
	infoColor.Println("📝 Press Ctrl+C to stop the server")

	if err := srv.Start(addr); err != nil {
		errorColor.Printf("❌ Server error: %v\n", err)
		os.Exit(1)
	}
}
