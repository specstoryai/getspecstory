<h1 align="center">📜 Lore</h1>

<p align="center"><strong>Your sessions are your lore. Forge them into skills.</strong></p>

<p align="center">
  <a href="https://github.com/specstoryai/getspecstory/actions/workflows/lore-validate.yml"><img src="https://github.com/specstoryai/getspecstory/actions/workflows/lore-validate.yml/badge.svg" alt="Lore Validate"></a>
  <a href="https://github.com/specstoryai/getspecstory/releases?q=lore&expanded=true"><img src="https://img.shields.io/github/v/tag/specstoryai/getspecstory?filter=lore%2Fv*&label=release" alt="Latest lore release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License: Apache-2.0"></a>
  <img src="https://img.shields.io/badge/dependencies-0-brightgreen" alt="Zero dependencies">
</p>

<p align="center">
Lore mines the SpecStory histories your coding agents leave behind  - <br>
finds the workflows you actually repeat, proves them with evidence and outcomes,<br>
and forges the ones you choose into skills installed across every agent you use.
</p>

---

## Install

Lore lives in the [getspecstory](https://github.com/specstoryai/getspecstory) monorepo, next to
the SpecStory CLI. One-liners for any harness (Node ≥ 22.5 either way):

```zsh
# any harness, via skills.sh (detects every agent on your machine and installs to each)
npx skills add specstoryai/getspecstory --skill lore

# Gemini CLI (native Agent Skills support)
gemini skills install https://github.com/specstoryai/getspecstory.git --path lore
```

```
# Claude Code, as a plugin (in-app)
/plugin marketplace add specstoryai/getspecstory
/plugin install lore@specstory
```

For development, install from a clone - every install point is a symlink back to it, so updating
is just `git -C ~/getspecstory pull`:

```zsh
# 1. clone the monorepo anywhere you keep code
git clone git@github.com:specstoryai/getspecstory.git ~/getspecstory

# 2. canonical skill location (Amp, Codex, and Gemini CLI read ~/.agents/skills natively)
mkdir -p ~/.agents/skills && ln -sfn ~/getspecstory/lore ~/.agents/skills/lore

# 3. fan out into every harness skills dir you have (symlink, never copy)
for h in ~/.claude/skills ~/.codex/skills ~/.config/opencode/skills; do
  [ -d "$h" ] && ln -sfn ~/.agents/skills/lore "$h/lore"
done
```

Releases: tags (`lore/vX.Y.Z`) run the test suite and publish a `lore.skill` artifact (the
upload-ready zip for claude.ai) via GitHub Actions - see [CHANGELOG.md](CHANGELOG.md) for what
shipped.

## Get started

In any agent that reads [Agent Skills](https://agentskills.io) (Claude Code, Codex, Amp, OpenCode, …):

```
/lore
```

(Invocation differs per harness - see
[Optimized for Claude Code, built for every harness](#optimized-for-claude-code-built-for-every-harness).)

Press Enter and it walks you through scope, time window, and goal - then mines, shows you
evidence-backed dossiers of your candidate skills, and presents a forge plan to approve. Or steer it
in plain English - there is no argument grammar to learn:

| Say | Get |
|---|---|
| *(just Enter)* | Guided setup, then the full pipeline. |
| `mine this project` | Mine this repo's history. |
| `across my projects in ~/code` | Cross-project mining: portable vs project-specific skills. |
| `last 30 days, just show candidates` | Dry run: evidence dossiers only, no forging. |
| `about supabase` / `only my judgment skills` | Narrow by topic, or by channel (themes vs runbooks). |
| `status` | What Lore has done: corpus, themes, forged skills, drift. |
| `what skills do I have?` | Inventory of every installed skill and what it does. |
| `show me the last plan` | Recall a canceled forge plan and resume. |
| `forge them all` | Batch forge after one confirmation. |
| `reset my lore` | Start fresh (asks first; forged skills stay installed). |

Everything accumulates in one file - `~/.specstory/lore.db` - incrementally and idempotently, across
all your projects and agents. Prefer the engine directly? It's one zero-dependency script - see
[Using the engine directly](HOW-IT-WORKS.md#using-the-engine-directly).

## What it finds

Lore's unit is the **beat**: your prompt (the intent), everything the agent did until your next
prompt (the method - real executed commands, files, exit codes), and your reply as a free outcome
label (approval ✓ or correction ✗). Patterns that recur across beats, sessions, projects, and
**teammates** become candidates; the strongest are **corroborated** - you asked for X and the agent
did Y, repeatedly, and it worked. And beyond commands, **theme mining** reads the conversational
beats - the reviews, decisions, and corrections - and surfaces the *latent* expertise you operate
without naming: how you review, how you diagnose, how you direct a model.

```
📜 Lore mined!
├─ 🗂  projects: stoa 1,243 · BearClaude 253
├─ 🧠 beats: 21,346 · ⚙️ executed commands: 36,474
├─ 🤖 agents: claude-code 1,375 · codex-cli 310 · cursor 6
├─ 👥 authors: Greg 1,243 · Sean 181 · Jake 72
├─ 🎯 outcomes: 744 ✓ approvals · 693 ✗ corrections
└─ 📦 your lore: ~/.specstory/lore.db
```

## Why it's different

- **Deterministic engine, judging agent.** A zero-dependency parser + SQLite engine does the
  counting; your agent does the naming and judging. The engine can't hallucinate, and the agent
  never reads 400k-line transcripts - only engine-exported evidence.
- **Outcomes from your own replies.** "No, wait - " marks a failure; "perfect, commit it" marks
  success. Skills are ranked by what actually worked, not what merely happened.
- **Latent expertise, not just runbooks.** Thematic lenses mine your judgment work - review craft,
  decision-making, model direction - into skills you didn't know you had ("huh, I *do* do that").
  Verified themes then **expand from anecdote to measurement**: the engine finds every corpus
  occurrence deterministically and reports the practice's **outcome lift** over your baseline.
- **Deep-mine with an adversary.** Top candidates get one subagent reading *every* beat
  (failures first) and a second trying to refute the result. Failure modes with recoveries are what
  make a forged skill deep instead of a runbook.
- **Team-aware.** Histories committed by teammates are attributed (git author → home-dir →
  machine user); a workflow several people share is a team skill, proposed for the repo - and never
  presented as yours when it isn't.
- **Forge once, install everywhere.** Skills land in `~/.agents/skills/<name>` and symlink into
  every harness. The registry remembers what was forged and declined, and proposes **updates** when
  the evidence grows - re-runs never duplicate.

<p align="center"><sub>Reads histories from every SpecStory provider: Claude Code · Codex · Cursor · Gemini · Factory Droid · DeepSeek · Antigravity - modern and legacy formats alike.</sub></p>

## Optimized for Claude Code, built for every harness

Lore is tuned for Claude Code: the two heavy phases ship as bundled **Workflow scripts** that fan
out subagents in parallel - deep mining runs one miner plus one adversarial verifier per candidate
cluster, theme sweeping runs one miner per thematic lens - and runs are resumable if interrupted.
Setup is a guided three-question start, and curation is presented as a **plan you approve** before
anything is written. The plan itself is engine-assembled and **hook-enforced**: a `PreToolUse` hook
bundled with the skill rejects any curation plan that does not embed the full evidence verbatim, so
you can never approve a forge you have not seen.

The skill is still harness-portable by design. The deterministic core (the parser, the corpus, the
registry) is a zero-dependency Node script that behaves identically everywhere, and the skill
contract degrades gracefully: harnesses with their own subagent mechanism fan out the same miner
and verifier briefs; harnesses without subagents mine the clusters sequentially with the agent
reading the engine's evidence exports directly. Forged skills are plain markdown either way, usable
from any agent.

After [installing](#install), invoke it per harness:

| Harness | Invocation |
|---|---|
| Claude Code | `/lore` |
| Codex | `$lore` (the `/` prefix is reserved for built-ins; also listed under `/skills`) |
| Amp | reads `~/.agents/skills` natively - just ask, e.g. "mine my lore" |
| Gemini CLI | reads `~/.agents/skills` natively - just ask; the model activates the skill (with your consent) |
| OpenCode | skills load automatically - just ask, e.g. "what could I forge into skills?" |

## Documentation

| Doc | What's in it |
|---|---|
| [HOW-IT-WORKS.md](HOW-IT-WORKS.md) | The narrative walkthrough - every pipeline stage, with diagrams. |
| [SKILL.md](SKILL.md) | The agent contract: the pipeline, the output-contract LAWs, forge templates. |
| [AS-BUILT-ARCHITECTURE.md](AS-BUILT-ARCHITECTURE.md) | Full technical architecture: schema, parsing rules, scoring, deep-mine, roadmap. |
| [CHANGELOG.md](CHANGELOG.md) | What's shipped, version by version. |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Dev setup, the two non-negotiable rules, fixtures-are-the-spec, releases. |

Licensed under [Apache-2.0](LICENSE).

```zsh
npm test    # 30 tests; the fixtures are the executable spec of every transcript format
```
