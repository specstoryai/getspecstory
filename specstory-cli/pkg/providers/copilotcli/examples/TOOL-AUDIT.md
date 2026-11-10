# Copilot CLI tool rendering audit

Source session: [`testdata/tool-exercise.jsonl`](../testdata/tool-exercise.jsonl), a real capture (session `ab68f63a-ec2a-4635-947c-487f56661807`) with the system prompt, model-call records and an attached screenshot removed, and account data, home paths and other sessions' IDs anonymized. Rendered history: [`tool-exercise.md`](tool-exercise.md), produced by `specstory sync copilot` from the session's recorded directory (`/private/tmp/copilot-v3`). The capture was also synced with `--log --debug --debug-raw`: no schema warnings and no unknown-kind logs. Versions: [`versions.txt`](versions.txt). Inventory: [`testdata/tools.txt`](../testdata/tools.txt).

Configuration: GitHub Copilot CLI 1.0.105 on macOS, run interactively; no plugin or user MCP server tools, no custom instructions. The experimental extensions feature was turned on partway, because the workflow tools need it. Main model `claude-sonnet-5.5`; the `task` sub-agents ran on `gpt-5.6-luna`.

## Declared, enabled and exercised

- **Declared:** Copilot publishes no tool declaration, so the 27 tools in the first section of `tools.txt` are the model's report of its tools in this configuration.
- **Enabled:** all 27. Nothing was disabled.
- **Exercised:** all 27, in 46 calls, including four failed calls (`success: false`), one shell command that exits 1, and three sub-agent (sidechain) calls.
- **Grades:** all 46 formatted.

Every invocation in the native log appears once, in native order, with its own result. The prompts and agent replies that quote tool names produce no phantom blocks.

Every block has a `Tool use: **<name>**` summary and the call's intent or main argument as the first line of the body. `edit` then shows a `Path:` line and a `diff` fence, `create` a `Path:` line and a fence tagged by extension, and a shell command a `bash` or `powershell` fence with its output in a `text` fence. Every other tool uses the shared **Input** list. A result that is a JSON document is indented in a `json` fence. Results are capped at `spi.ToolResultCap`. Failed calls render the native error message as their result, as the VS Code provider does; the normalized output marks them `is_error`.

## Exercised tools

| Tool | Markdown line | Grade | Data file | Data line | Comment |
| --- | --- | --- | --- | --- | --- |
| `create` | 15 | formatted | `testdata/tool-exercise.jsonl` | 8 (call), 10 (result) | `write`. `Path:` line, then file_text in a fence tagged by extension. |
| `bash` | 94 | formatted | `testdata/tool-exercise.jsonl` | 20 (call), 31 (result) | `shell`. Intent line, command in a bash fence, output in a text fence. |
| `bash` | 112 | formatted | `testdata/tool-exercise.jsonl` | 20 (call), 32 (result) | `shell`. Command exited 1: output and `Exit code: 1` shown; the call itself succeeded. Intent line, command in a bash fence, output in a text fence. |
| `view` | 133 | formatted | `testdata/tool-exercise.jsonl` | 20 (call), 27 (result, failed) | `read`. Failed call: the native error is the result. Path in the intent line and the input; contents as a fenced result. |
| `view` | 151 | formatted | `testdata/tool-exercise.jsonl` | 20 (call), 28 (result) | `read`. Path in the intent line and the input; contents as a fenced result. |
| `glob` | 174 | formatted | `testdata/tool-exercise.jsonl` | 20 (call), 30 (result) | `search`. Pattern as the intent line and the input. |
| `grep` | 192 | formatted | `testdata/tool-exercise.jsonl` | 20 (call), 29 (result) | `search`. Input list with the options; matches as a fenced result. |
| `read_bash` | 221 | formatted | `testdata/tool-exercise.jsonl` | 36 (call), 41 (result) | `shell`. Input list, output in a text fence with the exit line. |
| `bash` | 242 | formatted | `testdata/tool-exercise.jsonl` | 36 (call), 42 (result) | `shell`. Intent line, command in a bash fence, output in a text fence. |
| `create` | 260 | formatted | `testdata/tool-exercise.jsonl` | 36 (call), 40 (result) | `write`. `Path:` line, then file_text in a fence tagged by extension. |
| `list_bash` | 280 | formatted | `testdata/tool-exercise.jsonl` | 46 (call), 49 (result) | `shell`. Session list in a text fence. |
| `edit` | 298 | formatted | `testdata/tool-exercise.jsonl` | 46 (call), 50 (result) | `write`. `Path:` line, then a diff of old_str and new_str; confirmation result. |
| `stop_bash` | 323 | formatted | `testdata/tool-exercise.jsonl` | 54 (call), 62 (result) | `shell`. Input list, stop notice in a text fence. |
| `web_fetch` | 339 | formatted | `testdata/tool-exercise.jsonl` | 54 (call), 70 (result) | `read`. URL as the intent line. |
| `fetch_copilot_cli_documentation` | 359 | formatted | `testdata/tool-exercise.jsonl` | 54 (call), 63 (result) | `read`. Long result capped with the visible marker. |
| `sql` | 423 | formatted | `testdata/tool-exercise.jsonl` | 54 (call), 64 (result) | `generic`. Query in the input; rows as a fenced result. |
| `session_store_sql` | 452 | formatted | `testdata/tool-exercise.jsonl` | 54 (call), 82 (result) | `generic`. Query in the input; rows as a fenced result (session IDs anonymized in the fixture). |
| `task` | 482 | formatted | `testdata/tool-exercise.jsonl` | 54 (call), 81 (result) | `generic`. Description as the intent line; prompt in the input. |
| `bash` | 508 | formatted | `testdata/tool-exercise.jsonl` | 72 (call), 74 (result) | `shell`. Sub-agent call. Intent line, command in a bash fence, output in a text fence. |
| `task` | 539 | formatted | `testdata/tool-exercise.jsonl` | 86 (call), 95 (result) | `generic`. Description as the intent line; prompt in the input. |
| `semantic_code_search` | 561 | formatted | `testdata/tool-exercise.jsonl` | 86 (call), 104 (result) | `search`. Query as the intent line; long result capped with the visible marker. |
| `github-mcp-server-get_file_contents` | 656 | formatted | `testdata/tool-exercise.jsonl` | 86 (call), 112 (result) | `read`. JSON result indented in a json fence. |
| `github-mcp-server-search_code` | 702 | formatted | `testdata/tool-exercise.jsonl` | 86 (call), 105 (result) | `search`. JSON result indented in a json fence. |
| `github-mcp-server-search_users` | 735 | formatted | `testdata/tool-exercise.jsonl` | 86 (call), 103 (result) | `search`. JSON result indented in a json fence. |
| `github-mcp-server-list_copilot_spaces` | 765 | formatted | `testdata/tool-exercise.jsonl` | 86 (call), 102 (result) | `generic`. JSON result indented in a json fence. |
| `dynamic_workflows_manage` | 785 | formatted | `testdata/tool-exercise.jsonl` | 86 (call), 96 (result) | `generic`. Operation in the input; object meta in a fence. |
| `bash` | 807 | formatted | `testdata/tool-exercise.jsonl` | 107 (call), 109 (result) | `shell`. Sub-agent call. Intent line, command in a bash fence, output in a text fence. |
| `github-mcp-server-get_copilot_space` | 830 | formatted | `testdata/tool-exercise.jsonl` | 120 (call), 127 (result) | `generic`. Plain-text result. |
| `read_agent` | 849 | formatted | `testdata/tool-exercise.jsonl` | 120 (call), 125 (result) | `generic`. Agent ID in the input; reply as a fenced result. |
| `list_agents` | 870 | formatted | `testdata/tool-exercise.jsonl` | 120 (call), 126 (result) | `generic`. Agent list as a fenced result. |
| `ask_user` | 886 | formatted | `testdata/tool-exercise.jsonl` | 120 (call), 129 (result) | `generic`. Question and fenced schema in the input; the answer is the result. |
| `write_agent` | 929 | formatted | `testdata/tool-exercise.jsonl` | 133 (call), 136 (result) | `generic`. Message in the input; delivery notice. |
| `skill` | 946 | formatted | `testdata/tool-exercise.jsonl` | 133 (call), 137 (result) | `generic`. Skill name as the intent line and the input. |
| `bash` | 976 | formatted | `testdata/tool-exercise.jsonl` | 145 (call), 147 (result) | `shell`. Sub-agent call. Intent line, command in a bash fence, output in a text fence. |
| `read_agent` | 1005 | formatted | `testdata/tool-exercise.jsonl` | 154 (call), 158 (result) | `generic`. Agent ID in the input; reply as a fenced result. |
| `bash` | 1031 | formatted | `testdata/tool-exercise.jsonl` | 154 (call), 160 (result) | `shell`. Intent line, command in a bash fence, output in a text fence. |
| `sql` | 1050 | formatted | `testdata/tool-exercise.jsonl` | 154 (call), 159 (result) | `generic`. Query in the input; rows as a fenced result. |
| `bash` | 1116 | formatted | `testdata/tool-exercise.jsonl` | 170 (call), 172 (result) | `shell`. Intent line, command in a bash fence, output in a text fence. |
| `dynamic_workflows_manage` | 1152 | formatted | `testdata/tool-exercise.jsonl` | 182 (call), 184 (result, failed) | `generic`. Failed call: the native error is the result. Operation in the input; object meta in a fence. |
| `dynamic_workflows_manage` | 1178 | formatted | `testdata/tool-exercise.jsonl` | 188 (call), 190 (result, failed) | `generic`. Failed call: the native error is the result. Operation in the input; object meta in a fence. |
| `run_dynamic_workflow` | 1242 | formatted | `testdata/tool-exercise.jsonl` | 200 (call), 202 (result, failed) | `generic`. Failed call: the native error is the result. Name and args in the input. |
| `dynamic_workflows_manage` | 1300 | formatted | `testdata/tool-exercise.jsonl` | 220 (call), 222 (result) | `generic`. Operation in the input; object meta in a fence. |
| `view` | 1323 | formatted | `testdata/tool-exercise.jsonl` | 226 (call), 228 (result) | `read`. Path in the intent line and the input; contents as a fenced result. |
| `dynamic_workflows_manage` | 1400 | formatted | `testdata/tool-exercise.jsonl` | 232 (call), 234 (result) | `generic`. Operation in the input; object meta in a fence. |
| `run_dynamic_workflow` | 1436 | formatted | `testdata/tool-exercise.jsonl` | 238 (call), 240 (result) | `generic`. Name and args in the input. |
| `dynamic_workflows_manage` | 1465 | formatted | `testdata/tool-exercise.jsonl` | 258 (call), 260 (result) | `generic`. Operation in the input; object meta in a fence. |

## Unexercised tools

Not graded.

| Tool | Reason |
| --- | --- |
| Tools in the later sections of `tools.txt` | Not in this configuration's tool set: they were observed in the user's own sessions (terminal CLI with plan mode and the GitHub app's session tools, the GitHub Copilot app host, VS Code's agent host). They render through the same shared input list; `TestToolInventorySweep` asserts each name's type, and the 126 sessions in the author's own store (52 projects) sync with no Warn or Error log. |
| `write_bash` | Named by the CLI bundle but absent from the model report and from captured sessions. Types `shell`. |
| `powershell`, `read_powershell`, `write_powershell`, `stop_powershell`, `list_powershell` | Windows only, and this audit ran on macOS. Named by the CLI bundle. Type `shell`; a `powershell` call renders like `bash`. |
| `local_shell` | Not a model tool: recorded only for a user's `!` command, which renders as a user message (`TestCapturedUserShellCommands`). |
| `task_complete` | Autopilot only; not a tool block. Its summary renders as agent text (`TestTranscriptBuilder`). |
