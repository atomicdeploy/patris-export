# 📊 Patris Export

A fast and performant application for reading, parsing, and converting Paradox/BDE database files (`*.db`) from Patris81 software.

[![Build and Release](https://github.com/atomicdeploy/patris-export/actions/workflows/build.yml/badge.svg)](https://github.com/atomicdeploy/patris-export/actions/workflows/build.yml)
[![Go Version](https://img.shields.io/badge/Go-1.23-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

## ✨ Features

- 🔄 **Convert Paradox DB files** to JSON or CSV formats
- 🎯 **Persian/Farsi encoding support** - Automatically converts Patris81 proprietary encoding
- 👀 **File watching** - Automatically converts files when they change
- 🔒 **Write-lock prevention** - Copies files to temp location to avoid BDE conflicts
- 🔐 **File integrity** - CRC32 checksum calculation and verification
- 🌐 **REST API** - HTTP JSON API for accessing database records
- 🔌 **WebSocket support** - Real-time updates when database changes
- 🎨 **Beautiful CLI** - Colorful terminal output with emojis
- 🏢 **Company.inf support** - Parse company information files
- ⚡ **Fast and lightweight** - Written in Go with native performance
- 🐧🪟 **Cross-platform** - Supports both Linux and Windows

## 🚀 Installation

### From Release

Download the latest release for your platform from the [Releases page](https://github.com/atomicdeploy/patris-export/releases).

**Windows users:** The Windows release includes the required pxlib.dll file. Make sure to keep it in the same directory as the executable.

### From Source

Requirements:
- Go 1.23 or later
- pxlib development library
  - **Linux:** `sudo apt-get install pxlib-dev pxlib1`
  - **Windows:** See [docs/WINDOWS_BUILD.md](docs/WINDOWS_BUILD.md) for building pxlib

**On Ubuntu/Debian:**
```bash
sudo apt-get install pxlib-dev pxlib1
go install github.com/atomicdeploy/patris-export/cmd/patris-export@latest
```

**Build manually:**
```bash
git clone https://github.com/atomicdeploy/patris-export.git
cd patris-export
make build
```

## 📖 Usage

### Convert Database to JSON

```bash
patris-export convert kala.db -f json -o output/
```

### Convert Database to CSV

```bash
patris-export convert kala.db -f csv -o output/
```

### Watch File for Changes

```bash
patris-export convert kala.db -f json -w
```

This will automatically re-convert the file whenever it changes. The convert command uses a 1-second debounce by default, meaning that rapid successive changes to the file will only trigger one conversion after the changes have settled.

You can customize the debounce duration with the `--debounce` flag:

```bash
# No debounce (immediate conversion on every change)
patris-export convert kala.db -f json -w --debounce 0s

# 500ms debounce
patris-export convert kala.db -f json -w --debounce 500ms

# 5 second debounce
patris-export convert kala.db -f json -w --debounce 5s
```

### Avoid Write-Lock Conflicts

By default, patris-export copies the database file to a temporary location before reading it. This prevents write-lock conflicts with applications like Borland Database Engine (BDE) that may have the file open for writing.

```bash
# Default behavior - uses temp file (recommended)
patris-export convert kala.db -f json

# Disable temp file usage (direct access)
patris-export convert kala.db -f json --use-temp-file=false
```

When using the temp file feature:
- A CRC32 checksum is calculated and displayed for the source file
- The file is copied to the system temp directory in 10MB chunks
- The original file is released immediately after copying
- File modification time is preserved on the copy

Use `--verbose` flag to see detailed information about the temp file operation:

```bash
patris-export convert kala.db -f json -v
```

### Show Database Information

```bash
patris-export info kala.db
```

### Parse Company Information

```bash
patris-export company company.inf -c testdata/farsi_chars.txt
```

### Start REST API Server

```bash
patris-export serve kala.db -a :8080
```

Then access:
- Web interface: http://localhost:8080
- API records: http://localhost:8080/api/records
- API info: http://localhost:8080/api/info
- WebSocket: ws://localhost:8080/ws

The server watches the database file by default and broadcasts updates immediately (no debounce) to all connected WebSocket clients when changes are detected.

You can customize the debounce duration for the server with the `--debounce` flag:

```bash
# 500ms debounce for server updates
patris-export serve kala.db -a :8080 --debounce 500ms

# 1 second debounce
patris-export serve kala.db -a :8080 --debounce 1s
```

## 🎯 Using Character Mapping

For proper Persian/Farsi text conversion, use the character mapping file:

```bash
patris-export convert kala.db -c testdata/farsi_chars.txt -f json
```

## 🔌 WebSocket Example

Connect to the WebSocket endpoint to receive real-time updates:

```javascript
const ws = new WebSocket('ws://localhost:8080/ws');

ws.onmessage = (event) => {
    const data = JSON.parse(event.data);
    console.log('Received update:', data);
    console.log('Record count:', data.count);
    console.log('Records:', data.records);
};
```

The server will automatically broadcast updates to all connected clients immediately when the database file changes (no debounce delay for real-time responsiveness).

## 🏗️ Architecture

```
patris-export/
├── cmd/
│   └── patris-export/     # Main CLI application
├── pkg/
│   ├── paradox/           # Paradox DB file reader (using pxlib)
│   ├── converter/         # Patris encoding converter & exporter
│   ├── watcher/           # File watcher with hash-based change detection
│   └── server/            # REST API & WebSocket server
├── testdata/              # Sample database files
└── docs/                  # Documentation
```

## 🛠️ Development

### Build Commands

```bash
make build          # Build for current platform
make build-linux    # Build for Linux
make build-windows  # Build for Windows (see docs/WINDOWS_BUILD.md)
make build-all      # Build for all platforms
make test          # Run tests
make clean         # Clean build artifacts
make install       # Install to GOPATH/bin
```

### Running Tests

```bash
go test -v ./...
```

## 📋 Command Reference

### Global Flags

- `-c, --charmap` - Path to character mapping file (farsi_chars.txt)
- `-o, --output` - Output directory for converted files (default: current directory)
- `-v, --verbose` - Enable verbose logging
- `-t, --use-temp-file` - Copy database file to temp location before reading to prevent write-lock conflicts (default: true)

### Commands

#### `convert [database-file]`
Convert a Paradox database file to JSON or CSV.

**Flags:**
- `-f, --format` - Output format: json or csv (default: json)
- `-w, --watch` - Watch file for changes and auto-convert
- `-d, --debounce` - Debounce duration for watch mode (default: 1s, examples: 0s, 500ms, 5s)

#### `info [database-file]`
Display information about a Paradox database file (fields, record count, etc.)

#### `company [company.inf]`
Parse and display company information from company.inf file.

#### `serve [database-file]`
Start the REST API and WebSocket server.

**Flags:**
- `-a, --addr` - Server address (default: :8080)
- `-w, --watch` - Watch file for changes and broadcast updates (default: true)
- `-d, --debounce` - Debounce duration for watch mode (default: 0s, examples: 500ms, 1s, 5s)

## 🔧 API Reference

### REST Endpoints

#### `GET /`
Web interface with API documentation.

#### `GET /api/records`
Returns all database records in JSON format.

**Response:**
```json
{
  "success": true,
  "count": 100,
  "records": [...]
}
```

#### `GET /api/info`
Returns database schema information.

**Response:**
```json
{
  "success": true,
  "file": "kala.db",
  "num_records": 100,
  "num_fields": 10,
  "fields": [...]
}
```

### WebSocket

#### `ws://localhost:8080/ws`
Connect to receive real-time database updates.

**Message format:**
```json
{
  "type": "update",
  "timestamp": "2025-12-13T23:45:19Z",
  "count": 100,
  "records": [...]
}
```

## 🗺️ TODO

### Planned Features
- [ ] Support for additional database formats
- [ ] Batch processing of multiple files
- [ ] Database diff functionality
- [ ] Custom field filtering and transformation
- [ ] GraphQL API support
- [ ] Docker containerization
- [ ] Performance benchmarks
- [ ] Comprehensive test coverage
- [ ] Advanced WebSocket features (subscribe to specific records, filtering)
- [ ] Configuration file support (YAML/JSON)
- [ ] Compression support for large exports
- [ ] Incremental export (only changed records)
- [ ] SQL export format support
- [ ] Data validation and integrity checks
- [ ] Import capabilities (reverse conversion)

### Known Limitations
- Currently optimized for Patris81 database format
- Windows cross-compilation requires mingw-w64
- Large database files may require significant memory

## 📄 License

MIT License - see LICENSE file for details.

## 🙏 Acknowledgments

- [pxlib](https://pxlib.sourceforge.net/) - Paradox database library
- Original PHP implementation and character mappings
- Patris81 software and its database format

## 📞 Support

For issues, questions, or contributions, please visit the [GitHub repository](https://github.com/atomicdeploy/patris-export).
