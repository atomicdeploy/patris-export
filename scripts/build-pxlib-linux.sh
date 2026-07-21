#!/usr/bin/env bash
set -euo pipefail

PREFIX="${1:?Usage: $0 <install-prefix>}"
WORK_DIR="${PXLIB_WORK_DIR:-/tmp/patris-pxlib-linux}"
PXLIB_REPO="${PXLIB_REPO:-https://github.com/steinm/pxlib.git}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PXLIB_REF="${PXLIB_REF:-$(tr -d '[:space:]' < "$REPO_ROOT/dependencies/pxlib.ref")}"
SRC_DIR="$WORK_DIR/src"
BUILD_DIR="$WORK_DIR/build"

rm -rf "$WORK_DIR"
mkdir -p "$WORK_DIR" "$PREFIX"

git clone "$PXLIB_REPO" "$SRC_DIR"
git -C "$SRC_DIR" checkout --detach "$PXLIB_REF"

sed -i 's/#include <Windows\.h>/#include <windows.h>/g' "$SRC_DIR/src/paradox.c" 2>/dev/null || true
sed -i 's/#include <Winbase\.h>/#include <winbase.h>/g' "$SRC_DIR/src/paradox.c" 2>/dev/null || true
"$REPO_ROOT/scripts/patch-pxlib-cmake.sh" "$SRC_DIR"

cmake -S "$SRC_DIR" -B "$BUILD_DIR" \
    -DCMAKE_BUILD_TYPE=Release \
    -DENABLE_GSF=OFF \
    -DCMAKE_INSTALL_PREFIX="$PREFIX"
cmake --build "$BUILD_DIR" --config Release --parallel "$(nproc 2>/dev/null || echo 2)"
cmake --install "$BUILD_DIR" --prefix "$PREFIX"

mkdir -p "$PREFIX/include" "$PREFIX/lib"
if [ ! -f "$PREFIX/include/paradox.h" ]; then
    cp "$SRC_DIR"/include/*.h "$PREFIX/include/" 2>/dev/null || true
    cp "$BUILD_DIR"/include/*.h "$PREFIX/include/" 2>/dev/null || true
fi
find "$BUILD_DIR" -maxdepth 3 \( -name 'libpx*.a' -o -name 'libpx*.so*' \) -exec cp {} "$PREFIX/lib/" \; 2>/dev/null || true
static_objects="$(find "$BUILD_DIR" -path '*/CMakeFiles/pxlib.dir/objects.a' -print -quit)"
if [ -n "$static_objects" ]; then
    cp "$static_objects" "$PREFIX/lib/libpx_static.a"
else
    mapfile -d '' pxlib_objects < <(find "$BUILD_DIR/CMakeFiles/pxlib.dir" -type f -name '*.o' -print0 | LC_ALL=C sort -z)
    if [ "${#pxlib_objects[@]}" -eq 0 ]; then
        echo "pxlib build did not produce object files for the static backend" >&2
        exit 1
    fi
    "${AR:-ar}" rcs "$PREFIX/lib/libpx_static.a" "${pxlib_objects[@]}"
fi

if ls "$PREFIX/lib"/libpxlib.* >/dev/null 2>&1 && ! ls "$PREFIX/lib"/libpx.* >/dev/null 2>&1; then
    for lib in "$PREFIX/lib"/libpxlib.*; do
        cp "$lib" "${lib/libpxlib/libpx}"
    done
fi

test -f "$PREFIX/include/paradox.h"
test -f "$PREFIX/lib/libpx_static.a"
ls "$PREFIX/lib"/libpx.* >/dev/null 2>&1
echo "pxlib installed to $PREFIX"
