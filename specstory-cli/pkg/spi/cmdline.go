package spi

import "strings"

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
