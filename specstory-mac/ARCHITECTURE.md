# SpecStory for Mac

Always-on macOS menu bar app with a Granola-style pop-open desktop window. It vendors the `specstory` CLI, watches AI coding sessions across every supported agent, syncs with SpecStory Cloud, and offers RAG over your chats with "resume this session" as the killer action.

Full terrain research behind this design lives in the session scratchpad (`terrain/*.md`); the durable summary is this file.

## Structure

```
specstory-mac/
  project.yml            # XcodeGen, mirrors rookery/clients/mac
  Sources/               # app target: entry, models, services, views
  SpecStoryKit/          # local SwiftPM package: API models, parsers, stores (swift test here)
  Resources/bin/         # vendored specstory binaries (gitignored) + manifest.json (committed)
  scripts/vendor-cli.sh  # builds ../specstory-cli into Resources/bin + writes manifest
```

Build:

```zsh
./scripts/vendor-cli.sh          # once, and whenever the CLI changes
xcodegen generate
xcodebuild -project SpecStory.xcodeproj -scheme SpecStory -configuration Debug build
open "$(find ~/Library/Developer/Xcode/DerivedData -path '*/Build/Products/Debug/SpecStory.app' -print -quit)"
```

Tests: `cd SpecStoryKit && swift test`.

## Design decisions (resolved open questions)

1. **Menu bar agent**: `LSUIElement=true`. Activation policy flips to `.regular` while the main window is open (Dock icon appears, Granola behavior), back to `.accessory` on close.
2. **Signing**: dev builds `CODE_SIGNING_ALLOWED=NO` like Rook. Distribution (Developer ID + notarization + signed vendored binary) is deferred; App Store is off the table because the app reads `~/.claude`, `~/.cursor`, etc. and spawns children (no App Sandbox).
3. **Watch fleet**: max 8 concurrent `specstory watch --json` children over the most recently active projects (from `~/.specstory/sessions.db`), LRU-evicted; Tier-2 FSEvents tripwire on provider roots spins up children on demand.
4. **Cloud sync consent**: defer to the CLI's own config (`config.toml [cloud_sync]`); app exposes a global toggle only.
5. **Vendored binaries**: two arch binaries named like the monorepo (`specstory_darwin_arm64`, `specstory_darwin_x86_64`), selected at runtime by `BinaryLocator`. Binaries are gitignored (this repo contains the CLI source; `vendor-cli.sh` rebuilds deterministically); `Resources/bin/manifest.json` (committed) pins version + commit + sha256, verified at launch.
6. **CLI flag surface**: pinned to this repo's `specstory-cli` at HEAD; commit recorded in the manifest.
7. **Resume auth**: local sessions need no token. Cloud resume relies on the user's own `specstory login` state (`~/.specstory/cli/auth.json`); if absent, the resume sheet offers the login command first. Never pass tokens through terminal argv.
8. **Cloud-only resume with no local checkout**: prompt for a folder (NSOpenPanel), remember the projectId to path mapping in UserDefaults.
9. **Free tier**: local browsing, local search, local resume always work. Cloud/RAG affordances use the `gate(flag, entitled)` pattern: enabled, upsell, or hidden.
10. **Notifications**: new-session-started (primary), sync error, sign-in needed. Per-session "synced" stays a UI pill, not a notification. Authorization requested on first session detection.
11. **Analytics**: none app-side; the CLI's built-in PostHog stays as is.
12. **RAG scope**: cloud `/api/v1/chat` when signed in; signed out, the Ask panel serves local FTS results with a sign-in upsell for AI answers.
13. **Search vs Ask**: Granola pattern. Sidebar Search (⌘K) is a search overlay (local FTS + cloud GraphQL merged); the floating bottom bar is Ask (RAG).
14. **SpecStoryKit exists day one**: parsing, API models, Keychain, tokens are unit-tested there (`swift test`); the app target has no test bundle (Rook precedent).
15. **Reindex**: `specstory reindex` on launch (async); watch children keep the index live afterwards.
16. **Menu bar content**: rich `MenuBarExtra(.window)` popover: live sessions, recent sessions, sync state, quick actions. Not launch-window-only.

## Load-bearing invariants (from terrain research)

- Every CLI invocation passes `--no-version-check`; machine modes add `--json` and/or `--silent`.
- `watch`/`sync`/`list` are cwd-scoped; the cross-project substrate is `~/.specstory/sessions.db` (SQLite + FTS5, WAL; open read-only, it is a rebuildable cache).
- `watch --json` emits NDJSON `{timestamp, action: created|updated, session_id, start_time, end_time, provider, markdown_size, total_user_prompts, agent_activity (Int), markdown_file?}`; slog lines can interleave, so tolerate non-JSON lines. Pre-existing sessions are suppressed on initial scan, making `created` a clean new-session signal.
- Cloud API: base `https://cloud.specstory.com/api/v1`, Bearer auth, `{"success":true,"data":...}` envelope. Device flow: browser `/cli-login` code, `POST /device-login` (client `specstory-macapp`) yields a 10-year refresh JWT; `POST /device-refresh` mints 1 h access tokens (refresh when under 5 min left, mutex-serialized). App REST uses the access token; CLI children get the refresh token via env `SPECSTORY_CLOUD_TOKEN`, never argv. Keychain service `com.specstory.mac`; never touch `~/.specstory/cli/auth.json`.
- `/api/v1/chat` streams NDJSON (not SSE): `start | event | query_rewritten | embedding_search_results | chunk | end | error`; citations `[chunk:ID]` resolve via `GET /chunks/:id`. Flush the trailing partial line at stream end.
- URL ids are client identifiers (project slug + session clientId), never server UUIDs. Merge key local+cloud: clientId + projectId; title precedence `userTitle > metadata.title > name`.
- `metadata.deviceId`/`machineName` enable the per-machine badge and filter (a differentiator the web UI lacks).
- Resume: delegate to the CLI (`specstory resume <agent> --session specstory://projects/{pid}/sessions/{sid}`); it is interactive stdio, so land it in a real terminal (AppleScript automation, or copy-command fallback). Enable iff local, or `sessionDataSize > 0` plus resume flag plus entitlement.
- Child lifecycle: SIGTERM first; cloud flush can take up to 180 s, so only escalate to SIGKILL when no uploads are pending. Restart watch children on auth/provider/output-path/timezone changes with generation-counter latest-wins.
- Retry reads twice on 5xx/429 (300/900 ms). 401 means refresh; 401 on refresh means re-login. Flags fail open, entitlements fail closed.
- No em dashes in user-visible UI strings.
