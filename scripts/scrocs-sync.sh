#!/usr/bin/env bash
set -euo pipefail

SCROCS_HOME="${SCROCS_HOME:-$HOME/.local/share/scrocs}"
LOCK_DIR="$SCROCS_HOME/.lock"
LOG_FILE="${SCROCS_LOG_FILE:-$SCROCS_HOME/scrocs.log}"
MOUNT_POINT="${SCROCS_MOUNT_POINT:-$SCROCS_HOME/mount}"
RAW_DIR="${SCROCS_RAW_DIR:-$SCROCS_HOME/raw}"
PDF_DIR="${SCROCS_PDF_DIR:-$SCROCS_HOME/pdf}"
STATE_FILE="${SCROCS_STATE_FILE:-$SCROCS_HOME/imported.sha256}"
CONVERTER_BIN="${SCROCS_CONVERTER_BIN:-ebook-convert}"

mkdir -p "$SCROCS_HOME" "$MOUNT_POINT" "$RAW_DIR" "$PDF_DIR"
touch "$LOG_FILE" "$STATE_FILE"

log() {
  printf '%s %s\n' "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" "$*" >>"$LOG_FILE"
}

warn() {
  printf '%s\n' "$*" >&2
  log "$*"
}

url_encode() {
  if ! command -v python3 >/dev/null 2>&1; then
    warn "python3 is required for Bear URL encoding"
    return 1
  fi

  python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))' "$1"
}

is_mounted() {
  mount | /usr/bin/grep -Fq "on $MOUNT_POINT ("
}

is_kindle_connected() {
  /usr/sbin/system_profiler SPUSBDataType 2>/dev/null | /usr/bin/grep -qiE 'Kindle( Scribe)?|Amazon Kindle'
}

mount_mtp() {
  if ! command -v jmtpfs >/dev/null 2>&1; then
    warn "jmtpfs is not installed. Install with: brew install jmtpfs"
    return 1
  fi

  if is_mounted; then
    return 0
  fi

  if ! jmtpfs "$MOUNT_POINT" >>"$LOG_FILE" 2>&1; then
    warn "Unable to mount Kindle over MTP"
    return 1
  fi
}

unmount_mtp() {
  if is_mounted; then
    if command -v umount >/dev/null 2>&1; then
      umount "$MOUNT_POINT" >>"$LOG_FILE" 2>&1 || true
    fi
  fi
}

file_size() {
  local path="$1"
  stat -f '%z' "$path" 2>/dev/null || stat -c '%s' "$path"
}

sync_raw_files() {
  local source_root="$MOUNT_POINT/documents"
  if [[ ! -d "$source_root" ]]; then
    source_root="$MOUNT_POINT"
  fi

  while IFS= read -r -d '' source_file; do
    local rel_path dest_file source_size dest_size
    rel_path="${source_file#$MOUNT_POINT/}"
    dest_file="$RAW_DIR/$rel_path"
    mkdir -p "$(dirname "$dest_file")"

    source_size="$(file_size "$source_file")"
    dest_size="$(file_size "$dest_file" 2>/dev/null || true)"
    if [[ ! -f "$dest_file" || "$source_file" -nt "$dest_file" || "$source_size" != "$dest_size" ]]; then
      cp "$source_file" "$dest_file"
      log "Synced $rel_path"
    fi
  done < <(find "$source_root" -type f \( -iname '*.notebook' -o -iname '*.note' -o -iname '*.pdf' \) -print0)
}

already_imported() {
  local file_hash="$1"
  /usr/bin/grep -Fqx "$file_hash" "$STATE_FILE"
}

mark_imported() {
  local file_hash="$1"
  printf '%s\n' "$file_hash" >>"$STATE_FILE"
}

convert_to_pdf() {
  local input_file="$1"
  local relative_input="${input_file#$RAW_DIR/}"
  local output_file="$PDF_DIR/${relative_input%.*}.pdf"

  mkdir -p "$(dirname "$output_file")"

  if [[ "${input_file##*.}" == "pdf" || "${input_file##*.}" == "PDF" ]]; then
    cp "$input_file" "$output_file"
    printf '%s\n' "$output_file"
    return 0
  fi

  if ! command -v "$CONVERTER_BIN" >/dev/null 2>&1; then
    warn "Converter not found: $CONVERTER_BIN"
    return 1
  fi

  if "$CONVERTER_BIN" "$input_file" "$output_file" >>"$LOG_FILE" 2>&1; then
    printf '%s\n' "$output_file"
    return 0
  fi

  log "Failed to convert $relative_input"
  return 1
}

import_pdf_to_bear() {
  local pdf_file="$1"
  local title encoded_file encoded_title callback_url

  title="Kindle Scribe - $(basename "$pdf_file" .pdf)"
  encoded_file="$(url_encode "$pdf_file")"
  encoded_title="$(url_encode "$title")"
  callback_url="bear://x-callback-url/add-file?file=$encoded_file&title=$encoded_title&new_window=no&show_window=no"

  if open -g "$callback_url" >/dev/null 2>&1; then
    return 0
  fi

  log "Failed to import $(basename "$pdf_file") into Bear"
  return 1
}

main() {
  local existing_pid

  if ! mkdir "$LOCK_DIR" 2>/dev/null; then
    existing_pid="$(cat "$LOCK_DIR/pid" 2>/dev/null || true)"
    if [[ -n "$existing_pid" ]] && kill -0 "$existing_pid" 2>/dev/null; then
      log "Another sync is already running"
      exit 0
    fi

    warn "Found stale lock; cleaning up"
    rm -rf "$LOCK_DIR"
    mkdir "$LOCK_DIR"
  fi

  printf '%s\n' "$$" >"$LOCK_DIR/pid"
  trap 'unmount_mtp; rm -rf "$LOCK_DIR" >/dev/null 2>&1 || true' EXIT

  if ! is_kindle_connected; then
    log "Kindle Scribe not detected"
    exit 0
  fi

  log "Kindle Scribe detected; starting sync"

  if ! mount_mtp; then
    exit 1
  fi

  sync_raw_files

  local raw_file pdf_file file_hash
  while IFS= read -r -d '' raw_file; do
    if ! pdf_file="$(convert_to_pdf "$raw_file")"; then
      continue
    fi

    file_hash="$(/usr/bin/shasum -a 256 "$pdf_file" | /usr/bin/awk '{print $1}')"
    if already_imported "$file_hash"; then
      continue
    fi

    if import_pdf_to_bear "$pdf_file"; then
      mark_imported "$file_hash"
      log "Imported $(basename "$pdf_file") into Bear"
    fi
  done < <(find "$RAW_DIR" -type f \( -iname '*.notebook' -o -iname '*.note' -o -iname '*.pdf' \) -print0)

  log "Sync complete"
}

main "$@"
