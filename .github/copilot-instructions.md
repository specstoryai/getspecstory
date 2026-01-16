Examine the file changes for the pull request.

Ignore any changes to:
- .specstory/history files
- .claude files
- ./SpecStoryCLI or ./specstory binary
- CLAUDE.md
- AGENT.md
- changelog.md

Please code review the pull request.

Review line by line, pay attention to:

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

In addition to the line by line review, suggest the 3 most important improvements for this code review.

In addition to the 3 most important improvements, let's also think about if any of these changes could benefit from new, updated or expanded unit tests. We focus our unit tests on complicated logic, and combinatorial scenarios, not on coverage or completeness for completeness sake. Better to not have a unit test than to have tests that are simplistic or tautological. We also don't test 3rd party library or language features, only our own code.
