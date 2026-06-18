<!-- PASS-THROUGH FORGE PLAN: present this document VERBATIM as the plan body (SKILL.md LAW 1, failure mode #3). -->
# ⚒ Forge plan · projA lore

📜 13 sessions · 29 beats · 2 candidates proposed

## Proposed (2)

---

### 1 · verify-build
**high confidence** · verifier: confirmed · from `build:run × go build ▸ go test`

> **Use when:** Use when the user asks to run a build and the tests before declaring work done.

**Needs first:**
- A Go workspace with tests

**The method:**
1. Run go build ./... and stop on any compile error
2. Run go test ./... and read every failure before touching code

**Done when:**
- Both commands exit 0 in the same beat

**When it goes wrong:**
- ⚠ Tests fail after a green build
  ↳ Read the first failing test before editing anything  (`projA/.specstory/history/2026-05-02_11-00-00Z-can-we-run-a.md:30`)

---

### 2 · locate-before-touching
**latent practice** · 3 beats · evidence unchanged

**Locate the mechanism before touching it**

Pins the implementation site read-only, restates the observed behavior, and only then authorizes an edit that mirrors the located pattern.

*Miner's note: lens=diagnosis-style | the user experiences this as just asking questions*

**In their own words:**
> "Show me where in the code we are doing this. Don't write any code yet, just locate the mechanism."
>   (`cccc-cccc-cccc-cccc/2026-05-10_09-00-00Z-where-is-save-implemented.md#1`)

---

## Skipping (1)

- **xcodebuild ▸ xcodebuild**: generic build loop, no judgment encoded

---

## On approval

1. Forge each skill to personal scope (~/.agents/skills/<name>)
2. Symlink into every harness skills dir
3. Register each with `forged add`; record a `forged decline` for every skip

=== dossiers above: 2 ===
