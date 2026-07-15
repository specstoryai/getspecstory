#!/usr/bin/env bash
# Install the decisions2 skill. Self-contained; re-run any time to update.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
DEST="${1:-$HOME/.agents/skills/decisions2}"

if [ ! -f "$HERE/scripts/decisions2.mjs" ]; then
  echo "error: engine not found at $HERE/scripts - run this from the decisions2/ directory of a clone." >&2
  exit 1
fi

mkdir -p "$DEST"
rm -rf "$DEST/scripts"
cp -R "$HERE/scripts" "$DEST/scripts"
cp "$HERE/SKILL.md" "$DEST/SKILL.md"

mkdir -p "$HOME/.claude/skills"
ln -sfn "$DEST" "$HOME/.claude/skills/decisions2"

echo "installed decisions2:"
echo "  skill  -> $DEST"
echo "  linked -> $HOME/.claude/skills/decisions2"
