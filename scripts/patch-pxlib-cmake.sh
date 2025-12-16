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

if grep -q "\-Wall" CMakeLists.txt; then
    echo "  Separating compiler warning flags from preprocessor definitions..."
    
    python3 << 'PYTHON_SCRIPT'
with open('CMakeLists.txt', 'r') as f:
    lines = f.readlines()

new_lines = []
i = 0
patched = False

while i < len(lines):
    line = lines[i]
    
    # Look for the problematic add_definitions block
    if 'if(CMAKE_COMPILER_IS_GNUCC)' in line:
        # Check if this is followed by the multi-line add_definitions
        if i+1 < len(lines) and 'add_definitions(' in lines[i+1]:
            # Found it! Replace the entire block
            new_lines.append(line)  # Keep the if(CMAKE_COMPILER_IS_GNUCC)
            new_lines.append('    add_definitions(-DHAVE_CONFIG_H)\n')
            new_lines.append('    # Compiler warning flags separated to avoid passing them to windres\n')
            new_lines.append('    set(PXLIB_WARNING_FLAGS -Wall -Wpointer-arith -W ${PXLIB_EXTRA_GCC_FLAGS})\n')
            
            # Skip the old add_definitions block (lines i+1 through the closing paren)
            i += 1
            while i < len(lines) and ')' not in lines[i]:
                i += 1
            i += 1  # Skip the closing paren line
            patched = True
            continue
    
    # Look for add_library(pxlib SHARED and add target_compile_options after it
    if 'add_library(pxlib SHARED ${SOURCES})' in line and 'target_compile_options(pxlib' not in ''.join(lines[i:i+10]):
        new_lines.append(line)
        new_lines.append('\n')
        new_lines.append('# Apply warning flags only to C/C++ files, not RC files (windres doesn\'t understand them)\n')
        new_lines.append('if(CMAKE_COMPILER_IS_GNUCC)\n')
        new_lines.append('    target_compile_options(pxlib PRIVATE $<$<COMPILE_LANGUAGE:C>:${PXLIB_WARNING_FLAGS}>)\n')
        new_lines.append('endif()\n')
        i += 1
        continue
    
    new_lines.append(line)
    i += 1

if patched:
    with open('CMakeLists.txt', 'w') as f:
        f.writelines(new_lines)
    print("Patched successfully")
else:
    print("Warning: Pattern not found, CMakeLists.txt may have already been patched or has different format")
PYTHON_SCRIPT
    
    echo "  Separated warning flags from definitions and applied with generator expression"
else
    echo "  No add_definitions with warning flags found - checking if already patched..."
fi

echo "pxlib CMakeLists.txt patched successfully"
