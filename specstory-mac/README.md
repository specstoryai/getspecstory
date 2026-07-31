# SpecStory for Mac

Always-on menu bar app with a Granola-style desktop window for your AI coding sessions. It vendors the `specstory` CLI, watches every supported agent (Claude Code, Codex, Cursor, Cursor CLI, Gemini, Antigravity, DeepSeek, Droid), notifies you when a new session starts, syncs with SpecStory Cloud, and answers questions about your whole coding history with resume-into-session one click away.

## Build and run

```zsh
./scripts/vendor-cli.sh      # build the CLI from ../specstory-cli into Resources/bin
xcodegen generate
xcodebuild -project SpecStory.xcodeproj -scheme SpecStory -configuration Debug -derivedDataPath build/DerivedData build
open build/DerivedData/Build/Products/Debug/SpecStory.app
```

Always build with `-derivedDataPath build/DerivedData`; regenerating the project can otherwise mint a second Xcode DerivedData clone and you end up launching stale bundles.

Tests:

```zsh
cd SpecStoryKit && swift test
```

## What is where

```
Sources/               app target: AppModel + extensions, SwiftUI views, app services
SpecStoryKit/          local SwiftPM package: CLI engine, cloud API, chat stream,
                       SQLite index reader, models; all unit tests live here
Resources/bin/         vendored specstory binaries (gitignored) + committed manifest pin
scripts/vendor-cli.sh  rebuilds and pins the vendored CLI
ARCHITECTURE.md        design decisions and load-bearing invariants
CONTRACTS.md           module API contracts the services were built against
```

## The experience

- **Menu bar, always on.** Live session count in the status item menu, recent sessions, quick open, graceful fleet shutdown on quit.
- **Happening now.** New sessions in any supported agent appear within seconds, with provider brand marks, prompt counts, and sync state; a notification fires when one starts.
- **The feed.** Every session across every agent, project, and machine, date-grouped, local and cloud rows merged by client id; sessions from other machines carry a machine badge.
- **Search (⌘K).** Local FTS over `~/.specstory/sessions.db` merged with cloud search.
- **Ask anything.** Streaming RAG answers over your synced chats, grounded with source pills that deep-link to sessions.
- **Resume.** Any local or cloud-resumable session relaunches into its agent, in its project directory, in your terminal; cross-agent targets supported, commands copyable.

## Dev notes

- The app is unsandboxed (it reads agent stores under `~` and spawns the CLI); dev builds are unsigned (`CODE_SIGNING_ALLOWED=NO`). Distribution needs Developer ID + notarization and is deferred.
- Sign-in is the cloud device flow: browser shows a code at cloud.specstory.com/cli-login, the app exchanges it for a refresh token in the Keychain (`com.specstory.mac`). The CLI's own `~/.specstory/cli/auth.json` is never touched.
- Watch children run `watch --json` per project (never `--silent`, which suppresses the JSON events). Stale children from crashed runs are reaped at startup by bundle path.
- `specstory reindex` rebuilds the session index from scratch, so the app only runs it when the index is missing or a day stale.
