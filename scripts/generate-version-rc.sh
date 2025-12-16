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

# Parse repository URL to get owner and name
REPO_URL=$(git config --get remote.origin.url | sed 's/\.git$//')
# Handle both SSH (git@github.com:owner/repo) and HTTPS (https://github.com/owner/repo)
if [[ "$REPO_URL" =~ ^git@github\.com:(.+)/(.+)$ ]]; then
    REPO_OWNER="${BASH_REMATCH[1]}"
    REPO_NAME="${BASH_REMATCH[2]}"
elif [[ "$REPO_URL" =~ ^https://github\.com/(.+)/(.+)$ ]]; then
    REPO_OWNER="${BASH_REMATCH[1]}"
    REPO_NAME="${BASH_REMATCH[2]}"
else
    REPO_OWNER="Unknown"
    REPO_NAME="Unknown"
fi

# Validate REPO_OWNER and REPO_NAME to prevent injection (allow alphanumeric, dash, underscore, dot)
if [[ ! "$REPO_OWNER" =~ ^[a-zA-Z0-9._-]+$ ]]; then
    echo "Warning: Invalid repository owner format, using default" >&2
    REPO_OWNER="Unknown"
fi
if [[ ! "$REPO_NAME" =~ ^[a-zA-Z0-9._-]+$ ]]; then
    echo "Warning: Invalid repository name format, using default" >&2
    REPO_NAME="Unknown"
fi

# Function to escape strings for C string literals
escape_c_string() {
    # Escape backslashes first, then quotes, then newlines
    echo "$1" | sed 's/\\/\\\\/g' | sed 's/"/\\"/g' | sed 's/$/\\n/' | tr -d '\n' | sed 's/\\n$//'
}

# Try to fetch description via GitHub API (no auth required for public repos)
DESCRIPTION=""
if command -v curl &> /dev/null && [ "$REPO_OWNER" != "Unknown" ]; then
    # Use jq if available for safer JSON parsing, otherwise fallback
    if command -v jq &> /dev/null; then
        DESCRIPTION=$(curl -s "https://api.github.com/repos/$REPO_OWNER/$REPO_NAME" | jq -r '.description // ""' 2>/dev/null || echo "")
    else
        # Safer fallback: use python if available
        if command -v python3 &> /dev/null; then
            DESCRIPTION=$(curl -s "https://api.github.com/repos/$REPO_OWNER/$REPO_NAME" | python3 -c "import sys, json; data = json.load(sys.stdin); print(data.get('description', ''))" 2>/dev/null || echo "")
        fi
    fi
fi

# Fallback to default if GitHub API fails or returns empty
if [ -z "$DESCRIPTION" ]; then
    DESCRIPTION="Paradox/BDE database file converter"
fi

# Escape strings for safe C string literal insertion
DESCRIPTION_ESCAPED=$(escape_c_string "$DESCRIPTION")
COMPANY_NAME_ESCAPED=$(escape_c_string "$REPO_OWNER")

# Generate the resource file
cat > "$OUTPUT_FILE" << EOF
#include <windows.h>

#define VER_FILEVERSION             $VERSION_COMMA, 0
#define VER_FILEVERSION_STR         "$VERSION.0"

#define VER_PRODUCTVERSION          $VERSION_COMMA, 0
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
