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

# The issue is that CMake passes C compiler flags like -W, -Wall, etc. to windres via target_compile_options
# windres doesn't understand these flags and fails
# Solution: Use generator expressions to apply flags only to C/CXX files, not RC files

# First, find all target_compile_options lines and wrap them with generator expressions
# This ensures flags are only applied when compiling C/C++ code, not RC files
if grep -q "target_compile_options.*-Wall\|-W" CMakeLists.txt; then
    echo "  Wrapping compiler flags in generator expressions..."
    # We need to wrap each flag in a generator expression that excludes RC language
    # Replace patterns like: target_compile_options(pxlib PRIVATE -Wall -Wpointer-arith -W)
    # With: target_compile_options(pxlib PRIVATE $<$<COMPILE_LANGUAGE:C>:-Wall -Wpointer-arith -W>)
    sed -i '/target_compile_options.*pxlib.*PRIVATE/s/PRIVATE \(.*\))/PRIVATE $<$<COMPILE_LANGUAGE:C>:\1>)/' CMakeLists.txt
    echo "  ✓ Wrapped compiler flags to exclude RC files"
else
    echo "  No target_compile_options with warning flags found"
fi

echo "✅ pxlib CMakeLists.txt patched successfully"
