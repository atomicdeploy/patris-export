#!/bin/bash
# Script to patch pxlib CMakeLists.txt for building with custom resource file
# This script is shared between cross-compilation and native Windows builds

set -e

if [ -z "$1" ] || [ -z "$2" ]; then
    echo "Usage: $0 <path-to-pxlib-directory> <path-to-resource-file>"
    exit 1
fi

PXLIB_DIR="$1"
RESOURCE_FILE="$2"

if [ ! -d "$PXLIB_DIR" ]; then
    echo "Error: pxlib directory not found: $PXLIB_DIR"
    exit 1
fi

if [ ! -f "$RESOURCE_FILE" ]; then
    echo "Error: Resource file not found: $RESOURCE_FILE"
    exit 1
fi

cd "$PXLIB_DIR"

echo "Copying custom resource file..."
cp "$RESOURCE_FILE" ./pxlib.rc

echo "Patching CMakeLists.txt..."

# Remove any existing RC file references
if grep -q "target_sources(pxlib PRIVATE.*pxlib.rc)" CMakeLists.txt; then
  sed -i '/target_sources(pxlib PRIVATE.*pxlib.rc)/d' CMakeLists.txt
  echo "  Removed original RC file from CMakeLists.txt"
fi

# Add our RC file to CMakeLists.txt after the add_library line
sed -i '/add_library(pxlib/a \    # Add Windows resource file for version info\n    if(WIN32)\n        target_sources(pxlib PRIVATE ${CMAKE_SOURCE_DIR}/pxlib.rc)\n    endif()' CMakeLists.txt

echo "✅ pxlib CMakeLists.txt patched successfully"
