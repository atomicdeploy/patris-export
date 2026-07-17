#!/bin/bash
set -euo pipefail

# Script to generate Windows resource file with dynamic metadata from git/GitHub
# Usage: ./generate-version-rc.sh <output_file>

OUTPUT_FILE="${1:-cmd/patris-export/patris-export.rc}"
ICON_FILE="${PATRIS_EXPORT_ICON_FILE:-assets/windows/patris-api.ico}"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"

# Validate OUTPUT_FILE to prevent directory traversal and absolute paths
if [[ "$OUTPUT_FILE" == /* ]] || [[ "$OUTPUT_FILE" == *".."* ]]; then
    echo "Error: Invalid output file path. Absolute paths and directory traversal are not allowed." >&2
    exit 1
fi
# Prefer the explicit build version, then the canonical source version.
SOURCE_VERSION="$(sed -nE 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' "$REPO_ROOT/pkg/version/version.go" | head -n 1)"
VERSION="${VERSION:-$SOURCE_VERSION}"
VERSION=$(echo "$VERSION" | sed 's/^v//')
# Validate VERSION to allow only digits, dots, and optional pre-release (e.g., -rc1)
if [[ ! "$VERSION" =~ ^[0-9]+(\.[0-9]+)*(-[a-zA-Z0-9._-]+)?$ ]]; then
    echo "Warning: Invalid version format ('$VERSION'), using default '1.0.0'" >&2
    VERSION="1.0.0"
fi
VERSION_COMMA=$(echo "$VERSION" | sed 's/-.*$//' | sed 's/\./,/g')

SOURCE_BUILD_DATE="${BUILD_DATE:-$(git -C "$REPO_ROOT" show -s --format=%cI HEAD)}"
if [[ "$SOURCE_BUILD_DATE" =~ ^([0-9]{4})- ]]; then
    CURRENT_YEAR="${BASH_REMATCH[1]}"
else
    echo "Build date must start with a four-digit year: $SOURCE_BUILD_DATE" >&2
    exit 1
fi

REPO_OWNER="${PATRIS_EXPORT_REPO_OWNER:-atomicdeploy}"

# Validate the source-controlled company value before writing the RC file.
if [[ ! "$REPO_OWNER" =~ ^[a-zA-Z0-9._-]+$ ]]; then
    echo "Warning: Invalid repository owner format, using default" >&2
    REPO_OWNER="Unknown"
fi

# Function to escape strings for C string literals
escape_c_string() {
    local input="$1"
    # Use printf for safer escaping - it properly handles special characters
    # Replace backslash with double backslash, then quotes with escaped quotes
    printf '%s' "$input" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

DESCRIPTION="${PATRIS_EXPORT_FILE_DESCRIPTION:-Paradox/BDE extraction, transformation, and integration platform}"

# Truncate DESCRIPTION to 256 characters to avoid overly long resource strings
DESCRIPTION="${DESCRIPTION:0:256}"
# Escape strings for safe C string literal insertion
DESCRIPTION_ESCAPED=$(escape_c_string "$DESCRIPTION")
COMPANY_NAME_ESCAPED=$(escape_c_string "$REPO_OWNER")

# Generate the resource file
cat > "$OUTPUT_FILE" << EOF
#include <windows.h>

#define IDI_PATRIS_API_ICON 101
IDI_PATRIS_API_ICON ICON "$ICON_FILE"

#define VER_FILEVERSION             $VERSION_COMMA,0
#define VER_FILEVERSION_STR         "$VERSION.0"

#define VER_PRODUCTVERSION          $VERSION_COMMA,0
#define VER_PRODUCTVERSION_STR      "$VERSION"

#define VER_COMPANYNAME_STR         "$COMPANY_NAME_ESCAPED"
#define VER_FILEDESCRIPTION_STR     "$DESCRIPTION_ESCAPED"
#define VER_INTERNALNAME_STR        "patris-export"
#define VER_LEGALCOPYRIGHT_STR      "Copyright (C) $CURRENT_YEAR"
#define VER_ORIGINALFILENAME_STR    "patris-export.exe"
#define VER_PRODUCTNAME_STR         "Patris Export"

VS_VERSION_INFO VERSIONINFO
FILEVERSION     VER_FILEVERSION
PRODUCTVERSION  VER_PRODUCTVERSION
FILEFLAGSMASK   VS_FFI_FILEFLAGSMASK
FILEFLAGS       0x0L
FILEOS          VOS_NT_WINDOWS32
FILETYPE        VFT_APP
FILESUBTYPE     VFT2_UNKNOWN
BEGIN
    BLOCK "StringFileInfo"
    BEGIN
        BLOCK "040904B0"
        BEGIN
            VALUE "CompanyName",      VER_COMPANYNAME_STR
            VALUE "FileDescription",  VER_FILEDESCRIPTION_STR
            VALUE "FileVersion",      VER_FILEVERSION_STR
            VALUE "InternalName",     VER_INTERNALNAME_STR
            VALUE "LegalCopyright",   VER_LEGALCOPYRIGHT_STR
            VALUE "OriginalFilename", VER_ORIGINALFILENAME_STR
            VALUE "ProductName",      VER_PRODUCTNAME_STR
            VALUE "ProductVersion",   VER_PRODUCTVERSION_STR
        END
    END
    BLOCK "VarFileInfo"
    BEGIN
        VALUE "Translation", 0x409, 1200
    END
END
EOF

echo "Generated resource file: $OUTPUT_FILE"
echo "  Version: $VERSION"
echo "  Company: $REPO_OWNER"
echo "  Description: $DESCRIPTION"
echo "  Copyright: Copyright (C) $CURRENT_YEAR"
