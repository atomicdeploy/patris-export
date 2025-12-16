#!/bin/bash
# Script to patch pxlib CMakeLists.txt for building on Windows
# Fixes windres compiler flag issues by preventing C compiler flags from being passed to windres
# This script is shared between cross-compilation and native Windows builds

set -e

if [ -z "$1" ]; then
    echo "Usage: $0 <path-to-pxlib-directory>"
    exit 1
fi

PXLIB_DIR="$1"

if [ ! -d "$PXLIB_DIR" ]; then
    echo "Error: pxlib directory not found: $PXLIB_DIR"
    exit 1
fi

cd "$PXLIB_DIR"

echo "Patching CMakeLists.txt to fix windres compilation..."

# The issue is that CMake passes C compiler flags like -W to windres, which doesn't accept them
# We need to set the RC compiler flags separately to prevent this

# Find the line where the RC file is added and add proper configuration before it
if grep -q "target_sources(pxlib PRIVATE.*pxlib.rc)" CMakeLists.txt; then
    # Add RC compiler configuration before the target_sources line
    sed -i '/target_sources(pxlib PRIVATE.*pxlib.rc)/i \    # Configure RC compiler to not use C compiler flags\n    set_source_files_properties(${CMAKE_BINARY_DIR}/pxlib.rc PROPERTIES\n        COMPILE_FLAGS ""\n        LANGUAGE RC\n    )' CMakeLists.txt
    echo "  Added RC compiler configuration to CMakeLists.txt"
else
    echo "  Warning: No RC file target_sources found in CMakeLists.txt"
fi

echo "✅ pxlib CMakeLists.txt patched successfully"
