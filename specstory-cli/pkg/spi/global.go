package spi

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
)

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

// ProgressEnumerator is an OPTIONAL Provider capability: enumerate all sessions while reporting
// scan progress into r (which may be nil — ScanReporter is nil-safe, so report unconditionally).
// Providers that don't implement it are enumerated via ListAllAgentChatSessions with no live
// feedback. reindex uses this to render a per-agent "Scanning agents…" display.
type ProgressEnumerator interface {
	ListAllAgentChatSessionsProgress(r *ScanReporter) ([]GlobalSessionRef, error)
}

// ScanSessionsInParallel walks root for *.jsonl session files and scans each one concurrently
// with scan, returning the collected refs. It is the shared engine behind each provider's
// ListAllAgentChatSessionsProgress: providers differ only in where their sessions live (root)
// and how a single file's header maps to a GlobalSessionRef (scan).
//
// scan returns (ref, nil) to include a session, (nil, nil) to skip a non-session file
// (warmup-only / empty / sidechain-only), or (nil, err) to log-and-skip a malformed file.
// Files are independent and the per-file read+parse is CPU-bound, so scanning fans out across
// CPUs; output order is unspecified (reindex dedups and sorts later). r counts sessions found
// (not files scanned) and is nil-safe. label names the provider in scan-failure logs.
func ScanSessionsInParallel(root, label string, r *ScanReporter, scan func(path string) (*GlobalSessionRef, error)) ([]GlobalSessionRef, error) {
	// Phase 1: collect file paths (dirents only — no opens).
	var paths []string
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than abort the whole sweep
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walking %s sessions: %w", label, walkErr)
	}

	// Phase 2: scan headers in parallel. min(NumCPU, 12) is always ≥ 1, so no lower-bound guard.
	workers := min(runtime.NumCPU(), 12)
	pathCh := make(chan string, workers)
	refs := make([]GlobalSessionRef, 0, len(paths))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range pathCh {
				ref, scanErr := safeScan(scan, path)
				if scanErr != nil {
					slog.Warn("reindex: failed to scan session", "agent", label, "path", path, "error", scanErr)
					continue
				}
				if ref == nil {
					continue // not a session (warmup / empty / sidechain)
				}
				mu.Lock()
				refs = append(refs, *ref)
				mu.Unlock()
				r.Add(1) // count sessions found, not files scanned
			}
		}()
	}
	for _, path := range paths {
		pathCh <- path
	}
	close(pathCh)
	wg.Wait()
	return refs, nil
}

// safeScan runs scan(path), converting a panic into an error so one malformed session file is
// logged and skipped (via the caller's normal error path) rather than crashing the whole sweep.
func safeScan(scan func(string) (*GlobalSessionRef, error), path string) (ref *GlobalSessionRef, err error) {
	defer func() {
		if r := recover(); r != nil {
			ref, err = nil, fmt.Errorf("panic scanning session: %v", r)
		}
	}()
	return scan(path)
}
