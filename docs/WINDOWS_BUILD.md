# Building pxlib for Windows

This document explains how to build the pxlib library for Windows, which is required for the Windows build of patris-export.

## CI/CD Builds

The project includes two automated Windows build workflows:

1. **Windows MSVC Build** (`.github/workflows/build-windows.yml`) - Builds directly on Windows runners using MSVC
2. **Windows MinGW Build** (`.github/workflows/build.yml`) - Cross-compiles from Linux to Windows using MinGW

Both workflows automatically include Windows resource files with version information for both the executable and the pxlib DLL.

## Using Pre-built DLL (Recommended)

Download the pre-built pxlib DLL from the official pxlib repository releases:

https://github.com/steinm/pxlib

The CI/CD builds in that repository produce Windows DLLs that can be used directly.

## Building from Source on Windows

### Prerequisites
- CMake 3.12 or later
- Visual Studio 2019 or later (or MinGW-w64)
- Go 1.23 or later

### Build Steps

1. Clone the pxlib repository:
```bash
git clone https://github.com/steinm/pxlib.git
cd pxlib
```

2. Build using CMake:
```bash
mkdir build
cd build
cmake .. -DCMAKE_BUILD_TYPE=Release
cmake --build . --config Release
```

3. The resulting DLL will be in `build/Release/pxlib.dll`

4. Build patris-export with version information:
```bash
cd /path/to/patris-export

# The resource file will be automatically included if windres is available
go build -o patris-export.exe ./cmd/patris-export
```

## MinGW Cross-Compilation from Linux

For cross-compiling from Linux to Windows using MinGW, you'll need:

1. MinGW-w64 cross-compiler
2. pxlib built for Windows (DLL and headers)

### Steps:

1. Install MinGW-w64:
```bash
sudo apt-get install mingw-w64 mingw-w64-tools
```

2. Build pxlib with MinGW (automated by the build workflow):
```bash
git clone https://github.com/steinm/pxlib.git
cd pxlib

# Copy the resource file for version information
cp /path/to/patris-export/resources/pxlib.rc .

mkdir build-mingw
cd build-mingw
cmake .. -DCMAKE_TOOLCHAIN_FILE=../cmake/mingw-w64-x86_64.cmake -DCMAKE_BUILD_TYPE=Release
cmake --build . --config Release
```

3. Place the resulting library files where your Go build can find them:
```bash
# Copy headers
sudo cp ../include/*.h /usr/x86_64-w64-mingw32/include/

# Copy library
sudo cp libpx.dll.a /usr/x86_64-w64-mingw32/lib/
sudo cp px.dll /usr/x86_64-w64-mingw32/bin/
```

4. Build patris-export with version information:
```bash
cd /path/to/patris-export

# Use the Makefile which automatically compiles resource files
make build-windows
```

## Windows Version Information

Windows executables and DLLs include embedded version information via resource (.rc) files:

- `cmd/patris-export/patris-export.rc` - Version info for the main executable
- `resources/pxlib.rc` - Version info for the pxlib DLL

These are automatically compiled and embedded during the build process. To manually compile resource files:

```bash
# For MinGW cross-compilation on Linux
./scripts/compile-resources.sh

# Or manually with windres
x86_64-w64-mingw32-windres -i cmd/patris-export/patris-export.rc \
  -o cmd/patris-export/patris-export_windows_amd64.syso -O coff --target=pe-x86-64
```

The version information will appear in the Windows file properties dialog.

## Using in patris-export

Once you have the pxlib DLL, you can build patris-export for Windows:

### On Windows:
```bash
go build -o patris-export.exe ./cmd/patris-export
```

### Cross-compile from Linux:
```bash
make build-windows
```

Make sure the pxlib DLL is in the same directory as the patris-export executable when running on Windows.
