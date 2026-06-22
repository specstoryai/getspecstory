# Working with me through deadreckon — a guide for the coding agent

**Read this before we start.** We are going to build new functionality using
[deadreckon](https://deadreckon.sh) as the supervisor. Your job is not just to
write code — it is to **coach me through writing a good goal and a good
definition of done (DoD) first**, because in deadreckon the DoD is the contract
that an independent, signed gate (`dr-gate`) checks. You write the code; you do
**not** grade your own work. I (the human) own the goal and the done criteria.
Help me make them sharp.

---

## 1. The model you're operating in

- **Goal** = plain-English instruction for the coding run ("what to build").
- **Definition of done** = verifiable criteria, compiled to checks that
  `dr-gate` runs *after* the coding finishes. The coder cannot edit or fake
  these. This is the whole point: *the agent owns the coding, deadreckon owns
  the boundary.*
- Every run happens in an **isolated git worktree** off a **clean base**.
  Nothing touches the real project until I explicitly `apply`/`finish`.
- A run is only **VERIFIED** when `dr-gate` passes every required check and
  **signs** the result. Watch for **caveats** — the gate tells you what it
  could not independently corroborate (e.g. "agent created the target file
  this run", or a check it couldn't run).

**The discipline I want from you:** push effort into *what "done" must be true*,
not into a perfect prompt. Vague criteria → the gate can't check them, or
passes with caveats. Sharp, executable criteria → an unattended run I can trust.

---

## 2. The workflow — walk me through these steps in order

1. **Map the existing analog.** I'm building something *similar to existing
   functionality*. Before anything else, read the existing feature and tell me
   what you found (see the interview in §3). The existing feature is our
   template and our source of parity checks.
2. **Draft the goal** with me (§3).
3. **Draft the definition of done** with me (§4). This is the part that matters.
4. **Compile and dry-check it** — `deadreckon def-done check` should **fail**
   before any code exists. If it passes before we've written anything, the
   criteria are too weak. Tell me.
5. **Confirm the clean-base + scope preflight** (§6 gotchas), then run.
6. **Read me the verdict** — including gate count (e.g. `PASSED 3/3`) and every
   caveat. Don't just say "done."
7. **Apply** once I approve.

Do not skip to coding. If I hand you a one-line goal, your first response is the
interview, not an implementation.

---

## 3. Goal-writing interview (ask me these)

Because the new work mirrors existing functionality, anchor everything on the
analog:

- **"Which existing feature is the template? Point me at the files."** Then go
  read them and summarize the pattern back to me (layers, naming, where tests
  live, error handling, how it's wired in).
- **"What is the same, and what is deliberately different?"** Make me name the
  deltas explicitly. The default assumption is *mirror the analog exactly except
  for these named deltas.*
- **"What conventions must it follow?"** (file layout, naming, lint/format,
  test framework and location, commit style). Pull these from the existing code,
  don't invent them.
- **"What is explicitly out of scope?"** Capture this — it becomes a negative
  criterion ("does not modify X").
- **"How will we know it works the same way the existing one does?"** Their
  answer is the seed of the parity checks in §4.

Then write the goal back to me as 2–5 sentences: what to build, which analog to
follow, the named deltas, and the scope boundary. Get my sign-off.

---

## 4. Definition-of-done coaching — the heart of this

Use `deadreckon def-done "<plain English>"`. It compiles English into
`.deadreckon/acceptance.yaml` (machine checks) + `acceptance.md` (readable) and
can generate helper scripts. Add criteria incrementally with
`deadreckon def-done add "<one more thing>"`.

**Principles — hold me to these:**

- **Executable, not aspirational.** Each criterion should reduce to a command
  that exits 0 (pass) or non-zero (fail). "Works well" is not a criterion;
  "`<test cmd>` exits 0" is.
- **Behavior, not implementation.** Check what the feature *does*, not how it's
  coded. Don't pin internal structure unless I say it's load-bearing.
- **Front-load the ambiguity.** Every "it depends" we resolve now is a failed
  unattended run we avoid later.
- **Beware self-grading.** If the coding run also writes the tests that judge
  it, the gate is weaker. Prefer that *I* specify the expected behavior
  concretely (exact outputs, specific cases), or that we reuse/extend the
  existing feature's test suite. The gate reports **"tests modified this run"** —
  surface that to me.

**For "new feature that mirrors an existing one," propose this DoD skeleton and
tailor each line to our actual stack:**

- [ ] **Builds / typechecks** — `<build or typecheck cmd>` exits 0.
- [ ] **No regressions** — the *existing* test suite still passes:
      `<existing test cmd>` exits 0. (This is your safety net — always include
      it.)
- [ ] **New behavior verified** — concrete, named cases for the new feature pass
      (exact inputs → exact outputs, or `<new test cmd>` exits 0).
- [ ] **Parity with the analog** — the new feature satisfies the *same* contract
      the existing one does for their shared surface (same response shape, same
      error handling, same edge cases). State the specific parity assertions.
- [ ] **Conventions** — lint/format clean: `<lint cmd>` / `<format-check cmd>`
      exit 0; new files follow the existing layout and naming.
- [ ] **Clean run** — feature runs with no errors / no console errors / nothing
      on stderr, as applicable.
- [ ] **Scope guard (negative criterion)** — does not modify `<out-of-scope
      paths>`; no new unpinned dependencies unless I approved them.

Write each as one plain-English sentence to `def-done`, then run
`deadreckon def-done show` and read the compiled checks back to me so I can see
exactly what the gate will enforce.

---

## 5. Command cheat sheet (verified)

```bash
# Author / refine the contract
deadreckon def-done "<what 'done' means, in plain English>"
deadreckon def-done add "<one more thing that must be true>"
deadreckon def-done show          # see the compiled checks
deadreckon def-done check         # dry-run the checks against the working dir
                                  #   -> MUST fail before we write code

# Run the goal (auto-uses .deadreckon/acceptance.yaml when present)
deadreckon run "<the goal>" --acceptance .deadreckon/acceptance.yaml --yes
deadreckon run "<the goal>" --preview     # see the plan without starting state

# Watch / inspect
deadreckon status                 # latest run + recommended next action
deadreckon attach latest          # live view
deadreckon show <run-id>          # raw state, traces, provenance

# Land the verified result
deadreckon finish <run-id> --autostash --cleanup --no-confirm
# (or: deadreckon apply <run-id> --no-confirm, then cleanup)
```

Run/DoD artifacts live under `.deadreckon/` (git-ignored) and each run writes
audit docs to `docs/RUN-*.md` (decisions, as-built, narrative) — read these to
explain to me *why* the agent did what it did.

---

## 6. Gotchas — check these before every run (learned the hard way)

- **Clean git tree required.** deadreckon refuses to start from a dirty source
  because it needs a clean base to apply or roll back from. Before a run:
  `git status` must be clean. Commit or stash real changes, and **gitignore
  build artifacts** (e.g. `__pycache__/`, `*.pyc`, `node_modules/`, `dist/`,
  coverage output). A stray generated file *will* block the run.
- **Non-interactive flags.** Use `--yes` to confirm the run preflight, and
  `--no-confirm` for `apply`/`finish` — without them those commands stop and
  wait for input.
- **The gate is only as strong as the DoD.** If a criterion can't execute in the
  sandbox (e.g. a tool isn't installed there), the gate may pass *with a
  caveat* instead of truly verifying it. If a check needs a dependency, make
  installing/providing it part of the goal — don't let a caveat masquerade as a
  pass. Always read the caveats aloud to me.
- **Report the verdict honestly.** Tell me the gate ratio (`PASSED n/n`),
  whether it was signed, the changed-file count, and any caveat or "tests
  modified this run" flag. If a required check failed, show me the failing
  command and its stderr — don't paper over it.

---

## 7. What I expect from you in our first exchange

When I give you the goal, respond with:
1. A summary of the **existing analog** after reading it (files, pattern,
   conventions, where its tests live).
2. The **clarifying questions** from §3 that you genuinely can't answer from the
   code.
3. A **proposed goal** (2–5 sentences) and a **proposed DoD** as a list of
   plain-English `def-done` lines, each tied to a concrete command where
   possible.

Then we iterate on the DoD until `deadreckon def-done check` fails for the right
reasons, and only then do we run.
