#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PLIST_DIR="$HOME/Library/LaunchAgents"
PLIST_PATH="$PLIST_DIR/com.scrocs.sync.plist"
LOG_PATH="$HOME/Library/Logs/scrocs-launchd.log"
BIN_DIR="$HOME/.local/share/scrocs/bin"
BIN_PATH="$BIN_DIR/scrocs"

xml_escape() {
  printf '%s' "$1" | sed \
    -e 's/&/\&amp;/g' \
    -e 's/</\&lt;/g' \
    -e 's/>/\&gt;/g' \
    -e 's/\"/\&quot;/g' \
    -e "s/'/\&apos;/g"
}

mkdir -p "$PLIST_DIR" "$(dirname "$LOG_PATH")" "$BIN_DIR"

echo "Building scrocs binary with native MTP support..."
if ! (cd "$REPO_ROOT" && go build -tags mtp -o "$BIN_PATH" ./cmd/scrocs); then
  echo "Build failed. Ensure Go is installed and libusb development headers are available." >&2
  exit 1
fi

BIN_PATH_XML="$(xml_escape "$BIN_PATH")"
LOG_PATH_XML="$(xml_escape "$LOG_PATH")"

cat > "$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string>com.scrocs.sync</string>

    <key>ProgramArguments</key>
    <array>
      <string>$BIN_PATH_XML</string>
    </array>

    <key>RunAtLoad</key>
    <true/>

    <key>StartInterval</key>
    <integer>300</integer>

    <key>StandardOutPath</key>
    <string>$LOG_PATH_XML</string>

    <key>StandardErrorPath</key>
    <string>$LOG_PATH_XML</string>
  </dict>
</plist>
PLIST

launchctl unload "$PLIST_PATH" >/dev/null 2>&1 || true
launchctl load "$PLIST_PATH"

echo "Installed launchd agent: $PLIST_PATH"
echo "Binary path: $BIN_PATH"
