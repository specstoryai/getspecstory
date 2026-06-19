package cmd

import (
	"fmt"
	"image/color"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/config"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
)

// resumeTUIResult is what the picker hands back: the chosen session and target agent,
// or a cancel. It is read off the final model after the program exits.
type resumeTUIResult struct {
	session   *sessionindex.Session
	targetID  string
	cancelled bool
}

// tuiMode is the picker's top-level screen.
type tuiMode int

const (
	modeList     tuiMode = iota // browsing the session list (current or a drilled-in project)
	modeTarget                  // choosing which agent to resume into
	modeProjects                // the all-projects browser (Stage B)
)

// agentMeta carries an agent's display name + accent color for the list.
type agentMeta struct {
	name   string
	accent color.Color
}

// resumeTUI is the Stage A picker model: the current project's sessions across all
// agents, with agent filtering, dense/sparse views, a preview overlay, full-text search,
// and a final target-agent step. See docs/RESUME-TUI.md.
type resumeTUI struct {
	store       *sessionindex.Store
	projectID   string
	projectName string
	agents      map[string]agentMeta // provider id -> display meta
	installed   []agentChoice        // installed agents, for the target step
	presetTo    string               // pre-selected target (from `resume <agent>`), or ""
	lastAgent   string               // default target (last resumed), or ""

	// homeProjectID/Name + homeSessions are the current directory's project; the picker
	// can drill into other projects via the browser and toggle back to "home" with tab.
	homeProjectID   string
	homeProjectName string
	homeSessions    []sessionindex.Session

	all       []sessionindex.Session // sessions for the active project (projectID), newest first
	filtered  []sessionindex.Session // after agent filter + search
	cursor    int                    // index into filtered
	top       int                    // first visible row (scroll)
	inBrowser bool                   // the active session list was reached via the browser

	agentCycle  []string // "" (all) followed by each present agent id
	agentFilter string   // "" = all
	viewMode    string   // "dense" | "sparse"

	// all-projects browser (Stage B)
	projects         []sessionindex.ProjectSummary // all projects, most recent first
	projFiltered     []sessionindex.ProjectSummary // after project-name search
	projCursor       int
	projTop          int
	projectsLoaded   bool
	startedInBrowser bool // launched straight into the browser (empty home project)
	projSearching    bool
	projSearch       textinput.Model
	projSearchQuery  string

	// global session search: FTS across ALL projects, opened with / in the browser.
	globalActive    bool
	globalSearching bool
	globalInput     textinput.Model
	globalQuery     string
	globalResults   []sessionindex.Session
	globalCursor    int
	globalTop       int

	searching   bool
	search      textinput.Model
	searchQuery string

	previewing  bool
	previewBody string

	mode         tuiMode
	chosen       *sessionindex.Session
	targetCursor int

	width, height int
	result        resumeTUIResult
}

func newResumeTUI(store *sessionindex.Store, projectID, projectName string, sessions []sessionindex.Session,
	agents map[string]agentMeta, installed []agentChoice, presetTo, lastAgent, viewMode string) resumeTUI {

	ti := textinput.New()
	ti.Placeholder = "search sessions…"
	ti.Prompt = "/ "
	pi := textinput.New()
	pi.Placeholder = "filter projects…"
	pi.Prompt = "p "
	gi := textinput.New()
	gi.Placeholder = "search all sessions…"
	gi.Prompt = "/ "

	m := resumeTUI{
		store:           store,
		projectID:       projectID,
		projectName:     projectName,
		homeProjectID:   projectID,
		homeProjectName: projectName,
		homeSessions:    sessions,
		agents:          agents,
		installed:       installed,
		presetTo:        presetTo,
		lastAgent:       lastAgent,
		all:             sessions,
		viewMode:        viewMode,
		search:          ti,
		projSearch:      pi,
		globalInput:     gi,
	}
	m.rebuildAgentCycle()
	m.applyFilter()
	// Empty current project → open straight into the all-projects browser.
	if len(sessions) == 0 {
		m.startedInBrowser = true
		m.enterBrowser()
	}
	return m
}

func (m resumeTUI) Init() tea.Cmd { return nil }

func (m resumeTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyPressMsg:
		switch {
		case m.mode == modeTarget:
			return m.updateTarget(msg)
		case m.mode == modeProjects:
			switch {
			case m.globalSearching:
				return m.updateGlobalSearch(msg)
			case m.globalActive:
				return m.updateGlobalResults(msg)
			case m.projSearching:
				return m.updateProjectSearch(msg)
			default:
				return m.updateProjects(msg)
			}
		case m.searching:
			return m.updateSearch(msg)
		case m.previewing:
			return m.updatePreview(msg)
		default:
			return m.updateList(msg)
		}
	}
	return m, nil
}

// updateList handles keys while browsing the session list.
func (m resumeTUI) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.result = resumeTUIResult{cancelled: true}
		return m, tea.Quit
	case "esc":
		// In a drilled-in project, esc returns to the browser; at home it quits.
		if m.inBrowser {
			m.enterBrowser()
			return m, nil
		}
		m.result = resumeTUIResult{cancelled: true}
		return m, tea.Quit
	case "tab":
		// Toggle scope: home list ↔ all-projects browser.
		m.enterBrowser()
		return m, nil
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "pgup":
		m.moveCursor(-m.listHeight())
	case "pgdown":
		m.moveCursor(m.listHeight())
	case "home", "g":
		m.cursor, m.top = 0, 0
	case "end", "G":
		m.cursor = len(m.filtered) - 1
		m.clampScroll()
	case "enter":
		if sel := m.selected(); sel != nil {
			m.chosen = sel
			m.mode = modeTarget
			m.targetCursor = m.defaultTargetIndex()
		}
	case " ", "space":
		if sel := m.selected(); sel != nil {
			m.previewBody = m.loadPreview(sel)
			m.previewing = true
		}
	case "/":
		m.searching = true
		m.search.SetValue(m.searchQuery)
		return m, m.search.Focus()
	case "a":
		m.cycleAgent()
	case "v":
		m.toggleViewMode()
	}
	return m, nil
}

// updateSearch handles the full-text search input.
func (m resumeTUI) updateSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searching = false
		m.search.Blur()
		m.searchQuery = ""
		m.applyFilter()
		return m, nil
	case "enter":
		m.searching = false
		m.search.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	m.searchQuery = m.search.Value()
	m.applyFilter()
	return m, cmd
}

// updatePreview handles keys while the preview overlay is open.
func (m resumeTUI) updatePreview(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case " ", "space", "esc", "q":
		m.previewing = false
	}
	return m, nil
}

// updateTarget handles the target-agent selection step.
func (m resumeTUI) updateTarget(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeList
		m.chosen = nil
	case "ctrl+c":
		m.result = resumeTUIResult{cancelled: true}
		return m, tea.Quit
	case "up", "k":
		if m.targetCursor > 0 {
			m.targetCursor--
		}
	case "down", "j":
		if m.targetCursor < len(m.installed)-1 {
			m.targetCursor++
		}
	case "enter":
		m.result = resumeTUIResult{session: m.chosen, targetID: m.installed[m.targetCursor].id}
		return m, tea.Quit
	}
	return m, nil
}

// ---- list mechanics ----

func (m *resumeTUI) moveCursor(delta int) {
	if len(m.filtered) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > len(m.filtered)-1 {
		m.cursor = len(m.filtered) - 1
	}
	m.clampScroll()
}

func (m *resumeTUI) clampScroll() {
	h := m.listHeight()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+h {
		m.top = m.cursor - h + 1
	}
	if m.top < 0 {
		m.top = 0
	}
}

func (m resumeTUI) selected() *sessionindex.Session {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return nil
	}
	return &m.filtered[m.cursor]
}

// rowsPerSession is how many terminal lines one list row occupies in each view mode.
func (m resumeTUI) rowsPerSession() int {
	if m.viewMode == "sparse" {
		return 2
	}
	return 1
}

// listHeight is how many sessions fit in the list region (height minus chrome).
func (m resumeTUI) listHeight() int {
	const chrome = 6 // header(2) + glance(1) + footer(2) + margins
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

func (m *resumeTUI) rebuildAgentCycle() {
	present := map[string]bool{}
	for _, s := range m.all {
		present[s.Agent] = true
	}
	ids := make([]string, 0, len(present))
	for id := range present {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	m.agentCycle = append([]string{""}, ids...)
}

func (m *resumeTUI) cycleAgent() {
	cur := 0
	for i, id := range m.agentCycle {
		if id == m.agentFilter {
			cur = i
			break
		}
	}
	m.agentFilter = m.agentCycle[(cur+1)%len(m.agentCycle)]
	m.applyFilter()
}

func (m *resumeTUI) toggleViewMode() {
	if m.viewMode == "sparse" {
		m.viewMode = "dense"
	} else {
		m.viewMode = "sparse"
	}
	m.clampScroll()
}

// applyFilter rebuilds the visible list from the agent filter and search query.
func (m *resumeTUI) applyFilter() {
	base := m.all
	if q := ftsQuery(m.searchQuery); q != "" {
		if hits, err := m.store.Search(q); err == nil {
			base = nil
			for _, s := range hits {
				if s.ProjectID == m.projectID {
					base = append(base, s)
				}
			}
		}
	}
	out := make([]sessionindex.Session, 0, len(base))
	for _, s := range base {
		if m.agentFilter == "" || s.Agent == m.agentFilter {
			out = append(out, s)
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
	m.clampScroll()
}

func (m resumeTUI) defaultTargetIndex() int {
	// Prefer the explicit preset (`resume <agent>`), else the last-resumed agent,
	// else the chosen session's own agent (same-agent resume).
	want := m.presetTo
	if want == "" {
		want = m.lastAgent
	}
	if want == "" && m.chosen != nil {
		want = m.chosen.Agent
	}
	for i, a := range m.installed {
		if a.id == want {
			return i
		}
	}
	return 0
}

func (m resumeTUI) loadPreview(s *sessionindex.Session) string {
	body, err := m.store.SessionBody(s.Agent, s.SessionID)
	if err != nil || strings.TrimSpace(body) == "" {
		return ""
	}
	return body
}

// ftsQuery turns free-form input into a safe FTS5 prefix query (alnum tokens only).
func ftsQuery(input string) string {
	var toks []string
	for _, f := range strings.Fields(input) {
		var b strings.Builder
		for _, r := range f {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
			}
		}
		if b.Len() > 0 {
			toks = append(toks, b.String()+"*")
		}
	}
	return strings.Join(toks, " ")
}

// ---- rendering ----

var (
	styDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styFaint  = lipgloss.NewStyle().Faint(true)
	styBold   = lipgloss.NewStyle().Bold(true)
	styCursor = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	stySel    = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
)

func (m resumeTUI) View() tea.View {
	var content string
	switch {
	case m.mode == modeTarget:
		content = m.renderTarget()
	case m.mode == modeProjects && m.globalActive:
		content = m.renderGlobalResults()
	case m.mode == modeProjects:
		content = m.renderProjects()
	case m.previewing:
		content = m.renderPreview()
	default:
		content = m.renderListScreen()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m resumeTUI) renderListScreen() string {
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.lineWidth()))
	b.WriteString("\n")
	b.WriteString(m.renderRows())
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.lineWidth()))
	b.WriteString("\n")
	b.WriteString(m.renderGlance())
	b.WriteString("\n")
	b.WriteString(m.renderFooter())
	return b.String()
}

func (m resumeTUI) lineWidth() int {
	if m.width < 20 {
		return 80
	}
	return m.width
}

func (m resumeTUI) renderHeader() string {
	left := styBold.Render("resume") + styDim.Render(" · ") + m.projectName
	agent := "all"
	if m.agentFilter != "" {
		agent = m.agentName(m.agentFilter)
	}
	scope := "[This project]"
	if m.inBrowser {
		scope = "[All projects ▸ " + m.projectName + "]"
	}
	right := styDim.Render("scope: ") + stySel.Render(scope) +
		styDim.Render("   agent: ") + stySel.Render(agent)
	gap := m.lineWidth() - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m resumeTUI) renderRows() string {
	if len(m.filtered) == 0 {
		return styFaint.Render("  No sessions match.")
	}
	h := m.listHeight()
	end := min(m.top+h, len(m.filtered))
	var b strings.Builder
	for i := m.top; i < end; i++ {
		b.WriteString(m.sessionRow(m.filtered[i], i == m.cursor))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m resumeTUI) sessionRow(s sessionindex.Session, selected bool) string {
	cursor := "  "
	if selected {
		cursor = styCursor.Render("▸ ")
	}
	agent := m.agentTag(s.Agent)
	when := fmt.Sprintf("%-4s", relativeTime(s.UpdatedAt))
	name := sessionTitle(s)

	if m.viewMode == "sparse" {
		turns := styDim.Render(fmt.Sprintf("%d prompts", s.UserTurns))
		head := cursor + agent + "  " + renderName(name, selected, m.lineWidth()-24) + "   " + turns
		sub := "    " + styFaint.Render(fmt.Sprintf("%s ago · %s", relativeTime(s.UpdatedAt), shortID(s.SessionID)))
		return head + "\n" + sub
	}
	turns := styDim.Render(fmt.Sprintf("%4d", s.UserTurns))
	return cursor + agent + " " + styDim.Render(when) + "  " +
		renderName(name, selected, m.lineWidth()-22) + "  " + turns
}

func renderName(name string, selected bool, width int) string {
	if width < 8 {
		width = 8
	}
	t := truncate(name, width)
	if selected {
		return stySel.Render(t)
	}
	return t
}

func (m resumeTUI) renderGlance() string {
	sel := m.selected()
	if sel == nil {
		return ""
	}
	return styDim.Render("⟶  ") + styFaint.Render(truncate(sessionTitle(*sel), m.lineWidth()-4))
}

func (m resumeTUI) renderFooter() string {
	if m.searching {
		return m.search.View() + "    " + styFaint.Render("esc clear · enter apply")
	}
	scopeKey := "tab all-projects"
	if m.inBrowser {
		scopeKey = "tab/esc back"
	}
	keys := []string{"↑↓ move", "↵ resume", "space preview", "/ search", "a agent", scopeKey, "v " + m.viewMode, "q quit"}
	return styDim.Render(strings.Join(keys, "   "))
}

func (m resumeTUI) renderPreview() string {
	sel := m.chosenOrSelected()
	var b strings.Builder
	b.WriteString(styBold.Render("Preview") + styDim.Render(" · "+sessionTitle(*sel)))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.lineWidth()))
	b.WriteString("\n")
	b.WriteString(previewText(m.previewBody, m.lineWidth(), m.height-6))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.lineWidth()))
	b.WriteString("\n")
	b.WriteString(styDim.Render("space/esc close   ↵ back to list"))
	return b.String()
}

func (m resumeTUI) chosenOrSelected() *sessionindex.Session {
	if sel := m.selected(); sel != nil {
		return sel
	}
	return m.chosen
}

func (m resumeTUI) renderTarget() string {
	var b strings.Builder
	b.WriteString(styBold.Render("Resume into which agent?"))
	if m.chosen != nil {
		b.WriteString(styDim.Render("   " + sessionTitle(*m.chosen)))
	}
	b.WriteString("\n\n")
	for i, a := range m.installed {
		cursor := "   "
		label := a.provider.Name()
		if m.chosen != nil && a.id == m.chosen.Agent {
			label += styFaint.Render(" (same agent — native resume)")
		}
		if i == m.targetCursor {
			cursor = styCursor.Render(" ▸ ")
			label = stySel.Render(a.provider.Name()) + label[len(a.provider.Name()):]
		}
		b.WriteString(cursor + label + "\n")
	}
	b.WriteString("\n")
	b.WriteString(styDim.Render("↑↓ move   ↵ resume   esc back"))
	return b.String()
}

// ---- helpers ----

func (m resumeTUI) agentName(id string) string {
	if a, ok := m.agents[id]; ok {
		return a.name
	}
	return id
}

func (m resumeTUI) agentTag(id string) string {
	label := fmt.Sprintf("%-8s", id)
	if a, ok := m.agents[id]; ok {
		return lipgloss.NewStyle().Foreground(a.accent).Render(label)
	}
	return label
}

// sessionTitle is the human label for a session: name, then slug, then short id.
func sessionTitle(s sessionindex.Session) string {
	switch {
	case strings.TrimSpace(s.Name) != "":
		return s.Name
	case strings.TrimSpace(s.Slug) != "":
		return s.Slug
	default:
		return shortID(s.SessionID)
	}
}

// truncate shortens s to n runes with an ellipsis.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return string(r[:n-1]) + "…"
}

// relativeTime renders an ISO 8601 timestamp as a compact "2m"/"3h"/"5d"/"Jun 2".
func relativeTime(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return t.Local().Format("Jan 2")
	}
}

// previewText renders first user message · middle elision · final message to fit height.
func previewText(body string, width, height int) string {
	if strings.TrimSpace(body) == "" {
		return styFaint.Render("  (no preview available)")
	}
	if height < 4 {
		height = 4
	}
	turns := strings.Split(body, "\n\n")
	first := strings.TrimSpace(turns[0])
	last := ""
	if len(turns) > 1 {
		last = strings.TrimSpace(turns[len(turns)-1])
	}

	headBudget := height / 2
	tailBudget := height - headBudget - 1

	var b strings.Builder
	b.WriteString(styDim.Render("first ⟶ "))
	b.WriteString("\n")
	b.WriteString(clip(first, width, headBudget))
	if last != "" {
		b.WriteString("\n")
		b.WriteString(styFaint.Render(fmt.Sprintf("  ⋯ %d turns ⋯", len(turns))))
		b.WriteString("\n")
		b.WriteString(styDim.Render("final ⟶ "))
		b.WriteString("\n")
		b.WriteString(clip(last, width, tailBudget))
	}
	return b.String()
}

// clip wraps/limits text to width columns and maxLines lines.
func clip(s string, width, maxLines int) string {
	if maxLines < 1 {
		maxLines = 1
	}
	var lines []string
	for _, raw := range strings.Split(s, "\n") {
		for len([]rune(raw)) > width {
			r := []rune(raw)
			lines = append(lines, string(r[:width]))
			raw = string(r[width:])
		}
		lines = append(lines, raw)
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = truncate(lines[maxLines-1], width)
	}
	return strings.Join(lines, "\n")
}

// ---- integration with the resume command ----

// openOrBuildResumeIndex opens sessions.db, building it first (with the normal reindex
// UI) when it is missing or empty, then proceeding straight into the picker.
func openOrBuildResumeIndex() (*sessionindex.Store, error) {
	dbPath, err := sessionindex.DefaultPath()
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(dbPath); statErr == nil {
		s, err := sessionindex.Open(dbPath)
		if err != nil {
			return nil, err
		}
		if n, _ := s.Count(); n > 0 {
			return s, nil
		}
		_ = s.Close() // empty → rebuild below
	}
	if err := runReindex(false); err != nil {
		return nil, err
	}
	return sessionindex.Open(dbPath)
}

// selectResumeViaTUI runs the picker for the current project and returns the chosen
// resume plan (or nil if the user cancelled). On a successful selection it persists the
// view-mode and target-agent preferences to the user config.
func selectResumeViaTUI(registry *factory.Registry, store *sessionindex.Store, projectID, projectName, presetTo string) (*resumePlan, error) {
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

	viewMode, lastAgent := "dense", ""
	if cfg, _ := config.Load(nil); cfg != nil {
		viewMode = cfg.GetResumeViewMode()
		lastAgent = cfg.GetResumeLastAgent()
	}

	model := newResumeTUI(store, projectID, projectName, sessions, agents, installed, presetTo, lastAgent, viewMode)
	final, err := tea.NewProgram(model).Run()
	if err != nil {
		return nil, fmt.Errorf("resume picker failed: %w", err)
	}
	rm := final.(resumeTUI)
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
func (m *resumeTUI) enterBrowser() {
	if !m.projectsLoaded {
		if ps, err := m.store.ListProjects(); err == nil {
			m.projects = ps
		}
		m.projectsLoaded = true
	}
	m.applyProjectFilter()
	m.mode = modeProjects
}

// gotoHome returns the session list to the current directory's project.
func (m *resumeTUI) gotoHome() {
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
func (m *resumeTUI) drillInto(p sessionindex.ProjectSummary) {
	sessions, err := m.store.ListByProject(p.ProjectID)
	if err != nil {
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

func (m resumeTUI) updateProjects(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.result = resumeTUIResult{cancelled: true}
		return m, tea.Quit
	case "esc":
		if m.startedInBrowser {
			m.result = resumeTUIResult{cancelled: true}
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
		m.moveProjCursor(-m.projectsHeight())
	case "pgdown":
		m.moveProjCursor(m.projectsHeight())
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

func (m resumeTUI) updateProjectSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

func (m *resumeTUI) applyProjectFilter() {
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

func (m *resumeTUI) moveProjCursor(delta int) {
	if len(m.projFiltered) == 0 {
		return
	}
	m.projCursor += delta
	if m.projCursor < 0 {
		m.projCursor = 0
	}
	if m.projCursor > len(m.projFiltered)-1 {
		m.projCursor = len(m.projFiltered) - 1
	}
	m.clampProjScroll()
}

func (m *resumeTUI) clampProjScroll() {
	h := m.projectsHeight()
	if m.projCursor < m.projTop {
		m.projTop = m.projCursor
	}
	if m.projCursor >= m.projTop+h {
		m.projTop = m.projCursor - h + 1
	}
	if m.projTop < 0 {
		m.projTop = 0
	}
}

// projectsHeight reserves room for chrome plus a few date-bucket header lines.
func (m resumeTUI) projectsHeight() int {
	const chrome = 9
	avail := m.height - chrome
	if avail < 1 {
		avail = 1
	}
	return avail
}

func (m resumeTUI) renderProjects() string {
	var b strings.Builder

	left := styBold.Render("resume") + styDim.Render(" · ") + "all projects"
	right := styDim.Render(fmt.Sprintf("%d projects", len(m.projFiltered)))
	gap := m.lineWidth() - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	b.WriteString(left + strings.Repeat(" ", gap) + right + "\n")
	b.WriteString(strings.Repeat("─", m.lineWidth()) + "\n")

	if len(m.projFiltered) == 0 {
		b.WriteString(styFaint.Render("  No projects match."))
	} else {
		h := m.projectsHeight()
		end := min(m.projTop+h, len(m.projFiltered))
		lastBucket := ""
		for i := m.projTop; i < end; i++ {
			p := m.projFiltered[i]
			if bucket := dateBucket(p.LastActivity); bucket != lastBucket {
				b.WriteString(styFaint.Render("  ── "+bucket) + "\n")
				lastBucket = bucket
			}
			b.WriteString(m.projectRow(p, i == m.projCursor) + "\n")
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
	b.WriteString(styDim.Render(strings.Join(keys, "   ")))
	return b.String()
}

func (m resumeTUI) projectRow(p sessionindex.ProjectSummary, selected bool) string {
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
func (m resumeTUI) agentCountChips(counts map[string]int) string {
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

func (m resumeTUI) updateGlobalSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.exitGlobal()
		return m, nil
	case "enter":
		m.globalSearching = false // commit → browse the results
		m.globalInput.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.globalInput, cmd = m.globalInput.Update(msg)
	m.globalQuery = m.globalInput.Value()
	m.recomputeGlobal()
	return m, cmd
}

func (m resumeTUI) updateGlobalResults(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.result = resumeTUIResult{cancelled: true}
		return m, tea.Quit
	case "esc":
		m.exitGlobal()
	case "/":
		m.globalSearching = true
		m.globalInput.SetValue(m.globalQuery)
		return m, m.globalInput.Focus()
	case "up", "k":
		m.moveGlobalCursor(-1)
	case "down", "j":
		m.moveGlobalCursor(1)
	case "pgup":
		m.moveGlobalCursor(-m.globalHeight())
	case "pgdown":
		m.moveGlobalCursor(m.globalHeight())
	case "home", "g":
		m.globalCursor, m.globalTop = 0, 0
	case "end", "G":
		m.globalCursor = len(m.globalResults) - 1
		m.clampGlobalScroll()
	case "enter":
		// A global hit jumps straight to the target-agent step (fast path).
		if m.globalCursor >= 0 && m.globalCursor < len(m.globalResults) {
			m.chosen = &m.globalResults[m.globalCursor]
			m.mode = modeTarget
			m.targetCursor = m.defaultTargetIndex()
		}
	}
	return m, nil
}

func (m *resumeTUI) exitGlobal() {
	m.globalActive = false
	m.globalSearching = false
	m.globalInput.Blur()
	m.globalInput.SetValue("")
	m.globalQuery = ""
	m.globalResults = nil
	m.globalCursor, m.globalTop = 0, 0
}

func (m *resumeTUI) recomputeGlobal() {
	if q := ftsQuery(m.globalQuery); q == "" {
		m.globalResults = nil
	} else if hits, err := m.store.Search(q); err == nil {
		m.globalResults = hits
	} else {
		m.globalResults = nil
	}
	if m.globalCursor > len(m.globalResults)-1 {
		m.globalCursor = len(m.globalResults) - 1
	}
	if m.globalCursor < 0 {
		m.globalCursor = 0
	}
	m.globalTop = 0
}

func (m *resumeTUI) moveGlobalCursor(delta int) {
	if len(m.globalResults) == 0 {
		return
	}
	m.globalCursor += delta
	if m.globalCursor < 0 {
		m.globalCursor = 0
	}
	if m.globalCursor > len(m.globalResults)-1 {
		m.globalCursor = len(m.globalResults) - 1
	}
	m.clampGlobalScroll()
}

func (m *resumeTUI) clampGlobalScroll() {
	h := m.globalHeight()
	if m.globalCursor < m.globalTop {
		m.globalTop = m.globalCursor
	}
	if m.globalCursor >= m.globalTop+h {
		m.globalTop = m.globalCursor - h + 1
	}
	if m.globalTop < 0 {
		m.globalTop = 0
	}
}

func (m resumeTUI) globalHeight() int {
	const chrome = 6
	avail := m.height - chrome
	if avail < 1 {
		avail = 1
	}
	return avail
}

func (m resumeTUI) renderGlobalResults() string {
	var b strings.Builder

	left := styBold.Render("resume") + styDim.Render(" · search all projects")
	if q := strings.TrimSpace(m.globalQuery); q != "" && !m.globalSearching {
		left += styDim.Render(" · ") + stySel.Render(q)
	}
	right := styDim.Render(fmt.Sprintf("%d matches", len(m.globalResults)))
	gap := m.lineWidth() - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	b.WriteString(left + strings.Repeat(" ", gap) + right + "\n")
	b.WriteString(strings.Repeat("─", m.lineWidth()) + "\n")

	switch {
	case strings.TrimSpace(m.globalQuery) == "":
		b.WriteString(styFaint.Render("  Type to search sessions across all projects."))
	case len(m.globalResults) == 0:
		b.WriteString(styFaint.Render("  No matches."))
	default:
		h := m.globalHeight()
		end := min(m.globalTop+h, len(m.globalResults))
		for i := m.globalTop; i < end; i++ {
			b.WriteString(m.globalRow(m.globalResults[i], i == m.globalCursor))
			if i < end-1 {
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("\n" + strings.Repeat("─", m.lineWidth()) + "\n")
	if m.globalSearching {
		b.WriteString(m.globalInput.View() + "    " + styFaint.Render("esc cancel · enter browse results"))
		return b.String()
	}
	keys := []string{"↑↓ move", "↵ resume", "/ edit search", "esc back", "q quit"}
	b.WriteString(styDim.Render(strings.Join(keys, "   ")))
	return b.String()
}

// globalRow renders a cross-project search hit: agent · time · project · title.
func (m resumeTUI) globalRow(s sessionindex.Session, selected bool) string {
	cursor := "  "
	if selected {
		cursor = styCursor.Render("▸ ")
	}
	agent := m.agentTag(s.Agent)
	when := fmt.Sprintf("%-4s", relativeTime(s.UpdatedAt))
	proj := fmt.Sprintf("%-18s", truncate(sessionProject(s), 18))
	name := renderName(sessionTitle(s), selected, m.lineWidth()-46)
	return cursor + agent + " " + styDim.Render(when) + "  " + styFaint.Render(proj) + "  " + name
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
