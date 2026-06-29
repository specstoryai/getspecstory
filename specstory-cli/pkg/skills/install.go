package skills

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// This file holds the on-disk mechanics: it writes a skill to the canonical
// .agents/skills/<name> store and symlinks it into each selected agent's skills directory,
// exactly like npx skills' installer. The cloud delivers a skill as a single SKILL.md body,
// so the canonical directory contains one file; the layout (canonical + per-agent symlink)
// still matches so the two tools interoperate.

// skillNamePattern keeps only lowercase letters, digits, dots and underscores; every other
// run collapses to a hyphen. This mirrors npx skills' sanitizeName and, critically, makes a
// cloud-supplied name safe to use as a directory (no path traversal).
var skillNamePattern = regexp.MustCompile(`[^a-z0-9._]+`)

// SanitizeName converts a skill name into a filesystem-safe, kebab-case directory name.
func SanitizeName(name string) string {
	s := skillNamePattern.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, ".-")
	if len(s) > 255 {
		s = s[:255]
	}
	if s == "" {
		return "unnamed-skill"
	}
	return s
}

// canonicalSkillsDir is the base canonical store: ~/.agents/skills (global) or
// <project>/.agents/skills (project).
func canonicalSkillsDir(global bool, projectDir string) string {
	if global {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, agentsDirName, skillsSubdir)
	}
	return filepath.Join(projectDir, agentsDirName, skillsSubdir)
}

// agentBaseDir is where an agent's skills live for the chosen scope. Universal agents always
// resolve to the canonical store (their store IS .agents/skills).
func agentBaseDir(a Agent, global bool, projectDir string) string {
	if a.Universal() {
		return canonicalSkillsDir(global, projectDir)
	}
	if global {
		return a.GlobalDir
	}
	return filepath.Join(projectDir, a.ProjectDir)
}

// diskInstallResult reports what installSkillToDisk did, so the engine can record the lock
// entry and the UI can show where the skill landed.
type diskInstallResult struct {
	canonicalDir string
	agents       []string // agent names the skill is now available to (symlinked or universal-covered)
	skipped      []string // agents skipped because their project config dir is absent
}

// installSkillToDisk writes skillMd to the canonical store and links it into each target
// agent. targets that are not detected for a project install (their config dir is absent)
// are skipped rather than created, matching npx skills' behavior.
func installSkillToDisk(name, skillMd string, global bool, projectDir string, targets []Agent) (diskInstallResult, error) {
	safe := SanitizeName(name)
	canonicalBase := canonicalSkillsDir(global, projectDir)
	canonicalDir := filepath.Join(canonicalBase, safe)

	if !pathWithin(canonicalBase, canonicalDir) {
		return diskInstallResult{}, fmt.Errorf("invalid skill name %q: path traversal", name)
	}

	if err := cleanAndCreateDir(canonicalDir); err != nil {
		return diskInstallResult{}, fmt.Errorf("creating skill directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(canonicalDir, "SKILL.md"), []byte(skillMd), 0o644); err != nil {
		return diskInstallResult{}, fmt.Errorf("writing SKILL.md: %w", err)
	}

	res := diskInstallResult{canonicalDir: canonicalDir}
	for _, a := range targets {
		// Universal agents read the canonical store directly — no symlink needed.
		if a.Universal() {
			res.agents = append(res.agents, a.Name)
			continue
		}
		// For a project install, don't materialize an agent's config tree (.windsurf, etc.)
		// when the user isn't using that agent in this project; the skill is still available
		// via .agents/skills.
		if !global {
			root := filepath.Join(projectDir, firstPathSegment(a.ProjectDir))
			if !dirExists(root) {
				res.skipped = append(res.skipped, a.Name)
				continue
			}
		}
		agentBase := agentBaseDir(a, global, projectDir)
		if agentBase == "" { // agent has no store for this scope (e.g. no global dir)
			res.skipped = append(res.skipped, a.Name)
			continue
		}
		agentDir := filepath.Join(agentBase, safe)
		if err := linkOrCopy(canonicalDir, agentDir); err != nil {
			return res, fmt.Errorf("installing for %s: %w", a.DisplayName, err)
		}
		res.agents = append(res.agents, a.Name)
	}
	return res, nil
}

// removeSkillFromDisk undoes an install recorded in entry: it removes each agent symlink/dir
// and the canonical directory. It is best-effort across agents so one failure doesn't strand
// the rest; the first hard error is returned after attempting all removals.
func removeSkillFromDisk(name string, entry LockEntry) error {
	safe := SanitizeName(name)
	global := entry.Scope == "global"
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	for _, agentName := range entry.Agents {
		a, ok := FindAgent(agentName)
		if !ok {
			continue
		}
		if a.Universal() {
			continue // universal agents share the canonical dir, removed below
		}
		base := agentBaseDir(a, global, entry.ProjectPath)
		if base == "" {
			continue
		}
		note(removePath(filepath.Join(base, safe)))
	}

	// Remove the canonical directory (the recorded path is authoritative; fall back to a
	// recompute if the entry predates that field).
	canonical := entry.CanonicalPath
	if canonical == "" {
		canonical = filepath.Join(canonicalSkillsDir(global, entry.ProjectPath), safe)
	}
	note(removePath(canonical))
	return firstErr
}

// linkOrCopy creates a relative symlink at linkPath pointing to target; on failure (e.g. a
// filesystem without symlink support) it falls back to copying the directory.
func linkOrCopy(target, linkPath string) error {
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return err
	}
	// Replace anything already at the link path so reinstall is idempotent.
	if err := removePath(linkPath); err != nil {
		return err
	}
	rel, err := filepath.Rel(filepath.Dir(linkPath), target)
	if err != nil {
		rel = target // fall back to an absolute target
	}
	if err := os.Symlink(rel, linkPath); err == nil {
		return nil
	}
	// Symlink failed — copy the canonical directory instead.
	return copyDir(target, linkPath)
}

// removePath deletes a file, directory, or symlink if present. Absent is success.
func removePath(path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.RemoveAll(path)
}

// cleanAndCreateDir removes any existing directory and recreates it empty, so a reinstall
// never leaves behind files renamed away in a newer version of the skill.
func cleanAndCreateDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

// copyDir recursively copies src to dst (the symlink-unsupported fallback).
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}

// pathWithin reports whether target is base itself or nested inside it (path-traversal guard).
func pathWithin(base, target string) bool {
	b := filepath.Clean(base)
	t := filepath.Clean(target)
	if t == b {
		return true
	}
	return strings.HasPrefix(t, b+string(filepath.Separator))
}

// firstPathSegment returns the first element of a relative path (".claude/skills" -> ".claude").
func firstPathSegment(rel string) string {
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 {
		return rel
	}
	return parts[0]
}
