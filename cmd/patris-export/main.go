package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/filecopy"
	"github.com/atomicdeploy/patris-export/pkg/paradox"
	"github.com/atomicdeploy/patris-export/pkg/processmon"
	"github.com/atomicdeploy/patris-export/pkg/server"
	"github.com/atomicdeploy/patris-export/pkg/tui"
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
	configFile   string
	outputDir    string
	outputFormat string
	watchMode    bool
	verbose      bool
	directAccess bool

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
		Version: version.String(),
	}
	rootCmd.Short = "📦 Paradox/BDE database export service for Patris81"
	rootCmd.Long = cliIntro()
	rootCmd.SetVersionTemplate(version.Detailed() + "\n")

	// Global flags
	rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "Path to patris-export JSON config file")
	rootCmd.PersistentFlags().StringVarP(&charMapFile, "charmap", "c", "", "Path to character mapping file (farsi_chars.txt)")
	rootCmd.PersistentFlags().StringVarP(&outputDir, "output", "o", ".", "Output directory for converted files (use '-' for stdout)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
	rootCmd.PersistentFlags().BoolVarP(&directAccess, "direct-access", "d", false, "Access database file directly without temp copy (may conflict with BDE writes)")

	// Convert command
	convertCmd := &cobra.Command{
		Use:   "convert [database-file]",
		Short: "🔄 Convert a Paradox database file to JSON or CSV",
		Args:  cobra.ExactArgs(1),
		Run:   runConvert,
	}
	convertCmd.Flags().StringVarP(&outputFormat, "format", "f", "json", "Output format (json or csv)")
	convertCmd.Flags().BoolVarP(&watchMode, "watch", "w", false, "Watch file for changes and auto-convert")
	convertCmd.Flags().String("debounce", "1s", "Debounce duration for watch mode (e.g., 0s, 500ms, 1s, 5s)")

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
	serveCmd.Short = "🌐 Start REST API and WebSocket server"
	serveCmd.Args = cobra.MaximumNArgs(1)
	serveCmd.Flags().StringP("addr", "a", "", "Server address override (e.g., 127.0.0.1:8080 or :8080)")
	serveCmd.Flags().String("host", "", "Host to bind: 127.0.0.1, 0.0.0.0, or an explicit interface")
	serveCmd.Flags().Int("port", 0, "Port to listen on")
	serveCmd.Flags().BoolP("watch", "w", true, "Watch file for changes and broadcast updates")
	serveCmd.Flags().String("debounce", "0s", "Debounce duration for watch mode (e.g., 0s, 500ms, 1s, 5s)")

	tuiCmd := &cobra.Command{
		Use:   "tui [database-file]",
		Short: "🖥️ Open the terminal dashboard",
		Args:  cobra.MaximumNArgs(1),
		Run:   runTUI,
	}

	rootCmd.AddCommand(convertCmd, infoCmd, companyCmd, serveCmd, tuiCmd)

	if err := rootCmd.Execute(); err != nil {
		errorColor.Fprintf(os.Stderr, "❌ Error: %v\n", err)
		os.Exit(1)
	}
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
	mgr, err := appconfig.Load(configFile)
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
	effectiveConfig(cmd)
	dbFile := args[0]

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
	if directAccess {
		warningColor.Printf("⚠️  Direct file access mode for: %s (may conflict with BDE writes)\n", filepath.Base(filePath))
	} else {
		infoColor.Printf("📋 Using temporary file copy for: %s\n", filepath.Base(filePath))
	}
}

// checkProcessConflicts checks for potential conflicts with running processes
func checkProcessConflicts(dbFile string) {
	// Check for running patris81.exe processes
	patris81Processes, err := processmon.FindProcessByName("patris81.exe")
	if err == nil && len(patris81Processes) > 0 {
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
		if err == nil && len(fileInfo.Processes) > 0 {
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

	// Start server
	successColor.Printf("🌐 Server running at http://%s\n", addr)
	infoColor.Printf("⚙️ Config: %s\n", configManager.Path())
	infoColor.Println("📝 Press Ctrl+C to stop the server")

	if err := srv.Start(addr); err != nil {
		errorColor.Printf("❌ Server error: %v\n", err)
		os.Exit(1)
	}
}
