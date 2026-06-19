package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

const (
	// unknownProjectID buckets sessions whose originating cwd could not be resolved
	// (a few Claude warmups; all Cursor sessions until the deferred cwd recovery lands).
	unknownProjectID = "unknown"

	// reindexWriteBatch is how many sessions the writer goroutine commits per transaction.
	reindexWriteBatch = 200

	// reindexVersion is the logic version stamped on every indexed row. Bump it whenever
	// the parse/flatten/derivation logic changes so the next reindex re-parses everything
	// even when files are unchanged. It is part of the freshness fingerprint.
	reindexVersion = 1
)

// CreateReindexCommand builds the `specstory reindex` command: a full, from-scratch
// rebuild of the restore index (~/.specstory/sessions.db) of every coding-agent session
// SpecStory can find, across all projects and providers. See docs/SESSIONS-DB.md.
func CreateReindexCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild the restore index of all known agent sessions",
		Long: `Rebuild the restore index used by 'specstory resume'.

'reindex' enumerates every session across all installed agents and projects and writes a
searchable index to ~/.specstory/sessions.db. It is incremental: a session whose native
file is unchanged since it was last indexed is skipped, so re-runs are fast. Use --force to
re-index everything regardless. The index is a derived cache: it is safe to delete.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			return runReindex(force)
		},
	}
	cmd.Flags().Bool("force", false, "re-index every session even if unchanged")
	return cmd
}

// reindexItem is one unit of work: a session to parse and index. size/mtime are the
// native file's stat, captured during the freshness check and reused (no re-stat).
type reindexItem struct {
	agent string
	prov  spi.Provider
	ref   spi.GlobalSessionRef
	size  int64
	mtime int64
}

func runReindex(force bool) error {
	start := time.Now()

	// Graceful Ctrl+C: stop feeding work, flush what's written, exit cleanly.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	dbPath, err := sessionindex.DefaultPath()
	if err != nil {
		return err
	}
	store, err := sessionindex.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening restore index: %w", err)
	}
	defer func() { _ = store.Close() }()

	registry := factory.GetRegistry()

	// ---- Phase 1: enumerate every provider concurrently ----
	fprintln(os.Stderr, "\n🔍  Scanning agents…")
	ids := registry.ListIDs()
	perProvider := make([][]spi.GlobalSessionRef, len(ids))
	provs := make([]spi.Provider, len(ids))
	var ewg sync.WaitGroup
	for i, id := range ids {
		prov, err := registry.Get(id)
		if err != nil {
			continue
		}
		provs[i] = prov
		ewg.Add(1)
		go func(i int, id string, prov spi.Provider) {
			defer ewg.Done()
			refs, err := prov.ListAllAgentChatSessions()
			if err != nil {
				slog.Warn("reindex: enumeration failed", "provider", id, "error", err)
			}
			perProvider[i] = refs
		}(i, id, prov)
	}
	ewg.Wait()

	// Existing fingerprints, so unchanged sessions can be skipped (the incremental path).
	fingerprints := map[string]sessionindex.Fingerprint{}
	if !force {
		if fps, err := store.Fingerprints(); err != nil {
			slog.Warn("reindex: could not load fingerprints, re-indexing all", "error", err)
		} else {
			fingerprints = fps
		}
	}

	// Dedup enumerated refs by (agent, session_id), keeping the freshest file. A resumed
	// session can span MULTIPLE native files sharing one id; they all map to one
	// (agent, session_id) row, so without dedup the duplicates ping-pong — each run, one
	// file's stat never matches the fingerprint the other file just stored, re-indexing
	// both forever. Skipping the freshest by mtime makes the fingerprint stable. Sessions
	// with no native id are dropped (unresumable, and would collide on the PK).
	best := map[string]reindexItem{}
	var order []string // first-seen order, for deterministic processing
	for i, id := range ids {
		if provs[i] == nil {
			continue
		}
		for _, ref := range perProvider[i] {
			if strings.TrimSpace(ref.SessionID) == "" {
				slog.Debug("reindex: skipping session with no id", "agent", id, "path", ref.NativePath)
				continue
			}
			size, mtime := statNative(ref.NativePath)
			item := reindexItem{agent: id, prov: provs[i], ref: ref, size: size, mtime: mtime}
			key := sessionindex.FingerprintKey(id, ref.SessionID)
			if cur, ok := best[key]; ok {
				if mtime > cur.mtime {
					best[key] = item
				}
				continue
			}
			best[key] = item
			order = append(order, key)
		}
	}

	// Apply the incremental fingerprint skip to the deduped set.
	var work []reindexItem
	totals := map[string]int{}        // to-do (changed) per agent — drives the progress bars
	foundPerAgent := map[string]int{} // distinct sessions per agent — for the "Found" line
	found := 0
	unchanged := 0
	for _, key := range order {
		item := best[key]
		found++
		foundPerAgent[item.agent]++
		if fp, ok := fingerprints[key]; ok &&
			fp.Size == item.size && fp.Mtime == item.mtime && fp.Version == reindexVersion {
			unchanged++
			continue // already indexed and unchanged
		}
		work = append(work, item)
		totals[item.agent]++
	}

	if found == 0 {
		fprintln(os.Stderr, "No agent sessions found to index.")
		return nil
	}
	fprintf(os.Stderr, "✓   Found %d sessions  ·  %s\n", found, summarizeCounts(ids, foundPerAgent))
	if len(work) == 0 {
		fprintf(os.Stderr, "✓   All %d sessions already up to date.  (%.1fs)\n", found, time.Since(start).Seconds())
		return nil
	}
	if unchanged > 0 {
		fprintf(os.Stderr, "    %d unchanged · indexing %d…\n\n", unchanged, len(work))
	} else {
		fprintln(os.Stderr, "")
	}

	// ---- Phase 2+3: parse (worker pool) → write (single goroutine) ----
	prog := newReindexProgress(ids, totals)
	prog.begin()

	indexedAt := start.UTC().Format(time.RFC3339)
	cache := &projectIDCache{m: map[string]projectIDName{}}

	sessionsCh := make(chan sessionindex.Session, 256)
	var writeErr error
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		writeErr = drainToStore(store, sessionsCh)
	}()

	workCh := make(chan reindexItem)
	workers := min(runtime.NumCPU(), 12)
	var wwg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wwg.Add(1)
		go func() {
			defer wwg.Done()
			for item := range workCh {
				sess := buildSession(item, cache, indexedAt)
				prog.observe(sess.ProjectID)
				sessionsCh <- sess
				prog.inc(item.agent)
			}
		}()
	}

	for _, item := range work {
		select {
		case <-ctx.Done():
			goto closing
		case workCh <- item:
		}
	}
closing:
	close(workCh)
	wwg.Wait()
	close(sessionsCh)
	<-writerDone
	prog.end()

	if writeErr != nil {
		return fmt.Errorf("writing restore index: %w", writeErr)
	}

	// ---- Summary (counts reflect the WHOLE index, not just this run's work) ----
	elapsed := time.Since(start)
	projects, _ := store.ProjectCount(unknownProjectID)
	unattributed, _ := store.UnattributedCount(unknownProjectID)
	printReindexSummary(ids, foundPerAgent, dbPath, elapsed, prog.totalDone(), unchanged, projects, unattributed)
	analytics.TrackEvent(analytics.EventReindexCompleted, analytics.Properties{
		"sessions":     len(work),
		"unchanged":    unchanged,
		"projects":     projects,
		"unattributed": unattributed,
		"duration_ms":  elapsed.Milliseconds(),
	})
	return nil
}

// buildSession turns one enumeration ref into a fully-built index row. Every session
// yields a row from its metadata; the body + turn counts + last-activity time are
// ENRICHED via a full parse only when the originating cwd lets us re-locate the
// session (so Cursor, whose cwd is unknown for now, lands metadata-only). Nothing is
// dropped.
func buildSession(item reindexItem, cache *projectIDCache, indexedAt string) sessionindex.Session {
	ref := item.ref
	projectID, projectName := cache.resolve(ref.OriginCwd)

	sess := sessionindex.Session{
		ProjectID:    projectID,
		ProjectName:  projectName,
		Agent:        item.agent,
		SessionID:    ref.SessionID,
		CreatedAt:    ref.CreatedAt,
		UpdatedAt:    ref.CreatedAt, // default; overwritten by last-turn timestamp below
		Slug:         ref.Slug,
		Name:         ref.Name,
		NativePath:   ref.NativePath,
		OriginCwd:    ref.OriginCwd,
		Size:         item.size, // captured during the freshness check (no re-stat)
		Mtime:        item.mtime,
		IndexVersion: reindexVersion,
		IndexedAt:    indexedAt,
	}

	if ref.OriginCwd == "" {
		return sess // cannot re-locate without a cwd → metadata-only
	}
	full, err := item.prov.GetAgentChatSession(ref.OriginCwd, ref.SessionID, false)
	if err != nil || full == nil || full.SessionData == nil {
		slog.Debug("reindex: full parse unavailable, indexing metadata only",
			"agent", item.agent, "session", ref.SessionID, "error", err)
		return sess
	}

	data := full.SessionData
	sess.Body = flattenBody(data)
	sess.UserTurns, sess.TotalTurns = countTurns(data)
	if ts := lastTimestamp(data); ts != "" {
		sess.UpdatedAt = ts
	}
	return sess
}

// drainToStore writes parsed sessions to the index in batched transactions.
func drainToStore(store *sessionindex.Store, ch <-chan sessionindex.Session) error {
	batch := make([]sessionindex.Session, 0, reindexWriteBatch)
	flush := func() error {
		if err := store.UpsertBatch(batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	for sess := range ch {
		batch = append(batch, sess)
		if len(batch) >= reindexWriteBatch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

// ---- session field derivation ----

// flattenBody renders SessionData to plain user/agent text for full-text indexing,
// reusing the reconstruction flattener (synthetic noise already stripped). No
// migration note is prepended — this is index content, not a resumed transcript.
func flattenBody(data *schema.SessionData) string {
	turns := spi.FlattenSessionData(data, "")
	var b strings.Builder
	for _, t := range turns {
		if t.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(t.Text)
	}
	return b.String()
}

// countTurns returns (user prompts, all messages) across the session's exchanges.
func countTurns(data *schema.SessionData) (userTurns, totalTurns int) {
	for _, ex := range data.Exchanges {
		for _, m := range ex.Messages {
			totalTurns++
			if m.Role == schema.RoleUser {
				userTurns++
			}
		}
	}
	return userTurns, totalTurns
}

// lastTimestamp returns the last non-empty message timestamp in the session, or "".
func lastTimestamp(data *schema.SessionData) string {
	last := ""
	for _, ex := range data.Exchanges {
		for _, m := range ex.Messages {
			if m.Timestamp != "" {
				last = m.Timestamp
			}
		}
	}
	return last
}

// statNative returns the native file's size (bytes) and mtime (epoch ms), or zeros.
func statNative(path string) (size, mtimeMs int64) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	return info.Size(), info.ModTime().UnixMilli()
}

// ---- project identity cache (cwd -> project id/name) ----

type projectIDName struct{ id, name string }

// projectIDCache memoizes ComputeProjectID by cwd, since many sessions share a cwd and
// each resolution walks the filesystem to the git root.
type projectIDCache struct {
	mu sync.Mutex
	m  map[string]projectIDName
}

func (c *projectIDCache) resolve(cwd string) (string, string) {
	if strings.TrimSpace(cwd) == "" {
		return unknownProjectID, ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.m[cwd]; ok {
		return v.id, v.name
	}
	id, name, err := utils.ComputeProjectID(cwd)
	if err != nil || id == "" {
		id, name = unknownProjectID, ""
	}
	c.m[cwd] = projectIDName{id, name}
	return id, name
}

// ---- helpers ----

// summarizeCounts renders "claude 683 · codex 70 · …" in registry order, omitting zeros.
func summarizeCounts(ids []string, totals map[string]int) string {
	var parts []string
	for _, id := range ids {
		if totals[id] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", id, totals[id]))
		}
	}
	return strings.Join(parts, " · ")
}

// ---- live progress (per-agent bars) ----

// reindexProgress renders live per-agent progress. Workers bump atomic per-agent
// counters and observe project ids; a single render goroutine redraws the block in
// place on a TTY (ANSI cursor-up), or prints periodic plain lines otherwise. No worker
// draws directly. See docs/SESSIONS-DB.md "Reindex Progress UX".
type reindexProgress struct {
	agents []string          // agents with >0 sessions, in registry order
	totals map[string]int    // per-agent session totals
	done   map[string]*int64 // per-agent completed counters (atomic)
	tty    bool

	mu       sync.Mutex
	projects map[string]struct{} // distinct non-unknown project ids seen

	stopCh     chan struct{}
	doneCh     chan struct{}
	linesDrawn int
}

func newReindexProgress(ids []string, totals map[string]int) *reindexProgress {
	done := make(map[string]*int64)
	var agents []string
	for _, id := range ids {
		if totals[id] > 0 {
			agents = append(agents, id)
			done[id] = new(int64)
		}
	}
	return &reindexProgress{
		agents:   agents,
		totals:   totals,
		done:     done,
		tty:      isTerminal(os.Stderr),
		projects: map[string]struct{}{},
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

func (p *reindexProgress) begin() {
	go p.loop()
}

func (p *reindexProgress) end() {
	close(p.stopCh)
	<-p.doneCh
	p.draw(true) // final frame at 100%
}

func (p *reindexProgress) inc(agent string) {
	if c := p.done[agent]; c != nil {
		atomic.AddInt64(c, 1)
	}
}

func (p *reindexProgress) observe(projectID string) {
	// Unknown-project sessions are not counted toward the live "projects" total (the
	// final unattributed count comes from the DB at summary time).
	if projectID == unknownProjectID {
		return
	}
	p.mu.Lock()
	p.projects[projectID] = struct{}{}
	p.mu.Unlock()
}

func (p *reindexProgress) projectCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.projects)
}

func (p *reindexProgress) totalDone() int {
	var n int64
	for _, c := range p.done {
		n += atomic.LoadInt64(c)
	}
	return int(n)
}

func (p *reindexProgress) grandTotal() int {
	n := 0
	for _, a := range p.agents {
		n += p.totals[a]
	}
	return n
}

func (p *reindexProgress) loop() {
	defer close(p.doneCh)
	interval := 100 * time.Millisecond
	if !p.tty {
		interval = 1500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.draw(false)
		}
	}
}

func (p *reindexProgress) draw(final bool) {
	if !p.tty {
		if !final { // the final state is covered by the summary line
			fprintf(os.Stderr, "indexed %d/%d…\n", p.totalDone(), p.grandTotal())
		}
		return
	}

	lines := p.frameLines()
	var b strings.Builder
	if p.linesDrawn > 0 {
		fmt.Fprintf(&b, "\033[%dA", p.linesDrawn) // move cursor up to the block's top
	}
	for _, ln := range lines {
		b.WriteString("\033[2K") // clear the line, then redraw it
		b.WriteString(ln)
		b.WriteString("\n")
	}
	fprint(os.Stderr, b.String())
	p.linesDrawn = len(lines)
}

func (p *reindexProgress) frameLines() []string {
	doneN, totalN := p.totalDone(), p.grandTotal()
	lines := []string{
		fmt.Sprintf("Indexing  %d/%d · %d projects", doneN, totalN, p.projectCount()),
		"",
	}

	nameW := 0
	for _, a := range p.agents {
		if len(a) > nameW {
			nameW = len(a)
		}
	}
	for _, a := range p.agents {
		d := atomic.LoadInt64(p.done[a])
		t := int64(p.totals[a])
		check := ""
		if d >= t {
			check = " ✓"
		}
		lines = append(lines, fmt.Sprintf("  %-*s %s %d/%d%s", nameW, a, progressBar(d, t), d, t, check))
	}
	return lines
}

// progressBar renders a fixed-width unicode bar, e.g. ▕███████░░░░░░░▏.
func progressBar(done, total int64) string {
	const w = 14
	filled := 0
	if total > 0 {
		filled = int(done * int64(w) / total)
	}
	if filled > w {
		filled = w
	}
	return "▕" + strings.Repeat("█", filled) + strings.Repeat("░", w-filled) + "▏"
}

// isTerminal reports whether f is a character device (an interactive terminal),
// using only the standard library (no isatty dependency).
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// ---- summary ----

func printReindexSummary(ids []string, perAgent map[string]int, dbPath string, elapsed time.Duration, indexed, unchanged, projects, unattributed int) {
	w := os.Stderr
	if unchanged > 0 {
		fprintf(w, "\n✓   Indexed %d sessions (%d unchanged) into %s  (%.1fs)\n\n", indexed, unchanged, prettyPath(dbPath), elapsed.Seconds())
	} else {
		fprintf(w, "\n✓   Indexed %d sessions into %s  (%.1fs)\n\n", indexed, prettyPath(dbPath), elapsed.Seconds())
	}
	fprintf(w, "      %s\n\n", summarizeCounts(ids, perAgent))
	if unattributed > 0 {
		fprintf(w, "      %d projects  ·  %d unattributed\n", projects, unattributed)
	} else {
		fprintf(w, "      %d projects\n", projects)
	}
	fprintf(w, "\n")
}

// prettyPath abbreviates the user's home directory to ~ for display.
func prettyPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
