# scrocs

Kindle Scribe docs retriever for macOS.

## What this does

`scrocs` now uses a Go-native workflow for newer Kindle Scribe models that expose content over **MTP**:

1. Detect Kindle Scribe on USB
2. Sync notebook files from MTP storage using a Go MTP client
3. Convert notebook files to PDF using:
	- Go logic for `.notebook` / `.note` archives (embedded PDF/image extraction)
	- Calibre for Kindle Scribe `.nbk` payload files synced under `.notebooks`
4. Import each PDF into Bear via x-callback without forcing Bear into the foreground (`open -g`)

## Prerequisites (macOS)

Install dependencies:

```bash
brew install go libusb
```

> Native MTP support is compiled with `-tags mtp` and requires CGO + libusb headers.

## Install

From the repository root:

```bash
./scripts/install-launchd.sh
```

This builds `~/.local/share/scrocs/bin/scrocs`, installs `~/Library/LaunchAgents/com.scrocs.sync.plist`, and runs sync every 5 minutes in the background.

## Main binary

`~/.local/share/scrocs/bin/scrocs`

Useful environment variables:

- `SCROCS_HOME` (default `~/.local/share/scrocs`)
- `SCROCS_RAW_DIR`
- `SCROCS_PDF_DIR`
- `SCROCS_STATE_FILE`
- `SCROCS_LOG_FILE`
- `SCROCS_MTP_PATTERN` (default `(?i)kindle`)
- `SCROCS_EBOOK_CONVERT` (optional absolute path to `ebook-convert`)

## Notes

- The app keeps a SHA-256 import ledger to avoid duplicate Bear imports.
- `.pdf` files are copied directly.
- `.notebook` and `.note` files are converted by extracting embedded PDF content when present; otherwise embedded images are rendered into a PDF.
- `.nbk` files are converted via Calibre `ebook-convert`; this requires an NBK input plugin to be installed in Calibre.
- If native MTP support is unavailable at build time, the binary exits with a clear message to rebuild using `-tags mtp`.
