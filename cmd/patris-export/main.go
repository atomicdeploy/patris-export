package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/paradox"
	"github.com/atomicdeploy/patris-export/pkg/server"
	"github.com/atomicdeploy/patris-export/pkg/updater"
	"github.com/atomicdeploy/patris-export/pkg/watcher"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	// Version information
	Version   = "1.0.0"
	BuildDate = "unknown"

	// Global flags
	charMapFile    string
	outputDir      string
	outputFormat   string
	watchMode      bool
	verbose        bool
	debounceString string

	// Color definitions
	successColor = color.New(color.FgGreen, color.Bold)
	errorColor   = color.New(color.FgRed, color.Bold)
	infoColor    = color.New(color.FgCyan)
	warningColor = color.New(color.FgYellow)
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
		Version: Version,
	}

	// Global flags
	rootCmd.PersistentFlags().StringVarP(&charMapFile, "charmap", "c", "", "Path to character mapping file (farsi_chars.txt)")
	rootCmd.PersistentFlags().StringVarP(&outputDir, "output", "o", ".", "Output directory for converted files")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")

	// Convert command
	convertCmd := &cobra.Command{
		Use:   "convert [database-file]",
		Short: "🔄 Convert a Paradox database file to JSON or CSV",
		Args:  cobra.ExactArgs(1),
		Run:   runConvert,
	}
	convertCmd.Flags().StringVarP(&outputFormat, "format", "f", "json", "Output format (json or csv)")
	convertCmd.Flags().BoolVarP(&watchMode, "watch", "w", false, "Watch file for changes and auto-convert")
	convertCmd.Flags().StringVarP(&debounceString, "debounce", "d", "1s", "Debounce duration for watch mode (e.g., 0s, 500ms, 1s, 5s)")

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
		Use:   "serve [database-file]",
		Short: "🌐 Start REST API and WebSocket server",
		Args:  cobra.ExactArgs(1),
		Run:   runServe,
	}
	serveCmd.Flags().StringP("addr", "a", ":8080", "Server address (e.g., :8080)")
	serveCmd.Flags().BoolP("watch", "w", true, "Watch file for changes and broadcast updates")
	serveCmd.Flags().StringP("debounce", "d", "0s", "Debounce duration for watch mode (e.g., 0s, 500ms, 1s, 5s)")

	// Update command
	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "🚀 Update patris-export to the latest version",
		Long: `🚀 Update patris-export to the latest version from GitHub Actions artifacts.

Downloads the latest build artifact for your platform and replaces the current executable.
You can optionally specify a branch to download from (default: main).

Examples:
  patris-export update              # Update from main branch
  patris-export update --branch develop  # Update from develop branch

Note: Set GITHUB_TOKEN environment variable for higher API rate limits.`,
		Run: runUpdate,
	}
	updateCmd.Flags().StringP("branch", "b", "main", "Branch to download from")

	rootCmd.AddCommand(convertCmd, infoCmd, companyCmd, serveCmd, updateCmd)

	if err := rootCmd.Execute(); err != nil {
		errorColor.Fprintf(os.Stderr, "❌ Error: %v\n", err)
		os.Exit(1)
	}
}

func runConvert(cmd *cobra.Command, args []string) {
	dbFile := args[0]

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

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		errorColor.Printf("❌ Failed to create output directory: %v\n", err)
		os.Exit(1)
	}

	if watchMode {
		// Parse debounce duration
		debounceDuration := parseDebounceDuration(debounceString)

		infoColor.Printf("👀 Watching file: %s\n", dbFile)
		infoColor.Println("📝 Press Ctrl+C to stop watching")

		// Initial conversion
		convertFile(dbFile, charMap)

		// Set up watcher with configured debounce
		fw, err := watcher.NewFileWatcher()
		if err != nil {
			errorColor.Printf("❌ Failed to create file watcher: %v\n", err)
			os.Exit(1)
		}
		defer fw.Close()

		if err := fw.Watch(dbFile, func(path string) {
			infoColor.Printf("🔄 File changed: %s\n", filepath.Base(path))
			convertFile(path, charMap)
		}, debounceDuration); err != nil {
			errorColor.Printf("❌ Failed to watch file: %v\n", err)
			os.Exit(1)
		}

		fw.Start()

		// Wait forever
		select {}
	} else {
		convertFile(dbFile, charMap)
	}
}

func convertFile(dbFile string, charMap converter.CharMapping) {
	infoColor.Printf("🔍 Opening database: %s\n", filepath.Base(dbFile))

	// Open database
	db, err := paradox.Open(dbFile)
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

	infoColor.Printf("📊 Found %d records\n", len(records))

	// Create exporter
	exp := converter.NewExporter(converter.Patris2Fa)

	// Generate output filename
	baseName := strings.TrimSuffix(filepath.Base(dbFile), filepath.Ext(dbFile))
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

func runInfo(cmd *cobra.Command, args []string) {
	dbFile := args[0]

	infoColor.Printf("🔍 Reading database: %s\n", filepath.Base(dbFile))

	db, err := paradox.Open(dbFile)
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

func init() {
	// Set up logging
	log.SetFlags(0)
	log.SetOutput(os.Stdout)
}

func runServe(cmd *cobra.Command, args []string) {
	dbFile := args[0]
	addr, _ := cmd.Flags().GetString("addr")
	watchFile, _ := cmd.Flags().GetBool("watch")
	debounceStr, _ := cmd.Flags().GetString("debounce")

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
	srv, err := server.NewServer(dbFile, charMap)
	if err != nil {
		errorColor.Printf("❌ Failed to create server: %v\n", err)
		os.Exit(1)
	}
	defer srv.Close()

	// Start file watching if enabled
	if watchFile {
		// Parse debounce duration
		debounceDuration := parseDebounceDuration(debounceStr)

		if err := srv.StartWatching(debounceDuration); err != nil {
			errorColor.Printf("❌ Failed to start file watching: %v\n", err)
			os.Exit(1)
		}
	}

	// Start server
	successColor.Printf("🌐 Server running at http://localhost%s\n", addr)
	infoColor.Println("📝 Press Ctrl+C to stop the server")

	if err := srv.Start(addr); err != nil {
		errorColor.Printf("❌ Server error: %v\n", err)
		os.Exit(1)
	}
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

	// Derive repository information from go.mod
	repoOwner, repoName, err := updater.DeriveRepoInfoFromModule()
	if err != nil {
		errorColor.Printf("❌ Failed to determine repository information: %v\n", err)
		errorColor.Println("💡 Make sure you're running this from within the project directory")
		os.Exit(1)
	}

	infoColor.Printf("📦 Repository: %s/%s\n", repoOwner, repoName)

	// Create updater
	u := updater.NewUpdater(repoOwner, repoName)

	// Check platform support
	platformName := u.GetCurrentPlatformArtifactName()
	if platformName == "" {
		errorColor.Printf("❌ Auto-update is not supported on %s/%s\n", runtime.GOOS, runtime.GOARCH)
		errorColor.Println("💡 Supported platforms: linux/amd64, windows/amd64")
		os.Exit(1)
	}

	// Show current version
	infoColor.Printf("📦 Current version: %s (built: %s)\n", Version, BuildDate)
	infoColor.Printf("🌿 Target branch: %s\n", branch)
	infoColor.Printf("💻 Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println()

	// Check for GITHUB_TOKEN
	if os.Getenv("GITHUB_TOKEN") == "" {
		warningColor.Println("⚠️  GITHUB_TOKEN not set - using anonymous API access (lower rate limits)")
		warningColor.Println("💡 Set GITHUB_TOKEN environment variable for higher rate limits")
		fmt.Println()
	}

	// Step 1: Find latest successful build
	infoColor.Println("🔍 Searching for latest successful build...")
	run, err := u.GetLatestSuccessfulRun(branch)
	if err != nil {
		errorColor.Printf("❌ Failed to find latest build: %v\n", err)
		os.Exit(1)
	}

	successColor.Printf("✅ Found build #%d from %s\n", run.ID, run.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Println()

	// Step 2: Get artifacts
	infoColor.Println("📦 Fetching build artifacts...")
	artifacts, err := u.GetArtifactsForRun(run.ID)
	if err != nil {
		errorColor.Printf("❌ Failed to get artifacts: %v\n", err)
		os.Exit(1)
	}

	// Find the artifact for current platform
	var targetArtifact *updater.Artifact
	for i := range artifacts {
		if artifacts[i].Name == platformName {
			targetArtifact = &artifacts[i]
			break
		}
	}

	if targetArtifact == nil {
		errorColor.Printf("❌ No artifact found for platform: %s\n", platformName)
		errorColor.Println("💡 Available artifacts:")
		for _, a := range artifacts {
			fmt.Printf("   • %s\n", a.Name)
		}
		os.Exit(1)
	}

	if targetArtifact.Expired {
		errorColor.Println("❌ Artifact has expired - cannot download")
		os.Exit(1)
	}

	successColor.Printf("✅ Found artifact: %s (%.2f MB)\n", targetArtifact.Name, float64(targetArtifact.SizeInBytes)/(1024*1024))
	fmt.Println()

	// Step 3: Download artifact
	infoColor.Println("⬇️  Downloading artifact...")
	
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "patris-update-*")
	if err != nil {
		errorColor.Printf("❌ Failed to create temp directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tempDir) // Clean up

	zipPath, err := u.DownloadArtifact(targetArtifact, tempDir)
	if err != nil {
		errorColor.Printf("❌ Failed to download artifact: %v\n", err)
		fmt.Println()
		warningColor.Println("💡 GitHub Actions artifacts require authentication")
		warningColor.Println("   Please set the GITHUB_TOKEN environment variable:")
		fmt.Println()
		infoColor.Println("   export GITHUB_TOKEN='your_github_token'")
		infoColor.Println("   patris-export update")
		fmt.Println()
		warningColor.Println("   Get your token from: https://github.com/settings/tokens")
		warningColor.Println("   Required scope: 'actions:read'")
		fmt.Println()
		os.Exit(1)
	}

	successColor.Printf("✅ Downloaded to: %s\n", filepath.Base(zipPath))
	fmt.Println()

	// Step 4: Extract executable
	infoColor.Println("📂 Extracting executable...")
	extractedExe, err := u.ExtractExecutable(zipPath, tempDir)
	if err != nil {
		errorColor.Printf("❌ Failed to extract executable: %v\n", err)
		os.Exit(1)
	}

	successColor.Printf("✅ Extracted: %s\n", filepath.Base(extractedExe))
	fmt.Println()

	// Step 5: Replace current executable
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
	fmt.Println()
	infoColor.Println("💡 Run 'patris-export --version' to verify the update")
	fmt.Println()
}
