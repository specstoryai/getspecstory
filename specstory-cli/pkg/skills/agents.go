// Package skills is the headless engine behind the `specstory skills` command. It
// downloads SpecStory Cloud skills and installs them locally using the same layout and
// semantics as the public `npx skills` CLI (a canonical .agents/skills store, symlinked
// into each detected agent's skills directory, tracked in a shared .skill-lock.json).
//
// The package is deliberately UI-free: every operation is a plain function that returns
// structured data, so both the interactive TUI and the non-interactive `--json`
// subcommands call the same code — and a future front end (the VS Code extension) can
// drive identical behavior by shelling out to the CLI.
package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Agent describes one coding-agent harness we can install a skill for. The fields mirror
// the npx skills agent registry (src/agents.ts) so the two tools install to the same
// places and a skill installed by one is visible to the other.
type Agent struct {
	// Name is the canonical identifier (e.g. "claude-code"), used on the CLI and in the lock.
	Name string
	// DisplayName is the human label (e.g. "Claude Code").
	DisplayName string
	// ProjectDir is the project-relative skills directory (e.g. ".claude/skills").
	ProjectDir string
	// GlobalDir is the absolute global skills directory, or "" if the agent has no global store.
	GlobalDir string
	// ConfigDir is the absolute path whose existence means the agent is installed on this machine.
	ConfigDir string
}

// Universal reports whether the agent reads the canonical ".agents/skills" store directly.
// Universal agents need no per-agent symlink (the canonical copy is already their store),
// matching npx skills' isUniversalAgent.
func (a Agent) Universal() bool {
	return a.ProjectDir == filepath.Join(agentsDirName, skillsSubdir)
}

// Detected reports whether this agent appears installed (its config dir exists).
func (a Agent) Detected() bool {
	return a.ConfigDir != "" && dirExists(a.ConfigDir)
}

const (
	agentsDirName = ".agents"
	skillsSubdir  = "skills"
	lockFileName  = ".skill-lock.json"
)

// hostPaths are the per-machine base directories the registry is computed from. They are a
// struct (rather than read inline) so tests can build a registry rooted at a temp dir.
type hostPaths struct {
	home       string
	configHome string // $XDG_CONFIG_HOME or ~/.config
	codexHome  string // $CODEX_HOME or ~/.codex
	claudeHome string // $CLAUDE_CONFIG_DIR or ~/.claude
}

// resolveHostPaths reads the environment the same way npx skills does, honoring the same
// overrides the rest of this CLI already respects (CODEX_HOME, CLAUDE_CONFIG_DIR).
func resolveHostPaths() hostPaths {
	home, _ := os.UserHomeDir()
	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	claudeHome := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if claudeHome == "" {
		claudeHome = filepath.Join(home, ".claude")
	}
	return hostPaths{home: home, configHome: configHome, codexHome: codexHome, claudeHome: claudeHome}
}

// buildRegistry constructs the agent table for the given host paths. This is a pragmatic
// subset of the (much larger) npx skills registry covering the common coding agents; the
// layout rules are identical, so adding an agent is just one more row.
func buildRegistry(p hostPaths) []Agent {
	canonicalRel := filepath.Join(agentsDirName, skillsSubdir) // ".agents/skills"
	return []Agent{
		{Name: "amp", DisplayName: "Amp", ProjectDir: canonicalRel,
			GlobalDir: filepath.Join(p.configHome, "agents", "skills"), ConfigDir: filepath.Join(p.configHome, "amp")},
		{Name: "claude-code", DisplayName: "Claude Code", ProjectDir: filepath.Join(".claude", "skills"),
			GlobalDir: filepath.Join(p.claudeHome, "skills"), ConfigDir: p.claudeHome},
		{Name: "cline", DisplayName: "Cline", ProjectDir: canonicalRel,
			GlobalDir: filepath.Join(p.home, ".agents", "skills"), ConfigDir: filepath.Join(p.home, ".cline")},
		{Name: "codex", DisplayName: "Codex", ProjectDir: canonicalRel,
			GlobalDir: filepath.Join(p.codexHome, "skills"), ConfigDir: p.codexHome},
		{Name: "cursor", DisplayName: "Cursor", ProjectDir: canonicalRel,
			GlobalDir: filepath.Join(p.home, ".cursor", "skills"), ConfigDir: filepath.Join(p.home, ".cursor")},
		{Name: "droid", DisplayName: "Droid", ProjectDir: filepath.Join(".factory", "skills"),
			GlobalDir: filepath.Join(p.home, ".factory", "skills"), ConfigDir: filepath.Join(p.home, ".factory")},
		{Name: "gemini", DisplayName: "Gemini CLI", ProjectDir: filepath.Join(".gemini", "skills"),
			GlobalDir: filepath.Join(p.home, ".gemini", "skills"), ConfigDir: filepath.Join(p.home, ".gemini")},
		{Name: "github-copilot", DisplayName: "GitHub Copilot", ProjectDir: canonicalRel,
			GlobalDir: filepath.Join(p.home, ".copilot", "skills"), ConfigDir: filepath.Join(p.home, ".copilot")},
		{Name: "goose", DisplayName: "Goose", ProjectDir: filepath.Join(".goose", "skills"),
			GlobalDir: filepath.Join(p.configHome, "goose", "skills"), ConfigDir: filepath.Join(p.configHome, "goose")},
		{Name: "kilocode", DisplayName: "Kilo Code", ProjectDir: filepath.Join(".kilocode", "skills"),
			GlobalDir: filepath.Join(p.home, ".kilocode", "skills"), ConfigDir: filepath.Join(p.home, ".kilocode")},
		{Name: "opencode", DisplayName: "OpenCode", ProjectDir: canonicalRel,
			GlobalDir: filepath.Join(p.configHome, "opencode", "skills"), ConfigDir: filepath.Join(p.configHome, "opencode")},
		{Name: "qwen-code", DisplayName: "Qwen Code", ProjectDir: filepath.Join(".qwen", "skills"),
			GlobalDir: filepath.Join(p.home, ".qwen", "skills"), ConfigDir: filepath.Join(p.home, ".qwen")},
		{Name: "roo", DisplayName: "Roo Code", ProjectDir: filepath.Join(".roo", "skills"),
			GlobalDir: filepath.Join(p.home, ".roo", "skills"), ConfigDir: filepath.Join(p.home, ".roo")},
		{Name: "windsurf", DisplayName: "Windsurf", ProjectDir: filepath.Join(".windsurf", "skills"),
			GlobalDir: filepath.Join(p.home, ".codeium", "windsurf", "skills"), ConfigDir: filepath.Join(p.home, ".codeium", "windsurf")},
	}
}

// Registry returns the agent table for the current machine.
func Registry() []Agent {
	return buildRegistry(resolveHostPaths())
}

// DetectedAgents returns the installed agents, sorted by name. This is the default install
// target set (a skill is installed for every agent the user actually has).
func DetectedAgents() []Agent {
	var out []Agent
	for _, a := range Registry() {
		if a.Detected() {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// FindAgent looks up an agent by canonical name (case-insensitive). ok is false if unknown.
func FindAgent(name string) (Agent, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, a := range Registry() {
		if strings.ToLower(a.Name) == want {
			return a, true
		}
	}
	return Agent{}, false
}

// dirExists reports whether path exists and is a directory (following symlinks).
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
