#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEFAULT_LOGO="$HOME/Downloads/Patris_API_Logo.png"
if [ ! -f "$DEFAULT_LOGO" ]; then
    DEFAULT_LOGO="$ROOT_DIR/assets/patris-api-icon.png"
fi
LOGO_SOURCE="${1:-${PATRIS_EXPORT_LOGO_SOURCE:-$DEFAULT_LOGO}}"
ICON_PNG="${PATRIS_EXPORT_ICON_PNG:-$ROOT_DIR/assets/patris-api-icon.png}"
ICON_ICO="${PATRIS_EXPORT_ICON_ICO:-$ROOT_DIR/assets/windows/patris-api.ico}"
ICON_SIZES="${PATRIS_EXPORT_ICON_SIZES:-256,128,64,48,32,24,16}"
WEB_ICON_PNG="${PATRIS_EXPORT_WEB_ICON_PNG:-}"
WEB_FAVICON_ICO="${PATRIS_EXPORT_WEB_FAVICON_ICO:-$ROOT_DIR/web/assets/favicon.ico}"
NOTIFICATION_AUDIO="${PATRIS_EXPORT_NOTIFICATION_AUDIO:-$ROOT_DIR/web/assets/notification.ogg}"
CROP_ALPHA_THRESHOLD="${PATRIS_EXPORT_CROP_ALPHA_THRESHOLD:-2%}"

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
GEOMETRY="$("${MAGICK[@]}" "$LOGO_SOURCE" -alpha extract -threshold "$CROP_ALPHA_THRESHOLD" -format "%@" info:)"
if [ -z "$GEOMETRY" ]; then
    echo "Could not determine visible logo bounds with alpha threshold $CROP_ALPHA_THRESHOLD." >&2
    exit 1
fi
"${MAGICK[@]}" "$LOGO_SOURCE" -alpha set -crop "$GEOMETRY" +repage -background none -strip "$ICON_PNG"
CROPPED_GEOMETRY="$("${MAGICK[@]}" "$ICON_PNG" -alpha extract -threshold "$CROP_ALPHA_THRESHOLD" -format "%@" info:)"
CROPPED_EXPECTED="$("${MAGICK[@]}" identify -format "%wx%h+0+0" "$ICON_PNG")"
if [ "$CROPPED_GEOMETRY" != "$CROPPED_EXPECTED" ]; then
    echo "Generated icon PNG still has transparent edge padding at alpha threshold $CROP_ALPHA_THRESHOLD: bounds $CROPPED_GEOMETRY, expected $CROPPED_EXPECTED." >&2
    exit 1
fi
"${MAGICK[@]}" "$ICON_PNG" -define icon:auto-resize="$ICON_SIZES" "$ICON_ICO"
mkdir -p "$(dirname "$WEB_FAVICON_ICO")"
if [ -n "$WEB_ICON_PNG" ]; then
    mkdir -p "$(dirname "$WEB_ICON_PNG")"
    cp "$ICON_PNG" "$WEB_ICON_PNG"
fi
cp "$ICON_ICO" "$WEB_FAVICON_ICO"

if [ ! -f "$NOTIFICATION_AUDIO" ]; then
    echo "Notification audio missing: $NOTIFICATION_AUDIO" >&2
    exit 1
fi

echo "Rebuilt assets:"
echo "  Crop alpha threshold: $CROP_ALPHA_THRESHOLD"
echo "  $ICON_PNG"
echo "  $ICON_ICO"
if [ -n "$WEB_ICON_PNG" ]; then
    echo "  $WEB_ICON_PNG"
fi
echo "  $WEB_FAVICON_ICO"
echo "  $NOTIFICATION_AUDIO"
