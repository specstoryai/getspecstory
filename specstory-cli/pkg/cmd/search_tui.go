package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/session"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
)

// searchScreen is the search TUI's top-level view.
type searchScreen int

const (
	screenResults searchScreen = iota // typing + browsing results
	screenReader                      // reading a session (glamour)
	screenTarget                      // picking an agent to resume into
)

// searchFTSDebounceMsg / searchFTSResultMsg drive the async, debounced query — the same
// pattern as the resume picker, kept independent to avoid coupling.
type searchFTSDebounceMsg struct{ seq int }
type searchFTSResultMsg struct {
	seq  int
	hits []sessionindex.SearchHit
}

// searchTUI is the `specstory search` model: an always-on query input over the session
// index, a cross-project results list, a glamour-rendered reader, and `r` to resume a
// found session (through the shared launchResume path). See docs/SESSION-SEARCH.md.
type searchTUI struct {
	store     *sessionindex.Store
	registry  *factory.Registry
	agents    map[string]agentMeta
	installed []agentChoice

	// scope mirrors resume: current project when it has sessions, else all projects.
	homeProjectID   string
	homeProjectName string
	homeHasSessions bool
	scopeProjectID  string // "" = all projects, else the home project id

	input   textinput.Model
	query   string
	seq     int  // bumped per keystroke; debounced results must match
	loading bool // a query is scheduled or in flight (debounce + the query itself)
	// searchCancel aborts the in-flight FTS query when a newer keystroke supersedes it,
	// freeing the database connection (a broad prefix query can rank the whole corpus).
	searchCancel context.CancelFunc

	results  []sessionindex.Session
	snippets []string
	cursor   int
	top      int

	agentFilter string
	agentCycle  []string

	reader        viewport.Model
	readerSession *sessionindex.Session

	chosen       *sessionindex.Session
	targetCursor int
	presetTo     string
	lastAgent    string

	screen        searchScreen
	width, height int
	result        resumeTUIResult // set when the user asks to resume a found session
}

func newSearchTUI(store *sessionindex.Store, registry *factory.Registry, agents map[string]agentMeta,
	installed []agentChoice, homeID, homeName string, homeHasSessions bool,
	presetTo, lastAgent, initialQuery string) searchTUI {

	ti := textinput.New()
	ti.Prompt = "/ "
	ti.SetValue(initialQuery)
	// Focus here (not in Init): Init returns only a Cmd, so a focus set there is discarded.
	ti.Focus()

	// Search is a find-across-history tool, so it opens across ALL projects by default;
	// `tab` narrows to the current project (when it has indexed sessions). This is the one
	// deliberate scope divergence from resume, which defaults to the current project.
	scope := ""

	cycle := append([]string{""}, registry.ListIDs()...)

	m := searchTUI{
		store:           store,
		registry:        registry,
		agents:          agents,
		installed:       installed,
		homeProjectID:   homeID,
		homeProjectName: homeName,
		homeHasSessions: homeHasSessions,
		scopeProjectID:  scope,
		input:           ti,
		query:           initialQuery,
		agentCycle:      cycle,
		presetTo:        presetTo,
		lastAgent:       lastAgent,
		reader:          viewport.New(),
	}
	if strings.TrimSpace(initialQuery) != "" {
		m.seq = 1
		m.loading = true // pre-seeded query runs from Init; show "Searching…" until it lands
	}
	return m
}

func (m searchTUI) Init() tea.Cmd {
	if m.seq > 0 {
		// Pre-seeded query: the initial run isn't superseded yet, so a plain context is fine.
		return tea.Batch(m.input.Focus(), m.runSearch(m.seq, context.Background()))
	}
	return m.input.Focus()
}

func (m searchTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.reader.SetWidth(m.width)
		m.reader.SetHeight(m.readerHeight())
		return m, nil
	case searchFTSDebounceMsg:
		if msg.seq == m.seq {
			if m.searchCancel != nil {
				m.searchCancel() // abort any prior in-flight query, freeing its connection
			}
			ctx, cancel := context.WithCancel(context.Background())
			m.searchCancel = cancel
			return m, m.runSearch(msg.seq, ctx)
		}
		return m, nil
	case searchFTSResultMsg:
		if msg.seq == m.seq {
			m.applyResults(msg.hits)
			m.loading = false // newest query answered; stale results leave loading set
		}
		return m, nil
	case tea.KeyPressMsg:
		switch m.screen {
		case screenReader:
			return m.updateReader(msg)
		case screenTarget:
			return m.updateTarget(msg)
		default:
			return m.updateResults(msg)
		}
	}
	return m, nil
}

// updateResults handles the always-on query input + result navigation.
func (m searchTUI) updateResults(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.result = resumeTUIResult{cancelled: true}
		return m, tea.Quit
	case "esc":
		// Esc only clears the query; it never quits (the input is always focused, so the
		// quit key has to be a non-printable one — ctrl+c). No-op when already empty.
		if strings.TrimSpace(m.query) != "" {
			m.query = ""
			m.input.SetValue("")
			m.seq++
			m.results, m.snippets, m.cursor, m.top = nil, nil, 0, 0
			m.loading = false
		}
		return m, nil
	case "up", "ctrl+p":
		m.moveCursor(-1)
		return m, nil
	case "down", "ctrl+n":
		m.moveCursor(1)
		return m, nil
	case "pgup":
		m.moveCursor(-m.resultsHeight())
		return m, nil
	case "pgdown":
		m.moveCursor(m.resultsHeight())
		return m, nil
	case "enter", "right":
		if sel := m.selected(); sel != nil {
			return m.openReader(sel)
		}
		return m, nil
	case "ctrl+r":
		// Resume the highlighted result without reading it first.
		if sel := m.selected(); sel != nil && len(m.installed) > 0 {
			m.chosen = sel
			m.screen = screenTarget
			m.targetCursor = m.defaultTargetIndex()
		}
		return m, nil
	case "tab":
		m.toggleScope()
		return m, nil
	case "ctrl+a":
		m.cycleAgent()
		return m, nil
	}

	// Anything else edits the query → instant input, debounced async search.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.query = m.input.Value()
	m.seq++
	if !queryReady(m.query) {
		// Empty or too short to search: clear results, don't fire a query.
		m.results, m.snippets, m.cursor, m.top = nil, nil, 0, 0
		m.loading = false
		return m, cmd
	}
	m.loading = true // show "Searching…" through the debounce + query
	return m, tea.Batch(cmd, searchFTSDebounce(m.seq))
}

func (m searchTUI) updateReader(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.result = resumeTUIResult{cancelled: true}
		return m, tea.Quit
	case "esc", "left":
		m.screen = screenResults
		return m, nil
	case "r":
		if m.readerSession != nil && len(m.installed) > 0 {
			m.chosen = m.readerSession
			m.screen = screenTarget
			m.targetCursor = m.defaultTargetIndex()
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.reader, cmd = m.reader.Update(msg)
	return m, cmd
}

func (m searchTUI) updateTarget(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Back to wherever we came from (reader if open, else results).
		if m.readerSession != nil {
			m.screen = screenReader
		} else {
			m.screen = screenResults
		}
		return m, nil
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

// ---- mechanics ----

func (m *searchTUI) moveCursor(delta int) {
	if len(m.results) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > len(m.results)-1 {
		m.cursor = len(m.results) - 1
	}
	h := m.resultsHeight()
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

func (m searchTUI) selected() *sessionindex.Session {
	if m.cursor < 0 || m.cursor >= len(m.results) {
		return nil
	}
	return &m.results[m.cursor]
}

func (m *searchTUI) toggleScope() {
	if !m.homeHasSessions {
		return // nothing to scope to
	}
	if m.scopeProjectID == "" {
		m.scopeProjectID = m.homeProjectID
	} else {
		m.scopeProjectID = ""
	}
	m.seq++
	m.rerunSearch()
}

func (m *searchTUI) cycleAgent() {
	cur := 0
	for i, id := range m.agentCycle {
		if id == m.agentFilter {
			cur = i
			break
		}
	}
	m.agentFilter = m.agentCycle[(cur+1)%len(m.agentCycle)]
	m.rerunSearch()
}

// rerunSearch re-queries synchronously for the current scope (used by scope/agent toggles,
// which are one-off actions where a brief query is fine). applyResults applies the agent
// filter, so changing it re-filters via a fresh query.
func (m *searchTUI) rerunSearch() {
	m.cursor, m.top = 0, 0
	m.loading = false // synchronous re-query: results are ready by the next render
	if !queryReady(m.query) {
		m.results, m.snippets = nil, nil
		return
	}
	if hits, err := m.store.SearchWithSnippets(ftsQuery(m.query), m.scopeProjectID); err == nil {
		m.applyResults(hits)
	}
}

func (m searchTUI) defaultTargetIndex() int {
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

func (m searchTUI) openReader(s *sessionindex.Session) (tea.Model, tea.Cmd) {
	m.readerSession = s
	m.reader.SetWidth(m.width)
	m.reader.SetHeight(m.readerHeight())
	m.reader.SetContent(renderGlamour(m.sessionMarkdown(s), m.width))
	m.reader.GotoTop()
	m.screen = screenReader
	return m, nil
}

// sessionMarkdown returns the session as markdown for the reader — the real specstory
// render when the session can be re-parsed (needs a cwd), else the plain FTS body.
func (m searchTUI) sessionMarkdown(s *sessionindex.Session) string {
	if s.OriginCwd != "" {
		if prov, err := m.registry.Get(s.Agent); err == nil {
			if full, err := prov.GetAgentChatSession(s.OriginCwd, s.SessionID, false); err == nil &&
				full != nil && full.SessionData != nil {
				if md, err := session.GenerateMarkdownFromAgentSession(full.SessionData, false, true); err == nil {
					return md
				}
			}
		}
	}
	if body, _ := m.store.SessionBody(s.Agent, s.SessionID); strings.TrimSpace(body) != "" {
		return "```\n" + body + "\n```"
	}
	return "_(no readable content for this session)_"
}

// runSearch performs the FTS query off the UI thread for the current scope.
func (m searchTUI) runSearch(seq int, ctx context.Context) tea.Cmd {
	store := m.store
	query := m.query
	fq := ftsQuery(query)
	scope := m.scopeProjectID
	return func() tea.Msg {
		if !queryReady(query) {
			return searchFTSResultMsg{seq: seq}
		}
		hits, _ := store.SearchWithSnippetsContext(ctx, fq, scope)
		return searchFTSResultMsg{seq: seq, hits: hits}
	}
}

func (m *searchTUI) applyResults(hits []sessionindex.SearchHit) {
	m.results = nil
	m.snippets = nil
	for _, h := range hits {
		if m.agentFilter == "" || h.Agent == m.agentFilter {
			m.results = append(m.results, h.Session)
			m.snippets = append(m.snippets, h.Snippet)
		}
	}
	if m.cursor > len(m.results)-1 {
		m.cursor = len(m.results) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.top = 0
}

func searchFTSDebounce(seq int) tea.Cmd {
	return tea.Tick(searchDebounceDelay, func(time.Time) tea.Msg {
		return searchFTSDebounceMsg{seq: seq}
	})
}

// renderGlamour renders markdown to styled terminal output for the reader.
func renderGlamour(md string, width int) string {
	if width < 20 {
		width = 80
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(width-2))
	if err != nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return out
}

func (m searchTUI) resultsHeight() int {
	const chrome = 6
	h := m.height - chrome
	if h < 1 {
		h = 1
	}
	return h
}

func (m searchTUI) readerHeight() int {
	const chrome = 4
	h := m.height - chrome
	if h < 1 {
		h = 1
	}
	return h
}

func (m searchTUI) lineW() int {
	if m.width < 20 {
		return 80
	}
	return m.width
}

// ---- rendering ----

func (m searchTUI) View() tea.View {
	var content string
	switch m.screen {
	case screenReader:
		content = m.renderReader()
	case screenTarget:
		content = m.renderTarget()
	default:
		content = m.renderResults()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m searchTUI) renderResults() string {
	var b strings.Builder

	scope := "all projects"
	if m.scopeProjectID != "" {
		scope = m.homeProjectName
	}
	agent := "all"
	if m.agentFilter != "" {
		agent = m.agentFilter
		if a, ok := m.agents[m.agentFilter]; ok {
			agent = a.name
		}
	}
	count := fmt.Sprintf("%d results", len(m.results))
	if m.loading {
		count = "searching…"
	}
	left := styBold.Render("SpecStory Search") + styDim.Render(" · ") + scope
	right := styDim.Render(count) +
		styDim.Render("   agent: ") + stySel.Render(agent)
	b.WriteString(headerRow(left, right, m.lineW()) + "\n")
	b.WriteString(m.input.View() + "\n")
	b.WriteString(strings.Repeat("─", m.lineW()) + "\n")

	switch {
	case strings.TrimSpace(m.query) == "":
		b.WriteString(styFaint.Render("  Type to search your coding agent sessions" + map[bool]string{true: " (this project — tab for all)", false: " across all projects"}[m.scopeProjectID != ""]))
	case m.loading && len(m.results) == 0:
		// Don't claim "no matches" before the query has answered.
		b.WriteString(styFaint.Render("  Searching…"))
	case !queryReady(m.query):
		b.WriteString(styFaint.Render(fmt.Sprintf("  Keep typing… (%d+ characters)", minQueryLen)))
	case len(m.results) == 0:
		b.WriteString(styFaint.Render("  No matches."))
	default:
		h := m.resultsHeight()
		end := min(m.top+h, len(m.results))
		for i := m.top; i < end; i++ {
			b.WriteString(m.resultRow(m.results[i], i, i == m.cursor))
			if i < end-1 {
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("\n" + strings.Repeat("─", m.lineW()) + "\n")
	scopeKey := "tab to search all projects"
	if m.scopeProjectID == "" && m.homeHasSessions {
		scopeKey = "tab to search just this project"
	}
	keys := []string{"↑↓ move", "↵ read", "^r resume", scopeKey, "^a filter by agent", "ESC clear", "^c quit"}
	b.WriteString(styDim.Render(strings.Join(keys, " · ")))
	return b.String()
}

func (m searchTUI) resultRow(s sessionindex.Session, i int, selected bool) string {
	cursor := "  "
	if selected {
		cursor = styCursor.Render("▸ ")
	}
	agent := renderAgentTag(m.agents, s.Agent)
	// Fixed width so the project/snippet columns stay aligned even when some rows carry a
	// year stamp ("Dec 31 '25", up to 10 cols) and others are relative ("2d").
	when := fmt.Sprintf("%-10s", relativeTime(s.UpdatedAt))

	label := renderName(sessionTitle(s), selected, m.lineW()-48)
	if i < len(m.snippets) && m.snippets[i] != "" {
		label = renderSnippet(m.snippets[i], m.lineW()-48)
	}

	// Show the project column only when searching across all projects.
	if m.scopeProjectID == "" {
		proj := fmt.Sprintf("%-16s", truncate(sessionProject(s), 16))
		return cursor + agent + " " + styDim.Render(when) + "  " + styFaint.Render(proj) + "  " + label
	}
	return cursor + agent + " " + styDim.Render(when) + "  " + label
}

func (m searchTUI) renderReader() string {
	var b strings.Builder
	left := styBold.Render("read")
	if s := m.readerSession; s != nil {
		left += styDim.Render(" · ") + renderAgentTag(m.agents, s.Agent) +
			styDim.Render(" · ") + sessionProject(*s) + styDim.Render(" · ") + sessionTitle(*s)
	}
	b.WriteString(headerRow(left, "", m.lineW()) + "\n")
	b.WriteString(strings.Repeat("─", m.lineW()) + "\n")
	b.WriteString(m.reader.View())
	b.WriteString("\n" + strings.Repeat("─", m.lineW()) + "\n")
	keys := []string{"↑↓ scroll", "pgup/pgdn page", "r resume", "ESC back", "q quit"}
	b.WriteString(styDim.Render(strings.Join(keys, " · ")))
	return b.String()
}

func (m searchTUI) renderTarget() string {
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
	b.WriteString("\n" + styDim.Render(strings.Join([]string{"↑↓ move", "↵ resume", "ESC back"}, " · ")))
	return b.String()
}

// renderAgentTag renders a colored, fixed-width provider tag (shared with the picker).
func renderAgentTag(agents map[string]agentMeta, id string) string {
	label := fmt.Sprintf("%-8s", id)
	if a, ok := agents[id]; ok {
		return lipgloss.NewStyle().Foreground(a.accent).Render(label)
	}
	return label
}

// headerRow lays out a left and right segment across width with a gap between.
func headerRow(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}
