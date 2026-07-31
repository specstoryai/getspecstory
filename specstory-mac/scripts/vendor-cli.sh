#!/bin/zsh
# Vendors the specstory CLI into the app bundle inputs.
# Mirrors the specstory-monorepo bin/ pattern (per-platform binaries selected at
# runtime), improved with a committed manifest pinning version + commit + sha256.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLI_SRC="${SPECSTORY_CLI_SRC:-$ROOT/../specstory-cli}"
BIN_DIR="$ROOT/Resources/bin"

if [[ ! -f "$CLI_SRC/main.go" ]]; then
  echo "error: specstory CLI source not found at $CLI_SRC (set SPECSTORY_CLI_SRC)" >&2
  exit 1
fi

mkdir -p "$BIN_DIR"

COMMIT="$(git -C "$CLI_SRC" rev-parse HEAD)"
VERSION="$(git -C "$CLI_SRC" describe --tags --always 2>/dev/null || echo dev)"

echo "Vendoring specstory CLI $VERSION ($COMMIT) from $CLI_SRC"

(cd "$CLI_SRC" && GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o "$BIN_DIR/specstory_darwin_arm64" .)
(cd "$CLI_SRC" && GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o "$BIN_DIR/specstory_darwin_x86_64" .)
chmod +x "$BIN_DIR/specstory_darwin_arm64" "$BIN_DIR/specstory_darwin_x86_64"

SHA_ARM64="$(shasum -a 256 "$BIN_DIR/specstory_darwin_arm64" | cut -d' ' -f1)"
SHA_X86="$(shasum -a 256 "$BIN_DIR/specstory_darwin_x86_64" | cut -d' ' -f1)"

cat > "$BIN_DIR/manifest.json" <<EOF
{
  "cliVersion": "$VERSION",
  "gitCommit": "$COMMIT",
  "sha256": {
    "arm64": "$SHA_ARM64",
    "x86_64": "$SHA_X86"
  }
}
EOF

# Smoke test: the vendored binary must speak JSON (monorepo CI pattern).
SMOKE_DIR="$(mktemp -d)"
trap 'rm -rf "$SMOKE_DIR"' EXIT
ARCH_BIN="$BIN_DIR/specstory_darwin_arm64"
[[ "$(uname -m)" == "x86_64" ]] && ARCH_BIN="$BIN_DIR/specstory_darwin_x86_64"
if ! (cd "$SMOKE_DIR" && "$ARCH_BIN" list --json --no-version-check >/dev/null 2>&1); then
  echo "error: vendored binary failed the list --json smoke test" >&2
  exit 1
fi

echo "Vendored OK:"
cat "$BIN_DIR/manifest.json"
