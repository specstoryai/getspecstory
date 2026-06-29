package skills

import (
	"fmt"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
)

// Engine is the single entry point both the TUI and the --json subcommands use. Every
// user-facing skill operation goes through here, so the two faces (and a future front end
// driving the CLI) always behave identically.
//
// The cloud calls used by the library/install paths are held as function fields so tests can
// substitute them without a network; they default to the real cloud package functions.
type Engine struct {
	projectDir      string
	listLibrary     func() ([]cloud.SkillRow, error)
	setInstallState func(name, state string) error
}

// NewEngine builds an engine rooted at projectDir (the current working directory), used as
// the base for project-scope installs.
func NewEngine(projectDir string) *Engine {
	return &Engine{
		projectDir:      projectDir,
		listLibrary:     cloud.ListSkillLibrary,
		setInstallState: cloud.SetInstallState,
	}
}

// SkillView is a cloud skill joined with its local install state — the unified shape the UI
// renders and the --json surface emits.
type SkillView struct {
	cloud.SkillRow
	LocallyInstalled bool     `json:"locallyInstalled"`
	InstalledScope   string   `json:"installedScope,omitempty"`
	InstalledAgents  []string `json:"installedAgents,omitempty"`
	// Drift is true when the locally installed copy was built from a different cloud version
	// than the one currently in the library (server contentSha changed since install).
	Drift bool `json:"drift"`
}

// InstallReport summarizes a completed install/reinstall.
type InstallReport struct {
	Name          string   `json:"name"`
	Scope         string   `json:"scope"`
	CanonicalPath string   `json:"canonicalPath"`
	Agents        []string `json:"agents"`
	Skipped       []string `json:"skipped,omitempty"`
	// CloudSyncError is set when the local install succeeded but flipping the cloud
	// install_state failed; the skill is on disk, the cloud just didn't record it.
	CloudSyncError string `json:"cloudSyncError,omitempty"`
}

// UninstallReport summarizes a completed uninstall.
type UninstallReport struct {
	Name           string `json:"name"`
	CloudSyncError string `json:"cloudSyncError,omitempty"`
}

// InstalledSkill is a locally tracked skill (a lock entry plus its name) for `skills status`.
type InstalledSkill struct {
	Name string `json:"name"`
	LockEntry
}

// InstallOptions configures where and for whom a skill is installed.
type InstallOptions struct {
	Global bool     // install into the global store (default true at the command layer)
	Agents []string // agent names; empty means "all detected agents"
}

// List returns every cloud skill joined with local install state, sorted newest-first by
// state rank then name (installed/ready before review, matching the web UI ordering intent).
func (e *Engine) List() ([]SkillView, error) {
	rows, err := e.listLibrary()
	if err != nil {
		return nil, err
	}
	local, err := specStoryLockEntries()
	if err != nil {
		return nil, err
	}

	views := make([]SkillView, 0, len(rows))
	for _, r := range rows {
		v := SkillView{SkillRow: r}
		if entry, ok := local[r.Name]; ok {
			v.LocallyInstalled = true
			v.InstalledScope = entry.Scope
			v.InstalledAgents = entry.Agents
			v.Drift = entry.ContentSha != "" && r.ContentSha != "" && entry.ContentSha != r.ContentSha
		}
		views = append(views, v)
	}
	sort.SliceStable(views, func(i, j int) bool {
		ri, rj := stateRank(views[i].State), stateRank(views[j].State)
		if ri != rj {
			return ri > rj
		}
		return views[i].Name < views[j].Name
	})
	return views, nil
}

// Get returns a single skill view by name.
func (e *Engine) Get(name string) (SkillView, error) {
	views, err := e.List()
	if err != nil {
		return SkillView{}, err
	}
	for _, v := range views {
		if v.Name == name {
			return v, nil
		}
	}
	return SkillView{}, fmt.Errorf("skill %q not found in your cloud library", name)
}

// Approve approves a review-state skill (forges it into the library). The view must be a
// review row carrying a dossier id.
func (e *Engine) Approve(view SkillView) error {
	if view.State != cloud.SkillStateReview {
		return fmt.Errorf("only skills awaiting review can be approved; %q is %s", view.Name, view.State)
	}
	return cloud.ApproveDossier(view.DossierID)
}

// Decline rejects a review-state skill.
func (e *Engine) Decline(view SkillView, note string) error {
	if view.State != cloud.SkillStateReview {
		return fmt.Errorf("only skills awaiting review can be rejected; %q is %s", view.Name, view.State)
	}
	return cloud.DeclineDossier(view.DossierID, note)
}

// Install downloads a ready/installed skill and installs it locally, then records the cloud
// install_state. A review-state skill must be approved first.
func (e *Engine) Install(name string, opts InstallOptions) (InstallReport, error) {
	view, err := e.Get(name)
	if err != nil {
		return InstallReport{}, err
	}
	if view.State == cloud.SkillStateReview {
		return InstallReport{}, fmt.Errorf("%q is still awaiting review — approve it before installing", name)
	}
	if strings.TrimSpace(view.SkillMd) == "" {
		return InstallReport{}, fmt.Errorf("%q has no content to install", name)
	}

	// With no targets (no agents detected, or all deselected) the skill is still written to
	// the canonical .agents/skills store — universal agents read it directly, and `npx skills`
	// sees it. So an empty target set is a valid canonical-only install, not an error.
	targets, err := e.resolveTargets(opts.Agents)
	if err != nil {
		return InstallReport{}, err
	}

	disk, err := installSkillToDisk(name, view.SkillMd, opts.Global, e.projectDir, targets)
	if err != nil {
		return InstallReport{}, err
	}

	scope := scopeLabel(opts.Global)
	entry := LockEntry{
		Source:          sourceTypeSpecStory,
		SourceType:      sourceTypeSpecStory,
		SourceURL:       cloud.GetAPIBaseURL() + "/api/v1/lore/skills/" + name,
		SkillFolderHash: view.ContentSha,
		Scope:           scope,
		CanonicalPath:   disk.canonicalDir,
		Agents:          disk.agents,
		ClusterKey:      view.ClusterKey,
		Fingerprint:     view.Fingerprint,
		ContentSha:      view.ContentSha,
	}
	if !opts.Global {
		entry.ProjectPath = e.projectDir
	}
	if err := upsertLockEntry(name, entry); err != nil {
		return InstallReport{}, fmt.Errorf("recording installed skill: %w", err)
	}

	report := InstallReport{
		Name:          name,
		Scope:         scope,
		CanonicalPath: disk.canonicalDir,
		Agents:        disk.agents,
		Skipped:       disk.skipped,
	}
	// Flip the cloud install_state so the web/CLI agree on what's installed. The local
	// install already succeeded, so a sync failure is reported, not fatal.
	if err := e.setInstallState(name, cloud.InstallStateInstalled); err != nil {
		report.CloudSyncError = err.Error()
	}
	return report, nil
}

// Uninstall removes a locally installed skill (files + symlinks + lock entry) and clears the
// cloud install_state.
func (e *Engine) Uninstall(name string) (UninstallReport, error) {
	entry, ok, err := getLockEntry(name)
	if err != nil {
		return UninstallReport{}, err
	}
	if !ok || !entry.IsSpecStory() {
		return UninstallReport{}, fmt.Errorf("%q is not installed from SpecStory Cloud", name)
	}
	if err := removeSkillFromDisk(name, entry); err != nil {
		return UninstallReport{}, fmt.Errorf("removing skill files: %w", err)
	}
	if _, err := removeLockEntry(name); err != nil {
		return UninstallReport{}, fmt.Errorf("updating lock: %w", err)
	}

	report := UninstallReport{Name: name}
	if err := e.setInstallState(name, cloud.InstallStateAvailable); err != nil {
		report.CloudSyncError = err.Error()
	}
	return report, nil
}

// Reinstall re-fetches a skill and reinstalls it, refreshing the local copy to the current
// cloud version. When opts leaves scope/agents unset and the skill is already installed, the
// previous scope and agent set are reused.
func (e *Engine) Reinstall(name string, opts InstallOptions, scopeExplicit bool) (InstallReport, error) {
	if entry, ok, err := getLockEntry(name); err == nil && ok && entry.IsSpecStory() {
		if !scopeExplicit {
			opts.Global = entry.Scope != "project"
		}
		if len(opts.Agents) == 0 {
			opts.Agents = entry.Agents
		}
	}
	return e.Install(name, opts)
}

// TriggerRun starts a new cloud mining run and returns its id.
func (e *Engine) TriggerRun() (string, error) {
	return cloud.TriggerRun()
}

// ListRuns returns recent mining runs (newest first).
func (e *Engine) ListRuns() ([]cloud.Run, error) {
	return cloud.ListRuns()
}

// InstalledSkills returns the locally tracked SpecStory skills, sorted by name.
func (e *Engine) InstalledSkills() ([]InstalledSkill, error) {
	entries, err := specStoryLockEntries()
	if err != nil {
		return nil, err
	}
	out := make([]InstalledSkill, 0, len(entries))
	for name, entry := range entries {
		out = append(out, InstalledSkill{Name: name, LockEntry: entry})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// resolveTargets maps requested agent names to Agents. A nil names slice means "default to
// all detected agents"; an empty (non-nil) slice means "explicitly none" (canonical-only
// install). Unknown names are an error so a typo doesn't silently install nothing.
func (e *Engine) resolveTargets(names []string) ([]Agent, error) {
	if names == nil {
		return DetectedAgents(), nil
	}
	if len(names) == 0 {
		return nil, nil // explicit canonical-only
	}
	var out []Agent
	for _, n := range names {
		a, ok := FindAgent(n)
		if !ok {
			return nil, fmt.Errorf("unknown agent %q (known: %s)", n, strings.Join(agentNames(), ", "))
		}
		out = append(out, a)
	}
	return out, nil
}

// agentNames lists every known agent id, for error messages.
func agentNames() []string {
	reg := Registry()
	names := make([]string, len(reg))
	for i, a := range reg {
		names[i] = a.Name
	}
	return names
}

func scopeLabel(global bool) string {
	if global {
		return "global"
	}
	return "project"
}

// stateRank orders skills for display: installed > ready > review.
func stateRank(state string) int {
	switch state {
	case cloud.SkillStateInstalled:
		return 2
	case cloud.SkillStateReady:
		return 1
	default:
		return 0
	}
}
