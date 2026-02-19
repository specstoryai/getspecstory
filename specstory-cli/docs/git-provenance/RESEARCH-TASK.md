# AI Git Provenance Research Task

## Background

The goal of "AI agent code provenance" is to provide "AI Blame". AI blame means that for a specific line of code, or for all the lines of code in a specific file the origin of that line of code is known, and the following information about the origin is known:

- When the line of code was made/modified
- The line of code came from an AI coding agent or a manual change by a human
- When the line of code came from an AI coding agent, ideally all of the following information is available:
  - The human prompt that led to the AI coding agent making the change
  - The tool use that made the change
  - The agent response/commentary that led up to the tool use
  - The agent response/commentary that followed the tool use

The SpecStory CLI concurrently observes file system changes and AI terminal coding agent exhaust (activity) and correlates them together to provide AI agent code provenance. That provenance is stored in a local only SQLite DB, but to be truly useful it needs to be available to everyone, everywhere. 

One obvious answer to making the provenance available everywhere is to store it in git. In theory this could be in the same repository along with the code, or in a dedicated shadow repository.

- Ubiquitous existing system of record for code
- Ubiquitous tool and ecosystem support
- Distributed system
- Already provides "git blame" to answer the origin of any given line of code

This research task is to explore the various ways git can be used to store and present AI provenance.

## 7 Challenges to Explore

For any system to provide AI agent code provenance using git, the following challenges need a solution. Each AI provenance system may provide entirely or subtly different solutions to these challenges, leading to differences in outcomes. These differences provides trade-offs and pros and cons of each approach.

Understanding the different solutions in use today, and the trade-offs and pros and cons of each solution is the FOCUS of this research task.

### Capturing Agent Activity

The SpecStory CLI captures terminal agent change using an SPI/provider mechanism. There is a small SPI and a provider per agent. Each agent provider is open to do whatever is needed to provide standardized SessionData events each time there is agent activity (user message, agent message, tool use, etc.) in the coding agent. 

Most of the SpecStory CLI providers use fsnotify to observe a location where the coding agent is storing data for the session as JSON or JSONL. Some of them do the same for session data in SQLite DBs.

Agent coverage:

- Claude Code
- Codex CLI
- Gemini CLI
- Cursor CLI
- Factory Droid CLI

In progress:

- OpenCode
- Cursor VSC IDE
- Copilot VSC IDE
- Amp

The focus of this research is to understand what coding agents are supported and how the system knows that the coding agent did anything. When it did it. What the user and agent said.

The SpecStory CLI relies on its SPI/providers architecture and fsnotify. Other solutions may be similar and others will rely on coding agent hooks (callbacks).

### Capturing File Change & Micro-versioning

The 2nd piece of AI provenance is knowing what files changed. In an important sense, an AI provenance system backstopped by git gets this for "free" as the git system always understands files that are unchanged, staged and unstaged and the files that change in a commit and how they changed. So an AI provenance system could simply rely on git completely for understanding changes to files.

A challenge is that an agent may edit a file in response to a user prompt, edit it again later in response to a different user prompt, then edit it yet again in the agent loop in response to its own findings or thinking, and only then is this file finally committed. When committed git flattens this to just a single change from the file's prior state to the changed state. The fact that there were 3 different edits by the agent at different times, made in response to 2 different prompts and 3 different circumstances could be lost if the system is not solving this challenge in some way. 

One solution to this may be some sort of micro-versioning in between commits. Another may be to "punt" on this and leave it to the user to sort out how and why the file changed by reading the agent session. In theory an LLM could be used to read the session and associate the editing tool calls with the specific changed lines, though this solution may be slower and more expensive.

### Correlating Agent Change to File Change

Once a system has a handle on what files are changing and what agent activity is happening, some answer is needed to how the former is correlated to the latter. For example, if there are 2 concurrent agent sessions going on in the same local git repository, tackling unrelated goals, how do the file system changes get correlated to the correct agent activity to attribute the changes to the right AI provenance. 

Some agent activity is clearly directed at a specific file, such as when a coding agent uses a "write" or "edit" tool on a file. The bigger challenge here is when an agent uses more generic tools, often characterized as "shell" or "bash" tools. These can be straight forward commands, e.g. `echo "foo" > foo.txt` but they can also be complicated and even programmatic uses of shell tools, some of which may reference the file somewhere in the command (e.g. `sed`) and others which don't (e.g. `rails generate model Car`).

### Representing Agent Provenance

Once the agent activity has successfully been correlated to changes in file(s), the next step is to represent that linkage in a way that will be useful later for humans and agents. Each system will have different representation(s) for this data, they will include different data in the representation, and some standards may be proposed or possible are emerging to have some momentum.

Any good representation should be able support answering the question that for this change in this line of code, did an agent do it, what did the agent say about what it was doing, and what did the user prompt to make the agent perform this change.

### Storing Agent Provenance in Git

Once the correlation has been represented it needs stored in git so it can be safely stored, shared among team members, and used in AI "blame". There may be various approaches, such as use of git objects, git notes, and git commit messages, among others.

An important challenge is the extensive branching that happens in get repositories. Any AI provenance system needs a solution so that AI provenance survives as code is branched, merged, cherry-picked etc. across the version control life-cycle.

### AI Blame

Presumably each system will rely on `git blame` as a backstop, and once `git blame` identifies a specific commit as the origin of a line, it will supplement the line with additional AI provenance associated/linked with the commit. The details of how each system implements an "AI blame" (and even if they do), will differ.

Beyond basic line and file AI blame at the CLI or web level, there may be higher-level git and GitHub structures that are able to benefit from "AI blame" with the system. This could be things such as full commits, branches and PRs. These may benefit from AI blame as a complete unit, if possible in the system.

### Developer Experience (DX)

AI provenance is a "nice-to-have" but not "must-have". As long as the coding agent is efficiently and effectively coding in response to user prompts, capturing the linkage for historical purposes is nice, maybe even important, but it's not critical to having functioning software. Because of this, the developer experience of any AI provenance system can't be too onerous or require extensive diligence or changes to long worn habits.

What one-time setup is required, and how involved is it? What does a developer need to remember to do so that AI provenance is captured? What existing habits of use in commands/tools/UIs need changed to use the AI system? What new commands/tools/UI does a developer need to learn and how simple or complex are they? What mental model does the system suggest, and how well does this map on to the developer's existing mental model?

## 10 Existing Options to Investigate

First, for each of these 10 existing AI provenance systems, create 1 markdown document per system in `./docs/git-provenance/systems`. Each document must provide a complete description of exactly how the system works in practice, how it deals with each of the 7 enumerated challenges, and what its current activity level and level of interest/support/community are.

Next, for each of the 7 enumerated challenges, create 1 markdown document per challenge in `./docs/git-provenance/challenges`. Each document provides a description of all the different ways the challenge is addressed by the 9 existing systems, and importantly, how these various solutions compare to each other and the pros and cons of each.

Then, create a summary document as `./docs/git-provenance/SUMMARY.md` that focuses on summary tables comparing the 7 systems and the 7 challenge areas. Describe the big picture differences in approaches, the most important trade-offs and pros and cons. This should act as a good "executive summary" / tl;dr of the full research output.

Finally, prepare a single recommendations document as `./docs/git-provenance/RECOMMENDATIONS.md`

## Research Quality Bar (must follow)

The output should optimize for **mechanics clarity**, not just source collection. Evidence is necessary, but explanation quality is the deciding factor.

### Per-system document requirements

- Stay focused on what the system **actually does today** (as implemented/documented), not what it could do in theory.
- For each of the 7 challenges, include a concrete "how it works" explanation that walks through the mechanism step-by-step.
- Do not rely on a large evidence table to carry the section. Evidence tables support the section; they do not replace the explanation.
- Clearly label what is directly observed in code/docs vs what is inferred.

### Challenge-specific requirements

- **Challenge 1 (Capturing Agent Activity)** must explicitly list supported agents/providers for that system, and note confidence level/source (docs vs code vs both). This is foundational.
- **Challenge 4 (Representing Agent Provenance)** must include one or more sample representations:
  - Prefer real examples from docs/repo.
  - If real examples are unavailable, include generated examples clearly marked as synthetic but faithful to observed schema/behavior.
  - Explain how each important field in the sample is used later (especially for blame or provenance lookup).

### Tables / diagrams requirements

- Delivery cadence should be an actual release table (row-per-release), not a one-row summary.
- Use this minimum cadence table shape: `Repo | Version | Published (UTC) | Channel`.
- Mermaid diagrams must be valid and parse cleanly in common Mermaid renderers (avoid parser-fragile label syntax).

### Git AI

[Git AI](https://usegitai.com/) is on open source project.

Docs are [here](https://usegitai.com/docs/cli).

Code is [here](https://github.com/git-ai-project/git-ai) and [here](https://github.com/git-ai-project/action).

### Entire

[Entire](https://entire.io/) is a company and open source project.

[Docs](https://docs.entire.io/) are here.

Code is [here](https://github.com/entireio/cli) and [here](https://github.com/entireio/test-repo)

### cnotes

[cnotes](https://github.com/imjasonh/cnotes/) is an open source project.

### agentblame

[Agent Blame](https://github.com/mesa-dot-dev/agentblame) is an open source project.

### Git With Intent

[Git With Intent](https://github.com/intent-solutions-io/iam-git-with-intent) is an open source project.

### Agent Trace

[Agent Trace](https://agent-trace.dev/) is a proposal from Cursor.

"As agents write more code, it's important to understand what came from AI versus humans. This attribution is both the models used as well as the related agent conversations. Agent Trace defines an open, interoperable standard for recording this attribution data."

This is likely NOT a full AI provenance system, but likely only addresses the "Representing Agent Provenance" challenge (or maybe even a subset of that challenge).

Even though this is not a full system, Cursor is an important player in the field, and any attempt by them to standardize even a portion of the overall challenge of AI provenance deserves scrutiny and consideration.

### Intent Git Mode

Intent is our version control system. It uses this project (SpecStory CLI) as a Go lang library that provides agent activity to its AI provenance system. Right now Intent does its own file change to agent activity correlation, but that's going to move to all AI provenance provided by the SpecStory Go lang library.

Today Intent writes AI provenance into its file versioning CRDT's as provenance marks, which is how it solves the micro-versioning challenge above. Every file change that hits the file system is automatically micro-versioned.

This is our system, and we understand how it solves that part of the challenge well. The most important research to extract from Intent is its "Git Mode", where it puts the AI provenance in git notes, in addition to in the CRDTs. And folks use special intent commands to do git commits and pushes so the notes get written and pushed to the git repository.

The code is available here `/Users/sean/source/SpecStory/intent`.

### Wild Cards

What systems for AI provenance exist that aren't accounted for in these 9? Find them and present them to me. 

What efforts to standardize AI provenance (or even code provenance) exist that aren't accounted for in these 9? Find them and present them to me.
