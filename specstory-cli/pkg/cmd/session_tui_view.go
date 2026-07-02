package cmd

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/session"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
)

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

// agentScope mirrors the project-scope label: an unstyled "all agents" phrase (rendered like
// "all projects") when unfiltered, else "agent: <name>" matching projectScope's "project: <name>".
func (m sessionTUI) agentScope() string {
	if m.agentFilter == "" {
		return "all agents"
	}
	return styDim.Render("agent: ") + stySel.Render(m.agentName(m.agentFilter))
}

func (m sessionTUI) renderHeader() string {
	// Agent filter sits left, right after the project, rather than tucked in the far corner.
	left := m.headerLeft(m.projectScope()) + styDim.Render("  ·  ") + m.agentScope()
	return headerRow(left, "", m.lineWidth())
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

// rowCursor renders the two-column selection gutter shared by every list row.
func rowCursor(selected bool) string {
	if selected {
		return styCursor.Render("▸ ")
	}
	return "  "
}

// rowLabel picks a list row's primary text: the highlighted FTS snippet when a search
// is active, otherwise the session title. titleWidth/snippetWidth are the separate
// column budgets for each (snippet markup needs slightly more room). Shared by
// sessionRow and globalRow.
func rowLabel(s sessionindex.Session, selected bool, snippet string, titleWidth, snippetWidth int) string {
	if snippet != "" {
		return renderSnippet(snippet, snippetWidth)
	}
	return renderName(sessionTitle(s), selected, titleWidth)
}

// sessionRow renders one list row. When snippet is non-empty (search active) the row
// shows the highlighted match context instead of the session title.
func (m sessionTUI) sessionRow(s sessionindex.Session, selected bool, snippet string) string {
	cursor := rowCursor(selected)
	agent := m.agentTag(s.Agent)
	when := fmt.Sprintf("%-4s", relativeTime(s.UpdatedAt))

	if m.viewMode == "sparse" {
		turns := styDim.Render(fmt.Sprintf("%d prompts", s.UserTurns))
		label := rowLabel(s, selected, snippet, m.lineWidth()-24, m.lineWidth()-26)
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
	label := rowLabel(s, selected, snippet, m.lineWidth()-22-extra, m.lineWidth()-24-extra)
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
