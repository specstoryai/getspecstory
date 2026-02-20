# Plan: Shared Shell Command Path Extraction

## Context

All 5 providers (Claude Code, Codex CLI, Cursor CLI, Gemini CLI, Droid CLI) have shell-type tool use, but **none of them extract file paths from the actual shell command text**. Each provider's `extractPathHints()` only checks named input fields (`file_path`, `path`, etc.) which are irrelevant for shell tools -- the paths are embedded in the command text itself (`input["command"]` for all providers, also `input["cmd"]` for Droid CLI).

The primary value: extracting file paths from **redirect operators** (`>`, `>>`) and **file-creating commands** (`touch`, `mkdir`, `tee`, `cp` dest, `mv` dest, build output flags like `-o`). We don't care about read-only commands like `cat`, `head`, `grep`.

There's a failing test (`TestExtractPathHints_ShellCommand` in `pkg/providers/claudecode/agent_session_test.go:186`) that attempted this at the provider level -- it should be removed once the SPI-level tests cover this comprehensively.

## Part 1: New File -- `pkg/spi/shell_path_hints.go`

### Public API

```go
// ExtractShellPathHints parses a shell command and extracts file paths that
// indicate files being created or modified (redirect targets, file-creating commands).
func ExtractShellPathHints(command, cwd, workspaceRoot string) []string

// NormalizePath expands tilde, and converts absolute paths to workspace-relative
// paths when they fall under workspaceRoot.
func NormalizePath(path, workspaceRoot string) string
```

### Extraction Strategy

Focus on file-creation/modification patterns, not read-only access:

**1. Redirect targets (primary)** -- always indicate file creation/modification:

- `> file.txt`, `>> file.txt` (stdout redirect/append)
- `2> error.log`, `2>> error.log` (stderr redirect)
- `&> all.log`, `&>> all.log` (combined redirect)
- Handle both attached (`>file.txt`) and separated (`> file.txt`) forms

**2. File-creating commands (secondary)** -- positional args are files being created:

- `touch file1.txt file2.txt` -- all args are files
- `mkdir -p src/components/ui` -- all non-flag args are directories
- `tee output.txt` -- all non-flag args are files being written
- `cp src.txt dest.txt` -- last arg is destination being created
- `mv old.txt new.txt` -- last arg is destination being created
- `ln -s target link` -- last arg is link being created

**3. Build output flags** -- the `-o` flag value is a file being created:

- `go build -o specstory`
- `gcc -o output input.c`

**4. Not extracted** (read-only, don't create files):

- `cat`, `head`, `tail`, `less`, `more`, `wc`, `grep`, `find`, `ls`, `diff`
- `echo "text"` without redirect (echo content is not a file path)
- `git log`, `git status`, `git diff` (read-only git ops)

### Parsing Algorithm

1. Split multi-line commands on `\n`, process each line
2. Detect heredoc markers (`<<EOF`), skip heredoc content lines
3. Split on shell operators (`|`, `&&`, `||`, `;`) respecting quotes
4. For each sub-command:
   a. Scan for redirect operators, extract the target path
   b. Tokenize with `SplitCommandLine()` (from `pkg/spi/cmdline.go`)
   c. Identify the command name (first non-env-var token)
   d. If command is in the file-creating table, extract relevant positional args
   e. Skip flags (`-*`), URLs (`http://`/`https://`), env vars (`$VAR`), pure numbers
5. Resolve paths: resolve relative paths against CWD, then `NormalizePath` (which handles tilde expansion and workspace normalization)

### Path Resolution (when CWD provided)

- `file.txt` → `filepath.Join(cwd, "file.txt")` → normalize against workspaceRoot
- `./file.txt` → `filepath.Join(cwd, "./file.txt")` → normalize
- `../file.txt` → `filepath.Clean(filepath.Join(cwd, "../file.txt"))` → normalize
- `/abs/path` → normalize against workspaceRoot directly
- `~/file.txt` → `NormalizePath` expands tilde then normalizes against workspaceRoot

When no CWD field exists in tool input, fall back to workspaceRoot.

## Part 2: New Test File -- `pkg/spi/shell_path_hints_test.go`

Comprehensive table-driven tests covering:

**Redirect operations (separated `> file` form):**

- `echo "text" > /abs/path/file.txt` (absolute, workspace-normalized)
- `echo "text" > ./rel/file.txt` (relative with ./)
- `echo "text" > file.txt` (bare filename)
- `echo "text" >> log.txt` (append redirect)
- `command 2> error.log` (stderr redirect)
- `command &> all.log` (combined redirect)
- `echo "text" > "path with spaces.txt"` (quoted redirect target)
- `echo "text" > ~/project/file.txt` (tilde path)
- `echo "text" > ../other/file.txt` (parent directory)

**Redirect operations (attached `>file` form, no space):**

- `echo "text" >file.txt` (bare filename, no space)
- `echo "text" >>log.txt` (append, no space)
- `command 2>error.log` (stderr, no space)
- `command &>all.log` (combined, no space)
- `echo "text" >/abs/path/file.txt` (absolute, no space)
- `echo "text" >./rel/file.txt` (relative with ./, no space)

**File-creating commands:**

- `touch new_file.ts`
- `touch file1.txt file2.txt file3.go` (multiple files)
- `mkdir -p src/components/ui`
- `cp src/main.go backup/main.go` (destination path)
- `mv old.go new.go` (destination path)
- `tee output.txt` (tee target)
- `ln -s /target link_name`

**Build output:**

- `go build -o specstory`
- `go build -o ./bin/specstory`
- `gcc -o output input.c`

**Pipes with redirects:**

- `grep pattern src/ | tee results.txt` (tee creates file)
- `cat input.txt | sort > sorted.txt` (redirect creates file)

**Command chaining:**

- `mkdir -p build && cp src/main.go build/` (both create)
- `touch a.txt; touch b.txt` (both create)
- `cd /tmp && echo "hi" > out.txt` (redirect in chained command)

**CWD resolution:**

- Bare path `file.txt` with CWD `/project/src` and workspaceRoot `/project` → `src/file.txt`
- `./file.txt` with CWD resolved → workspace-relative
- `../config.json` with CWD `/project/src` → `config.json`

**Tilde expansion:**

- `touch ~/project/file.txt` → expanded absolute path

**Workspace normalization:**

- Absolute path under workspaceRoot → relative path

**Edge cases / no false positives:**

- Empty command → no paths
- `ls -la` → no paths (read-only)
- `echo "hello world"` → no paths (no redirect)
- `cat file.txt` → no paths (read-only)
- `curl https://example.com` → no paths (URL, not file)
- `git commit -m "fix bug"` → no paths
- `export FOO=bar` → no paths
- `pwd` → no paths

**Multi-line:**

- `touch a.txt\ntouch b.txt` → two paths

**Heredoc:**

- `cat <<EOF > output.txt\ncontent\nEOF` → `output.txt` (redirect target, skip heredoc body)

## Part 3: Provider Integration (5 files)

Each provider's `extractPathHints` adds shell command path extraction. The command field is `"command"` across all providers (Droid CLI also checks `"cmd"` as a fallback).

### 1. Claude Code -- `pkg/providers/claudecode/agent_session.go:418-452`

Remove `TestExtractPathHints_ShellCommand` from `agent_session_test.go:186-227` (replaced by SPI-level tests).

Add to `extractPathHints()` after existing field checks:

```go
if command, ok := input["command"].(string); ok && command != "" {
    shellPaths := spi.ExtractShellPathHints(command, workspaceRoot, workspaceRoot)
    // append unique paths
}
```

### 2. Codex CLI -- `pkg/providers/codexcli/agent_session.go:541-566`

Add in the else branch:

```go
if command, ok := input["command"].(string); ok && command != "" {
    cwd, _ := input["workdir"].(string)
    if cwd == "" { cwd = workspaceRoot }
    shellPaths := spi.ExtractShellPathHints(command, cwd, workspaceRoot)
    // append unique paths
}
```

### 3. Cursor CLI -- `pkg/providers/cursorcli/agent_session.go:389-421`

Add to `extractCursorPathHints()`:

```go
if command, ok := input["command"].(string); ok && command != "" {
    shellPaths := spi.ExtractShellPathHints(command, workspaceRoot, workspaceRoot)
    // append unique paths
}
```

### 4. Gemini CLI -- `pkg/providers/geminicli/agent_session.go:321-337`

Add to `extractPathHintsFromTool()`:

```go
if command := inputAsString(toolCall.Args, "command"); command != "" {
    cwd := inputAsString(toolCall.Args, "dir_path")
    if cwd == "" { cwd = workspaceRoot }
    shellPaths := spi.ExtractShellPathHints(command, cwd, workspaceRoot)
    // append unique paths
}
```

### 5. Droid CLI -- `pkg/providers/droidcli/agent_session.go:324-350`

Add after existing field checks. Droid checks `"workdir"` then `"cwd"` for the shell working directory:

```go
if command, ok := input["command"].(string); ok && command != "" {
    cwd, _ := input["workdir"].(string)
    if cwd == "" { cwd, _ = input["cwd"].(string) }
    if cwd == "" { cwd = workspaceRoot }
    shellPaths := spi.ExtractShellPathHints(command, cwd, workspaceRoot)
    // append unique paths
}
```

## Part 4: Consolidate `NormalizePath` (DRY cleanup)

Replace the 5 identical provider-local functions with calls to `spi.NormalizePath()`:

| Provider    | Current function           | File:Line              |
|-------------|----------------------------|------------------------|
| Claude Code | `normalizePath()`          | `agent_session.go:455` |
| Codex CLI   | `normalizePath()`          | `agent_session.go:616` |
| Cursor CLI  | `normalizeCursorPath()`    | `agent_session.go:424` |
| Gemini CLI  | `normalizeGeminiPath()`    | `agent_session.go:340` |
| Droid CLI   | `normalizeWorkspacePath()` | `agent_session.go:366` |

Each gets replaced with `spi.NormalizePath(path, workspaceRoot)`. The `contains()`/`containsPath()` helpers are also duplicated but can stay provider-local since they're trivial.

## Reuse Existing Code

- `pkg/spi/cmdline.go` -- `SplitCommandLine()` for shell tokenization (handles quotes, escapes)
- `pkg/spi/path_utils.go` -- `GetCanonicalPath()` available if needed

## Verification

```zsh
# Run the new shared function tests
go test -v ./pkg/spi/ -run TestExtractShellPathHints

# Run all tests for regressions (includes verifying removal of old Claude Code test)
go test -v ./...

# Run the linter
golangci-lint run
```
