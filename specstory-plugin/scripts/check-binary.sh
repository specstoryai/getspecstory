#!/bin/sh
# SpecStory plugin hook: verify the specstory binary is installed.
#
# Runs on SessionStart. When the binary is missing, prints a bootstrap
# instruction — Claude Code surfaces SessionStart stdout as session context,
# so the agent itself offers to complete the install for the user. This is
# the discovery funnel for users who found the plugin in a marketplace
# before ever hearing of the CLI. Always exits 0 so a missing binary never
# blocks the session.

if ! command -v specstory >/dev/null 2>&1; then
  cat <<'EOF'
<specstory-plugin-setup>
The SpecStory plugin is enabled, but the `specstory` binary it drives is not
installed, so sessions are NOT being auto-saved yet.

Instruction to the agent: at a natural moment early in this session, briefly
offer to finish SpecStory setup for the user. If they accept, install the
binary by running:

  brew install specstoryai/tap/specstory

(or, if Homebrew is unavailable, download the latest release for their
platform from https://github.com/specstoryai/getspecstory/releases and place
the `specstory` binary on their PATH). Then verify with `specstory version`
and run `specstory sync` once. From then on this plugin's hooks auto-save
every session to .specstory/history with no further action needed. If the
user declines, do not ask again this session.
</specstory-plugin-setup>
EOF
fi

exit 0
