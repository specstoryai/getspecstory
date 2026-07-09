package cmd

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
)

// machineEntry is one stop in the machine-filter ring: a stable key and its display label.
type machineEntry struct {
	key   string // "" (all machines), "local" (this machine), or a remote deviceId
	label string // "all machines", "local only", or a machineName (disambiguated if it collides)
}

// rebuildMachineCycle derives the machine-filter ring from the current cloud rows. The ring is
// [all] until cloud rows from OTHER machines exist, at which point it becomes all → "local only" →
// each remote machine (keyed by stable deviceId, labelled by machineName, sorted by deviceId for
// stable order; duplicate names get (1)/(2) suffixes). This machine never appears as its own remote
// entry — it's covered by "local only".
func (m *sessionTUI) rebuildMachineCycle() {
	// Latest machineName per remote deviceId.
	names := map[string]string{}
	for _, s := range m.cloudAll {
		if !s.IsCloud || s.DeviceID == "" || s.DeviceID == m.deviceID {
			continue
		}
		if s.MachineName != "" {
			names[s.DeviceID] = s.MachineName
		} else if _, ok := names[s.DeviceID]; !ok {
			names[s.DeviceID] = shortID(s.DeviceID)
		}
	}

	if len(names) == 0 {
		// No other machines to distinguish — the filter would be a no-op, so hide it.
		m.machineCycle = []machineEntry{{key: "", label: "all machines"}}
		m.machineFilter = ""
		return
	}

	ids := make([]string, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// Disambiguate duplicate machine names with deterministic (1)/(2) suffixes.
	labelCount := map[string]int{}
	for _, id := range ids {
		labelCount[names[id]]++
	}
	labelSeen := map[string]int{}

	cycle := []machineEntry{{key: "", label: "all machines"}, {key: "local", label: "local only"}}
	for _, id := range ids {
		label := names[id]
		if labelCount[label] > 1 {
			labelSeen[label]++
			label = fmt.Sprintf("%s (%d)", label, labelSeen[label])
		}
		cycle = append(cycle, machineEntry{key: id, label: label})
	}
	m.machineCycle = cycle

	// If the active filter's machine dropped out of the ring, fall back to all.
	if !machineKeyInCycle(cycle, m.machineFilter) {
		m.machineFilter = ""
	}
}

func machineKeyInCycle(cycle []machineEntry, key string) bool {
	for _, e := range cycle {
		if e.key == key {
			return true
		}
	}
	return false
}

// cycleMachine advances the machine filter to the next ring entry and re-filters the cached rows
// client-side (no re-query), mirroring cycleAgent.
func (m *sessionTUI) cycleMachine() tea.Cmd {
	if len(m.machineCycle) <= 1 {
		return nil // nothing to cycle (only "all")
	}
	idx := 0
	for i, e := range m.machineCycle {
		if e.key == m.machineFilter {
			idx = i
			break
		}
	}
	m.machineFilter = m.machineCycle[(idx+1)%len(m.machineCycle)].key
	m.refilterCurrentAgent()
	return m.requestVisibleSnippets(modeList)
}

// machineMatch reports whether a row passes the current machine filter. "local only" keeps local
// rows plus this machine's own cloud rows (its synced-then-pruned sessions); a specific remote
// deviceId keeps only that machine's cloud rows.
func (m *sessionTUI) machineMatch(s sessionindex.Session) bool {
	switch m.machineFilter {
	case "":
		return true
	case "local":
		return !s.IsCloud || s.DeviceID == m.deviceID
	default:
		return s.IsCloud && s.DeviceID == m.machineFilter
	}
}

// machineScopeLabel returns the display label for the active machine filter, or "" when unfiltered.
func (m sessionTUI) machineScopeLabel() string {
	for _, e := range m.machineCycle {
		if e.key == m.machineFilter && e.key != "" {
			return e.label
		}
	}
	return ""
}

// Cloud resume — the blended-browse glue. SpecStory Cloud sessions from the user's
// other machines are fetched best-effort, off the UI thread, and merged into the local list
// (local-first, dedup local-preferred, re-sorted by recency). None of this is on the
// critical path of a local resume: any failure degrades silently to local-only.

// Footer nudges shown to users who can't yet use cloud resume. Eligibility = logged-in +
// Pro; the two states get different invitations.
const (
	cloudNudgeLogin   = "Log into SpecStory Cloud to search & resume sessions from your other machines"
	cloudNudgeUpgrade = "Upgrade to Pro to search & resume sessions from your other machines"
)

// cloudEligibilityMsg carries the async eligibility result (a network entitlement check).
type cloudEligibilityMsg struct {
	loggedIn bool
	pro      bool
}

// cloudSessionsMsg carries a completed cloud fetch for a project. sessions is already converted
// to index rows (IsCloud=true, agent resolved to a provider id); err set = silent degrade.
type cloudSessionsMsg struct {
	projectID string
	sessions  []sessionindex.Session
	err       error
}

// cloudProjectsMsg carries the completed cloud project list, already converted to project rows
// (IsCloud=true) with zero-session ghosts filtered out; err set = silent degrade.
type cloudProjectsMsg struct {
	projects []sessionindex.ProjectSummary
	err      error
}

// cloudEligibilityCmd runs the (networked) eligibility check off the UI thread.
func cloudEligibilityCmd() tea.Cmd {
	return func() tea.Msg {
		loggedIn, pro := cloud.ResumeEligibility()
		return cloudEligibilityMsg{loggedIn: loggedIn, pro: pro}
	}
}

// cloudFetchCmd lists a project's cloud-resumable sessions off the UI thread and converts them to
// index rows. agentIDByName resolves each row's display agent name back to a provider id.
func cloudFetchCmd(projectID string, agentIDByName map[string]string) tea.Cmd {
	return func() tea.Msg {
		cs, err := cloud.ListCloudSessions(projectID)
		if err != nil {
			return cloudSessionsMsg{projectID: projectID, err: err}
		}
		return cloudSessionsMsg{projectID: projectID, sessions: cloudToSessions(cs, agentIDByName)}
	}
}

// cloudProjectsFetchCmd lists the user's cloud projects off the UI thread for blending cloud-only
// projects into the all-projects browser.
func cloudProjectsFetchCmd() tea.Cmd {
	return func() tea.Msg {
		cp, err := cloud.ListCloudProjects()
		if err != nil {
			return cloudProjectsMsg{err: err}
		}
		return cloudProjectsMsg{projects: cloudToProjects(cp)}
	}
}

// cloudToSessions converts cloud session summaries to local index rows for blending. Rows whose
// agent can't be resolved to a known provider id are skipped — they can't be reconstructed/resumed
// here, so rendering them would only offer an action that fails.
func cloudToSessions(cs []cloud.CloudSession, agentIDByName map[string]string) []sessionindex.Session {
	out := make([]sessionindex.Session, 0, len(cs))
	for _, c := range cs {
		agentID, ok := agentIDByName[c.Metadata.AgentName]
		if !ok {
			slog.Debug("cloud resume: skipping session with unknown agent",
				"agentName", c.Metadata.AgentName, "sessionId", c.ClientID)
			continue
		}
		out = append(out, sessionindex.Session{
			ProjectID:   c.ProjectID,
			Agent:       agentID,
			SessionID:   c.ClientID,
			CreatedAt:   c.CreatedAt,
			UpdatedAt:   c.UpdatedAt,
			Name:        c.Name,
			IsCloud:     true,
			DeviceID:    c.Metadata.DeviceID,
			MachineName: c.Metadata.MachineName,
		})
	}
	return out
}

// cloudFetchForActiveCmd starts a cloud fetch for the active project when the user is cloud-
// eligible, marking the pending indicator; returns nil otherwise. Callers invoke it after changing
// the active project (drill into a project, or tab/esc back home) so that project's cloud sessions
// blend in, mirroring the initial fetch on open.
func (m *sessionTUI) cloudFetchForActiveCmd() tea.Cmd {
	if !m.cloudEligible {
		return nil
	}
	m.cloudPending = true
	return cloudFetchCmd(m.projectID, m.agentIDByName)
}

// cloudToProjects converts cloud project summaries to project rows for the blended browser. A
// project with zero sessions is skipped: a workspace can exist in the cloud with no sessions (e.g.
// created on first push, its sessions since deleted), in which case last_session is null and its
// lastUpdated defaults to ~now server-side — it would float to the top of the recency sort as a
// phantom with nothing to resume.
func cloudToProjects(cs []cloud.CloudProject) []sessionindex.ProjectSummary {
	out := make([]sessionindex.ProjectSummary, 0, len(cs))
	for _, c := range cs {
		if c.SessionCount == 0 {
			continue
		}
		out = append(out, sessionindex.ProjectSummary{
			ProjectID:    c.ID,
			ProjectName:  c.Name,
			Sessions:     c.SessionCount,
			LastActivity: c.LastUpdated,
			IsCloud:      true,
		})
	}
	return out
}

// applyCloudEligibility records the eligibility result, sets the appropriate nudge, and — when
// eligible — starts the first cloud fetch for the active (home) project's sessions and the user's
// cloud project list (for the blended all-projects browser).
func (m sessionTUI) applyCloudEligibility(msg cloudEligibilityMsg) (tea.Model, tea.Cmd) {
	m.cloudChecked = true
	m.cloudEligible = msg.loggedIn && msg.pro
	switch {
	case !msg.loggedIn:
		m.cloudNudge = cloudNudgeLogin
	case !msg.pro:
		m.cloudNudge = cloudNudgeUpgrade
	default:
		m.cloudNudge = ""
	}
	if !m.cloudEligible {
		return m, nil
	}
	m.cloudPending = true
	return m, tea.Batch(
		cloudFetchCmd(m.projectID, m.agentIDByName),
		cloudProjectsFetchCmd(),
	)
}

// applyCloudProjects records the cloud project list and, if the browser is showing, re-applies the
// project filter so cloud-only projects appear immediately (preserving the highlighted project).
func (m sessionTUI) applyCloudProjects(msg cloudProjectsMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		slog.Debug("cloud resume: list projects failed, showing local only", "error", msg.err)
		return m, nil
	}
	m.cloudProjects = msg.projects
	if m.mode == modeProjects {
		selID := ""
		if m.projCursor >= 0 && m.projCursor < len(m.projFiltered) {
			selID = m.projFiltered[m.projCursor].ProjectID
		}
		m.applyProjectFilter()
		m.projCursor = indexOfProject(m.projFiltered, selID)
		m.clampProjScroll()
	}
	return m, nil
}

// applyCloudSessions merges a completed cloud fetch into the active list: dedup local-preferred,
// re-sort by recency, preserve the selected row. Stale/failed fetches degrade silently.
func (m sessionTUI) applyCloudSessions(msg cloudSessionsMsg) (tea.Model, tea.Cmd) {
	m.cloudPending = false
	if msg.err != nil {
		slog.Debug("cloud resume: fetch failed, showing local only",
			"project", msg.projectID, "error", msg.err)
		return m, nil
	}
	// Discard results for a project the user has since navigated away from.
	if msg.projectID != m.projectID {
		return m, nil
	}

	// Preserve the selected session across the merge so the cursor doesn't jump.
	selID := ""
	if sel := m.selected(); sel != nil {
		selID = sel.SessionID
	}

	// Hold the cloud rows separately from m.all; refilterCurrentAgent merges them in. This
	// survives the local re-queries (index warm, post-delete refresh) that replace m.all.
	m.cloudAll = msg.sessions
	m.rebuildAgentCycle()
	m.applyFilter()
	m.cursor = indexOfSession(m.filtered, selID)
	m.clampScroll()
	return m, m.requestVisibleSnippets(modeList)
}

// mergeCloudRows blends cloud rows into a local list: dedup by (agent, session_id) with the LOCAL
// copy preferred (it resumes instantly and offline), then re-sort the union by recency
// (UpdatedAt desc). The TUI model never sorts its input, so cloud rows must be interleaved by time
// here rather than appended.
func mergeCloudRows(local, cloudRows []sessionindex.Session) []sessionindex.Session {
	seen := make(map[string]bool, len(local))
	for _, s := range local {
		seen[sessionindex.FingerprintKey(s.Agent, s.SessionID)] = true
	}
	merged := make([]sessionindex.Session, 0, len(local)+len(cloudRows))
	merged = append(merged, local...)
	for _, c := range cloudRows {
		if seen[sessionindex.FingerprintKey(c.Agent, c.SessionID)] {
			continue // local wins
		}
		merged = append(merged, c)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return sessionUpdatedTime(merged[i]).After(sessionUpdatedTime(merged[j]))
	})
	return merged
}

// sessionUpdatedTime parses a row's UpdatedAt for recency sorting. Local and cloud timestamps are
// both RFC3339 but differ in precision/offset (e.g. "…Z" vs "…+00:00" with microseconds), so a raw
// string compare would misorder them — parse to a real time instead. Unparseable → zero (sorts last).
func sessionUpdatedTime(s sessionindex.Session) time.Time {
	if t, err := time.Parse(time.RFC3339, s.UpdatedAt); err == nil {
		return t
	}
	return time.Time{}
}

// mergeCloudProjects blends cloud-only projects into the local project list: dedup by project_id
// with the local rollup preferred (it has the real per-agent chips and resumes offline), then
// re-sort the union by recency (LastActivity desc). Same shape as mergeCloudRows for sessions.
func mergeCloudProjects(local, cloudProjects []sessionindex.ProjectSummary) []sessionindex.ProjectSummary {
	seen := make(map[string]bool, len(local))
	for _, p := range local {
		seen[p.ProjectID] = true
	}
	merged := make([]sessionindex.ProjectSummary, 0, len(local)+len(cloudProjects))
	merged = append(merged, local...)
	for _, c := range cloudProjects {
		if seen[c.ProjectID] {
			continue // local wins
		}
		merged = append(merged, c)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return projectActivityTime(merged[i]).After(projectActivityTime(merged[j]))
	})
	return merged
}

// projectActivityTime parses a project's LastActivity for recency sorting (RFC3339, like
// sessionUpdatedTime). Unparseable → zero (sorts last).
func projectActivityTime(p sessionindex.ProjectSummary) time.Time {
	if t, err := time.Parse(time.RFC3339, p.LastActivity); err == nil {
		return t
	}
	return time.Time{}
}

// cloudDeleteResultMsg carries the outcome of an async cloud delete.
type cloudDeleteResultMsg struct {
	pd  pendingDelete
	err error
}

// cloudDeleteCmd deletes a cloud session or project via the API off the UI thread.
func cloudDeleteCmd(pd pendingDelete) tea.Cmd {
	return func() tea.Msg {
		var err error
		if pd.source == deleteFromProjects {
			err = cloud.DeleteCloudProject(pd.projectID)
		} else {
			err = cloud.DeleteCloudSession(pd.projectID, pd.sessionID)
		}
		return cloudDeleteResultMsg{pd: pd, err: err}
	}
}

// applyCloudDeleteResult removes the deleted cloud row/project from the cached cloud data and
// refreshes the view, or surfaces a transient error notice if the delete failed.
func (m sessionTUI) applyCloudDeleteResult(msg cloudDeleteResultMsg) (tea.Model, tea.Cmd) {
	pd := msg.pd
	if msg.err != nil {
		slog.Debug("cloud resume: cloud delete failed", "error", msg.err)
		m.statusMsg = "Cloud delete failed — " + pd.label + " left in place"
		return m, nil
	}

	scope := "session"
	if pd.source == deleteFromProjects {
		scope = "project"
	}
	analytics.TrackEvent(analytics.EventSessionDeleted, analytics.Properties{
		"scope":  scope,
		"agent":  pd.agent,
		"source": "cloud",
	})

	if pd.source == deleteFromProjects {
		m.cloudProjects = removeCloudProject(m.cloudProjects, pd.projectID)
		selID := ""
		if m.projCursor >= 0 && m.projCursor < len(m.projFiltered) {
			selID = m.projFiltered[m.projCursor].ProjectID
		}
		m.applyProjectFilter()
		m.projCursor = indexOfProject(m.projFiltered, selID)
		m.clampProjScroll()
		return m, nil
	}

	m.cloudAll = removeSession(m.cloudAll, pd.agent, pd.sessionID)
	m.rebuildAgentCycle()
	m.applyFilter()
	m.cursor = clampIndex(m.cursor, len(m.filtered))
	m.clampScroll()
	return m, m.requestVisibleSnippets(modeList)
}

// removeCloudProject returns projects without the given project_id.
func removeCloudProject(projects []sessionindex.ProjectSummary, projectID string) []sessionindex.ProjectSummary {
	out := make([]sessionindex.ProjectSummary, 0, len(projects))
	for _, p := range projects {
		if p.ProjectID == projectID {
			continue
		}
		out = append(out, p)
	}
	return out
}

// indexOfProject returns the position of the project with id in list, or 0 when absent (so a
// refreshed list with a vanished selection lands safely at the top).
func indexOfProject(list []sessionindex.ProjectSummary, id string) int {
	if id == "" {
		return 0
	}
	for i := range list {
		if list[i].ProjectID == id {
			return i
		}
	}
	return 0
}
