package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/config"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/skills"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// CreateSkillsCommand builds the `specstory skills` command: an interactive TUI to browse,
// preview, approve/reject, and install the skills SpecStory Cloud generated from your coding
// sessions — plus non-interactive `--json` subcommands that expose the SAME engine so a
// front end (e.g. the VS Code extension) can drive identical behavior by shelling out.
//
// Running `specstory skills` with no subcommand opens the TUI; the subcommands (list, show,
// install, uninstall, reinstall, approve, reject, status) are the machine-readable surface.
func CreateSkillsCommand(cloudURL *string) *cobra.Command {
	longDesc := `Browse, approve, and install skills generated from your coding sessions.

SpecStory Cloud mines your synced sessions into reusable skills. 'skills' opens an
interactive browser to preview them, approve or reject the ones awaiting review, and install
the ready ones into your coding agents (Claude Code, Codex, Cursor, and more) — installed
skills can be uninstalled or reinstalled at any time.

Requires a SpecStory Cloud login and a Pro plan. Every action is also available as a
non-interactive subcommand with '--json' for scripting and front-end integration.`

	skillsCmd := &cobra.Command{
		Use:   "skills",
		Short: "Browse, approve, and install skills generated from your sessions",
		Long:  longDesc,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureSkillsAccess(); err != nil {
				return err
			}
			analytics.TrackEvent(analytics.EventSkillsActivated, nil)

			eng := skills.NewEngine(mustGetwd())
			viewMode, defaultLocation := "dense", "global"
			if cfg, _ := config.Load(nil); cfg != nil {
				viewMode = cfg.GetSkillsViewMode()
				defaultLocation = cfg.GetSkillsDefaultLocation()
			}
			return runSkillsTUI(eng, viewMode, defaultLocation)
		},
	}

	// Mirrors resume: a hidden override the VS Code extension uses to point at a non-prod
	// cloud. The root PersistentPreRunE calls SetAPIBaseURL from this shared var.
	skillsCmd.PersistentFlags().StringVar(cloudURL, "cloud-url", "", "override the default cloud API base URL")
	_ = skillsCmd.PersistentFlags().MarkHidden("cloud-url")

	skillsCmd.AddCommand(
		newSkillsListCmd(),
		newSkillsShowCmd(),
		newSkillsInstallCmd(),
		newSkillsUninstallCmd(),
		newSkillsReinstallCmd(),
		newSkillsApproveCmd(),
		newSkillsRejectCmd(),
		newSkillsStatusCmd(),
	)
	return skillsCmd
}

// ---- access gating ----

// ensureSkillsAccess verifies the user is logged in and has the Pro "skills" entitlement.
// It is the single gate for both the TUI and the subcommands. The server also enforces the
// entitlement, so this is a fast, friendly client-side check, not the security boundary.
func ensureSkillsAccess() error {
	if !cloud.IsAuthenticated() {
		return utils.ValidationError{Message: "skills require a SpecStory Cloud login. Run 'specstory login' first."}
	}
	ent, err := cloud.GetEntitlement()
	if err != nil {
		return fmt.Errorf("checking your plan: %w", err)
	}
	if !ent.Features.Skills {
		plan := ent.Plan
		if plan == "" {
			plan = "free"
		}
		return utils.ValidationError{Message: fmt.Sprintf(
			"skills require a Pro plan (your plan: %s). Upgrade at https://cloud.specstory.com to enable skills.", plan)}
	}
	return nil
}

// ---- non-interactive subcommands (the machine-readable / front-end surface) ----

func newSkillsListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your cloud skills and their local install state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureSkillsAccess(); err != nil {
				return err
			}
			views, err := skills.NewEngine(mustGetwd()).List()
			if err != nil {
				return err
			}
			if jsonOut {
				return printJSON(cmd.OutOrStdout(), views)
			}
			renderSkillsTable(cmd.OutOrStdout(), views)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output JSON")
	return cmd
}

func newSkillsShowCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a skill's details and SKILL.md content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureSkillsAccess(); err != nil {
				return err
			}
			view, err := skills.NewEngine(mustGetwd()).Get(args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return printJSON(cmd.OutOrStdout(), view)
			}
			fprintf(cmd.OutOrStdout(), "%s  (%s)\n\n%s\n", view.Name, view.State, view.SkillMd)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output JSON")
	return cmd
}

func newSkillsInstallCmd() *cobra.Command {
	var jsonOut, global, project bool
	var agents []string
	cmd := &cobra.Command{
		Use:   "install <name>",
		Short: "Install a ready skill into your agents",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureSkillsAccess(); err != nil {
				return err
			}
			opts, err := installOptionsFromFlags(global, project, agents)
			if err != nil {
				return err
			}
			report, err := skills.NewEngine(mustGetwd()).Install(args[0], opts)
			if err != nil {
				return err
			}
			analytics.TrackEvent(analytics.EventSkillsInstalled, analytics.Properties{
				"scope": report.Scope, "agents": len(report.Agents),
			})
			_ = config.SaveSkillsPrefs("", report.Scope)
			if jsonOut {
				return printJSON(cmd.OutOrStdout(), report)
			}
			renderInstallReport(cmd.OutOrStdout(), report)
			return nil
		},
	}
	addInstallFlags(cmd, &jsonOut, &global, &project, &agents)
	return cmd
}

func newSkillsReinstallCmd() *cobra.Command {
	var jsonOut, global, project bool
	var agents []string
	cmd := &cobra.Command{
		Use:   "reinstall <name>",
		Short: "Reinstall a skill, refreshing it to the current cloud version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureSkillsAccess(); err != nil {
				return err
			}
			opts, err := installOptionsFromFlags(global, project, agents)
			if err != nil {
				return err
			}
			scopeExplicit := cmd.Flags().Changed("global") || cmd.Flags().Changed("project")
			report, err := skills.NewEngine(mustGetwd()).Reinstall(args[0], opts, scopeExplicit)
			if err != nil {
				return err
			}
			analytics.TrackEvent(analytics.EventSkillsInstalled, analytics.Properties{
				"scope": report.Scope, "agents": len(report.Agents), "reinstall": true,
			})
			if jsonOut {
				return printJSON(cmd.OutOrStdout(), report)
			}
			renderInstallReport(cmd.OutOrStdout(), report)
			return nil
		},
	}
	addInstallFlags(cmd, &jsonOut, &global, &project, &agents)
	return cmd
}

func newSkillsUninstallCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "uninstall <name>",
		Short: "Uninstall a locally installed skill",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureSkillsAccess(); err != nil {
				return err
			}
			report, err := skills.NewEngine(mustGetwd()).Uninstall(args[0])
			if err != nil {
				return err
			}
			analytics.TrackEvent(analytics.EventSkillsUninstalled, nil)
			if jsonOut {
				return printJSON(cmd.OutOrStdout(), report)
			}
			fprintf(cmd.OutOrStdout(), "Uninstalled %s.\n", report.Name)
			if report.CloudSyncError != "" {
				fprintf(cmd.OutOrStdout(), "  (cloud state not updated: %s)\n", report.CloudSyncError)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output JSON")
	return cmd
}

func newSkillsApproveCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "approve <name>",
		Short: "Approve a skill awaiting review (forges it into your library)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureSkillsAccess(); err != nil {
				return err
			}
			eng := skills.NewEngine(mustGetwd())
			view, err := eng.Get(args[0])
			if err != nil {
				return err
			}
			if err := eng.Approve(view); err != nil {
				return err
			}
			analytics.TrackEvent(analytics.EventSkillsApproved, nil)
			if jsonOut {
				return printJSON(cmd.OutOrStdout(), map[string]any{"name": view.Name, "approved": true})
			}
			fprintf(cmd.OutOrStdout(), "Approved %s — it's now ready to install.\n", view.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output JSON")
	return cmd
}

func newSkillsRejectCmd() *cobra.Command {
	var jsonOut bool
	var note string
	cmd := &cobra.Command{
		Use:   "reject <name>",
		Short: "Reject a skill awaiting review",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureSkillsAccess(); err != nil {
				return err
			}
			eng := skills.NewEngine(mustGetwd())
			view, err := eng.Get(args[0])
			if err != nil {
				return err
			}
			if err := eng.Decline(view, note); err != nil {
				return err
			}
			analytics.TrackEvent(analytics.EventSkillsRejected, nil)
			if jsonOut {
				return printJSON(cmd.OutOrStdout(), map[string]any{"name": view.Name, "rejected": true})
			}
			fprintf(cmd.OutOrStdout(), "Rejected %s.\n", view.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output JSON")
	cmd.Flags().StringVar(&note, "note", "", "optional reason for rejecting")
	return cmd
}

func newSkillsStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show locally installed skills (from SpecStory Cloud)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// status reads only the local lock file, so it works without a login.
			installed, err := skills.NewEngine(mustGetwd()).InstalledSkills()
			if err != nil {
				return err
			}
			if jsonOut {
				return printJSON(cmd.OutOrStdout(), installed)
			}
			if len(installed) == 0 {
				fprintln(cmd.OutOrStdout(), "No SpecStory Cloud skills installed.")
				return nil
			}
			for _, s := range installed {
				fprintf(cmd.OutOrStdout(), "%-30s %-8s %s\n", s.Name, s.Scope, strings.Join(s.Agents, ", "))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output JSON")
	return cmd
}

// ---- shared subcommand helpers ----

func addInstallFlags(cmd *cobra.Command, jsonOut, global, project *bool, agents *[]string) {
	cmd.Flags().BoolVar(jsonOut, "json", false, "output JSON")
	cmd.Flags().BoolVar(global, "global", false, "install into the global store (~/.agents/skills) — the default")
	cmd.Flags().BoolVar(project, "project", false, "install into the current project (./.agents/skills)")
	cmd.Flags().StringSliceVar(agents, "agents", nil, "comma-separated agents to install for (default: all detected)")
}

// installOptionsFromFlags resolves the location and agent selection. Global is the default;
// --project flips it. Specifying both is an error.
func installOptionsFromFlags(global, project bool, agents []string) (skills.InstallOptions, error) {
	if global && project {
		return skills.InstallOptions{}, utils.ValidationError{Message: "choose one of --global or --project, not both"}
	}
	return skills.InstallOptions{Global: !project, Agents: agents}, nil
}

func mustGetwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		slog.Error("Failed to get current working directory", "error", err)
		return "."
	}
	return cwd
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func renderSkillsTable(w io.Writer, views []skills.SkillView) {
	if len(views) == 0 {
		fprintln(w, "No skills yet. Run agents with SpecStory, sync, and skills will be generated for you.")
		return
	}
	for _, v := range views {
		marker := " "
		if v.LocallyInstalled {
			marker = "✓"
		}
		fprintf(w, "%s %-9s %-30s %s\n", marker, v.State, truncate(v.Name, 30), truncate(v.Trigger, 60))
	}
}

func renderInstallReport(w io.Writer, r skills.InstallReport) {
	fprintf(w, "Installed %s (%s) for: %s\n", r.Name, r.Scope, strings.Join(r.Agents, ", "))
	fprintf(w, "  %s\n", r.CanonicalPath)
	if len(r.Skipped) > 0 {
		fprintf(w, "  skipped (not used in this project): %s\n", strings.Join(r.Skipped, ", "))
	}
	if r.CloudSyncError != "" {
		fprintf(w, "  (cloud state not updated: %s)\n", r.CloudSyncError)
	}
}

// ---- interactive TUI ----

// skillsMode is the TUI's top-level screen.
type skillsMode int

const (
	skillsModeList    skillsMode = iota // browsing the skill list
	skillsModeInstall                   // the install panel (location + agent selection)
	skillsModeConfirm                   // a yes/no confirmation for approve/reject/uninstall/reinstall
)

// skillsTUI is the model behind `specstory skills`. It mirrors the resume picker's structure
// (Init/Update/View, a glamour preview overlay, a hand-rolled list) and reuses its shared
// lipgloss styles and helpers (styDim, styCursor, headerRow, truncate, renderGlamour, ...).
type skillsTUI struct {
	engine          *skills.Engine
	all             []skills.SkillView
	filtered        []skills.SkillView
	cursor, top     int
	filterCycle     []string // "", review, ready, installed
	filter          string
	viewMode        string
	defaultLocation string

	previewing  bool
	reader      viewport.Model
	readerSkill *skills.SkillView

	mode skillsMode

	// install panel state
	pendingInstall *skills.SkillView
	installGlobal  bool
	detected       []skills.Agent
	agentSel       []bool
	installCursor  int // 0 = location row, 1..len(detected) = agent rows

	// confirm state. pendingActionCmd is the async network action to run if the user
	// confirms (built by the start* helpers); it emits an actionResultMsg.
	confirmMsg       string
	pendingActionCmd tea.Cmd

	// async state. loading = the initial library fetch is in flight; busy = a mutating
	// action (approve/install/...) is in flight. While either is true the spinner runs
	// (spinning guards against starting a second tick loop) and list keys are ignored.
	loading  bool
	busy     bool
	spinning bool
	spinner  spinner.Model

	status        string
	width, height int
}

// skillsLoadedMsg carries the result of an async library fetch (initial load or refresh).
type skillsLoadedMsg struct {
	views []skills.SkillView
	err   error
}

// actionResultMsg carries the result of an async mutating action.
type actionResultMsg struct {
	status string
	err    error
	reload bool   // re-fetch the library after a successful mutation
	scope  string // install scope to remember as the new default, if any
}

// runSkillsTUI runs the interactive browser. The library is fetched asynchronously after the
// program starts (Init), so the UI paints immediately with a spinner instead of blocking.
func runSkillsTUI(engine *skills.Engine, viewMode, defaultLocation string) error {
	m := skillsTUI{
		engine:          engine,
		viewMode:        viewMode,
		defaultLocation: defaultLocation,
		installGlobal:   defaultLocation != "project",
		filterCycle:     []string{"", cloud.SkillStateReview, cloud.SkillStateReady, cloud.SkillStateInstalled},
		reader:          viewport.New(),
		detected:        skills.DetectedAgents(),
		loading:         true,
		spinning:        true,
		spinner:         spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
	m.applyFilter()

	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return fmt.Errorf("skills browser failed: %w", err)
	}
	if fm, ok := final.(skillsTUI); ok {
		// Persist the last-used location for next time.
		_ = config.SaveSkillsPrefs(fm.viewMode, scopeFromGlobal(fm.installGlobal))
	}
	return nil
}

func (m skillsTUI) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadCmd())
}

func (m skillsTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.reader.SetWidth(m.width)
		m.reader.SetHeight(m.skillsPreviewHeight())
		return m, nil
	case spinner.TickMsg:
		// Keep one tick loop alive only while there is something to animate.
		if m.loading || m.busy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		m.spinning = false
		return m, nil
	case skillsLoadedMsg:
		return m.applyLoaded(msg), nil
	case actionResultMsg:
		return m.applyActionResult(msg)
	case tea.KeyPressMsg:
		// While a request is in flight, swallow input (except quit) so the user can't act on
		// stale state mid-operation.
		if m.loading || m.busy {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}
		switch {
		case m.previewing:
			return m.updatePreview(msg)
		case m.mode == skillsModeConfirm:
			return m.updateConfirm(msg)
		case m.mode == skillsModeInstall:
			return m.updateInstall(msg)
		default:
			return m.updateList(msg)
		}
	}
	return m, nil
}

// loadCmd fetches the library off the UI thread, emitting a skillsLoadedMsg.
func (m skillsTUI) loadCmd() tea.Cmd {
	eng := m.engine
	return func() tea.Msg {
		views, err := eng.List()
		return skillsLoadedMsg{views: views, err: err}
	}
}

// applyLoaded folds a fetched library into the model, preserving the cursor by name so a
// post-action refresh doesn't jump the selection.
func (m skillsTUI) applyLoaded(msg skillsLoadedMsg) skillsTUI {
	m.loading = false
	if msg.err != nil {
		m.status = "Failed to load skills: " + msg.err.Error()
		return m
	}
	selName := ""
	if sel := m.selectedSkill(); sel != nil {
		selName = sel.Name
	}
	m.all = msg.views
	m.applyFilter()
	if selName != "" {
		for i := range m.filtered {
			if m.filtered[i].Name == selName {
				m.cursor = i
				break
			}
		}
		m.clampSkillScroll()
	}
	return m
}

// applyActionResult clears the busy state, shows the outcome, and refreshes on success.
func (m skillsTUI) applyActionResult(msg actionResultMsg) (tea.Model, tea.Cmd) {
	m.busy = false
	if msg.err != nil {
		m.status = msg.err.Error()
		return m, nil
	}
	m.status = msg.status
	if msg.scope != "" {
		m.defaultLocation = msg.scope
	}
	if msg.reload {
		return m, m.loadCmd()
	}
	return m, nil
}

// startAction puts the model into the busy state and returns the command batch that runs the
// action and (re)starts the spinner if it isn't already running.
func (m *skillsTUI) startAction(cmd tea.Cmd) tea.Cmd {
	m.busy = true
	if !m.spinning {
		m.spinning = true
		return tea.Batch(m.spinner.Tick, cmd)
	}
	return cmd
}

// updateList handles keys while browsing the skill list.
func (m skillsTUI) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "up", "k":
		m.moveSkillCursor(-1)
	case "down", "j":
		m.moveSkillCursor(1)
	case "pgup":
		m.moveSkillCursor(-m.skillsListHeight())
	case "pgdown":
		m.moveSkillCursor(m.skillsListHeight())
	case "home", "g":
		m.cursor, m.top = 0, 0
	case "end", "G":
		m.cursor = len(m.filtered) - 1
		m.clampSkillScroll()
	case " ", "space":
		if sel := m.selectedSkill(); sel != nil {
			return m.openSkillPreview(sel)
		}
	case "a":
		m.filter = nextInCycle(m.filterCycle, m.filter)
		m.applyFilter()
	case "v":
		if m.viewMode == "sparse" {
			m.viewMode = "dense"
		} else {
			m.viewMode = "sparse"
		}
	case "K": // approve (review rows)
		return m.startApprove()
	case "X": // reject (review rows)
		return m.startReject()
	case "i": // install (ready rows)
		return m.startInstall()
	case "u": // uninstall (locally installed)
		return m.startUninstall()
	case "R": // reinstall (locally installed)
		return m.startReinstall()
	}
	return m, nil
}

// updatePreview handles keys while the glamour preview is open.
func (m skillsTUI) updatePreview(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case " ", "space", "esc", "q":
		m.previewing = false
		m.readerSkill = nil
		return m, nil
	}
	var cmd tea.Cmd
	m.reader, cmd = m.reader.Update(msg)
	return m, cmd
}

// updateInstall handles the install panel (location toggle + agent checklist).
func (m skillsTUI) updateInstall(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = skillsModeList
		m.pendingInstall = nil
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.installCursor > 0 {
			m.installCursor--
		}
	case "down", "j":
		if m.installCursor < len(m.detected) {
			m.installCursor++
		}
	case " ", "space":
		if m.installCursor == 0 {
			m.installGlobal = !m.installGlobal
		} else {
			i := m.installCursor - 1
			m.agentSel[i] = !m.agentSel[i]
		}
	case "enter":
		return m, m.runInstall()
	}
	return m, nil
}

// updateConfirm handles a yes/no confirmation, dispatching the pending action asynchronously.
func (m skillsTUI) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		action := m.pendingActionCmd
		m.mode = skillsModeList
		m.pendingActionCmd = nil
		if action == nil {
			return m, nil
		}
		m.status = "Working…"
		return m, m.startAction(action)
	case "n", "N", "esc", "q":
		m.mode = skillsModeList
		m.pendingActionCmd = nil
		m.status = "Cancelled."
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// ---- action starters ----

func (m skillsTUI) startApprove() (tea.Model, tea.Cmd) {
	sel := m.selectedSkill()
	if sel == nil || sel.State != cloud.SkillStateReview {
		m.status = "Only skills awaiting review can be approved."
		return m, nil
	}
	name := sel.Name
	eng := m.engine
	m.confirmMsg = fmt.Sprintf("Approve %q? It will be forged into your library and become installable.", name)
	m.pendingActionCmd = func() tea.Msg {
		view, err := eng.Get(name)
		if err == nil {
			err = eng.Approve(view)
		}
		if err != nil {
			return actionResultMsg{err: fmt.Errorf("approve failed: %w", err)}
		}
		analytics.TrackEvent(analytics.EventSkillsApproved, nil)
		return actionResultMsg{status: fmt.Sprintf("Approved %s.", name), reload: true}
	}
	m.mode = skillsModeConfirm
	return m, nil
}

func (m skillsTUI) startReject() (tea.Model, tea.Cmd) {
	sel := m.selectedSkill()
	if sel == nil || sel.State != cloud.SkillStateReview {
		m.status = "Only skills awaiting review can be rejected."
		return m, nil
	}
	name := sel.Name
	eng := m.engine
	m.confirmMsg = fmt.Sprintf("Reject %q? It will be dismissed from your review queue.", name)
	m.pendingActionCmd = func() tea.Msg {
		view, err := eng.Get(name)
		if err == nil {
			err = eng.Decline(view, "")
		}
		if err != nil {
			return actionResultMsg{err: fmt.Errorf("reject failed: %w", err)}
		}
		analytics.TrackEvent(analytics.EventSkillsRejected, nil)
		return actionResultMsg{status: fmt.Sprintf("Rejected %s.", name), reload: true}
	}
	m.mode = skillsModeConfirm
	return m, nil
}

func (m skillsTUI) startInstall() (tea.Model, tea.Cmd) {
	sel := m.selectedSkill()
	if sel == nil {
		return m, nil
	}
	if sel.State == cloud.SkillStateReview {
		m.status = "Approve this skill before installing it."
		return m, nil
	}
	m.pendingInstall = sel
	m.installGlobal = m.defaultLocation != "project"
	m.detected = skills.DetectedAgents()
	m.agentSel = make([]bool, len(m.detected))
	for i := range m.agentSel {
		m.agentSel[i] = true // default: install for every detected agent
	}
	m.installCursor = 0
	m.mode = skillsModeInstall
	return m, nil
}

func (m skillsTUI) startUninstall() (tea.Model, tea.Cmd) {
	sel := m.selectedSkill()
	if sel == nil || !sel.LocallyInstalled {
		m.status = "This skill isn't installed locally."
		return m, nil
	}
	name := sel.Name
	eng := m.engine
	m.confirmMsg = fmt.Sprintf("Uninstall %q? Its files and agent links will be removed.", name)
	m.pendingActionCmd = func() tea.Msg {
		report, err := eng.Uninstall(name)
		if err != nil {
			return actionResultMsg{err: fmt.Errorf("uninstall failed: %w", err)}
		}
		analytics.TrackEvent(analytics.EventSkillsUninstalled, nil)
		status := fmt.Sprintf("Uninstalled %s.", name)
		if report.CloudSyncError != "" {
			status = fmt.Sprintf("Uninstalled %s (cloud state not updated).", name)
		}
		return actionResultMsg{status: status, reload: true}
	}
	m.mode = skillsModeConfirm
	return m, nil
}

func (m skillsTUI) startReinstall() (tea.Model, tea.Cmd) {
	sel := m.selectedSkill()
	if sel == nil || !sel.LocallyInstalled {
		m.status = "This skill isn't installed locally."
		return m, nil
	}
	name := sel.Name
	eng := m.engine
	m.confirmMsg = fmt.Sprintf("Reinstall %q? It will be refreshed to the current cloud version for the same agents.", name)
	m.pendingActionCmd = func() tea.Msg {
		report, err := eng.Reinstall(name, skills.InstallOptions{}, false)
		if err != nil {
			return actionResultMsg{err: fmt.Errorf("reinstall failed: %w", err)}
		}
		analytics.TrackEvent(analytics.EventSkillsInstalled, analytics.Properties{"scope": report.Scope, "reinstall": true})
		return actionResultMsg{status: fmt.Sprintf("Reinstalled %s.", name), reload: true}
	}
	m.mode = skillsModeConfirm
	return m, nil
}

// runInstall validates the panel selections and returns an async command that performs the
// install off the UI thread, emitting an actionResultMsg.
func (m *skillsTUI) runInstall() tea.Cmd {
	if m.pendingInstall == nil {
		m.mode = skillsModeList
		return nil
	}
	name := m.pendingInstall.Name
	global := m.installGlobal
	var agents []string
	for i, on := range m.agentSel {
		if on {
			agents = append(agents, m.detected[i].Name)
		}
	}
	if len(agents) == 0 {
		m.status = "Select at least one agent (space to toggle)."
		return nil
	}
	m.mode = skillsModeList
	m.pendingInstall = nil
	m.status = "Installing…"

	eng := m.engine
	cmd := func() tea.Msg {
		report, err := eng.Install(name, skills.InstallOptions{Global: global, Agents: agents})
		if err != nil {
			return actionResultMsg{err: fmt.Errorf("install failed: %w", err)}
		}
		analytics.TrackEvent(analytics.EventSkillsInstalled, analytics.Properties{
			"scope": report.Scope, "agents": len(report.Agents),
		})
		return actionResultMsg{
			status: fmt.Sprintf("Installed %s (%s) for %s.", name, report.Scope, strings.Join(report.Agents, ", ")),
			reload: true,
			scope:  report.Scope,
		}
	}
	return m.startAction(cmd)
}

// ---- list mechanics ----

func (m *skillsTUI) applyFilter() {
	out := make([]skills.SkillView, 0, len(m.all))
	for _, v := range m.all {
		if m.filter == "" || v.State == m.filter {
			out = append(out, v)
		}
	}
	m.filtered = out
	if m.cursor > len(m.filtered)-1 {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.top = 0
	m.clampSkillScroll()
}

func (m *skillsTUI) moveSkillCursor(delta int) {
	moveCursorWithin(&m.cursor, &m.top, delta, len(m.filtered), m.skillsListHeight())
}

func (m *skillsTUI) clampSkillScroll() {
	clampScrollWithin(&m.cursor, &m.top, m.skillsListHeight())
}

func (m skillsTUI) selectedSkill() *skills.SkillView {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return nil
	}
	return &m.filtered[m.cursor]
}

func (m skillsTUI) openSkillPreview(s *skills.SkillView) (tea.Model, tea.Cmd) {
	m.readerSkill = s
	m.reader.SetWidth(m.width)
	m.reader.SetHeight(m.skillsPreviewHeight())
	body := s.SkillMd
	if strings.TrimSpace(body) == "" {
		body = "_(no content)_"
	}
	m.reader.SetContent(renderGlamour(body, m.width))
	m.reader.GotoTop()
	m.previewing = true
	return m, nil
}

func (m skillsTUI) skillsListHeight() int {
	const chrome = 6 // header + two rules + status + footer + margin
	avail := m.height - chrome
	per := 1
	if m.viewMode == "sparse" {
		per = 2
	}
	if avail < 1 {
		avail = 1
	}
	n := avail / per
	if n < 1 {
		n = 1
	}
	return n
}

func (m skillsTUI) skillsPreviewHeight() int {
	h := m.height - 4
	if h < 1 {
		h = 1
	}
	return h
}

func (m skillsTUI) skillsLineWidth() int {
	if m.width < 20 {
		return 80
	}
	return m.width
}

// ---- rendering ----

func (m skillsTUI) View() tea.View {
	var content string
	switch {
	case m.previewing:
		content = m.renderSkillPreview()
	case m.mode == skillsModeInstall:
		content = m.renderInstallPanel()
	case m.mode == skillsModeConfirm:
		content = m.renderConfirm()
	default:
		content = m.renderSkillList()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m skillsTUI) renderSkillList() string {
	var b strings.Builder
	scope := "all"
	if m.filter != "" {
		scope = m.filter
	}
	left := styBold.Render("SpecStory Skills") + styDim.Render("  ·  ") + styDim.Render("filter: ") + stySel.Render(scope)
	right := styDim.Render(fmt.Sprintf("%d skills", len(m.filtered)))
	b.WriteString(headerRow(left, right, m.skillsLineWidth()))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.skillsLineWidth()))
	b.WriteString("\n")
	b.WriteString(m.renderSkillRows())
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.skillsLineWidth()))
	b.WriteString("\n")
	status := m.status
	if m.busy || m.loading {
		status = m.spinner.View() + " " + status
	}
	if strings.TrimSpace(status) != "" {
		b.WriteString(styFaint.Render(status))
		b.WriteString("\n")
	}
	b.WriteString(m.renderSkillFooter())
	return b.String()
}

func (m skillsTUI) renderSkillRows() string {
	if m.loading && len(m.all) == 0 {
		return styFaint.Render("  " + m.spinner.View() + " Loading skills…")
	}
	if len(m.all) == 0 {
		return styFaint.Render("  No skills generated yet. Keep coding with SpecStory syncing your sessions — skills will appear here.")
	}
	if len(m.filtered) == 0 {
		return styFaint.Render("  No skills match this filter.")
	}
	h := m.skillsListHeight()
	end := min(m.top+h, len(m.filtered))
	var b strings.Builder
	for i := m.top; i < end; i++ {
		b.WriteString(m.skillRow(m.filtered[i], i == m.cursor))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m skillsTUI) skillRow(v skills.SkillView, selected bool) string {
	cursor := rowCursor(selected)
	state := skillStateBadge(v.State)
	installed := "  "
	if v.LocallyInstalled {
		installed = stySel.Render("✓ ")
	}
	name := truncate(v.Name, 34)
	if selected {
		name = stySel.Render(name)
	}
	if m.viewMode == "sparse" {
		sub := "      " + styFaint.Render(truncate(v.Trigger, m.skillsLineWidth()-8))
		drift := ""
		if v.Drift {
			drift = styFaint.Render("  · update available")
		}
		return cursor + installed + state + " " + name + drift + "\n" + sub
	}
	trigger := styDim.Render(truncate(v.Trigger, m.skillsLineWidth()-52))
	return cursor + installed + state + " " + fmt.Sprintf("%-34s", name) + " " + trigger
}

func (m skillsTUI) renderSkillFooter() string {
	keys := []string{"↑↓ move", "space preview", "a filter", "i install", "K keep", "X dismiss", "u uninstall", "R reinstall", "q quit"}
	return styDim.Render(strings.Join(keys, " · "))
}

func (m skillsTUI) renderSkillPreview() string {
	var b strings.Builder
	left := styBold.Render("Preview")
	if s := m.readerSkill; s != nil {
		left += styDim.Render(" · ") + skillStateBadge(s.State) + styDim.Render(" · ") + s.Name
	}
	b.WriteString(left)
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.skillsLineWidth()))
	b.WriteString("\n")
	b.WriteString(m.reader.View())
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.skillsLineWidth()))
	b.WriteString("\n")
	b.WriteString(styDim.Render(strings.Join([]string{"↑↓ scroll", "space/esc close"}, " · ")))
	return b.String()
}

func (m skillsTUI) renderInstallPanel() string {
	var b strings.Builder
	name := ""
	if m.pendingInstall != nil {
		name = m.pendingInstall.Name
	}
	b.WriteString(styBold.Render("Install " + name))
	b.WriteString("\n\n")

	// Location row.
	locCursor := "   "
	if m.installCursor == 0 {
		locCursor = styCursor.Render(" ▸ ")
	}
	loc := "global  (~/.agents/skills)"
	if !m.installGlobal {
		loc = "project (./.agents/skills)"
	}
	b.WriteString(locCursor + styDim.Render("location: ") + stySel.Render(loc) + styFaint.Render("  (space to toggle)"))
	b.WriteString("\n\n")

	b.WriteString(styDim.Render("  install for these agents (space to toggle):"))
	b.WriteString("\n")
	if len(m.detected) == 0 {
		b.WriteString(styFaint.Render("    No known agents detected on this machine."))
		b.WriteString("\n")
	}
	for i, a := range m.detected {
		cursor := "   "
		if m.installCursor == i+1 {
			cursor = styCursor.Render(" ▸ ")
		}
		check := "[ ]"
		if m.agentSel[i] {
			check = "[x]"
		}
		b.WriteString(cursor + check + " " + a.DisplayName)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(styDim.Render(strings.Join([]string{"↑↓ move", "space toggle", "↵ install", "esc cancel"}, " · ")))
	return b.String()
}

func (m skillsTUI) renderConfirm() string {
	var b strings.Builder
	b.WriteString(styBold.Render("Confirm"))
	b.WriteString("\n\n")
	b.WriteString("  " + m.confirmMsg)
	b.WriteString("\n\n")
	b.WriteString(styDim.Render(strings.Join([]string{"y confirm", "n cancel"}, " · ")))
	return b.String()
}

// skillStateBadge renders a short colored state tag.
func skillStateBadge(state string) string {
	switch state {
	case cloud.SkillStateReview:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("review   ")
	case cloud.SkillStateReady:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("ready    ")
	case cloud.SkillStateInstalled:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render("installed")
	default:
		return fmt.Sprintf("%-9s", state)
	}
}

func scopeFromGlobal(global bool) string {
	if global {
		return "global"
	}
	return "project"
}
