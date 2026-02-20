package spi

import (
	"os"
	"path/filepath"
	"strings"
)

// NormalizePath converts absolute paths to workspace-relative paths when they
// fall under workspaceRoot. This consolidates the identical normalization logic
// previously duplicated across all 5 providers.
func NormalizePath(path, workspaceRoot string) string {
	if workspaceRoot == "" {
		return path
	}

	// If path is absolute and starts with workspace root, make it relative
	if filepath.IsAbs(path) && strings.HasPrefix(path, workspaceRoot) {
		relPath, err := filepath.Rel(workspaceRoot, path)
		if err == nil {
			return relPath
		}
	}

	return path
}

// expandTilde replaces a leading ~/ with the user's home directory.
// Returns the path unchanged if it doesn't start with ~/ or if the
// home directory can't be determined.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}

	return filepath.Join(home, path[2:])
}

// ExtractShellPathHints parses a shell command and extracts file paths that
// indicate files being created or modified. This covers redirect targets
// (>, >>), file-creating commands (touch, mkdir, etc.), and build output flags (-o).
//
// Read-only commands (cat, grep, ls, etc.) are intentionally ignored since
// we only care about paths where content is being written.
func ExtractShellPathHints(command, cwd, workspaceRoot string) []string {
	if command == "" {
		return nil
	}

	var paths []string
	seen := make(map[string]bool)

	addPath := func(raw string) {
		if raw == "" {
			return
		}
		resolved := resolvePath(raw, cwd, workspaceRoot)
		if resolved == "" {
			return
		}
		if !seen[resolved] {
			seen[resolved] = true
			paths = append(paths, resolved)
		}
	}

	// Split multi-line commands and process each line, handling heredocs
	lines := strings.Split(command, "\n")
	var inHeredoc bool
	var heredocMarker string

	for _, line := range lines {
		// Handle heredoc body: skip lines until we hit the end marker
		if inHeredoc {
			trimmed := strings.TrimSpace(line)
			if trimmed == heredocMarker {
				inHeredoc = false
			}
			continue
		}

		// Split line on shell operators (|, &&, ||, ;) respecting quotes
		subCommands := splitOnShellOperators(line)

		for _, sub := range subCommands {
			sub = strings.TrimSpace(sub)
			if sub == "" {
				continue
			}

			// Extract redirect targets before tokenizing (they're the primary signal)
			redirectPaths, remaining, heredocEnd := extractRedirects(sub)
			for _, rp := range redirectPaths {
				addPath(rp)
			}

			// If we found a heredoc marker, enter heredoc mode
			if heredocEnd != "" {
				inHeredoc = true
				heredocMarker = heredocEnd
			}

			// Tokenize the remaining command (without redirect parts)
			tokens := SplitCommandLine(remaining)
			if len(tokens) == 0 {
				continue
			}

			// Find the actual command name (skip leading env var assignments)
			cmdIdx := findCommandIndex(tokens)
			if cmdIdx >= len(tokens) {
				continue
			}
			cmdName := filepath.Base(tokens[cmdIdx])
			rawArgs := tokens[cmdIdx+1:]

			// Check for -o output flag before filtering (build tools like go build, gcc)
			if oPath := extractOutputFlag(rawArgs); oPath != "" {
				addPath(oPath)
				continue
			}

			// Handle sed -i specially: only writes when -i flag is present,
			// first positional arg is the expression, rest are file paths
			if cmdName == "sed" {
				if sedPaths := extractSedInPlacePaths(rawArgs); len(sedPaths) > 0 {
					for _, sp := range sedPaths {
						addPath(sp)
					}
				}
				continue
			}

			args := filterArgs(rawArgs)

			// Extract paths from file-creating commands
			createdPaths := extractCreatedPaths(cmdName, args)
			for _, cp := range createdPaths {
				addPath(cp)
			}
		}
	}

	return paths
}

// fileCreatingCommands maps command names to how their positional args should be
// interpreted. "all" means every non-flag arg is a file being created; "last"
// means only the last arg is the destination being created.
var fileCreatingCommands = map[string]string{
	"touch": "all",
	"mkdir": "all",
	"tee":   "all",
	"cp":    "last",
	"mv":    "last",
	"ln":    "last",
}

// extractSedInPlacePaths handles `sed -i` which modifies files in-place.
// Without -i, sed is read-only (stdout). With -i, the trailing arguments
// that look like file paths are files being modified.
//
// Rather than trying to parse sed's complex arg format (macOS vs Linux -i
// differences, expressions vs paths), we use a heuristic: collect all
// non-flag positional args and keep only those that look like file paths
// (not sed expressions).
func extractSedInPlacePaths(rawArgs []string) []string {
	// Only extract paths when -i flag is present (in-place edit)
	hasInPlace := false
	for _, arg := range rawArgs {
		if arg == "-i" || strings.HasPrefix(arg, "-i.") {
			hasInPlace = true
			break
		}
	}
	if !hasInPlace {
		return nil
	}

	// Collect non-flag positional args, skipping -e/-f values
	var positional []string
	skipNext := false
	for _, arg := range rawArgs {
		if skipNext {
			skipNext = false
			continue
		}
		// -e and -f consume the next arg as expression/file
		if arg == "-e" || arg == "-f" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		positional = append(positional, arg)
	}

	// Keep only args that look like file paths, not sed expressions
	var paths []string
	for _, arg := range positional {
		if !looksLikeSedExpression(arg) {
			paths = append(paths, arg)
		}
	}
	return paths
}

// looksLikeSedExpression returns true if the string looks like a sed expression
// rather than a file path. Sed expressions typically start with s/ or y/ (substitution),
// are numeric addresses, or are single-letter commands.
func looksLikeSedExpression(s string) bool {
	if s == "" {
		return false
	}
	// s/.../ substitution or y/.../ transliterate with any delimiter
	if len(s) >= 2 && (s[0] == 's' || s[0] == 'y') {
		d := s[1]
		if d == '/' || d == '|' || d == '#' || d == ',' || d == ':' {
			return true
		}
	}
	// Address patterns like /regex/d — exactly 2 slashes with a short suffix.
	// Distinguish from file paths like /Users/foo/bar.py by checking the
	// content after the second slash is a single sed command character (or empty).
	if s[0] == '/' {
		secondSlash := strings.Index(s[1:], "/")
		if secondSlash >= 0 {
			afterPattern := s[1+secondSlash+1:]
			// Sed address: /regex/ or /regex/d — short suffix, no path separators
			if len(afterPattern) <= 1 && !strings.Contains(afterPattern, "/") {
				return true
			}
		}
	}
	// Numeric address or range: "3", "1,5", "$"
	if s == "$" {
		return true
	}
	allDigitsOrComma := true
	for _, r := range s {
		if r != ',' && (r < '0' || r > '9') {
			allDigitsOrComma = false
			break
		}
	}
	if allDigitsOrComma {
		return true
	}
	// Single-letter sed commands
	if len(s) == 1 && strings.ContainsRune("dpqGHNPx", rune(s[0])) {
		return true
	}
	return false
}

// extractOutputFlag checks raw args for -o <path> (used by build tools).
// Returns the output path or empty string.
func extractOutputFlag(args []string) string {
	for i, arg := range args {
		if arg == "-o" && i+1 < len(args) {
			return args[i+1]
		}
		// Handle -o attached to value: -ooutput
		if len(arg) > 2 && arg[0] == '-' && arg[1] == 'o' && arg[2] != '-' {
			return arg[2:]
		}
	}
	return ""
}

// extractCreatedPaths returns file paths from commands that create/modify files.
func extractCreatedPaths(cmdName string, args []string) []string {
	if len(args) == 0 {
		return nil
	}

	mode, ok := fileCreatingCommands[cmdName]
	if !ok {
		return nil
	}

	switch mode {
	case "all":
		return args
	case "last":
		if len(args) >= 2 {
			return []string{args[len(args)-1]}
		}
	}

	return nil
}

// filterArgs removes flags, URLs, env vars, and numeric-only tokens from an
// argument list, returning only potential file path arguments.
func filterArgs(tokens []string) []string {
	var args []string
	skipNext := false

	for _, tok := range tokens {
		if skipNext {
			skipNext = false
			continue
		}

		// Skip flags
		if strings.HasPrefix(tok, "-") {
			// Flags that consume the next token as their value
			if tok == "-o" || tok == "-t" || tok == "-m" {
				skipNext = true
			}
			continue
		}

		// Skip URLs
		if strings.HasPrefix(tok, "http://") || strings.HasPrefix(tok, "https://") {
			continue
		}

		// Skip env var references
		if strings.HasPrefix(tok, "$") {
			continue
		}

		// Skip pure numbers
		if isNumeric(tok) {
			continue
		}

		args = append(args, tok)
	}

	return args
}

// findCommandIndex returns the index of the first token that isn't an
// environment variable assignment (KEY=VALUE).
func findCommandIndex(tokens []string) int {
	for i, tok := range tokens {
		if !strings.Contains(tok, "=") || strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, ".") {
			return i
		}
	}
	return len(tokens)
}

// splitOnShellOperators splits a command line on |, &&, ||, and ; while
// respecting quoted strings. Returns individual sub-commands.
func splitOnShellOperators(line string) []string {
	var parts []string
	var current strings.Builder
	var inQuote rune
	var escaped bool

	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}

		if r == '\\' {
			current.WriteRune(r)
			escaped = true
			continue
		}

		if inQuote != 0 {
			current.WriteRune(r)
			if r == inQuote {
				inQuote = 0
			}
			continue
		}

		if r == '"' || r == '\'' {
			current.WriteRune(r)
			inQuote = r
			continue
		}

		// Check for operators
		if r == ';' {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}

		if r == '|' {
			if i+1 < len(runes) && runes[i+1] == '|' {
				// || operator
				parts = append(parts, current.String())
				current.Reset()
				i++ // skip second |
				continue
			}
			// pipe |
			parts = append(parts, current.String())
			current.Reset()
			continue
		}

		if r == '&' {
			if i+1 < len(runes) && runes[i+1] == '&' {
				// && operator
				parts = append(parts, current.String())
				current.Reset()
				i++ // skip second &
				continue
			}
			// single & (background) - just include it in current
			current.WriteRune(r)
			continue
		}

		current.WriteRune(r)
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

// extractRedirects scans a command string for redirect operators and extracts
// the target file paths. Returns the extracted paths, the command with redirect
// parts removed, and any heredoc end marker found.
func extractRedirects(cmd string) (paths []string, remaining string, heredocMarker string) {
	var result strings.Builder
	runes := []rune(cmd)
	i := 0
	var inQuote rune
	var escaped bool

	for i < len(runes) {
		r := runes[i]

		if escaped {
			result.WriteRune(r)
			escaped = false
			i++
			continue
		}

		if r == '\\' {
			result.WriteRune(r)
			escaped = true
			i++
			continue
		}

		if inQuote != 0 {
			result.WriteRune(r)
			if r == inQuote {
				inQuote = 0
			}
			i++
			continue
		}

		if r == '"' || r == '\'' {
			result.WriteRune(r)
			inQuote = r
			i++
			continue
		}

		// Check for heredoc operator <<
		if r == '<' && i+1 < len(runes) && runes[i+1] == '<' {
			// Skip the << and read the heredoc marker, then continue scanning
			// the rest of the line for redirects (e.g., "cat <<EOF > output.txt")
			j := i + 2
			// Skip optional - (for <<-)
			if j < len(runes) && runes[j] == '-' {
				j++
			}
			// Skip whitespace
			for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
				j++
			}
			// Read the marker (strip quotes if present)
			marker := readHeredocMarker(runes, j)
			if marker != "" {
				heredocMarker = marker
			}
			// Skip past the marker token
			_, afterMarker := readToken(runes, j)
			i = afterMarker
			continue
		}

		// Detect redirect operators: >, >>, 2>, 2>>, &>, &>>
		isRedirect, skipLen, isAppend := detectRedirect(runes, i)
		_ = isAppend // both > and >> indicate file creation

		if isRedirect {
			// Skip the redirect operator
			j := i + skipLen

			// Skip whitespace between operator and target
			for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
				j++
			}

			// Read the redirect target (file path)
			target, endIdx := readToken(runes, j)
			if target != "" {
				paths = append(paths, target)
			}
			i = endIdx
			continue
		}

		result.WriteRune(r)
		i++
	}

	return paths, result.String(), heredocMarker
}

// detectRedirect checks if position i in runes is the start of a redirect
// operator. Returns whether it's a redirect, how many runes the operator
// consumes, and whether it's an append (>>).
func detectRedirect(runes []rune, i int) (isRedirect bool, skipLen int, isAppend bool) {
	n := len(runes)
	r := runes[i]

	// &> or &>>
	if r == '&' && i+1 < n && runes[i+1] == '>' {
		if i+2 < n && runes[i+2] == '>' {
			return true, 3, true // &>>
		}
		return true, 2, false // &>
	}

	// 2> or 2>>
	if r == '2' && i+1 < n && runes[i+1] == '>' {
		if i+2 < n && runes[i+2] == '>' {
			return true, 3, true // 2>>
		}
		return true, 2, false // 2>
	}

	// > or >>
	if r == '>' {
		if i+1 < n && runes[i+1] == '>' {
			return true, 2, true // >>
		}
		return true, 1, false // >
	}

	return false, 0, false
}

// readToken reads a single token starting at position i, handling quoted strings.
// Returns the token value (without quotes) and the index after the token.
func readToken(runes []rune, i int) (string, int) {
	n := len(runes)
	if i >= n {
		return "", i
	}

	// Handle quoted token
	if runes[i] == '"' || runes[i] == '\'' {
		quote := runes[i]
		i++ // skip opening quote
		var tok strings.Builder
		for i < n && runes[i] != quote {
			if runes[i] == '\\' && i+1 < n {
				i++ // skip backslash
				tok.WriteRune(runes[i])
			} else {
				tok.WriteRune(runes[i])
			}
			i++
		}
		if i < n {
			i++ // skip closing quote
		}
		return tok.String(), i
	}

	// Unquoted token: read until whitespace or shell metacharacter
	var tok strings.Builder
	for i < n {
		r := runes[i]
		if r == ' ' || r == '\t' || r == '\n' || r == '|' || r == ';' || r == '&' || r == '>' || r == '<' {
			break
		}
		tok.WriteRune(r)
		i++
	}
	return tok.String(), i
}

// readHeredocMarker reads the heredoc end marker starting at position i,
// stripping any surrounding quotes.
func readHeredocMarker(runes []rune, i int) string {
	n := len(runes)
	if i >= n {
		return ""
	}

	// Handle quoted marker
	if runes[i] == '"' || runes[i] == '\'' {
		quote := runes[i]
		i++
		var marker strings.Builder
		for i < n && runes[i] != quote {
			marker.WriteRune(runes[i])
			i++
		}
		return marker.String()
	}

	// Unquoted marker: read until whitespace
	var marker strings.Builder
	for i < n && runes[i] != ' ' && runes[i] != '\t' && runes[i] != '\n' {
		marker.WriteRune(runes[i])
		i++
	}
	return marker.String()
}

// resolvePath expands tildes, resolves relative paths against cwd, and
// normalizes against workspaceRoot.
func resolvePath(raw, cwd, workspaceRoot string) string {
	if raw == "" {
		return ""
	}

	path := raw

	// Expand tilde
	path = expandTilde(path)

	// Resolve relative paths against cwd
	if !filepath.IsAbs(path) && cwd != "" {
		path = filepath.Clean(filepath.Join(cwd, path))
	}

	// Normalize against workspace root
	return NormalizePath(path, workspaceRoot)
}

// isNumeric returns true if the string contains only digits.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
