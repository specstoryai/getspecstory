package spi

// GlobalSessionRef is a lightweight, project-discovering reference to a single
// native session, returned by Provider.ListAllAgentChatSessions.
//
// Unlike the project-scoped ListAgentChatSessions(projectPath), the project is NOT
// an input here: each ref carries the originating working directory (read from inside
// the session) so the caller — the `specstory reindex` command — can resolve project
// identity with utils.ComputeProjectID. The project is discovered, not supplied.
//
// It is intentionally metadata-only (no full SessionData parse); reindex re-fetches
// full data per ref via GetAgentChatSession(OriginCwd, SessionID) when it needs the
// conversation body. See docs/SESSIONS-DB.md.
type GlobalSessionRef struct {
	SessionID  string // native session id (uuid)
	CreatedAt  string // ISO 8601 creation timestamp (first turn), may be empty
	Slug       string // filename-safe slug derived from the first user message
	Name       string // human-readable description (may be empty)
	NativePath string // absolute path the provider opens to read this session
	OriginCwd  string // working directory the session was launched from (-> project_id)
}

// PathSessionReader is an OPTIONAL capability a Provider may implement: parse a single
// session directly from its already-known native file path, skipping the by-id discovery
// search that GetAgentChatSession performs.
//
// Why: some providers locate a session by scanning their native store. Codex's by-id
// lookup walks the entire ~/.codex/sessions tree on every call, so resolving N sessions
// that way is O(N²). reindex already holds each session's exact file path
// (GlobalSessionRef.NativePath) from enumeration, so a path-keyed parse turns that back
// into O(N). reindex prefers this when a provider implements it and the ref carries a
// NativePath, and falls back to GetAgentChatSession otherwise.
//
// originCwd is the session's originating working directory (GlobalSessionRef.OriginCwd),
// passed through as the workspace root for path normalization — matching what
// GetAgentChatSession receives as projectPath.
type PathSessionReader interface {
	GetAgentChatSessionByPath(nativePath string, originCwd string, debugRaw bool) (*AgentChatSession, error)
}
