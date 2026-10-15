package cursorcloud

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// apiKeyEnvVar is the environment variable Cursor's own tooling reads the API
// key from; it overrides the config file so a one-off key needs no edit.
const apiKeyEnvVar = "CURSOR_API_KEY"

// Settings handed in by the CLI at startup. Each is set once before any
// provider method runs, so no locking is needed.
var (
	configAPIKey        string
	configMirrorDir     string
	userDataDirOverride string
)

// SetAPIKey sets the key from `cursor_api_key` in specstory.toml. The
// CURSOR_API_KEY environment variable still wins when set.
func SetAPIKey(key string) {
	configAPIKey = strings.TrimSpace(key)
}

// SetMirrorDir sets the mirror location from `cursorcloud_mirror_dir` in
// specstory.toml (tilde-expanded). Empty keeps the project-local default.
func SetMirrorDir(dir string) {
	configMirrorDir = expandHome(strings.TrimSpace(dir))
}

// SetUserDataDirOverride sets the mirror location from
// `--user-data-dir cursorcloud:<path>`, which wins over the config file.
func SetUserDataDirOverride(dir string) {
	userDataDirOverride = expandHome(strings.TrimSpace(dir))
}

// resolveAPIKey returns the key and where it came from ("" when none).
func resolveAPIKey() (key, source string) {
	if env := strings.TrimSpace(os.Getenv(apiKeyEnvVar)); env != "" {
		return env, apiKeyEnvVar
	}
	if configAPIKey != "" {
		return configAPIKey, "cursor_api_key"
	}
	return "", ""
}

// mirrorFor returns the mirror for a project and whether it lives inside the
// project, which decides whether ensureRoot writes the protective .gitignore.
// The override and config locations are shared by every project and usually
// sit outside it; the default keeps each project's mirror next to its history.
// Locality is decided from the resolved path, not from which setting supplied
// it, because a configured path such as ".specstory/cursorcloud" still writes
// unredacted transcripts into the project and still needs the .gitignore.
func mirrorFor(projectPath string) (mirror, bool) {
	root := userDataDirOverride
	if root == "" {
		root = configMirrorDir
	}
	if root == "" {
		return mirror{root: filepath.Join(projectPath, ".specstory", "cursorcloud")}, true
	}
	root = canonicalMirrorRoot(root)
	return mirror{root: root}, insideProject(root, projectPath)
}

// canonicalMirrorRoot resolves a configured root to an absolute path with
// symlinks evaluated. Session refs carry mirror paths as NativePath, which must
// be absolute so a resume from another directory still finds them, and the
// locality check must see where a symlink really points. The directory may not
// exist yet (ensureRoot creates it), so the nearest existing ancestor is
// resolved and the missing tail re-appended.
func canonicalMirrorRoot(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	tail := ""
	for dir := abs; ; {
		if _, err := os.Stat(dir); err == nil {
			return filepath.Join(spi.CanonicalizePathOrClean(dir), tail)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return abs
		}
		tail = filepath.Join(filepath.Base(dir), tail)
		dir = parent
	}
}

// insideProject reports whether root, already canonical, lies strictly below
// projectPath. The project root itself does not count: a "*" .gitignore there
// would hide the whole project from git, which is worse than an unprotected
// mirror, so that misconfiguration is left to the user.
func insideProject(root, projectPath string) bool {
	if projectPath == "" {
		return false
	}
	rel, err := filepath.Rel(spi.CanonicalizePathOrClean(projectPath), root)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// globalMirror is the mirror enumerated by reindex, which has no project: the
// shared location when one is configured, else the current project's mirror.
func globalMirror() mirror {
	if userDataDirOverride != "" || configMirrorDir != "" {
		m, _ := mirrorFor("")
		return m
	}
	cwd, err := os.Getwd()
	if err != nil {
		slog.Warn("ListAllAgentChatSessions: cannot resolve working directory", "error", err)
		return mirror{}
	}
	m, _ := mirrorFor(spi.CanonicalizePathOrClean(cwd))
	return m
}

// projectOrigin returns the normalized origin URL of the project's repository,
// or "" when the project has none. Agents are matched to projects by
// repository, because the API records a repository and never a directory.
func projectOrigin(projectPath string) string {
	origin := spi.GitOriginURL(projectPath)
	if origin == "" {
		return ""
	}
	return spi.NormalizeGitRemoteURL(origin)
}

// agentMatchesOrigin reports whether any of the agent's repositories is the
// project's origin. Case is ignored because GitHub and GitLab treat owner and
// repository names case-insensitively, so a clone's origin and the URL Cursor
// records can differ only in case and still name the same repository.
func agentMatchesOrigin(agent apiAgent, origin string) bool {
	if origin == "" {
		return false
	}
	for _, repo := range agent.Repos {
		if strings.EqualFold(spi.NormalizeGitRemoteURL(repo.URL), origin) {
			return true
		}
	}
	return false
}

// workspaceRootFor is the SessionData workspace root: the caller's project,
// else the process working directory as the documented last resort.
func workspaceRootFor(projectPath string) string {
	if projectPath != "" {
		return projectPath
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// expandHome expands a leading ~ so config and flag values can use it.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimLeft(path[1:], `/\`))
}
