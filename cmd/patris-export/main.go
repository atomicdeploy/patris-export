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
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/embedded"
	"github.com/atomicdeploy/patris-export/pkg/filecopy"
	"github.com/atomicdeploy/patris-export/pkg/ipc"
	"github.com/atomicdeploy/patris-export/pkg/paradox"
	"github.com/atomicdeploy/patris-export/pkg/processmon"
	"github.com/atomicdeploy/patris-export/pkg/server"
	"github.com/atomicdeploy/patris-export/pkg/tui"
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
	charMapFile  string
	configFiles  []string
	tempDir      string
	outputDir    string
	outputFormat string
	watchMode    bool
	verbose      bool
	directAccess bool
	rtlMode      bool

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
		Use:   "patris-export",
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
	}
	rootCmd.Short = "📦 Paradox/BDE database export service for Patris81"
	rootCmd.Long = cliIntro()
	rootCmd.SetVersionTemplate(version.Detailed() + "\n")

	// Global flags
	rootCmd.PersistentFlags().StringArrayVar(&configFiles, "config", nil, "Path to patris-export config file; repeat to layer JSON/YAML/TOML files")
	rootCmd.PersistentFlags().StringVarP(&charMapFile, "charmap", "c", "", "Optional custom character mapping file; embedded Patris81 mapping is used by default")
	rootCmd.PersistentFlags().StringVarP(&outputDir, "output", "o", ".", "Output directory for converted files (use '-' for stdout)")
	rootCmd.PersistentFlags().StringVar(&tempDir, "temp-dir", "", "Temp directory for copied/downloaded database files (default: system temp)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
	rootCmd.PersistentFlags().BoolVarP(&directAccess, "direct-access", "d", false, "Access database file directly without temp copy (may conflict with BDE writes)")
	rootCmd.PersistentFlags().BoolVarP(&rtlMode, "rtl", "r", false, "Opt in to RTL logical text conversion for mixed Persian/Latin output")

	// Convert command
	convertCmd := &cobra.Command{
		Use:   "convert [database-file-or-url]",
		Short: "🔄 Convert a Paradox database file to JSON or CSV",
		Args:  cobra.ExactArgs(1),
		Run:   runConvert,
	}
	convertCmd.Flags().StringVarP(&outputFormat, "format", "f", "json", "Output format (json or csv)")
	convertCmd.Flags().BoolVarP(&watchMode, "watch", "w", false, "Watch file or URL for changes and auto-convert")
	convertCmd.Flags().String("debounce", "1s", "Debounce duration for local files; polling interval for URLs (e.g., 0s, 500ms, 1s, 5m)")

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

Set GITHUB_TOKEN for private repositories and higher API rate limits.`,
		Run: runUpdate,
	}
	updateCmd.Flags().StringP("branch", "b", "main", "Branch to download from")

	rootCmd.AddCommand(convertCmd, infoCmd, companyCmd, serveCmd, ipcCmd, tuiCmd, updateCmd)

	if err := rootCmd.Execute(); err != nil {
		errorColor.Fprintf(os.Stderr, "❌ Error: %v\n", err)
		os.Exit(1)
	}
}

func getenvDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func runUpdate(cmd *cobra.Command, args []string) {
	branch, err := cmd.Flags().GetString("branch")
	if err != nil {
		errorColor.Printf("❌ Failed to read 'branch' flag: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	successColor.Println("🚀 Patris Export Auto-Update")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	repoOwner := getenvDefault("PATRIS_REPO_OWNER", defaultRepoOwner)
	repoName := getenvDefault("PATRIS_REPO_NAME", defaultRepoName)
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
	if !rootFlags.Changed("output") {
		outputDir = cfg.Convert.Output
	}
	if !rootFlags.Changed("temp-dir") {
		tempDir = cfg.Runtime.TempDir
	}
	filecopy.SetTempDir(appconfig.ResolveTempDir(tempDir))
	converter.SetRTLConversion(rtlMode)
	return mgr, cfg
}

func runTUI(cmd *cobra.Command, args []string) {
	mgr, cfg := effectiveConfig(cmd)
	if len(args) > 0 {
		cfg.Database.Path = args[0]
	}
	if err := tui.Run(cfg, mgr.Path(), version.Current()); err != nil {
		errorColor.Printf("❌ TUI error: %v\n", err)
		os.Exit(1)
	}
}

func runConvert(cmd *cobra.Command, args []string) {
	_, cfg := effectiveConfig(cmd)
	dbFile := args[0]
	if !cmd.Flags().Changed("format") {
		outputFormat = cfg.Convert.Format
	}
	if !cmd.Flags().Changed("watch") {
		watchMode = cfg.Convert.Watch
	}

	// Check if output is stdout
	useStdout := outputDir == "-"

	// Validate that watch mode is not used with stdout
	if watchMode && useStdout {
		errorColor.Println("❌ Watch mode cannot be used with stdout output")
		errorColor.Println("💡 Remove -w flag or specify a file/directory for output")
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

	// Create output directory if it doesn't exist and we're not using stdout
	if !useStdout {
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			errorColor.Printf("❌ Failed to create output directory: %v\n", err)
			os.Exit(1)
		}
	}

	if !useStdout {
		// Display temp file setting
		displayFileStatus(dbFile)

		// Check for process conflicts
		checkProcessConflicts(dbFile)
	}

	if watchMode {
		// Parse debounce duration
		debounceStr, _ := cmd.Flags().GetString("debounce")
		if !cmd.Flags().Changed("debounce") {
			debounceStr = cfg.Convert.Debounce
		}
		debounceDuration := parseDebounceDuration(debounceStr)

		infoColor.Printf("👀 Watching file: %s\n", dbFile)
		infoColor.Println("📝 Press Ctrl+C to stop watching")

		// Initial conversion
		convertFile(dbFile, charMap, useStdout)

		// Set up watcher with configured debounce
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
				convertFile(path, charMap, useStdout)
			}, pollInterval); err != nil {
				errorColor.Printf("❌ Failed to poll URL: %v\n", err)
				os.Exit(1)
			}
			select {}
		}

		if err := fw.Watch(dbFile, func(path string) {
			infoColor.Printf("🔄 File changed: %s\n", filepath.Base(path))
			convertFile(path, charMap, useStdout)
		}, debounceDuration); err != nil {
			errorColor.Printf("❌ Failed to watch file: %v\n", err)
			os.Exit(1)
		}

		fw.Start()

		// Wait forever
		select {}
	} else {
		convertFile(dbFile, charMap, useStdout)
	}
}
func convertFile(dbFile string, charMap converter.CharMapping, useStdout bool) {
	fileToOpen, cleanup, err := prepareFileForReading(dbFile, !useStdout)
	if err != nil {
		errorColor.Printf("failed to prepare database file: %v\n", err)
		return
	}
	defer cleanup()

	if !useStdout {
		infoColor.Printf("Opening database: %s\n", filepath.Base(dbFile))
	}

	// Open database
	db, err := paradox.Open(fileToOpen)
	if err != nil {
		errorColor.Printf("❌ Failed to open database: %v\n", err)
		return
	}
	defer db.Close()

	// Get records
	records, err := db.GetRecords()
	if err != nil {
		errorColor.Printf("❌ Failed to read records: %v\n", err)
		return
	}

	if !useStdout {
		infoColor.Printf("📊 Found %d records\n", len(records))
	}

	// Create exporter
	exp := converter.NewExporter(converter.Patris2Fa)

	if useStdout {
		// Export to stdout
		if outputFormat == "csv" {
			// Get fields for CSV header
			fields, err := db.GetFields()
			if err != nil {
				errorColor.Printf("❌ Failed to get fields: %v\n", err)
				return
			}

			if err := exp.ExportToCSVWriter(records, fields, os.Stdout); err != nil {
				errorColor.Printf("❌ Failed to export to CSV: %v\n", err)
				return
			}
		} else {
			if err := exp.ExportToJSONWriter(records, os.Stdout); err != nil {
				errorColor.Printf("❌ Failed to export to JSON: %v\n", err)
				return
			}
		}
	} else {
		// Export to file
		// Generate output filename
		sourceName := sourceBaseName(dbFile)
		baseName := strings.TrimSuffix(sourceName, filepath.Ext(sourceName))
		var outputFile string

		if outputFormat == "csv" {
			outputFile = filepath.Join(outputDir, baseName+".csv")

			// Get fields for CSV header
			fields, err := db.GetFields()
			if err != nil {
				errorColor.Printf("❌ Failed to get fields: %v\n", err)
				return
			}

			if err := exp.ExportToCSV(records, fields, outputFile); err != nil {
				errorColor.Printf("❌ Failed to export to CSV: %v\n", err)
				return
			}
		} else {
			outputFile = filepath.Join(outputDir, baseName+".json")
			if err := exp.ExportToJSON(records, outputFile); err != nil {
				errorColor.Printf("❌ Failed to export to JSON: %v\n", err)
				return
			}
		}

		successColor.Printf("✅ Successfully exported to: %s\n", outputFile)
	}
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
		errorColor.Printf("❌ Failed to open database: %v\n", err)
		os.Exit(1)
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
		infoColor.Printf("âš™ï¸ Config: %s\n", configManager.Path())
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
