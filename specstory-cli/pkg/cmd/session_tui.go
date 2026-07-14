package cmd

import (
	"context"
	"image/color"
	"log/slog"
	"sort"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
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

// deleteSource records which view a pending delete was triggered from, so the confirm prompt,
// the store call (a single session vs. a whole project), and the post-delete refresh all match
// the originating view.
type deleteSource int

const (
	deleteFromList     deleteSource = iota // a session in the (home or drilled-in) session list
	deleteFromGlobal                       // a session in the cross-project search results
	deleteFromProjects                     // a whole project in the all-projects browser
)

// pendingDelete is the target captured when 'd' is pressed, applied only if the user confirms.
// It is a soft delete: the session(s) are hidden from the picker/search and won't be re-indexed,
// but the native files on disk are untouched and the rows survive as tombstones. See
// sessionindex.SoftDeleteSession / SoftDeleteProject.
type pendingDelete struct {
	source    deleteSource
	agent     string // session target (deleteFromList / deleteFromGlobal)
	sessionID string
	projectID string // project target (deleteFromProjects); also the cloud project for a cloud session
	label     string // human label for the confirmation line
	count     int    // project session count (deleteFromProjects)
	// isCloud routes the delete to the Cloud API (a permanent, cross-machine delete) instead of a
	// local soft delete, and switches the confirmation copy to match.
	isCloud bool
}

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
	// previewSeq is bumped on every openPreview. A cloud preview loads its markdown off-thread
	// (a blob fetch), so its late-arriving result is applied only when this seq still matches —
	// dropping the result if the user has since closed the reader or moved to another session.
	previewSeq int

	// confirmingDelete is a top-level modal (like previewing): 'd' in a session list, the global
	// search results, or the project browser opens a y/N soft-delete confirmation for pendingDelete.
	confirmingDelete bool
	pendingDelete    pendingDelete

	// statusMsg is a transient one-line notice (currently only a failed delete) shown under the
	// footer until the next keypress. A soft delete otherwise gives no feedback beyond the row
	// disappearing, so a failure would look like a silent no-op without this.
	statusMsg string

	// Cloud resume: SpecStory Cloud sessions from the user's OTHER machines, blended
	// into the browser best-effort and local-first. Eligibility (logged-in + Pro) is
	// checked once, async, off the UI thread; cloudEligible gates fetches; cloudPending drives the
	// "searching cloud…" indicator; cloudNudge is the footer invitation shown to ineligible users.
	// agentIDByName reverse-maps a cloud row's display agent name ("Claude Code") back to
	// the provider id ("claude") that dedup and the resume/reconstruct path key on.
	cloudChecked  bool
	cloudEligible bool
	cloudPending  bool
	cloudNudge    string
	// cloudAll holds the active project's cloud sessions SEPARATELY from m.all. They must not live
	// in m.all: the background index warm and post-delete refresh both do `m.all = ListByProject()`,
	// which would discard anything merged into it. Instead they're merged with m.all at filter time
	// (refilterCurrentAgent), so a local re-query can't wipe them.
	cloudAll      []sessionindex.Session
	cloudProjects []cloud.CloudProject // cloud projects, each carrying its resumable session summaries inline; merged into the browser at filter time
	// cloudLocalKeys is the local per-project session fingerprint set, fetched alongside
	// cloudProjects so the all-projects rollup can dedup cloud sessions against local by
	// (agent, session_id) — local preferred — and recompute accurate per-agent chips / totals / last
	// activity from the union. Nil until the first cloud-projects fetch completes.
	cloudLocalKeys map[string][]sessionindex.ProjectSessionKey
	agentIDByName  map[string]string // agent display name -> provider id

	// Machine filter (the `m` key), modeled on the agent filter: cycle all → "local only" → each
	// remote machine. Only meaningful once cloud rows from OTHER machines are present, so the ring
	// is just [all] until then. Keyed by stable deviceId; displayed by machineName.
	machineFilter string         // "" = all; "local" = this machine; else a remote deviceId
	machineCycle  []machineEntry // the filter ring, rebuilt from cloud rows in play
	deviceID      string         // this machine's stable id (for the "local only" match)

	// Cloud search (global search view). Kept separate from the local FTS results, exactly like
	// cloudAll vs all in browse, so a fresh local query (per keystroke) and a cloud result (on the
	// slower fire-on-pause debounce) don't clobber each other; rebuildGlobalResults merges them.
	globalLocal         []sessionindex.Session // raw local FTS results for the active global query
	globalCloud         []sessionindex.Session // cloud search hits for the active global query
	globalCloudSnippets map[string]string      // cloud snippets, keyed by FingerprintKey(agent, id)
	cloudSearchSeq      int                    // debounce seq for cloud search (separate from searchSeq)
	cloudSearchPending  bool                   // a cloud search is in flight (drives the indicator)

	// Cloud search for the project-scoped list search (`/` inside a drilled-in project). Same
	// separate-slice pattern as globalCloud: the server scopes these to the active project, and
	// refilterCurrentAgent merges them into m.filtered so a per-keystroke local re-query can't
	// discard them.
	listCloud         []sessionindex.Session
	listCloudSnippets map[string]string // cloud snippets for the list search, keyed by FingerprintKey

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

	// pinnedSession is a session resolved by `resume --session <uri>` (no preset agent). When
	// set, the TUI opens straight at the target picker (modeTarget) with this session pinned as
	// chosen — skipping the browse step — so the user only picks which agent to resume into. The
	// default target highlight (last-resumed, else the session's own agent) still applies.
	pinnedSession *sessionindex.Session
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
	// Reverse map for blending cloud rows: their metadata carries the display agent name, but
	// dedup and resume key on the provider id.
	m.agentIDByName = make(map[string]string, len(agents))
	for id, meta := range agents {
		m.agentIDByName[meta.name] = id
	}
	m.deviceID = cloud.DeviceID()
	m.rebuildAgentCycle()
	m.applyFilter()

	switch {
	case opts.pinnedSession != nil:
		// `resume --session <uri>` (no preset agent): open at the target picker pinned to the
		// resolved session, skipping the browse step. Takes precedence over the empty-project
		// browser fallback and the search entry — a pinned session is the whole point of the
		// invocation. esc at the target picker drops into the browse list (the escape hatch).
		m.chosen = opts.pinnedSession
		m.mode = modeTarget
		m.targetCursor = m.defaultTargetIndex()
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
	// Kick the async cloud-eligibility check once on open (off the UI thread). When it resolves,
	// Update either starts the first cloud fetch (eligible) or sets the footer nudge.
	cmds := []tea.Cmd{cloudEligibilityCmd()}
	// Search starts focused in the all-projects input; kick the blink and any pre-seeded query.
	if m.globalSearching {
		cmds = append(cmds, m.globalInput.Focus())
		if queryReady(m.globalQuery) {
			cmds = append(cmds, searchDebounce(m.searchSeq, modeProjects))
		}
	}
	return tea.Batch(cmds...)
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
	case cloudEligibilityMsg:
		return m.applyCloudEligibility(msg)
	case cloudSessionsMsg:
		return m.applyCloudSessions(msg)
	case cloudProjectsMsg:
		return m.applyCloudProjects(msg)
	case cloudDeleteResultMsg:
		return m.applyCloudDeleteResult(msg)
	case cloudSearchDebounceMsg:
		return m.applyCloudSearchDebounce(msg)
	case cloudSearchResultMsg:
		return m.applyCloudSearchResult(msg)
	case cloudPreviewMsg:
		return m.applyCloudPreview(msg)
	case tea.KeyPressMsg:
		// Any keypress dismisses a lingering status notice (e.g. a failed delete); it has served
		// its purpose once the user has read it and moved on.
		m.statusMsg = ""
		switch {
		case m.confirmingDelete:
			// The delete confirmation is a top-level modal, reachable from the list, the global
			// results, or the project browser, so it is checked before any mode-specific routing.
			return m.updateDeleteConfirm(msg)
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
		// Resume the highlighted session. Enter is NOT a resume trigger (a stray return
		// shouldn't launch an agent); it previews, aliased to space below.
		if sel := m.selected(); sel != nil {
			return m.beginResume(sel)
		}
	// Enter is silently aliased to space so either key previews (the hint only advertises
	// space). 'r' remains the sole resume trigger.
	case " ", "space", "enter":
		if sel := m.selected(); sel != nil {
			return m.openPreview(sel)
		}
	case "/":
		m.searching = true
		m.search.SetValue(m.searchQuery)
		return m, m.search.Focus()
	case "a":
		return m, m.cycleAgent()
	case "m":
		return m, m.cycleMachine()
	case "v":
		m.toggleViewMode()
		return m, m.requestVisibleSnippets(modeList)
	case "u":
		if cmd := m.upgradeCmd(); cmd != nil {
			return m, cmd
		}
	case "d":
		if sel := m.selected(); sel != nil {
			m.pendingDelete = pendingDelete{source: deleteFromList, agent: sel.Agent, sessionID: sel.SessionID, projectID: sel.ProjectID, isCloud: sel.IsCloud, label: sessionTitle(*sel)}
			m.confirmingDelete = true
		}
	}
	return m, nil
}

// updateDeleteConfirm handles the y/N soft-delete confirmation modal. Deletion requires an
// explicit "y"; every other key (n, esc, q, or a stray press) cancels, so the destructive path
// is never the default.
func (m sessionTUI) updateDeleteConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		return m.performDelete()
	case "ctrl+c":
		m.result = sessionTUIResult{cancelled: true}
		return m, tea.Quit
	default:
		m.confirmingDelete = false
	}
	return m, nil
}

// performDelete applies the pending soft delete, records an analytics event, and refreshes the
// originating view. A soft delete hides the session(s) from the picker/search and stops reindex
// re-adding them; the native session files on disk are left untouched.
func (m sessionTUI) performDelete() (tea.Model, tea.Cmd) {
	m.confirmingDelete = false
	pd := m.pendingDelete

	// Cloud rows delete from SpecStory Cloud via the API, async so a slow/down server can't freeze
	// the TUI. The row stays until the delete confirms (applyCloudDeleteResult removes it).
	if pd.isCloud {
		return m, cloudDeleteCmd(pd)
	}

	affected, err := softDeletePending(pd)
	if err != nil {
		slog.Debug("resume: soft delete failed", "error", err)
		m.statusMsg = "Delete failed — " + pd.label + " left in place"
		return m, nil
	}

	scope := "session"
	if pd.source == deleteFromProjects {
		scope = "project"
	}
	analytics.TrackEvent(analytics.EventSessionDeleted, analytics.Properties{
		"scope":    scope,
		"agent":    pd.agent,
		"affected": affected,
	})

	return m.refreshAfterDelete(pd)
}

// softDeletePending opens a short-lived writer handle to sessions.db — the browse handle is
// read-only (OpenReader) — applies the tombstone, and closes it. Delete is a rare interactive
// action, so the open/close cost is irrelevant; WAL lets this writer commit alongside the read
// handle and the background warm's writer.
func softDeletePending(pd pendingDelete) (int, error) {
	dbPath, err := sessionindex.DefaultPath()
	if err != nil {
		return 0, err
	}
	w, err := sessionindex.Open(dbPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = w.Close() }()

	if pd.source == deleteFromProjects {
		return w.SoftDeleteProject(pd.projectID)
	}
	return w.SoftDeleteSession(pd.agent, pd.sessionID)
}

// refreshAfterDelete re-syncs the view the delete was triggered from so the removed session or
// project drops out immediately, without a restart. The read handle (m.store) sees the writer's
// committed change on its next query (WAL starts a fresh read snapshot per statement).
func (m sessionTUI) refreshAfterDelete(pd pendingDelete) (tea.Model, tea.Cmd) {
	switch pd.source {
	case deleteFromProjects:
		// Re-roll the project list; a project with no live sessions left drops out entirely.
		m.projectsLoaded = false
		m.enterBrowser()
		// Deleting the last project would leave the cursor past the shortened list, so clamp it
		// (mirrors the session-list and global paths below) before fixing the scroll window.
		m.projCursor = clampIndex(m.projCursor, len(m.projFiltered))
		m.clampProjScroll()
		return m, nil
	case deleteFromGlobal:
		// Drop the deleted hit from the in-memory results. Its FTS row is already gone, so a
		// re-query would also exclude it, but removing in place avoids an async round trip.
		m.globalResults = removeSession(m.globalResults, pd.agent, pd.sessionID)
		delete(m.globalSnippets, sessionindex.FingerprintKey(pd.agent, pd.sessionID))
		m.globalCursor = clampIndex(m.globalCursor, len(m.globalResults))
		m.clampGlobalScroll()
		return m, m.requestVisibleSnippets(modeProjects)
	default: // deleteFromList
		// Re-query the active project (home or drilled-in) and rebuild. The cursor keeps its
		// index (clamped), so the next session slides up under it.
		sessions, err := m.store.ListByProject(m.projectID)
		if err != nil {
			slog.Debug("resume: refresh after delete failed", "error", err)
			return m, nil
		}
		if m.projectID == m.homeProjectID {
			m.homeSessions = sessions
		}
		m.all = sessions
		m.rebuildAgentCycle()
		m.applyFilter()
		m.cursor = clampIndex(m.cursor, len(m.filtered))
		m.clampScroll()
		return m, m.requestVisibleSnippets(modeList)
	}
}

// removeSession returns sessions without the (agent, sessionID) entry.
func removeSession(sessions []sessionindex.Session, agent, sessionID string) []sessionindex.Session {
	out := make([]sessionindex.Session, 0, len(sessions))
	for _, s := range sessions {
		if s.Agent == agent && s.SessionID == sessionID {
			continue
		}
		out = append(out, s)
	}
	return out
}

// clampIndex keeps a cursor within [0, n-1], or 0 when the list is empty.
func clampIndex(i, n int) int {
	if i > n-1 {
		i = n - 1
	}
	if i < 0 {
		i = 0
	}
	return i
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
		m.cloudSearchSeq++
		m.listCloud = nil
		m.listCloudSnippets = nil
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
	m.cloudSearchSeq++ // supersede any in-flight cloud search
	// Current cloud hits are for the previous query — drop them so a fresh local result doesn't merge
	// stale cloud rows; the fire-on-pause cloud search refills them.
	m.listCloud = nil
	m.listCloudSnippets = nil
	if !queryReady(m.searchQuery) {
		// Too short to search: show the full (agent-filtered) list synchronously.
		m.cloudSearchPending = false
		m.snippetSeq++
		m.applyFilter()
		return m, cmd
	}
	cmds := []tea.Cmd{cmd, searchDebounce(m.searchSeq, modeList)}
	// Cloud search fires on its own slower fire-on-pause debounce, only when eligible.
	if m.cloudEligible {
		cmds = append(cmds, cloudSearchDebounce(m.cloudSearchSeq, modeList))
	}
	return m, tea.Batch(cmds...)
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
	m.reader.GotoTop()
	m.previewing = true
	m.previewSeq++

	// A cloud-only session has no local native file and no local FTS body, so sessionMarkdown
	// can't render it. Fetch its SessionData blob from the cloud (the same one resume uses) and
	// render it off-thread, showing a placeholder meanwhile so the reader opens instantly.
	if s.IsCloud {
		m.reader.SetContent(renderGlamour("_Loading from SpecStory Cloud…_", m.width))
		return m, cloudPreviewCmd(*s, m.previewSeq)
	}

	m.reader.SetContent(renderGlamour(sessionMarkdown(m.registry, m.store, s), m.width))
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
	// Cloud rows can introduce agents not present locally, so include them in the filter ring.
	for _, s := range m.cloudAll {
		present[s.Agent] = true
	}
	ids := make([]string, 0, len(present))
	for id := range present {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	m.agentCycle = append([]string{""}, ids...)
	// The machine ring is derived from the same row set (specifically the cloud rows), so keep it
	// in lockstep with the agent ring.
	m.rebuildMachineCycle()
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
	// Browse (no active search): blend the active project's cloud sessions with the local ones —
	// deduped local-preferred and re-sorted by recency. Kept out of m.all so local re-queries
	// (index warm, post-delete refresh) can't discard them. Search stays local-only for now.
	if len(m.cloudAll) > 0 {
		src = mergeCloudRows(m.all, m.cloudAll)
	}
	if searching {
		// Blend the project's cloud search hits (fetched server-side, scoped to this project) with the
		// local FTS results — deduped local-preferred and recency-sorted, the same merge browse and
		// global search use. Kept out of searchRaw so a per-keystroke local re-query can't drop them.
		src = m.searchRaw
		if len(m.listCloud) > 0 {
			src = mergeCloudRows(m.searchRaw, m.listCloud)
		}
	}
	out := make([]sessionindex.Session, 0, len(src))
	for _, s := range src {
		if m.agentFilter != "" && s.Agent != m.agentFilter {
			continue
		}
		if !m.machineMatch(s) {
			continue
		}
		out = append(out, s)
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
