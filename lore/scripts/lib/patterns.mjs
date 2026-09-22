// patterns.mjs - THE FORMAT GRAMMAR.
//
// Every regex below is preceded by a verbatim example of the transcript bytes it matches.
// This file is the single source of truth for what SpecStory transcripts look like, across
// providers and eras; parse.mjs consumes these names and contains no inline format knowledge.
//
// Eras (verified against specstory-cli source + a 1,311-session real corpus):
//   MODERN  (Markdown v2.x, ~Oct 2025+): tool calls wrapped in <tool-use> HTML envelopes.
//   LEGACY  (~Jun–Oct 2025):             bare "Tool use: **Name**" lines, no envelope.
// Both eras share the session header and the _**User/Agent**_ turn markers.

// ───────────────────────────── 1. SESSION ENVELOPE ─────────────────────────────

// <!-- Claude Code Session ec78670b-092f-44b2-86ce-d7a05e11f4bd (2026-02-17 18:26:27Z) -->
// <!-- Codex CLI Session 0199d491-acf0-7d51-9384-85984d07bdb1 (2025-10-11T18:39:00.871Z) -->
// <!-- cursor Session 33333333-... -->                  (provider names vary, incl. lowercase)
// Capture 1 = provider name (slugified into the agent tag), capture 2 = session UUID.
export const SESSION_HDR = /<!--\s*([A-Za-z][A-Za-z0-9 .+-]*?)\s+Session\s+([0-9a-fA-F-]+)/

// 2026-05-01_10-00-00Z-can-we-run-a.md   → "2026-05-01" (session date comes from the filename)
export const FILE_DATE = /(\d{4}-\d{2}-\d{2})/

// ───────────────────────────── 2. TURN MARKERS ─────────────────────────────

// _**User (2026-05-01 10:00:00Z)**_      (modern: timestamped)
// _**User**_                              (legacy: bare)
export const USER_MARK = /^_\*\*User\b/

// _**Agent (claude-opus-4-6 2026-05-01 10:00:05Z)**_
// _**Agent - sidechain (claude-haiku-... )**_          (subagent turns carry " - sidechain")
export const TURN_MARK = /^_\*\*(?:User|Agent)\b/

// ───────────────────── 3. TOOL BLOCKS - MODERN ENVELOPE ─────────────────────

// <tool-use data-tool-type="shell" data-tool-name="Bash"><details>
// Capture 1 = type (each provider's own classifier: shell/read/write/search/task/generic/unknown),
// capture 2 = literal tool name. Type "shell" is the primary executed-command signal.
export const TOOLUSE_OPEN = /<tool-use\b[^>]*\bdata-tool-type="([^"]*)"[^>]*\bdata-tool-name="([^"]+)"/

// Tool names treated as command executors even when the type attribute is not "shell"
// (Codex output-poll blocks render exec_command with type="unknown" - they carry exit codes).
export const SHELL_TOOLS = new Set(['Bash', 'exec_command', 'shell_command', 'shell'])

// type="shell" tools that are NOT command executors - scanning them would only add noise:
//   LS / list_directory   directory listings (Cursor, Gemini)
//   TaskOutput, KillShell, BashOutput   Claude background-task plumbing (fetch output / kill)
export const SHELL_EXCLUDE = new Set(['LS', 'list_directory', 'TaskOutput', 'KillShell', 'BashOutput'])

// WHERE THE COMMAND LIVES inside a shell <tool-use> block - four verified locations:

// (a) Codex single-line: inside the <summary> itself.
//     <summary>Tool use: **exec_command** `git status --short`</summary>
export const SUMMARY_CMD = /Tool use:\s*\*\*[^*]+\*\*\s*`([^`]+)`/

// (b) Claude single-line: an inline-backtick body line (NO fence).
//     `go build ./...`
export const INLINE_CMD = /^`([^`].*?)`$/

// (c) Multi-line: a fenced block whose language is a shell.
//     ```bash
//     go test ./...
//     golangci-lint run
//     ```
export const FENCE_LINE = /^```([a-zA-Z0-9_-]*)\s*$/
export const SHELL_FENCE_LANGS = new Set(['bash', 'sh', 'shell', 'zsh'])
// Heredoc bodies inside those fences are content, not commands:
//     git commit -m "$(cat <<'EOF'   …skip until the line that is exactly EOF…
export const HEREDOC_OPEN = /<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?/

// (d) Codex envelope "shell" (generic key-value render): a "- command:" bullet.
//     Single-line:  - command: `[bash -lc go vet ./pkg/...]`
//     MULTI-LINE (the backtick closes lines later - real corpus, 2,313 occurrences):
//       - command: `[bash -lc python - <<'PY'
//       with open('docs/plan.md') as f:
//           print(f.read())
//       PY]`
export const BULLET_CMD = /^- command:\s*`(.+?)`\s*$/
export const BULLET_CMD_OPEN = /^- command:\s*`(.+)$/      // opener when the close-backtick is absent
export const BULLET_CMD_CLOSE = /\]?`\s*$/                  // ...]` or ...` terminates the bullet

// Tool OUTPUT containers (never commands; scanned only for failure signals):
//     ```text            (Claude stdout fence)        ```            (Codex plain output fence)
//     Process exited with code 1                      Error: / fatal: / panic: / FAIL / npm ERR!
export const EXIT_CODE = /exited with code (\d+)/
export const ERROR_HEAD = /^(Error|error:|fatal:|panic:|FAIL\b|npm ERR!)/

// ───────────────────── 4. TOOL BLOCKS - LEGACY (~2025) ─────────────────────

// Tool use: **Bash** Remove xcuserdata from git tracking     (Claude legacy)
// Tool use: **shell**                                        (Codex legacy - 6,787 in real corpus)
// Tool use: **Read** `./BearApp/.gitignore`
export const LEGACY_TOOL = /^Tool use: \*\*([A-Za-z_]+)\*\*(.*)$/

// Legacy names whose following lines carry an executed command:
//     Tool use: **shell**
//
//     `bash -lc rg -n "NewIntegrator" -g'*.go'`        ← INLINE_CMD form, or a bash fence
//
//     Output:                                           ← Codex says "Output:", Claude "Result:"
export const LEGACY_SHELL_NAMES = new Set(['Bash', 'shell', 'shell_command', 'exec_command'])
export const LEGACY_OUTPUT_MARK = /^(Result|Output):/

// Legacy tool name → tool-type (modern providers classify for us; legacy we map ourselves).
export const LEGACY_TYPE = { Bash: 'shell', shell: 'shell', shell_command: 'shell', exec_command: 'shell', Read: 'read', WebFetch: 'read', Write: 'write', Edit: 'write', MultiEdit: 'write', NotebookEdit: 'write', Grep: 'search', Glob: 'search', WebSearch: 'search', TodoWrite: 'task', LS: 'shell' }

// ───────────────────── 5. COMMAND NORMALIZATION VOCABULARIES ─────────────────────

// Leading imperative verbs recognized as the start of a task intent ("can we RUN a BUILD…").
export const VERBS = new Set(['evaluate', 'add', 'fix', 'create', 'write', 'refactor', 'review', 'implement',
  'build', 'update', 'remove', 'delete', 'migrate', 'release', 'debug', 'investigate', 'analyze',
  'analyse', 'test', 'document', 'trace', 'verify', 'audit', 'optimize', 'optimise', 'rename', 'wire',
  'integrate', 'generate', 'extract', 'design', 'plan', 'set', 'configure', 'deploy', 'check', 'make',
  'port', 'scaffold', 'rewrite', 'clean', 'split', 'merge', 'diagnose', 'inspect', 'summarize'])

// Command heads treated as recon/file-op noise - dropped from procedures. Everything NOT here
// is kept, including project-specific tools (stoa, supabase, xcodebuild, ./scripts/run.sh, …).
export const NOISE = new Set(['cd', 'ls', 'cat', 'echo', 'pwd', 'grep', 'rg', 'egrep', 'fgrep', 'sed', 'awk',
  'head', 'tail', 'find', 'rm', 'cp', 'mv', 'mkdir', 'touch', 'export', 'source', 'which', 'env',
  'sleep', 'true', 'false', 'chmod', 'chown', 'wc', 'sort', 'uniq', 'xargs', 'cut', 'tee', 'printf',
  'set', 'read', 'test', 'time', 'sudo', 'open', 'code', 'jq', 'tr', 'basename', 'dirname', 'realpath',
  'date', 'kill', 'ps', 'df', 'du', 'tar', 'unzip', 'zip', 'curl', 'wget', 'nc', 'ssh', 'scp', 'watch',
  'clear', 'exit', 'nano', 'vim', 'vi', 'less', 'more', 'man', 'type', 'history', 'alias', 'cls',
  'nl', 'column', 'fold', 'expand', 'xxd', 'od', 'strings', 'hexdump', 'comm', 'diff', 'paste',
  // shell control-flow keywords leak as heads from multi-line loops ("do echo ▸ done")
  'do', 'done', 'then', 'fi', 'else', 'elif', 'esac', 'while', 'for', 'until', 'case', 'in', 'function'])

// Mainstream tools - used to DOWN-weight (specificity term): everyone's git habit ≠ your skill.
export const COMMON = new Set(['git', 'npm', 'node', 'go', 'python', 'python3', 'docker', 'make', 'yarn', 'pnpm', 'npx', 'pip', 'pip3', 'bun'])

// Stopwords for intent tokenization.
export const STOP = new Set(['the', 'a', 'an', 'and', 'or', 'but', 'to', 'of', 'in', 'on', 'for', 'with', 'this',
  'that', 'these', 'those', 'it', 'its', 'is', 'are', 'was', 'were', 'be', 'been', 'i', 'you', 'we', 'me',
  'my', 'our', 'your', 'want', 'need', 'please', 'should', 'would', 'could', 'can', 'will', 'let', 'lets',
  'make', 'sure', 'like', 'just', 'now', 'then', 'all', 'any', 'some', 'into', 'from', 'about', 'how',
  'what', 'when', 'where', 'which', 'who', 'do', 'does', 'did', 'so', 'if', 'as', 'at', 'by', 'up', 'out',
  'not', 'no', 'yes', 'ok', 'okay', 'also', 'have', 'has', 'had', 'get', 'got', 'use', 'using', 'them',
  'they', 'their', 'there', 'here', 'one', 'two', 'thing', 'things', 'way', 'really', 'going'])

// ───────────────────── 6. BEHAVIOR CLASSIFIERS ─────────────────────

// Ways-of-working detectors, applied to the user's intent text.
export const META = [
  { id: 'read-only-diagnosis', label: 'Read-only diagnosis (no edits)', re: /\b(do not edit|don'?t edit|read[- ]only|without (?:editing|changing) (?:any )?files?)\b/i },
  { id: 'trace-code-paths', label: 'Trace the code paths before acting', re: /\b(trace (?:the )?code|code paths?|trace through)\b/i },
  { id: 'externalize-to-file', label: 'Write the result to a durable file', re: /\bwrite (?:it|this|that|the \w+)? ?to (?:a |the )?(?:file|@|docs|`)/i },
  { id: 'verification-demand', label: 'Demand proof it works', re: /\b(prove it works|are you sure|verify (?:that|it|this)|show me (?:the|it)|did you (?:actually|really))\b/i },
  { id: 'reasoning-dial', label: 'Dial up reasoning', re: /\b(ultrathink|think (?:hard|harder|deeply|step by step))\b/i },
  { id: 'as-built-doc', label: 'Produce/refresh an AS-BUILT map', re: /\bAS[- ]BUILT\b|as-built\.md/i },
  { id: 'goal-rider-spec', label: 'Brief work as a goal/rider spec', re: /\b(goal\+rider|goal doc|rider|acceptance criteria|self-grad)/i },
  { id: 'focus-files-allowlist', label: 'Constrain to focus files', re: /\b(focus files?|only (?:touch|edit|change) |allowlist|scope (?:to|is))\b/i },
]

// Outcome labels from the user's NEXT reply (free supervision):
//   "no wait, that's wrong…"  → the PRIOR beat is `corrected`
//   "perfect, commit this"    → the PRIOR beat is `success`
export const CORRECTED_RE = /^(no\b|nope\b|stop\b|wait\b|actually\b|don'?t\b|hmm+\b|that'?s (?:wrong|not)|still (?:broken|failing|wrong)|didn'?t work|not what)/i
export const SUCCESS_RE = /\b(write a commit|commit (?:this|it|that|all)|lets? commit|perfect|works now|looks? good|nice work|great work|ship it|that worked|love it)\b/i

export function tokenize(s) {
  return (s.toLowerCase().match(/[a-z][a-z0-9_-]{2,}/g) || []).filter(w => !STOP.has(w))
}
export function leadingVerb(words) {
  for (let i = 0; i < Math.min(words.length, 14); i++) if (VERBS.has(words[i])) return words[i]
  return null
}
export function classifyOutcome(nextIntentFirstLine) {
  if (!nextIntentFirstLine) return 'end'
  if (CORRECTED_RE.test(nextIntentFirstLine)) return 'corrected'
  if (SUCCESS_RE.test(nextIntentFirstLine)) return 'success'
  return 'neutral'
}

// ============================================================================
// SECTION 7 - SECRET REDACTION (the emit boundary)
// ============================================================================
// Transcripts can contain real credentials (a pasted curl -H "Authorization: Bearer ...",
// an exported API key). The corpus stores what the transcript stores - it is the user's own
// local file - but NOTHING the engine EMITS for an LLM to read (beat spans, dossier/theme/plan
// renders, report evidence) may carry a secret value. Redaction is deterministic and happens
// here, before display, so the agent never has to handle a live credential; the agent-side
// scrub at forge time (SKILL.md Step 5) remains as defense in depth.
//
// Patterns are PROVIDER-SHAPED prefixes plus an assignment heuristic. Deliberately NOT matched:
// bare 40/64-char hex (git SHAs are evidence, not secrets).
//
//   AKIAIOSFODNN7EXAMPLE                          AWS access key id
//   ghp_16C7e42F292c6912E7710c838347Ae178B4a      GitHub token (ghp_/gho_/ghu_/ghs_/ghr_)
//   github_pat_11ABC..._...                       GitHub fine-grained PAT
//   xoxb-1234567890-abcdefghij                    Slack token
//   sk-proj-abc123..., sk-ant-api03-...           OpenAI/Anthropic-style keys
//   AIzaSyA-1234567890abcdefghijklmnopqrstu       Google API key
//   eyJhbGciOi....eyJzdWIiOi....SflKxwRJSM        JWT (three base64url segments)
//   Authorization: Bearer <anything long>          bearer credentials
//   API_KEY=hunter2hunter2 / "token": "abc12345"  assignment with a secret-named key
//   -----BEGIN ... PRIVATE KEY----- ... -----END  key blocks
export const SECRET_PATTERNS = [
  { name: 'aws-key', re: /\bAKIA[0-9A-Z]{16}\b/g },
  { name: 'github-token', re: /\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b/g },
  { name: 'slack-token', re: /\bxox[baprs]-[A-Za-z0-9-]{10,}\b/g },
  { name: 'api-key', re: /\bsk-[A-Za-z0-9_-]{20,}\b/g },
  { name: 'google-key', re: /\bAIza[0-9A-Za-z_-]{35}\b/g },
  { name: 'jwt', re: /\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}\b/g },
  { name: 'private-key', re: /-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----/g },
  { name: 'bearer', re: /\b([Bb]earer\s+)[A-Za-z0-9._~+/=-]{16,}/g, keep: 1 },
  // key = value where the key NAME says secret; the value is masked, the structure kept.
  // Values containing ( or ) are code expressions ("token = tokenize(x)"), not credentials.
  { name: 'assignment', re: /\b((?:api[_-]?key|apikey|secret|token|passwd|password|credentials?|access[_-]?key|auth[_-]?token)["']?\s*[:=]\s*["']?)([^\s"'()]{8,})(?![\w(])/gi, keep: 1 },
]

// Mask secret VALUES in text bound for an LLM or chat; structure and surrounding evidence stay
// verbatim. Returns the redacted string.
export function redactSecrets(s) {
  if (!s) return s
  let out = s
  for (const p of SECRET_PATTERNS) {
    out = out.replace(p.re, (...m) => (p.keep ? m[p.keep] : '') + `[REDACTED:${p.name}]`)
  }
  return out
}
