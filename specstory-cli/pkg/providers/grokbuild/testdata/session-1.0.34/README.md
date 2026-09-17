# Grok 1.0.34 capture

Captured on macOS arm64 from `grok 1.0.34 (3736acbc8658) [stable]`, session `01a0b14e-1cd4-7051-aa41-f25319cef01b`, September 17, 2026. `tools.txt` comes from the harness's `available_commands.tools` declaration. This session used the default local configuration; the declaration included MCP discovery/dispatch, with testrail/xray available and testmo reporting an authentication requirement.

The transcript, updates, and summary preserve native shapes and order. Paths are anonymized, opaque reasoning and unrelated system/context text are replaced, and events are limited to tool-completion records used by the parser. No tool invocation or result is synthesized. The final backend web search was left `in_progress` by the native headless run; this is not success-path search coverage.

Additional captures in `discovery/`, `headless/`, and `scheduler/` use the same baseline and sanitization. The headless capture still used workstation MCP configuration despite its separate Grok store. The scheduler capture isolated both HOME and GROK_HOME; it completed an empty scheduler list, then the service's usage limit stopped the run before any schedule or media mutation. See [tool-audit.md](tool-audit.md) for every block and coverage limit.
