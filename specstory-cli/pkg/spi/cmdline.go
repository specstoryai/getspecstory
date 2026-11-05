package spi

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// AgentWorkingDir returns the directory a launched agent runs in, so anything
// observing the agent's work looks where RunInProject puts it. --project-path
// need not name a local directory: it can be a remote workspace's path or a
// .code-workspace file. No process can run there, so the agent stays in
// launchDir. A relative path counts as remote: on Windows \home\user is not
// absolute, and statting it would find an unrelated directory on the current
// drive.
func AgentWorkingDir(projectPath, launchDir string) string {
	if info, err := os.Stat(projectPath); err == nil && filepath.IsAbs(projectPath) && info.IsDir() {
		return projectPath
	}
	return launchDir
}

// RunningAgentDir returns the directory of an agent launched from this process,
// which is what its watcher and exit sweep must follow: the agent records that
// directory on its sessions, so with a non-local --project-path a watcher on
// projectPath would save nothing and report no error.
func RunningAgentDir(projectPath string) string {
	launchDir, err := os.Getwd()
	if err != nil {
		slog.Warn("RunningAgentDir: Failed to read the working directory", "error", err)
		return projectPath
	}
	return AgentWorkingDir(projectPath, CanonicalizePathOrClean(launchDir))
}

// RunInProject makes cmd run in the project directory. Agents key their sessions
// to the directory they run in, so with --project-path a child left in the
// launch directory writes sessions the watcher never looks at. Moving the child
// changes what relative paths mean, so anything resolved from the launch
// directory is made absolute first: a relative command, which exec would
// otherwise look for under Dir, and pathEnvVars, the variables naming a storage
// directory the provider reads from the same place the agent writes. A value
// starting with ~ is left for the reader to expand.
func RunInProject(cmd *exec.Cmd, projectPath string, pathEnvVars ...string) {
	if AgentWorkingDir(projectPath, "") == "" {
		if projectPath != "" {
			slog.Warn("RunInProject: not a local directory, running the agent in the launch directory",
				"projectPath", projectPath)
		}
		return
	}
	if cmd.Path != filepath.Base(cmd.Path) && !filepath.IsAbs(cmd.Path) {
		if abs, err := filepath.Abs(cmd.Path); err == nil {
			cmd.Path = abs
		}
	}
	cmd.Dir = projectPath
	for _, key := range pathEnvVars {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "~") {
			continue
		}
		if abs, err := filepath.Abs(value); err == nil {
			// os/exec keeps the last occurrence of a duplicated key.
			cmd.Env = append(cmd.Environ(), key+"="+abs)
		}
	}
}

// SplitCommandLine splits a command line string into arguments, respecting quoted strings.
//
// Supports both single and double quotes. Quotes can be escaped with backslash.
// This is used by providers to parse custom command strings that may contain paths
// or arguments with spaces.
//
// Examples:
//   - "claude --debug" -> ["claude", "--debug"]
//   - `claude --config "~/My Settings/config.json"` -> ["claude", "--config", "~/My Settings/config.json"]
//   - `claude --msg 'It'\”s working'` -> ["claude", "--msg", "It's working"]
//   - `claude --path "C:\\Users\\test"` -> ["claude", "--path", "C:\Users\test"]
//
// Behavior:
//   - Single and double quotes are treated equivalently
//   - Backslash escapes the next character (including quotes and backslashes)
//   - Whitespace (space, tab, newline) outside quotes separates arguments
//   - Empty quoted strings are ignored (e.g., `cmd "" --arg` -> ["cmd", "--arg"])
//   - Unclosed quotes consume to end of string
func SplitCommandLine(s string) []string {
	var args []string
	var current strings.Builder
	var inQuote rune // ' or " when inside quotes, 0 otherwise
	var escaped bool

	for _, r := range s {
		if escaped {
			// Previous character was backslash, add this character literally
			current.WriteRune(r)
			escaped = false
			continue
		}

		if r == '\\' {
			// Next character will be escaped
			escaped = true
			continue
		}

		if inQuote != 0 {
			// Inside quotes
			if r == inQuote {
				// End quote
				inQuote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}

		// Not inside quotes
		if r == '"' || r == '\'' {
			// Start quote
			inQuote = r
			continue
		}

		if r == ' ' || r == '\t' || r == '\n' {
			// Whitespace outside quotes - end of argument
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}

		// Regular character
		current.WriteRune(r)
	}

	// Add final argument if any
	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args
}

// EnsureResumeArgs makes sure a parsed command line carries `<subcommand> <id>`
// for the session the caller asked for. Shared by the agents that resume via a
// subcommand rather than a flag (Codex, Muse); flag-based agents use
// EnsureResumeFlagArgs.
//
// A custom command that already names the subcommand keeps its position — the
// id is filled in after it, replacing one the command already carried — rather
// than gaining a second one; any other custom subcommand is left alone and the
// pair is appended, which is the only thing that can be done without
// second-guessing the user.
//
// The caller's slice is never mutated: providers hand in a sub-slice of their
// parsed command line, which appending to in place would corrupt.
func EnsureResumeArgs(args []string, subcommand string, resumeSessionID string) []string {
	if resumeSessionID == "" {
		return args
	}

	for i, arg := range args {
		if arg != subcommand {
			continue
		}
		// A configured command can pin an id of its own, but it describes a
		// default while resumeSessionID describes this run: resuming the pinned
		// session instead would silently open a different conversation than the
		// one asked for. Provider requires the requested session to win.
		if i+1 < len(args) && strings.TrimSpace(args[i+1]) != "" && !strings.HasPrefix(args[i+1], "-") {
			replaced := slices.Clone(args)
			replaced[i+1] = resumeSessionID
			return replaced
		}
		// slices.Concat always allocates a new backing array.
		return slices.Concat(args[:i+1], []string{resumeSessionID}, args[i+1:])
	}

	return slices.Concat(args, []string{subcommand, resumeSessionID})
}

// EnsureResumeFlagArgs gives an explicit session precedence over configured
// flags, including aliases and repeated flags. It preserves arguments after
// -- as positional input and never modifies the caller's backing array.
func EnsureResumeFlagArgs(args []string, sessionID string, flags ...string) []string {
	if sessionID == "" || len(flags) == 0 {
		return args
	}
	result := slices.Clone(args)
	found := false
	i := 0
	for ; i < len(result); i++ {
		arg := result[i]
		if arg == "--" {
			break
		}
		flag, _, assigned := strings.Cut(arg, "=")
		if !slices.Contains(flags, flag) {
			continue
		}
		found = true
		if assigned {
			result[i] = flag + "=" + sessionID
		} else {
			if i+1 < len(result) && !strings.HasPrefix(result[i+1], "-") {
				result[i+1] = sessionID
			} else {
				result = slices.Insert(result, i+1, sessionID)
			}
			i++
		}
	}
	if !found {
		result = slices.Insert(result, i, flags[0], sessionID)
	}
	return result
}
