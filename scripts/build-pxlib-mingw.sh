#!/usr/bin/env bash
set -euo pipefail

PREFIX="${1:?Usage: $0 <install-prefix>}"
WORK_DIR="${PXLIB_WORK_DIR:-/tmp/patris-pxlib-mingw}"
PXLIB_REPO="${PXLIB_REPO:-https://github.com/steinm/pxlib.git}"
PXLIB_REF="${PXLIB_REF:-}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC_DIR="$WORK_DIR/src"
BUILD_DIR="$WORK_DIR/build"

rm -rf "$WORK_DIR"
mkdir -p "$WORK_DIR" "$PREFIX"

git clone "$PXLIB_REPO" "$SRC_DIR"
if [ -n "$PXLIB_REF" ]; then
    git -C "$SRC_DIR" checkout "$PXLIB_REF"
fi

sed -i 's/#include <Windows\.h>/#include <windows.h>/g' "$SRC_DIR/src/paradox.c" 2>/dev/null || true
sed -i 's/#include <Winbase\.h>/#include <winbase.h>/g' "$SRC_DIR/src/paradox.c" 2>/dev/null || true
"$REPO_ROOT/scripts/patch-pxlib-cmake.sh" "$SRC_DIR"

cmake_args=(
    -S "$SRC_DIR"
    -B "$BUILD_DIR"
    -DCMAKE_BUILD_TYPE=Release
    -DENABLE_GSF=OFF
    -DCMAKE_INSTALL_PREFIX="$PREFIX"
)

if [ "${PXLIB_MINGW_CROSS:-0}" = "1" ]; then
    TOOLCHAIN_FILE="$WORK_DIR/mingw-toolchain.cmake"
    cat > "$TOOLCHAIN_FILE" <<'EOF'
set(CMAKE_SYSTEM_NAME Windows)
set(CMAKE_SYSTEM_PROCESSOR x86_64)
set(CMAKE_C_COMPILER x86_64-w64-mingw32-gcc)
set(CMAKE_CXX_COMPILER x86_64-w64-mingw32-g++)
set(CMAKE_RC_COMPILER x86_64-w64-mingw32-windres)
set(CMAKE_FIND_ROOT_PATH /usr/x86_64-w64-mingw32)
set(CMAKE_FIND_ROOT_PATH_MODE_PROGRAM NEVER)
set(CMAKE_FIND_ROOT_PATH_MODE_LIBRARY ONLY)
set(CMAKE_FIND_ROOT_PATH_MODE_INCLUDE ONLY)
EOF
    cmake_args+=(-DCMAKE_TOOLCHAIN_FILE="$TOOLCHAIN_FILE")
else
    cmake_args+=(-G "MinGW Makefiles")
fi

cmake "${cmake_args[@]}"
cmake --build "$BUILD_DIR" --config Release --parallel "${NUMBER_OF_PROCESSORS:-$(nproc 2>/dev/null || echo 2)}"
cmake --install "$BUILD_DIR" --prefix "$PREFIX"

mkdir -p "$PREFIX/include" "$PREFIX/lib" "$PREFIX/bin"
if [ ! -f "$PREFIX/include/paradox.h" ]; then
    cp "$SRC_DIR"/include/*.h "$PREFIX/include/" 2>/dev/null || true
    cp "$BUILD_DIR"/include/*.h "$PREFIX/include/" 2>/dev/null || true
fi
find "$BUILD_DIR" -maxdepth 3 \( -name 'libpx*.a' -o -name 'libpx*.dll.a' \) -exec cp {} "$PREFIX/lib/" \; 2>/dev/null || true
find "$BUILD_DIR" -maxdepth 3 -name '*.dll' -exec cp {} "$PREFIX/bin/" \; 2>/dev/null || true

if ls "$PREFIX/lib"/libpx.* >/dev/null 2>&1 && ! ls "$PREFIX/lib"/libpxlib.* >/dev/null 2>&1; then
    for lib in "$PREFIX/lib"/libpx.*; do
        cp "$lib" "${lib/libpx/libpxlib}"
    done
fi

test -f "$PREFIX/include/paradox.h"
ls "$PREFIX/lib"/libpxlib.* >/dev/null 2>&1
echo "pxlib installed to $PREFIX"
