<img width="1551" height="158" alt="spst_logo" src="https://github.com/user-attachments/assets/de7fc781-3f37-49f1-a847-b9bbadc05091" />


# Intent is the new source code

**Turn your AI development conversations into searchable, shareable knowledge.**

Never lose a brilliant solution, code snippet, or architectural decision again. SpecStory captures, indexes, and makes searchable every interaction you have with AI coding assistants across all your projects and tools.

[![Installs](https://img.shields.io/endpoint?url=https%3A%2F%2Fspecstory.com%2Fapi%2Fbadge%3Fstat%3Dinstalls&style=flat-square)](https://specstory.com/api/badge?stat=installs)
[![Active Users](https://img.shields.io/endpoint?url=https%3A%2F%2Fspecstory.com%2Fapi%2Fbadge%3Fstat%3DactiveUsers&style=flat-square)](https://specstory.com/api/badge?stat=activeUsers)
[![Sessions Saved](https://img.shields.io/endpoint?url=https%3A%2F%2Fspecstory.com%2Fapi%2Fbadge%3Fstat%3DsessionsSaved&style=flat-square)](https://specstory.com/api/badge?stat=sessionsSaved)
[![Rules Generated](https://img.shields.io/endpoint?url=https%3A%2F%2Fspecstory.com%2Fapi%2Fbadge%3Fstat%3DrulesGenerated&style=flat-square)](https://specstory.com/api/badge?stat=rulesGenerated)

## How It Works
```
AI Coding Tools              Local First                  Cloud Platform
─────────────────           ─────────────                ─────────────────
                                                          (Login Required)
Cursor IDE         ┐
Copilot IDE        │
Claude Code CLI    ├──────►  .specstory/history/  ──────►  cloud.specstory.com
Cursor CLI         │          (Auto-Saved Locally)          (Search & Share)
Codex CLI          ┘
```

## Workflow

1. **Capture** - Extensions save every AI interaction locally to `.specstory/history/`
2. **Sync (Optional)** - Only if logged in, sessions sync to cloud
3. **Search** - Find conversations locally or across all projects in cloud
4. **Share** - Export and share specific solutions with your team

## Supported Development Tools

SpecStory integrates seamlessly with your favorite AI coding tools, automatically saving all conversations locally to `.specstory/history/` in your project. **Everything is local-first** - your data stays on your machine unless you choose to sync to the cloud.

### Installation

| Product | Type | Supported Agents | Min Version | Installation | Changelog |
|---------|------|------------------|-------------|--------------|-----------|
| **[Cursor Extension](https://www.cursor.com/)** | IDE | [Cursor AI](https://www.cursor.com/) | v0.43.6+ | Search "SpecStory" in Extensions (Cmd/Ctrl+Shift+X) → Install | [📋 View](https://marketplace.visualstudio.com/items/SpecStory.specstory-vscode/changelog) |
| **[VSC Copilot Extension](https://github.com/features/copilot)** | IDE | [GitHub Copilot](https://github.com/features/copilot) | v1.300.0+ | Search "SpecStory" in Extensions (Cmd/Ctrl+Shift+X) → Install | [📋 View](https://marketplace.visualstudio.com/items/SpecStory.specstory-vscode/changelog) |
| **[SpecStory CLI](https://specstory.com/specstory-cli)** | CLI | [Claude Code](https://claude.ai/claude-code)<br/>[Cursor CLI](https://cursor.com/cli)<br/>[Codex CLI](https://www.openai.com/codex) | v1.0.27+<br/>v2025.09.18+<br/>v0.42.0+ | `brew tap specstoryai/tap`<br/>`brew install specstory` | [📋 View](https://github.com/specstoryai/getspecstory/releases) |

> [!NOTE]
> For Cursor users: Install from within Cursor, not from the Visual Studio Marketplace website. [Learn why](https://github.com/specstoryai/getspecstory/issues/8)

### CLI Tools

**One installation works with all CLI tools** - Claude Code, Cursor CLI, and Codex:

```bash
# Check which agents are installed
specstory check

# Launch your preferred agent with auto-save
specstory run claude    # Launch Claude Code
specstory run cursor    # Launch Cursor CLI
specstory run codex     # Launch Codex CLI
specstory run           # Launch default agent
```

All sessions automatically save to `.specstory/history/` in your current project.

> [!TIP]
> The SpecStory CLI acts as a wrapper that enhances any of these terminal agents with automatic session saving. You only need the respective agent installed (e.g., Claude Code) for SpecStory to work with it.

## SpecStory Cloud ☁️

[**SpecStory Cloud**](https://cloud.specstory.com) transforms your local AI conversations into a powerful, centralized knowledge system.

### The Problem We Solve
- **Lost Context**: Critical decisions and solutions scattered across machines and projects
- **No Global Search**: Finding that perfect solution from last month is impossible
- **Fragile Sharing**: Passing around Markdown files doesn't scale

### The Solution
SpecStory Cloud creates your personal AI coding knowledge base:
- 🔍 **Search Everywhere**: Full-text search across all your projects via web interface. RAG coming soon.
- 🎯 **Explicit Opt-In**: Nothing syncs to cloud without sign-up and login first
- 📚 **Organized by Project**: Automatic categorization by repository and time
- 🚀 **API Access**: Programmatic sync and search for automation
- 👥 **Team Features**: Coming soon - share knowledge across your organization

[Get Started with SpecStory Cloud →](https://cloud.specstory.com)

### How to Sync to Cloud

| Method | One-Time Setup | Live Sessions | Past Sessions |
|--------|----------------|---------------|---------------|
| **SpecStory CLI** | `specstory login` | Auto-pushed when using `specstory run` while logged in | Use `specstory sync` to push existing local sessions |
| **Cursor Extension** | Command Palette → "SpecStory: Open Cloud Sync Configuration" | Configure auto-sync in settings | Use sync command from Command Palette |
| **VSCode Extension** | Command Palette → "SpecStory: Open Cloud Sync Configuration" | Configure auto-sync in settings | Use sync command from Command Palette |

> [!IMPORTANT]
> **Local-First & Private by Default**: All sessions are saved locally to `.specstory/history/`. Nothing is ever sent to the cloud unless you explicitly login with. Even after logging in, you can control what gets synced.

## Additional Tools

### BearClaude (macOS)
For those who prefer a dedicated spec-writing environment, [BearClaude](https://bearclaude.specstory.com) offers a three-pane interface combining documentation, editing, and Claude Code terminal. Perfect for maintaining specs alongside your code. ([Learn more](https://docs.specstory.com/bearclaude/introduction))

## Documentation & Support

- 📚 **[Full Documentation](https://docs.specstory.com/overview)** - Complete guides and [Cloud API reference](https://docs.specstory.com/api-reference/introduction)
- 🐛 **[Report Issues](https://github.com/specstoryai/getspecstory/issues)** - We actively monitor and respond
- 📖 **[Contribute to Docs](https://github.com/specstoryai/docs/)** - PRs welcome!

## Connect with Us

- **X/Twitter**: [@specstoryai](https://twitter.com/specstoryai)
- **LinkedIn**: [SpecStory](https://www.linkedin.com/company/specstory)
- **Slack**: [Join our community](https://specstory.slack.com/join/shared_invite/zt-2vq0274ck-MYS39rgOpDSmgfE1IeK9gg#/shared-invite/email)

## Reviews & Feedback

Love SpecStory? Help others discover their AI coding memory upgrade by leaving a [review](https://marketplace.visualstudio.com/items?itemName=SpecStory.specstory-vscode&ssr=false#review-details)! 🧠

## Star History

![Star History Chart](https://api.star-history.com/svg?repos=specstoryai/getspecstory&type=Date)
