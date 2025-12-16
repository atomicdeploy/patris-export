# Windows Resource Files

This directory contains Windows resource (.rc) files that provide version information for Windows executables.

## Files

Currently, this directory only contains this README. The patris-export executable resource file is located at `cmd/patris-export/patris-export.rc`.

For the pxlib.dll library, we use the upstream version information from the pxlib project itself, which is automatically configured during the build process.

## Usage

Resource files are automatically compiled by the build workflows:
- `.github/workflows/build.yml` - MinGW cross-compilation from Linux to Windows
- `.github/workflows/build-windows.yml` - Windows MSVC build

The resource files are compiled using `windres` (Windows Resource Compiler) and embedded into the executables/DLLs during the build process.

## Updating Version Information

For patris-export:
1. Edit `cmd/patris-export/patris-export.rc`
2. Update the version numbers in both the `#define` statements and the `VERSIONINFO` block
3. Rebuild the project

For pxlib:
- Version information comes from the upstream pxlib project and is automatically configured by CMake during the build.
