#!/usr/bin/env bash
# Install the decisions skill so it is available from any Claude Code session.
#
# Self-contained: bundles decisions' own engine. No dependency on any other skill.
# Re-run any time to update. Pass a target dir to override the default.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"          # .../decisions
DEST="${1:-$HOME/.agents/skills/decisions}"

if [ ! -f "$HERE/scripts/decisions.mjs" ]; then
  echo "error: engine not found at $HERE/scripts - run this from the decisions/ directory of a clone." >&2
  exit 1
fi

mkdir -p "$DEST"
rm -rf "$DEST/scripts"
cp -R "$HERE/scripts" "$DEST/scripts"          # bundle the self-contained engine
cp "$HERE/SKILL.md" "$DEST/SKILL.md"           # SKILL.md calls ${CLAUDE_SKILL_DIR}/scripts/decisions.mjs

mkdir -p "$HOME/.claude/skills"
ln -sfn "$DEST" "$HOME/.claude/skills/decisions"

echo "installed decisions:"
echo "  skill  -> $DEST"
echo "  linked -> $HOME/.claude/skills/decisions"
echo "Open a new Claude Code session, then run /decisions (skills load at session start)."
