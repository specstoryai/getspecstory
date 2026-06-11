export const meta = {
  name: 'lore-deep-mine',
  description: 'Phase C: one miner + one adversarial verifier per top corroborated cluster; each reads the exact beat spans (engine-exported) and produces a deep-skill dossier (method, verification, failure modes)',
  phases: [
    { title: 'Mine', detail: 'one agent per cluster reads stratified beat spans and drafts the dossier' },
    { title: 'Verify', detail: 'adversarial pass per dossier: one procedure or several? do failure modes trace to refs?' },
  ],
}

// args: { skillDir: string, db: string, clusters: [{ key: string, kind: 'corr'|'gram'|'sig'|'meta'|'theme', fingerprint: string }] }
const A = typeof args === 'string' ? JSON.parse(args) : (args || {})   // tolerate stringified args
const SKILL = A.skillDir
const DB = A.db
const CLUSTERS = A.clusters || []
if (!CLUSTERS.length) throw new Error('deep-mine: args.clusters is empty - pass [{key, kind, fingerprint}]')
const FLAG = { corr: '--corr', gram: '--gram', sig: '--sig', meta: '--meta', theme: '--theme' }

const DOSSIER_SCHEMA = {
  type: 'object', additionalProperties: false,
  required: ['cluster', 'name', 'trigger', 'steps', 'verification', 'failureModes', 'parameters', 'confidence', 'evidenceRefs'],
  properties: {
    cluster: { type: 'string' },
    name: { type: 'string', description: 'proposed kebab-case skill name' },
    trigger: { type: 'string', description: 'the "Use when ..." description line the forged skill would fire on' },
    preconditions: { type: 'array', items: { type: 'string' }, description: 'what must be true before this procedure applies' },
    steps: { type: 'array', items: { type: 'string' }, description: 'the canonical method, from the REAL commands across beats - not invented' },
    variations: { type: 'array', items: { type: 'string' }, description: 'observed deviations and when they apply (flags added, order changed)' },
    verification: { type: 'array', items: { type: 'string' }, description: 'how success was actually checked in the beats (commands run, outputs inspected)' },
    failureModes: { type: 'array', items: {
      type: 'object', additionalProperties: false, required: ['what', 'recovery', 'ref'],
      properties: { what: { type: 'string' }, recovery: { type: 'string', description: 'how the user/agent recovered' }, ref: { type: 'string', description: 'beat ref path:line this came from' } },
    }, description: 'mine the corrected beats hard - this section is what makes the skill deep' },
    parameters: { type: 'array', items: { type: 'string' }, description: 'what varies across beats (refs, schemes, paths) vs the invariant skeleton' },
    confidence: { type: 'string', enum: ['high', 'medium', 'low'] },
    evidenceRefs: { type: 'array', items: { type: 'string' }, description: 'path:line refs backing the dossier' },
  },
}

const VERDICT_SCHEMA = {
  type: 'object', additionalProperties: false,
  required: ['cluster', 'verdict', 'problems', 'fixes'],
  properties: {
    cluster: { type: 'string' },
    verdict: { type: 'string', enum: ['confirmed', 'needs-edits', 'refuted'] },
    problems: { type: 'array', items: { type: 'string' }, description: 'conflated procedures, unsupported claims, refs that do not say what is claimed' },
    fixes: { type: 'array', items: { type: 'string' }, description: 'concrete edits that would make the dossier accurate' },
  },
}

phase('Mine')

function exportCmd(c) {
  return `node "${SKILL}/scripts/mine-skills.mjs" beats ${FLAG[c.kind] || '--corr'} "${c.key}" --db "${DB}" --max 25`
}

const dossiers = (await parallel(CLUSTERS.map(c => () => agent(
  `You are a DEEP-MINE agent for SpecStory Lore. Your cluster: "${c.key}".

Run this command with Bash and read its JSON output - it contains the EXACT transcript spans
(user intent + agent method + outcome label) for every beat in this cluster, with all
failure ("corrected") beats included first:

  ${exportCmd(c)}

Read EVERY span. Then produce the dossier:
- steps: the canonical procedure as it actually ran (real commands, real order). Note variations.
- verification: what was actually checked to call it done (from the spans, not imagined).
- failureModes: mine the [corrected] beats hardest - what went wrong, how it was recovered,
  with the beat ref for each. This section is the difference between a runbook and a skill.
- parameters: what varies across beats vs what is invariant.
Ground every claim in the spans; if you cannot point to a ref, leave it out. Do not pad:
empty variations/preconditions are fine.`,
  { label: `mine:${c.key.slice(0, 40)}`, phase: 'Mine', schema: DOSSIER_SCHEMA }
)))).filter(Boolean)
log(`Mined ${dossiers.length}/${CLUSTERS.length} dossiers`)

phase('Verify')

const verdicts = (await parallel(dossiers.map(d => () => agent(
  `You are an ADVERSARIAL VERIFIER. A deep-mine agent produced this dossier for cluster "${d.cluster}":

${JSON.stringify(d, null, 2)}

Re-run the same export it used and check the spans yourself:

  ${exportCmd(CLUSTERS.find(c => c.key === d.cluster) || { kind: 'corr', key: d.cluster })}

Actively try to REFUTE the dossier: Is this genuinely ONE procedure, or several conflated? Do the
failureModes' refs actually contain those failures and recoveries? Are the steps real commands from
the spans or invented? Is the trigger faithful to how the intent actually shows up? Return
needs-edits with concrete fixes when partially wrong; refuted only when the core claim is false.`,
  { label: `verify:${d.cluster.slice(0, 40)}`, phase: 'Verify', schema: VERDICT_SCHEMA }
)))).filter(Boolean)

const byCluster = Object.fromEntries(verdicts.map(v => [v.cluster, v]))
return {
  dossiers: dossiers.map(d => ({
    ...d,
    fingerprint: (CLUSTERS.find(c => c.key === d.cluster) || {}).fingerprint || '',
    verdict: byCluster[d.cluster]?.verdict || 'unverified',
    problems: byCluster[d.cluster]?.problems || [],
    fixes: byCluster[d.cluster]?.fixes || [],
  })),
}
