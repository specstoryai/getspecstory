# Grok 1.0.34 tool audit

Environment: macOS arm64, `grok 1.0.34 (3736acbc8658) [stable]`, default workstation configuration. Testrail/Xray MCP discovery was available; Testmo connection reported an authentication requirement. No account tool or feedback message was sent. The 27-name inventory comes from the harness declaration, not the model-written tool list inside the conversation. Paths in this fixture are anonymized; see [capture notes](README.md).

This primary captured session contains 15 native invocations: 14 assistant tool calls and one backend web-search record. They appear exactly once and in native order in [session.md](session.md). Repeated names are distinct call IDs. The provider regression test checks these IDs and order directly.

| # | Tool | Markdown line | Grade | Native file:line | Result line | Audit note |
|---|---|---:|---|---|---:|---|
| 1 | `write` | [26](session.md#L26) | formatted | [chat_history.jsonl:8](../../testdata/session-1.0.34/chat_history.jsonl#L8) | 9 | Complete content and native creation result. |
| 2 | `list_dir` | [100](session.md#L100) | formatted | [chat_history.jsonl:11](../../testdata/session-1.0.34/chat_history.jsonl#L11) | 12 | Directory and complete listing, fenced to preserve layout. |
| 3 | `read_file` | [123](session.md#L123) | formatted | [chat_history.jsonl:14](../../testdata/session-1.0.34/chat_history.jsonl#L14) | 15 | Path, observed range, full text or native failure; language/fence preserved. |
| 4 | `grep` | [161](session.md#L161) | formatted | [chat_history.jsonl:17](../../testdata/session-1.0.34/chat_history.jsonl#L17) | 18 | Pattern, glob, case/context/result limits and full XML-like result are visible. |
| 5 | `search_replace` | [205](session.md#L205) | formatted | [chat_history.jsonl:20](../../testdata/session-1.0.34/chat_history.jsonl#L20) | 21 | Both complete edit inputs and native result. |
| 6 | `search_replace` | [233](session.md#L233) | formatted | [chat_history.jsonl:23](../../testdata/session-1.0.34/chat_history.jsonl#L23) | 24 | Both complete edit inputs and native result. |
| 7 | `todo_write` | [261](session.md#L261) | formatted | [chat_history.jsonl:26](../../testdata/session-1.0.34/chat_history.jsonl#L26) | 27 | All native items/statuses rendered; no second raw copy of the checklist. |
| 8 | `run_terminal_command` | [283](session.md#L283) | formatted | [chat_history.jsonl:29](../../testdata/session-1.0.34/chat_history.jsonl#L29) | 30 | Command/options, exit status and full output; nested fences and Unicode remain intact. |
| 9 | `run_terminal_command` | [315](session.md#L315) | formatted | [chat_history.jsonl:32](../../testdata/session-1.0.34/chat_history.jsonl#L32) | 33 | Command/options, exit status and full output; nested fences and Unicode remain intact. |
| 10 | `read_file` | [343](session.md#L343) | formatted | [chat_history.jsonl:35](../../testdata/session-1.0.34/chat_history.jsonl#L35) | 36 | Path, observed range, full text or native failure; language/fence preserved. |
| 11 | `run_terminal_command` | [367](session.md#L367) | formatted | [chat_history.jsonl:38](../../testdata/session-1.0.34/chat_history.jsonl#L38) | 39 | Command/options, exit status and full output; nested fences and Unicode remain intact. |
| 12 | `get_command_or_subagent_output` | [405](session.md#L405) | formatted | [chat_history.jsonl:41](../../testdata/session-1.0.34/chat_history.jsonl#L41) | 42 | Task IDs/timeout and full recorded task output. |
| 13 | `kill_command_or_subagent` | [447](session.md#L447) | formatted | [chat_history.jsonl:44](../../testdata/session-1.0.34/chat_history.jsonl#L44) | 45 | Task ID and termination result. |
| 14 | `monitor` | [467](session.md#L467) | formatted | [chat_history.jsonl:47](../../testdata/session-1.0.34/chat_history.jsonl#L47) | 48 | Command, timeout, persistent flag and native started response. |
| 15 | `web_search` | [495](session.md#L495) | formatted | [chat_history.jsonl:50](../../testdata/session-1.0.34/chat_history.jsonl#L50) | — | Query and in_progress status preserved; native record has no sources/result. This is pending-path coverage only. |

## Additional captured blocks

The following tables audit every invocation in the additional native captures. `search_tool` returns catalog definitions, including complete JSON schemas under readable tool names; schema JSON is retained as schema, rather than an unformatted envelope. `use_tool` exercised only invalid-name failures. `ask_user_question` completed with the native headless fallback. `workflow` was cancelled by the native permission flow before execution, not a workflow validation failure. Plan entry succeeded but plan exit was not invoked. The final scheduler session used a fresh `HOME` and `GROK_HOME`, with no workstation plugins/MCP configuration.

### Discovery

| # | Tool | Markdown line | Grade | Native file:line | Result line | Audit note |
|---|---|---:|---|---|---:|---|
| 1 | `search_tool` | [25](discovery/session.md#L25) | formatted | [chat_history.jsonl:8](../../testdata/session-1.0.34/discovery/chat_history.jsonl#L8) | 9 | Query/options and each discovered server, tool, description, score/schema and catalog status retained. Catalog lookup only; returned account tools were not called. |
| 2 | `use_tool` | [268](discovery/session.md#L268) | formatted | [chat_history.jsonl:11](../../testdata/session-1.0.34/discovery/chat_history.jsonl#L11) | 12 | Attempted MCP name/input and native invalid-name error preserved. Failure coverage only. |
| 3 | `web_fetch` | [288](discovery/session.md#L288) | formatted | [chat_history.jsonl:14](../../testdata/session-1.0.34/discovery/chat_history.jsonl#L14) | 15 | URL and complete public example.com response. |

### Headless

| # | Tool | Markdown line | Grade | Native file:line | Result line | Audit note |
|---|---|---:|---|---|---:|---|
| 1 | `search_tool` | [25](headless/session.md#L25) | formatted | [chat_history.jsonl:8](../../testdata/session-1.0.34/headless/chat_history.jsonl#L8) | 9 | Query/options and each discovered server, tool, description, score/schema and catalog status retained. Catalog lookup only; returned account tools were not called. |
| 2 | `search_tool` | [690](headless/session.md#L690) | formatted | [chat_history.jsonl:12](../../testdata/session-1.0.34/headless/chat_history.jsonl#L12) | 13 | Query/options and each discovered server, tool, description, score/schema and catalog status retained. Catalog lookup only; returned account tools were not called. |
| 3 | `search_tool` | [1345](headless/session.md#L1345) | formatted | [chat_history.jsonl:12](../../testdata/session-1.0.34/headless/chat_history.jsonl#L12) | 14 | Query/options and each discovered server, tool, description, score/schema and catalog status retained. Catalog lookup only; returned account tools were not called. |
| 4 | `ask_user_question` | [1461](headless/session.md#L1461) | formatted | [chat_history.jsonl:16](../../testdata/session-1.0.34/headless/chat_history.jsonl#L16) | 17 | Question and both choices plus native no-user fallback. |
| 5 | `use_tool` | [1483](headless/session.md#L1483) | formatted | [chat_history.jsonl:19](../../testdata/session-1.0.34/headless/chat_history.jsonl#L19) | 20 | Attempted MCP name/input and native invalid-name error preserved. Failure coverage only. |
| 6 | `enter_plan_mode` | [1499](headless/session.md#L1499) | formatted | [chat_history.jsonl:22](../../testdata/session-1.0.34/headless/chat_history.jsonl#L22) | 23 | Complete native instructions and plan file location. |
| 7 | `workflow` | [1531](headless/session.md#L1531) | formatted | [chat_history.jsonl:25](../../testdata/session-1.0.34/headless/chat_history.jsonl#L25) | 26 | Source name/type and cancellation rendered as an error, using failed update status despite no completion event. |

### Scheduler

| # | Tool | Markdown line | Grade | Native file:line | Result line | Audit note |
|---|---|---:|---|---|---:|---|
| 1 | `scheduler_list` | [25](scheduler/session.md#L25) | formatted | [chat_history.jsonl:6](../../testdata/session-1.0.34/scheduler/chat_history.jsonl#L6) | 7 | Successful empty-list response; no task created. |

## Coverage gaps

The default harness declares 27 tools. Across these four captures, 18 distinct declared tools were actually invoked, plus repeated invocations: 26 blocks total. This count includes pending, failed and cancelled paths; it does not imply 18 successful implementations.

| Unexercised declared tool | Reason |
|---|---|
| `scheduler_create`, `scheduler_delete` | User approved one bounded create/delete test. Grok returned the account usage-limit error immediately after `scheduler_list`, before either mutation. No schedule needed cleanup. |
| `image_gen`, `image_edit`, `image_to_video`, `reference_to_video` | Approved bounded media QA was stopped by the same usage limit before any media call. No media was generated. Generic input/result fallback is covered, but native media success is unverified. |
| `exit_plan_mode` | Restricted headless run entered plan mode but did not invoke exit; later account limit prevented another exercise. |
| `spawn_subagent` | Actual delegation was excluded by the review environment's no-subagent instruction. Existing parent/child fixtures and metadata-watch regressions cover parsing, not a fresh 1.0.34 spawn. |
| `send_feedback` | Excluded because it sends an external message outside the authorized QA scope. |

No rendering grade is assigned to an unexercised tool. `web_search` has only a pending native record, `use_tool` only failed names, and `workflow` only a cancellation. Successful search/MCP dispatch/workflow execution remain unverified. The broad headless run used only `GROK_HOME` isolation and still saw workstation MCP configuration; only the final scheduler and factory runs isolated `HOME` as well.
