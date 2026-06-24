#!/usr/bin/env bash
# Install the workthreads skill so it is available from any Claude Code session.
#
# Self-contained: bundles workthreads' own engine. No dependency on any other skill.
# Re-run any time to update. Pass a target dir to override the default.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"          # .../workthreads
DEST="${1:-$HOME/.agents/skills/workthreads}"

if [ ! -f "$HERE/scripts/workthreads.mjs" ]; then
  echo "error: engine not found at $HERE/scripts - run this from the workthreads/ directory of a clone." >&2
  exit 1
fi

mkdir -p "$DEST"
rm -rf "$DEST/scripts"
cp -R "$HERE/scripts" "$DEST/scripts"          # bundle the self-contained engine
cp "$HERE/SKILL.md" "$DEST/SKILL.md"           # SKILL.md calls ${CLAUDE_SKILL_DIR}/scripts/workthreads.mjs

mkdir -p "$HOME/.claude/skills"
ln -sfn "$DEST" "$HOME/.claude/skills/workthreads"

echo "installed workthreads:"
echo "  skill  -> $DEST"
echo "  linked -> $HOME/.claude/skills/workthreads"
echo "Open a new Claude Code session, then run /workthreads (skills load at session start)."
