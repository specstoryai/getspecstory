package spi

import (
	"slices"
	"strings"
)

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
// subcommand rather than a flag (Codex, Muse); providers using a flag build
// their own arguments.
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
