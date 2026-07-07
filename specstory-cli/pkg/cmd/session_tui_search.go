package cmd

import (
	"context"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
)

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
//   - Bare words become independent prefix terms, AND-ed (matched anywhere, any order) —
//     a loose search:  thank you      -> thank* you*
//   - A double-quoted span becomes a phrase: tokens adjacent and in order —
//     an exact phrase:  "thank you"    -> "thank you"
//   - A still-open quote (the user is mid-typing the phrase) keeps the final token a prefix,
//     so results keep updating live:  "thank yo    -> thank + yo*
//
// A single typed word that contains punctuation (e.g. a filename, "poem.txt") is itself an
// adjacency phrase. The index's unicode61 tokenizer splits on every non-alphanumeric run, so
// "poem.txt" is stored as the two adjacent tokens poem, txt — never the fused "poemtxt". We
// must tokenize the query the same way, or the term can't match what was indexed. So a bare
// "poem.txt" becomes the phrase poem + txt* (adjacent, prefix the last token for type-ahead);
// quoted, it is the committed phrase "poem txt" (adjacent, no prefix).
//
// Only letters and digits survive tokenization, so no FTS5 syntax character from the raw
// input can reach MATCH — every query this builds is safe to execute.
func ftsQuery(input string) string {
	// Splitting on '"' yields alternating runs: even indexes are outside quotes, odd indexes
	// are inside. An odd-indexed run that is also the LAST run means the closing quote hasn't
	// been typed yet (an open phrase), so its final token stays a prefix for live type-ahead.
	segs := strings.Split(input, `"`)
	var parts []string
	for i, seg := range segs {
		switch {
		case i%2 == 0: // outside quotes: each word is its own loose, prefix-terminated term
			for _, f := range strings.Fields(seg) {
				if toks := fieldTokens(f); len(toks) > 0 {
					parts = append(parts, strings.Join(toks, " + ")+"*")
				}
			}
		case i == len(segs)-1: // open phrase (still typing): adjacency across all tokens + prefix last
			if toks := segmentTokens(seg); len(toks) > 0 {
				parts = append(parts, strings.Join(toks, " + ")+"*")
			}
		default: // closed phrase: exact adjacency, no prefix (a committed phrase)
			if toks := segmentTokens(seg); len(toks) > 0 {
				parts = append(parts, `"`+strings.Join(toks, " ")+`"`)
			}
		}
	}
	return strings.Join(parts, " ")
}

// fieldTokens splits one whitespace-delimited field into its FTS5 tokens the way the index's
// default unicode61 tokenizer does: every run of non-alphanumeric characters is a token
// boundary. So "poem.txt" -> [poem, txt] and "max-cpu!" -> [max, cpu]. Mirroring the index
// tokenizer here is the whole point — previously punctuation was stripped and the pieces
// fused into one token ("poemtxt") that the index, holding poem and txt separately, never had.
func fieldTokens(field string) []string {
	var tokens []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			tokens = append(tokens, b.String())
			b.Reset()
		}
	}
	for _, r := range field {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

// segmentTokens tokenizes a whole quoted segment into one flat, ordered token list — both the
// whitespace between words and any punctuation within them are token boundaries (unicode61),
// so the phrase matches the adjacency the index actually stored.
func segmentTokens(seg string) []string {
	var tokens []string
	for _, f := range strings.Fields(seg) {
		tokens = append(tokens, fieldTokens(f)...)
	}
	return tokens
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
		query, projectID = m.globalQuery, m.globalScopeID // "" = all projects, else scoped
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

	// Cache the raw results so a subsequent agent cycle re-filters them in memory (no re-query).
	m.searchRaw = sessions
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
