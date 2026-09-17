
<img width="1649" height="158" alt="Group 6 (1)" src="https://github.com/user-attachments/assets/93f0210f-c3ce-4035-91df-ec597e00a3ce" />


# Intent is the new source code

**Turn your AI development conversations into searchable, shareable knowledge.**

Never lose a brilliant solution, code snippet, or architectural decision again. SpecStory captures, indexes, and makes searchable every interaction you have with AI coding assistants across all your projects and tools.

<p align="left">
  <strong>Install SpecStory ──▶ </strong>&nbsp;
  <a href="https://specstory.com"><img src="https://img.shields.io/endpoint?url=https%3A%2F%2Fspecstory.com%2Fapi%2Fbadge%3Fstat%3Dinstalls&style=flat-square" alt="Installs" style="vertical-align: middle;"></a>
  <a href="https://specstory.com"><img src="https://img.shields.io/endpoint?url=https%3A%2F%2Fspecstory.com%2Fapi%2Fbadge%3Fstat%3DactiveUsers&style=flat-square" alt="Active Users" style="vertical-align: middle;"></a>
  <a href="https://specstory.com"><img src="https://img.shields.io/endpoint?url=https%3A%2F%2Fspecstory.com%2Fapi%2Fbadge%3Fstat%3DsessionsSaved&style=flat-square" alt="Sessions Saved" style="vertical-align: middle;"></a>
</p>

<p align="left">
  <strong>Contribute to OSS ──▶</strong>&nbsp;
  <a href="https://github.com/specstoryai/getspecstory/tree/main/specstory-cli"><img src="https://img.shields.io/badge/CLI-Open%20Source-brightgreen?style=flat-square" alt="CLI Open Source" style="vertical-align: middle;"></a>
  <a href="./lore"><img src="https://img.shields.io/badge/Lore-Forge%20Skills-brightgreen?style=flat-square" alt="Lore: forge skills from your sessions" style="vertical-align: middle;"></a>
</p>

<p align="left">
  <strong>Connect with us ───▶</strong>&nbsp;
  <a href="https://twitter.com/specstoryai"><img src="https://img.shields.io/badge/X-000000?style=flat-square&logoColor=white" alt="X" style="vertical-align: middle;"></a>
  <a href="https://www.linkedin.com/company/specstory"><img src="https://img.shields.io/badge/LinkedIn-0077B5?style=flat-square&logo=linkedin&logoColor=white" alt="LinkedIn" style="vertical-align: middle;"></a>
  <a href="https://specstory.slack.com/join/shared_invite/zt-2vq0274ck-MYS39rgOpDSmgfE1IeK9gg#/shared-invite/email"><img src="https://img.shields.io/badge/Slack-4A154B?style=flat-square&logo=slack&logoColor=white" alt="Slack" style="vertical-align: middle;"></a>
  <a href="https://discord.gg/E47yQyEUd3"><img src="https://img.shields.io/badge/Discord-5865F2?style=flat-square&logo=discord&logoColor=white" alt="Discord" style="vertical-align: middle;"></a>
  <a href="https://www.youtube.com/@specstory"><img src="https://img.shields.io/badge/YouTube-FF0000?style=flat-square&logo=youtube&logoColor=white" alt="YouTube" style="vertical-align: middle;"></a>
</p>

## How It Works
```
AI Coding Tools              Local First                  Cloud Platform
─────────────────           ─────────────                ─────────────────
                                                          (Login Required)
Cursor IDE         ┐
Copilot IDE        │
Claude Code CLI    │
Cursor CLI         │
Codex CLI          ├──────►  .specstory/history/  ──────►  cloud.specstory.com
Droid CLI          │          (Auto-Saved Locally)        (Search, Ask & Share)
DeepSeek TUI       │
Antigravity CLI    │
Grok Build         │
Muse Code          │
Pi                 │
Qwen Code          ┘
```

## Workflow

1. **Capture** - SpecStory CLI and IDE extensions save every AI interaction locally to `.specstory/history/`
2. **Sync (Optional)** - Sync your local sessions to SpecStory Cloud for safekeeping and team sharing
3. **Search** - Find conversations across all projects locally, or across multiple computers and team members in SpecStory Cloud
4. **Chat (Optional)** - Ask questions across your sessions in SpecStory Cloud to revisit decisions, understand past work, and find solutions
5. **Share** - Export and share specific solutions with your team
6. **Resume** - Resume sessions locally between different agents, and between multiple computers and team members with SpecStory Cloud
7. **Process** - Run [`/lore`](./lore) to mine your history into reusable, evidence-backed agent skills

## Supported Development Tools

SpecStory integrates seamlessly with your favorite AI coding tools, automatically saving all conversations locally to `.specstory/history/` in your project. **Everything is local-first** - your data stays on your machine unless you choose to sync to SpecStory Cloud.

### Installation

| Supported Agent                                                               | Agent Type | Min Version  | SpecStory Product                                                | Source Code                                                                                             | Installation                                                  |
| ----------------------------------------------------------------------------- | ---------- | ------------ | ---------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| [Cursor AI](https://www.cursor.com/)                                          | IDE        | v0.43.6+     | **[Cursor Extension](https://www.cursor.com/)**                  | Closed                                                                                                  | Search "SpecStory" in Extensions (Cmd/Ctrl+Shift+X) → Install |
| [GitHub Copilot](https://github.com/features/copilot)                         | IDE        | v1.300.0+    | **[VSC Copilot Extension](https://github.com/features/copilot)** | Closed                                                                                                  | Search "SpecStory" in Extensions (Cmd/Ctrl+Shift+X) → Install |
| [Claude Code](https://claude.ai/claude-code)                                  | CLI        | v1.0.27+     | **[SpecStory CLI](https://specstory.com/specstory-cli)**         | [Open](https://github.com/specstoryai/getspecstory/tree/dev/specstory-cli/pkg/providers/claudecode)     | `brew tap specstoryai/tap`<br/>`brew install specstory`       |
| [Codex CLI](https://www.openai.com/codex)                                     | CLI        | v0.42.0+     | **[SpecStory CLI](https://specstory.com/specstory-cli)**         | [Open](https://github.com/specstoryai/getspecstory/tree/dev/specstory-cli/pkg/providers/codexcli)       | `brew tap specstoryai/tap`<br/>`brew install specstory`       |
| [Cursor CLI](https://cursor.com/cli)                                          | CLI        | v2025.09.18+ | **[SpecStory CLI](https://specstory.com/specstory-cli)**         | [Open](https://github.com/specstoryai/getspecstory/tree/dev/specstory-cli/pkg/providers/cursorcli)      | `brew tap specstoryai/tap`<br/>`brew install specstory`       |
| [Droid CLI](https://factory.ai/product/cli)                                   | CLI        | v0.56.3+     | **[SpecStory CLI](https://specstory.com/specstory-cli)**         | [Open](https://github.com/specstoryai/getspecstory/tree/dev/specstory-cli/pkg/providers/droidcli)       | `brew tap specstoryai/tap`<br/>`brew install specstory`       |
| [Gemini CLI](https://docs.cloud.google.com/gemini/docs/codeassist/gemini-cli) | CLI        | 0.15.1+      | **[SpecStory CLI](https://specstory.com/specstory-cli)**         | [Open](https://github.com/specstoryai/getspecstory/tree/dev/specstory-cli/pkg/providers/geminicli)      | `brew tap specstoryai/tap`<br/>`brew install specstory`       |
| [DeepSeek TUI](https://github.com/Hmbown/DeepSeek-TUI)                        | CLI        | 0.8.39+      | **[SpecStory CLI](https://specstory.com/specstory-cli)**         | [Open](https://github.com/specstoryai/getspecstory/tree/dev/specstory-cli/pkg/providers/deepseektui)    | `brew tap specstoryai/tap`<br/>`brew install specstory`       |
| [Antigravity CLI](https://antigravity.google/)                                | CLI        | v1.1.5+      | **[SpecStory CLI](https://specstory.com/specstory-cli)**         | [Open](https://github.com/specstoryai/getspecstory/tree/dev/specstory-cli/pkg/providers/antigravitycli) | `brew tap specstoryai/tap`<br/>`brew install specstory`       |
| [Muse Code](https://developer.meta.com/ai/products/muse-code/)                | CLI        | 0.1.0+       | **[SpecStory CLI](https://specstory.com/specstory-cli)**         | [Open](https://github.com/specstoryai/getspecstory/tree/dev/specstory-cli/pkg/providers/musecode)       | `brew tap specstoryai/tap`<br/>`brew install specstory`       |
| [Qwen Code](https://github.com/QwenLM/qwen-code)                              | CLI        | 0.23.4+      | **[SpecStory CLI](https://specstory.com/specstory-cli)**         | [Open](https://github.com/specstoryai/getspecstory/tree/dev/specstory-cli/pkg/providers/qwencode)       | `brew tap specstoryai/tap`<br/>`brew install specstory`       |
| [Pi](https://pi.dev) | CLI | 0.85.1+ | **[SpecStory CLI](https://specstory.com/specstory-cli)** | [Open](https://github.com/specstoryai/getspecstory/tree/dev/specstory-cli/pkg/providers/piagent) | `brew tap specstoryai/tap`<br/>`brew install specstory` |
| [Grok Build](https://x.ai/cli) | CLI | 1.0.3+ | **[SpecStory CLI](https://specstory.com/specstory-cli)** | [Open](https://github.com/specstoryai/getspecstory/tree/dev/specstory-cli/pkg/providers/grokbuild) | `brew tap specstoryai/tap`<br/>`brew install specstory` |
| Any [Agent Skills](https://agentskills.io)                                    | Skill      | Node 22.5+   | **[Lore](./lore)** 📜                                             | [Open](./lore)                                                                                          | `npx skills add specstoryai/getspecstory --skill lore`        |

> [!TIP]
> Don't see the agent you need listed here? The [SpecStory CLI](https://github.com/specstoryai/getspecstory/tree/dev/specstory-cli) is open source and it's easy to contribute new agent providers.

> [!NOTE]
> For Cursor users: Install from within Cursor, not from the Visual Studio Marketplace website. [Learn why](https://github.com/specstoryai/getspecstory/issues/8)

### Terminal Coding Agents

**One installation works with all terminal coding agents** - Claude Code, Cursor CLI, Codex, Droid, DeepSeek, Antigravity CLI, Qwen Code, and Pi:

```bash
# Check which agents are installed
specstory check

# Launch your preferred agent with session auto-save
specstory run claude       # Launch Claude Code
specstory run cursor       # Launch Cursor CLI
specstory run codex        # Launch Codex CLI
specstory run droid        # Launch Droid CLI
specstory run deepseek     # Launch DeepSeek TUI
specstory run antigravity  # Launch Antigravity CLI
specstory run grok         # Launch Grok Build
specstory run muse         # Launch Muse Code
specstory run qwen         # Launch Qwen Code
specstory run pi           # Launch Pi
specstory run              # Launch the default agent (Claude Code)

# Render all prior agent coding sessions in the project as markdown
specstory sync
```

All sessions automatically save to `.specstory/history/` in your current project.

> [!TIP]
> The SpecStory CLI acts as a wrapper that enhances any of these terminal agents with automatic session saving. You only need the respective agent installed (e.g., Claude Code) for SpecStory to work with it.

Learn much more about the [SpecStory CLI](https://github.com/specstoryai/getspecstory/tree/dev/specstory-cli).

## SpecStory Cloud ☁️

[**SpecStory Cloud**](https://cloud.specstory.com) transforms your local AI conversations into a powerful, centralized knowledge system.

### The Problem We Solve
- **Lost Context**: Critical decisions and solutions scattered across machines and projects
- **No Global Search**: Finding that perfect solution from last month is impossible
- **Fragile Sharing**: Passing around Markdown files doesn't scale

### The Solution
SpecStory Cloud creates your personal AI coding knowledge base:
- 🎯 **Explicit Opt-In**: Nothing syncs to SpecStory Cloud without an optional sign-up and login first
- 🔍 **Search Everywhere**: Full-text search across all your projects from all your computers.
- 💬 **Chat with Your History**: Ask questions across your sessions to revisit decisions, understand past work, and find solutions.
- 📚 **Organized by Project**: Automatic categorization by repository and time
- 🚀 **API Access**: Programmatic sync and search for automation
- 👥 **Team Features**: Share knowledge across your organization with SpecStory Cloud for Teams

[Get Started with SpecStory Cloud →](https://cloud.specstory.com)

### How to Sync to Cloud

| Method               | One-Time Setup                                               | Live Sessions                                          | Past Sessions                                        |
|----------------------|--------------------------------------------------------------|--------------------------------------------------------|------------------------------------------------------|
| **SpecStory CLI**    | `specstory login`                                            | Auto-pushed when using `specstory run` while logged in | Use `specstory sync` to push existing local sessions |
| **Cursor Extension** | Command Palette → "SpecStory: Open Cloud Sync Configuration" | Configure auto-sync in settings                        | Use sync command from Command Palette                |
| **VSCode Extension** | Command Palette → "SpecStory: Open Cloud Sync Configuration" | Configure auto-sync in settings                        | Use sync command from Command Palette                |

> [!IMPORTANT]
> **Local-First & Private by Default**: All sessions are saved locally to `.specstory/history/`. Nothing is ever sent to the cloud unless you explicitly login with. Even after logging in, you can control what gets synced.

## Lore 📜

[**Lore**](./lore) turns the sessions SpecStory saves into agent skills forged from how you actually work. Your sessions are your lore.

### The Problem We Solve
- **Repeated Yourself Again**: You re-explain the same workflows, conventions, and fixes to your agent every session
- **Skills From Guesswork**: Hand-written agent skills describe how you think you work, not how you demonstrably do

### The Solution
Lore mines your `.specstory/history` into evidence - what you actually ran, what worked, and the judgment you apply without noticing - and forges the skills you approve into every agent on your machine.

Install:

```sh
npx skills add specstoryai/getspecstory --skill lore
```

Then invoke it - `/lore` in Claude Code, `$lore` in Codex, or just ask ("mine my lore") in Gemini CLI and others:

```
/lore
```

[Get Started with Lore →](./lore)

## Documentation & Support

- 📚 **[Full Documentation](https://docs.specstory.com/overview)** - Complete guides and [Cloud API reference](https://docs.specstory.com/api-reference/introduction)
- 📜 **[Lore](./lore)** - Forge your `.specstory/history` into installable agent skills, with evidence and outcomes
- 🐛 **[Report Issues](https://github.com/specstoryai/getspecstory/issues)** - We actively monitor and respond
- 📖 **[Contribute to Docs](https://github.com/specstoryai/docs/)** - PRs welcome!

## Reviews & Feedback

Love SpecStory? Help others discover their AI coding memory upgrade by leaving a [review](https://marketplace.visualstudio.com/items?itemName=SpecStory.specstory-vscode&ssr=false#review-details)! 🧠

## Star History

![Star History Chart](https://api.star-history.com/svg?repos=specstoryai/getspecstory&type=Date)

---

Made with ❤️ by SpecStory
