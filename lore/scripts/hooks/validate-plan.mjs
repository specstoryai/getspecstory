#!/usr/bin/env node
// validate-plan.mjs - PreToolUse hook on ExitPlanMode, wired in SKILL.md frontmatter.
//
// This is LAW 1's harness-level enforcement for plan-style curation (failure mode #3: a plan that
// names skills but summarizes the dossier evidence away, so the user approves a forge they never
// saw). Instructions ask; hooks enforce. The plan body must BE the engine's `plan render` artifact:
//   - the PASS-THROUGH FORGE PLAN marker (only the engine emits it)
//   - the "=== dossiers above: N ===" sentinel as the plan's LAST line
//   - at least N "### " card headers backing the sentinel's count
// Anything else is denied with a reason the model can act on. A malformed stdin or a different
// tool exits 0 silently - a broken hook must never block unrelated work.

import { readFileSync } from 'node:fs'

let input
try { input = JSON.parse(readFileSync(0, 'utf8')) } catch { process.exit(0) }
if (input.tool_name !== 'ExitPlanMode') process.exit(0)

const plan = String(input.tool_input?.plan || '')
const problems = []

if (!plan.includes('<!-- PASS-THROUGH FORGE PLAN')) {
  problems.push('the plan is not the engine-rendered forge plan (missing the PASS-THROUGH FORGE PLAN marker)')
}
const sentinel = plan.match(/^=== dossiers above: (\d+) ===\s*$/m)
if (!sentinel) {
  problems.push('missing the "=== dossiers above: N ===" sentinel')
} else {
  const lastLine = plan.trimEnd().split('\n').pop().trim()
  if (!/^=== dossiers above: \d+ ===$/.test(lastLine)) problems.push('the sentinel must be the LAST line of the plan')
  const n = +sentinel[1]
  const cards = (plan.match(/^### /gm) || []).length
  if (n < 1) problems.push('the sentinel must count at least 1 candidate')
  if (cards < n) problems.push(`the sentinel claims ${n} candidate(s) but only ${cards} dossier card header(s) ("### ") are present`)
}

if (problems.length) {
  process.stdout.write(JSON.stringify({
    hookSpecificOutput: {
      hookEventName: 'PreToolUse',
      permissionDecision: 'deny',
      permissionDecisionReason:
        'LAW 1 (lore, failure mode #3): the forge plan must be the engine-rendered curation document, '
        + 'not your own summary. Problems: ' + problems.join('; ') + '. '
        + 'Fix: write the manifest JSON ({proposed:[{cluster|theme, name}], skipped:[{candidate, reason}]}), run '
        + '`node "<skill-dir>/scripts/mine-skills.mjs" plan render --file <manifest>`, and pass its stdout '
        + 'UNEDITED as the plan. If this plan is genuinely not lore curation, tell the user the lore hook blocked it.',
    },
  }))
}
process.exit(0)
