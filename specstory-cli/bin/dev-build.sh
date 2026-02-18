#!/usr/bin/env bash
# Build specstory-cli for linux, darwin (amd64 and arm64), and windows (amd64). Output to specstory-monorepo/bin.
# Run from anywhere.

set -e

# Output path: relative to CWD (where you run the script), default dist
OUTPUT_RELATIVE_PATH=${1:-dist}
OUTPUT_RELATIVE_PATH="${OUTPUT_RELATIVE_PATH#/}"  # strip leading slash to avoid // when joining
START_DIR="$(pwd)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
DEST_DIR="$(cd "$START_DIR/$OUTPUT_RELATIVE_PATH" && pwd)"

cd "$CLI_DIR"
mkdir -p "$DEST_DIR"
rm -f "$DEST_DIR"/specstory_*

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"
LDFLAGS="-s -w -X main.version=$VERSION -X github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics.apiKey=${POSTHOG_API_KEY:-}"

# os goarch filename_arch
for target in \
  "linux amd64 x86_64" \
  "linux arm64 arm64" \
  "darwin amd64 x86_64" \
  "darwin arm64 arm64"
do
  read -r os goarch filename_arch <<< "$target"
  out="$DEST_DIR/specstory_${os}_${filename_arch}"
  echo "Building $out..."
  CGO_ENABLED=0 GOOS="$os" GOARCH="$goarch" go build -ldflags="$LDFLAGS" -o "$out" ./main.go
done

echo "Building $DEST_DIR/specstory_win32_amd64.exe..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="$LDFLAGS" -o "$DEST_DIR/specstory_win32.exe" ./main.go

echo "Done. Binaries in $DEST_DIR"
