# SpecStory Plugin

Auto-save every AI coding session as git-friendly markdown in `.specstory/history` — for both **Claude Code** and **Codex CLI** from a single plugin.

The plugin wires lifecycle hooks that run [`specstory sync`](https://docs.specstory.com) after each turn, so your full conversation history is always on disk, searchable, and diffable. No wrapper process, no workflow change.

## The binary — installed for you

The plugin drives the `specstory` binary, but you don't need it before installing the plugin. If it's missing, the plugin bootstraps it:

- **In Claude Code**, a `SessionStart` notice tells the agent to offer finishing setup — say yes and it runs the install for you.
- **Anywhere**, run the bundled `/specstory:setup` skill, which installs the binary (Homebrew, or a GitHub release download when brew isn't available), verifies it, and runs your first sync.

Prefer to install it yourself first? `brew install specstoryai/tap/specstory`. Until the binary exists, the auto-save hooks no-op silently — nothing breaks.

## Install for Claude Code

```zsh
claude plugin marketplace add specstoryai/getspecstory
claude plugin install specstory@specstory
```

Or interactively: `/plugin marketplace add specstoryai/getspecstory`, then `/plugin install specstory@specstory`.

## Install for Codex CLI

```zsh
codex plugin marketplace add specstoryai/getspecstory
codex plugin add specstory@specstory
```

Codex asks you to review and trust the plugin's hooks on first use (`/hooks` to manage) — this is Codex's standard trust gate for command hooks.

## What the hooks do

| Event | Action |
|-------|--------|
| `SessionStart` | Checks that the `specstory` binary is installed; if missing, instructs the agent to offer completing setup |
| `Stop` (turn completes) | Runs `specstory sync <agent> -s <session-id>` for the current session |
| `SessionEnd` (Claude Code only) | Final sync of the session |

All hooks are best-effort: failures never block or interrupt your agent session.

## Relationship to `specstory run`

`specstory run claude` / `specstory run codex` wrap the agent process and remain fully supported. This plugin is the no-wrapper alternative: install once, and post-turn hooks keep `.specstory/history` current in every project automatically. Use whichever fits your workflow — they produce the same markdown history.
