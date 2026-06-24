#!/usr/bin/env bash
# Install the workthreads skill so it is available from any Claude Code session.
#
# Workthreads reuses Lore's engine, so this bundles `lore/scripts` together with the
# skill into a self-contained install dir, then symlinks it into ~/.claude/skills.
# Re-run any time to update to the current engine. Pass a dir to override the target.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"          # .../workthreads
ENGINE_SRC="$HERE/../lore/scripts"             # the shared Lore engine
DEST="${1:-$HOME/.agents/skills/workthreads}"

if [ ! -f "$ENGINE_SRC/mine-skills.mjs" ]; then
  echo "error: Lore engine not found at $ENGINE_SRC - run this from a full clone of the repo." >&2
  exit 1
fi

mkdir -p "$DEST"
rm -rf "$DEST/scripts"
cp -R "$ENGINE_SRC" "$DEST/scripts"            # bundle the engine
cp "$HERE/SKILL.md" "$DEST/SKILL.md"           # SKILL.md already calls ${CLAUDE_SKILL_DIR}/scripts/...

mkdir -p "$HOME/.claude/skills"
ln -sfn "$DEST" "$HOME/.claude/skills/workthreads"

echo "installed workthreads:"
echo "  skill   -> $DEST"
echo "  linked  -> $HOME/.claude/skills/workthreads"
echo "Open a new Claude Code session, then run /workthreads (skills load at session start)."
