#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEFAULT_LOGO="$HOME/Downloads/Patris_API_Logo.png"
if [ ! -f "$DEFAULT_LOGO" ]; then
    DEFAULT_LOGO="$ROOT_DIR/assets/windows/patris-api-icon.png"
fi
LOGO_SOURCE="${1:-${PATRIS_LOGO_SOURCE:-$DEFAULT_LOGO}}"
ICON_PNG="${PATRIS_ICON_PNG:-$ROOT_DIR/assets/windows/patris-api-icon.png}"
ICON_ICO="${PATRIS_ICON_ICO:-$ROOT_DIR/assets/windows/patris-api.ico}"
ICON_SIZES="${PATRIS_ICON_SIZES:-256,128,64,48,32,24,16}"
WEB_ICON_PNG="${PATRIS_WEB_ICON_PNG:-$ROOT_DIR/web/assets/patris-api-icon.png}"
WEB_FAVICON_ICO="${PATRIS_WEB_FAVICON_ICO:-$ROOT_DIR/web/assets/favicon.ico}"
NOTIFICATION_AUDIO="${PATRIS_NOTIFICATION_AUDIO:-$ROOT_DIR/web/assets/notification.ogg}"

if command -v magick >/dev/null 2>&1; then
    MAGICK=(magick)
elif command -v convert >/dev/null 2>&1; then
    MAGICK=(convert)
else
    echo "ImageMagick is required to rebuild Windows icon assets." >&2
    exit 1
fi

if [ ! -f "$LOGO_SOURCE" ]; then
    echo "Logo source not found: $LOGO_SOURCE" >&2
    exit 1
fi

mkdir -p "$(dirname "$ICON_PNG")"
GEOMETRY="$("${MAGICK[@]}" "$LOGO_SOURCE" -alpha extract -threshold 0 -format "%@" info:)"
if [ -z "$GEOMETRY" ]; then
    echo "Could not determine non-transparent logo bounds." >&2
    exit 1
fi
"${MAGICK[@]}" "$LOGO_SOURCE" -alpha set -crop "$GEOMETRY" +repage -background none \
    -gravity center -extent '%[fx:max(w,h)]x%[fx:max(w,h)]' "$ICON_PNG"
"${MAGICK[@]}" "$ICON_PNG" -define icon:auto-resize="$ICON_SIZES" "$ICON_ICO"
mkdir -p "$(dirname "$WEB_ICON_PNG")" "$(dirname "$WEB_FAVICON_ICO")"
cp "$ICON_PNG" "$WEB_ICON_PNG"
cp "$ICON_ICO" "$WEB_FAVICON_ICO"

if [ ! -f "$NOTIFICATION_AUDIO" ]; then
    echo "Notification audio missing: $NOTIFICATION_AUDIO" >&2
    exit 1
fi

echo "Rebuilt assets:"
echo "  $ICON_PNG"
echo "  $ICON_ICO"
echo "  $WEB_ICON_PNG"
echo "  $WEB_FAVICON_ICO"
echo "  $NOTIFICATION_AUDIO"
