<img width="540" alt="SpecStory" src="https://github.com/user-attachments/assets/0e6844db-aaa0-4b0d-99ce-911ba237acfe" />

[![Installs](https://img.shields.io/endpoint?url=https%3A%2F%2Fspecstory.com%2Fapi%2Fbadge%3Fstat%3Dinstalls&style=flat-square)](https://specstory.com/api/badge?stat=installs)
[![Active Users](https://img.shields.io/endpoint?url=https%3A%2F%2Fspecstory.com%2Fapi%2Fbadge%3Fstat%3DactiveUsers&style=flat-square)](https://specstory.com/api/badge?stat=activeUsers)
[![Sessions Saved](https://img.shields.io/endpoint?url=https%3A%2F%2Fspecstory.com%2Fapi%2Fbadge%3Fstat%3DsessionsSaved&style=flat-square)](https://specstory.com/api/badge?stat=sessionsSaved)
[![Rules Generated](https://img.shields.io/endpoint?url=https%3A%2F%2Fspecstory.com%2Fapi%2Fbadge%3Fstat%3DrulesGenerated&style=flat-square)](https://specstory.com/api/badge?stat=rulesGenerated)

SpecStory is a Visual Studio Code extension that makes it easy to **save, search and share your AI chat history.** 
- It exports your AI chats in searchable structured Markdown that you can version.
- Never lose a great solution, easily share interactions, and use conversation history to refine custom instructions and project rules.

We support [Cursor IDE](https://www.cursor.com/) and [GitHub Copilot](https://github.com/features/copilot) as of release `v0.11.0` [(changelog)](https://marketplace.visualstudio.com/items/SpecStory.specstory-vscode/changelog).

- For Cursor: It is tested to work for `Version: 0.43.6` and later
- For Copilot: It is tested to work for `Version: 1.300.0` and later

When enabled, SpecStory automatically saves every AI chat to your local project's `.specstory` directory.

To learn about additional features, check out our [website](https://specstory.com/) and read the [docs](https://docs.specstory.com/introduction).

## Getting Started

1️⃣ We recommend installing directly from the Extension Marketplace in your preferred editor.

| Step | GitHub Copilot | Cursor | Install from VSIX |
|------|---------------|--------|------------------|
| 1 | Open VSCode with GitHub Copilot installed and activated. | Open Cursor. | Make sure you're on the latest version of [GitHub Copilot](https://github.blog/changelog/label/copilot/) or [Cursor](https://www.cursor.com/changelog). |
| 2 | Search for **SpecStory** in the Extensions Marketplace (Ctrl/CMD-Shift-X). | Search for **SpecStory** in the Extensions Marketplace (Ctrl/CMD-Shift-X). | Download our extension: [specstory-vscode-latest.vsix](https://static.specstory.com/vscode-extension/releases/specstory-vscode-latest.vsix) |
| 3 | Click **Install**. Consult our [Changelog](https://marketplace.visualstudio.com/items/SpecStory.specstory-vscode/changelog) to make sure you're on the latest version. | Click **Install**. Consult our [Changelog](https://marketplace.visualstudio.com/items/SpecStory.specstory-vscode/changelog) to make sure you're on the latest version. | From either editor, pop open the Command Palette (Ctrl/CMD-Shift-P) and choose `Extensions: Install from VSIX…`. |
| 4 | Once installed, SpecStory automatically saves your Copilot chats to the `.specstory` folder in your project. | Once installed, SpecStory automatically saves your Cursor chats to the `.specstory` folder in your project. | To verify the installation, open the Command Palette again (Ctrl/CMD-Shift-P) and type `SpecStory`. You should see the available commands show up. |

> [!NOTE]
> As a Cursor user, if you try to install from the [Visual Studio Marketplace](https://marketplace.visualstudio.com/items?itemName=SpecStory.specstory-vscode) website banner you might find yourself in a state where VSCode has SpecStory installed but Cursor doesn't. [It makes for a bad time](https://github.com/specstoryai/getspecstory/issues/8)

2️⃣ Take a quick spin through our [docs](https://docs.specstory.com/introduction), they explain installation details in more depth. 

3️⃣ Love the extension? Help others discover their AI assistant's memory upgrade by dropping a quick [review](https://marketplace.visualstudio.com/items?itemName=SpecStory.specstory-vscode&ssr=false#review-details)! 🧠

## Filing issues

> [!NOTE]
The extension is in rapid, active development. If you're not on `Cursor Version: 0.43.6` or later you might experience errors due to the way versions of Cursor prior to `0.43.5` stored chat and composer data that have since changed.
 
We are actively monitoring this repo. Please use it to file bugs and feature enhancements. We'd love to hear from you!

## Contributing to the docs

We welcome edits and contribution to our documentation. Just issue a pull request against our [docs repo](https://github.com/specstoryai/docs/).

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=specstoryai/getspecstory&type=Date)
