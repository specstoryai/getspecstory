package cmd

import (
	"context"
	"fmt"
	"image/color"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/config"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// ---- integration with the resume command ----

// openOrBuildResumeIndex opens sessions.db, building it first (with the normal reindex
// UI) when it is missing or empty, then proceeding straight into the picker. builtFresh
// reports whether a full foreground build just ran — when it did, the index is already
// fully current, so callers skip the background warm (it would be redundant).
func openOrBuildResumeIndex() (store *sessionindex.Store, builtFresh bool, err error) {
	dbPath, err := sessionindex.DefaultPath()
	if err != nil {
		return nil, false, err
	}
	if _, statErr := os.Stat(dbPath); statErr == nil {
		s, err := sessionindex.OpenReader(dbPath)
		if err != nil {
			return nil, false, err
		}
		if n, _ := s.Count(); n > 0 {
			return s, false, nil
		}
		_ = s.Close() // empty → rebuild below
	}
	if err := runReindex(false); err != nil {
		return nil, false, err
	}
	s, err := sessionindex.OpenReader(dbPath)
	return s, true, err
}

// indexWarmedMsg is sent to the running TUI when the background warm finishes refreshing the
// CURRENT project (the first warm pass). The model re-queries so new/changed sessions for this
// project appear without a restart. The subsequent full pass is silent (no refresh).
type indexWarmedMsg struct{}

// startIndexWarm launches the background two-pass index warm for projectID, refreshing the
// running TUI (via p.Send) when the current-project pass completes. The returned cancel func
// should be invoked when the TUI exits: the warm is best-effort and must never block process
// exit. When builtFresh is true the index was just rebuilt in the foreground, so there is
// nothing to warm and this only returns a cancel func.
func startIndexWarm(p *tea.Program, projectID string, builtFresh bool) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	if builtFresh {
		return cancel
	}
	dbPath, err := sessionindex.DefaultPath()
	if err != nil {
		slog.Debug("warm: no db path; skipping", "error", err)
		return cancel
	}
	go warmIndexInBackground(ctx, dbPath, projectID, func() { p.Send(indexWarmedMsg{}) })
	return cancel
}

// selectResumeViaTUI runs the picker for the current project and returns the chosen
// resume plan (or nil if the user cancelled). On a successful selection it persists the
// view-mode and target-agent preferences to the user config.
func selectResumeViaTUI(registry *factory.Registry, store *sessionindex.Store, projectID, projectName, presetTo string, builtFresh bool) (*resumePlan, error) {
	sessions, err := store.ListByProject(projectID)
	if err != nil {
		return nil, fmt.Errorf("loading sessions: %w", err)
	}
	// An empty current project is fine — the picker opens in the all-projects browser.
	// Only bail when the whole index is empty (nothing to resume anywhere).
	if total, _ := store.Count(); total == 0 {
		fprintln(os.Stderr, "\nNo agent sessions indexed yet. Run an agent here, then try again (or `specstory reindex`).")
		return nil, nil
	}

	agents := map[string]agentMeta{}
	var installed []agentChoice
	for _, id := range registry.ListIDs() {
		prov, err := registry.Get(id)
		if err != nil {
			continue
		}
		agents[id] = agentMeta{name: prov.Name(), accent: colorForAgent(id)}
		if prov.Check("").Success {
			installed = append(installed, agentChoice{id: id, provider: prov})
		}
	}
	if len(installed) == 0 {
		fprintln(os.Stderr, "\nNo installed agents found to resume into.")
		return nil, nil
	}

	// A preset target (`resume <agent>`) must be installed to resume into. The command already
	// confirmed it is a known provider; here, with the installed set known, confirm it is
	// available and resolve it to its canonical ID. When set, the picker skips the
	// target-selection step entirely (see beginResume).
	if presetTo != "" {
		resolved := ""
		for _, a := range installed {
			if strings.EqualFold(a.id, presetTo) {
				resolved = a.id
				break
			}
		}
		if resolved == "" {
			return nil, utils.ValidationError{Message: fmt.Sprintf(
				"agent %q is not installed, so it can't be a resume target.", presetTo)}
		}
		presetTo = resolved
	}

	viewMode, lastAgent := "dense", ""
	if cfg, _ := config.Load(nil); cfg != nil {
		viewMode = cfg.GetResumeViewMode()
		lastAgent = cfg.GetResumeLastAgent()
	}

	model := newSessionTUI(store, registry, projectID, projectName, sessions, agents, installed, sessionTUIOpts{
		title:     "SpecStory Resume",
		presetTo:  presetTo,
		lastAgent: lastAgent,
		viewMode:  viewMode,
	})
	p := tea.NewProgram(model)
	cancelWarm := startIndexWarm(p, projectID, builtFresh)
	final, err := p.Run()
	cancelWarm() // stop warming the moment the picker exits (never block on it)
	if err != nil {
		return nil, fmt.Errorf("resume picker failed: %w", err)
	}
	rm, ok := final.(sessionTUI)
	if !ok {
		return nil, fmt.Errorf("resume picker returned unexpected model type %T", final)
	}
	if rm.result.cancelled || rm.result.session == nil {
		return nil, nil
	}

	fromID := rm.result.session.Agent
	fromProv, err := registry.Get(fromID)
	if err != nil {
		return nil, fmt.Errorf("unknown source agent %q: %w", fromID, err)
	}
	toProv, err := registry.Get(rm.result.targetID)
	if err != nil {
		return nil, fmt.Errorf("unknown target agent %q: %w", rm.result.targetID, err)
	}

	if err := config.SaveResumePrefs(rm.viewMode, rm.result.targetID); err != nil {
		slog.Debug("resume: could not save prefs", "error", err)
	}

	return &resumePlan{
		from:      fromProv,
		fromID:    fromID,
		sessionID: rm.result.session.SessionID,
		fromCwd:   rm.result.session.OriginCwd,
		to:        toProv,
		toID:      rm.result.targetID,
	}, nil
}

// colorForAgent returns a stable accent color per provider for the list.
func colorForAgent(id string) color.Color {
	switch id {
	case "claude":
		return lipgloss.Color("170") // purple
	case "codex":
		return lipgloss.Color("42") // green
	case "cursor":
		return lipgloss.Color("39") // blue
	case "gemini":
		return lipgloss.Color("45") // cyan
	case "droid":
		return lipgloss.Color("214") // orange
	case "deepseek":
		return lipgloss.Color("203") // red
	default:
		return lipgloss.Color("250")
	}
}

// ---- all-projects browser (Stage B) ----

// enterBrowser switches to the all-projects browser, loading the project rollup lazily.
func (m *sessionTUI) enterBrowser() {
	if !m.projectsLoaded {
		if ps, err := m.store.ListProjects(); err == nil {
			m.projects = ps
		} else {
			slog.Debug("session browser: failed to list projects", "error", err)
		}
		m.projectsLoaded = true
	}
	m.applyProjectFilter()
	m.mode = modeProjects
}

// gotoHome returns the session list to the current directory's project.
func (m *sessionTUI) gotoHome() {
	m.projectID, m.projectName = m.homeProjectID, m.homeProjectName
	m.all = m.homeSessions
	m.inBrowser = false
	m.agentFilter, m.searchQuery = "", ""
	m.search.SetValue("")
	m.cursor, m.top = 0, 0
	m.rebuildAgentCycle()
	m.applyFilter()
	m.mode = modeList
}

// drillInto opens a project's session list from the browser.
func (m *sessionTUI) drillInto(p sessionindex.ProjectSummary) {
	sessions, err := m.store.ListByProject(p.ProjectID)
	if err != nil {
		slog.Debug("session browser: failed to list project sessions", "project", p.ProjectID, "error", err)
		return
	}
	m.projectID = p.ProjectID
	m.projectName = projectDisplayName(p)
	m.all = sessions
	m.inBrowser = true
	m.agentFilter, m.searchQuery = "", ""
	m.search.SetValue("")
	m.cursor, m.top = 0, 0
	m.rebuildAgentCycle()
	m.applyFilter()
	m.mode = modeList
}

func (m sessionTUI) updateProjects(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.result = sessionTUIResult{cancelled: true}
		return m, tea.Quit
	case "esc":
		if m.startedInBrowser {
			m.result = sessionTUIResult{cancelled: true}
			return m, tea.Quit
		}
		m.gotoHome()
	case "tab":
		if !m.startedInBrowser {
			m.gotoHome()
		}
	case "up", "k":
		m.moveProjCursor(-1)
	case "down", "j":
		m.moveProjCursor(1)
	case "pgup":
		m.moveProjCursor(-m.projectsBudget())
	case "pgdown":
		m.moveProjCursor(m.projectsBudget())
	case "home", "g":
		m.projCursor, m.projTop = 0, 0
	case "end", "G":
		m.projCursor = len(m.projFiltered) - 1
		m.clampProjScroll()
	case "enter":
		if m.projCursor >= 0 && m.projCursor < len(m.projFiltered) {
			m.drillInto(m.projFiltered[m.projCursor])
		}
	case "/":
		// FTS over sessions across ALL projects (consistent with / in a session list).
		m.globalActive = true
		m.globalSearching = true
		m.globalInput.SetValue(m.globalQuery)
		return m, m.globalInput.Focus()
	case "p":
		// Filter the project list by name.
		m.projSearching = true
		m.projSearch.SetValue(m.projSearchQuery)
		return m, m.projSearch.Focus()
	}
	return m, nil
}

func (m sessionTUI) updateProjectSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.projSearching = false
		m.projSearch.Blur()
		m.projSearchQuery = ""
		m.applyProjectFilter()
		return m, nil
	case "enter":
		m.projSearching = false
		m.projSearch.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.projSearch, cmd = m.projSearch.Update(msg)
	m.projSearchQuery = m.projSearch.Value()
	m.applyProjectFilter()
	return m, cmd
}

func (m *sessionTUI) applyProjectFilter() {
	q := strings.ToLower(strings.TrimSpace(m.projSearchQuery))
	if q == "" {
		m.projFiltered = m.projects
	} else {
		out := make([]sessionindex.ProjectSummary, 0, len(m.projects))
		for _, p := range m.projects {
			if strings.Contains(strings.ToLower(projectDisplayName(p)), q) {
				out = append(out, p)
			}
		}
		m.projFiltered = out
	}
	if m.projCursor > len(m.projFiltered)-1 {
		m.projCursor = len(m.projFiltered) - 1
	}
	if m.projCursor < 0 {
		m.projCursor = 0
	}
	m.projTop = 0
}

func (m *sessionTUI) moveProjCursor(delta int) {
	n := len(m.projFiltered)
	if n == 0 {
		return
	}
	m.projCursor += delta
	if m.projCursor < 0 {
		m.projCursor = 0
	}
	if m.projCursor > n-1 {
		m.projCursor = n - 1
	}
	m.clampProjScroll()
}

// clampProjScroll scrolls projTop so the cursor row stays on screen, counting the interspersed
// date-bucket headers (via projLines) rather than assuming one line per row. The generic
// clampScrollWithin can't be used here because those headers make the rows variable-height.
func (m *sessionTUI) clampProjScroll() {
	if m.projCursor < m.projTop {
		m.projTop = m.projCursor // cursor above the window — pull the top up to it
	}
	// Cursor below the fold: advance the top until rows [projTop, projCursor] fit the budget.
	budget := m.projectsBudget()
	for m.projTop < m.projCursor && m.projLines(m.projTop, m.projCursor+1) > budget {
		m.projTop++
	}
}

// projLines is how many terminal lines the project rows [from, end) occupy, including the
// date-bucket header renderProjects emits at the first rendered row and at every bucket change.
// It is the shared accounting clampProjScroll and renderProjects both use, so they agree on
// exactly which rows fit and the cursor can never land below the fold.
func (m sessionTUI) projLines(from, end int) int {
	lines := 0
	prevBucket := "" // the first rendered row always emits a header (matches renderProjects)
	for i := from; i < end; i++ {
		if b := dateBucket(m.projFiltered[i].LastActivity); b != prevBucket {
			lines++ // bucket header line
			prevBucket = b
		}
		lines++ // the project row itself
	}
	return lines
}

// projectsBudget is the number of terminal lines available to the project list region. The
// project rows AND the date-bucket headers share this budget (see projLines), so the floor is
// 2 — room for at least the cursor row plus its header.
func (m sessionTUI) projectsBudget() int {
	const chrome = 5 // header(1) + two rules(2) + footer(1) + margin
	avail := m.height - chrome
	if avail < 2 {
		avail = 2
	}
	return avail
}

func (m sessionTUI) renderProjects() string {
	var b strings.Builder

	left := m.headerLeft("all projects")
	right := styDim.Render(fmt.Sprintf("%d projects", len(m.projFiltered)))
	b.WriteString(headerRow(left, right, m.lineWidth()) + "\n")
	b.WriteString(strings.Repeat("─", m.lineWidth()) + "\n")

	if len(m.projFiltered) == 0 {
		b.WriteString(styFaint.Render("  No projects match."))
	} else {
		// Fill the line budget exactly, charging a line for each bucket header as well as each
		// row, so the rendered block never overflows past the fold (see projLines/clampProjScroll).
		budget := m.projectsBudget()
		lines := 0
		lastBucket := ""
		for i := m.projTop; i < len(m.projFiltered); i++ {
			p := m.projFiltered[i]
			bucket := dateBucket(p.LastActivity)
			cost := 1
			if bucket != lastBucket {
				cost++ // room for the bucket header line
			}
			if lines+cost > budget {
				break // out of vertical room
			}
			if bucket != lastBucket {
				b.WriteString(styFaint.Render("  ── "+bucket) + "\n")
				lastBucket = bucket
			}
			b.WriteString(m.projectRow(p, i == m.projCursor) + "\n")
			lines += cost
		}
	}

	b.WriteString(strings.Repeat("─", m.lineWidth()) + "\n")
	if m.projSearching {
		b.WriteString(m.projSearch.View() + "    " + styFaint.Render("esc clear · enter apply"))
		return b.String()
	}
	keys := []string{"↑↓ move", "↵ open", "/ search sessions", "p filter projects"}
	if !m.startedInBrowser {
		keys = append(keys, "tab this project")
	}
	keys = append(keys, "q quit")
	b.WriteString(styDim.Render(strings.Join(keys, " · ")))
	return b.String()
}

func (m sessionTUI) projectRow(p sessionindex.ProjectSummary, selected bool) string {
	cursor := "  "
	if selected {
		cursor = styCursor.Render("▸ ")
	}
	chips := m.agentCountChips(p.AgentCounts)
	when := relativeTime(p.LastActivity)
	return cursor + renderName(projectDisplayName(p), selected, 32) + "  " +
		chips + styDim.Render("  · "+when)
}

// agentCountChips renders "claude 12 · codex 3" with colored agent tags.
func (m sessionTUI) agentCountChips(counts map[string]int) string {
	ids := make([]string, 0, len(counts))
	for id := range counts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		tag := id
		if a, ok := m.agents[id]; ok {
			tag = lipgloss.NewStyle().Foreground(a.accent).Render(id)
		}
		parts = append(parts, fmt.Sprintf("%s %d", tag, counts[id]))
	}
	return strings.Join(parts, styDim.Render(" · "))
}

func projectDisplayName(p sessionindex.ProjectSummary) string {
	if strings.TrimSpace(p.ProjectName) != "" {
		return p.ProjectName
	}
	if p.ProjectID == unknownProjectID || p.ProjectID == "" {
		return "(unknown project)"
	}
	return p.ProjectID
}

// dateBucket groups a timestamp into a relative bucket for the browser rollup.
func dateBucket(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return "Older"
	}
	switch d := dayDiff(t.Local()); {
	case d <= 0:
		return "Today"
	case d == 1:
		return "Yesterday"
	case d < 7:
		return "Previous 7 days"
	case d < 30:
		return "Previous 30 days"
	default:
		return "Older"
	}
}

// dayDiff returns whole calendar days between now and t (both local), now - t.
func dayDiff(t time.Time) int {
	now := time.Now()
	ny, nm, nd := now.Date()
	ty, tm, td := t.Date()
	a := time.Date(ny, nm, nd, 0, 0, 0, 0, now.Location())
	c := time.Date(ty, tm, td, 0, 0, 0, 0, now.Location())
	return int(a.Sub(c).Hours() / 24)
}

// ---- global session search (FTS across all projects, from the browser) ----

func (m sessionTUI) updateGlobalSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// In `search`, the search is the root → esc quits. In `resume` (reached via the
		// browser), esc backs out to the browser.
		if m.startedInSearch {
			m.result = sessionTUIResult{cancelled: true}
			return m, tea.Quit
		}
		m.searchSeq++
		m.exitGlobal()
		return m, nil
	case "tab":
		return m, m.toggleGlobalScope()
	case "enter":
		m.globalSearching = false // commit → browse the results
		m.globalInput.Blur()
		return m, nil
	case "up", "down", "pgup", "pgdown":
		// Arrows are useless in the one-line input, so commit and let the same key move into
		// the results — reach a hit without a separate enter.
		m.globalSearching = false
		m.globalInput.Blur()
		return m.updateGlobalResults(msg)
	}
	var cmd tea.Cmd
	m.globalInput, cmd = m.globalInput.Update(msg)
	m.globalQuery = m.globalInput.Value()
	m.searchSeq++
	if !queryReady(m.globalQuery) {
		// Too short to search the whole corpus: no results until the 2nd character.
		m.globalResults = nil
		m.globalSnippets = nil
		m.snippetSeq++
		return m, cmd
	}
	return m, tea.Batch(cmd, searchDebounce(m.searchSeq, modeProjects))
}

func (m sessionTUI) updateGlobalResults(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.result = sessionTUIResult{cancelled: true}
		return m, tea.Quit
	case "esc":
		// In `search`, the search is the root → esc quits. In `resume` (reached via the
		// browser), esc backs out to the browser.
		if m.startedInSearch {
			m.result = sessionTUIResult{cancelled: true}
			return m, tea.Quit
		}
		m.exitGlobal()
	case "tab":
		return m, m.toggleGlobalScope()
	case "/":
		m.globalSearching = true
		m.globalInput.SetValue(m.globalQuery)
		return m, m.globalInput.Focus()
	case "up", "k":
		m.moveGlobalCursor(-1)
		return m, m.requestVisibleSnippets(modeProjects)
	case "down", "j":
		m.moveGlobalCursor(1)
		return m, m.requestVisibleSnippets(modeProjects)
	case "pgup":
		m.moveGlobalCursor(-m.globalHeight())
		return m, m.requestVisibleSnippets(modeProjects)
	case "pgdown":
		m.moveGlobalCursor(m.globalHeight())
		return m, m.requestVisibleSnippets(modeProjects)
	case "home", "g":
		m.globalCursor, m.globalTop = 0, 0
		return m, m.requestVisibleSnippets(modeProjects)
	case "end", "G":
		m.globalCursor = len(m.globalResults) - 1
		m.clampGlobalScroll()
		return m, m.requestVisibleSnippets(modeProjects)
	case "r":
		// Resume the highlighted hit (enter is a no-op, mirroring the list).
		if sel := m.globalSelected(); sel != nil {
			return m.beginResume(sel)
		}
	case " ", "space":
		if sel := m.globalSelected(); sel != nil {
			return m.openPreview(sel)
		}
	case "a":
		return m, m.cycleAgent()
	case "v":
		m.toggleViewMode()
		m.clampGlobalScroll() // toggleViewMode clamps the list; the global list scrolls separately
		return m, m.requestVisibleSnippets(modeProjects)
	}
	return m, nil
}

// globalSelected returns the highlighted cross-project hit, or nil.
func (m sessionTUI) globalSelected() *sessionindex.Session {
	if m.globalCursor < 0 || m.globalCursor >= len(m.globalResults) {
		return nil
	}
	return &m.globalResults[m.globalCursor]
}

// toggleGlobalScope flips the cross-project search between all projects and a single project
// (tab): the highlighted hit's project when a result is selected, else the current directory's
// project (nothing to point at). A second tab widens back to all projects. It then re-runs the
// query from the top against the new scope.
func (m *sessionTUI) toggleGlobalScope() tea.Cmd {
	switch {
	case m.globalScopeID != "":
		// Already scoped → widen back to all projects.
		m.globalScopeID, m.globalScopeName = "", ""
	case m.globalSelected() != nil:
		// A result is highlighted → scope to its project.
		sel := m.globalSelected()
		m.globalScopeID, m.globalScopeName = sel.ProjectID, sessionProject(*sel)
	default:
		// No results to point at → scope to the current directory's project.
		m.globalScopeID, m.globalScopeName = m.homeProjectID, m.homeProjectName
	}
	m.globalCursor, m.globalTop = 0, 0
	m.searchSeq++
	if !queryReady(m.globalQuery) {
		m.globalResults = nil
		m.globalSnippets = nil
		m.snippetSeq++
		return nil
	}
	return searchDebounce(m.searchSeq, modeProjects)
}

func (m *sessionTUI) exitGlobal() {
	m.globalActive = false
	m.globalSearching = false
	m.globalInput.Blur()
	m.globalInput.SetValue("")
	m.globalQuery = ""
	m.globalScopeID, m.globalScopeName = "", "" // re-enter search at all-projects scope
	m.globalResults = nil
	m.globalSnippets = nil
	m.snippetSeq++
	m.globalCursor, m.globalTop = 0, 0
}

func (m *sessionTUI) moveGlobalCursor(delta int) {
	moveCursorWithin(&m.globalCursor, &m.globalTop, delta, len(m.globalResults), m.globalHeight())
}

func (m *sessionTUI) clampGlobalScroll() {
	clampScrollWithin(&m.globalCursor, &m.globalTop, m.globalHeight())
}

func (m sessionTUI) globalHeight() int {
	const chrome = 6
	avail := m.height - chrome
	if avail < 1 {
		avail = 1
	}
	n := avail / m.rowsPerSession()
	if n < 1 {
		n = 1
	}
	return n
}

func (m sessionTUI) renderGlobalResults() string {
	var b strings.Builder

	scope := "all projects"
	if m.globalScopeID != "" {
		scope = styDim.Render("project: ") + stySel.Render(m.globalScopeName)
	}
	left := m.headerLeft(scope) + styDim.Render("  ·  ") + m.agentScope()
	if q := strings.TrimSpace(m.globalQuery); q != "" && !m.globalSearching {
		left += styDim.Render(" · ") + stySel.Render(q)
	}
	right := styDim.Render(fmt.Sprintf("%d matches", len(m.globalResults)))
	b.WriteString(headerRow(left, right, m.lineWidth()) + "\n")
	b.WriteString(strings.Repeat("─", m.lineWidth()) + "\n")

	scopeWord := "all projects"
	if m.globalScopeID != "" {
		scopeWord = "this project"
	}
	switch {
	case strings.TrimSpace(m.globalQuery) == "":
		b.WriteString(styFaint.Render("  Type to search sessions across " + scopeWord + "."))
	case len(m.globalResults) == 0:
		b.WriteString(styFaint.Render("  No matches."))
	default:
		h := m.globalHeight()
		end := min(m.globalTop+h, len(m.globalResults))
		for i := m.globalTop; i < end; i++ {
			s := m.globalResults[i]
			snip := m.globalSnippets[sessionindex.FingerprintKey(s.Agent, s.SessionID)]
			b.WriteString(m.globalRow(s, i == m.globalCursor, snip))
			if i < end-1 {
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("\n" + strings.Repeat("─", m.lineWidth()) + "\n")
	// esc quits when search is the root (`specstory search`); otherwise it backs out to the
	// browser it was opened from (`specstory resume`).
	escHint := "esc back"
	if m.startedInSearch {
		escHint = "esc quit"
	}
	scopeKey := "tab this project"
	if m.globalScopeID != "" {
		scopeKey = "tab all projects"
	}
	if m.globalSearching {
		// While typing: esc quits (search root) or cancels back to the browser (resume). Only
		// offer "enter browse results" once there's actually something to browse.
		inputHint := "esc quit"
		if !m.startedInSearch {
			inputHint = "esc cancel"
		}
		inputHint += " · " + scopeKey
		if len(m.globalResults) > 0 {
			inputHint += " · enter browse results"
		}
		b.WriteString(m.globalInput.View() + "    " + styFaint.Render(inputHint))
		return b.String()
	}
	keys := []string{"↑↓ move", "r resume", "space preview", "a agent", "v " + m.viewMode, "/ edit search", scopeKey, escHint, "q quit"}
	b.WriteString(styDim.Render(strings.Join(keys, " · ")))
	return b.String()
}

// globalRow renders a cross-project search hit: agent · time · project · highlighted match
// snippet. Honors the dense/sparse view mode, matching the project session list.
func (m sessionTUI) globalRow(s sessionindex.Session, selected bool, snippet string) string {
	cursor := rowCursor(selected)
	agent := m.agentTag(s.Agent)
	proj := fmt.Sprintf("%-18s", truncate(sessionProject(s), 18))

	if m.viewMode == "sparse" {
		label := rowLabel(s, selected, snippet, m.lineWidth()-30, m.lineWidth()-32)
		head := cursor + agent + "  " + styFaint.Render(proj) + "  " + label
		sub := "    " + styFaint.Render(fmt.Sprintf("%s ago · %s", relativeTime(s.UpdatedAt), shortID(s.SessionID)))
		return head + "\n" + sub
	}

	when := fmt.Sprintf("%-4s", relativeTime(s.UpdatedAt))
	label := rowLabel(s, selected, snippet, m.lineWidth()-46, m.lineWidth()-48)
	return cursor + agent + " " + styDim.Render(when) + "  " + styFaint.Render(proj) + "  " + label
}

func sessionProject(s sessionindex.Session) string {
	if strings.TrimSpace(s.ProjectName) != "" {
		return s.ProjectName
	}
	if s.ProjectID == unknownProjectID || s.ProjectID == "" {
		return "(unknown)"
	}
	return s.ProjectID
}
