# Building pxlib for Windows

This document explains how to build the pxlib library for Windows, which is required for the Windows build of patris-export.

## Using Pre-built DLL (Recommended)

Download the pre-built pxlib DLL from the official pxlib repository releases:

https://github.com/steinm/pxlib

The CI/CD builds in that repository produce Windows DLLs that can be used directly.

## Building from Source on Windows

### Prerequisites
- CMake 3.12 or later
- Visual Studio 2019 or later (or MinGW-w64)

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

## Cross-Compilation from Linux

For cross-compilation from Linux to Windows, you'll need:

1. MinGW-w64 cross-compiler
2. pxlib built for Windows (DLL and headers)

### Steps:

1. Install MinGW-w64:
```bash
sudo apt-get install mingw-w64
```

2. Build pxlib with MinGW:
```bash
git clone https://github.com/steinm/pxlib.git
cd pxlib
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

4. Now you can cross-compile patris-export for Windows:
```bash
cd /path/to/patris-export
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build -o patris-export.exe ./cmd/patris-export
```

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
