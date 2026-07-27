# Extension Architecture for Remote Workspaces

This document explains how the SpecStory CLI and VS Code extension work together across different workspace types (local, WSL, SSH remote), including the architectural decisions and trade-offs.

## Overview

The SpecStory extension can run in two modes:
- **Standalone CLI**: Run manually by users from command line
- **Extension-Integrated**: Run automatically by VS Code extension

Both modes must handle conversations from different tools stored in different locations, with different accessibility depending on where the CLI executes.

## Data Locality: Where Conversations Are Stored

Understanding where each tool stores its data is critical to the architecture:

### IDE Tools (Stored on Client Machine)

These tools always store conversations on the Windows client, even when working with remote workspaces:

| Tool | Storage Location | Accessible From |
|------|------------------|-----------------|
| **Cursor IDE** | `%APPDATA%\Cursor\User\workspaceStorage\{workspace-id}\state.vscdb` | Windows client only |
| **Copilot IDE** | `%APPDATA%\Code\User\workspaceStorage\{workspace-id}\chatSessions\*.jsonl` | Windows client only |

**Why on client?** These are VS Code/Cursor IDE extensions that run in UI mode on the client machine. Their storage follows the IDE, not the workspace.

### CLI/Agent Tools (Stored on Execution Machine)

These tools store conversations wherever they execute:

| Tool | Storage Location | Accessible From |
|------|------------------|-----------------|
| **Claude Code** | `~/.claude/projects/{project-name}/*.jsonl` | Machine where agent runs |
| **Cursor CLI** | `~/.cursor/chats/*.json` | Machine where CLI runs |
| **Gemini CLI** | `~/.gemini/tmp/{project-hash}/*.jsonl` | Machine where CLI runs |

**Why on execution machine?** These are CLI tools that run in terminals. They store data locally on whichever machine executes them.

## Architecture Decision: Run CLI on Client (Locally)

We decided to **always run the CLI on the Windows client** (where the extension runs), not on remote machines. Here's why:

### ✅ Advantages of Running Locally

1. **Direct access to IDE tool data** (Cursor IDE, Copilot IDE)
   - These are typically the primary conversation sources
   - Data is always on the client
   - No network access needed

2. **Single code path** for all workspace types
   - Same logic for local, WSL, and SSH workspaces
   - Simpler to maintain and debug

3. **No SSH access required**
   - CLI doesn't need SSH credentials
   - No additional authentication complexity

4. **Works with company VPNs/firewalls**
   - Some environments restrict SSH
   - Local execution always works

### ❌ Disadvantages of Running Locally

1. **Cannot access CLI/Agent tools on remote machines**
   - Claude Code conversations created on SSH remote are inaccessible
   - Would need separate mechanism to fetch remote agent data

### Why Not Run on Remote?

Running the CLI on a remote SSH machine creates significant problems:

#### SSH Remote: Major Issues ❌

| Aspect | Problem |
|--------|---------|
| **IDE Data Access** | Remote machine cannot access Windows `%APPDATA%` (no network path) |
| **Authentication** | Need SSH credentials, key management |
| **CLI Distribution** | Must copy CLI binary to remote machine |
| **Extension Complexity** | Need to spawn remote commands, handle SSH connections |
| **Workspace Detection** | Remote machine doesn't know about Windows workspace storage |

#### WSL: Possible But More Complex ⚠️

WSL is easier than SSH but still requires extra work:

| Aspect | Status |
|--------|--------|
| **IDE Data Access** | ✅ Possible: WSL can access `/mnt/c/Users/Admin/AppData/...` |
| **Authentication** | ✅ No SSH needed |
| **CLI Distribution** | ⚠️ Need Linux binary or run Windows binary via WSL interop |
| **Extension Complexity** | ⚠️ Need to spawn commands in WSL context |
| **Workspace Detection** | ⚠️ Need to convert Windows paths to WSL paths |

**Verdict:** WSL remote execution is *possible* but adds complexity without significant benefit, since we can already access WSL workspace data from Windows via the workspace storage approach.

## Manual CLI Usage

When users run the CLI manually from the command line:

### Scenario 1: Running on Local Windows Machine

```zsh
cd C:\Users\Admin\code\myproject
specstory list
```

**What it finds:**
- ✅ Cursor IDE conversations (all workspaces matching `myproject` basename)
- ✅ Copilot IDE conversations (all workspaces matching `myproject` basename)
- ✅ Claude Code conversations (if created locally)
- ✅ Cursor CLI conversations (if created locally)

**How it works:**
1. Gets current working directory: `C:\Users\Admin\code\myproject`
2. Extracts basename: `myproject`
3. Searches workspace storage for all workspaces matching `myproject`:
   - Local: `file:///c:/Users/Admin/code/myproject`
   - WSL: `vscode-remote://wsl+ubuntu/home/user/code/myproject`
   - SSH: `vscode-remote://ssh-remote+server/home/user/code/myproject`
4. Reads conversations from matched workspace storage (on Windows)

### Scenario 2: Running on Remote SSH Machine

```zsh
# SSH'd into remote server
cd /home/user/code/myproject
specstory list
```

**What it finds:**
- ❌ Cursor IDE conversations (storage is on Windows client)
- ❌ Copilot IDE conversations (storage is on Windows client)
- ✅ Claude Code conversations (if created on this remote machine)
- ✅ Cursor CLI conversations (if created on this remote machine)

**How it works:**
1. Gets current working directory: `/home/user/code/myproject`
2. Extracts basename: `myproject`
3. Looks for workspace storage on the remote machine
4. **Doesn't find any** - IDE workspace storage is on Windows, not accessible
5. Only finds CLI/Agent tool data that exists locally on the remote

**Use case:** This mode is useful if you primarily use CLI agents (Claude Code, Cursor CLI) on remote machines and want to export those conversations.

### Scenario 3: Running in WSL

```zsh
# In WSL terminal
cd /home/user/code/myproject
specstory list
```

**What it finds:**
- ⚠️ Cursor IDE conversations (if workspace storage path is adjusted for WSL)
- ⚠️ Copilot IDE conversations (if workspace storage path is adjusted for WSL)
- ✅ Claude Code conversations (if created in WSL)
- ✅ Cursor CLI conversations (if created in WSL)

**How it works:**
1. Gets current working directory: `/home/user/code/myproject`
2. Extracts basename: `myproject`
3. Looks for workspace storage
4. **Could** access Windows storage via `/mnt/c/Users/Admin/AppData/...` but currently doesn't
5. Would need additional logic to detect WSL and map to Windows paths

## CLI Output Files: Read and Write Behaviour

The CLI reads and writes two categories of files in `.specstory/`:

### `.specstory/.project.json`

**Written:** On every `run`, `watch`, or `sync` invocation. Creates the file if it doesn't exist, otherwise updates it.

**Read back:** On every subsequent invocation, to reuse the existing `workspace_id` and `project_name`. Without this file, the CLI generates a new identity each run, which breaks cloud sync matching and causes duplicate project entries.

### `.specstory/history/*.md`

**Written:** Once per conversation session. Filename encodes the session timestamp and slug.

**Read back:** Before each write, the CLI reads the existing file (if any) to compare content. If identical, the write is skipped. This dedup check avoids unnecessary writes and downstream sync churn.

**Consequence for the extension:** Because both file types are also read back, the temp directory used by the extension must be **persistent across runs** — not cleaned up after each sync.

## Extension-Integrated Usage

When the VS Code extension runs the CLI, it operates in one of two modes depending on the workspace type.

### Mode A: Local Workspace — Direct Write

For local workspaces (including WSL with bidirectional filesystem access), the CLI can write directly to the workspace. The extension passes the workspace's own `.specstory/` directory as `--specstory-dir`. No copying is needed.

```
┌─────────────────────────────────────────────────┐
│ Windows Client                                  │
│                                                 │
│  ┌─────────────────┐                           │
│  │ VS Code         │                           │
│  │ Extension       │                           │
│  └────────┬────────┘                           │
│           │ 1. Spawn CLI                       │
│           ▼                                     │
│  ┌─────────────────────────────────┐           │
│  │ SpecStory CLI                   │           │
│  │ --specstory-dir {workspace}/    │           │
│  │                 .specstory/     │           │
│  └────────┬────────────────────────┘           │
│           │ 2. Write directly                  │
│           ▼                                     │
│  ┌─────────────────────┐                       │
│  │ {workspace}/        │                       │
│  │ .specstory/         │                       │
│  │ ├─ .project.json    │                       │
│  │ └─ history/         │                       │
│  │    ├─ conv1.md      │                       │
│  │    └─ conv2.md      │                       │
│  └─────────────────────┘                       │
│                                                 │
└─────────────────────────────────────────────────┘
```

**Step-by-step:**

1. **Extension gets workspace path** (local filesystem path, directly accessible):
   ```typescript
   const workspacePath = vscode.workspace.workspaceFolders[0].uri.fsPath;
   // e.g., "C:\Users\Admin\code\myproject"
   const specstoryDir = path.join(workspacePath, '.specstory');
   ```

2. **Extension spawns CLI:**
   ```typescript
   spawn('./specstory.exe', [
       'sync',
       '--specstory-dir', specstoryDir
   ], { cwd: workspacePath });
   ```

3. **CLI writes directly to workspace** — no further action needed from the extension.

### Mode B: Remote Workspace (SSH) — Temp Dir + Copy

For SSH remote workspaces, the local filesystem and the remote are separated by a network boundary. The CLI cannot write to the remote directly, so the extension uses a persistent local temp directory as an intermediary, then copies the output via the VS Code FS API (which handles SSH transparently).

```
┌─────────────────────────────────────────────────┐
│ Windows Client                                  │
│                                                 │
│  ┌─────────────────┐                           │
│  │ VS Code         │                           │
│  │ Extension       │                           │
│  └────────┬────────┘                           │
│           │ 1. Spawn CLI                       │
│           ▼                                     │
│  ┌─────────────────────────────────┐           │
│  │ SpecStory CLI                   │           │
│  │ --specstory-dir {tempDir}       │           │
│  └────────┬────────────────────────┘           │
│           │ 2. Write to temp                   │
│           ▼                                     │
│  ┌─────────────────────┐                       │
│  │ C:\Temp\            │                       │
│  │ specstory-{hash}\   │  ← persistent         │
│  │ ├─ .project.json    │                       │
│  │ └─ history\         │                       │
│  │    ├─ conv1.md      │                       │
│  │    └─ conv2.md      │                       │
│  └────────┬────────────┘                       │
│           │ 3. Extension copies via FS API     │
│           ▼                                     │
└───────────┼─────────────────────────────────────┘
            │
            │ vscode.workspace.fs.writeFile()
            │ (SSH: transparent upload)
            │
            ▼
┌─────────────────────────────────────────────────┐
│ SSH Remote Workspace                            │
│                                                 │
│  /path/to/myproject/                           │
│  └─ .specstory/                                │
│     ├─ .project.json                           │
│     └─ history/                                │
│        ├─ conv1.md                             │
│        └─ conv2.md                             │
│                                                 │
└─────────────────────────────────────────────────┘
```

**Step-by-step:**

1. **Extension gets workspace URI and determines it is remote:**
   ```typescript
   const workspaceUri = vscode.workspace.workspaceFolders[0].uri;
   // e.g., "vscode-remote://ssh-remote+server/home/user/code/myproject"
   const isRemote = workspaceUri.scheme === 'vscode-remote';
   ```

2. **Extension creates (or reuses) a persistent local temp directory:**
   ```typescript
   // Stable per-workspace dir, NOT cleaned up between runs.
   // CLI reads .project.json back on every invocation, so the dir must persist.
   const tempDir = path.join(os.tmpdir(), 'specstory-' + workspaceHash);
   await fs.mkdir(tempDir, { recursive: true });
   ```

3. **Extension bootstraps identity on first use:**
   ```typescript
   // If the remote workspace already has a .project.json (e.g. from a previous
   // manual CLI run on the remote), copy it into the temp dir so the CLI reuses
   // the same identity rather than generating a new one.
   const remoteProjectJson = vscode.Uri.joinPath(workspaceUri, '.specstory', '.project.json');
   const localProjectJson = path.join(tempDir, '.project.json');
   if (!fs.existsSync(localProjectJson)) {
       try {
           const content = await vscode.workspace.fs.readFile(remoteProjectJson);
           await fs.writeFile(localProjectJson, content);
       } catch {
           // Remote doesn't have one yet — CLI will create a new identity.
       }
   }
   ```

4. **Extension spawns CLI:**
   ```typescript
   spawn('./specstory.exe', [
       'sync',
       '--specstory-dir', tempDir
   ], { cwd: workspacePath });
   ```

5. **CLI exports to temp:**
   - Reads Cursor IDE / Copilot IDE conversations from Windows workspace storage
   - Reads `{tempDir}/.project.json` to reuse existing identity (or creates one)
   - Reads `{tempDir}/history/*.md` to skip unchanged files
   - Writes `{tempDir}/.project.json` and `{tempDir}/history/*.md`

6. **Extension copies to remote workspace:**
   ```typescript
   // Copy .project.json
   const projectJson = await fs.readFile(path.join(tempDir, '.project.json'));
   await vscode.workspace.fs.writeFile(
       vscode.Uri.joinPath(workspaceUri, '.specstory', '.project.json'),
       projectJson
   );

   // Copy each new or changed markdown file
   for (const file of markdownFiles) {
       const content = await fs.readFile(path.join(tempDir, 'history', file));
       await vscode.workspace.fs.writeFile(
           vscode.Uri.joinPath(workspaceUri, '.specstory', 'history', file),
           content
       );
   }
   ```

### What the Extension Can Access

| Workspace Type | Cursor IDE | Copilot IDE | Claude Code (local) | Claude Code (remote) |
|----------------|------------|-------------|---------------------|----------------------|
| **Local** | ✅ | ✅ | ✅ | N/A |
| **WSL** | ✅ | ✅ | ✅ (Windows) | ❌ (WSL) |
| **SSH** | ✅ | ✅ | ✅ (Windows) | ❌ (Remote) |

**Why these limitations?**
- ✅ IDE tools: Always accessible because storage is on Windows client
- ✅ Local Claude Code: Accessible because it's on the same Windows machine
- ❌ Remote Claude Code: Not accessible because data is on remote machine, CLI runs on Windows

### Watch Mode

The extension watches for file changes and triggers sync automatically:

```typescript
// Watch Cursor IDE database
const cursorPattern = new vscode.RelativePattern(
    '%APPDATA%/Cursor/User/workspaceStorage',
    '**/state.vscdb'
);
const cursorWatcher = vscode.workspace.createFileSystemWatcher(cursorPattern);
cursorWatcher.onDidChange(() => scheduleSync());

// Watch Copilot IDE sessions
const copilotPattern = new vscode.RelativePattern(
    '%APPDATA%/Code/User/workspaceStorage',
    '**/chatSessions/*.{json,jsonl}'
);
const copilotWatcher = vscode.workspace.createFileSystemWatcher(copilotPattern);
copilotWatcher.onDidChange(() => scheduleSync());

// Debounced sync (avoid rapid successive syncs)
function scheduleSync() {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(async () => {
        await syncConversations(); // Run CLI + copy files
    }, 2000); // Wait 2 seconds after last change
}
```

**How it works:**
1. Extension watches workspace storage directories on Windows
2. When file changes detected (new conversation, edited message), schedule sync
3. Debounce timer ensures sync only runs after 2 seconds of quiet
4. Sync runs CLI → exports to persistent temp dir → copies to workspace

## WSL vs. SSH: Key Differences

### WSL (Windows Subsystem for Linux)

**Workspace URI format:**
- `file://wsl.localhost/Ubuntu/home/user/code/myproject`
- `vscode-remote://wsl+ubuntu/home/user/code/myproject`

**Filesystem Access:**
- ✅ WSL can access Windows: `/mnt/c/Users/Admin/...`
- ✅ Windows can access WSL: `\\wsl$\Ubuntu\home\user\...`
- ✅ Bidirectional filesystem access

**Storage Location:**
- IDE tools: Windows (`%APPDATA%`)
- CLI tools: Could be on Windows or WSL, depending on where executed

**Architecture Implications:**
- Running CLI on Windows: ✅ Simple, works perfectly
- Running CLI in WSL: ⚠️ Possible, needs path mapping for IDE data access
- **Chosen approach:** Run on Windows (simpler, no path mapping needed)

### SSH Remote

**Workspace URI format:**
- `vscode-remote://ssh-remote+config/home/user/code/myproject`
- `vscode-remote://ssh-remote%2B7b22686f73744e616d65223a22736572766572227d/path`

**Filesystem Access:**
- ❌ Remote cannot access Windows filesystem
- ❌ Windows cannot directly access remote filesystem (needs SSH)
- ❌ No bidirectional filesystem access without SSH/network protocol

**Storage Location:**
- IDE tools: Always on Windows client (`%APPDATA%`)
- CLI tools: On remote machine (`~/.claude`, `~/.cursor`, etc.)

**Architecture Implications:**
- Running CLI on Windows: ✅ Accesses IDE data, ❌ Cannot access remote CLI data
- Running CLI on remote: ❌ Cannot access IDE data, ✅ Accesses remote CLI data
- **Problem:** No single execution location accesses everything
- **Chosen approach:** Run on Windows for IDE data (primary use case), accept limitation on remote CLI data

### Comparison Table

| Aspect | WSL | SSH |
|--------|-----|-----|
| **Filesystem access** | Bidirectional | Requires SSH protocol |
| **IDE data from remote** | Possible via `/mnt/c/...` | Impossible without SSH |
| **CLI execution location** | Windows or WSL (both work) | Windows or remote (neither perfect) |
| **Complexity** | Low (shared filesystem) | High (network boundary) |
| **Our choice** | Run on Windows | Run on Windows |
| **Trade-off** | Could run in WSL but unnecessary | Must accept missing remote CLI data |

## CLI Flags for Extension Integration

### `--output-dir <path>` (existing)

**Purpose:** Export markdown history files to a specific directory instead of `.specstory/history/`.

**Limitation:** Only controls where `history/*.md` files are written. `.project.json` is still written relative to the working directory at `{cwd}/.specstory/.project.json`. This makes `--output-dir` alone insufficient for the remote workspace use case, where the extension needs to intercept **all** CLI output files via a temp directory.

### `--specstory-dir <path>` (planned)

**Purpose:** Redirect all `.specstory/` output — both `.project.json` and `history/*.md` — to a specific directory. This is the flag the extension will use for remote workspaces.

**Use case:** Extension wants to capture all CLI output in a temp directory, then copy everything to the final remote destination via the VS Code FS API.

**Behaviour when set:**
- `.project.json` → `{specstory-dir}/.project.json`
- `history/*.md` → `{specstory-dir}/history/*.md`
- `--output-dir` can still override the history path further if needed (takes precedence)

**Example:**
```zsh
specstory sync --specstory-dir C:\Temp\specstory-{workspace-hash}
```

**Implementation plan:**
1. Add `specstoryDir` global variable and register `--specstory-dir` flag on `sync`, `run`, `watch`
2. Thread through `SetupOutputConfig()` in `pkg/utils/path_utils.go`
3. In `pkg/utils/project_identity.go`, accept an override base directory so `.project.json` resolves to `{specstory-dir}/.project.json` instead of `{cwd}/.specstory/.project.json`

## Future Enhancements

### Accessing Remote CLI/Agent Data

To access Claude Code conversations created on SSH remote machines:

**Option 1: Remote CLI execution**
- Extension spawns CLI on remote via SSH
- Remote CLI exports local agent data
- Extension fetches via SSH/SCP
- Complex but comprehensive

**Option 2: Agent-specific sync**
- Each agent tool provides its own sync mechanism
- Extension uses agent's API to fetch remote data
- Cleaner separation of concerns

**Option 3: Hybrid approach**
- Windows CLI exports IDE data (current approach)
- Separate command to fetch remote agent data via SSH
- Merge results in extension

### `--workspace-uri <uri>` Flag

**Purpose:** Precisely filter to a single workspace when multiple workspaces have the same folder name.

**Use case:**
- User has `myproject` locally, in WSL, and on SSH remote (all same name)
- Extension only wants conversations from the current workspace
- Not from all workspaces with that name

**Example:**
```zsh
specstory list \
    --workspace-uri "vscode-remote://ssh-remote+server/home/user/code/myproject"
```

**Implementation:**
1. Find all workspaces whose URI matches the provided URI
2. Return conversations from that specific workspace

## Summary

### Architecture Decisions

1. **Run CLI on Windows client** - Direct access to IDE data (primary use case)
2. **Two write modes based on workspace type:**
   - **Local**: pass `--specstory-dir {workspace}/.specstory` — CLI writes directly, no copying
   - **Remote (SSH)**: pass `--specstory-dir {tempDir}` — CLI writes to persistent temp, extension copies via VS Code FS API
3. **Persistent temp dir for remote only** - Must persist across runs so CLI can read back `.project.json` and skip unchanged `.md` files
4. **Extension bootstraps identity for remote** - Copies remote `.project.json` into temp dir on first use so CLI maintains a consistent project identity
5. **Extension watches files** - VS Code FS API handles cross-platform/remote writes
6. **Basename matching** - Simpler than full path, works across environments

### Trade-offs

**What we gain:**
- ✅ Simple, maintainable architecture
- ✅ Direct access to Cursor IDE and Copilot IDE data
- ✅ Works with local, WSL, and SSH workspaces
- ✅ No SSH credential management
- ✅ Single code path for all workspace types

**What we sacrifice:**
- ❌ Cannot access CLI/Agent tools on remote SSH machines
- ❌ Cannot access CLI/Agent tools in WSL (unless run from WSL)
- ⚠️ Slight performance overhead from temp directory + copy

**Justification:**
- Most users primarily use IDE tools (Cursor IDE, Copilot IDE)
- These are the tools with richer UIs and more common usage
- CLI/Agent tools on remotes are edge cases
- Can be addressed with future enhancements if needed
