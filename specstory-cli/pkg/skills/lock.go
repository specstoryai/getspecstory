package skills

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The lock file is the on-disk record of which skills are installed. We deliberately reuse
// the SAME file the public `npx skills` CLI uses (~/.agents/.skill-lock.json, or
// $XDG_STATE_HOME/skills/.skill-lock.json) so the two ecosystems coexist and `npx skills
// list` sees skills installed from SpecStory Cloud. SpecStory-owned entries are tagged with
// SourceType == sourceTypeSpecStory; we never touch entries owned by other tools.

const (
	lockVersion         = 3 // matches npx skills CURRENT_VERSION (folder-hash support)
	sourceTypeSpecStory = "specstory-cloud"
)

// LockEntry is one installed skill. The first block mirrors npx skills' SkillLockEntry
// (so the file round-trips through their tooling); the second block is SpecStory-specific
// bookkeeping needed to uninstall/reinstall cleanly. Unknown keys written by other tools
// are preserved via Extra.
type LockEntry struct {
	// --- npx skills compatible fields ---
	Source          string `json:"source"`
	SourceType      string `json:"sourceType"`
	SourceURL       string `json:"sourceUrl"`
	SkillFolderHash string `json:"skillFolderHash"`
	InstalledAt     string `json:"installedAt"`
	UpdatedAt       string `json:"updatedAt"`

	// --- SpecStory-specific bookkeeping ---
	Scope         string   `json:"specstoryScope,omitempty"`         // "global" | "project"
	ProjectPath   string   `json:"specstoryProjectPath,omitempty"`   // project root, for project-scope installs
	CanonicalPath string   `json:"specstoryCanonicalPath,omitempty"` // the .agents/skills/<name> dir
	Agents        []string `json:"specstoryAgents,omitempty"`        // agent names this skill was symlinked into
	ClusterKey    string   `json:"specstoryClusterKey,omitempty"`    // cloud re-fetch keys
	Fingerprint   string   `json:"specstoryFingerprint,omitempty"`
	ContentSha    string   `json:"specstoryContentSha,omitempty"` // server contentSha at install time, for drift detection
}

// IsSpecStory reports whether this entry was installed from SpecStory Cloud (and is thus
// ours to manage). Entries from other tools are left untouched.
func (e LockEntry) IsSpecStory() bool {
	return e.SourceType == sourceTypeSpecStory
}

// lockFile is the full document. dismissed/lastSelectedAgents from npx are preserved
// verbatim through Extra so we never clobber the other tool's state.
type lockFile struct {
	Version int                        `json:"version"`
	Skills  map[string]LockEntry       `json:"skills"`
	Extra   map[string]json.RawMessage `json:"-"` // top-level keys we don't model (dismissed, lastSelectedAgents, ...)
}

// lockPath returns the lock file path, matching npx skills' getSkillLockPath.
func lockPath() string {
	home, _ := os.UserHomeDir()
	if xdg := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); xdg != "" {
		return filepath.Join(xdg, "skills", lockFileName)
	}
	return filepath.Join(home, agentsDirName, lockFileName)
}

// readLock loads the lock file, returning an empty (current-version) document when it is
// missing or in an older incompatible format — mirroring npx skills' wipe-on-old-version.
func readLock() (*lockFile, error) {
	path := lockPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyLock(), nil
		}
		return nil, err
	}

	// Capture every top-level key so unmodeled state (dismissed, lastSelectedAgents) is
	// preserved on write.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return emptyLock(), nil // corrupt → start fresh rather than fail the command
	}

	lf := emptyLock()
	if v, ok := raw["version"]; ok {
		_ = json.Unmarshal(v, &lf.Version)
	}
	if s, ok := raw["skills"]; ok {
		_ = json.Unmarshal(s, &lf.Skills)
	}
	if lf.Version < lockVersion || lf.Skills == nil {
		return emptyLock(), nil
	}
	for k, v := range raw {
		if k == "version" || k == "skills" {
			continue
		}
		lf.Extra[k] = v
	}
	return lf, nil
}

// writeLock persists the lock file, recreating the parent directory if needed and
// preserving any unmodeled top-level keys.
func writeLock(lf *lockFile) error {
	path := lockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	// Re-assemble as a generic object so Extra keys survive the round-trip.
	out := map[string]json.RawMessage{}
	maps.Copy(out, lf.Extra)
	if b, err := json.Marshal(lf.Version); err == nil {
		out["version"] = b
	}
	if b, err := json.Marshal(lf.Skills); err == nil {
		out["skills"] = b
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func emptyLock() *lockFile {
	return &lockFile{Version: lockVersion, Skills: map[string]LockEntry{}, Extra: map[string]json.RawMessage{}}
}

// upsertLockEntry adds or updates a SpecStory skill entry, preserving the original
// installedAt on update.
func upsertLockEntry(name string, entry LockEntry) error {
	lf, err := readLock()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	entry.UpdatedAt = now
	if existing, ok := lf.Skills[name]; ok && existing.InstalledAt != "" {
		entry.InstalledAt = existing.InstalledAt
	} else {
		entry.InstalledAt = now
	}
	lf.Skills[name] = entry
	return writeLock(lf)
}

// removeLockEntry deletes a skill entry. Returns whether it existed.
func removeLockEntry(name string) (bool, error) {
	lf, err := readLock()
	if err != nil {
		return false, err
	}
	if _, ok := lf.Skills[name]; !ok {
		return false, nil
	}
	delete(lf.Skills, name)
	return true, writeLock(lf)
}

// getLockEntry returns a single skill entry, or ok=false when absent.
func getLockEntry(name string) (LockEntry, bool, error) {
	lf, err := readLock()
	if err != nil {
		return LockEntry{}, false, err
	}
	e, ok := lf.Skills[name]
	return e, ok, nil
}

// specStoryLockEntries returns only the entries installed from SpecStory Cloud.
func specStoryLockEntries() (map[string]LockEntry, error) {
	lf, err := readLock()
	if err != nil {
		return nil, err
	}
	out := map[string]LockEntry{}
	for name, e := range lf.Skills {
		if e.IsSpecStory() {
			out[name] = e
		}
	}
	return out, nil
}
