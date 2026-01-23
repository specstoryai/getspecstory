#!/usr/bin/env bash
# Updates the Homebrew formula with new version and SHA256 hashes.
# Usage: update-homebrew-formula.sh <formula_path> <version> \
#        <sha_darwin_arm64> <sha_darwin_x86_64> <sha_linux_arm64> <sha_linux_x86_64>

set -euo pipefail

FORMULA="$1"
VERSION="$2"
SHA_DARWIN_ARM64="$3"
SHA_DARWIN_X86_64="$4"
SHA_LINUX_ARM64="$5"
SHA_LINUX_X86_64="$6"

# Validate inputs
if [[ ! -f "$FORMULA" ]]; then
  echo "Error: Formula file not found: $FORMULA" >&2
  exit 1
fi

for sha in "$SHA_DARWIN_ARM64" "$SHA_DARWIN_X86_64" "$SHA_LINUX_ARM64" "$SHA_LINUX_X86_64"; do
  if [[ ! "$sha" =~ ^[a-f0-9]{64}$ ]]; then
    echo "Error: Invalid SHA256 hash: $sha" >&2
    exit 1
  fi
done

echo "Updating formula: $FORMULA"
echo "  Version: $VERSION"

# Update version using sed (temp file for macOS compatibility)
sed "s/^  version \".*\"/  version \"${VERSION}\"/" "$FORMULA" > "${FORMULA}.tmp"
mv "${FORMULA}.tmp" "$FORMULA"

# Context-aware SHA256 update using awk
awk -v darwin_arm64="$SHA_DARWIN_ARM64" \
    -v darwin_x86="$SHA_DARWIN_X86_64" \
    -v linux_arm64="$SHA_LINUX_ARM64" \
    -v linux_x86="$SHA_LINUX_X86_64" '
{
  if (/Darwin_arm64/) { arch = "darwin_arm64" }
  else if (/Darwin_x86_64/) { arch = "darwin_x86" }
  else if (/Linux_arm64/) { arch = "linux_arm64" }
  else if (/Linux_x86_64/) { arch = "linux_x86" }

  if (/sha256 "[a-f0-9]{64}"/) {
    if (arch == "darwin_arm64") { sub(/sha256 "[a-f0-9]{64}"/, "sha256 \"" darwin_arm64 "\"") }
    else if (arch == "darwin_x86") { sub(/sha256 "[a-f0-9]{64}"/, "sha256 \"" darwin_x86 "\"") }
    else if (arch == "linux_arm64") { sub(/sha256 "[a-f0-9]{64}"/, "sha256 \"" linux_arm64 "\"") }
    else if (arch == "linux_x86") { sub(/sha256 "[a-f0-9]{64}"/, "sha256 \"" linux_x86 "\"") }
  }
  print
}' "$FORMULA" > "${FORMULA}.tmp" && mv "${FORMULA}.tmp" "$FORMULA"

echo "Formula updated successfully"
