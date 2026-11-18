#!/bin/bash
# Build everything from source: server (Linux) + desktop app (macOS universal).
# Usage: scripts/build.sh            -> bin/, TempRoom.app, TempRoom-mac.zip
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${VERSION:-2.1.0}"
export GOFLAGS="${GOFLAGS:-} -trimpath"
# Optional built-in public relay, e.g. TEMPROOM_DEFAULT_RELAY=https://relay.example.com
# Without it the app requires a private Worker (see worker/README.md).
LDFLAGS=""
if [ -n "${TEMPROOM_DEFAULT_RELAY:-}" ]; then
  LDFLAGS="-X temproom/internal/app.DefaultServerURL=${TEMPROOM_DEFAULT_RELAY}"
fi

# The desktop app embeds cmd/temproom-app/dist; frontend/dist is the editable source.
rm -rf cmd/temproom-app/dist && cp -R frontend/dist cmd/temproom-app/dist

export MACOSX_DEPLOYMENT_TARGET=11.0
export CGO_CFLAGS="-mmacosx-version-min=11.0" CGO_LDFLAGS="-mmacosx-version-min=11.0"
go test -count=1 ./... >/dev/null
if command -v node >/dev/null; then node --check worker/src/index.js; fi  # private relay (Cloudflare Worker)
mkdir -p bin
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/temproom-server-linux-amd64 ./cmd/temproom-server
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bin/temproom-server-linux-arm64 ./cmd/temproom-server

CGO_ENABLED=1 GOARCH=arm64 go build -ldflags "$LDFLAGS" -o bin/temproom-app-arm64 ./cmd/temproom-app
if CGO_ENABLED=1 GOARCH=amd64 go build -ldflags "$LDFLAGS" -o bin/temproom-app-amd64 ./cmd/temproom-app; then
  lipo -create -output bin/temproom-app bin/temproom-app-arm64 bin/temproom-app-amd64
else
  echo "warning: Intel build failed; app is Apple Silicon only" >&2
  cp bin/temproom-app-arm64 bin/temproom-app
fi
rm -f bin/temproom-app-arm64 bin/temproom-app-amd64

APP=OneExit.app
rm -rf "$APP" && mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp bin/temproom-app "$APP/Contents/MacOS/OneExit"
if [ -f assets/AppIcon.icns ]; then
  cp assets/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"
fi
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundlePackageType</key><string>APPL</string>
    <key>CFBundleName</key><string>OneExit</string>
    <key>CFBundleDisplayName</key><string>OneExit</string>
    <key>CFBundleExecutable</key><string>OneExit</string>
    <key>CFBundleIdentifier</key><string>com.oneexit.app</string>
    <key>CFBundleIconFile</key><string>AppIcon</string>
    <key>CFBundleVersion</key><string>${VERSION}</string>
    <key>CFBundleShortVersionString</key><string>${VERSION}</string>
    <key>LSMinimumSystemVersion</key><string>11.0</string>
    <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST
codesign --force --deep --sign - "$APP"
rm -f OneExit-mac.zip && ditto -c -k --keepParent "$APP" OneExit-mac.zip
[ -d landing ] && cp OneExit-mac.zip landing/OneExit-mac.zip  # landing download link
rm -rf bin/TempRoom.app TempRoom.app TempRoom-mac.zip landing/TempRoom-mac.zip
echo "built: bin/temproom-server-linux-{amd64,arm64}, $APP, OneExit-mac.zip ($(lipo -archs bin/temproom-app))"
