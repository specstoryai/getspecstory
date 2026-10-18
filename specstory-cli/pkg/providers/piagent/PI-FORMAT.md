# Pi session format

Baseline: **0.85.1**, the exact output of `pi --version` from `@earendil-works/pi-coding-agent`. Observations below come from its installed `session-manager.js`, tool declarations and implementations, and the [captured examples](examples/).

## Store and durable writes

The default store is `~/.pi/agent/sessions/--<encoded-cwd>--/`. `PI_CODING_AGENT_DIR` replaces `~/.pi/agent`; `PI_CODING_AGENT_SESSION_DIR` instead selects a flat directory shared by projects. A per-invocation `--session-dir` is not discoverable by this provider unless the override also identifies it.

Pi canonicalizes the local cwd, removes one leading slash or backslash, replaces remaining slashes, backslashes and colons with hyphens, and wraps the result in `--`. This encoding is lossy: project attribution must check the header's `cwd`. A recorded cwd can belong to another machine and is never canonicalized by the reader.

Files are named `<timestamp>_<session-id>.jsonl`, with colons and periods in the timestamp replaced by hyphens. Each JSONL file is the durable transcript for one session. Normal creation buffers the header, settings and initial user turn in memory until the first assistant message; Pi then writes the accumulated records and appends subsequent entries. A newly started conversation can therefore be absent from disk. Loading and migrating an existing file, branching into a new session, and file replacement must not be treated as ordinary append-only activity. The provider watches durable JSONL files directly, retaining only the active tree path for display. Nested extension/subagent files are outside its session-discovery depth.

There is no shared rolling transcript, checkpoint or lock file that this provider needs to read. Watch startup records existing files without publishing them; writes, late directory creation and replacement trigger bounded reconciliation. Shutdown drains callbacks and performs a final reconciliation.

## Header and records

The first nonblank record is a header:

```json
{"type":"session","version":3,"id":"session-id","timestamp":"2026-09-17T12:00:00.000Z","cwd":"/project"}
```

`version: 3` describes the file format, not the Pi application release. Normalized `Provider.Version` is therefore `unknown`. Normalized workspace selection is native `cwd`, then caller project, then process cwd; global enumeration leaves an absent native origin empty.

Subsequent records have `type`, `id`, `parentId` (null at the root), and an ISO timestamp. Message records wrap a `message` object; native messages also carry epoch-millisecond timestamps. File order chooses the latest leaf; following `parentId` and reversing the resulting path gives the active conversation. Inactive branches remain in `RawData` but are not displayed. Cycles terminate the walk. Compaction markers preserve the earlier transcript rather than applying Pi's context-window truncation.

| Entry type | Interpretation |
| --- | --- |
| `message` | User/assistant conversation or a tool result |
| `model_change`, `thinking_level_change` | Runtime settings; assistant records carry their own model |
| `compaction` | Render `summary` as a marker while retaining earlier turns |
| `branch_summary` | Context about a branch the user left; not an active conversation turn |
| `session_info` | User-visible session name; latest recorded value used in listing |
| `label` | Tree-navigation metadata |
| `custom`, `custom_message` | Extension state/injected context; retained raw, omitted from conversation |

Unknown kinds and roles warn with file and physical line number. Valid envelopes remain in the tree so descendants do not lose their ancestors. Oversized and malformed body records warn and are skipped. A missing/invalid header prevents conversion. Accepted original record bytes supply normalized data, raw uploads and debug exports from one snapshot.

## Messages

- `user`: `content` is a string or an array of content blocks. Text is rendered; images remain in raw records.
- `assistant`: `content` interleaves `text`, `thinking` and `toolCall` blocks. Tool calls have `id`, `name` and `arguments`. Normalization preserves this ordering, assigning distinct message IDs and carrying usage once per native assistant entry.
- `toolResult`: `toolCallId`, `toolName`, `content`, optional `details`, and `isError`. Results merge into the matching call within the exchange. Text results and structured details remain available in normalized tool output.
- `bashExecution`: rendered as a user message labeled "User ran a shell command", with the command, output, recorded exit code, and cancellation/truncation markers. It starts a user exchange without inventing an assistant tool call. Shell output is sanitized and capped like shell-tool output; accepted raw records retain the original data. `excludeFromContext` does not suppress the message from either the archive or reconstructed conversation text.
- `custom`, `branchSummary`, `compactionSummary`: context-only message roles, omitted from normalized conversation. Durable `compaction` entries are handled separately.

Assistant `model` is the actual model label when recorded. `usage.input`, `output`, `reasoning`, `cacheRead` and `cacheWrite` map to their distinct schema fields. `totalTokens` is not relabeled as input tokens. Provider/API identities and unsupported content remain in raw data.

## Native tools

Pi's `getAllTools()` declares eight built-ins; the default active subset is `read`, `bash`, `edit`, `write`. Extensions can add tools, which receive generic rendering and type `unknown` until supported by evidence.

| Tool | Arguments observed in 0.85.1 | Result and rendering |
| --- | --- | --- |
| `read` | `path`, optional `offset`, `limit` | Text or image blocks; text fenced using the file extension |
| `write` | `path`, `content` | Success text; full input content in an extension-tagged fence |
| `edit` | `path`, `edits: [{oldText,newText}]` | Success text plus `details.diff`, `details.patch`, `details.firstChangedLine`; requested and resulting edits shown as diffs |
| `bash` | `command`, optional `timeout` | Text stdout/stderr; sanitized control sequences in a `text` fence |
| `powershell` | `command`, optional `timeout` | Windows-only execution; same shell rendering and error precedence |
| `grep` | `pattern`, optional `path`, `glob`, `ignoreCase`, `literal`, `context`, `limit` | Text matches; labeled scope and options |
| `find` | `pattern`, optional `path`, `limit` | Text paths; requires Pi's `fd` helper |
| `ls` | optional `path`, `limit` | Text listing; classified as workspace read |

Results can include truncation metadata and temporary output paths in `details`; unknown fields remain visible as deterministic JSON. Displayed results are capped with an explicit marker; input content is not truncated. Failed calls show errors before generic input details rather than success-formatting diagnostic diffs. Native error results and host limitations are distinct from missing rendering support.

## Resume and reconstruction

`pi --session-id <id>` selects the exact header ID for the current project and appends to that same file. If Pi cannot find it, it creates a session with that ID; the provider must place the reconstructed file in the correct store first. Canonical local destination spelling matters, including `/tmp` versus `/private/tmp` on macOS.

Reconstruction creates a fresh v3 header, UUIDs, a linear parent chain, and `specstorySourceSessionId`. The shared portability contract flattens text, thinking and tool descriptions into user/assistant text and removes synthetic command scaffolding. It deliberately drops model/provider/API metadata, usage and path hints. Consequently imported assistant messages encode empty `provider`, `model` and `api` strings; they must not be attributed to an invented model. A migration note also has no model attribution.

Pi 0.85.1 accepted this representation in a live headless resume, recalled an earlier token and appended to the same file with an explicitly selected live model. If no model is selected explicitly, Pi's loader may report that it could not restore the empty historical model and fall back to its configured model. This is a model-selection notice, not evidence of an incomplete or corrupt transcript. Native interactive UI and successful PowerShell execution still require separate platform validation.

## Skills

Pi loads project skills from `.pi/skills` and global skills from `~/.pi/agent/skills` (its native agent-directory override also affects global resources). The SpecStory skills registry uses canonical ID `pi` and those default paths, matching the upstream skills layout. Session-store overrides do not move project skills.
