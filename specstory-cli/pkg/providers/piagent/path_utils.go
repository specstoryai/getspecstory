package piagent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Environment overrides pi documents in `pi --help` (config.ts ENV_AGENT_DIR /
// ENV_SESSION_DIR). PI_CODING_AGENT_DIR relocates the agent dir (sessions live
// under <dir>/sessions/--<encoded-cwd>--/). PI_CODING_AGENT_SESSION_DIR points
// at a sessions directory used FLAT — pi writes session files directly into it
// with no per-cwd encoding, so project scoping must filter by header cwd.
const (
	envAgentDir   = "PI_CODING_AGENT_DIR"
	envSessionDir = "PI_CODING_AGENT_SESSION_DIR"
)

// piSessionsRoot returns the directory under which pi stores sessions and
// whether that directory uses the flat layout (files directly in the root)
// instead of the default per-cwd encoded subdirectories. Precedence mirrors
// pi's own resolution (main.ts): PI_CODING_AGENT_SESSION_DIR >
// PI_CODING_AGENT_DIR/sessions > ~/.pi/agent/sessions. pi's --session-dir flag
// and settings.json sessionDir are per-invocation and not visible here.
// It does not require the directory to exist.
func piSessionsRoot() (string, bool, error) {
	if dir := strings.TrimSpace(os.Getenv(envSessionDir)); dir != "" {
		return expandTilde(dir), true, nil
	}
	if dir := strings.TrimSpace(os.Getenv(envAgentDir)); dir != "" {
		return filepath.Join(expandTilde(dir), "sessions"), false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("pi: cannot resolve home dir: %w", err)
	}
	return filepath.Join(home, ".pi", "agent", "sessions"), false, nil
}

// expandTilde expands a leading "~/" to the user's home directory, matching
// how pi itself expands its env overrides (expandTildePath).
func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// EncodeCwd maps a working directory to pi's session-subdirectory name,
// mirroring pi's own encoder exactly (session-manager.js:
// `--${resolvedCwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-")}--`):
// one leading '/' or '\' is stripped, every remaining '/', '\', and ':' becomes
// '-', and the result is wrapped in '--'. E.g. "/Users/jane/proj" ->
// "--Users-jane-proj--". Replacing the full character class matters: a project
// path containing ':' (legal on macOS/Linux) would otherwise compute a
// directory name pi never writes, silently finding zero sessions.
func EncodeCwd(cwd string) string {
	trimmed := cwd
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		trimmed = trimmed[1:]
	}
	encoded := strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(trimmed)
	return "--" + encoded + "--"
}

// projectCandidates returns the working-directory forms a pi session for this
// project may have been recorded under: the absolute path as given and, when
// different, its symlink-resolved form. pi encodes the cwd as its own process
// saw it, which may be either form (e.g. /tmp/foo vs /private/tmp/foo on
// macOS), so callers must check both.
func projectCandidates(projectPath string) ([]string, error) {
	cwd := strings.TrimSpace(projectPath)
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("pi: cannot get working dir: %w", err)
		}
		cwd = wd
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("pi: cannot resolve %s: %w", cwd, err)
	}
	candidates := []string{abs}
	if real, rErr := filepath.EvalSymlinks(abs); rErr == nil && real != abs {
		candidates = append(candidates, real)
	}
	return candidates, nil
}

// ProjectSessionDir returns the session directory pi would use for the given
// project (for display/diagnostics), WITHOUT requiring it to exist. In the
// flat PI_CODING_AGENT_SESSION_DIR layout this is the override directory
// itself; otherwise it is the encoded per-cwd subdirectory for the project's
// absolute path.
func ProjectSessionDir(projectPath string) (string, error) {
	root, flat, err := piSessionsRoot()
	if err != nil {
		return "", err
	}
	if flat {
		return root, nil
	}
	candidates, err := projectCandidates(projectPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, EncodeCwd(candidates[0])), nil
}

// SessionFilesInProject lists the pi session files belonging to the given
// project. In the default layout it unions the encoded directories for each
// project-path candidate (raw and symlink-resolved) and then checks each
// file's header cwd, because EncodeCwd is lossy: /a-b and /a/b both encode to
// --a-b--, so the directory alone can hold another project's sessions. A file
// whose header has no cwd is kept there (see headerBelongsToProject). In the
// flat override layout it lists the root and keeps only files whose header
// cwd matches the project, the same filtering pi applies to custom session
// dirs. Returns an empty slice (no error) if nothing exists yet.
func SessionFilesInProject(projectPath string) ([]string, error) {
	root, flat, err := piSessionsRoot()
	if err != nil {
		return nil, err
	}
	candidates, err := projectCandidates(projectPath)
	if err != nil {
		return nil, err
	}
	if flat {
		return flatSessionFiles(root, candidates)
	}
	var files []string
	seen := make(map[string]bool)
	for _, c := range candidates {
		dir := filepath.Join(root, EncodeCwd(c))
		dirFiles, dErr := jsonlFilesInDir(dir)
		if dErr != nil {
			return nil, dErr
		}
		for _, f := range dirFiles {
			if seen[f] {
				continue
			}
			seen[f] = true
			if sessionFileBelongsToProject(f, candidates, false) {
				files = append(files, f)
			}
		}
	}
	return files, nil
}

// flatSessionFiles lists *.jsonl in a flat override sessions dir, keeping only
// files whose session header cwd matches one of the project candidates.
func flatSessionFiles(root string, candidates []string) ([]string, error) {
	all, err := jsonlFilesInDir(root)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range all {
		if sessionFileBelongsToProject(f, candidates, true) {
			files = append(files, f)
		}
	}
	return files, nil
}

// sessionFileBelongsToProject reads the header of the session file at path and
// reports whether the file belongs to the project described by candidates. See
// headerBelongsToProject for the rule. A header that cannot be read drops the
// file when strict is set; otherwise the file is kept, and the caller that
// parses it reports the error itself, as it did before the cwd check existed.
func sessionFileBelongsToProject(path string, candidates []string, strict bool) bool {
	h, err := readHeader(path)
	if err != nil {
		if strict {
			slog.Debug("pi: skipping unreadable session file", "path", path, "error", err)
			return false
		}
		return true
	}
	return headerBelongsToProject(path, h, candidates, strict)
}

// headerBelongsToProject decides from a session header whether the file at
// path belongs to the project described by candidates (the raw absolute path
// and its symlink-resolved form). A header cwd that is present and matches a
// candidate keeps the file; one that matches no candidate rejects it, with a
// debug log naming the file and the cwd.
//
// A nil header (not a pi session, or an empty file) or a header without a cwd
// is decided by strict. The flat layout passes strict=true: the directory says
// nothing about the project, so a file without a cwd cannot be placed and is
// dropped. The default layout passes strict=false and fails open: the encoded
// directory already names the project, so the file is kept. Pi lists cwd as a
// required header field in every version of its format, so in practice the
// check always has a value.
func headerBelongsToProject(path string, h *sessionHeader, candidates []string, strict bool) bool {
	if h == nil || h.Cwd == "" {
		return !strict
	}
	if cwdMatchesCandidate(h.Cwd, candidates) {
		return true
	}
	slog.Debug("pi: skipping session file recorded for another project", "path", path, "cwd", h.Cwd)
	return false
}

// cwdMatchesCandidate reports whether a session header cwd matches one of the
// project's candidate working-directory forms (raw and symlink-resolved).
func cwdMatchesCandidate(cwd string, candidates []string) bool {
	for _, c := range candidates {
		if cwd == c {
			return true
		}
	}
	return false
}

// jsonlFilesInDir lists the *.jsonl files directly inside dir (no recursion).
// Returns an empty slice (no error) if the directory does not exist.
func jsonlFilesInDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("pi: reading session dir %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	return files, nil
}
