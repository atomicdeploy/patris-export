#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="$ROOT_DIR/build"
DEPS_DIR="$ROOT_DIR/.deps"
SOURCE_VERSION="$(sed -nE 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' "$ROOT_DIR/pkg/version/version.go" | head -n 1)"
VERSION="${VERSION:-$SOURCE_VERSION}"
VERSION="${VERSION#v}"
if [[ ! "$VERSION" =~ ^[0-9]+(\.[0-9]+)*(-[a-zA-Z0-9._-]+)?$ ]]; then
    echo "Invalid VERSION '$VERSION' or source version metadata." >&2
    exit 1
fi
VERSION_PKG="github.com/atomicdeploy/patris-export/pkg/version"
BUILD_DATE="${BUILD_DATE:-$(date -u +'%Y-%m-%dT%H:%M:%SZ')}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short=12 HEAD 2>/dev/null || echo unknown)}"
export VERSION BUILD_DATE COMMIT
TARGET="current"
CI_MODE="${CI:-0}"
SKIP_PXLIB=0
SKIP_WEB=0
SKIP_ASSETS=0
RUN_TESTS=0

if [ -t 1 ]; then
    BOLD=$'\033[1m'
    DIM=$'\033[2m'
    RED=$'\033[31m'
    GREEN=$'\033[32m'
    YELLOW=$'\033[33m'
    CYAN=$'\033[36m'
    RESET=$'\033[0m'
else
    BOLD=""
    DIM=""
    RED=""
    GREEN=""
    YELLOW=""
    CYAN=""
    RESET=""
fi

usage() {
    cat <<'EOF'
Patris Export build helper

Usage:
  ./build.sh [options]

Options:
  --target <name>     Build target: current, linux, windows-cross, windows-native, windows, all
  --ci                CI mode: deterministic paths and non-interactive output
  --skip-pxlib        Use existing CGO/PXLIB_ROOT settings instead of building upstream pxlib
  --no-web            Skip npm install and web frontend build
  --no-assets         Skip optional asset regeneration
  --test              Run go test ./... after building
  -h, --help          Show this help

Environment:
  VERSION             Version string embedded in the binary (default: pkg/version/version.go)
  PXLIB_ROOT          Existing pxlib install prefix to use
  PXLIB_REPO/PXLIB_REF Override upstream pxlib source and ref
  USE_VCPKG=1         Adds VCPKG_ROOT installed include/lib/bin paths as optional C dependency paths
EOF
}

log() { printf '%s%s%s\n' "${CYAN}" "$*" "${RESET}"; }
ok() { printf '%s✅ %s%s\n' "${GREEN}" "$*" "${RESET}"; }
warn() { printf '%s⚠️  %s%s\n' "${YELLOW}" "$*" "${RESET}"; }
fail() { printf '%s❌ %s%s\n' "${RED}" "$*" "${RESET}" >&2; exit 1; }
step() { printf '\n%s%s%s\n' "${BOLD}" "$*" "${RESET}"; }

have() { command -v "$1" >/dev/null 2>&1; }
need() {
    local name="$1"
    local hint="${2:-Install $1 and retry.}"
    have "$name" || fail "Required tool not found: $name. $hint"
}

is_windows_shell() {
    case "$(uname -s 2>/dev/null || echo unknown)" in
        MINGW*|MSYS*|CYGWIN*) return 0 ;;
        *) return 1 ;;
    esac
}

to_windows_path() {
    local value="$1"
    if is_windows_shell && have cygpath; then
        cygpath -m "$value"
    else
        printf '%s' "$value"
    fi
}

ldflags() {
    printf -- '-X %s.Version=%s -X %s.BuildDate=%s -X %s.Commit=%s' \
        "$VERSION_PKG" "$VERSION" "$VERSION_PKG" "$BUILD_DATE" "$VERSION_PKG" "$COMMIT"
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --target)
            [ "$#" -ge 2 ] || fail "--target requires a value"
            TARGET="$2"
            shift 2
            ;;
        --target=*)
            TARGET="${1#*=}"
            shift
            ;;
        --ci)
            CI_MODE=1
            shift
            ;;
        --skip-pxlib)
            SKIP_PXLIB=1
            shift
            ;;
        --no-web|--skip-web)
            SKIP_WEB=1
            shift
            ;;
        --no-assets|--skip-assets)
            SKIP_ASSETS=1
            shift
            ;;
        --test)
            RUN_TESTS=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            fail "Unknown option: $1"
            ;;
    esac
done

if [ "$TARGET" = "current" ]; then
    if is_windows_shell; then
        TARGET="windows-native"
    else
        TARGET="linux"
    fi
fi
if [ "$TARGET" = "windows" ]; then
    if is_windows_shell; then
        TARGET="windows-native"
    else
        TARGET="windows-cross"
    fi
fi

if [ "$TARGET" = "all" ]; then
    extra=()
    [ "$CI_MODE" = "1" ] && extra+=(--ci)
    [ "$SKIP_WEB" = "1" ] && extra+=(--no-web)
    [ "$SKIP_ASSETS" = "1" ] && extra+=(--no-assets)
    [ "$SKIP_PXLIB" = "1" ] && extra+=(--skip-pxlib)
    "$0" --target linux "${extra[@]}"
    "$0" --target windows-cross "${extra[@]}"
    exit 0
fi

cd "$ROOT_DIR"
if is_windows_shell && have cygpath; then
    [ -n "${PXLIB_ROOT:-}" ] && PXLIB_ROOT="$(cygpath -u "$PXLIB_ROOT")" && export PXLIB_ROOT
    [ -n "${VCPKG_ROOT:-}" ] && VCPKG_ROOT="$(cygpath -u "$VCPKG_ROOT")" && export VCPKG_ROOT
fi

step "🏗️  Patris Export build"
log "Target: ${BOLD}$TARGET${RESET}"
log "Version: ${VERSION}  Commit: ${COMMIT}  Date: ${BUILD_DATE}"

need git "Git is required for version metadata and upstream pxlib fetches."
    need go "Install Go 1.25 or newer."
if [ "$SKIP_WEB" -eq 0 ]; then
    need npm "Install Node.js/npm 24 or newer."
fi

run_assets() {
    [ "$SKIP_ASSETS" -eq 0 ] || return 0
    if have magick || have convert; then
        step "🎨 Rebuilding assets"
        bash "$ROOT_DIR/scripts/rebuild-assets.sh"
    elif [ -f "$ROOT_DIR/assets/windows/patris-api.ico" ] && [ -f "$ROOT_DIR/web/assets/favicon.ico" ]; then
        warn "ImageMagick not found; using checked-in icon/favicon assets."
    else
        fail "ImageMagick is required because generated icon assets are missing."
    fi
}

run_web() {
    [ "$SKIP_WEB" -eq 0 ] || return 0
    step "🌐 Building web frontend"
    (cd "$ROOT_DIR/web" && npm ci && npm run build)
}

run_tests() {
    [ "$RUN_TESTS" -eq 1 ] || return 0
    step "🧪 Running Go tests"
    go test ./...
}

ensure_python3() {
    if command -v python3 >/dev/null 2>&1 && python3 -c 'import sys' >/dev/null 2>&1; then
        return 0
    fi
    local shim_dir="$DEPS_DIR/build-bin"
    mkdir -p "$shim_dir"
    if command -v python >/dev/null 2>&1 && python -c 'import sys' >/dev/null 2>&1; then
        cat > "$shim_dir/python3" <<'EOF'
#!/usr/bin/env bash
exec python "$@"
EOF
    elif command -v py >/dev/null 2>&1 && py -3 -c 'import sys' >/dev/null 2>&1; then
        cat > "$shim_dir/python3" <<'EOF'
#!/usr/bin/env bash
exec py -3 "$@"
EOF
    else
        fail "Python 3 is required to generate Windows resources."
    fi
    chmod +x "$shim_dir/python3"
    export PATH="$shim_dir:$PATH"
}

use_optional_vcpkg() {
    if [[ "${USE_VCPKG:-}" =~ ^(1|true|yes|on)$ ]]; then
        local vcpkg_root="${VCPKG_ROOT:-$DEPS_DIR/vcpkg}"
        local triplet="${VCPKG_DEFAULT_TRIPLET:-x64-windows}"
        local installed="$vcpkg_root/installed/$triplet"
        if [ -d "$installed" ]; then
            warn "Using optional vcpkg C dependency paths: $installed"
            export CGO_CFLAGS="${CGO_CFLAGS:-} -I$(to_windows_path "$installed")/include"
            export CGO_LDFLAGS="${CGO_LDFLAGS:-} -L$(to_windows_path "$installed")/lib -L$(to_windows_path "$installed")/bin"
            export PATH="$installed/bin:$PATH"
        fi
    fi
}

prepare_linux_pxlib() {
    if [ -n "${PXLIB_ROOT:-}" ] && [ -f "$PXLIB_ROOT/include/paradox.h" ]; then
        warn "Using pxlib from PXLIB_ROOT=$PXLIB_ROOT"
        export CGO_CFLAGS="-I$PXLIB_ROOT/include ${CGO_CFLAGS:-}"
        export CGO_LDFLAGS="-L$PXLIB_ROOT/lib ${CGO_LDFLAGS:-}"
        export LD_LIBRARY_PATH="$PXLIB_ROOT/lib:${LD_LIBRARY_PATH:-}"
        return 0
    fi
    if [ "$SKIP_PXLIB" -eq 1 ]; then
        warn "Skipping pxlib build; using existing CGO_CFLAGS/CGO_LDFLAGS."
        return 0
    fi
    need cmake "Install CMake or use --skip-pxlib with system pxlib-dev."
    step "📦 Building upstream pxlib for Linux"
    local prefix="$DEPS_DIR/pxlib-linux"
    bash "$ROOT_DIR/scripts/build-pxlib-linux.sh" "$prefix"
    export CGO_CFLAGS="-I$prefix/include ${CGO_CFLAGS:-}"
    export CGO_LDFLAGS="-L$prefix/lib ${CGO_LDFLAGS:-}"
    export LD_LIBRARY_PATH="$prefix/lib:${LD_LIBRARY_PATH:-}"
}

prepare_windows_pxlib() {
    local prefix="$1"
    if [ -n "${PXLIB_ROOT:-}" ] && [ -f "$PXLIB_ROOT/include/paradox.h" ]; then
        warn "Using pxlib from PXLIB_ROOT=$PXLIB_ROOT"
        prefix="$PXLIB_ROOT"
        export PXLIB_ROOT="$prefix"
        export CGO_CFLAGS="-I$(to_windows_path "$prefix")/include ${CGO_CFLAGS:-}"
        export CGO_LDFLAGS="-L$(to_windows_path "$prefix")/lib -L$(to_windows_path "$prefix")/bin ${CGO_LDFLAGS:-}"
        return 0
    fi
    if [ "$SKIP_PXLIB" -eq 1 ]; then
        warn "Skipping pxlib build; using existing CGO_CFLAGS/CGO_LDFLAGS."
        return 0
    else
        need cmake "Install CMake for upstream pxlib builds."
        step "📦 Building upstream pxlib for Windows"
        if [ "$TARGET" = "windows-cross" ]; then
            export PXLIB_MINGW_CROSS=1
        fi
        bash "$ROOT_DIR/scripts/build-pxlib-mingw.sh" "$prefix"
    fi
    export PXLIB_ROOT="$prefix"
    export CGO_CFLAGS="-I$(to_windows_path "$prefix")/include ${CGO_CFLAGS:-}"
    export CGO_LDFLAGS="-L$(to_windows_path "$prefix")/lib -L$(to_windows_path "$prefix")/bin ${CGO_LDFLAGS:-}"
}

copy_windows_dlls() {
    local prefix="${PXLIB_ROOT:-}"
    mkdir -p "$BUILD_DIR"
    if [ -n "$prefix" ] && [ -d "$prefix/bin" ]; then
        for dll in "$prefix"/bin/*.dll; do
            [ -f "$dll" ] && cp "$dll" "$BUILD_DIR/" || true
        done
    fi
    local gcc_path
    gcc_path="$(command -v "${CC:-gcc}" 2>/dev/null || true)"
    if [ -n "$gcc_path" ]; then
        local gcc_dir
        gcc_dir="$(dirname "$gcc_path")"
        for dll in libgcc_s_seh-1.dll libwinpthread-1.dll libstdc++-6.dll; do
            [ -f "$gcc_dir/$dll" ] && cp "$gcc_dir/$dll" "$BUILD_DIR/" || true
        done
    fi
}

compile_windows_resources() {
    local windres_name="$1"
    step "🪟 Compiling Win32 icon/version resources"
    ensure_python3
    bash "$ROOT_DIR/scripts/generate-version-rc.sh" cmd/patris-export/patris-export.rc
    "$windres_name" \
        --target=pe-x86-64 \
        -i cmd/patris-export/patris-export.rc \
        -o cmd/patris-export/patris-export_windows_amd64.syso \
        -O coff
}

build_linux() {
    prepare_linux_pxlib
    step "🐧 Building Linux executable and shared library"
    make build-linux build-lib-linux
    run_tests
}

build_windows_cross() {
    need x86_64-w64-mingw32-gcc "Install mingw-w64 cross GCC."
    need x86_64-w64-mingw32-windres "Install mingw-w64 windres."
    export CC=x86_64-w64-mingw32-gcc
    prepare_windows_pxlib "$DEPS_DIR/pxlib-windows"
    use_optional_vcpkg
    run_assets
    run_web
    compile_windows_resources x86_64-w64-mingw32-windres
    mkdir -p "$BUILD_DIR"
    step "🪟 Building Windows executable and DLL"
    CGO_ENABLED=1 GOOS=windows GOARCH=amd64 "$GO_BINARY" build -ldflags "$(ldflags)" -o "$BUILD_DIR/patris-export-windows-amd64.exe" ./cmd/patris-export
    CGO_ENABLED=1 GOOS=windows GOARCH=amd64 "$GO_BINARY" build -buildmode=c-shared -ldflags "$(ldflags)" -o "$BUILD_DIR/patris-export.dll" ./cmd/patris-export-lib
    copy_windows_dlls
    run_tests
}

build_windows_native() {
    need gcc "Install MSYS2/MinGW or WinLibs GCC and put it on PATH."
    local windres_name
    windres_name="$(command -v windres 2>/dev/null || command -v x86_64-w64-mingw32-windres 2>/dev/null || true)"
    [ -n "$windres_name" ] || fail "Required tool not found: windres."
    export CC="${CC:-gcc}"
    prepare_windows_pxlib "$DEPS_DIR/pxlib-windows-native"
    use_optional_vcpkg
    run_assets
    run_web
    compile_windows_resources "$windres_name"
    mkdir -p "$BUILD_DIR"
    step "🪟 Building native Windows executable and DLL"
    CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -ldflags "$(ldflags)" -o "$BUILD_DIR/patris-export-windows-amd64.exe" ./cmd/patris-export
    CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -buildmode=c-shared -ldflags "$(ldflags)" -o "$BUILD_DIR/patris-export.dll" ./cmd/patris-export-lib
    copy_windows_dlls
    run_tests
}

GO_BINARY="$(command -v go)"
mkdir -p "$BUILD_DIR" "$DEPS_DIR"

case "$TARGET" in
    linux) build_linux ;;
    windows-cross) build_windows_cross ;;
    windows-native) build_windows_native ;;
    *) fail "Unsupported target: $TARGET" ;;
esac

step "📦 Build artifacts"
artifact_count=0
for artifact in "$BUILD_DIR"/*; do
    [ -f "$artifact" ] || continue
    artifact_count=$((artifact_count + 1))
    size="$(wc -c < "$artifact" | tr -d '[:space:]')"
    printf '  %s  %s bytes\n' "$(basename "$artifact")" "$size"
done
[ "$artifact_count" -gt 0 ] || warn "No files found in $BUILD_DIR"
ok "Build complete"
