# Qwen Code session format

Verified with **Qwen Code `0.23.4`**, the stable npm release on 2026-09-15.
Registry ID: `qwen`. Package: `@qwen-code/qwen-code`. Executable: `qwen`.

## Evidence

The installed vendor bundle supplies `Storage`, `sanitizeCwd`,
`ChatRecordingService`, `SessionService`, `ToolNames`, and
`ToolNamesMigration`. Module filenames are hashed and change between releases.
`factory/list-tools` discovers and imports the declaration module; it does not
ask a model to enumerate tools. `testdata/tools.json` contains the 54 declared
names, including the three migration aliases, and their expected SPI types.

`testdata/session-current.jsonl` was recorded by the real 0.23.4 executable
against a local deterministic OpenAI-compatible test endpoint. Only its
workspace path was normalized. It exercises a user turn, shell invocation,
result, response and native system records. Other fixtures isolate compaction,
notifications, malformed records, and parallel result folding.

Native and reconstructed resume were exercised with the actual Qwen loader.
The captured outbound model request contained the previous conversation before
the test endpoint responded. This verifies persistence and loading, not model
quality or an authenticated cloud integration.

## Storage and project identity

Default location:

```text
~/.qwen/projects/<sanitized-cwd>/chats/<session-id>.jsonl
```

`QWEN_RUNTIME_DIR` overrides the runtime store; otherwise `QWEN_HOME` overrides
`~/.qwen`. Both support `~` expansion and relative paths, resolved from the
launching process directory. Before moving Qwen into a selected project,
SpecStory passes absolute override values to the child so discovery and writing
cannot diverge. SpecStory honors these environment overrides for
discovery, enumeration, watching and reconstruction.

Qwen lowercases the cwd on Windows, then replaces every character outside
`[a-zA-Z0-9]` with `-`. Its JavaScript regexp operates on UTF-16 code units: an
emoji becomes two hyphens. Unix case is preserved. The encoding is lossy:
`a-b` and `a_b` collide, so the provider also checks the recorded cwd. Qwen's
reserved `.qwen/worktrees/<name>` paths belong to their parent project; an
arbitrary child directory does not.

Node's process cwd resolves symlinks on the tested macOS system. SpecStory
uses the canonical local project path for child launch, native destination,
and reconstructed record cwd. Qwen validates the hash of the record cwd as
well as the file location, so canonicalizing only the filename is insufficient.
Recorded origins in enumeration are returned verbatim, including unknown origins.

Only immediate `chats/<id>.jsonl` transcripts are discovered. Ledger files
(`<id>.ledger.jsonl`), runtime state (`<id>.runtime.json`), locks, nested stores,
and archived sessions are not independent conversations to export. The durable
transcript is separate from runtime/writer state; resume does not require
SpecStory to synthesize those sidecars. The read paths never write to Qwen's store.

## Record envelope and lifecycle

One JSON object per line, with these principal fields:

| Field | Meaning |
| --- | --- |
| `uuid`, `parentUuid` | Record identity and native history linkage; the root parent may be null |
| `sessionId` | Native session ID, normally also the filename stem |
| `timestamp` | RFC 3339 timestamp |
| `type`, `subtype`, `provenance` | Conversational versus system origin |
| `cwd`, `version`, `gitBranch` | Recorded workspace and agent metadata |
| `message` | Gemini-shaped `{role, parts}` payload |
| `model`, `usageMetadata` | Model identity and per-response token counts |
| `toolCallResult` | Tool status and human-readable display payload |

Resume appends to the same transcript. Compaction appends a
`system/chat_compression` record carrying its summary in `systemPayload`;
it does not erase the original conversation. Qwen also uses parent links for
rewinds. SpecStory exports the recorded chronological conversation, preserving
previously recorded turns, as its Claude Code parser does; it does not turn the
export into Qwen's active-branch projection. Native same-agent resume remains
Qwen's responsibility. RawData preserves the entire original transcript.

| Record | Conversion |
| --- | --- |
| Real `user` | Starts an exchange; includes mid-turn user interjections |
| System-provenance `user` | Task notifications are folded into their original tool calls by `tool-use-id`, or by a background shell's launch task ID; other injected context is skipped |
| `assistant` | Text, thinking and tool calls stay in native part order |
| `tool_result` | Matched by exact call ID and folded into its invocation |
| `system` | Native control/metadata records; retained in raw/debug output |

Slash commands are system records, not user prompts. Empty/system-only sessions
are skipped by sync, list, reindex and watch. Unknown fields survive in debug
records. Malformed records are logged and skipped; a trailing partial write is
retried on the next activity. Each record is bounded to 64 MiB while reading.
An oversized record is drained through its newline, allowing later turns to
survive. Metadata-only enumeration retains envelope metadata and the first
real user prompt rather than accumulating transcript bodies.

## Message and tool conversion

Parts may contain text, `thought: true`, `functionCall`, `functionResponse`,
`inlineData`, or `fileData`. Each assistant part becomes a distinct message
with a stable part-index suffix. Usage is attached only to the final message
from that record. Token fields map directly to input, output, cached and
thinking counts; the provider does not invent totals or model names.

Attachment-only prompts remain visible through MIME/URI text markers. Binary
payloads remain in RawData; markdown does not embed base64 images.

Results arrive separately from calls and can be out of order. The response ID
is preferred, with the envelope `toolCallResult.callId` as the fallback.
`response.output`/`response.error` carry model-facing output; `resultDisplay`
may be plain text or a file-operation object with `fileDiff`. Failure text
wins over success formatting, including status-only error/cancellation cases.

Rendering uses shared fence, language, diff, string and todo helpers. Reads
preserve indentation; search results are fenced; writes retain full content and
results; edits retain complete native diffs or a before/after fallback.
Questions show choices, notebook edits show source and native diffs, and `exec`
shows JavaScript. Writes retain explicit `record_as_artifact` options. JSON
result strings are indented without converting numeric values. Shell results
show compact stdout/stderr plus recorded directory, exit code and error/signal
details; monitor results retain the complete startup response and limits.
Background shells retain `is_background`, the full launch acknowledgment and
output-file path, plus later status, exit code and output tail. Shell completion
notifications omit `tool-use-id`, so their `task-id` is matched to the ID in the
original shell launch response. Unmatched or ambiguous task IDs are not guessed.
Output-tail indentation and XML attributes marking truncation or unreadable
files are preserved; referenced output files are not read during export.
Known tools get scalar argument labels with structured data retained as JSON.
Unknown tools retain generic JSON input/output. Nothing is silently dropped
because the provider does not recognize an output object.

`replace`, `search_file_content`, and `task` are vendor-declared aliases for
`edit`, `grep_search`, and `agent`. Dynamic `computer_use__*` tools are generic;
MCP tools remain unknown/generic-rendered. They are external inventories, so the
factory's built-in declaration reading does not pretend to enumerate them.
Subagent launches and asynchronous results are represented in the parent's
tool blocks. XML-escaped task notifications are decoded and retained in delivery
order, including monitor events and completion/cancellation sequences. These
notifications never become human prompts or replace the immediate tool response.
Separate child transcripts live under `subagents/<session-id>/`, outside the
`chats/` files enumerated as top-level sessions; the parent export includes the
reported result, not the child's complete internal transcript.

## Watch and execution contract

The watcher establishes a silent startup snapshot before returning. It watches
recent transcript files using fsnotify, with a one-second metadata reconcile
for new files and missed events. It never watches the entire flat chats
directory on macOS, where that would hold a descriptor per historical file.
File watches and remembered signatures are pruned using `spi.WatchWindowCutoff`.
A resumed old file becomes active again when its mtime advances.

For a missing store, one watch follows the nearest existing ancestor. A newly
created chats directory is scanned so the first session is not lost during
bootstrap. Traversal stops at the session-file depth. Delivery is ordered and
panic-contained. Each start owns a new context. Stop performs a final disk
reconcile and joins all callbacks before `run` returns the agent's exit status.
A callback that never returns will also prevent shutdown, matching the promise
to join saves; there is no silent timeout that abandons a save.

Custom commands and configured `qwen_cmd` are honored. An explicit requested
resume ID replaces pinned, bare or equals-form `--resume`/`-r` arguments without
mutating the caller's argument slice. Qwen launches in the selected project cwd.

## Reconstruction

Reconstruction uses shared turn preparation and emits user/model text records
with fresh UUIDs, a parent chain, a new session ID and millisecond timestamps.
The first record includes `specstorySourceSessionId`. User account metadata and
invented telemetry are not emitted. No additional end-of-session record was
required by the verified native loader.

`NativeSessionPath` returns the path without creating directories. The command
layer creates the destination and writes the generated bytes. Both native and
cross-provider resume are supported. Cloud resume uses the same serializer,
but authenticated cloud upload/download and interactive picker verification
remain separate release checks.
