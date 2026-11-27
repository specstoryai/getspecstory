package monitor

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/claudecode"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/codexcli"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/cursorcli"
)

// StorageRoots holds the agent-side session storage directories the monitor
// watches. Injectable (rather than hardcoded ~ paths) so tests can point each
// root at a t.TempDir() fixture and so the hidden --storage-root flag can
// redirect them for smoke testing.
type StorageRoots struct {
	Claude string // ~/.claude/projects
	Codex  string // ~/.codex/sessions (honors CODEX_HOME)
	Cursor string // ~/.cursor/chats
	// CopilotIDE maps a VS Code variant ID (copilotide.Variant.ID, e.g.
	// "copilotide" for stock VS Code) to that variant's workspaceStorage
	// directory. Per-variant because each VS Code distribution keeps its own
	// storage tree; variants absent from this machine have no entry.
	CopilotIDE map[string]string
}

// DefaultStorageRoots returns the real storage roots the agents write to.
// Paths are returned whether or not they exist; the monitor command skips
// missing ones at startup.
func DefaultStorageRoots() (StorageRoots, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return StorageRoots{}, err
	}

	// Codex honors CODEX_HOME; reuse the provider's own root resolution so the
	// monitor watches exactly where the codexcli provider reads from.
	codexRoot, err := codexcli.SessionsRoot()
	if err != nil {
		return StorageRoots{}, err
	}

	cursorRoot, err := cursorcli.GetCursorChatsDir()
	if err != nil {
		return StorageRoots{}, err
	}

	// Copilot chat storage is per VS Code distribution. copilotStorageRoots
	// resolves the per-variant workspaceStorage directories, but only when the
	// build is compiled with the copilotide_monitor tag (which pulls in the
	// copilotide provider); the default build returns nil here and the monitor
	// simply watches no Copilot IDE storage.
	copilotRoots := copilotStorageRoots()

	return StorageRoots{
		// claudecode only exports an existence-checking accessor for this path
		// (GetClaudeCodeProjectsDir), but we need the path even when it does
		// not exist yet, so build it the same way that accessor does.
		Claude:     filepath.Join(homeDir, ".claude", "projects"),
		Codex:      codexRoot,
		Cursor:     cursorRoot,
		CopilotIDE: copilotRoots,
	}, nil
}

// repoEntry caches the per-repo lookup keys used by each provider's resolver.
type repoEntry struct {
	// path is the repo root exactly as discovered (what the supervisor spawns
	// watch children in).
	path string
	// canonical is the symlink-resolved path used for cwd prefix comparisons,
	// because agents may record either form (macOS /tmp vs /private/tmp etc.).
	canonical string
	// claudeEncoded is Claude Code's project-directory name for this repo.
	claudeEncoded string
}

// Resolver maps a storage-root filesystem event to the discovered repository
// the activity belongs to.
type Resolver struct {
	roots StorageRoots
	// repos is sorted by descending canonical-path length so prefix scans
	// naturally implement longest-prefix matching (a session cwd may be a
	// subdirectory of one repo that is itself inside another discovered repo).
	repos []repoEntry
	// cursorHashToRepo inverts Cursor's one-way md5(project path) naming by
	// hashing every discovered repo up front.
	cursorHashToRepo map[string]string
}

// NewResolver builds a Resolver for the given discovered repo roots and
// storage roots. All per-repo derived keys (Claude encoding, Cursor hash,
// canonical path) are computed once here rather than per event.
func NewResolver(repos []string, roots StorageRoots) *Resolver {
	r := &Resolver{
		roots:            roots,
		cursorHashToRepo: make(map[string]string, len(repos)),
	}

	for _, repo := range repos {
		repo = filepath.Clean(repo)

		// Fall back to the raw path when symlink resolution fails (repo
		// deleted mid-run); comparisons then just use the discovered form.
		canonical, err := filepath.EvalSymlinks(repo)
		if err != nil {
			canonical = repo
		}

		r.repos = append(r.repos, repoEntry{
			path:          repo,
			canonical:     canonical,
			claudeEncoded: claudecode.EncodeProjectDirName(repo),
		})

		if hash := cursorcli.ProjectHash(repo); hash != "" {
			r.cursorHashToRepo[hash] = repo
		}
	}

	sort.Slice(r.repos, func(i, j int) bool {
		return len(r.repos[i].canonical) > len(r.repos[j].canonical)
	})

	return r
}

// Resolve maps a filesystem event path from one of the storage roots to the
// discovered repo the activity belongs to. Returns ok=false when the event is
// under no known root, belongs to no discovered repo, or (for Codex) the
// session metadata isn't readable yet — Codex creates the .jsonl before
// writing its session_meta line, and the follow-up Write event retries.
//
// VS Code Copilot IDE events do not flow through here: workspaceStorage holds
// thousands of workspace directories, so Copilot uses a dedicated selective
// watcher (WatchCopilotWorkspaceStorage) that pre-maps the handful of
// repo-relevant chatSessions directories instead of watching everything.
//
// Cursor IDE (pkg/providers/cursoride) has no resolver family here on
// purpose: its chat activity lands in SQLite databases — every conversation
// is a key-value row in the single global globalStorage/state.vscdb, with
// per-workspace state.vscdb files mapping composer IDs to workspaces — so
// attributing new activity to a project requires opening and polling those
// databases, not the cheap event-path mapping used below. Deferred until
// there is demand for it.
func (r *Resolver) Resolve(eventPath string) (string, bool) {
	if rel, ok := pathUnder(r.roots.Claude, eventPath); ok {
		return r.resolveClaude(rel)
	}
	if _, ok := pathUnder(r.roots.Codex, eventPath); ok {
		return r.resolveCodex(eventPath)
	}
	if rel, ok := pathUnder(r.roots.Cursor, eventPath); ok {
		return r.resolveCursor(rel)
	}
	return "", false
}

// resolveClaude maps a path relative to the Claude projects root to a repo.
// The first component is Claude Code's encoded project directory name. The
// encoding is lossy and one-way, so instead of decoding we compare against the
// pre-encoded names of every discovered repo: an exact match is a session in
// the repo root, and an "<encoded>-" prefix match is a session whose cwd was a
// subdirectory of the repo (subdir separators encode to dashes). repos is
// length-sorted so the first hit is the longest — i.e. most specific — match.
func (r *Resolver) resolveClaude(rel string) (string, bool) {
	entryName := firstComponent(rel)
	for _, repo := range r.repos {
		if entryName == repo.claudeEncoded || strings.HasPrefix(entryName, repo.claudeEncoded+"-") {
			return repo.path, true
		}
	}
	return "", false
}

// resolveCodex maps a Codex session file event to a repo by reading the cwd
// recorded in the session's metadata header, then longest-prefix matching it
// against the discovered repos.
func (r *Resolver) resolveCodex(eventPath string) (string, bool) {
	// Only session .jsonl files carry metadata; directory-creation events for
	// the YYYY/MM/DD chain (and any stray files) are not activity by themselves.
	if !strings.HasSuffix(eventPath, ".jsonl") {
		return "", false
	}
	cwd, err := codexcli.SessionOriginCwd(eventPath)
	if err != nil || cwd == "" {
		return "", false
	}
	return r.repoContaining(cwd)
}

// resolveCursor maps a path relative to the Cursor chats root to a repo. The
// first component is md5(project path); the hash is one-way, so lookup uses
// the table of pre-hashed discovered repos.
func (r *Resolver) resolveCursor(rel string) (string, bool) {
	repo, ok := r.cursorHashToRepo[firstComponent(rel)]
	return repo, ok
}

// repoContaining returns the discovered repo whose tree contains p, preferring
// the longest (most deeply nested) match. p may be the repo root itself or any
// subdirectory of it — agents record the session cwd, which is often a subdir.
func (r *Resolver) repoContaining(p string) (string, bool) {
	candidate := filepath.Clean(p)
	// Compare in canonical (symlink-resolved) space when possible so a cwd
	// recorded through a symlinked path still matches the discovered repo.
	if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
		candidate = resolved
	}

	for _, repo := range r.repos {
		if candidate == repo.canonical || strings.HasPrefix(candidate, repo.canonical+string(filepath.Separator)) {
			return repo.path, true
		}
		// Belt and suspenders: also accept the raw discovered form, in case
		// canonicalization diverged between discovery time and event time.
		if candidate == repo.path || strings.HasPrefix(candidate, repo.path+string(filepath.Separator)) {
			return repo.path, true
		}
	}
	return "", false
}

// pathUnder reports whether p is strictly inside root, returning the relative
// remainder. The root itself is not "under" (an event for the root directory
// carries no entry name to resolve).
func pathUnder(root, p string) (string, bool) {
	if root == "" {
		return "", false
	}
	prefix := filepath.Clean(root) + string(filepath.Separator)
	if !strings.HasPrefix(p, prefix) {
		return "", false
	}
	return p[len(prefix):], true
}

// firstComponent returns the first path component of a relative path.
func firstComponent(rel string) string {
	if i := strings.IndexByte(rel, filepath.Separator); i >= 0 {
		return rel[:i]
	}
	return rel
}
