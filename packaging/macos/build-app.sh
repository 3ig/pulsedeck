#!/bin/bash
# Builds dist/Pulsedeck.app (universal binary, ad-hoc signed) and dist/Pulsedeck-macos.dmg.
# Usage: packaging/macos/build-app.sh <version>
set -euo pipefail
VERSION="$1"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
APP="$ROOT/dist/Pulsedeck.app"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

# The menu-bar icon needs Cocoa, so macOS builds use cgo (unlike the others).
for arch in arm64 amd64; do
  CGO_ENABLED=1 GOOS=darwin GOARCH=$arch go build -C "$ROOT" -ldflags "-s -w -X main.version=$VERSION" -o "$WORK/pulsedeck-$arch" .
done
lipo -create -output "$APP/Contents/MacOS/pulsedeck" "$WORK/pulsedeck-arm64" "$WORK/pulsedeck-amd64"
sed "s/VERSION/$VERSION/g" "$ROOT/packaging/macos/Info.plist" > "$APP/Contents/Info.plist"

# App icon from the master artwork (regenerate with: go run ./packaging/mkicons).
mkdir "$WORK/Pulsedeck.iconset"
for size in 16 32 128 256 512; do
  sips -z $size $size "$ROOT/packaging/icon.png" --out "$WORK/Pulsedeck.iconset/icon_${size}x${size}.png" >/dev/null
  sips -z $((size * 2)) $((size * 2)) "$ROOT/packaging/icon.png" --out "$WORK/Pulsedeck.iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$WORK/Pulsedeck.iconset" -o "$APP/Contents/Resources/Pulsedeck.icns"

# Ad-hoc signature: enough to run on this Mac. Distributing to other Macs
# needs a Developer ID signature and notarisation.
codesign --force --deep --sign - "$APP"
# A disk image with the usual "drag to Applications" shortcut.
STAGE="$WORK/dmg"
mkdir "$STAGE"
cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
rm -f "$ROOT/dist/Pulsedeck-macos.dmg"
hdiutil create -quiet -volname Pulsedeck -srcfolder "$STAGE" -ov -format UDZO "$ROOT/dist/Pulsedeck-macos.dmg"
echo "built $APP"
