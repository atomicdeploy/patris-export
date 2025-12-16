package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/converter"
	"github.com/atomicdeploy/patris-export/pkg/paradox"
	"github.com/atomicdeploy/patris-export/pkg/server"
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

	rootCmd.AddCommand(convertCmd, infoCmd, companyCmd, serveCmd)

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
		debounceDuration, err := time.ParseDuration(debounceString)
		if err != nil {
			errorColor.Printf("❌ Invalid debounce duration '%s': %v\n", debounceString, err)
			errorColor.Println("💡 Valid examples: 0s, 500ms, 1s, 5s, 1m")
			os.Exit(1)
		}

		infoColor.Printf("👀 Watching file: %s (debounce: %s)\n", dbFile, debounceDuration)
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
		debounceDuration, err := time.ParseDuration(debounceStr)
		if err != nil {
			errorColor.Printf("❌ Invalid debounce duration '%s': %v\n", debounceStr, err)
			errorColor.Println("💡 Valid examples: 0s, 500ms, 1s, 5s, 1m")
			os.Exit(1)
		}

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
