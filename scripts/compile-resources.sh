#!/bin/bash
# Script to compile Windows resource files for embedding in Go binaries

set -euo pipefail

# Determine which windres binary to use
if [ -n "$WINDRES" ]; then
    WINDRES_BIN="$WINDRES"
else
    # Try common names in order
    if command -v x86_64-w64-mingw32-windres &> /dev/null; then
        WINDRES_BIN="x86_64-w64-mingw32-windres"
    elif command -v windres &> /dev/null; then
        WINDRES_BIN="windres"
    elif command -v x86_64-w64-mingw32-windres.exe &> /dev/null; then
        WINDRES_BIN="x86_64-w64-mingw32-windres.exe"
    else
        echo "Error: No suitable windres binary found in PATH."
        echo "Please install mingw-w64 tools and ensure windres is available in your PATH."
        echo "You can also set the WINDRES environment variable to specify the binary."
        exit 1
    fi
fi

# Compile patris-export resource file
echo "Compiling patris-export.rc to patris-export.syso..."
"$WINDRES_BIN" \
    -i cmd/patris-export/patris-export.rc \
    -o cmd/patris-export/patris-export_windows_amd64.syso \
    -O coff \
    --target=pe-x86-64

# Verify the output file was created
if [ ! -f cmd/patris-export/patris-export_windows_amd64.syso ]; then
    echo "❌ Error: Resource file compilation failed - output file not created"
    exit 1
fi

echo "✅ Resource file compiled successfully"
ls -lh cmd/patris-export/patris-export_windows_amd64.syso
