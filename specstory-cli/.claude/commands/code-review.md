---
allowed-tools: Bash(git status:*), Bash(git diff:*)
description: Code review the current proposed code change
---

## Context

- Current git status: !`git status`
- Current git diff: !`git diff`
- Current untracked files: !`git ls-files --others --exclude-standard`

## Your task

Examine the current proposed code changes and new files using git commands.

Ignore any changes to:
- .specstory/history files
- .claude/ files
- .cursor/ files
- ./specstory binary
- CLAUDE.md
- AGENT.md
- changelog.md

Please code review this change.

Review line by line and explain to me the purpose of each change. Use file names and line numbers to reference the changes, but also sequentially number every observation so we can easily reference them.

In the line by line review, pay attention to:

- clarity of variable names
- Go lang idiomatic code
- code clarity and simplicity, readable over clever
- "why" comments, not "how" comments
- missing comments
- missing log output
- missing analytics tracking
- single function exit point where possible (immediate guard clauses are OK)
- verify the code has to exist, is actually needed, and is in use
- verify the code is DRY, and doesn't replicate the same or similar code
- for test cases, ensure a data-driven approach is being used rather than lots of repetitive test code

Format your line by line review like this example:

```
  cmd/remote.go

  Line 12 (removed): Removed unused import of pkg/service package
  - (1) ✅ Good - Cleaning up unused imports is proper Go hygiene

  Lines 157-162 (modified): Changed from loading config directly to using RPC client
  // Old: config, err := service.LoadConfig(dir)
  // New: rpcClient, err := client.NewRPCClient(dir)
  - (2) ✅ Good architectural decision - Now operates via daemon RPC instead of direct file access
  - (3) ✅ Proper defer cleanup pattern for RPC client
  - (4) ✅ Variable name rpcClient is clear and follows conventions

  Lines 164-189 (new): Added remote status check before disabling
  - (5) ✅ Good UX - Detects current connection state to provide better feedback
  - (6) ✅ Type assertions use the two-value form (ok pattern) for safety
  - (7) ⚠️ Nested if statements become deeply indented (4 levels) - could be refactored for readability
  - (8) ✅ Variable names wasConnected, oldURL are descriptive
  - (9) ❓ Missing error handling - if remote.status call fails, we continue anyway. Should we?

  ---
  pkg/remote/sync.go
  Lines 260-265 (modified): Success messages now come after RPC call
  - (10) ✅ Comment "config is now saved" is helpful context
  - (11) ⚠️ Misleading comment placement - comment says "config is now saved" but we can't be certain from this code's perspective (that happens in the daemon)
```

In addition to the line by line review, suggest the 2-5 most important improvements for this code review.

In addition to the most important improvements, let's also think about if any of these changes could benefit from new, updated or expanded unit tests. We focus our unit tests on complicated logic, and combinatorial scenarios, not on coverage or completeness for completeness sake. Better to not have a unit test than to have tests that are simplistic or tautological. We also don't test 3rd party library or language features, only our own code.

For any new test cases that were added, confirm the tests are:

- Not tautological - They would catch real bugs
- Well-designed - Each assertion accurately verifies specific logic and complexity in the code being tested
- Reliable - They won't be flaky, don't rely on external state, and don't catch false positives.
- Not repetitive - They test different scenarios, and use data-driven testing rather than repetitive test code.