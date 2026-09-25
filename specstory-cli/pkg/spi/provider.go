package spi

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// CheckResult contains the result of a provider check operation
type CheckResult struct {
	Success      bool   // Whether the check succeeded
	Version      string // Version of the provider (empty on failure)
	Location     string // File path/location of the provider executable or IDE storage
	ErrorType    string // CheckError* classification on failure; empty on success
	ErrorMessage string // Error message if check failed (empty on success)
}

// ProgressCallback reports progress during session parsing/processing
// current: number of items processed so far (1-based)
// total: total number of items to process
type ProgressCallback func(current, total int)

// AgentChatSession represents a chat session from an AI coding agent
type AgentChatSession struct {
	SessionID   string              // Stable and unique identifier for the session (often a UUID)
	CreatedAt   string              // Stable ISO 8601 timestamp when session was created
	Slug        string              // Stable human-readable but file name safe slug, often derived from first user message
	SessionData *schema.SessionData // Structured session data in unified format
	RawData     string              // Raw session data (e.g., JSON blobs for Cursor CLI, JSONL for Claude Code and Codex CLI, etc.)
}

// SessionDataJSON marshals the session's normalized SessionData to a JSON string for
// cloud sync. SessionData is the canonical cloud-resume representation (the cloud
// stores and serves it verbatim), so it is pushed alongside the existing markdown and
// rawData. Returns "" (and logs) when there is no SessionData or marshaling fails, so
// callers can pass the result straight through — an empty payload simply means "no
// cloud-resumable blob for this session" and the server leaves session_data_size NULL.
func (s *AgentChatSession) SessionDataJSON() string {
	if s == nil || s.SessionData == nil {
		return ""
	}
	data, err := json.Marshal(s.SessionData)
	if err != nil {
		slog.Warn("Failed to marshal SessionData for cloud sync", "sessionId", s.SessionID, "error", err)
		return ""
	}
	return string(data)
}

// SessionMetadata contains lightweight metadata about a session without full content
// Used by ListAgentChatSessions for efficient session listing
type SessionMetadata struct {
	SessionID string `json:"session_id" csv:"session_id"` // Stable and unique identifier for the session
	CreatedAt string `json:"created_at" csv:"created_at"` // Stable ISO 8601 timestamp when session was created
	Slug      string `json:"slug" csv:"slug"`             // Stable human-readable session name/slug
	Name      string `json:"name" csv:"name"`             // Human-readable description of the session (may be empty if not available)
}

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

	// Fingerprint is the provider's own freshness token for the session, for a
	// store where NativePath is shared by every session (one SQLite database):
	// that file's size and mtime move whenever any session changes, so a
	// fingerprint taken from it would re-read every session on each reindex.
	// Nil means reindex fingerprints NativePath itself.
	Fingerprint *SessionFingerprint
}

// SessionFingerprint is a pair of values that change whenever a session's
// content does. Reindex compares them to the pair it stored when it last
// indexed the session; their meaning is the provider's (a row count and the
// newest write time, say), not a file's size and mtime.
type SessionFingerprint struct {
	Size  int64
	Mtime int64
}

// ScanReporter accumulates a provider's enumeration progress (sessions found) so the CLI can
// render a live "scanning" display. It is safe for concurrent use and safe to call on a nil
// receiver (a no-op), so providers can report unconditionally.
type ScanReporter struct {
	found atomic.Int64
}

// Add records that n more sessions have been found. Files that yield no session (warmup-only or
// sidechain-only transcripts) are deliberately not counted, so the running total reflects real
// sessions rather than raw files.
func (r *ScanReporter) Add(n int) {
	if r != nil {
		r.found.Add(int64(n))
	}
}

// Found returns the number of sessions found so far.
func (r *ScanReporter) Found() int64 {
	if r == nil {
		return 0
	}
	return r.found.Load()
}

// Provider defines the interface that all agent coding tool providers must implement.
// PathSessionReader and ProgressEnumerator below are optional capabilities.
type Provider interface {
	// Name returns the human-readable name of the provider (e.g., "Claude Code", "Cursor CLI", "Codex CLI")
	Name() string

	// Check verifies if the provider is properly installed and returns version info
	// customCommand: empty string = use detected/default binary path, non-empty = use this specific command/path
	Check(customCommand string) CheckResult

	// DetectAgent checks if the provider's agent has been used in the given project path
	// projectPath: Agent's working directory
	// helpOutput: if true AND no activity is found, the provider should output helpful guidance
	// Returns true if the agent has created artifacts/sessions in the specified path
	DetectAgent(projectPath string, helpOutput bool) bool

	// GetAgentChatSession retrieves a single chat session by ID for the given project path
	// projectPath: Agent's working directory
	// sessionID: specific session identifier to retrieve (always provided, never empty)
	// debugRaw: if true, provider should write provider-specific raw debug files to .specstory/debug/<sessionID>/
	//           (e.g., numbered JSON files). The unified session-data.json is written centrally by the CLI.
	// Returns nil if the session is not found, error for actual errors
	GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*AgentChatSession, error)

	// GetAgentChatSessions retrieves all chat sessions for the given project path
	// projectPath: Agent's working directory
	// debugRaw: if true, provider should write provider-specific raw debug files to .specstory/debug/<sessionID>/
	//           (e.g., numbered JSON files). The unified session-data.json is written centrally by the CLI.
	// progress: optional callback for reporting progress during parsing (nil = no progress reporting)
	// Returns a slice of AgentChatSession structs containing session data
	GetAgentChatSessions(projectPath string, debugRaw bool, progress ProgressCallback) ([]AgentChatSession, error)

	// ListAgentChatSessions retrieves lightweight metadata for all sessions without full parsing
	// This should be faster than GetAgentChatSessions as it only needs to return SessionMetadata and not the full AgentChatSession
	// projectPath: Agent's working directory
	// Returns a slice of SessionMetadata (ordering is provider-defined; consumers will sort if needed)
	ListAgentChatSessions(projectPath string) ([]SessionMetadata, error)

	// ExecAgentAndWatch executes the agent in interactive mode and watches for session updates
	// Blocks until the agent exits, calling sessionCallback for each new/updated session
	// projectPath: Agent's working directory
	// customCommand: empty string = use detected/default binary with default args, non-empty = use this specific command/path
	// resumeSessionID: empty string = start new session, non-empty = resume this specific session ID
	// debugRaw: if true, provider should write provider-specific raw debug files to .specstory/debug/<sessionID>/
	//           (e.g., numbered JSON files). The unified session-data.json is written centrally by the CLI.
	// sessionCallback is called with each session update. Deliver updates in order,
	// contain callback panics, and finish all callbacks before returning.
	// The implementation should handle its own file watching and session tracking
	ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*AgentChatSession)) error

	// WatchAgent watches for agent activity and calls the callback with an updated AgentChatSession
	// Does NOT execute the agent - only watches for new activity
	// Runs until error or context cancellation
	// ctx: Context for cancellation and timeout control
	// projectPath: Agent's working directory
	// debugRaw: if true, provider should write provider-specific raw debug files to .specstory/debug/<sessionID>/
	//           (e.g., numbered JSON files). The unified session-data.json is written centrally by the CLI.
	// sessionCallback: called with AgentChatSession on each update. Deliver updates
	// in order, contain callback panics, and finish all callbacks before returning.
	// The implementation should handle its own file watching and session tracking
	WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*AgentChatSession)) error

	// ReconstructSession rebuilds the provider's native session format from the
	// neutral SessionData so the agent can resume the conversation (the reverse of
	// the parse/generate path). It is a pure transform: it returns the native file
	// bytes, a freshly minted native-format session ID, and a suggested filename,
	// but does NOT touch the filesystem — writing into the live agent store is the
	// caller's responsibility.
	//
	// Every provider carries this responsibility; providers that do not yet have a
	// native serializer return ErrReconstructionUnsupported.
	// See docs/SESSION-PORTABILITY.md for the design.
	ReconstructSession(data *schema.SessionData, opts ReconstructOptions) (*ReconstructedSession, error)

	// NativeSessionPath resolves the absolute path where a reconstructed session
	// file (with the given base filename, from ReconstructedSession.Filename)
	// belongs in this provider's native store for the given project, WITHOUT
	// requiring the directory to exist — the caller creates it and writes the file.
	// Providers without a native serializer return ErrReconstructionUnsupported.
	NativeSessionPath(projectPath string, filename string) (string, error)

	// SupportsReconstruction reports whether this provider has a native
	// serializer — i.e. whether it can be a cross-agent (or cloud) resume
	// target. It must agree with ReconstructSession/NativeSessionPath returning
	// ErrReconstructionUnsupported, and must be a pure capability answer with no
	// filesystem access: callers invoke it just to build target lists (some
	// NativeSessionPath implementations prepare directories, so probing that is
	// not a substitute).
	SupportsReconstruction() bool

	// ListAllAgentChatSessions enumerates every session in this provider's native
	// store, regardless of project — the inverse of the project-scoped
	// ListAgentChatSessions. Each returned ref carries the originating cwd (read from
	// inside the session) so the caller can resolve project identity; the project is
	// discovered, not supplied. Lightweight: metadata only, no full SessionData parse.
	// Used by `specstory reindex` to (re)build the restore index. See docs/SESSIONS-DB.md.
	ListAllAgentChatSessions() ([]GlobalSessionRef, error)
}

// Optional provider capabilities. A provider may implement either, both, or
// neither; callers fall back to the required Provider methods when absent.

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

// ProgressEnumerator is an OPTIONAL Provider capability: enumerate all sessions while reporting
// scan progress into r (which may be nil — ScanReporter is nil-safe, so report unconditionally).
// Providers that don't implement it are enumerated via ListAllAgentChatSessions with no live
// feedback. reindex uses this to render a per-agent "Scanning agents…" display.
type ProgressEnumerator interface {
	ListAllAgentChatSessionsProgress(r *ScanReporter) ([]GlobalSessionRef, error)
}
