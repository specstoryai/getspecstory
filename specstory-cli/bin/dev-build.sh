#!/usr/bin/env bash
# Build specstory-cli for linux, darwin, and windows (amd64 and arm64 each).
# Output goes to the first argument, relative to the current directory (default: dist).
# Run from anywhere.

set -e

# Output path: relative to CWD (where you run the script), default dist
OUTPUT_RELATIVE_PATH=${1:-dist}
OUTPUT_RELATIVE_PATH="${OUTPUT_RELATIVE_PATH#/}"  # strip leading slash to avoid // when joining
# Version to embed in the binary; falls back to git tag or "dev"
VERSION="${2:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"
START_DIR="$(pwd)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
# Create the output dir before canonicalizing it — `cd` into a not-yet-existing
# directory would fail the whole script on first run.
DEST_DIR="$START_DIR/$OUTPUT_RELATIVE_PATH"
mkdir -p "$DEST_DIR"
DEST_DIR="$(cd "$DEST_DIR" && pwd)"

cd "$CLI_DIR"
rm -f "$DEST_DIR"/specstory_*

LDFLAGS="-s -w -X main.version=$VERSION -X github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics.apiKey=${POSTHOG_API_KEY:-}"

# os goarch filename_arch
for target in \
  "linux amd64 x86_64" \
  "linux arm64 arm64" \
  "darwin amd64 x86_64" \
  "darwin arm64 arm64" \
  "windows amd64 x86_64 .exe" \
  "windows arm64 arm64 .exe"
do
  read -r os goarch filename_arch file_ext <<< "$target"
  out="$DEST_DIR/specstory_${os}_${filename_arch}${file_ext}"
  echo "Building $out..."
  CGO_ENABLED=0 GOOS="$os" GOARCH="$goarch" go build -ldflags="$LDFLAGS" -o "$out" .
done

echo "Done. Binaries in $DEST_DIR"
