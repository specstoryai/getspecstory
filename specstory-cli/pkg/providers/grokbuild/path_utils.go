package grokbuild

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Package-level variables for mocking in tests
var (
	osUserHomeDir = os.UserHomeDir
	osStat        = os.Stat
	osGetwd       = os.Getwd
)

// uuidLike matches the session directory names Grok creates (UUIDv7).
// Used to tell session directories apart from the sidecar files and the
// `subagents` directory that live alongside them.
var uuidLike = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// GrokPathError describes actionable filesystem failures when locating Grok data.
type GrokPathError struct {
	Kind      string   // sessions_missing, project_missing
	Path      string   // offending path
	KnownCwds []string // project working directories discovered on disk (optional)
	Message   string   // user-facing explanation
}

func (e *GrokPathError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

// GetGrokHome returns the Grok home directory, honoring the GROK_HOME override.
func GetGrokHome() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("GROK_HOME")); custom != "" {
		return custom, nil
	}
	homeDir, err := osUserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(homeDir, ".grok"), nil
}

// GetGrokSessionsDir returns the root of the Grok session store (<grok home>/sessions).
func GetGrokSessionsDir() (string, error) {
	home, err := GetGrokHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "sessions"), nil
}

// DecodeCwdDirname recovers the working directory a Grok project group directory
// stands for. The directory name is the cwd percent-encoded with an RFC 3986
// unreserved allowlist, so `/Users/a/b` becomes `%2FUsers%2Fa%2Fb`.
//
// Why decode instead of encode: the provider never has to reproduce Grok's exact
// escape table, which would silently drop whole projects if it drifted. Grok also
// falls back to a `<slug>-<hash>` directory name when the encoded form would get
// too long, writing the true path into a `.cwd` sidecar; reading that sidecar
// handles the fallback for free.
func DecodeCwdDirname(groupDir string) (string, bool) {
	name := filepath.Base(groupDir)

	// url.PathUnescape, not QueryUnescape: the latter turns '+' into a space,
	// which Grok's encoder never produces.
	if decoded, err := url.PathUnescape(name); err == nil && looksAbsolute(decoded) {
		return decoded, true
	}

	// Long-path fallback: the true cwd is in a .cwd sidecar next to the sessions.
	if content, err := os.ReadFile(filepath.Join(groupDir, ".cwd")); err == nil {
		if cwd := strings.TrimSpace(string(content)); cwd != "" {
			return cwd, true
		}
	}

	return "", false
}

// looksAbsolute reports whether a decoded directory name is a plausible absolute
// path. It tests the shape rather than the running OS, because a session store
// written on one platform can be read on another.
func looksAbsolute(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	// Windows drive letter, e.g. C:\Users\a or C:/Users/a
	if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return true
	}
	return false
}

// ResolveGrokProjectDir locates the Grok group directory holding the sessions for
// the given project path. It scans the session store and compares the decoded
// working directory of each group against the project, canonicalizing both so a
// symlinked or differently-cased path still matches.
func ResolveGrokProjectDir(projectPath string) (string, error) {
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return "", err
	}

	sessionsDir, err := GetGrokSessionsDir()
	if err != nil {
		return "", err
	}

	if _, err := osStat(sessionsDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &GrokPathError{
				Kind:    "sessions_missing",
				Path:    sessionsDir,
				Message: fmt.Sprintf("Grok sessions directory %q not found. Run Grok Build at least once, or verify GROK_HOME.", sessionsDir),
			}
		}
		return "", fmt.Errorf("failed to read Grok sessions directory %q: %w", sessionsDir, err)
	}

	wanted := canonical(projectPath)

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return "", fmt.Errorf("failed to read Grok sessions directory %q: %w", sessionsDir, err)
	}

	var known []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		groupDir := filepath.Join(sessionsDir, entry.Name())
		cwd, ok := DecodeCwdDirname(groupDir)
		if !ok {
			continue
		}
		known = append(known, cwd)
		if canonical(cwd) == wanted {
			slog.Debug("ResolveGrokProjectDir: matched project group directory",
				"projectPath", projectPath, "groupDir", groupDir)
			return groupDir, nil
		}
	}

	sort.Strings(known)
	summary := "(none discovered)"
	if len(known) > 0 {
		summary = strings.Join(known, ", ")
	}

	return "", &GrokPathError{
		Kind:      "project_missing",
		Path:      sessionsDir,
		KnownCwds: known,
		Message: fmt.Sprintf("No Grok Build data found for %q. Projects with Grok sessions: %s. Start a Grok Build session in your repo to create it.",
			projectPath, summary),
	}
}

// defaultProjectPath fills in an empty project path with the current working
// directory. Callers always pass a real path today, but resolving it here means
// every entry point stamps a real workspace root into the session data.
func defaultProjectPath(projectPath string) (string, error) {
	if projectPath != "" {
		return projectPath, nil
	}
	cwd, err := osGetwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}
	return cwd, nil
}

// canonical resolves symlinks and filesystem casing so two spellings of the same
// directory compare equal. Falls back to the input when the path cannot be resolved.
func canonical(p string) string {
	resolved, err := spi.GetCanonicalPath(p)
	if err != nil {
		return p
	}
	return resolved
}

// ListGrokProjectCwds returns the working directories of every project group in
// the store, sorted. Used for the detection help text.
func ListGrokProjectCwds() ([]string, error) {
	sessionsDir, err := GetGrokSessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read Grok sessions directory %q: %w", sessionsDir, err)
	}

	var cwds []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if cwd, ok := DecodeCwdDirname(filepath.Join(sessionsDir, entry.Name())); ok {
			cwds = append(cwds, cwd)
		}
	}
	sort.Strings(cwds)
	return cwds, nil
}
