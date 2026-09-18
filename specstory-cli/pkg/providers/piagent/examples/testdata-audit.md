# Pi testdata audit

Audited September 17, 2026 on macOS arm64, from revision `fc67087`. All nine files in `../testdata/` are native-format inputs consumed by automated tests. No generated review output was found there.

Compiled the actual provider suite with `go test -c ./pkg/providers/piagent`, then ran that binary with `-test.count=1 -test.timeout=90s` from a disposable copy of the package. The full suite passed initially. Each fixture was moved out of `testdata` individually, the full suite was rerun, and the fixture was restored before the next case. Every removal failed its consuming tests; the final restored suite passed. The working-tree fixtures were never changed.

| Fixture | Tests that fail when absent | Asserted behavior |
|---|---|---|
| [branching.jsonl](../testdata/branching.jsonl) | `TestFormatTree_BranchingLeafSelection` | Select the active leaf and exclude abandoned conversation branches. |
| [compaction.jsonl](../testdata/compaction.jsonl) | `TestFormatEntries_CompactionPreservesAllEntries` | Preserve pre-compaction conversation entries. |
| [compaction_missing.jsonl](../testdata/compaction_missing.jsonl) | `TestFormatEntries_CompactionDanglingKeptIdHarmless` | Preserve history when the compaction kept-entry ID is dangling. |
| [fields.jsonl](../testdata/fields.jsonl) | `TestFormatFields_SessionHeader`, `TestFormatFields_UserMessageStringContent`, `TestFormatFields_UserMessageImageSkipped`, `TestFormatFields_AssistantAllFields`, `TestFormatFields_ToolResultFields`, `TestFormatFields_NonConversationRolesSkipped`, `TestScan_PopulatesSlugAndName` | Map session headers, user text/images, assistant metadata, tool results, non-conversation roles, and scan metadata. |
| [full_format.jsonl](../testdata/full_format.jsonl) | `TestFormatEntries_ControlEntriesSkipped`, `TestFormatEntries_BranchSummaryEntry`, `TestFormatEntries_CompactionKeepsHistoryAndSummary` | Skip control and branch-summary records while retaining history and a compaction marker. |
| [multi_compaction.jsonl](../testdata/multi_compaction.jsonl) | `TestFormatTree_MultipleCompactionsAllRendered` | Render every compaction marker while preserving the conversation. |
| [real_world.jsonl](../testdata/real_world.jsonl) | `TestFormatEntries_RealWorldCompactionAndBashExecution`, `TestReconstructSession_UserShellExecution`, `TestScan_ShellOnlySession` | Preserve real compaction and user shell execution in parsing, reconstruction and scanning. |
| [sample.jsonl](../testdata/sample.jsonl) | `TestParseSession_MapsRealPiV3Session`, `TestParseSession_ProviderVersionPopulated` | Map a native v3 session into normalized exchanges, tools, usage and provider metadata. |
| [v1_legacy.jsonl](../testdata/v1_legacy.jsonl) | `TestFormatTree_Version1Legacy` | Parse and scan the legacy v1 session format. |

These are substantive parser, format, reconstruction and metadata assertions in [parser_test.go](../parser_test.go), [format_test.go](../format_test.go), [reconstruct_test.go](../reconstruct_test.go), and [robustness_test.go](../robustness_test.go). The removal experiment establishes file-level necessity for the current suite; it does not claim that every individual record or byte is essential.

Result: retain all nine fixtures in `testdata`. Human-review captures and QA results remain in `examples`, including this audit.
