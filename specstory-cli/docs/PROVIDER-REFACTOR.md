# Provider helper dedup refactor

**Date:** 2026-08-13 · **Status:** Phases 1 and 2 implemented (Option B); Phase 3 items 1 and 3 implemented; items 2, 4, 5 not started
**Scope:** ~200 lines of helpers copied across `deepseektui`, `droidcli`, and `antigravitycli` (`stringValue`, `renderJSONValue`, `normalizeToolName`, `todoSymbol`, `languageFromPath`, `formatDiffBlock`, `renderGenericJSON`, `canonicalizePath`, `dispatchSession`, `classifyCheckError`, `trackCheckSuccess`/`trackCheckFailure`), researched against all ten provider packages and the shared packages (`pkg/spi`, `pkg/utils`, `pkg/session`).

---

## 1. Key findings

The premise "copied verbatim" is only two-thirds true. Byte-for-byte diffs of the extracted function bodies show three tiers:

1. **Byte-identical (pure copy-paste):** `normalizeToolName`, `languageFromPath` (the trio's variant), `classifyCheckError`, `trackCheckSuccess`, `trackCheckFailure`. `canonicalizePath` and `dispatchSession` differ only by a variable name and the log prefix string.
2. **Semantically drifted copies:** `stringValue` (droidcli handles `fmt.Stringer`, `[]byte`, `float32`, `int32`, `uint`, `uint64`; the other two silently drop those types), `renderJSONValue` (droidcli handles `[]byte` but has an escaping bug — see §2), `todoSymbol` (deepseektui trims whitespace, droidcli doesn't; antigravitycli doesn't have it at all), `formatDiffBlock` (antigravitycli uses `TrimSuffix` where the others use `TrimRight`, so multiple trailing newlines render differently), and `renderGenericJSON` (antigravitycli is structurally different: it filters agy-injected `toolAction`/`toolSummary` keys and uses `json.MarshalIndent` instead of the hand-rolled sorted emitter).
3. **Same logic under different names in other providers:** `claudecode` and `cursorcli` share a duplicate `getLanguageFromExtension` pair with a much richer extension map (`pkg/providers/claudecode/markdown_tools.go:94`, `pkg/providers/cursorcli/markdown_tools.go:13`); `geminicli` has its own `languageFromPath` with different fallback behavior (`pkg/providers/geminicli/markdown_tools.go:338`) and a case-sensitive `todoStatusSymbol` (`pkg/providers/geminicli/markdown_tools.go:267`); `codexcli` has the most complete `classifyCheckError` (`pkg/providers/codexcli/codex_cli_exec.go:210`); six providers inline their own analytics tracking with divergent property sets.

**Bonus duplication found during research:** `antigravitycli` reimplements `spi.CodeFence` verbatim as a private `codeFence` (`pkg/providers/antigravitycli/markdown_tools.go:755` vs `pkg/spi/fence.go:23`). Same algorithm, same output — a pure delete.

### Full inventory of the twelve named helpers

|        Helper        |       deepseektui       |        droidcli         |     antigravitycli      |                        Divergence                         |
| -------------------- | ----------------------- | ----------------------- | ----------------------- | --------------------------------------------------------- |
| `stringValue`        | `markdown_tools.go:411` | `markdown_tools.go:612` | `markdown_tools.go:848` | droidcli is a strict superset                             |
| `renderJSONValue`    | `markdown_tools.go:395` | `markdown_tools.go:593` | —                       | droidcli adds `[]byte`, but has broken quote-escaping     |
| `normalizeToolName`  | `markdown_tools.go:435` | `markdown_tools.go:161` | `markdown_tools.go:872` | byte-identical                                            |
| `todoSymbol`         | `markdown_tools.go:443` | `markdown_tools.go:722` | —                       | deepseektui adds `TrimSpace`                              |
| `languageFromPath`   | `markdown_tools.go:351` | `markdown_tools.go:704` | `markdown_tools.go:771` | byte-identical (geminicli's differs)                      |
| `formatDiffBlock`    | `markdown_tools.go:326` | `markdown_tools.go:360` | `markdown_tools.go:730` | same output except agy's `TrimSuffix` + local `codeFence` |
| `renderGenericJSON`  | `markdown_tools.go:371` | `markdown_tools.go:569` | `markdown_tools.go:791` | agy filters injected keys, uses `MarshalIndent`           |
| `canonicalizePath`   | `path_utils.go:184`     | `provider.go:389`       | `path_utils.go:165`     | functionally identical                                    |
| `dispatchSession`    | `watcher.go:190`        | `watcher.go:232`        | `watcher.go:324`        | identical except log prefix                               |
| `classifyCheckError` | `provider.go:247`       | `provider.go:230`       | `provider.go:315`       | byte-identical (codexcli/geminicli differ)                |
| `trackCheckSuccess`  | `provider.go:289`       | `provider.go:296`       | `provider.go:360`       | byte-identical                                            |
| `trackCheckFailure`  | `provider.go:301`       | `provider.go:308`       | `provider.go:372`       | byte-identical                                            |

Call-site volume (excluding definitions and tests): `stringValue` 84, `renderGenericJSON` 43, `normalizeToolName` 6, `canonicalizePath` 6, `trackCheckFailure` 6, the rest 2–5 each.

---

## 2. Latent bugs surfaced by the research

These exist today and are worth fixing as part of (or independent of) the dedup:

1. **droidcli `renderJSONValue` escaping** (`pkg/providers/droidcli/markdown_tools.go:593`): the marshal-error fallback uses `fmt.Sprintf("\"%s\"", …)` instead of `%q`, so a value containing quotes or backslashes produces malformed JSON in the rendered fence. deepseektui's copy already uses `%q` correctly.
2. **geminicli hardcoded fences** (`pkg/providers/geminicli/markdown_tools.go:225` and `:278`): emits literal ` ```diff ` / ` ```json ` instead of `spi.CodeFence`, so content containing a triple-backtick run breaks out of the fence and swallows the rest of the document. Every other provider uses `spi.CodeFence` for exactly this reason.
3. **geminicli `classifyCheckError` unreachable arm** (`pkg/providers/geminicli/provider.go:204`): its `case errors.As(err, &pathErr):` matches *any* `os.PathError`, so a permission error wrapped in a `PathError` returns `version_failed` and the later `os.ErrPermission` arm can never fire for that shape.
4. **Trio `classifyCheckError` gap**: a `PathError` wrapping `os.ErrNotExist` falls through to `version_failed` instead of `not_found`. codexcli's version handles this correctly.

---

## 3. Where a shared home can live (import-graph constraints)

The dependency direction is strict and verified:

- `pkg/spi` imports only `pkg/spi/schema`. Every provider imports `pkg/spi` (+ `pkg/spi/schema`, `pkg/analytics`, and usually `pkg/log`).
- `pkg/spi/factory/registry.go:13` imports all providers, and `pkg/utils/watch_agents.go:12` imports `pkg/spi/factory`. **Any provider importing `pkg/utils` creates an import cycle.** `pkg/session` imports `pkg/utils` (plus `pkg/cloud`, `pkg/telemetry`), so it is transitively off-limits too.
- `pkg/analytics` imports no internal packages — a leaf, already imported by every provider.

So the candidates are `pkg/spi`, `pkg/analytics` (for the tracking helpers only), or a new leaf package.

### Option A — everything into `pkg/spi`

All twelve helpers become exported `spi` functions; `trackCheck*` pulls `pkg/analytics` into `spi`'s import set.

- **Pros:** one shared home; providers already import it; `spi` already holds shared rendering helpers (`CodeFence`, `CapRunes`, `reconstruct.go`) and path helpers (`GetCanonicalPath`), so this follows precedent.
- **Cons:** couples the SPI definition package to PostHog analytics. `spi` today depends on nothing but its own schema — that purity is worth something, and `PROVIDER-SPI.md` documents `spi` as the pure interface layer.

### Option B — `pkg/spi` for pure helpers, `pkg/analytics` for the tracking pair

Ten helpers go into `pkg/spi` (all are stdlib-only — even `dispatchSession` uses `log/slog`, and `classifyCheckError` uses `errors`/`os`/`exec`). `trackCheckSuccess`/`trackCheckFailure` become `analytics.TrackCheckSuccess`/`analytics.TrackCheckFailure` — they are thin wrappers around `analytics.TrackEvent`, so the analytics package is their natural home and no new import edges appear anywhere.

- **Pros:** zero new dependency edges in the whole graph; each helper lands in the package that conceptually owns it; `spi` stays pure.
- **Cons:** the check-related helpers split across two packages (`spi.ClassifyCheckError` + `analytics.TrackCheckSuccess`), so a provider's `Check` implementation reads from two homes.

### Option C — new leaf package (e.g. `pkg/spi/providerutil`)

All twelve helpers into one new sub-package importing `spi` + `analytics`.

- **Pros:** `spi` stays pristine; single home.
- **Cons:** a third shared location for provider code when `spi` already plays that role; every provider adds an import; the markdown helpers would sit next to `spi.CodeFence`'s callers but in a different package.

**Recommendation: Option B.** It is the only option that adds no dependency edges at all, and the split it creates mirrors the split that already exists at every call site (rendering/path/check logic vs. analytics emission).

### Proposed new files (requires approval per project convention)

- `pkg/spi/tool_markdown.go` + `pkg/spi/tool_markdown_test.go` — the seven markdown-rendering helpers
- `pkg/spi/check.go` + `pkg/spi/check_test.go` — `ClassifyCheckError` (next to the `CheckResult` type already in `spi/provider.go`)
- `pkg/analytics/check_events.go` — `TrackCheckSuccess`, `TrackCheckFailure`

`CanonicalizePathOrClean` goes into the existing `pkg/spi/path_utils.go` next to `GetCanonicalPath`; `DispatchSession` into the existing `pkg/spi/global.go` next to `safeScan` (the existing panic-isolation precedent).

---

## 4. The plan, phased by risk

### Phase 1 — zero behavior change (mechanical moves)

Every item here consolidates byte-identical (or label-only-different) code. Rendered markdown and emitted analytics are unchanged.

1. Delete `antigravitycli`'s private `codeFence` (`markdown_tools.go:755`); switch its callers to `spi.CodeFence`. Output is provably identical.
2. `spi.NormalizeToolName` ← the three identical copies.
3. `spi.LanguageFromPath` ← the three identical copies (geminicli's variant **stays put** in this phase; see Phase 3).
4. `spi.CanonicalizePathOrClean` ← the three functionally identical wrappers (trim → `GetCanonicalPath` → `Abs`+`Clean` fallback).
5. `spi.DispatchSession(label string, cb func(*spi.AgentChatSession), session *spi.AgentChatSession)` ← the three copies; the provider passes its log label (`"droidcli"`, `"deepseek"`, `"antigravity"`), preserving today's log lines exactly.
6. `spi.ClassifyCheckError` ← the three identical copies (codexcli's richer variant stays put; see Phase 3).
7. `analytics.TrackCheckSuccess` / `analytics.TrackCheckFailure` ← the three identical pairs.
8. Move the existing provider-local tests to the new shared homes: `TestClassifyCheckError` (`pkg/providers/deepseektui/provider_test.go:12`, `pkg/providers/antigravitycli/provider_test.go:36`), `TestCanonicalizePath` (`pkg/providers/deepseektui/path_utils_test.go:176`).

### Phase 2 — merge the drifted trio copies (tiny, well-understood output deltas)

Each item resolves a specific divergence; the resolution is stated so the delta is a decision, not an accident.

1. `spi.StringValue` ← adopt droidcli's superset type switch. For deepseektui/antigravitycli this is strictly widening: inputs that previously rendered as `""` now render their value.
2. `spi.RenderJSONValue` ← deepseektui's `%q` fallback (fixes the droidcli escaping bug, §2.1) **plus** droidcli's `[]byte` case. Superset of both, malformed-JSON bug removed.
3. `spi.TodoSymbol` ← deepseektui's version (with `TrimSpace`). Strictly widening for droidcli.
4. `spi.FormatDiffBlock` ← droidcli's guard style with `TrimRight`. antigravitycli's output changes only when old/new text ends in multiple newlines (trailing blank lines inside the diff fence disappear — arguably a fix).
5. `spi.RenderGenericJSON(args map[string]any, dropKeys ...string)` ← this is the one real design decision in Phase 2:
   - **Option 5a — keep the hand-rolled sorted emitter** (deepseektui/droidcli style) and have antigravitycli pass `dropKeys: "toolAction", "toolSummary"`. deepseektui/droidcli output is byte-stable; antigravitycli's nested values change from `MarshalIndent`'s multi-line form to the emitter's compact single-line form.
   - **Option 5b — standardize on `MarshalIndent`** (antigravitycli style) with the same `dropKeys` parameter. Simpler code (~15 lines vs ~45, and `renderJSONValue` becomes internal-only or is deleted), deterministic key order for free, and antigravitycli output is byte-stable; deepseektui/droidcli nested values gain indentation.
   - **Recommendation: 5b.** It deletes more code, `MarshalIndent`'s output is the more readable form for nested arguments, and the only cost is a cosmetic change in regenerated deepseektui/droidcli markdown. The antigravitycli key-filtering test (`pkg/providers/antigravitycli/markdown_tools_test.go:145`) moves to `spi` as the regression guard for `dropKeys`.
6. Add tests for the merged behavior in `pkg/spi/tool_markdown_test.go`: `StringValue` type coverage, `RenderJSONValue` escaping (the §2.1 case), `TodoSymbol` trimming, `FormatDiffBlock` trailing-newline handling, `RenderGenericJSON` key sorting + `dropKeys`. Today `stringValue`, `renderJSONValue`, `normalizeToolName`, and `todoSymbol` are untested everywhere.

### Phase 3 — cross-provider convergence (optional; each item independent and decision-gated)

The research found the same logic under different names in the other seven providers, but folding them in changes rendered markdown or emitted telemetry beyond the trio. Each item can be taken or skipped independently; none blocks Phases 1–2.

1. **Language maps.** Merge `claudecode`/`cursorcli` `getLanguageFromExtension` (rich map, but missing `ToLower` — `FOO.PY` falls through today) and geminicli's `languageFromPath` (returns `"text"` instead of `""`, no `yml`/`md` aliases) into `spi.LanguageFromPath` as a superset map + `ToLower`. Requires one decision: empty-extension fallback `""` (trio) vs `"text"` (gemini). Changes fence info strings in regenerated markdown for all five providers.
2. **Todo checkboxes.** droidcli/deepseektui/geminicli render `x`; claudecode/cursorcli/codexcli render `X`; geminicli's matcher is case-sensitive with no aliases. Unify on one symbol set and matcher via `spi.TodoSymbol`.
3. **Check-error taxonomy.** codexcli's `classifyCheckError` is the most complete (`nil→""`, `PathError`+`ErrNotExist→not_found`, `no_output`, default `unknown`); claudecode/cursorcli inline a variant defaulting to `""`; geminicli has the unreachable-arm bug (§2.3); the trio defaults to `version_failed`. Unifying changes the `error_type` values that reach PostHog, so it needs a deliberate choice of the canonical bucket set (recommend codexcli's, minus the codex-specific `errNoVersionOutput` sentinel, which stays a codex concern via an error-wrapping seam or a pre-check).
4. **Analytics property schema.** Six providers inline `TrackEvent` calls with drifting property sets (codexcli emits `stderr` unconditionally and folds `resolved_path` into `command_path`; claudecode/cursorcli/geminicli omit `resolved_path`/`version_flag`/`stderr` and use `output` instead of `error_message`; cursoride/copilotide use a `location` key nobody else has). Migrating everyone to `analytics.TrackCheckSuccess`/`TrackCheckFailure` normalizes the schema — the highest-value consolidation found, but it **changes emitted telemetry**, so PostHog dashboards/queries need a review and `docs/POSTHOG.md` an update. cursoride/copilotide have no `--version` exec at all, so they keep their DB-oriented failure types and only adopt the shared property names.
5. **Dispatch variants.** cursorcli/cursoride add `sync.WaitGroup` accounting to their inline dispatch (their watchers wait for in-flight callbacks on shutdown); copilotide's `deliverSession` is synchronous. Folding these into `spi.DispatchSession` requires an optional `*sync.WaitGroup` parameter. Low value relative to the coupling it adds — recommend leaving these three as-is.

**Recommendation on Phase 3 overall:** take items 1 and 3 (they fix real bugs along the way), treat item 4 as its own follow-up once someone can vet the PostHog side, skip items 2 and 5 unless output consistency across providers becomes a goal.

### Independent bug fixes (any phase, or standalone)

- geminicli hardcoded fences → `spi.CodeFence` (§2.2). Pure robustness fix, output identical except when content contains backtick runs.
- geminicli `classifyCheckError` unreachable arm (§2.3) — fixed for free if Phase 3 item 3 is taken; otherwise a two-line local fix.

---

## 5. What deliberately stays provider-local

- **Tool-type classifiers** (`toolType`, `classifyToolType`, `classifyGeminiToolType`, `classifyCursorToolType`, `MapToolType`): per-provider lookup tables keyed to each agent's actual tool names. Same *shape*, genuinely different *content* — not duplication. (Worth a separate look: droidcli's `toolType` returns bare strings where everyone else returns `schema.ToolType*` constants.)
- **Diff assembly around `FormatDiffBlock`**: cursorcli adds `---`/`+++` headers, codexcli renders pre-formed patches with headings, droidcli has a second `DiffLines`-based builder that handles context lines. These consume a shared fence helper but are legitimately different renderers.
- **Path normalizers with different semantics**: `vscode.NormalizePathForComparison` (WSL UNC handling, deliberately skips canonicalization for Unix-style paths on Windows), `codexcli.normalizeCodexPath` (foreign-rooted-path handling, no case folding), `utils.canonicalizeWorkspacePath` (pure lowercase for hashing). Merging any of these into `CanonicalizePathOrClean` would corrupt the cases they exist for.
- **copilotide/cursoride check flows**: no `--version` exec; their `database_not_found`-style error types are correct as-is.

---

## 6. Verification

After each phase:

```zsh
golangci-lint run
go test -v ./...
```

Plus, for Phases 2–3 (output-affecting): regenerate markdown for a sample session per touched provider with `./specstory sync --debug` against fixture data, and diff against pre-refactor output to confirm the only deltas are the ones each item's decision explicitly accepted.

---

## 7. Decisions taken

1. **Shared-home layout: Option B.** Pure helpers in `pkg/spi`, the tracking pair in `pkg/analytics`. No new dependency edges anywhere in the graph.
2. **`RenderGenericJSON` core: 5b (`MarshalIndent`).** `renderJSONValue` had no call sites outside `renderGenericJSON`, so choosing 5b deleted it outright rather than promoting it to `spi.RenderJSONValue` — the droidcli escaping bug (§2.1) is gone with the code that held it.

3. **Phase 3 scope: items 1 and 3.** Items 2 (todo checkboxes), 4 (analytics property schema) and 5 (dispatch variants) were not taken.
4. **Extensionless-file fence tag: `""`.** Already the behavior of 4 of the 5 providers, and an untagged fence leaves the renderer free rather than asserting plaintext. Only geminicli's output changes.
5. **Residual `error_type`: `"unknown"`.** Every row on `ext_check_install_failed` is by definition a failed version check, so `version_failed` was tautological there and carried no information; `unknown` instead marks the rows where `error_message` is the only explanation. It also matches codexcli's retry semantics (unclassified → worth trying another flag).

Still open: Phase 3 items 2, 4 and 5 — for item 4, PostHog sign-off plus a `docs/POSTHOG.md` update — and the geminicli hardcoded-fence bug in §2.2. The §2.3 unreachable-arm bug was fixed by item 3.

---

## 8. What actually landed (Phases 1 and 2)

Net **−607 lines** across 19 files (282 added, 889 deleted), `golangci-lint run` clean, `go test ./...` green.

### New shared code

|                           File                           |                                                       Contents                                                       |
| -------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `pkg/spi/tool_markdown.go`                               | `StringValue`, `NormalizeToolName`, `TodoSymbol`, `LanguageFromPath`, `FormatDiffBlock`, `RenderGenericJSON`         |
| `pkg/spi/check.go`                                       | `ClassifyCheckError` + the `CheckErrorNotFound` / `CheckErrorPermissionDenied` / `CheckErrorVersionFailed` constants |
| `pkg/analytics/check_events.go`                          | `CheckAttempt`, `TrackCheckSuccess`, `TrackCheckFailure`                                                             |
| `pkg/spi/path_utils.go`                                  | `CanonicalizePathOrClean` (added next to `GetCanonicalPath`)                                                         |
| `pkg/spi/global.go`                                      | `DispatchSession` (added next to `safeScan`)                                                                         |
| `pkg/spi/tool_markdown_test.go`, `pkg/spi/check_test.go` | new tests, including first-ever coverage of `StringValue`, `NormalizeToolName`, `TodoSymbol`, and `FormatDiffBlock`  |

### Two deviations from the plan as written

1. **The analytics signatures became a struct.** The originals took 6 and 8 positional arguments, the failure one with five consecutive strings — transposable in silence. `CheckAttempt` carries the invocation fields (and is filled in progressively, since `ResolvedPath` is only known once the binary is located) while the outcome stays a parameter: `TrackCheckSuccess(attempt, version)` and `TrackCheckFailure(attempt, errorType, message, stderr)`. Emitted properties are byte-identical to before, including `stderr` appearing only when non-empty. This matters for Phase 3 item 4, which would extend these calls to six more providers.
2. **Phase 2 item 4 was a no-op, not a delta.** The plan claimed `TrimRight` vs antigravitycli's `TrimSuffix` diverged on multiple trailing newlines. It cannot: `strings.Split` never yields a line containing `\n`, so the builder always ends with exactly one, and both trims produce the same string. `FormatDiffBlock` was a zero-behavior-change consolidation.

### Verified output changes

Only one, and it is the accepted cost of 5b: in deepseektui and droidcli, **nested** objects and arrays in the generic-JSON fallback now render indented across multiple lines instead of compact on one. Flat scalar arguments — the overwhelming majority — are byte-identical, as are all of antigravitycli's, which already used `MarshalIndent`. Everything else in both phases was verified byte-identical by diffing the extracted function bodies before the move.

### Test relocations

`TestClassifyCheckError` (both the deepseektui table and the antigravitycli assertions, merged into one table that now also covers `exec.ErrNotFound`) and `TestCanonicalizePath` moved to `pkg/spi`. antigravitycli's `TestCodeFence_OutrunsEmbeddedBackticks` was deleted as redundant — `pkg/spi/fence_test.go` already covers it. Its `TestRenderGenericJSON_DropsMetaKeys` was retargeted rather than moved: as `TestGenericFallback_DropsMetaKeys` it now drives `formatToolInput`, so it guards the provider's `metaArgKeys` wiring instead of re-testing shared code that `pkg/spi` already tests.

---

## 9. What actually landed (Phase 3, items 1 and 3)

Cumulative with Phases 1–2: **−953 lines** (307 added, 1260 deleted), `golangci-lint run` clean, `go test ./...` green. Every provider package now shares one language map and one check-error classifier — ten at the time of this phase, plus `musecode` (§10).

### Item 1 — language maps

`spi.LanguageFromPath` became the superset: cursorcli's rich table (`js`/`jsx`→javascript, `ts`/`tsx`→typescript, `py`→python, `rb`→ruby, `rs`→rust, `cs`→csharp, `h`/`c`→c, `hpp`/`cpp`/`cc`→cpp) plus `yml`→yaml and `md`→markdown, all behind `strings.ToLower`, with `""` for a path that has no usable extension. `claudecode.getLanguageFromExtension`, `cursorcli.getLanguageFromExtension` and `geminicli.languageFromPath` are gone.

Fence info strings that change in regenerated markdown:

|               Provider                |                                                                       What changes                                                                        |
| ------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| deepseektui, droidcli, antigravitycli | gain the language aliases: `.js` was ` ```js `, now ` ```javascript `; same for ts/py/rb/rs/cs/jsx/tsx/h/c/hpp/cpp/cc                                     |
| claudecode                            | gains `ToLower` and the md/jsx/tsx/c/cpp/cs/rs aliases; `.md` was ` ```md `, now ` ```markdown `; `.PY` was the unusable tag ` ```PY `, now ` ```python ` |
| cursorcli                             | gains `ToLower` only — uppercase extensions stop leaking through as unusable tags; otherwise unchanged                                                    |
| geminicli                             | gains every alias, and an extensionless file goes from ` ```text ` to an untagged fence                                                                   |

The `ToLower` additions are strictly corrective: a shouted extension previously produced a tag no highlighter recognizes.

### Item 3 — check-error taxonomy

`spi.ClassifyCheckError` adopted codexcli's completeness (`nil` → `""`, `PathError` wrapping `ErrNotExist` → `not_found`) with `unknown` as the residual, and `spi.CheckErrorNoOutput` joined the constants for the value codexcli and cursorcli report directly. `codexcli.classifyCheckError` is now a two-line wrapper resolving its own `errNoVersionOutput` sentinel before delegating; `geminicli.classifyGeminiCheckError` and the inline classification blocks in claudecode and cursorcli are gone.

Two bugs fixed on the way:

- **The §2.3 unreachable arm, in three providers not one.** geminicli, claudecode and cursorcli all wrote `case errors.As(err, &pathErr):` as a bare switch arm, so a `PathError` that was neither missing-file nor permission consumed the arm and the following `errors.Is(err, os.ErrPermission)` case could never run. The shared version flattens this: each `PathError` arm re-tests `errors.As` **and** the specific cause, so a non-matching `PathError` falls through to the permission check.
- **The §2.4 gap.** The deepseektui/droidcli/antigravitycli trio and geminicli now classify a `PathError` wrapping `ErrNotExist` as `not_found` instead of burying it in the residual bucket.

Telemetry changes: deepseektui, droidcli, antigravitycli and geminicli now emit `error_type: "unknown"` where they emitted `"version_failed"`. No user-facing remediation text changes — every provider's message builder routes both values through the same `default:` arm.

### The one thing that made item 3 risky

`codexcli` was using the classifier's return value as **control flow**, not just telemetry. `codex_cli_exec.go` tries `--version` then `-V`, and `if classifyCheckError(err) != "unknown" || idx == len(flags)-1` decides whether to stop or try the next flag — an unclassified failure being the only kind another flag might fix. Renaming the residual bucket would have silently collapsed that retry to a single attempt. The literal is now `spi.CheckErrorUnknown`, so the coupling is explicit and a future rename moves both sides together.

---

## 10. Follow-on: bringing `musecode` in line

`musecode` was written in parallel with this refactor and merged after it, so it reintroduced five of the consolidated helpers. It was already clean on the worst issue — it used `spi.CodeFence` at all seven fence sites, so it never had the §2.2 hardcoded-fence bug — and its exact-match tool dispatch needs no name normalization.

Applied, using the same decisions recorded in §7:

| Removed from `musecode` | Replaced with | Effect |
|---|---|---|
| `formatGenericBodyFromInput` | `spi.RenderGenericJSON` | none — was behaviorally identical |
| `canonicalPath` | `spi.CanonicalizePathOrClean` | trims before canonicalizing and falls back through `filepath.Abs`, so a relative workspace root now compares equal to its absolute form |
| `classifyMuseCheckError` | `spi.ClassifyCheckError` | `error_type` values change (below) |
| `languageFromPath` | `spi.LanguageFromPath` | fence tags change (below) |
| three inline `analytics.TrackEvent` calls | `analytics.CheckAttempt` + `TrackCheckSuccess`/`TrackCheckFailure` | adds `resolved_path`, `version_flag`, and `stderr` (which muse captured but never reported) |

`classifyMuseCheckError` was the pre-fix shape, carrying both §2.3 and §2.4. Verified deltas against the shared classifier: a `PathError` wrapping `ErrNotExist` was `version_failed`, now `not_found` (the practical bug — a binary that vanishes between `LookPath` and `Run`); a plain error and an unrelated `PathError` were `version_failed`, now `unknown`; `nil` was `version_failed`, now `""`. The §2.3 arm needs a contrived multi-`%w` chain to trigger, so it was latent rather than user-visible. `buildMuseCheckErrorMessage` has a `default:` arm, so no remediation text changed.

`languageFromPath` was byte-identical to the geminicli variant deleted in item 1, so muse picks up the same changes: `Makefile` and `""` go from ` ```text ` to an untagged fence, and the aliases apply (`.md` → markdown, `.py` → python, `.yml` → yaml). `TestLanguageFromPath` moved to `pkg/spi`; one fence assertion in `markdown_tools_test.go` moved from ` ````md ` to ` ````markdown `.

Two fixes outside the dedup:

- **`triggerCallback` had no panic recovery** (`watcher.go`). A panic in the consumer's callback would unwind the fsnotify event goroutine and take the process down over one bad session. Delivery stays synchronous — ordering matters and `spi.DispatchSession` would have made it async — so the fix is a local `defer recover()`, locked in by `TestTriggerCallback_ContainsConsumerPanic`. **geminicli still has this gap**, with the same singleton-watcher shape; it is not muse-specific and remains open.
- **An empty `--version` reported success with a blank version.** Now substitutes `"unknown"`, matching deepseektui and antigravitycli. (cursorcli and codexcli instead treat empty output as a `no_output` failure; muse follows the former.)

Left alone deliberately: `inputAsString` (single-key, with nil handling for muse's JSON nulls and a `json.Marshal` fallback where `spi.StringValue` returns `""`), `diffFromFindReplace` (same logic as `spi.FormatDiffBlock` but unfenced, because muse truncates before fencing — deduping needs `FormatDiffBlock` split into a `DiffLines` core), `truncate` (duplicates `spi.CapRunes` with a different visible marker), and `todoStatusSymbol` (symbols already match `spi.TodoSymbol`; switching is free but belongs with the skipped item 2).
