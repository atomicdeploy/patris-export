# Windows Resource Files

This directory contains Windows resource (.rc) files that provide version information for Windows executables and DLLs.

## Files

### pxlib.rc
Resource file for the pxlib.dll library. Contains:
- File version: 0.6.7.0
- Product version: 0.6.7
- Product name: pxlib
- Description: Paradox database library

This file is copied into the pxlib build directory during the CI/CD workflows and compiled into the DLL to provide version information visible in Windows file properties.

## Usage

These resource files are automatically used by the build workflows:
- `.github/workflows/build.yml` - Cross-compilation from Linux to Windows
- `.github/workflows/build-windows.yml` - Native Windows build

The resource files are compiled using `windres` (Windows Resource Compiler) and embedded into the executables/DLLs during the build process.

## Updating Version Information

To update version information:

1. Edit the appropriate .rc file
2. Update the version numbers in both the `#define` statements and the `VERSIONINFO` block
3. Rebuild the project

The patris-export executable resource file is located at `cmd/patris-export/patris-export.rc`.
