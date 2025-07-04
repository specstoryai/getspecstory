## v0.4.0 (alpha) - 2025-07-04

### New

- All the latest Claude Code auto-save features of [SpecStory 0.3.0](https://github.com/specstoryai/getspecstory/releases/tag/v0.3.0)

### Fixed

- All the latest Claude Code auto-save fixes of [SpecStory 0.3.0](https://github.com/specstoryai/getspecstory/releases/tag/v0.3.0)


### Known Issues 

- Some output from Claude Code might be low/no contrast in the Claude Code Terminal 
- Markdown formatting inconsistencies
- Cross-file search not yet implemented
- Registration may not work for some users


## v0.3.0 (alpha) - 2025-07-02

### New

- You can jump straight to open a file with Cmd-P or File → Go to File...
- BearClaude will play a system sound when Claude Code needs your attention to approve an action. You can select the sound, or turn this off in BearClaude → Settings.
- Active panel highlighting to make it easier to see if the markdown editor or Claude Code terminal is the active panel
- All the latest Claude Code auto-save features of [SpecStory 0.2.0](https://github.com/specstoryai/getspecstory/releases/tag/v0.2.0)

### Fixed

- Fixed problems starting Claude Code within BearClaude for some users with environments that rely on the contents of `.zshrc` or `.bashrc`.
- All the latest Claude Code auto-save fixes of [SpecStory 0.2.0](https://github.com/specstoryai/getspecstory/releases/tag/v0.2.0)


## v0.2.0 (alpha) - 2025-06-27

### New

- Launching BearClaude now works in the terminal, you can install `bearclaude` in BearClaude → Settings
- All the latest Claude Code auto-save features of [SpecStory 0.1.0](https://github.com/specstoryai/getspecstory/releases/tag/v0.1.0)
- Home and End keys now work in the Claude Code terminal
- Add File → Save (Cmd-S), even though BearClaude uses auto-save, Cmd-S is muscle memory to many people
- Update the default markdown editor to be the Editor Only mode
- Make the toggle between Editor Only and Live Preview mode "sticky" when BearClaude is restarted

### Fixed

- All the latest Claude Code auto-save fixes of [SpecStory 0.1.0](https://github.com/specstoryai/getspecstory/releases/tag/v0.1.0)
- Fixed ability to pass arguments to Claude Code via the Claude Code command in settings
- Fixed the issue where the toolbar at the top of the Claude Code terminal would disappear when the app window was resized
- Fixed a failure mode when the first time Claude Code has ever been run is in BearClaude
- Better window sizing for first ever BearClaude launch
 

## v0.1.0 (alpha) - 2025-06-23

### New

- SpecFlow Templates
  - New projects include [SpecFlow](https://www.specflow.com/) template files to help you turn your intent into software through structured planning and iterative execution with Claude Code
  - A "sticky" option to toggle off templates in new projects
- Multiple Window Support
  - Open multiple BearClaude windows with Cmd-Shift-N (or File → New BearClaude Window)
  - Work on the same project in multiple windows, or manage different projects simultaneously
- Claude Code
  - BearClaude now detects when Claude Code fails to start and provides helpful resources when it doesn't
  - Claude Code help is now available in the help menu
  - Claude Code best practices are now available in the help menu

### Fixed

- Removed inoperable default SwiftUI tab menu items


## v0.0.2 (alpha) - 2025-06-20

### New

- DMG installer for macOS
- Checks for new versions of BearClaude

### Fixed

- Shift-enter didn’t work in the Claude Code terminal
- In the Markdown editor, typing at the bottom of the doc caused text to disappear from view


## v0.0.1 (alpha) - 2025-06-18

BearClaude's initial alpha is here. Built for ourselves, released for you. A native macOS app with Claude Code built-in. Finally on your dock. 🐻✨

### New

- Local-first markdown editing
- Live preview for markdown
- Claude Code integration
- Chat history auto-save
