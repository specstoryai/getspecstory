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
// project. The override and config locations are shared by every project; the
// default keeps each project's mirror next to its history.
func mirrorFor(projectPath string) (mirror, bool) {
	if userDataDirOverride != "" {
		return mirror{root: userDataDirOverride}, false
	}
	if configMirrorDir != "" {
		return mirror{root: configMirrorDir}, false
	}
	return mirror{root: filepath.Join(projectPath, ".specstory", "cursorcloud")}, true
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
// project's origin.
func agentMatchesOrigin(agent apiAgent, origin string) bool {
	if origin == "" {
		return false
	}
	for _, repo := range agent.Repos {
		if spi.NormalizeGitRemoteURL(repo.URL) == origin {
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
