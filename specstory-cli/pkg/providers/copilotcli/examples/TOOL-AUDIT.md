# Copilot CLI tool rendering audit

Source session: [`testdata/tool-exercise.jsonl`](../testdata/tool-exercise.jsonl), a real capture (session `4ea18a78-3f3c-4f67-a61e-21fab96427be`) with hook and `system.message` records removed and paths anonymized. Rendered history: [`tool-exercise.md`](tool-exercise.md), produced by `specstory sync copilot -s 4ea18a78-3f3c-4f67-a61e-21fab96427be --print` from the session's recorded directory (`/private/tmp/copilot-tools-exercise`). The capture was also synced with `--log --debug --debug-raw`: no schema warnings and no unknown-kind logs. Versions: [`versions.txt`](versions.txt). Inventory: [`testdata/tools.txt`](../testdata/tools.txt).

Configuration: an isolated `COPILOT_HOME` holding only the login and settings; no plugins, no user MCP servers, no custom instructions, no extensions. Main model `claude-sonnet-5.5`; the `task` sub-agent ran on `gpt-5.6-luna`.

## Declared, enabled and exercised

- **Declared:** Copilot publishes no tool declaration, so the 25 tools in the first section of `tools.txt` are the model's report of its tools in this configuration.
- **Enabled:** all 25. Nothing was disabled.
- **Exercised:** 21 of the 25, in 27 calls (`view`, `bash` and `list_agents` more than once), including two failed calls (`success: false`), one shell command that exits 1, and one sub-agent (sidechain) call.

Every invocation in the native log appears once, in native order, with its own result. The prompts and agent replies that quote tool names produce no phantom blocks.

Rendering follows the VS Code Copilot provider's style (shared `spi.RenderToolInputList`, `RenderShellCall` and `RenderToolResult`): a `Tool use: **<name>**` summary, the call's intent or main argument as the first line of the body, an **Input** list, then a **Result** fence capped at `spi.ToolResultCap`. Failed calls render the native error message as their result, as the VS Code provider does; the normalized output marks them `is_error`.

## Exercised tools

| Tool | Markdown line | Grade | Data file | Data line | Comment |
| --- | --- | --- | --- | --- | --- |
| `view` | 15 | formatted | `testdata/tool-exercise.jsonl` | 7 (call), 9 (result) | `read`. Path in the intent line and the input; file list as a plain fenced result. |
| `bash` | 65 | formatted | `testdata/tool-exercise.jsonl` | 13 (call), 35 (result) | `shell`. Description as the intent line, command in a bash fence, output with the shell's exit line. |
| `bash` | 84 | formatted | `testdata/tool-exercise.jsonl` | 13 (call), 32 (result) | `shell`. Failed command (exit 1): the stderr text and exit code are shown; the call itself succeeded. |
| `bash` | 103 | formatted | `testdata/tool-exercise.jsonl` | 13 (call), 31 (result) | `shell`. Async start: the background-shell notice is the result. |
| `create` | 121 | formatted | `testdata/tool-exercise.jsonl` | 13 (call), 29 (result) | `write`. Multi-line file_text gets its own fence; path inline. |
| `glob` | 146 | formatted | `testdata/tool-exercise.jsonl` | 13 (call), 34 (result) | `search`. Pattern as the intent line; both inputs; matched paths. |
| `web_fetch` | 166 | formatted | `testdata/tool-exercise.jsonl` | 13 (call), 33 (result) | `read`. URL as the intent line; max_length and url inputs; fetched text. |
| `dynamic_workflows_manage` | 186 | formatted | `testdata/tool-exercise.jsonl` | 13 (call), 25 (result) | `generic`. Intent line from the call's own description; operation input; result text. |
| `sql` | 204 | formatted | `testdata/tool-exercise.jsonl` | 13 (call), 28 (result) | `generic`. Description and SQL query inputs; the tool's markdown table kept verbatim in the result fence. |
| `session_store_sql` | 227 | formatted | `testdata/tool-exercise.jsonl` | 13 (call), 26 (result) | `generic`. Same as sql, plus the source input. |
| `list_agents` | 253 | formatted | `testdata/tool-exercise.jsonl` | 13 (call), 27 (result) | `generic`. No arguments, so no Input list; result shown. |
| `list_bash` | 265 | formatted | `testdata/tool-exercise.jsonl` | 13 (call), 30 (result) | `shell`. No arguments; result shown. |
| `edit` | 283 | formatted | `testdata/tool-exercise.jsonl` | 39 (call), 45 (result) | `write`. old_str and new_str inputs plus path; confirmation result. |
| `read_bash` | 303 | formatted | `testdata/tool-exercise.jsonl` | 39 (call), 52 (result) | `shell`. delay and shellId inputs; still-running notice. |
| `task` | 322 | formatted | `testdata/tool-exercise.jsonl` | 39 (call), 60 (result) | `generic`. Sub-agent launch: all five inputs and the agent's final reply. Its own turns follow as sidechain messages. |
| `fetch_copilot_cli_documentation` | 345 | formatted | `testdata/tool-exercise.jsonl` | 39 (call), 44 (result) | `read`. No arguments; long help text capped by the shared result limit with the truncation marker. |
| `view` | 413 | formatted | `testdata/tool-exercise.jsonl` | 54 (call), 56 (result) | `read`. Sidechain tool call, under an `Agent - sidechain` header with the sub-agent's model. |
| `view` | 443 | formatted | `testdata/tool-exercise.jsonl` | 67 (call), 72 (result) | `read`. Shows the edited content. |
| `grep` | 461 | formatted | `testdata/tool-exercise.jsonl` | 67 (call), 73 (result) | `search`. Pattern as the intent line; flag-style input (-n) kept literally. |
| `stop_bash` | 482 | formatted | `testdata/tool-exercise.jsonl` | 67 (call), 71 (result) | `shell`. shellId input; stop confirmation. |
| `view` | 534 | formatted | `testdata/tool-exercise.jsonl` | 85 (call), 94 (result, failed) | `read`. Failed call (success=false): the native error message is the result; schema output carries is_error. |
| `list_agents` | 552 | formatted | `testdata/tool-exercise.jsonl` | 85 (call), 93 (result) | `generic`. scope input; result. |
| `github-mcp-server-search_users` | 568 | formatted | `testdata/tool-exercise.jsonl` | 85 (call), 97 (result) | `search`. GitHub MCP call: query as the intent line; JSON result kept verbatim. |
| `github-mcp-server-get_file_contents` | 587 | formatted | `testdata/tool-exercise.jsonl` | 85 (call), 100 (result) | `read`. GitHub MCP call: path as the intent line; long file capped with the truncation marker. |
| `github-mcp-server-list_copilot_spaces` | 644 | formatted | `testdata/tool-exercise.jsonl` | 85 (call), 98 (result, failed) | `generic`. Failed GitHub MCP call (success=false): the server's error message is the result. |
| `github-mcp-server-search_code` | 656 | formatted | `testdata/tool-exercise.jsonl` | 85 (call), 99 (result) | `search`. GitHub MCP call: array input (fields) rendered as compact JSON. |
| `skill` | 676 | formatted | `testdata/tool-exercise.jsonl` | 85 (call), 95 (result) | `generic`. Skill name as the intent line and input; load confirmation. |

## Unexercised tools

Not graded.

| Tool | Reason |
| --- | --- |
| `run_dynamic_workflow` | Deliberately not called: it launches a multi-agent workflow and spends credits. Types `generic`; would render through the shared input list. |
| `read_agent`, `write_agent` | Need a background agent ID; the one `task` agent ran synchronously and finished, leaving none (`list_agents` confirms). |
| `github-mcp-server-get_copilot_space` | Needs a Copilot Space owner and name; this account has no Spaces (`list_copilot_spaces` failed with not_found). |
| Tools in the later sections of `tools.txt` | Not in this configuration's tool set: they were observed in the user's own sessions (terminal CLI with plan mode and the GitHub app's session tools, the GitHub Copilot app host, VS Code's agent host). They render through the same shared input list; `TestToolInventorySweep` asserts each name's type, and the 126 sessions in the author's own store (52 projects) sync with no Warn or Error log. |
| `task_complete` | Autopilot only; not a tool block. Its summary renders as agent text (`TestTranscriptBuilder`). |
