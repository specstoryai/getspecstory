package spi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// markdownHeadingRe matches a leading Markdown ATX heading marker after outer
// whitespace has been trimmed: 1-6 '#' characters followed by whitespace. The
// trailing whitespace requirement is what distinguishes a real heading ("# Title")
// from hashtag-like text ("#login issue"), which must be left untouched.
var markdownHeadingRe = regexp.MustCompile(`^#{1,6}\s+`)

// stripMarkdownHeading removes a leading markdown heading marker (e.g. "# Title" → "Title").
// These are structural formatting, not meaningful slug content.
func stripMarkdownHeading(message string) string {
	trimmed := strings.TrimSpace(message)
	if markdownHeadingRe.MatchString(trimmed) {
		trimmed = markdownHeadingRe.ReplaceAllString(trimmed, "")
		trimmed = strings.TrimSpace(trimmed)
	}
	return trimmed
}

// xmlTagsRe matches XML-style tag blocks including their content, such as
// <ide_opened_file>...</ide_opened_file> or self-closing <tag />.
// These are injected by IDE tools/system prompts and should be stripped before
// processing user message text for filename or display purposes.
//
// The regex has two alternations:
//   - <tag ...>content</tag>  — matched with (?s) so . spans newlines; uses .*? (lazy)
//     to consume the shortest content between matching open/close tags.
//   - <tag ... />             — self-closing tags.
//
// Known limitation: nested tags with the same name (e.g. <t><t>x</t></t>) will
// only match up to the first closing tag, leaving a dangling </t>. This is
// acceptable because IDE-injected tags are not nested this way.
var xmlTagsRe = regexp.MustCompile(`(?s)<[a-zA-Z_][a-zA-Z0-9_-]*(?:\s[^>]*)?>.*?</[a-zA-Z_][a-zA-Z0-9_-]*>|<[a-zA-Z_][a-zA-Z0-9_-]*(?:\s[^>]*)?/>`)

// whitespaceRe collapses any run of Unicode whitespace into a single space.
var whitespaceRe = regexp.MustCompile(`\s+`)

// stripXMLTags removes XML-style tag blocks (and their content) from s and
// returns the trimmed result with normalized whitespace. This cleans up injected
// IDE/system context that precedes the real user message text while preserving
// word boundaries where tags appeared between tokens.
func stripXMLTags(s string) string {
	cleaned := xmlTagsRe.ReplaceAllString(s, " ")
	cleaned = strings.TrimSpace(cleaned)
	cleaned = whitespaceRe.ReplaceAllString(cleaned, " ")
	return cleaned
}

// extractWordsFromMessage extracts up to maxWords from the message, handling various edge cases
func extractWordsFromMessage(message string, maxWords int) []string {
	if message == "" {
		return []string{}
	}

	// Strip leading markdown heading markers before they get converted to "hash"
	message = stripMarkdownHeading(message)

	// Normalize unicode and remove accents
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	normalized, _, _ := transform.String(t, message)

	// Convert to lowercase
	normalized = strings.ToLower(normalized)

	// Replace special characters with their word equivalents
	normalized = strings.ReplaceAll(normalized, "@", " at ")
	normalized = strings.ReplaceAll(normalized, "&", " and ")
	normalized = strings.ReplaceAll(normalized, "#", " hash ")

	// Remove all punctuation and special characters, replace with spaces
	// This regex keeps letters, numbers, and spaces
	re := regexp.MustCompile(`[^a-z0-9\s]+`)
	normalized = re.ReplaceAllString(normalized, " ")

	// Split into words and filter out empty strings
	words := strings.Fields(normalized)

	// Take up to maxWords
	if len(words) > maxWords {
		words = words[:maxWords]
	}

	return words
}

// generateFilenameFromWords creates a filename from words, handling edge cases.
// Enforces lowercase to guarantee filesystem-safe, consistent filenames regardless
// of how the words were prepared by the caller.
func generateFilenameFromWords(words []string) string {
	if len(words) == 0 {
		return ""
	}

	// Join words with hyphens and enforce lowercase as a safety guarantee
	filename := strings.ToLower(strings.Join(words, "-"))

	// Ensure no double hyphens
	for strings.Contains(filename, "--") {
		filename = strings.ReplaceAll(filename, "--", "-")
	}

	// Remove leading/trailing hyphens
	filename = strings.Trim(filename, "-")

	return filename
}

// GenerateFilenameFromUserMessage generates a filename based on the first user message content
func GenerateFilenameFromUserMessage(message string) string {
	words := extractWordsFromMessage(stripXMLTags(message), 4)
	return generateFilenameFromWords(words)
}

// GenerateReadableName creates a human-readable session name from a user message
// The name is truncated to 100 characters at a word boundary and cleaned of excess whitespace
func GenerateReadableName(message string) string {
	if message == "" {
		return ""
	}

	message = stripXMLTags(message)
	if message == "" {
		return ""
	}

	// Normalize whitespace: replace newlines and multiple spaces with single space
	name := strings.Join(strings.Fields(message), " ")

	// Truncate to reasonable length (100 chars) at word boundary
	maxLength := 100
	if len(name) <= maxLength {
		return name
	}

	// Find last space before maxLength to avoid breaking words
	truncated := name[:maxLength]
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > 0 {
		truncated = truncated[:lastSpace]
	}

	// Add ellipsis to indicate truncation
	return truncated + "..."
}

// ReadableTitleFromSessionData derives the same human-readable title a local row shows — the first
// user message run through GenerateReadableName (XML-stripped, whitespace-normalized, capped at 100
// chars). Used at cloud-sync time so a cloud row can display a prompt title identical to its local
// counterpart. Because it runs through GenerateReadableName, the stored value stays tiny even when
// the first message is enormous (e.g. a pasted log dump). Empty when there is no user message.
func ReadableTitleFromSessionData(data *schema.SessionData) string {
	return GenerateReadableName(firstUserMessageText(data))
}

// firstUserMessageText returns the concatenated text content of the first user-role message in the
// session — the prompt that opens the conversation. Skips a user message that carries no text (e.g.
// a tool result) and moves to the next. Empty when none is found.
func firstUserMessageText(data *schema.SessionData) string {
	if data == nil {
		return ""
	}
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Role != schema.RoleUser {
				continue
			}
			var b strings.Builder
			for _, part := range msg.Content {
				if part.Type == "text" {
					b.WriteString(part.Text)
				}
			}
			if text := strings.TrimSpace(b.String()); text != "" {
				return text
			}
		}
	}
	return ""
}

// appendRemainingParts appends the remaining path components from parts[startIdx:] to the
// result path, skipping any empty components. This is used when we can't canonicalize the
// rest of the path (e.g., directory doesn't exist or can't be read).
func appendRemainingParts(result string, parts []string, startIdx int) string {
	for j := startIdx; j < len(parts); j++ {
		if parts[j] != "" {
			result = filepath.Join(result, parts[j])
		}
	}
	return result
}

// GetCanonicalPath resolves a path to its canonical form by resolving symlinks
// and correcting case. This is important on case-insensitive filesystems like macOS
// where "/Users/Foo" and "/users/foo" refer to the same directory but would produce
// different hash values.
//
// The algorithm works by walking the path component by component, starting from the root.
// For each component, it opens the parent directory and reads its actual entries,
// performing case-insensitive matching to find the correct casing on disk.
// This ensures that the returned path matches the real filesystem casing, which is
// important for consistent hashing and avoiding subtle bugs on case-insensitive filesystems.
//
// Note: This can be slow for long paths or directories with many entries, since it
// requires reading each directory's contents.
//
// If a component doesn't exist on disk, the remaining path components are
// appended as-is, making this function safe to use with paths that don't
// fully exist yet.
func GetCanonicalPath(p string) (string, error) {
	// Ensure we have an absolute, clean path
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		var err error
		p, err = filepath.Abs(p)
		if err != nil {
			return "", err
		}
	}

	// Resolve any symlinks in the path to get the real physical path
	// This is critical for ensuring consistent path hashing across different
	// ways of accessing the same directory (e.g., through a symlink vs directly)
	realPath, err := filepath.EvalSymlinks(p)
	if err == nil && realPath != p {
		// Successfully resolved symlinks - update p to the resolved path for case normalization
		p = realPath
	} else if err != nil {
		// Symlink resolution failed (e.g., path doesn't exist yet, or Windows junction points).
		// This is expected in normal operation — fall back to the original path and continue
		// with case normalization below.
		slog.Debug("GetCanonicalPath: symlink resolution failed, using original path",
			"path", p, "error", err)
	}

	// Separate the volume (drive letter "D:" or UNC share "\\server\share" on Windows,
	// empty on Unix) from the rest of the path. Without this, splitting "D:\foo\bar" on
	// the OS separator yields ["D:", "foo", "bar"] — and the original code skipped the
	// first element under the assumption it was empty (true only for Unix paths like
	// "/foo/bar"). The drive letter would be silently dropped, leaving a path that
	// resolved against the current drive instead of the requested one. UNC paths were
	// even worse: "\\server\share\foo" would lose one of the leading backslashes and
	// stop being a UNC reference at all.
	volume := filepath.VolumeName(p)
	remainder := p[len(volume):]

	// Split the remainder. The leading separator produces an empty first element which
	// we skip, so on every platform the loop processes only real path components.
	parts := strings.Split(remainder, string(os.PathSeparator))

	// Walk from the volume root. On Unix that's "/"; on Windows it's "D:\" or
	// "\\server\share\".
	result := volume + string(os.PathSeparator)

	firstPartIndex := 1

	// Build path component by component
	for i := firstPartIndex; i < len(parts); i++ {
		if parts[i] == "" {
			continue
		}

		// Open current result directory to read its contents
		f, err := os.Open(result)
		if err != nil {
			// Can't read directory - might not exist yet
			// Append remaining path as-is
			return appendRemainingParts(result, parts, i), nil
		}

		names, err := f.Readdirnames(-1)
		_ = f.Close() // Best effort close, error doesn't affect result
		if err != nil {
			return "", err
		}

		// Find the correct case
		found := false
		for _, name := range names {
			if strings.EqualFold(name, parts[i]) {
				result = filepath.Join(result, name)
				found = true
				break
			}
		}

		if !found {
			// Component doesn't exist, append remaining as-is
			result = appendRemainingParts(result, parts, i)
			break
		}
	}

	return result, nil
}

// debugBaseDirOverride allows overriding the base directory for debug output.
// When set, debug files are written to this directory instead of the default
// .specstory/debug/ location. This is set via the --debug-dir CLI flag or
// the debug_dir config option.
var debugBaseDirOverride string

// SetDebugBaseDir sets the override base directory for debug output.
// When set to a non-empty value, GetDebugDir will use this as the base
// instead of the default .specstory/debug/ path.
func SetDebugBaseDir(dir string) {
	debugBaseDirOverride = dir
}

// GetDebugDir returns the debug directory path for a given session ID or UUID.
//
// The debug directory is used to store detailed JSON records and debugging
// information for SpecStory CLI sessions. This is shared across all providers
// (Claude Code, Codex CLI, Cursor CLI) to maintain a consistent debug structure.
//
// When debugBaseDirOverride is set (via SetDebugBaseDir), the directory structure
// is: <override>/<sessionID>/
// Otherwise: .specstory/debug/<sessionID>/
//
// Example:
//
//	debugDir := spi.GetDebugDir("abc123-def456")
//	// Returns: ".specstory/debug/abc123-def456"
func GetDebugDir(sessionID string) string {
	if debugBaseDirOverride != "" {
		return filepath.Join(debugBaseDirOverride, sessionID)
	}
	return filepath.Join(".specstory", "debug", sessionID)
}

// WriteDebugSessionData writes the SessionData as formatted JSON to the debug directory.
// This provides a standardized, provider-agnostic debug output alongside provider-specific raw data.
//
// The file is written to: .specstory/debug/<sessionID>/session-data.json
//
// Returns an error if the debug directory cannot be created or the file cannot be written.
func WriteDebugSessionData(sessionID string, sessionData *schema.SessionData) error {
	if sessionData == nil {
		return fmt.Errorf("sessionData is nil")
	}

	// Get debug directory
	debugDir := GetDebugDir(sessionID)

	// Create debug directory if it doesn't exist
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		return fmt.Errorf("failed to create debug directory: %w", err)
	}

	// Marshal SessionData to pretty-printed JSON
	jsonData, err := json.MarshalIndent(sessionData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal SessionData: %w", err)
	}

	// Write to session-data.json
	filePath := filepath.Join(debugDir, "session-data.json")
	if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write session-data.json: %w", err)
	}

	// Log absolute path for debugging
	absPath, _ := filepath.Abs(filePath)
	slog.Debug("Wrote debug session data", "sessionId", sessionID, "path", absPath)
	return nil
}

// FileURIToPath converts a file:// URI to a local filesystem path, decoding
// percent-escapes (file:///tmp/my%20file.go → /tmp/my file.go). It is the
// single URI→path converter shared by providers whose stores record file URIs
// (Cursor IDE workspace identifiers, Antigravity tool args and project
// configs), so the non-POSIX forms are translated identically everywhere:
//
//   - WSL URIs (file://wsl.localhost/<distro>/path, file://wsl$/...): the host
//     and distro segment are stripped, yielding the in-distro POSIX path.
//   - Drive-letter URIs (file:///C:/Users/...): on Windows the spurious
//     leading slash is trimmed and separators converted (C:\Users\...).
//   - UNC URIs (file://server/share/...): on Windows the host becomes the UNC
//     root (\\server\share\...). On POSIX systems a host has no filesystem
//     meaning and is dropped.
//
// Returns an error for unparseable URIs and schemes other than file.
func FileURIToPath(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("failed to parse URI: %w", err)
	}
	if parsed.Scheme != "file" && parsed.Scheme != "" {
		return "", fmt.Errorf("unsupported URI scheme %q: %s", parsed.Scheme, uri)
	}

	// url.Parse already percent-decodes into Path (e.g. %20 -> space), so no
	// extra PathUnescape is needed — unescaping again would corrupt paths
	// containing literal % sequences (a folder named "a%20b" arrives encoded
	// as "a%2520b" and must decode to "a%20b", not "a b").
	path := parsed.Path

	// WSL URIs carry the distro as the first path segment under a wsl host;
	// the usable path is what remains inside the distro.
	if strings.EqualFold(parsed.Host, "wsl.localhost") || strings.HasPrefix(strings.ToLower(parsed.Host), "wsl$") {
		if len(path) > 1 {
			if idx := strings.Index(path[1:], "/"); idx >= 0 {
				slog.Debug("Converted WSL file URI to path", "uri", uri, "path", path[idx+1:])
				return path[idx+1:], nil
			}
		}
		return "", fmt.Errorf("malformed WSL URI path: %s", uri)
	}

	if filepath.Separator == '\\' {
		// A non-WSL host names a UNC share.
		if parsed.Host != "" {
			return `\\` + parsed.Host + filepath.FromSlash(path), nil
		}
		// Drive-letter URI paths arrive with a spurious leading slash (/C:/Users).
		return filepath.FromSlash(strings.TrimPrefix(path, "/")), nil
	}

	return path, nil
}

// ParseVSCodeRemoteURI extracts the filesystem path from a vscode-remote:// URI,
// the scheme VS Code-lineage IDEs (VS Code, Cursor, VSCodium, ...) store in
// workspace.json when a folder is opened through a remote extension.
// Handles four types of remote URIs:
//  1. WSL: vscode-remote://wsl%2B{distro}/{path} - path is the WSL filesystem path
//  2. SSH: vscode-remote://ssh-remote%2B{config-hex}/{path} - path is the remote filesystem path
//  3. Tunnel: vscode-remote://tunnel%2B{host}/{path} - path is the remote filesystem path
//  4. Dev container: vscode-remote://dev-container%2B{config-hex}/{path} - path is the container-internal path
//
// Go's url.Parse rejects %2B in the host component, so we parse manually.
func ParseVSCodeRemoteURI(uri string) (string, error) {
	// Strip scheme prefix: "vscode-remote://"
	remainder := strings.TrimPrefix(uri, "vscode-remote://")

	// Split host from path at the first unencoded slash after the host
	// The host is percent-encoded (e.g., "wsl%2Bubuntu" or "ssh-remote%2B{config-hex}")
	slashIdx := strings.Index(remainder, "/")
	if slashIdx < 0 {
		return "", fmt.Errorf("malformed vscode-remote URI (no path): %s", uri)
	}

	host := remainder[:slashIdx]
	pathPart := remainder[slashIdx:]

	// Decode the host to determine remote type
	decodedHost, err := url.PathUnescape(host)
	if err != nil {
		decodedHost = host
	}

	hostLower := strings.ToLower(decodedHost)

	// Check if it's a supported remote type: each host is either bare ("wsl") or
	// carries a "+{config}" suffix ("wsl+ubuntu", "ssh-remote+{config-hex}").
	supportedHosts := []string{"wsl", "ssh-remote", "tunnel", "dev-container"}
	supported := false
	for _, host := range supportedHosts {
		if hostLower == host || strings.HasPrefix(hostLower, host+"+") {
			supported = true
			break
		}
	}
	if !supported {
		return "", fmt.Errorf("unsupported vscode-remote host %q: %s", decodedHost, uri)
	}

	// Decode the path portion
	path, err := url.PathUnescape(pathPart)
	if err != nil {
		return "", fmt.Errorf("failed to decode vscode-remote URI path: %w", err)
	}

	// Log the conversion with appropriate context
	switch {
	case strings.HasPrefix(hostLower, "ssh-remote"):
		slog.Debug("Converted vscode-remote SSH URI to path", "uri", uri, "path", path)
	case strings.HasPrefix(hostLower, "tunnel"):
		slog.Debug("Converted vscode-remote tunnel URI to path", "uri", uri, "path", path)
	case strings.HasPrefix(hostLower, "dev-container"):
		slog.Debug("Converted vscode-remote dev container URI to path", "uri", uri, "path", path)
	default:
		slog.Debug("Converted vscode-remote WSL URI to path", "uri", uri, "path", path)
	}

	return path, nil
}

// IsRemoteURIRequiringBasenameMatch checks if a URI is a vscode-remote SSH, tunnel, or
// dev-container URI. Case-insensitive to stay consistent with ParseVSCodeRemoteURI, which
// accepts uppercase authorities — this check gates basename matching, so a case mismatch
// would silently drop remote workspaces instead of just changing a log line.
//
// SSH, tunnel, and dev-container workspace paths all live outside the local filesystem (a
// remote machine or a container), so direct path comparison can never succeed for them and
// the folder basename is the only usable signal. WSL is deliberately excluded: WSL paths are
// reachable from the host via \\wsl.localhost\... / /mnt/wsl/..., so direct path matching
// already works there and doesn't need the (weaker, name-collision-prone) basename fallback.
func IsRemoteURIRequiringBasenameMatch(uri string) bool {
	lower := strings.ToLower(uri)
	return strings.HasPrefix(lower, "vscode-remote://ssh-remote") ||
		strings.HasPrefix(lower, "vscode-remote://tunnel") ||
		strings.HasPrefix(lower, "vscode-remote://dev-container")
}

// IsWSL reports whether this process is running inside Windows Subsystem for
// Linux. WSL matters to providers because a VS Code-lineage IDE installed on
// the Windows side keeps all of its data (workspace storage, global database)
// on the Windows filesystem even for WSL projects, so discovery must look at
// /mnt/c/... before the native Linux locations.
func IsWSL() bool {
	// /proc/version identifies the kernel; WSL kernels carry a "microsoft"
	// (WSL2) or "wsl" marker. On non-Linux systems the file doesn't exist.
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	versionLower := strings.ToLower(string(data))
	return strings.Contains(versionLower, "microsoft") || strings.Contains(versionLower, "wsl")
}

// FindWindowsAppDataPathFromWSL locates a path under a Windows user's roaming
// AppData when running inside WSL, by scanning /mnt/c/Users for a profile that
// actually contains it. elem is the path relative to AppData/Roaming (e.g.
// "Cursor", "User", "workspaceStorage"). Returns "" when no profile has it.
//
// The scan takes the first matching profile: WSL doesn't record which Windows
// user owns it, so a real per-user mapping isn't available — on the common
// single-user machine this is exact, and on multi-user machines it is the best
// guess we can make. Only the default C: mount is searched; a non-standard
// automount root is out of scope and falls back to native-Linux discovery.
func FindWindowsAppDataPathFromWSL(elem ...string) string {
	usersDir := "/mnt/c/Users"
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		slog.Debug("Could not read Windows Users directory from WSL", "path", usersDir, "error", err)
		return ""
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Skip Windows system profiles — they never hold IDE data
		username := entry.Name()
		if username == "Public" || username == "Default" || username == "All Users" {
			continue
		}

		candidate := filepath.Join(append([]string{usersDir, username, "AppData", "Roaming"}, elem...)...)
		if _, err := os.Stat(candidate); err == nil {
			slog.Debug("Found path on Windows filesystem from WSL",
				"path", candidate,
				"windowsUser", username)
			return candidate
		}
	}

	slog.Debug("Path not found in any Windows user profile from WSL", "elem", filepath.Join(elem...))
	return ""
}

// ResolveUserDataDirOverride resolves an explicit `--user-data-dir
// <provider-id>:<path>` override to the storage location elem beneath it (e.g.
// "User", "workspaceStorage"). ok is false when no override is set or when the
// derived path does not exist. The missing-path case logs a warning rather than
// failing hard so a stale override doesn't silently kill the provider — callers
// are expected to fall through to OS-default discovery.
func ResolveUserDataDirOverride(override, providerID string, elem ...string) (string, bool) {
	if override == "" {
		return "", false
	}
	candidate := filepath.Join(append([]string{override}, elem...)...)
	if _, err := os.Stat(candidate); err == nil {
		slog.Debug("Using --user-data-dir override", "provider", providerID, "path", candidate)
		return candidate, true
	}
	slog.Warn("--user-data-dir override path missing; falling back to OS default",
		"provider", providerID, "override", override, "candidate", candidate)
	return "", false
}
