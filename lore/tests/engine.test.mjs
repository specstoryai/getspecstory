// engine.test.mjs — unit tests for the pure parsing layer + an end-to-end integration test
// over the synthetic fixtures (one per provider form factor of the verified output formula).
// Run: npm test   (node --test tests/)

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, writeFileSync, rmSync, cpSync, existsSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { tmpdir } from 'node:os'
import { DatabaseSync } from 'node:sqlite'
import { headsFrom, extractShellBlock, parseSessionFile, intentSig } from '../scripts/lib/parse.mjs'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const FIX = join(ROOT, 'fixtures')
const fixture = (p) => readFileSync(join(FIX, p), 'utf8')

// ---------- headsFrom: raw command string -> meaningful heads ----------

test('headsFrom: splits chains, keeps subcommands, drops noise and prose', () => {
  assert.deepEqual(headsFrom('go build ./... && go test ./...'), ['go build', 'go test'])
  assert.deepEqual(headsFrom('git status --short'), ['git status'])
  assert.deepEqual(headsFrom('FOO=bar go test ./...'), ['go test'])            // env prefix stripped
  assert.deepEqual(headsFrom("bash -lc 'go vet ./pkg/...'"), ['go vet'])       // bash -lc unwrapped
  assert.deepEqual(headsFrom('nl -ba file.go | sed -n 1,2p'), [])              // recon noise dropped
  assert.deepEqual(headsFrom('Co-Authored-By: Claude <noreply@anthropic.com>'), [])  // prose rejected
  assert.deepEqual(headsFrom('./scripts/run.sh deploy'), ['./scripts/run.sh deploy'])  // project scripts kept
})

// ---------- extractShellBlock: the four verified command locations ----------

test('extractShellBlock: Codex single-line lives in the <summary>; nonzero exit counted', () => {
  const { cmds, fails } = extractShellBlock(
    '<summary>Tool use: **exec_command** `go build ./...`</summary>',
    ['```', 'Process exited with code 1', '```'], 10)
  assert.deepEqual(cmds.map(c => c.cmd), ['go build ./...'])
  assert.equal(fails, 1)
})

test('extractShellBlock: Claude single-line is an inline-backtick body line; output fence ignored', () => {
  const { cmds, fails } = extractShellBlock(
    '<summary>Tool use: **Bash**</summary>',
    ['Build the project', '', '`go build ./...`', '', '```text', 'ok', '```'], 10)
  assert.deepEqual(cmds.map(c => c.cmd), ['go build ./...'])
  assert.equal(fails, 0)
})

test('extractShellBlock: multi-line bash fence; heredoc bodies are skipped', () => {
  const { cmds } = extractShellBlock(
    '<summary>Tool use: **Bash**</summary>',
    ['```bash', 'git add -A', `git commit -m "$(cat <<'EOF'`, 'feat: thing', 'Co-Authored-By: Claude', 'EOF', ')"', '```'], 10)
  const raw = cmds.map(c => c.cmd)
  assert.ok(raw.includes('git add -A'))
  assert.ok(!raw.some(c => c.startsWith('Co-Authored-By')), 'heredoc body must not leak as commands')
})

test('extractShellBlock: legacy shell renders as a "- command:" bullet', () => {
  const { cmds } = extractShellBlock(
    '<summary>Tool use: **shell**</summary>',
    ['**Input:**', '', '- command: `[bash -lc go vet ./pkg/...]`', '- workdir: `/work`', '**Result:**', '```', 'ok', '```'], 10)
  assert.deepEqual(cmds.map(c => c.cmd), ['bash -lc go vet ./pkg/...'])
})

// ---------- parseSessionFile: beats, outcomes, agents, metas ----------

test('parseSessionFile: beats segmented; outcome from next turn (success)', () => {
  const { agent, beats } = parseSessionFile(fixture('projA/.specstory/history/2026-05-01_10-00-00Z-can-we-run-a.md'))
  assert.equal(agent, 'claude-code')
  assert.equal(beats.length, 2)
  assert.equal(beats[0].outcome, 'success')          // next turn: "ok lets write a commit"
  assert.equal(beats[1].outcome, 'end')
  const heads = beats[0].cmds.map(c => c.head)
  assert.deepEqual(heads, ['go build', 'go test', 'golangci-lint run'])
})

test('parseSessionFile: steering correction labels the prior beat; meta detectors fire', () => {
  const { beats } = parseSessionFile(fixture('projA/.specstory/history/2026-05-02_11-00-00Z-can-we-run-a.md'))
  assert.equal(beats[0].outcome, 'corrected')        // next turn starts "no wait, ..."
  assert.ok(beats[1].metas.some(m => m.id === 'trace-code-paths'))
  assert.ok([...beats[1].files].includes('./pkg/cli/root.go'))   // Read tool path captured
})

test('parseSessionFile: read-only meta on the opening prompt', () => {
  const { beats } = parseSessionFile(fixture('projA/.specstory/history/2026-05-03_12-00-00Z-can-we-run-a.md'))
  assert.ok(beats[0].metas.some(m => m.id === 'read-only-diagnosis'))
})

test('parseSessionFile: legacy (~2025) bare "Tool use:" format — commands, files, errors', () => {
  const { agent, beats } = parseSessionFile(fixture('projA/.specstory/history/2025-07-10_09-00-00Z-fix-the-gitignore.md'))
  assert.equal(agent, 'claude-code')
  assert.equal(beats.length, 1)
  const heads = beats[0].cmds.map(c => c.head)
  assert.deepEqual(heads, ['git rm', 'git status'])               // inline-backtick commands captured
  assert.ok([...beats[0].files].includes('./BearApp/.gitignore'))  // legacy Read path captured
  assert.equal(beats[0].fails, 1)                              // "fatal:" in Result fence counted
})

test('intentSig: verb + salient keyword', () => {
  assert.equal(intentSig('can we run a build and all the tests'), 'build:run')
  assert.equal(intentSig('ok lets write a commit'), 'write:commit')
  assert.equal(intentSig('hello there'), null)
})

// ---------- integration: index fixtures -> corpus -> corroborated report ----------

test('integration: multi-provider, cross-project, corroborated with outcomes', () => {
  const dbPath = join(tmpdir(), `lore-test-${process.pid}.db`)
  rmSync(dbPath, { force: true })
  try {
    const out = execFileSync('node', ['scripts/mine-skills.mjs', 'index', '--projects', 'fixtures', '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    assert.match(out, /indexed 12 sessions/)
    assert.match(out, /7 project\(s\)/)

    // multi-agent tagging straight from the corpus
    const db = new DatabaseSync(dbPath)
    const agents = new Set(db.prepare('SELECT DISTINCT agent FROM sessions').all().map(r => r.agent))
    assert.deepEqual([...agents].sort(), ['antigravity', 'claude-code', 'codex-cli', 'cursor-cli', 'deepseek-tui', 'factory-droid-cli', 'gemini-cli'])

    // relative --projects input must be stored as ABSOLUTE paths (valid from any future cwd)
    for (const r of db.prepare('SELECT path FROM sessions').all()) {
      assert.ok(r.path.startsWith('/'), `stored path must be absolute, got: ${r.path}`)
    }

    // codex exit-code failure captured; legacy bullet command captured
    const fails = db.prepare("SELECT SUM(exit_fails) f FROM beats e JOIN sessions s ON s.id=e.session_id WHERE s.agent='codex-cli'").get()
    assert.equal(fails.f, 1)
    const vet = db.prepare("SELECT COUNT(*) c FROM commands WHERE head='go vet'").get()
    assert.ok(vet.c >= 1)

    // report: corroborated build:run × go-build procedure, with both outcome polarities
    const json = JSON.parse(execFileSync('node',
      ['scripts/mine-skills.mjs', 'report', '--db', dbPath, '--min-sessions', '2', '--emit', 'json'],
      { cwd: ROOT, encoding: 'utf8' }))
    assert.equal(json.scope.sessions, 12)
    assert.equal(json.scope.projects, 7)
    const corr = json.corroborated.find(c => c.isig === 'build:run' && c.gram.includes('go build ▸ go test'))
    assert.ok(corr, 'expected corroborated build:run × go build ▸ go test')
    assert.ok(corr.ok >= 2 && corr.bad >= 1, `outcome rates captured (got ok=${corr.ok} bad=${corr.bad})`)
    // the intent recurs in BOTH projects -> portable signal on the intents channel
    const intent = json.intents.find(c => c.key === 'build:run')
    assert.equal(intent.np, 3)   // projA + projB + provider-gemini all ask for a build

    // compact emit opens with the badge and ends with the engine-built pass-through footer (LAW 2)
    const compact = execFileSync('node', ['scripts/mine-skills.mjs', 'report', '--db', dbPath, '--min-sessions', '2'], { cwd: ROOT, encoding: 'utf8' })
    assert.ok(compact.startsWith('📜'), 'badge must be the first line')
    assert.match(compact, /PASS-THROUGH FOOTER/)
    assert.match(compact, /📜 Lore mined!/)
    assert.ok(!JSON.stringify(json).includes('PASS-THROUGH'), 'json emit stays pure')
  } finally {
    rmSync(dbPath, { force: true })
    rmSync(dbPath + '-wal', { force: true })
    rmSync(dbPath + '-shm', { force: true })
  }
})

// ---------- idempotency: re-run, force, and prune semantics ----------

test('idempotency: re-index skips unchanged, --force re-parses, prune drops deleted transcripts', () => {
  const work = join(tmpdir(), `lore-idem-${process.pid}`)
  const dbPath = join(work, 'corpus.db')
  rmSync(work, { recursive: true, force: true })
  cpSync(FIX, join(work, 'fix'), { recursive: true })
  try {
    const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    assert.match(run('index', '--projects', join(work, 'fix')), /indexed 12 sessions \(0 unchanged/)
    // second run: nothing changed -> fully skipped (idempotent)
    assert.match(run('index', '--projects', join(work, 'fix')), /indexed 0 sessions \(12 unchanged/)
    // --force: everything re-parsed, corpus stays consistent (replace, not duplicate)
    assert.match(run('index', '--projects', join(work, 'fix'), '--force'), /indexed 12 sessions/)
    const db = new DatabaseSync(dbPath)
    assert.equal(db.prepare('SELECT COUNT(*) c FROM sessions').get().c, 12)
    db.close()
    // delete a transcript on disk -> prune removes its rows
    rmSync(join(work, 'fix', 'projB', '.specstory', 'history', '2026-05-05_14-00-00Z-can-we-run-a.md'))
    assert.match(run('prune'), /pruned 1 sessions .*\(11 remain\)/)
    const db2 = new DatabaseSync(dbPath)
    assert.equal(db2.prepare('SELECT COUNT(*) c FROM sessions').get().c, 11)
    assert.equal(db2.prepare('SELECT COUNT(*) c FROM beats e WHERE NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id=e.session_id)').get().c, 0, 'no orphan beats')
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
})

// ---------- Phase C primitives: beat export + dossier cache ----------

test('beats export: stratified spans with stable fingerprint; dossier cache roundtrip', () => {
  const work = join(tmpdir(), `lore-epi-${process.pid}`)
  const dbPath = join(work, 'corpus.db')
  rmSync(work, { recursive: true, force: true })
  try {
    const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    run('index', '--projects', 'fixtures')
    const out = JSON.parse(run('beats', '--corr', 'build:run × go build ▸ go test', '--max', '5'))
    assert.equal(out.totalMatching, 3)
    assert.equal(out.beats[0].outcome, 'corrected', 'corrected beats are exported first')
    assert.ok(out.beats.every(e => e.text.includes('go build')), 'spans contain the method')
    assert.match(out.fingerprint, /^[0-9a-f]{16}$/)
    const again = JSON.parse(run('beats', '--corr', 'build:run × go build ▸ go test'))
    assert.equal(again.fingerprint, out.fingerprint, 'fingerprint stable across calls')
    // cache roundtrip
    execFileSync('node', ['scripts/mine-skills.mjs', 'dossier', 'put', '--key', 'k1', '--fingerprint', out.fingerprint, '--file', '-', '--db', dbPath],
      { cwd: ROOT, input: '{"steps":["x"]}', encoding: 'utf8' })
    const got = JSON.parse(run('dossier', 'get', '--key', 'k1'))
    assert.equal(got.fingerprint, out.fingerprint)
    assert.deepEqual(JSON.parse(got.json), { steps: ['x'] })
    // render: canonical pass-through blocks ending with the LAW 1 sentinel
    const md = run('dossier', 'render')
    assert.match(md, /PASS-THROUGH DOSSIERS/)
    assert.match(md, /=== dossiers above: 1 ===/)
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
})

// ---------- forged registry: provenance, declines, drift detection ----------

test('forged registry: up-to-date → evidence growth + hand-edit → update-carefully; declines suppressed', () => {
  const work = join(tmpdir(), `lore-forged-${process.pid}`)
  const dbPath = join(work, 'corpus.db')
  rmSync(work, { recursive: true, force: true })
  cpSync(FIX, join(work, 'fix'), { recursive: true })
  try {
    const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    run('index', '--projects', join(work, 'fix'))
    // register a forged skill + a decline
    const skillFile = join(work, 'SKILL.md')
    execFileSync('node', ['-e', `require('fs').writeFileSync(${JSON.stringify(skillFile)}, '# s')`])
    run('forged', 'add', '--name', 'verify-build', '--path', skillFile, '--cluster', 'build:run × go build ▸ go test', '--kind', 'corr')
    run('forged', 'decline', '--cluster', 'write:commit × git add ▸ git commit', '--kind', 'corr')
    let chk = JSON.parse(run('forged', 'check'))
    assert.equal(chk.find(r => r.name === 'verify-build').recommendation, 'up-to-date')
    assert.match(chk.find(r => r.status === 'declined').recommendation, /^suppress/)
    // grow the cluster (one more session with a corrected beat) and hand-edit the skill file
    const src = readFileSync(join(work, 'fix/projA/.specstory/history/2026-05-02_11-00-00Z-can-we-run-a.md'), 'utf8')
    execFileSync('node', ['-e', `require('fs').writeFileSync(${JSON.stringify(join(work, 'fix/projA/.specstory/history/2026-05-06_09-00-00Z-can-we-run-a.md'))}, process.argv[1])`,
      src.replaceAll('2026-05-02 11:0', '2026-05-06 09:0').replace('000000000002', '000000000007')])
    run('index', '--projects', join(work, 'fix'))
    execFileSync('node', ['-e', `require('fs').appendFileSync(${JSON.stringify(skillFile)}, 'tweak')`])
    chk = JSON.parse(run('forged', 'check'))
    const v = chk.find(r => r.name === 'verify-build')
    assert.match(v.recommendation, /^update-carefully/)
    assert.ok(v.newCorrected >= 1)
    assert.equal(v.file, 'hand-edited')
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
})

// Regression: a real forge run crashed on (a) declining a gram cluster with no --kind (the old
// default was corr, whose key parser threw) and (b) declining a theme (kind unsupported). Kind
// must now be inferred from the cluster's shape, theme must be a first-class kind, and a stats
// failure must never lose the registry write.
test('forged registry: kind inferred from cluster shape; theme kind supported; never crashes', () => {
  const work = join(tmpdir(), `lore-forged-kind-${process.pid}`)
  const dbPath = join(work, 'corpus.db')
  rmSync(work, { recursive: true, force: true })
  cpSync(FIX, join(work, 'fix'), { recursive: true })
  try {
    const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    run('index', '--projects', join(work, 'fix'))
    // gram-shaped cluster, NO --kind: must infer gram and record (this exact call used to exit 1)
    run('forged', 'decline', '--cluster', 'go build ▸ go test', '--note', 'too thin')
    // corr-shaped and sig-shaped, NO --kind
    run('forged', 'decline', '--cluster', 'build:run × go build ▸ go test')
    run('forged', 'decline', '--cluster', 'build:run')
    // theme kind: store a theme, then decline it by id (used to throw "beats: need --corr...")
    const keys = JSON.parse(run('beats', '--gram', 'go build ▸ go test', '--max', '5')).beats.map(b => b.key)
    execFileSync('node', ['scripts/mine-skills.mjs', 'theme', 'put', '--file', '-', '--db', dbPath],
      { cwd: ROOT, input: JSON.stringify({ id: 'freeze-first', title: 'Freeze first', description: 'd', beatKeys: keys, lens: 'test' }), encoding: 'utf8' })
    run('forged', 'decline', '--cluster', 'freeze-first')
    const rows = JSON.parse(run('forged', 'list'))
    const byKey = Object.fromEntries(rows.map(r => [r.cluster_key, r]))
    assert.equal(byKey['go build ▸ go test'].kind, 'gram')
    assert.equal(byKey['build:run × go build ▸ go test'].kind, 'corr')
    assert.equal(byKey['build:run'].kind, 'sig')
    assert.equal(byKey['freeze-first'].kind, 'theme')
    assert.ok(byKey['freeze-first'].sessions >= 1, 'theme stats resolved from member beats')
    // check must handle every kind without crashing and give declines a recommendation
    const chk = JSON.parse(run('forged', 'check'))
    assert.equal(chk.length, 4)
    for (const r of chk) assert.match(r.recommendation, /suppress|re-engage/)
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
})

// ---------- provider coverage: every README-claimed provider header parses ----------

test('provider headers: Gemini CLI, Factory Droid CLI, DeepSeek TUI, Antigravity all parse', () => {
  const cases = [
    ['provider-gemini/.specstory/history/2026-05-20_09-00-00Z-list-the-workspace.md', 'gemini-cli', 'npm run'],
    ['provider-droid/.specstory/history/2026-05-21_09-00-00Z-show-git-status.md', 'factory-droid-cli', 'git status'],
    ['provider-deepseek/.specstory/history/2026-05-22_09-00-00Z-count-source-lines.md', 'deepseek-tui', 'pytest'],
    ['provider-antigravity/.specstory/history/2026-05-23_09-00-00Z-run-the-linter.md', 'antigravity', 'npx eslint'],
  ]
  for (const [file, agent, head] of cases) {
    const parsed = parseSessionFile(fixture(file))
    assert.equal(parsed.agent, agent, file)
    assert.equal(parsed.beats[0].outcome, 'success', `${file}: "that worked, thanks" labels success`)
    assert.ok(parsed.beats[0].cmds.some(c => c.head.startsWith(head)), `${file}: expected a "${head}" command`)
  }
})

// ---------- projC: the semantic channel as fixtures (shapes, lenses, snowball) ----------
// Content paraphrases practices mined from REAL runs (locate-before-touching,
// falsify-with-discriminating-observation) so the fixtures spec what the skill actually surfaces.

test('projC semantic fixtures: shapes and lens filters select the right beats', () => {
  const dbPath = join(tmpdir(), `lore-projc-${process.pid}.db`)
  rmSync(dbPath, { force: true })
  try {
    const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    run('index', '--projects', 'fixtures')
    const sample = (...extra) => JSON.parse(run('beats', '--project', 'projC', '--max', '20', ...extra)).beats
    assert.equal(sample('--shape', 'conversation').length, 2, 'review-critique + docs near-miss are tool-less')
    assert.equal(sample('--shape', 'read-only').length, 4, 'locate/diagnose beats read but never execute')
    assert.equal(sample('--shape', 'write').length, 3)
    // each thematic lens regex selects at least one projC beat
    for (const re of ['review|assess|critique|evaluate', 'verify|prove|check|confirm', 'revert|start over|rewrite']) {
      assert.ok(sample('--intent-re', re).length >= 1, `lens regex ${re} must match projC`)
    }
    // the discriminating-observation correction is labeled from the next prompt
    const corrected = sample().filter(b => b.outcome === 'corrected')
    assert.equal(corrected.length, 1)
    assert.match(corrected[0].text, /review the error handling/)
  } finally {
    rmSync(dbPath, { force: true }); rmSync(dbPath + '-wal', { force: true }); rmSync(dbPath + '-shm', { force: true })
  }
})

test('projC snowball: expansion finds the cross-session member; the near-miss stays below the bar', () => {
  const dbPath = join(tmpdir(), `lore-snowball-${process.pid}.db`)
  rmSync(dbPath, { force: true })
  try {
    const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    run('index', '--projects', 'fixtures')
    const S1 = 'cccc-cccc-cccc-cccc/2026-05-10_09-00-00Z-where-is-save-implemented.md'
    const S2 = 'cccc-cccc-cccc-cccc/2026-05-11_10-00-00Z-where-is-token-validation.md'
    execFileSync('node', ['scripts/mine-skills.mjs', 'theme', 'put', '--file', '-', '--db', dbPath], {
      cwd: ROOT, encoding: 'utf8',
      input: JSON.stringify({ id: 'locate-before-touching', title: 'Locate before touching',
        description: 'pins the implementation site read-only before authorizing any edit', beatKeys: [`${S1}#0`, `${S1}#1`] }),
    })
    const exp = JSON.parse(run('theme', 'expand', '--key', 'locate-before-touching'))
    const keys = exp.candidates.map(c => c.key)
    assert.ok(keys.includes(`${S2}#0`), 'true cross-session member surfaces (should-flag)')
    assert.ok(!keys.includes(`${S2}#1`), 'the "where can I download docs" near-miss stays below the default bar (should-pass)')
    // lowering the bar surfaces the near-miss as a LEAD - this is exactly what verify-before-grow rejects
    const loose = JSON.parse(run('theme', 'expand', '--key', 'locate-before-touching', '--min-score', '1'))
    assert.ok(loose.candidates.map(c => c.key).includes(`${S2}#1`))
  } finally {
    rmSync(dbPath, { force: true }); rmSync(dbPath + '-wal', { force: true }); rmSync(dbPath + '-shm', { force: true })
  }
})

// ---------- golden conformance: the rendered forge plan is byte-stable ----------
// fixtures/golden/forge-plan.md is the executable spec of plan layout. If a renderer change is
// intentional, regenerate with UPDATE_GOLDEN=1 npm test and review the golden diff in the PR.

test('golden forge plan: plan render output matches fixtures/golden/forge-plan.md byte for byte', () => {
  const dbPath = join(tmpdir(), `lore-golden-${process.pid}.db`)
  rmSync(dbPath, { force: true })
  try {
    const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    run('index', '--scan', 'fixtures')
    const S1 = 'cccc-cccc-cccc-cccc/2026-05-10_09-00-00Z-where-is-save-implemented.md'
    const S2 = 'cccc-cccc-cccc-cccc/2026-05-11_10-00-00Z-where-is-token-validation.md'
    execFileSync('node', ['scripts/mine-skills.mjs', 'dossier', 'put', '--key', 'build:run × go build ▸ go test', '--fingerprint', 'golden-fp', '--file', '-', '--db', dbPath], {
      cwd: ROOT, encoding: 'utf8',
      input: JSON.stringify({
        name: 'verify-build', confidence: 'high', verdict: 'confirmed',
        trigger: 'Use when the user asks to run a build and the tests before declaring work done.',
        preconditions: ['A Go workspace with tests'],
        steps: ['Run go build ./... and stop on any compile error', 'Run go test ./... and read every failure before touching code'],
        verification: ['Both commands exit 0 in the same beat'],
        failureModes: [{ what: 'Tests fail after a green build', recovery: 'Read the first failing test before editing anything', ref: 'projA/.specstory/history/2026-05-02_11-00-00Z-can-we-run-a.md:30' }],
        parameters: ['package selector (./... vs a single package)'],
      }),
    })
    execFileSync('node', ['scripts/mine-skills.mjs', 'theme', 'put', '--file', '-', '--db', dbPath], {
      cwd: ROOT, encoding: 'utf8',
      input: JSON.stringify({
        id: 'locate-before-touching', title: 'Locate the mechanism before touching it',
        description: 'Pins the implementation site read-only, restates the observed behavior, and only then authorizes an edit that mirrors the located pattern.',
        beatKeys: [`${S1}#0`, `${S1}#1`, `${S2}#0`],
        evidence: [
          { key: `${S1}#1`, quote: "Show me where in the code we are doing this. Don't write any code yet, just locate the mechanism." },
          { key: 'meta', quote: 'lens=diagnosis-style | the user experiences this as just asking questions' },
        ],
      }),
    })
    const manifest = {
      project: 'projA', scope: 'personal scope (~/.agents/skills/<name>)',
      proposed: [
        { cluster: 'build:run × go build ▸ go test', name: 'verify-build' },
        { theme: 'locate-before-touching', name: 'locate-before-touching' },
      ],
      skipped: [{ candidate: 'xcodebuild ▸ xcodebuild', reason: 'generic build loop, no judgment encoded' }],
    }
    const plan = execFileSync('node', ['scripts/mine-skills.mjs', 'plan', 'render', '--file', '-', '--db', dbPath],
      { cwd: ROOT, input: JSON.stringify(manifest), encoding: 'utf8' })
    const goldenPath = join(FIX, 'golden', 'forge-plan.md')
    if (process.env.UPDATE_GOLDEN) {
      execFileSync('mkdir', ['-p', join(FIX, 'golden')])
      writeFileSync(goldenPath, plan)
    }
    assert.equal(plan, readFileSync(goldenPath, 'utf8'),
      'plan layout drifted - if intentional, regenerate with UPDATE_GOLDEN=1 npm test and review the golden diff')
  } finally {
    rmSync(dbPath, { force: true }); rmSync(dbPath + '-wal', { force: true }); rmSync(dbPath + '-shm', { force: true })
  }
})

// ---------- plan persistence: a canceled forge is recallable ----------

test('plan last: re-renders the most recent manifest against the current corpus', () => {
  const dbPath = join(tmpdir(), `lore-planlast-${process.pid}.db`)
  rmSync(dbPath, { force: true })
  try {
    const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    run('index', '--projects', 'fixtures')
    execFileSync('node', ['scripts/mine-skills.mjs', 'dossier', 'put', '--key', 'build:run × go build ▸ go test', '--fingerprint', 'f', '--file', '-', '--db', dbPath],
      { cwd: ROOT, input: JSON.stringify({ name: 'verify-build', confidence: 'high', steps: ['go build', 'go test'] }), encoding: 'utf8' })
    const manifest = { project: 'projA', proposed: [{ cluster: 'build:run × go build ▸ go test', name: 'verify-build' }] }
    const first = execFileSync('node', ['scripts/mine-skills.mjs', 'plan', 'render', '--file', '-', '--db', dbPath],
      { cwd: ROOT, input: JSON.stringify(manifest), encoding: 'utf8' })
    // ... user cancels the forge; a later session recalls the plan without re-judging
    assert.equal(run('plan', 'last'), first, 'recalled plan re-renders identically on an unchanged corpus')
    const list = JSON.parse(run('plan', 'list'))
    assert.equal(list.length, 1)
    assert.deepEqual(list[0].proposed, ['verify-build'])
    assert.match(run('runs', 'list'), /rendered 1 candidate/)
  } finally {
    rmSync(dbPath, { force: true }); rmSync(dbPath + '-wal', { force: true }); rmSync(dbPath + '-shm', { force: true })
  }
})

// ---------- snowball expansion: theme expand -> grow -> outcome lift on the card ----------

test('theme expand/grow: deterministic candidates from member vocabulary; card gains lift line', () => {
  const work = join(tmpdir(), `lore-expand-${process.pid}`)
  const dbPath = join(work, 'corpus.db')
  rmSync(work, { recursive: true, force: true })
  cpSync(FIX, join(work, 'fix'), { recursive: true })
  try {
    const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    run('index', '--projects', join(work, 'fix'))
    // seed a theme from a STRICT SUBSET of the build/test beats - expansion should find the rest
    const allKeys = JSON.parse(run('beats', '--gram', 'go build ▸ go test', '--max', '25')).beats.map(b => b.key)
    assert.ok(allKeys.length >= 2, 'fixtures must have at least 2 build/test beats')
    const seed = allKeys.slice(0, allKeys.length - 1)
    execFileSync('node', ['scripts/mine-skills.mjs', 'theme', 'put', '--file', '-', '--db', dbPath],
      { cwd: ROOT, input: JSON.stringify({ id: 'build-discipline', title: 'Build discipline', description: 'builds then tests', beatKeys: seed }), encoding: 'utf8' })
    const exp = JSON.parse(run('theme', 'expand', '--key', 'build-discipline', '--min-score', '1'))
    assert.ok(exp.terms.length >= 1, 'discriminating terms extracted')
    assert.ok(exp.candidates.length >= 1, 'finds candidates beyond current members')
    assert.ok(!exp.candidates.some(c => seed.includes(c.key)), 'existing members are never candidates')
    // grow with one confirmed candidate: member count rises, fingerprint changes
    const fpBefore = JSON.parse(run('theme', 'get', '--key', 'build-discipline')).fingerprint
    const out = run('theme', 'grow', '--key', 'build-discipline', '--keys', exp.candidates[0].key)
    assert.match(out, /grown to \d+ members \(\+1\)/)
    assert.notEqual(JSON.parse(run('theme', 'get', '--key', 'build-discipline')).fingerprint, fpBefore)
    // with >= 3 judged members the card carries the outcome-lift line
    const card = run('theme', 'render', '--key', 'build-discipline')
    if ((JSON.parse(run('beats', '--theme', 'build-discipline', '--max', '25')).beats
      .filter(b => b.outcome === 'success' || b.outcome === 'corrected').length) >= 3) {
      assert.match(card, /\*\*Outcome:\*\* \d+% ✓ over \d+ judged beats · corpus baseline \d+%/)
    }
    // grow is journaled (self-reporting duty)
    assert.match(run('runs', 'list'), /grew to \d+ members/)
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
})

// ---------- plan render + the ExitPlanMode hook: LAW 1 as enforcement, not instruction ----------

test('plan render: engine assembles the full curation plan (dossier + theme cards, sentinel last)', () => {
  const work = join(tmpdir(), `lore-plan-${process.pid}`)
  const dbPath = join(work, 'corpus.db')
  rmSync(work, { recursive: true, force: true })
  cpSync(FIX, join(work, 'fix'), { recursive: true })
  try {
    const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    run('index', '--projects', join(work, 'fix'))
    // cache one command dossier and one verified theme
    execFileSync('node', ['scripts/mine-skills.mjs', 'dossier', 'put', '--key', 'build:run × go build ▸ go test', '--fingerprint', 'f1', '--file', '-', '--db', dbPath],
      { cwd: ROOT, input: JSON.stringify({ name: 'verify-build', trigger: 'Use when building', steps: ['go build', 'go test'], confidence: 'high' }), encoding: 'utf8' })
    const keys = JSON.parse(run('beats', '--gram', 'go build ▸ go test', '--max', '5')).beats.map(b => b.key)
    execFileSync('node', ['scripts/mine-skills.mjs', 'theme', 'put', '--file', '-', '--db', dbPath],
      { cwd: ROOT, input: JSON.stringify({ id: 'freeze-first', title: 'Freeze first', description: 'names the cause before touching code', beatKeys: keys }), encoding: 'utf8' })
    const manifest = {
      project: 'projA', scope: 'personal (~/.agents/skills/<name>)',
      proposed: [{ cluster: 'build:run × go build ▸ go test', name: 'verify-build' }, { theme: 'freeze-first', name: 'freeze-before-fixing' }],
      skipped: [{ candidate: 'xcodebuild ▸ xcodebuild', reason: 'generic build loop' }],
    }
    const plan = execFileSync('node', ['scripts/mine-skills.mjs', 'plan', 'render', '--file', '-', '--db', dbPath],
      { cwd: ROOT, input: JSON.stringify(manifest), encoding: 'utf8' })
    assert.match(plan, /<!-- PASS-THROUGH FORGE PLAN/)
    assert.match(plan, /# ⚒ Forge plan · projA lore/)
    assert.match(plan, /### 1 · verify-build/)
    assert.match(plan, /### 2 · freeze-before-fixing/)
    assert.match(plan, /\*\*xcodebuild ▸ xcodebuild\*\*: generic build loop/)
    assert.equal(plan.trimEnd().split('\n').pop(), '=== dossiers above: 2 ===', 'sentinel is the last line')
    // proposing an un-mined cluster must fail loudly - the plan can only contain mined evidence
    assert.throws(() => execFileSync('node', ['scripts/mine-skills.mjs', 'plan', 'render', '--file', '-', '--db', dbPath],
      { cwd: ROOT, input: JSON.stringify({ proposed: [{ cluster: 'never-mined' }] }), encoding: 'utf8' }), /Command failed/)
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
})

test('ExitPlanMode hook: denies thin plans, allows the engine artifact, ignores other tools', () => {
  const hook = (payload) => execFileSync('node', [join(ROOT, 'scripts/hooks/validate-plan.mjs')],
    { input: JSON.stringify(payload), encoding: 'utf8' })
  // a hand-written "thin plan" (failure mode #3) is denied with an actionable reason
  const thin = hook({ tool_name: 'ExitPlanMode', tool_input: { plan: '# Forge plan\n\nI will forge 2 skills.\n' } })
  const verdict = JSON.parse(thin)
  assert.equal(verdict.hookSpecificOutput.permissionDecision, 'deny')
  assert.match(verdict.hookSpecificOutput.permissionDecisionReason, /plan render/)
  // the engine artifact passes (no output = allow)
  const good = '<!-- PASS-THROUGH FORGE PLAN: ... -->\n# Forge plan - x lore\n\n### a\n### b\n\n=== dossiers above: 2 ===\n'
  assert.equal(hook({ tool_name: 'ExitPlanMode', tool_input: { plan: good } }), '')
  // sentinel not last, or card count short of N: denied
  assert.match(JSON.parse(hook({ tool_name: 'ExitPlanMode', tool_input: { plan: good + '\nP.S. trust me' } })).hookSpecificOutput.permissionDecision, /deny/)
  assert.match(JSON.parse(hook({ tool_name: 'ExitPlanMode', tool_input: { plan: good.replace('### b\n', '') } })).hookSpecificOutput.permissionDecision, /deny/)
  // other tools and garbage stdin pass through silently - the hook must never block unrelated work
  assert.equal(hook({ tool_name: 'Bash', tool_input: { command: 'ls' } }), '')
  assert.equal(execFileSync('node', [join(ROOT, 'scripts/hooks/validate-plan.mjs')], { input: 'not json', encoding: 'utf8' }), '')
})

// ---------- skills inventory: every installed skill, lore-forged or not ----------

test('skills inventory: forged vs other, symlink dedup, broken links, registry orphans', () => {
  const work = join(tmpdir(), `lore-skills-${process.pid}`)
  const dbPath = join(work, 'corpus.db')
  rmSync(work, { recursive: true, force: true })
  cpSync(FIX, join(work, 'fix'), { recursive: true })
  const agents = join(work, 'agents-skills'), claude = join(work, 'claude-skills')
  try {
    const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    run('index', '--projects', join(work, 'fix'))
    // canonical forged skill in agents/, symlinked into claude/ (the fan-out dedupes to one row)
    execFileSync('mkdir', ['-p', join(agents, 'verify-build'), join(claude, 'foreign-skill')])
    writeFileSync(join(agents, 'verify-build', 'SKILL.md'),
      '---\nname: verify-build\ndescription: Build then test before declaring done.\n---\nbody\n')
    execFileSync('ln', ['-s', join(agents, 'verify-build'), join(claude, 'verify-build')])
    writeFileSync(join(claude, 'foreign-skill', 'SKILL.md'),
      '---\nname: foreign-skill\ndescription: Somebody else made this one.\n---\nbody\n')
    execFileSync('ln', ['-s', join(work, 'nowhere'), join(claude, 'dead-link')])
    run('forged', 'add', '--name', 'verify-build', '--path', join(agents, 'verify-build', 'SKILL.md'),
      '--cluster', 'build:run × go build ▸ go test')
    run('forged', 'add', '--name', 'ghost-skill', '--path', join(work, 'gone', 'SKILL.md'), '--cluster', 'build:run')
    const roots = `agents=${agents},claude=${claude}`
    const md = run('skills', '--roots', roots)
    assert.match(md, /PASS-THROUGH SKILLS/)
    assert.match(md, /⚒ FORGED BY LORE \(1\)/)
    assert.match(md, /verify-build.* · \d+ sessions · /)
    assert.match(md, /agents\+claude/, 'symlink fan-out dedupes to one row with both harnesses')
    assert.match(md, /📚 OTHER INSTALLED SKILLS \(1\)/)
    assert.match(md, /foreign-skill\s+claude/)
    assert.match(md, /Somebody else made this one\./)
    assert.match(md, /REGISTERED BUT NOT INSTALLED \(1\)/)
    assert.match(md, /ghost-skill/)
    assert.match(md, /BROKEN SYMLINKS \(1\)/)
    assert.match(md, /dead-link/)
    // json emit stays pure and carries the same groups
    const j = JSON.parse(run('skills', '--roots', roots, '--emit', 'json'))
    assert.equal(j.forged.length, 1)
    assert.equal(j.other.length, 1)
    assert.equal(j.missing.length, 1)
    assert.equal(j.broken.length, 1)
    assert.ok(!JSON.stringify(j).includes('PASS-THROUGH'))
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
})

// ---------- secret redaction: nothing the engine emits carries a live credential ----------
// (skills.sh Snyk audit W007: evidence rendering must not force the LLM to handle secrets)

test('redactSecrets: masks credential values, keeps structure and git SHAs', async () => {
  const { redactSecrets } = await import('../scripts/lib/patterns.mjs')
  const cases = [
    ['export GITHUB_TOKEN=ghp_16C7e42F292c6912E7710c838347Ae178B4a', /GITHUB_TOKEN=\[REDACTED:/],
    ['aws_key = AKIAIOSFODNN7EXAMPLE', /\[REDACTED:aws-key\]/],
    ['curl -H "Authorization: Bearer abcdef1234567890XYZ"', /Bearer \[REDACTED:bearer\]/],
    ['ANTHROPIC_API_KEY=sk-ant-api03-aaaaaaaaaaaaaaaaaaaa', /\[REDACTED:/],
    ['password: hunter2hunter2', /password: \[REDACTED:assignment\]/],
    ['xoxb-1234567890-abcdefghij', /\[REDACTED:slack-token\]/],
  ]
  for (const [input, want] of cases) assert.match(redactSecrets(input), want, input)
  // should-pass: evidence that must survive verbatim
  for (const keep of [
    'git checkout 4e38e7b2c1d0aa93f1e2b3c4d5e6f7a8b9c0d1e2',   // 40-hex commit SHA
    'const token = tokenize(intent)',                           // code, not a credential
    'go test ./... && golangci-lint run',
  ]) assert.equal(redactSecrets(keep), keep, keep)
})

test('redaction holds at the emit boundaries: beat spans and the forge plan', () => {
  const work = join(tmpdir(), `lore-redact-${process.pid}`)
  const dbPath = join(work, 'corpus.db')
  rmSync(work, { recursive: true, force: true })
  cpSync(FIX, join(work, 'fix'), { recursive: true })
  try {
    const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    // plant a fake credential into a transcript copy, where a real paste would land
    const f = join(work, 'fix/projA/.specstory/history/2026-05-01_10-00-00Z-can-we-run-a.md')
    writeFileSync(f, readFileSync(f, 'utf8').replace('go build ./...', 'GITHUB_TOKEN=ghp_16C7e42F292c6912E7710c838347Ae178B4a go build ./...'))
    run('index', '--projects', join(work, 'fix'))
    // beat span export: the span text is redacted, the command structure survives
    const out = run('beats', '--sig', 'build:run', '--max', '25')
    assert.ok(!out.includes('ghp_16C7e42F292c6912E7710c838347Ae178B4a'), 'no live token in exported spans')
    assert.match(out, /GITHUB_TOKEN=\[REDACTED:github-token\] go build/)
    // forge plan: a dossier whose cached JSON carries a secret still renders redacted
    execFileSync('node', ['scripts/mine-skills.mjs', 'dossier', 'put', '--key', 'build:run × go build ▸ go test', '--fingerprint', 'f', '--file', '-', '--db', dbPath],
      { cwd: ROOT, input: JSON.stringify({ name: 'verify-build', confidence: 'high', steps: ['run with AKIAIOSFODNN7EXAMPLE in the env'] }), encoding: 'utf8' })
    const plan = execFileSync('node', ['scripts/mine-skills.mjs', 'plan', 'render', '--file', '-', '--db', dbPath],
      { cwd: ROOT, input: JSON.stringify({ project: 'projA', proposed: [{ cluster: 'build:run × go build ▸ go test', name: 'verify-build' }] }), encoding: 'utf8' })
    assert.ok(!plan.includes('AKIAIOSFODNN7EXAMPLE'), 'no live key in the plan')
    assert.match(plan, /\[REDACTED:aws-key\]/)
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
})

// ---------- reset: wipe all persistence ----------

test('reset: deletes the corpus file and reports what was wiped', () => {
  const dbPath = join(tmpdir(), `lore-reset-${process.pid}.db`)
  rmSync(dbPath, { force: true })
  const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
  run('index', '--projects', 'fixtures')
  const out = run('reset')
  assert.match(out, /lore reset - deleted/)
  assert.match(out, /sessions 12/)
  assert.ok(!existsSync(dbPath), 'corpus file must be gone')
})

// ---------- scan: any-depth history discovery (monorepos) ----------

test('scan: finds nested .specstory/history dirs at any depth, including the root project', () => {
  const dbPath = join(tmpdir(), `lore-scan-${process.pid}.db`)
  rmSync(dbPath, { force: true })
  try {
    const out = execFileSync('node', ['scripts/mine-skills.mjs', 'index', '--scan', 'fixtures', '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    assert.match(out, /indexed 13 sessions/)        // projA(5) + projB(1) + projC(2) + providers(4) + nested subpkg(1)
    assert.match(out, /8 project\(s\)/)
    const db = new DatabaseSync(dbPath)
    const names = db.prepare('SELECT DISTINCT project_name FROM sessions ORDER BY 1').all().map(r => r.project_name)
    assert.deepEqual(names, ['projA', 'projB', 'projC', 'provider-antigravity', 'provider-deepseek', 'provider-droid', 'provider-gemini', 'subpkg'])
  } finally {
    rmSync(dbPath, { force: true }); rmSync(dbPath + '-wal', { force: true }); rmSync(dbPath + '-shm', { force: true })
  }
})

// ---------- authorship: git add-author > path sniff > machine user ----------

test('author attribution: git author wins; path-sniff and machine-user fallbacks', async () => {
  const { sniffAuthor } = await import('../scripts/lib/parse.mjs')
  assert.equal(sniffAuthor('ran `ls /Users/sean/Source/App` then /Users/sean/x again and /Users/gdc/y once'), 'sean')
  assert.equal(sniffAuthor('no home paths here'), null)

  const work = join(tmpdir(), `lore-auth-${process.pid}`)
  const dbPath = join(work, 'corpus.db')
  rmSync(work, { recursive: true, force: true })
  cpSync(join(FIX, 'projA'), join(work, 'repo'), { recursive: true })
  const g = (...a) => execFileSync('git', ['-C', join(work, 'repo'), ...a], { encoding: 'utf8', env: { ...process.env, GIT_AUTHOR_NAME: 'x', GIT_AUTHOR_EMAIL: 'x@x', GIT_COMMITTER_NAME: 'x', GIT_COMMITTER_EMAIL: 'x@x' } })
  try {
    g('init', '-q')
    g('add', '.specstory/history/2025-07-10_09-00-00Z-fix-the-gitignore.md')
    g('commit', '-q', '-m', 'add transcript', '--author', 'Sean Johnson <sean@x>')
    execFileSync('node', ['scripts/mine-skills.mjs', 'index', '--dir', join(work, 'repo', '.specstory', 'history'), '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    const db = new DatabaseSync(dbPath)
    const rows = Object.fromEntries(db.prepare('SELECT id, author FROM sessions').all().map(r => [r.id.split('/').pop(), r.author]))
    assert.equal(rows['2025-07-10_09-00-00Z-fix-the-gitignore.md'], 'Sean Johnson')   // git add-author wins
    const other = rows['2026-05-01_10-00-00Z-can-we-run-a.md']
    assert.equal(other, process.env.USER, 'uncommitted transcript falls back to the machine user')
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
})

// ---------- corpus-audit recoveries: multi-line bullets + legacy codex shell ----------

test('extractShellBlock: MULTI-LINE "- command:" bullet (codex envelope shell) — first line is the command', () => {
  const { cmds } = extractShellBlock(
    '<summary>Tool use: **shell**</summary>',
    ['**Input:**', '', "- command: `[bash -lc python - <<'PY'", "with open('docs/plan.md') as f:", '    print(f.read())', 'PY]`',
     '- timeout_ms: `120000`', '**Result:**', '```', 'ok', '```'], 10)
  assert.deepEqual(cmds.map(c => c.cmd), ["bash -lc python - <<'PY'"])
  assert.deepEqual(headsFrom(cmds[0].cmd), ['python'])              // unwraps to the real interpreter
})

test('parseSessionFile: LEGACY codex "Tool use: **shell**" with Output: marker captures commands', () => {
  const text = [
    '## 2025-10-26 16:21:00Z', '',
    '<!-- Codex CLI Session 66666666-aaaa-bbbb-cccc-000000000009 (2025-10-26 16:21:00Z) -->', '',
    '_**User**_', '', 'tail the build log', '', '---', '',
    '_**Agent (gpt-5-codex)**_', '',
    'Tool use: **shell**', '',
    '`bash -lc rg -n "NewIntegrator" -g\'*.go\'`', '',
    'Output:', '```', 'pkg/beat/integration.go:123: hit', '```', '',
  ].join('\n')
  const { agent, beats } = parseSessionFile(text)
  assert.equal(agent, 'codex-cli')
  assert.deepEqual(beats[0].cmds.map(c => c.head), ['rg'].filter(() => false).concat([]))  // rg is NOISE → dropped
  // use a non-noise command to assert capture end-to-end
  const text2 = text.replace('rg -n "NewIntegrator" -g\'*.go\'', 'go vet ./pkg/...')
  const r2 = parseSessionFile(text2)
  assert.deepEqual(r2.beats[0].cmds.map(c => c.head), ['go vet'])
})

// ---------- self-reporting: status, runs journal, theme render ----------

test('status + runs journal + theme render: the skill accounts for what it has done', () => {
  const dbPath = join(tmpdir(), `lore-status-${process.pid}.db`)
  rmSync(dbPath, { force: true })
  try {
    const run = (...args) => execFileSync('node', ['scripts/mine-skills.mjs', ...args, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    run('index', '--projects', 'fixtures')
    run('runs', 'add', '--summary', 'test run: mined fixtures', '--project', 'projA')
    const keys = JSON.parse(run('beats', '--gram', 'go build ▸ go test', '--max', '4')).beats.map(b => b.key)
    execFileSync('node', ['scripts/mine-skills.mjs', 'theme', 'put', '--file', '-', '--db', dbPath],
      { cwd: ROOT, input: JSON.stringify({ id: 't1', title: 'T', description: 'D', beatKeys: keys, evidence: [{ key: keys[0], quote: 'q' }] }), encoding: 'utf8' })
    const st = run('status')
    assert.match(st, /PASS-THROUGH STATUS/)
    assert.match(st, /CORPUS/); assert.match(st, /MINED\s+🏺 1 themes/); assert.match(st, /test run: mined fixtures/)
    const tr = run('theme', 'render')
    assert.match(tr, /=== themes above: 1 ===/)
    assert.match(tr, /evidence unchanged/)
    const runs = JSON.parse(run('runs', 'list'))
    assert.ok(runs.some(r => r.cmd === 'index') && runs.some(r => r.cmd === 'lore-run') && runs.some(r => r.cmd === 'theme'))
  } finally {
    rmSync(dbPath, { force: true }); rmSync(dbPath + '-wal', { force: true }); rmSync(dbPath + '-shm', { force: true })
  }
})
