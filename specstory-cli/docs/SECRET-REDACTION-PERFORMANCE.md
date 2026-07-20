# Secret Redaction Performance

## Background

The CLI redacts secrets from generated markdown before writing it to disk and syncing it to cloud (`pkg/session/redact.go`, betterleaks default ruleset). Two cloud-synced payloads also need redacted:

- **Raw data** (`session.RawData`): the provider's native session data (JSONL for Claude Code and Codex CLI, generated JSON blobs for the Cursor providers). No structured consumer today; used only for token-usage extraction, which treats it as plain text. Persisted in cloud storage as "insurance."
- **SessionData JSON** (`session.SessionDataJSON()`): the normalized session structure. Consumed by session resumption (low volume). Regenerated wholesale on every save.

Extending redaction to these payloads is correct and safe (see Correctness below). The performance question is how to afford scanning them on every save. The answer: use the betterleaks library the way its own CLI uses it — a faster regex engine plus chunked fragments — after which straightforward per-save scans of all three payloads are cheap.

## Measurements

All numbers from real sessions of this repository on an Apple Silicon dev machine. Detector construction is a one-time ~5ms cost.

Our current usage (`detect.DetectString` on the whole payload, default stdlib regex engine) versus the corrected usage (RE2 engine, ~100KB fragments):

|                 Payload                  | stdlib, whole content | RE2, whole content | RE2 + 100KB chunks |
| ---------------------------------------- | --------------------- | ------------------ | ------------------ |
| Raw JSONL, worst session (10.1MB)        | 1m 48s                | 2.4s               | **815ms**          |
| Raw JSONL (6.6MB)                        | 1m 10s                | 1.0s               | —                  |
| Raw JSONL (8.3MB)                        | 7.2s                  | 0.8s               | —                  |
| SessionData JSON (2.8MB, pretty-printed) | 4.4s                  | 180ms              | **53ms**           |
| Generated markdown, large (1.3MB)        | 1.3s                  | 58ms               | **34ms**           |

Findings were identical across engines in every test. Zero true findings across ~30MB of real transcripts (the ruleset's false-positive discipline holds on coding-agent content).

For reference, the Betterleaks CLI itself (`betterleaks dir`) scans 67MB of history markdown in ~500ms — the same two factors plus parallel workers across files.

## Why the default usage is slow

The cost model for a Betterleaks scan:

```
scan time ≈ fragment bytes × (rules whose keywords appear anywhere in the fragment) × per-rule engine cost
```

Three findings, verified by per-rule timing that reconciles to the measured totals within ~20%:

- **Engine.** Library consumers get Go's stdlib `regexp` — an NFA simulation with no DFA, running at ~0.1–1 MB/s per active rule on dense content (a CPU profile shows ~98% of scan time in `regexp.(*machine)`). The betterleaks CLI instead calls `regexp.SetEngine(re2.RE2{})` at startup (`cmd/root.go`): the C++ RE2 engine compiled to WebAssembly, executed via wazero — pure Go, no cgo, compatible with our `CGO_ENABLED=0` release builds. This alone is worth 20–70× on our content.
- **Fragment size.** The keyword prefilter (aho-corasick) skips a rule only when its keywords are absent from the *entire fragment*. `DetectString` makes the whole payload one fragment, so one keyword hit anywhere subjects all of it to that rule's regex. Coding transcripts are saturated with trigger words ("key", "token", "api", "secret" — ~48% of lines in a sampled session; 67 of 324 rules triggered on the worst file), and transcripts that *discuss* secret detection also activate vendor rules by mentioning vendor names. The CLI's file source instead scans ~100KB chunks (`sources/file.go`, `defaultBufferSize`), confining each triggered rule to the chunk that triggered it.
- **Parallelism.** The CLI scans files/chunks concurrently. Not needed for our per-save budgets, but available as a lever.

## Correctness findings

Verified empirically by running the detector and the `applyRedactions` replacement loop over JSON-escaped fixtures (under both engines):

- Token-class secrets match character classes that exclude `"` and `\`, and the `[REDACTED:rule-id]` placeholder is JSON-string-safe, so replacement inside a JSON string value cannot break the document.
- The multi-line `private-key` rule matches PEM blocks even in JSON-escaped form (its `[\s\S-]` body matches through literal `\n` sequences), and replacement stays inside the string value.
- Worst case: a secret spanning two JSON string fields. The match swallows the structural `","field":"` between them and the replacement *merges the two fields into one string*. The result is still valid JSON — the failure mode is silent field-merging in the region that contained a real secret, not a parse error. This is fail-closed (losing data around a leaked secret beats leaking it) and is accepted; no post-redaction JSON validation is performed.
- Each payload is scanned in its own textual form (decoded markdown, escaped raw/SessionData). A secret whose match depends on surrounding context could in principle be detected in one form and missed in another, leaving payloads inconsistently redacted. Accepted: token-class secrets are escape-invariant, and the PEM class detects in both forms as shown above.
- go-re2 stops matching at invalid UTF-8 instead of substituting replacement characters (stdlib behavior). Session payloads are JSON — valid UTF-8 by construction — so this is a low-risk difference, accepted.

## Design

### Engine selection

At process startup, before any detection, set the Betterleaks regex engine to RE2:

- Import `github.com/betterleaks/betterleaks/regexp` and `github.com/betterleaks/betterleaks/regexp/re2`; call `regexp.SetEngine(re2.RE2{})` once. Rule compilation is lazy (deferred to first match), so setting the engine before the first `RedactContent` call is sufficient; doing it at startup removes any ordering concern.
- This adds `github.com/betterleaks/go-re2` (plus wazero) as dependencies. go-re2 embeds a compressed RE2 WASM module; measured binary-size delta: +4.5M unstripped, +3.1M on the stripped (`-s -w`) release build.
- go-re2 is slower than stdlib for tiny inputs (per-call WASM overhead). Our fragments are ~100KB, comfortably past that threshold.

### Chunked scanning

`RedactContent` scans content in ~100KB fragments instead of one `DetectString` call over the whole payload, mirroring the betterleaks file source:

- Split at newline boundaries near the 100KB target (fall back to a hard split for single lines exceeding the chunk size).
- Re-scan a small overlap window across each boundary so a secret spanning the split (e.g. a decoded multi-line PEM block) is still caught. Applying the same finding twice is naturally idempotent — `applyRedactions` skips secrets no longer present.
- Findings from all chunks accumulate and are applied to the full payload with the existing `applyRedactions` loop (longest-first, deterministic tie-break).

### Per-save scans of all three payloads

Every payload is redacted independently, wholesale, on each save — no cross-payload state, no cache, no incremental bookkeeping:

- **Markdown**: existing scan, now chunked + RE2 (drops from ~1s to ~34ms on the largest observed file).
- **Raw data**: scanned and redacted before `SyncSessionToCloud`.
- **SessionData JSON**: scanned and redacted before `SyncSessionToCloud`.

The shared `[REDACTED:rule-id]` placeholder keeps payloads consistent where the same secret is detected in each.

### Fail-closed principles

- Detector initialization failure: warn once, write content unredacted (history must not be lost) — existing behavior.
- Redaction structural damage (field-merge tail case): ship the redacted payload anyway; a mangled region around a real secret beats a leaked secret.

## Performance budgets

|                                 Path                                 |        Measured basis         |        Budget        |
| -------------------------------------------------------------------- | ----------------------------- | -------------------- |
| Typical save event, all three payloads                               | sub-MB payloads at 12–50 MB/s | tens of milliseconds |
| Worst observed session (10MB raw + 2.8MB SessionData + 1MB markdown) | 815ms + 53ms + 34ms           | ~1s                  |
| Detector construction                                                | ~5ms                          | once per process     |

If the worst-case tail ever becomes a problem (sessions far larger than observed), parallel chunk scanning is the next lever; nothing in this design precludes it.

## Reproducing the measurements

Build a small program that constructs the detector with `detect.NewDetectorDefaultConfig()` and times `DetectString` over candidate files — whole-content vs ~100KB newline-aligned chunks, with and without `regexp.SetEngine(re2.RE2{})` — against the largest local session data:

```zsh
ls -lS ~/.claude/projects/<project-dir>/*.jsonl | head
```

Per-rule attribution: iterate `config.Default()`'s rules, skip rules whose keywords are absent from the content, and time each rule's `FindAllStringIndex` over the content individually.
