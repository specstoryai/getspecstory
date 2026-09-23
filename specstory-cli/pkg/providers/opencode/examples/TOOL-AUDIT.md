# OpenCode Tool Audit

Tested with OpenCode 2.0.14 on macOS 26.6.2 (Darwin 25.6.0 arm64), `build` agent, default configuration, no plugins or MCP servers, model `opencode/big-pickle`. Rendered by this branch's `./specstory sync` (see `versions.txt`).

The native records are the captured fixtures in `../testdata/`; the rendered histories are the files next to this one. Each rendered block pairs with the native tool part in the same order (asserted by `TestConvertToolExerciseSession`). The data line is the fixture line holding the assistant record that contains the call.

## Graded tool calls

| Tool | Markdown line | Grade | Data file | Data line | Comment |
|---|---|---|---|---|---|
| `read` | examples/tool-exercise.md:47 | formatted | testdata/tool-exercise.jsonl | 3 | caption plus the file (language fence) or directory listing (text fence); offset/limit as labeled lines |
| `shell` | examples/tool-exercise.md:121 | formatted | testdata/tool-exercise.jsonl | 3 | command in a bash fence; output sanitized; exit code from metadata; workdir/timeout labeled |
| `read` | examples/tool-exercise.md:239 | formatted | testdata/tool-exercise.jsonl | 4 | caption plus the file (language fence) or directory listing (text fence); offset/limit as labeled lines |
| `glob` | examples/tool-exercise.md:255 | formatted | testdata/tool-exercise.jsonl | 4 | pattern in summary; path as a labeled line; matches in a text fence |
| `webfetch` | examples/tool-exercise.md:267 | formatted | testdata/tool-exercise.jsonl | 4 | URL in summary; page in a markdown (or html) fence |
| `websearch` | examples/tool-exercise.md:283 | formatted | testdata/tool-exercise.jsonl | 4 | failed call: error text rendered (Web search cancelled) |
| `write` | examples/tool-exercise.md:316 | formatted | testdata/tool-exercise.jsonl | 5 | content in a language fence; acknowledgement inline |
| `edit` | examples/tool-exercise.md:333 | formatted | testdata/tool-exercise.jsonl | 5 | OpenCode's unified diff from metadata; replace-all flagged |
| `grep` | examples/tool-exercise.md:360 | formatted | testdata/tool-exercise.jsonl | 5 | pattern in summary; path/include as labeled lines; matches in a text fence |
| `shell` | examples/tool-exercise.md:381 | formatted | testdata/tool-exercise.jsonl | 5 | command in a bash fence; output sanitized; exit code from metadata; workdir/timeout labeled |
| `websearch` | examples/tool-exercise.md:399 | formatted | testdata/tool-exercise.jsonl | 5 | failed call: error text rendered (Web search cancelled) |
| `skill` | examples/tool-exercise.md:453 | formatted | testdata/tool-exercise.jsonl | 6 | skill id in summary; wrapper stripped; document in a markdown fence |
| `subagent` | examples/tool-exercise.md:737 | formatted | testdata/tool-exercise.jsonl | 6 | agent, prompt, child session id and answer |
| `execute` | examples/tool-exercise.md:759 | formatted | testdata/tool-exercise.jsonl | 6 | Code Mode script in a javascript fence; result (json when JSON); inner calls listed |
| `read` | examples/tool-exercise.md:789 | formatted | testdata/tool-exercise.jsonl | 6 | caption plus the file (language fence) or directory listing (text fence); offset/limit as labeled lines |
| `execute` | examples/tool-exercise.md:842 | formatted | testdata/tool-exercise.jsonl | 7 | Code Mode script in a javascript fence; result (json when JSON); inner calls listed |
| `read` | examples/tool-exercise.md:893 | formatted | testdata/tool-exercise.jsonl | 7 | failed call: error text rendered (File not found: missing.txt) |
| `read` | examples/tool-exercise.md:901 | formatted | testdata/tool-exercise.jsonl | 7 | caption plus the file (language fence) or directory listing (text fence); offset/limit as labeled lines |
| `shell` | examples/tool-exercise.md:935 | formatted | testdata/tool-exercise.jsonl | 8 | command in a bash fence; output sanitized; exit code from metadata; workdir/timeout labeled |
| `read` | examples/optional-params.md:41 | formatted | testdata/optional-params.jsonl | 3 | caption plus the file (language fence) or directory listing (text fence); offset/limit as labeled lines |
| `read` | examples/optional-params.md:73 | formatted | testdata/optional-params.jsonl | 4 | caption plus the file (language fence) or directory listing (text fence); offset/limit as labeled lines |
| `read` | examples/optional-params.md:95 | formatted | testdata/optional-params.jsonl | 4 | caption plus the file (language fence) or directory listing (text fence); offset/limit as labeled lines |
| `read` | examples/optional-params.md:112 | formatted | testdata/optional-params.jsonl | 4 | caption plus the file (language fence) or directory listing (text fence); offset/limit as labeled lines |
| `read` | examples/optional-params.md:130 | formatted | testdata/optional-params.jsonl | 5 | caption plus the file (language fence) or directory listing (text fence); offset/limit as labeled lines |
| `read` | examples/optional-params.md:175 | formatted | testdata/optional-params.jsonl | 6 | caption plus the file (language fence) or directory listing (text fence); offset/limit as labeled lines |
| `glob` | examples/optional-params.md:192 | formatted | testdata/optional-params.jsonl | 6 | pattern in summary; path as a labeled line; matches in a text fence |
| `grep` | examples/optional-params.md:206 | formatted | testdata/optional-params.jsonl | 6 | pattern in summary; path/include as labeled lines; matches in a text fence |
| `shell` | examples/optional-params.md:223 | formatted | testdata/optional-params.jsonl | 6 | command in a bash fence; output sanitized; exit code from metadata; workdir/timeout labeled |
| `webfetch` | examples/optional-params.md:244 | formatted | testdata/optional-params.jsonl | 6 | URL in summary; page in a markdown (or html) fence |
| `edit` | examples/optional-params.md:258 | formatted | testdata/optional-params.jsonl | 6 | OpenCode's unified diff from metadata; replace-all flagged |
| `read` | examples/optional-params.md:307 | formatted | testdata/optional-params.jsonl | 7 | caption plus the file (language fence) or directory listing (text fence); offset/limit as labeled lines |
| `question` | examples/question.md:21 | formatted | testdata/question.jsonl | 3 | questions and options as lists; chosen answer from metadata |

## Inventory coverage

`../testdata/tools.txt` (read by `TestToolInventorySweep`) lists the direct tools declared to the model; two `factory/list-tools` readings under an isolated home agreed on the same twelve.

| Tool | Declared | Enabled | Exercised | Notes |
|---|---|---|---|---|
| `edit` | yes | yes | yes | |
| `execute` | yes | yes | yes | Code Mode `search()` and `opencode.models` succeeded; browser calls failed (no desktop browser connected) and render as the inner-call error |
| `glob` | yes | yes | yes | |
| `grep` | yes | yes | yes | |
| `question` | yes | yes (TUI only) | yes | Asked from the interactive TUI, which shows the form; `opencode run` has no one to answer it |
| `read` | yes | yes | yes | including a missing file |
| `shell` | yes | yes | yes | including a non-zero exit |
| `skill` | yes | yes | yes | built-in `opencode` skill |
| `subagent` | yes | yes | yes | `explore` agent; its conversation is a child session, not listed on its own |
| `webfetch` | yes | yes | yes | markdown and html formats |
| `websearch` | yes | yes | failure only | Both attempts returned `Web search cancelled` (no web search provider configured); the error rendering is verified, the success rendering is generic text and unverified |
| `write` | yes | yes | yes | |

The Code Mode catalog (`browser.*`, `opencode.*`) is reachable only inside `execute` and appears in session data only as `execute`'s `metadata.toolCalls`, which the `execute` renderer lists. `patch` (OpenCode's name for `apply_patch`, offered to some OpenAI models) is known to the TUI's renderer but was not declared to the models available here, so it has no bespoke renderer and would render generically.
