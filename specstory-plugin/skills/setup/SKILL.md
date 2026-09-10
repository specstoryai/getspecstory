---
name: setup
description: Install and verify the SpecStory CLI binary so this plugin's auto-save hooks work. Use when the user asks to set up SpecStory, finish SpecStory installation, or when the specstory binary is missing and the user wants session auto-saving enabled.
---

# SpecStory Setup

Complete the SpecStory installation: this plugin's hooks auto-save every coding session to markdown in `.specstory/history`, but they need the `specstory` binary on the PATH.

## Steps

1. Check whether the binary is already installed:

   ```zsh
   command -v specstory && specstory version --no-version-check
   ```

   If it's installed, tell the user setup is already complete and stop.

2. Install it. Prefer Homebrew when available:

   ```zsh
   brew install specstoryai/tap/specstory
   ```

   If Homebrew is not available, download the latest release archive for the user's platform (Darwin/Linux, arm64/x86_64) from https://github.com/specstoryai/getspecstory/releases/latest — asset names look like `SpecStoryCLI_Darwin_arm64.tar.gz` — extract the `specstory` binary, and move it to a directory on their PATH (e.g. `/usr/local/bin`, may need sudo).

3. Verify the install:

   ```zsh
   specstory version --no-version-check
   ```

4. Run an initial sync in the current project so the user sees immediate value:

   ```zsh
   specstory sync
   ```

   Show the user what appeared under `.specstory/history/`.

5. Tell the user setup is done: from now on the plugin's hooks save every session automatically — no wrapper command, no workflow change. Their history is local-first; `specstory login` is only needed for optional cloud sync.
