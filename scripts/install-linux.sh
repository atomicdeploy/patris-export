#!/usr/bin/env bash
set -euo pipefail

ACTION="${1:-install}"
SERVICE_NAME="${PATRIS_EXPORT_SERVICE_NAME:-patris-export}"
APP_USER="${PATRIS_EXPORT_USER:-patris-export}"
APP_GROUP="${PATRIS_EXPORT_GROUP:-$APP_USER}"
PREFIX="${PATRIS_EXPORT_PREFIX:-/opt/patris-export}"
BIN_DIR="${PATRIS_EXPORT_BIN_DIR:-$PREFIX/bin}"
LIB_DIR="${PATRIS_EXPORT_LIB_DIR:-$PREFIX/lib}"
CONFIG_DIR="${PATRIS_EXPORT_CONFIG_DIR:-/etc/patris-export}"
ENV_FILE="${PATRIS_EXPORT_ENV_FILE:-$CONFIG_DIR/patris-export.env}"
UNIT_FILE="${PATRIS_EXPORT_UNIT_FILE:-/etc/systemd/system/$SERVICE_NAME.service}"
DB_PATH="${PATRIS_EXPORT_DB_PATH:-/var/lib/patris-export/kala.db}"
ADDR="${PATRIS_EXPORT_ADDR:-:8080}"
DEBOUNCE="${PATRIS_EXPORT_DEBOUNCE:-500ms}"
NATIVE_TOASTS="${PATRIS_EXPORT_NATIVE_TOASTS:-false}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_VERSION="$(sed -nE 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' "$REPO_ROOT/pkg/version/version.go" | head -n 1)"
VERSION="${VERSION:-$SOURCE_VERSION}"
VERSION_PKG="github.com/atomicdeploy/patris-export/pkg/version"
COMMIT="${COMMIT:-$(git -C "$REPO_ROOT" rev-parse --short=12 HEAD 2>/dev/null || echo unknown)}"
BUILD_DIR="${BUILD_DIR:-build}"
PXLIB_PREFIX="${PXLIB_PREFIX:-$PREFIX/pxlib}"

need_root() {
    if [ "$(id -u)" -ne 0 ]; then
        echo "This action requires root. Re-run with sudo." >&2
        exit 1
    fi
}

has_systemd() {
    command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]
}

systemctl_maybe() {
    if has_systemd; then
        systemctl "$@"
    else
        echo "systemd is not active; skipped: systemctl $*" >&2
    fi
}

ensure_user() {
    if ! getent group "$APP_GROUP" >/dev/null; then
        groupadd --system "$APP_GROUP"
    fi
    if ! id "$APP_USER" >/dev/null 2>&1; then
        useradd --system --gid "$APP_GROUP" --home-dir "$PREFIX" --shell /usr/sbin/nologin "$APP_USER"
    fi
}

build_pxlib_if_needed() {
    if [ -f "$PXLIB_PREFIX/include/paradox.h" ] && ls "$PXLIB_PREFIX/lib"/libpx.* >/dev/null 2>&1; then
        return
    fi

    if command -v cmake >/dev/null 2>&1 && command -v git >/dev/null 2>&1; then
        "$REPO_ROOT/scripts/build-pxlib-linux.sh" "$PXLIB_PREFIX"
    else
        echo "cmake/git missing; falling back to system pxlib packages if present." >&2
    fi
}

build_binary() {
    cd "$REPO_ROOT"
    mkdir -p "$BUILD_DIR"

    build_pxlib_if_needed
    if [ -f "$PXLIB_PREFIX/include/paradox.h" ]; then
        export CGO_CFLAGS="-I$PXLIB_PREFIX/include ${CGO_CFLAGS:-}"
        export CGO_LDFLAGS="-L$PXLIB_PREFIX/lib ${CGO_LDFLAGS:-}"
        export LD_LIBRARY_PATH="$PXLIB_PREFIX/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
    fi

    (cd web && npm ci && npm run build)
    CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build \
        -ldflags "-X $VERSION_PKG.Version=$VERSION -X $VERSION_PKG.BuildDate=$(date -u +'%Y-%m-%dT%H:%M:%SZ') -X $VERSION_PKG.Commit=$COMMIT" \
        -o "$BUILD_DIR/patris-export-linux-amd64" ./cmd/patris-export
    cp "$BUILD_DIR/patris-export-linux-amd64" "$BUILD_DIR/patris-export"
}

env_content() {
    cat <<EOF
PATRIS_EXPORT_DB_PATH="$DB_PATH"
PATRIS_EXPORT_ADDR="$ADDR"
PATRIS_EXPORT_DEBOUNCE="$DEBOUNCE"
PATRIS_EXPORT_EXTRA_ARGS=""
PATRIS_EXPORT_NATIVE_TOASTS="$NATIVE_TOASTS"
LD_LIBRARY_PATH="$PXLIB_PREFIX/lib:$LIB_DIR"
EOF
}

write_env_file() {
    install -d -m 0755 "$CONFIG_DIR"
    env_content > "$ENV_FILE"
    chmod 0644 "$ENV_FILE"
}

unit_content() {
    cat <<EOF
[Unit]
Description=Patris Export API and web UI
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$APP_USER
Group=$APP_GROUP
EnvironmentFile=$ENV_FILE
WorkingDirectory=$PREFIX
ExecStart=$BIN_DIR/patris-export serve \${PATRIS_EXPORT_DB_PATH} --addr \${PATRIS_EXPORT_ADDR} --debounce \${PATRIS_EXPORT_DEBOUNCE} \${PATRIS_EXPORT_EXTRA_ARGS}
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=/tmp /var/tmp

[Install]
WantedBy=multi-user.target
EOF
}

write_unit_file() {
    unit_content > "$UNIT_FILE"
    chmod 0644 "$UNIT_FILE"
}

install_app() {
    need_root
    ensure_user
    build_binary

    install -d -m 0755 "$BIN_DIR" "$LIB_DIR" "$PREFIX"
    install -m 0755 "$REPO_ROOT/$BUILD_DIR/patris-export" "$BIN_DIR/patris-export"
    if command -v ln >/dev/null 2>&1 && [ "$BIN_DIR" != "/usr/local/bin" ]; then
        ln -sf "$BIN_DIR/patris-export" /usr/local/bin/patris-export
    fi

    if [ -d "$PXLIB_PREFIX/lib" ]; then
        find "$PXLIB_PREFIX/lib" -maxdepth 1 -type f \( -name 'libpx*.so*' -o -name 'libpx*.a' \) -exec cp -a {} "$LIB_DIR/" \;
    fi

    write_env_file
    write_unit_file
    chown -R "$APP_USER:$APP_GROUP" "$PREFIX"
    systemctl_maybe daemon-reload

    echo "Installed $SERVICE_NAME"
    echo "  Binary: $BIN_DIR/patris-export"
    echo "  Config: $ENV_FILE"
    echo "  Unit:   $UNIT_FILE"
}

uninstall_app() {
    need_root
    systemctl_maybe stop "$SERVICE_NAME" || true
    systemctl_maybe disable "$SERVICE_NAME" || true
    rm -f "$UNIT_FILE"
    systemctl_maybe daemon-reload
    rm -rf "$PREFIX" "$CONFIG_DIR"
    echo "Removed $SERVICE_NAME files. User/group are left in place."
}

case "$ACTION" in
    install) install_app ;;
    uninstall|remove) uninstall_app ;;
    enable) need_root; systemctl_maybe enable "$SERVICE_NAME" ;;
    disable) need_root; systemctl_maybe disable "$SERVICE_NAME" ;;
    start) need_root; systemctl_maybe start "$SERVICE_NAME" ;;
    stop) need_root; systemctl_maybe stop "$SERVICE_NAME" ;;
    restart) need_root; systemctl_maybe restart "$SERVICE_NAME" ;;
    status) systemctl_maybe status "$SERVICE_NAME" ;;
    env) need_root; write_env_file ;;
    unit) need_root; write_unit_file ;;
    print-env) env_content ;;
    print-unit) unit_content ;;
    *)
        echo "Usage: $0 {install|uninstall|enable|disable|start|stop|restart|status|env|unit|print-env|print-unit}" >&2
        exit 2
        ;;
esac
