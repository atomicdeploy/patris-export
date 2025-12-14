# 📊 Patris Export

A fast and performant application for reading, parsing, and converting Paradox/BDE database files (`*.db`) from Patris81 software.

[![Build and Release](https://github.com/atomicdeploy/patris-export/actions/workflows/build.yml/badge.svg)](https://github.com/atomicdeploy/patris-export/actions/workflows/build.yml)
[![Go Version](https://img.shields.io/badge/Go-1.23-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

## ✨ Features

- 🔄 **Convert Paradox DB files** to JSON or CSV formats
- 🎯 **Persian/Farsi encoding support** - Automatically converts Patris81 proprietary encoding
- 🔤 **RTL text conversion** - Optimizes mixed Persian/English text for RTL display contexts
- 👀 **File watching** - Automatically converts files when they change
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

### Convert with RTL Text Optimization

For mixed Persian/English content that should display correctly in RTL contexts:

```bash
patris-export convert kala.db -f json --rtl -o output/
```

This converts text like "LAN8720 ماژول شبکه" to "ماژول شبکه LAN8720" for proper RTL display.

### Watch File for Changes

```bash
patris-export convert kala.db -f json -w
```

This will automatically re-convert the file whenever it changes.

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

The server will automatically broadcast updates to all connected clients when the database file changes.

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
- `-r, --rtl` - Enable RTL text conversion for mixed Persian/English content
- `-v, --verbose` - Enable verbose logging

### Commands

#### `convert [database-file]`
Convert a Paradox database file to JSON or CSV.

**Flags:**
- `-f, --format` - Output format: json or csv (default: json)
- `-w, --watch` - Watch file for changes and auto-convert

#### `info [database-file]`
Display information about a Paradox database file (fields, record count, etc.)

#### `company [company.inf]`
Parse and display company information from company.inf file.

#### `serve [database-file]`
Start the REST API and WebSocket server.

**Flags:**
- `-a, --addr` - Server address (default: :8080)
- `-w, --watch` - Watch file for changes and broadcast updates (default: true)

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
