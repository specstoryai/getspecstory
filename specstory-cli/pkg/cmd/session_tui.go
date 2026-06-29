package cmd

import (
	"context"
	"image/color"
	"log/slog"
	"sort"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
)

// sessionTUIResult is what the picker hands back: the chosen session and target agent,
// or a cancel. It is read off the final model after the program exits.
type sessionTUIResult struct {
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

// sessionTUI is the shared model behind BOTH `specstory resume` and `specstory search`.
// The two commands are the same UI with different entry points: resume opens on the current
// project's session list; search opens straight into the all-projects FTS with the input
// focused. Everything else — keys, preview, agent filter, dense/sparse, the target-agent
// step — is identical by construction. See docs/RESUME-TUI.md and docs/SESSION-SEARCH.md.
type sessionTUI struct {
	store       *sessionindex.Store
	registry    *factory.Registry // re-parses a session for the glamour preview
	title       string            // header title, e.g. "SpecStory Resume" / "SpecStory Search"
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

	all              []sessionindex.Session // sessions for the active project (projectID), newest first
	searchRaw        []sessionindex.Session // last FTS results for the active query, BEFORE the agent filter
	filtered         []sessionindex.Session // after agent filter + search
	filteredSnippets map[string]string      // match snippets for visible filtered rows, keyed by agent/session
	cursor           int                    // index into filtered
	top              int                    // first visible row (scroll)
	inBrowser        bool                   // the active session list was reached via the browser

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
	startedInSearch  bool // launched straight into the all-projects search (`specstory search`)
	projSearching    bool
	projSearch       textinput.Model
	projSearchQuery  string

	// global session search: FTS across all projects, or scoped to a single project with tab
	// (the highlighted hit's project, else the current directory's). Opened with / in the
	// browser or as `search`.
	globalActive    bool
	globalSearching bool
	globalScopeID   string // "" = all projects; else the project id the search is scoped to
	globalScopeName string // display name for the scoped project
	globalInput     textinput.Model
	globalQuery     string
	globalResults   []sessionindex.Session
	globalSnippets  map[string]string
	globalCursor    int
	globalTop       int

	searching   bool
	search      textinput.Model
	searchQuery string
	searchSeq   int // bumped per search keystroke; debounced FTS results must match it
	snippetSeq  int // bumped per lazy snippet request; stale snippet results are discarded
	// searchCancel aborts the in-flight FTS query when a newer keystroke supersedes it,
	// freeing the database connection (a broad prefix query can rank the whole corpus).
	searchCancel context.CancelFunc

	// previewing shows a glamour-rendered, scrollable reader for the highlighted session,
	// identical to search's reader (see openPreview / renderPreview).
	previewing    bool
	reader        viewport.Model
	readerSession *sessionindex.Session

	mode         tuiMode
	chosen       *sessionindex.Session
	targetCursor int

	width, height int
	result        sessionTUIResult
}

// sessionTUIOpts carries the per-command entry configuration. The only real differences
// between `resume` and `search` live here: the header title, what the positional arg means
// (presetTo vs. initialQuery), and whether to open in the all-projects search.
type sessionTUIOpts struct {
	title         string // header title ("SpecStory Resume" / "SpecStory Search")
	presetTo      string // resume: pre-selected target agent (from `resume <agent>`)
	lastAgent     string // default target (last resumed), or ""
	viewMode      string // "dense" | "sparse"
	initialQuery  string // search: pre-seed the all-projects query
	startInSearch bool   // search: open in the all-projects FTS with the input focused
}

func newSessionTUI(store *sessionindex.Store, registry *factory.Registry, projectID, projectName string,
	sessions []sessionindex.Session, agents map[string]agentMeta, installed []agentChoice, opts sessionTUIOpts) sessionTUI {

	ti := textinput.New()
	ti.Prompt = "/ "
	pi := textinput.New()
	pi.Prompt = "p "
	gi := textinput.New()
	gi.Prompt = "/ "

	m := sessionTUI{
		store:           store,
		registry:        registry,
		title:           opts.title,
		reader:          viewport.New(),
		projectID:       projectID,
		projectName:     projectName,
		homeProjectID:   projectID,
		homeProjectName: projectName,
		homeSessions:    sessions,
		agents:          agents,
		installed:       installed,
		presetTo:        opts.presetTo,
		lastAgent:       opts.lastAgent,
		all:             sessions,
		viewMode:        opts.viewMode,
		search:          ti,
		projSearch:      pi,
		globalInput:     gi,
	}
	m.rebuildAgentCycle()
	m.applyFilter()

	switch {
	case opts.startInSearch:
		// `search`: land directly in the all-projects FTS, input focused (so the user types
		// immediately, exactly like before). Init fires the pre-seeded query, if any. The
		// search IS the root view here, so esc quits rather than unwinding into the browser.
		m.startedInSearch = true
		m.enterBrowser()
		m.globalActive = true
		m.globalSearching = true
		m.globalQuery = opts.initialQuery
		m.globalInput.SetValue(opts.initialQuery)
		m.globalInput.Focus() // focus state persists on the stored model; Init returns the blink+query cmds
		if queryReady(m.globalQuery) {
			m.searchSeq = 1
		}
	case len(sessions) == 0:
		// `resume` with an empty current project → open straight into the all-projects browser.
		m.startedInBrowser = true
		m.enterBrowser()
	}
	return m
}

func (m sessionTUI) Init() tea.Cmd {
	// Search starts focused in the all-projects input; kick the blink and any pre-seeded query.
	if m.globalSearching {
		cmds := []tea.Cmd{m.globalInput.Focus()}
		if queryReady(m.globalQuery) {
			cmds = append(cmds, searchDebounce(m.searchSeq, modeProjects))
		}
		return tea.Batch(cmds...)
	}
	return nil
}

func (m sessionTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.reader.SetWidth(m.width)
		m.reader.SetHeight(m.previewHeight())
		return m, nil
	case indexWarmedMsg:
		return m.refreshAfterWarm()
	case searchDebounceMsg:
		// Fire the actual query only if no newer keystroke has arrived (debounce).
		if msg.seq == m.searchSeq {
			if m.searchCancel != nil {
				m.searchCancel() // abort any prior in-flight query, freeing its connection
			}
			ctx, cancel := context.WithCancel(context.Background())
			m.searchCancel = cancel
			return m, m.runSearch(msg.seq, msg.kind, ctx)
		}
		return m, nil
	case searchResultMsg:
		// Apply only the latest query's results (discard stale async results).
		if msg.seq == m.searchSeq {
			m.applySearchResults(msg.kind, msg.sessions)
			return m, m.requestVisibleSnippets(msg.kind)
		}
		return m, nil
	case snippetResultMsg:
		if msg.seq == m.snippetSeq {
			if msg.kind == modeProjects {
				if m.globalSnippets == nil {
					m.globalSnippets = map[string]string{}
				}
				for key, snip := range msg.snippets {
					m.globalSnippets[key] = snip
				}
			} else {
				if m.filteredSnippets == nil {
					m.filteredSnippets = map[string]string{}
				}
				for key, snip := range msg.snippets {
					m.filteredSnippets[key] = snip
				}
			}
		}
		return m, nil
	case tea.KeyPressMsg:
		switch {
		case m.previewing:
			// The preview is a top-level overlay: it opens over the list OR the global
			// results, so it must be checked before any mode-specific routing.
			return m.updatePreview(msg)
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
		default:
			return m.updateList(msg)
		}
	}
	return m, nil
}

// refreshAfterWarm re-queries the index after the background current-project warm pass lands,
// so newly created or changed sessions for THIS project appear without a restart. It refreshes
// in place only when the home project's session list is what's on screen (preserving the
// cursor by session id), or re-runs a settled cross-project search to fold in the fresh rows.
// In any other view it just caches the refreshed sessions for when the user returns home.
func (m sessionTUI) refreshAfterWarm() (tea.Model, tea.Cmd) {
	sessions, err := m.store.ListByProject(m.homeProjectID)
	if err != nil {
		slog.Debug("resume: refresh after warm failed", "error", err)
		return m, nil
	}
	m.homeSessions = sessions

	// In-place refresh when the home project's list is the active view.
	if m.mode == modeList && !m.inBrowser && m.projectID == m.homeProjectID {
		selID := ""
		if sel := m.selected(); sel != nil {
			selID = sel.SessionID
		}
		m.all = sessions
		m.rebuildAgentCycle()
		m.applyFilter()
		m.cursor = indexOfSession(m.filtered, selID)
		m.clampScroll()
		return m, m.requestVisibleSnippets(modeList)
	}

	// A settled cross-project search (not mid-typing): re-run it so refreshed rows show up.
	// While typing, the user's own debounced queries already see the fresh data.
	if m.mode == modeProjects && m.globalActive && !m.globalSearching && queryReady(m.globalQuery) {
		m.searchSeq++
		return m, searchDebounce(m.searchSeq, modeProjects)
	}
	return m, nil
}

// indexOfSession returns the position of the session with id in list, or 0 when not present
// (so a refreshed list with a deleted selection lands safely at the top).
func indexOfSession(list []sessionindex.Session, id string) int {
	if id == "" {
		return 0
	}
	for i := range list {
		if list[i].SessionID == id {
			return i
		}
	}
	return 0
}

// updateList handles keys while browsing the session list.
func (m sessionTUI) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.result = sessionTUIResult{cancelled: true}
		return m, tea.Quit
	case "esc":
		// In a drilled-in project, esc returns to the browser; at home it quits.
		if m.inBrowser {
			m.enterBrowser()
			return m, nil
		}
		m.result = sessionTUIResult{cancelled: true}
		return m, tea.Quit
	case "tab":
		// Toggle scope: home list ↔ all-projects browser.
		m.enterBrowser()
		return m, nil
	case "up", "k":
		m.moveCursor(-1)
		return m, m.requestVisibleSnippets(modeList)
	case "down", "j":
		m.moveCursor(1)
		return m, m.requestVisibleSnippets(modeList)
	case "pgup":
		m.moveCursor(-m.listHeight())
		return m, m.requestVisibleSnippets(modeList)
	case "pgdown":
		m.moveCursor(m.listHeight())
		return m, m.requestVisibleSnippets(modeList)
	case "home", "g":
		m.cursor, m.top = 0, 0
		return m, m.requestVisibleSnippets(modeList)
	case "end", "G":
		m.cursor = len(m.filtered) - 1
		m.clampScroll()
		return m, m.requestVisibleSnippets(modeList)
	case "r":
		// Resume the highlighted session (enter is deliberately a no-op here, so a stray
		// return can't accidentally launch an agent).
		if sel := m.selected(); sel != nil {
			return m.beginResume(sel)
		}
	case " ", "space":
		if sel := m.selected(); sel != nil {
			return m.openPreview(sel)
		}
	case "/":
		m.searching = true
		m.search.SetValue(m.searchQuery)
		return m, m.search.Focus()
	case "a":
		return m, m.cycleAgent()
	case "v":
		m.toggleViewMode()
		return m, m.requestVisibleSnippets(modeList)
	}
	return m, nil
}

// updateSearch handles the full-text search input. The typed character is applied to the
// input immediately (instant feedback); the FTS query runs async + debounced so a slow
// query never blocks typing. See searchDebounceMsg / searchResultMsg.
func (m sessionTUI) updateSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searching = false
		m.search.Blur()
		m.searchQuery = ""
		m.searchSeq++ // invalidate any in-flight search
		m.snippetSeq++
		m.applyFilter()
		return m, nil
	case "enter":
		m.searching = false
		m.search.Blur()
		return m, nil
	case "up", "down", "pgup", "pgdown":
		// Arrows do nothing inside the one-line input, so commit the search and let the same
		// key move the now-focused list — the user reaches a result without a separate enter.
		m.searching = false
		m.search.Blur()
		return m.updateList(msg)
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	m.searchQuery = m.search.Value()
	m.searchSeq++
	if !queryReady(m.searchQuery) {
		// Too short to search: show the full (agent-filtered) list synchronously.
		m.snippetSeq++
		m.applyFilter()
		return m, cmd
	}
	return m, tea.Batch(cmd, searchDebounce(m.searchSeq, modeList))
}

// updatePreview handles keys while the glamour preview reader is open. Close keys return to
// the underlying screen (list or global results); `r` resumes the previewed session;
// everything else (↑↓ pgup/pgdn) scrolls the viewport.
func (m sessionTUI) updatePreview(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case " ", "space", "esc", "q":
		m.previewing = false
		m.readerSession = nil
		return m, nil
	case "r":
		if m.readerSession != nil {
			sess := m.readerSession
			m.previewing = false
			m.readerSession = nil
			return m.beginResume(sess)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.reader, cmd = m.reader.Update(msg)
	return m, cmd
}

// openPreview loads the highlighted session into the glamour reader. Identical to search's
// openReader so the two previews render the same way.
func (m sessionTUI) openPreview(s *sessionindex.Session) (tea.Model, tea.Cmd) {
	m.readerSession = s
	m.reader.SetWidth(m.width)
	m.reader.SetHeight(m.previewHeight())
	m.reader.SetContent(renderGlamour(sessionMarkdown(m.registry, m.store, s), m.width))
	m.reader.GotoTop()
	m.previewing = true
	return m, nil
}

// previewHeight is the viewport height inside the preview chrome (title + two rules + footer).
func (m sessionTUI) previewHeight() int {
	const chrome = 4
	h := m.height - chrome
	if h < 1 {
		h = 1
	}
	return h
}

// updateTarget handles the target-agent selection step.
func (m sessionTUI) updateTarget(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeList
		m.chosen = nil
	case "ctrl+c":
		m.result = sessionTUIResult{cancelled: true}
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
		m.result = sessionTUIResult{session: m.chosen, targetID: m.installed[m.targetCursor].id}
		return m, tea.Quit
	}
	return m, nil
}

// ---- list mechanics ----

// moveCursorWithin advances *cursor by delta within a list of n items, clamping to
// [0, n-1] and then realigning the scroll window via clampScrollWithin. It is the
// shared core of the three list cursors (sessions, projects, global search), which
// differ only in which fields they track. A no-op for an empty list.
func moveCursorWithin(cursor, top *int, delta, n, height int) {
	if n == 0 {
		return
	}
	*cursor += delta
	if *cursor < 0 {
		*cursor = 0
	}
	if *cursor > n-1 {
		*cursor = n - 1
	}
	clampScrollWithin(cursor, top, height)
}

// clampScrollWithin keeps the scroll window [*top, *top+height) covering *cursor.
func clampScrollWithin(cursor, top *int, height int) {
	if *cursor < *top {
		*top = *cursor
	}
	if *cursor >= *top+height {
		*top = *cursor - height + 1
	}
	if *top < 0 {
		*top = 0
	}
}

func (m *sessionTUI) moveCursor(delta int) {
	moveCursorWithin(&m.cursor, &m.top, delta, len(m.filtered), m.listHeight())
}

func (m *sessionTUI) clampScroll() {
	clampScrollWithin(&m.cursor, &m.top, m.listHeight())
}

func (m sessionTUI) selected() *sessionindex.Session {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return nil
	}
	return &m.filtered[m.cursor]
}

// rowsPerSession is how many terminal lines one list row occupies in each view mode.
func (m sessionTUI) rowsPerSession() int {
	if m.viewMode == "sparse" {
		return 2
	}
	return 1
}

// listHeight is how many sessions fit in the list region (height minus chrome).
func (m sessionTUI) listHeight() int {
	const chrome = 5 // header(1) + two rules(2) + footer(1) + margin
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

func (m *sessionTUI) rebuildAgentCycle() {
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

func (m *sessionTUI) cycleAgent() tea.Cmd {
	// In the all-projects search, cycle over every known agent and re-run the query; in a
	// session list, cycle over the agents actually present and re-filter in place.
	if m.mode == modeProjects && m.globalActive {
		m.agentFilter = nextInCycle(m.allAgentCycle(), m.agentFilter)
		m.searchSeq++
		return searchDebounce(m.searchSeq, modeProjects)
	}
	m.agentFilter = nextInCycle(m.agentCycle, m.agentFilter)
	// Re-filter the cached results in memory — the agent filter is client-side, so cycling it
	// must not trigger applyFilter's synchronous FTS query on the UI thread.
	m.refilterCurrentAgent()
	return m.requestVisibleSnippets(modeList)
}

// allAgentCycle is the agent-filter ring for cross-project search: "" then every known agent.
func (m sessionTUI) allAgentCycle() []string {
	ids := make([]string, 0, len(m.agents))
	for id := range m.agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return append([]string{""}, ids...)
}

// nextInCycle returns the element after cur in cycle, wrapping around.
func nextInCycle(cycle []string, cur string) string {
	idx := 0
	for i, id := range cycle {
		if id == cur {
			idx = i
			break
		}
	}
	return cycle[(idx+1)%len(cycle)]
}

func (m *sessionTUI) toggleViewMode() {
	if m.viewMode == "sparse" {
		m.viewMode = "dense"
	} else {
		m.viewMode = "sparse"
	}
	m.clampScroll()
}

// applyFilter rebuilds the visible list from the agent filter and search query. When a search
// is active it runs the FTS query, caches the raw results (searchRaw), then applies the agent
// filter; otherwise the list is the project's sessions filtered by agent. Snippets are fetched
// lazily for the visible rows. Callers that only change the agent filter should use
// refilterCurrentAgent, which reuses searchRaw and avoids a second query.
func (m *sessionTUI) applyFilter() {
	if queryReady(m.searchQuery) {
		m.searchRaw, _ = m.store.Search(ftsQuery(m.searchQuery), m.projectID)
	} else {
		m.searchRaw = nil
	}
	m.refilterCurrentAgent()
}

// refilterCurrentAgent re-derives the visible list for the current agent filter WITHOUT a new
// FTS query. The agent filter is applied client-side, so cycling agents during a search only
// needs to re-filter the cached raw results (searchRaw) — keeping `a` off the synchronous DB
// path that applyFilter would otherwise take on every keystroke. With no active search it
// filters the project's sessions (m.all).
func (m *sessionTUI) refilterCurrentAgent() {
	searching := queryReady(m.searchQuery)
	src := m.all
	if searching {
		src = m.searchRaw
	}
	out := make([]sessionindex.Session, 0, len(src))
	for _, s := range src {
		if m.agentFilter == "" || s.Agent == m.agentFilter {
			out = append(out, s)
		}
	}
	m.filtered = out
	if searching {
		// Keep any snippets already fetched (keyed by agent/session, so still valid); ensure the
		// map exists so requestVisibleSnippets can fill in the now-visible rows.
		if m.filteredSnippets == nil {
			m.filteredSnippets = map[string]string{}
		}
	} else {
		m.filteredSnippets = nil
	}

	if m.cursor > len(m.filtered)-1 {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.top = 0
	m.clampScroll()
}

// beginResume advances from a chosen session toward launch. When a target agent was
// pre-selected via `resume <agent>`, that choice is honored immediately and the picker exits
// — the user is never asked to pick a target. Otherwise it moves to the target-selection step.
func (m sessionTUI) beginResume(sess *sessionindex.Session) (tea.Model, tea.Cmd) {
	m.chosen = sess
	if m.presetTo != "" {
		m.result = sessionTUIResult{session: sess, targetID: m.presetTo}
		return m, tea.Quit
	}
	m.mode = modeTarget
	m.targetCursor = m.defaultTargetIndex()
	return m, nil
}

func (m sessionTUI) defaultTargetIndex() int {
	// Prefer the last-resumed agent, else the chosen session's own agent (same-agent resume).
	// An explicit preset (`resume <agent>`) never reaches here — beginResume skips the
	// target-selection step entirely in that case.
	want := m.lastAgent
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
