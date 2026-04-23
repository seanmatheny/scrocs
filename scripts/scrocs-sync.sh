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
MOUNT_BIN="${SCROCS_MOUNT_BIN:-$(command -v mount || true)}"
UMOUNT_BIN="${SCROCS_UMOUNT_BIN:-$(command -v umount || true)}"

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
  python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))' "$1"
}

is_kindle_connected() {
  /usr/sbin/system_profiler SPUSBDataType 2>/dev/null | /usr/bin/grep -qiE 'Kindle( Scribe)?|Amazon Kindle'
}

mount_mtp() {
  if ! command -v jmtpfs >/dev/null 2>&1; then
    warn "jmtpfs is not installed. Install with: brew install jmtpfs"
    return 1
  fi

  if [[ -n "$MOUNT_BIN" ]] && "$MOUNT_BIN" | /usr/bin/grep -Fq "on $MOUNT_POINT ("; then
    return 0
  fi

  if ! jmtpfs "$MOUNT_POINT" >>"$LOG_FILE" 2>&1; then
    warn "Unable to mount Kindle over MTP"
    return 1
  fi
}

unmount_mtp() {
  if [[ -n "$MOUNT_BIN" ]] && "$MOUNT_BIN" | /usr/bin/grep -Fq "on $MOUNT_POINT ("; then
    if [[ -n "$UMOUNT_BIN" ]]; then
      "$UMOUNT_BIN" "$MOUNT_POINT" >>"$LOG_FILE" 2>&1 || true
    fi
  fi
}

sync_raw_files() {
  local source_root="$MOUNT_POINT/documents"
  if [[ ! -d "$source_root" ]]; then
    source_root="$MOUNT_POINT"
  fi

  while IFS= read -r -d '' source_file; do
    local rel_path dest_file
    rel_path="${source_file#$MOUNT_POINT/}"
    dest_file="$RAW_DIR/$rel_path"
    mkdir -p "$(dirname "$dest_file")"

    if [[ ! -f "$dest_file" || "$source_file" -nt "$dest_file" ]]; then
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
  if ! mkdir "$LOCK_DIR" 2>/dev/null; then
    log "Another sync is already running"
    exit 0
  fi

  trap 'unmount_mtp; rmdir "$LOCK_DIR" >/dev/null 2>&1 || true' EXIT

  if ! is_kindle_connected; then
    log "Kindle Scribe not detected"
    exit 0
  fi

  log "Kindle Scribe detected; starting sync"

  if ! mount_mtp; then
    exit 1
  fi

  sync_raw_files

  while IFS= read -r -d '' raw_file; do
    local pdf_file file_hash

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
