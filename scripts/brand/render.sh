#!/usr/bin/env bash
# Renders the link card and the README lockups from the HTML sources next to this
# script, with headless Chrome (needs network for the IBM Plex Sans font).
# The icon files (favicon.ico, touch and app icons) come from src/resources/images/logo.svg
# and favicon.svg with rsvg-convert.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
out="$here/../../src/resources/images"
chrome="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
shot() { # <html> <width> <height> <png> [extra flags]
  "$chrome" --headless=new --disable-gpu --hide-scrollbars --virtual-time-budget=8000 \
    --window-size="$2,$3" "${@:5}" --screenshot="$4" "file://$here/$1" >/dev/null 2>&1
}
shot og-card.html 1200 630 "$out/og-image.png"
shot lockup-dark.html 640 160 "$out/logo-lockup-dark.png" --default-background-color=00000000
shot lockup-light.html 640 160 "$out/logo-lockup-light.png" --default-background-color=00000000
