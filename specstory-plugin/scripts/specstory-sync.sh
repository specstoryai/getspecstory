#!/bin/sh
# SpecStory plugin hook: save the current session to markdown.
#
# Invoked by Claude Code / Codex lifecycle hooks (Stop, SessionEnd) with the
# hook payload JSON on stdin. Extracts session_id and cwd from the payload and
# runs `specstory sync <agent> -s <session-id>` in the session's project
# directory, falling back to a full project sync when no session id is present.
#
# Every failure path exits 0: a missing binary or a sync error must never
# block or annoy the user's agent session — auto-save is best-effort.

AGENT="${1:-}"
[ -n "$AGENT" ] || exit 0

command -v specstory >/dev/null 2>&1 || exit 0

PAYLOAD="$(cat 2>/dev/null || true)"

# Minimal JSON field extraction. The payload is machine-generated with string
# values, so a sed capture is sufficient and avoids a jq dependency.
json_field() {
  printf '%s' "$PAYLOAD" | sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" | head -n 1
}

SESSION_ID="$(json_field session_id)"
SESSION_CWD="$(json_field cwd)"

if [ -n "$SESSION_CWD" ] && [ -d "$SESSION_CWD" ]; then
  cd "$SESSION_CWD" 2>/dev/null || exit 0
fi

if [ -n "$SESSION_ID" ]; then
  specstory sync "$AGENT" -s "$SESSION_ID" >/dev/null 2>&1 || true
else
  specstory sync "$AGENT" >/dev/null 2>&1 || true
fi

exit 0
