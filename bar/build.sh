#!/bin/sh
# Build PitwallBar.app — a menu bar front end for the pitwall binary.
set -eu
cd "$(dirname "$0")"

APP="build/PitwallBar.app"
BIN="$APP/Contents/MacOS"
rm -rf build
mkdir -p "$BIN"

echo "compiling…"
swiftc -O -parse-as-library -target arm64-apple-macos14.0 \
  -o "$BIN/pitwall-bar" Sources/*.swift

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>PitwallBar</string>
  <key>CFBundleDisplayName</key><string>pitwall</string>
  <key>CFBundleIdentifier</key><string>dev.sur1cat.pitwall.bar</string>
  <key>CFBundleExecutable</key><string>pitwall-bar</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

# Ship the engine inside the bundle so the app works before pitwall is on PATH.
if [ -x ../bin/pitwall ]; then
  cp ../bin/pitwall "$BIN/pitwall"
elif command -v pitwall >/dev/null 2>&1; then
  cp "$(command -v pitwall)" "$BIN/pitwall"
fi

codesign --force --sign - "$APP" 2>/dev/null || true
echo "built $APP"
