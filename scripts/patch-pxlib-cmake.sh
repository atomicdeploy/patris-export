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

# The issue is that add_definitions() in pxlib's CMakeLists.txt includes both
# preprocessor definitions (-DHAVE_CONFIG_H) and compiler warning flags (-Wall, -W, etc.)
# These are applied to ALL file types including RC files, causing windres to fail
# because windres doesn't understand C compiler warning flags.
#
# Solution: Separate compiler flags from preprocessor definitions
# - Keep -DHAVE_CONFIG_H in add_definitions() (needed for RC preprocessing)
# - Move warning flags to target_compile_options() with generator expression to exclude RC files

if grep -Pzo "add_definitions\([^)]*-Wall" CMakeLists.txt > /dev/null 2>&1; then
    echo "  Separating compiler warning flags from preprocessor definitions..."
    
    # Use Python to patch the CMakeLists.txt file properly
    python3 << 'PYTHON_SCRIPT'
import re

with open('CMakeLists.txt', 'r') as f:
    content = f.read()

# Find and replace the add_definitions block within CMAKE_COMPILER_IS_GNUCC
pattern = r'if\(CMAKE_COMPILER_IS_GNUCC\)\s*add_definitions\(\s*-DHAVE_CONFIG_H\s*-Wall -Wpointer-arith -W\s*\$\{PXLIB_EXTRA_GCC_FLAGS\}\s*\)'
replacement = '''if(CMAKE_COMPILER_IS_GNUCC)
    add_definitions(-DHAVE_CONFIG_H)
    # Compiler warning flags separated to avoid passing them to windres
    set(PXLIB_WARNING_FLAGS -Wall -Wpointer-arith -W ${PXLIB_EXTRA_GCC_FLAGS})'''

content = re.sub(pattern, replacement, content, flags=re.MULTILINE | re.DOTALL)

# Add target_compile_options after add_library(pxlib SHARED ...)
# Use generator expression to only apply warning flags to C files, not RC files
pattern = r'(add_library\(pxlib SHARED \$\{SOURCES\}\))'
replacement = r'''\1

# Apply warning flags only to C/C++ files, not RC files (windres doesn't understand them)
if(CMAKE_COMPILER_IS_GNUCC)
    target_compile_options(pxlib PRIVATE $<$<COMPILE_LANGUAGE:C>:${PXLIB_WARNING_FLAGS}>)
endif()'''

content = re.sub(pattern, replacement, content)

with open('CMakeLists.txt', 'w') as f:
    f.write(content)

print("  ✓ Patched successfully")
PYTHON_SCRIPT
    
    echo "  ✓ Separated warning flags from definitions and applied with generator expression"
else
    echo "  No add_definitions with warning flags found - checking if already patched..."
fi

echo "✅ pxlib CMakeLists.txt patched successfully"
