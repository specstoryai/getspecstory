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
	"unicode"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/config"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/session"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
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
	projSearching    bool
	projSearch       textinput.Model
	projSearchQuery  string

	// global session search: FTS across ALL projects, opened with / in the browser.
	globalActive    bool
	globalSearching bool
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
		// immediately, exactly like before). Init fires the pre-seeded query, if any.
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

func (m *sessionTUI) moveCursor(delta int) {
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

func (m *sessionTUI) clampScroll() {
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
	m.applyFilter()
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

// applyFilter rebuilds the visible list from the agent filter and search query. When a
// search is active, snippets are fetched lazily for the visible rows; otherwise the list
// is the project's sessions filtered by agent and snippets are cleared.
func (m *sessionTUI) applyFilter() {
	m.filtered = m.filtered[:0]
	m.filteredSnippets = nil

	if queryReady(m.searchQuery) {
		var out []sessionindex.Session
		if sessions, err := m.store.Search(ftsQuery(m.searchQuery), m.projectID); err == nil {
			for _, s := range sessions {
				if m.agentFilter == "" || s.Agent == m.agentFilter {
					out = append(out, s)
				}
			}
		}
		m.filtered = out
		m.filteredSnippets = map[string]string{}
	} else {
		out := make([]sessionindex.Session, 0, len(m.all))
		for _, s := range m.all {
			if m.agentFilter == "" || s.Agent == m.agentFilter {
				out = append(out, s)
			}
		}
		m.filtered = out
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

// ftsQuery turns free-form input into a safe FTS5 prefix query (alnum tokens only).
// minQueryLen is the shortest query we run a full-text search for. A single-character
// prefix (e.g. "t*") matches almost the entire corpus, and ranking + snippet generation
// over that set is pathologically slow (~80s on a ~800-session index), so we never fire
// it. Shared by resume and search so both behave the same.
const minQueryLen = 2

// queryReady reports whether input has enough searchable characters (letters/digits, the
// same runes ftsQuery keeps) to run an FTS query. Below minQueryLen we don't search.
func queryReady(input string) bool {
	n := 0
	for _, r := range input {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
			if n >= minQueryLen {
				return true
			}
		}
	}
	return false
}

// ftsQuery turns free-form input into an FTS5 query string. Three shapes:
//
//   - Bare words become independent prefix tokens, AND-ed (matched anywhere, any order) —
//     a loose search:  thank you      -> thank* you*
//   - A double-quoted span becomes a phrase: tokens adjacent and in order —
//     an exact phrase:  "thank you"    -> "thank you"
//   - A still-open quote (the user is mid-typing the phrase) keeps the final word a prefix,
//     so results keep updating live:  "thank yo    -> thank + yo*
//
// Only letters and digits survive tokenization, so no FTS5 syntax character from the raw
// input can reach MATCH — every query this builds is safe to execute.
func ftsQuery(input string) string {
	// Splitting on '"' yields alternating runs: even indexes are outside quotes, odd indexes
	// are inside. An odd-indexed run that is also the LAST run means the closing quote hasn't
	// been typed yet (an open phrase), so its final word stays a prefix for live type-ahead.
	segs := strings.Split(input, `"`)
	var parts []string
	for i, seg := range segs {
		words := alnumWords(seg)
		if len(words) == 0 {
			continue
		}
		switch {
		case i%2 == 0: // outside quotes: loose prefix tokens
			for _, w := range words {
				parts = append(parts, w+"*")
			}
		case i == len(segs)-1: // open phrase (still typing): adjacency + prefix last token
			parts = append(parts, strings.Join(words, " + ")+"*")
		default: // closed phrase: exact adjacency
			parts = append(parts, `"`+strings.Join(words, " ")+`"`)
		}
	}
	return strings.Join(parts, " ")
}

// alnumWords splits s on whitespace and reduces each field to its letters and digits,
// dropping any field with none. This is the tokenization FTS5's default tokenizer applies,
// mirrored here so queryReady and ftsQuery agree on what counts as a searchable token.
func alnumWords(s string) []string {
	var words []string
	for _, f := range strings.Fields(s) {
		var b strings.Builder
		for _, r := range f {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
			}
		}
		if b.Len() > 0 {
			words = append(words, b.String())
		}
	}
	return words
}

// ---- rendering ----

var (
	styDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styFaint  = lipgloss.NewStyle().Faint(true)
	styBold   = lipgloss.NewStyle().Bold(true)
	styCursor = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	stySel    = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
)

func (m sessionTUI) View() tea.View {
	var content string
	switch {
	case m.previewing:
		content = m.renderPreview()
	case m.mode == modeTarget:
		content = m.renderTarget()
	case m.mode == modeProjects && m.globalActive:
		content = m.renderGlobalResults()
	case m.mode == modeProjects:
		content = m.renderProjects()
	default:
		content = m.renderListScreen()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m sessionTUI) renderListScreen() string {
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

func (m sessionTUI) lineWidth() int {
	if m.width < 20 {
		return 80
	}
	return m.width
}

// headerLeft is the shared "<title> · <scope>" left segment used by every screen.
func (m sessionTUI) headerLeft(scope string) string {
	return styBold.Render(m.title) + styDim.Render(" · ") + scope
}

// projectScope labels the current project, e.g. "project: getspecstory" (name in white).
func (m sessionTUI) projectScope() string {
	return styDim.Render("project: ") + stySel.Render(m.projectName)
}

func (m sessionTUI) renderHeader() string {
	agent := "all"
	if m.agentFilter != "" {
		agent = m.agentName(m.agentFilter)
	}
	left := m.headerLeft(m.projectScope())
	right := styDim.Render("agent: ") + stySel.Render(agent)
	return headerRow(left, right, m.lineWidth())
}

func (m sessionTUI) renderRows() string {
	if len(m.filtered) == 0 {
		return styFaint.Render("  No sessions match.")
	}
	h := m.listHeight()
	end := min(m.top+h, len(m.filtered))
	var b strings.Builder
	for i := m.top; i < end; i++ {
		b.WriteString(m.sessionRow(m.filtered[i], i == m.cursor, m.snippetAt(i)))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// snippetAt returns the search snippet for filtered row i, or "" (no active search).
func (m sessionTUI) snippetAt(i int) string {
	if i >= 0 && i < len(m.filtered) {
		s := m.filtered[i]
		return m.filteredSnippets[sessionindex.FingerprintKey(s.Agent, s.SessionID)]
	}
	return ""
}

// sessionRow renders one list row. When snippet is non-empty (search active) the row
// shows the highlighted match context instead of the session title.
func (m sessionTUI) sessionRow(s sessionindex.Session, selected bool, snippet string) string {
	cursor := "  "
	if selected {
		cursor = styCursor.Render("▸ ")
	}
	agent := m.agentTag(s.Agent)
	when := fmt.Sprintf("%-4s", relativeTime(s.UpdatedAt))

	if m.viewMode == "sparse" {
		turns := styDim.Render(fmt.Sprintf("%d prompts", s.UserTurns))
		label := renderName(sessionTitle(s), selected, m.lineWidth()-24)
		if snippet != "" {
			label = renderSnippet(snippet, m.lineWidth()-26)
		}
		head := cursor + agent + "  " + label + "   " + turns
		sub := "    " + styFaint.Render(fmt.Sprintf("%s ago · %s", relativeTime(s.UpdatedAt), shortID(s.SessionID)))
		return head + "\n" + sub
	}
	turns := styDim.Render(fmt.Sprintf("%4d", s.UserTurns))
	// A year-stamped date ("Dec 31 '25") is wider than the 4-col slot; shrink the label by the
	// overflow so the right-hand turns column can't get pushed off the line and wrap.
	extra := len(when) - 4
	if extra < 0 {
		extra = 0
	}
	label := renderName(sessionTitle(s), selected, m.lineWidth()-22-extra)
	if snippet != "" {
		label = renderSnippet(snippet, m.lineWidth()-24-extra)
	}
	return cursor + agent + " " + styDim.Render(when) + "  " + label + "  " + turns
}

// renderSnippet renders a FTS snippet (matched terms wrapped in the control-char marks)
// with the matches highlighted, collapsing whitespace and clipping to maxWidth columns.
func renderSnippet(snip string, maxWidth int) string {
	if maxWidth < 8 {
		maxWidth = 8
	}
	snip = strings.NewReplacer("\n", " ", "\t", " ", "\r", " ").Replace(snip)
	hl := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(lipgloss.Color("221"))

	var b strings.Builder
	var seg strings.Builder
	matched := false
	visible := 0
	flush := func() {
		if seg.Len() == 0 {
			return
		}
		if matched {
			b.WriteString(hl.Render(seg.String()))
		} else {
			b.WriteString(seg.String())
		}
		seg.Reset()
	}
	for _, r := range snip {
		switch r {
		case '\x02':
			flush()
			matched = true
			continue
		case '\x03':
			flush()
			matched = false
			continue
		}
		if visible >= maxWidth {
			flush()
			b.WriteString("…")
			return b.String()
		}
		seg.WriteRune(r)
		visible++
	}
	flush()
	return b.String()
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

func (m sessionTUI) renderGlance() string {
	sel := m.selected()
	if sel == nil {
		return ""
	}
	return styDim.Render("⟶  ") + styFaint.Render(truncate(sessionTitle(*sel), m.lineWidth()-4))
}

func (m sessionTUI) renderFooter() string {
	if m.searching {
		return m.search.View() + "    " + styFaint.Render("esc clear · enter apply")
	}
	scopeKey := "tab all-projects"
	if m.inBrowser {
		scopeKey = "tab/esc back"
	}
	keys := []string{"↑↓ move", "r resume", "space preview", "/ search", "a agent", scopeKey, "v " + m.viewMode, "q quit"}
	return styDim.Render(strings.Join(keys, " · "))
}

func (m sessionTUI) renderPreview() string {
	var b strings.Builder
	left := styBold.Render("Preview")
	if s := m.readerSession; s != nil {
		left += styDim.Render(" · ") + m.agentTag(s.Agent) +
			styDim.Render(" · ") + sessionTitle(*s)
	}
	b.WriteString(left)
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.lineWidth()))
	b.WriteString("\n")
	b.WriteString(m.reader.View())
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.lineWidth()))
	b.WriteString("\n")
	b.WriteString(styDim.Render(strings.Join([]string{"↑↓ scroll", "pgup/pgdn page", "r resume", "space/esc close"}, " · ")))
	return b.String()
}

func (m sessionTUI) renderTarget() string {
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
	b.WriteString(styDim.Render(strings.Join([]string{"↑↓ move", "↵ resume", "esc back"}, " · ")))
	return b.String()
}

// ---- helpers ----

func (m sessionTUI) agentName(id string) string {
	if a, ok := m.agents[id]; ok {
		return a.name
	}
	return id
}

func (m sessionTUI) agentTag(id string) string {
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
		lt := t.Local()
		if lt.Year() == time.Now().Year() {
			return lt.Format("Jan 2")
		}
		// Disambiguate prior years so "Dec 31" can't be mistaken for this year: "Dec 31 '25".
		return lt.Format("Jan 2 '06")
	}
}

// headerRow lays out a left and right segment across width with a gap between.
func headerRow(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// renderGlamour renders markdown to styled terminal output for the preview reader.
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

// sessionMarkdown returns the session as markdown for the preview — the real specstory
// render when the session can be re-parsed (needs a cwd), else the plain FTS body.
func sessionMarkdown(registry *factory.Registry, store *sessionindex.Store, s *sessionindex.Session) string {
	if s.OriginCwd != "" {
		if prov, err := registry.Get(s.Agent); err == nil {
			if full, err := prov.GetAgentChatSession(s.OriginCwd, s.SessionID, false); err == nil &&
				full != nil && full.SessionData != nil {
				if md, err := session.GenerateMarkdownFromAgentSession(full.SessionData, false, true); err == nil {
					return md
				}
			}
		}
	}
	if body, _ := store.SessionBody(s.Agent, s.SessionID); strings.TrimSpace(body) != "" {
		return "```\n" + body + "\n```"
	}
	return "_(no readable content for this session)_"
}

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
	rm := final.(sessionTUI)
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

func (m *sessionTUI) clampProjScroll() {
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
func (m sessionTUI) projectsHeight() int {
	const chrome = 9
	avail := m.height - chrome
	if avail < 1 {
		avail = 1
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
		m.searchSeq++
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
		m.exitGlobal()
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

func (m *sessionTUI) exitGlobal() {
	m.globalActive = false
	m.globalSearching = false
	m.globalInput.Blur()
	m.globalInput.SetValue("")
	m.globalQuery = ""
	m.globalResults = nil
	m.globalSnippets = nil
	m.snippetSeq++
	m.globalCursor, m.globalTop = 0, 0
}

func (m *sessionTUI) moveGlobalCursor(delta int) {
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

func (m *sessionTUI) clampGlobalScroll() {
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

	left := m.headerLeft("all projects")
	if q := strings.TrimSpace(m.globalQuery); q != "" && !m.globalSearching {
		left += styDim.Render(" · ") + stySel.Render(q)
	}
	right := styDim.Render(fmt.Sprintf("%d matches", len(m.globalResults)))
	if m.agentFilter != "" {
		right += styDim.Render("   agent: ") + stySel.Render(m.agentName(m.agentFilter))
	}
	b.WriteString(headerRow(left, right, m.lineWidth()) + "\n")
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
			s := m.globalResults[i]
			snip := m.globalSnippets[sessionindex.FingerprintKey(s.Agent, s.SessionID)]
			b.WriteString(m.globalRow(s, i == m.globalCursor, snip))
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
	keys := []string{"↑↓ move", "r resume", "space preview", "a agent", "v " + m.viewMode, "/ edit search", "esc back", "q quit"}
	b.WriteString(styDim.Render(strings.Join(keys, " · ")))
	return b.String()
}

// globalRow renders a cross-project search hit: agent · time · project · highlighted match
// snippet. Honors the dense/sparse view mode, matching the project session list.
func (m sessionTUI) globalRow(s sessionindex.Session, selected bool, snippet string) string {
	cursor := "  "
	if selected {
		cursor = styCursor.Render("▸ ")
	}
	agent := m.agentTag(s.Agent)
	proj := fmt.Sprintf("%-18s", truncate(sessionProject(s), 18))

	if m.viewMode == "sparse" {
		label := renderName(sessionTitle(s), selected, m.lineWidth()-30)
		if snippet != "" {
			label = renderSnippet(snippet, m.lineWidth()-32)
		}
		head := cursor + agent + "  " + styFaint.Render(proj) + "  " + label
		sub := "    " + styFaint.Render(fmt.Sprintf("%s ago · %s", relativeTime(s.UpdatedAt), shortID(s.SessionID)))
		return head + "\n" + sub
	}

	when := fmt.Sprintf("%-4s", relativeTime(s.UpdatedAt))
	label := renderName(sessionTitle(s), selected, m.lineWidth()-46)
	if snippet != "" {
		label = renderSnippet(snippet, m.lineWidth()-48)
	}
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

// ---- async, debounced full-text search ----

// searchDebounceMsg fires after a brief typing pause; the query runs only if its seq is
// still current. searchResultMsg carries the async FTS results back to the model. Both
// keep typing instant: the input updates synchronously while the query runs off-thread.
type searchDebounceMsg struct {
	seq  int
	kind tuiMode // modeList (project-scoped) or modeProjects (global)
}

type searchResultMsg struct {
	seq      int
	kind     tuiMode
	sessions []sessionindex.Session
}

type snippetResultMsg struct {
	seq      int
	kind     tuiMode
	snippets map[string]string
}

const searchDebounceDelay = 50 * time.Millisecond

// searchDebounce schedules a debounce tick. On fire, the model checks the seq and decides
// whether to actually run the query (so only the last keystroke in a burst queries).
func searchDebounce(seq int, kind tuiMode) tea.Cmd {
	return tea.Tick(searchDebounceDelay, func(time.Time) tea.Msg {
		return searchDebounceMsg{seq: seq, kind: kind}
	})
}

// runSearch returns a command that performs the FTS query off the UI thread.
func (m sessionTUI) runSearch(seq int, kind tuiMode, ctx context.Context) tea.Cmd {
	store := m.store
	query, projectID := m.searchQuery, m.projectID
	if kind == modeProjects {
		query, projectID = m.globalQuery, ""
	}
	fq := ftsQuery(query)
	return func() tea.Msg {
		if !queryReady(query) {
			return searchResultMsg{seq: seq, kind: kind}
		}
		sessions, _ := store.SearchContext(ctx, fq, projectID)
		return searchResultMsg{seq: seq, kind: kind, sessions: sessions}
	}
}

// applySearchResults installs async FTS results into the matching list (scoped or global).
func (m *sessionTUI) applySearchResults(kind tuiMode, sessions []sessionindex.Session) {
	if kind == modeProjects {
		m.globalSnippets = map[string]string{}
		var out []sessionindex.Session
		for _, s := range sessions {
			if m.agentFilter == "" || s.Agent == m.agentFilter {
				out = append(out, s)
			}
		}
		m.globalResults = out
		if m.globalCursor > len(m.globalResults)-1 {
			m.globalCursor = len(m.globalResults) - 1
		}
		if m.globalCursor < 0 {
			m.globalCursor = 0
		}
		m.globalTop = 0
		return
	}

	var out []sessionindex.Session
	for _, s := range sessions {
		if m.agentFilter == "" || s.Agent == m.agentFilter {
			out = append(out, s)
		}
	}
	m.filtered = out
	m.filteredSnippets = map[string]string{}
	if m.cursor > len(m.filtered)-1 {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.top = 0
	m.clampScroll()
}

func (m *sessionTUI) requestVisibleSnippets(kind tuiMode) tea.Cmd {
	var query string
	var rows []sessionindex.Session
	if kind == modeProjects {
		if !queryReady(m.globalQuery) || len(m.globalResults) == 0 {
			return nil
		}
		h := m.globalHeight()
		end := min(m.globalTop+h, len(m.globalResults))
		if m.globalTop >= end {
			return nil
		}
		query = m.globalQuery
		rows = append([]sessionindex.Session(nil), m.globalResults[m.globalTop:end]...)
	} else {
		if !queryReady(m.searchQuery) || len(m.filtered) == 0 {
			return nil
		}
		h := m.listHeight()
		end := min(m.top+h, len(m.filtered))
		if m.top >= end {
			return nil
		}
		query = m.searchQuery
		rows = append([]sessionindex.Session(nil), m.filtered[m.top:end]...)
	}

	store := m.store
	fq := ftsQuery(query)
	m.snippetSeq++
	seq := m.snippetSeq
	return func() tea.Msg {
		snips, _ := store.SnippetsContext(context.Background(), fq, rows)
		return snippetResultMsg{seq: seq, kind: kind, snippets: snips}
	}
}
