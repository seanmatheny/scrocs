#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SERVICE_LABEL="com.scrocs.sync"
PLIST_DIR="$HOME/Library/LaunchAgents"
PLIST_PATH="$PLIST_DIR/${SERVICE_LABEL}.plist"
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

# Build a PATH that includes both Intel and Apple-Silicon Homebrew prefixes so
# that libusb (needed by the MTP build tag) can be resolved at runtime even
# when launched from launchd, which starts with a minimal environment.
HOMEBREW_BIN_XML="$(xml_escape "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin")"
HOMEBREW_LIB_XML="$(xml_escape "/usr/local/lib:/opt/homebrew/lib")"

cat > "$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string>$SERVICE_LABEL</string>

    <key>ProgramArguments</key>
    <array>
      <string>$BIN_PATH_XML</string>
    </array>

    <key>EnvironmentVariables</key>
    <dict>
      <key>PATH</key>
      <string>$HOMEBREW_BIN_XML</string>
      <key>DYLD_LIBRARY_PATH</key>
      <string>$HOMEBREW_LIB_XML</string>
    </dict>

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

# Unload/load using bootstrap domain for compatibility with macOS 10.11 and
# later (launchctl load/unload are deprecated on modern macOS).
DOMAIN="gui/$(id -u)"
launchctl bootout "$DOMAIN/$SERVICE_LABEL" 2>/dev/null || \
  launchctl unload "$PLIST_PATH" 2>/dev/null || true
launchctl bootstrap "$DOMAIN" "$PLIST_PATH" 2>/dev/null || \
  launchctl load "$PLIST_PATH"

echo "Installed launchd agent: $PLIST_PATH"
echo "Binary path: $BIN_PATH"
