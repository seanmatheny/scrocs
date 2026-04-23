#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEFAULT_BIN="$HOME/.local/share/scrocs/bin/scrocs"
FALLBACK_BIN="$REPO_ROOT/bin/scrocs"
BIN_PATH="${SCROCS_BIN:-$DEFAULT_BIN}"

if [[ ! -x "$BIN_PATH" && -x "$FALLBACK_BIN" ]]; then
  BIN_PATH="$FALLBACK_BIN"
fi

if [[ ! -x "$BIN_PATH" ]]; then
  echo "scrocs binary not found at '$BIN_PATH'. Run ./scripts/install-launchd.sh first." >&2
  exit 1
fi

exec "$BIN_PATH" "$@"
