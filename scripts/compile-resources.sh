#!/bin/bash
# Script to compile Windows resource files for embedding in Go binaries

set -e

# Check if windres is available
if ! command -v x86_64-w64-mingw32-windres &> /dev/null; then
    echo "Error: x86_64-w64-mingw32-windres not found"
    echo "Install mingw-w64 tools: sudo apt-get install mingw-w64"
    exit 1
fi

# Compile patris-export resource file
echo "Compiling patris-export.rc to patris-export.syso..."
x86_64-w64-mingw32-windres \
    -i cmd/patris-export/patris-export.rc \
    -o cmd/patris-export/patris-export_windows_amd64.syso \
    -O coff \
    --target=pe-x86-64

echo "✅ Resource file compiled successfully"
ls -lh cmd/patris-export/patris-export_windows_amd64.syso
