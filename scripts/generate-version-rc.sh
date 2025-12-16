#!/bin/bash
set -e

# Script to generate Windows resource file with dynamic metadata from git/GitHub
# Usage: ./generate-version-rc.sh <output_file>

OUTPUT_FILE="${1:-cmd/patris-export/patris-export.rc}"

# Get version from git tag or default to 1.0.0
VERSION=$(git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo "1.0.0")
VERSION_COMMA=$(echo "$VERSION" | sed 's/\./, /g')

# Get current year
CURRENT_YEAR=$(date +%Y)

# Try to get repository description from GitHub API
REPO_URL=$(git config --get remote.origin.url | sed 's/\.git$//' | sed 's|^git@github.com:|https://github.com/|')
REPO_OWNER=$(echo "$REPO_URL" | sed 's|https://github.com/||' | cut -d'/' -f1)
REPO_NAME=$(echo "$REPO_URL" | sed 's|https://github.com/||' | cut -d'/' -f2)

# Try to fetch description via GitHub API (no auth required for public repos)
DESCRIPTION=""
if command -v curl &> /dev/null; then
    DESCRIPTION=$(curl -s "https://api.github.com/repos/$REPO_OWNER/$REPO_NAME" | grep '"description"' | head -1 | sed 's/.*"description": "\(.*\)",/\1/')
fi

# Fallback to default if GitHub API fails
if [ -z "$DESCRIPTION" ]; then
    DESCRIPTION="Paradox/BDE database file converter"
fi

# Get company name from repository owner
COMPANY_NAME="$REPO_OWNER"

# Generate the resource file
cat > "$OUTPUT_FILE" << EOF
#include <windows.h>

#define VER_FILEVERSION             $VERSION_COMMA, 0
#define VER_FILEVERSION_STR         "$VERSION.0"

#define VER_PRODUCTVERSION          $VERSION_COMMA, 0
#define VER_PRODUCTVERSION_STR      "$VERSION"

#define VER_COMPANYNAME_STR         "$COMPANY_NAME"
#define VER_FILEDESCRIPTION_STR     "$DESCRIPTION"
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
echo "  Company: $COMPANY_NAME"
echo "  Description: $DESCRIPTION"
echo "  Copyright: Copyright (C) $CURRENT_YEAR"
