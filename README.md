# scrocs

Kindle Scribe docs retriever for macOS.

## What this does

`scrocs` provides a lightweight background sync flow for newer Kindle Scribe models that only expose content over **MTP** (no normal USB volume mount):

1. Detect Kindle Scribe on USB
2. Mount over MTP with `jmtpfs`
3. Copy notebook files locally
4. Convert notebook files to PDF (default: Calibre `ebook-convert`)
5. Import each PDF into Bear via x-callback without forcing Bear into the foreground (`open -g`)

## Prerequisites (macOS)

Install dependencies:

```bash
brew install jmtpfs calibre
```

> `ebook-convert` comes from Calibre.

## Install

From the repository root (`/home/runner/work/scrocs/scrocs` in this environment):

```bash
/home/runner/work/scrocs/scrocs/scripts/install-launchd.sh
```

This installs `~/Library/LaunchAgents/com.scrocs.sync.plist` and runs sync every 45 seconds in the background.

## Main script

`/home/runner/work/scrocs/scrocs/scripts/scrocs-sync.sh`

Useful environment variables:

- `SCROCS_HOME` (default `~/.local/share/scrocs`)
- `SCROCS_CONVERTER_BIN` (default `ebook-convert`)
- `SCROCS_LOG_FILE`

## Notes

- This workflow is intentionally lightweight and launchd-based.
- The script keeps a SHA-256 import ledger to avoid duplicate Bear imports.
- If your converter needs custom arguments, point `SCROCS_CONVERTER_BIN` to a wrapper script.
