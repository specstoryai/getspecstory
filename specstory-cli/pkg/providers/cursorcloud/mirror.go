package cursorcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// streamRetention is how long the API replays a finished run's stream, as
// advertised by x-cursor-stream-retention-seconds: 86400 on every capture.
const streamRetention = 24 * time.Hour

// mirrorGitignore keeps the project-local mirror out of version control. The
// mirror holds raw API transcripts, which are never redacted (redaction
// applies to markdown and cloud sync), and .specstory/ is usually committed.
const mirrorGitignore = "# Raw Cursor Cloud Agent transcripts mirrored by SpecStory; unredacted, so never committed.\n*\n"

// mirror is the local copy of the API that every read path uses as the
// provider's native store.
type mirror struct {
	root string
}

func (m mirror) agentDir(agentID string) string { return filepath.Join(m.root, "agents", agentID) }

func (m mirror) agentFile(agentID string) string {
	return filepath.Join(m.agentDir(agentID), "agent.json")
}

func (m mirror) runsDir(agentID string) string { return filepath.Join(m.agentDir(agentID), "runs") }

func (m mirror) runFile(agentID, runID string) string {
	return filepath.Join(m.runsDir(agentID), runID+".json")
}

func (m mirror) eventsFile(agentID, runID string) string {
	return filepath.Join(m.runsDir(agentID), runID+".events.jsonl")
}

// agentSnapshot is one consistent read of an agent's mirror directory: every
// conversion, RawData and debug export of a session comes from one snapshot.
type agentSnapshot struct {
	dir             string
	agentRaw        json.RawMessage
	agent           apiAgent
	conversationRaw json.RawMessage
	conversation    *v0Conversation
	usageRaw        json.RawMessage
	runs            []runSnapshot
}

type runSnapshot struct {
	runRaw json.RawMessage
	run    apiRun
	events []sseEvent
}

// loadAgentSnapshot reads an agent directory. A directory without agent.json
// is not an agent yet (a refresh writes agent.json last) and yields nil.
func loadAgentSnapshot(dir string) (*agentSnapshot, error) {
	agentRaw, err := os.ReadFile(filepath.Join(dir, "agent.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	record, err := decodeAgent(agentRaw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, "agent.json"), err)
	}
	snap := &agentSnapshot{dir: dir, agentRaw: record.raw, agent: record.agent}

	if raw, err := os.ReadFile(filepath.Join(dir, "conversation.json")); err == nil {
		var conv v0Conversation
		if jsonErr := json.Unmarshal(raw, &conv); jsonErr != nil {
			slog.Warn("Ignoring undecodable conversation", "path", filepath.Join(dir, "conversation.json"), "error", jsonErr)
		} else {
			snap.conversationRaw = raw
			snap.conversation = &conv
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "usage.json")); err == nil && json.Valid(raw) {
		snap.usageRaw = raw
	}

	runsDir := filepath.Join(dir, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(runsDir, name))
		if err != nil {
			return nil, err
		}
		record, err := decodeRun(raw)
		if err != nil {
			slog.Warn("Skipping undecodable run", "path", filepath.Join(runsDir, name), "error", err)
			continue
		}
		events, err := readEventsFile(filepath.Join(runsDir, record.run.ID+".events.jsonl"))
		if err != nil {
			return nil, err
		}
		snap.runs = append(snap.runs, runSnapshot{runRaw: record.raw, run: record.run, events: events})
	}
	// One run is one exchange in creation order. The API lists runs newest
	// first; the id breaks ties so the order never depends on directory order.
	sort.SliceStable(snap.runs, func(i, j int) bool {
		a, b := snap.runs[i].run, snap.runs[j].run
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt < b.CreatedAt
		}
		return a.ID < b.ID
	})
	return snap, nil
}

// agentDirs lists the agent directories in the mirror, sorted by id.
func (m mirror) agentDirs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(m.root, "agents"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() && validAgentID(entry.Name()) {
			dirs = append(dirs, filepath.Join(m.root, "agents", entry.Name()))
		}
	}
	return dirs, nil
}

// readStoredAgent returns the mirrored agent object, or nil when absent.
func (m mirror) readStoredAgent(agentID string) *apiAgent {
	raw, err := os.ReadFile(m.agentFile(agentID))
	if err != nil {
		return nil
	}
	record, err := decodeAgent(raw)
	if err != nil {
		return nil
	}
	return &record.agent
}

// ensureRoot creates the mirror root and, for a mirror inside the project,
// the .gitignore that keeps raw transcripts out of commits.
func (m mirror) ensureRoot(projectLocal bool) error {
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return err
	}
	if !projectLocal {
		return nil
	}
	path := filepath.Join(m.root, ".gitignore")
	if existing, err := os.ReadFile(path); err == nil && gitignoreExcludesAll(existing) {
		return nil
	}
	return writeFileAtomic(path, []byte(mirrorGitignore))
}

// gitignoreExcludesAll reports whether a .gitignore carries the bare "*" rule
// the mirror relies on. A file that exists without it (edited, truncated) would
// let git track raw transcripts, so ensureRoot rewrites it rather than trusting
// its presence.
func gitignoreExcludesAll(content []byte) bool {
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == "*" {
			return true
		}
	}
	return false
}

// writeFileAtomic replaces path with data via a temporary file and a rename, so
// a concurrent reader sees the old file or the new one, never a torn write.
// An unchanged file is left alone, which keeps the directory fingerprint (and
// so reindex) stable across syncs that bring nothing new.
func writeFileAtomic(path string, data []byte) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// writeEvents stores a run's events atomically.
func (m mirror) writeEvents(agentID, runID string, events []sseEvent) error {
	data, err := encodeEvents(events)
	if err != nil {
		return err
	}
	return writeFileAtomic(m.eventsFile(agentID, runID), data)
}

// refreshOptions tunes a refresh for its caller.
type refreshOptions struct {
	// skipStreams names runs whose events another goroutine is writing (a live
	// attach); the refresh must not replace their files underneath it.
	skipStreams map[string]bool
	now         time.Time
}

// refreshAgent brings one agent's mirror up to date. Runs, conversation and
// usage are re-fetched only when agent.updatedAt differs from what the mirror
// holds. Streams are fetched for finished runs whose capture is missing or
// partial while the API still retains them; a whole capture is never
// re-fetched. Unfinished runs are not streamed here, because reading a live
// stream blocks until the run ends; the watcher attaches to those instead.
// agent.json is written last so an interrupted refresh is retried next time.
func (m mirror) refreshAgent(ctx context.Context, c *apiClient, record agentRecord, opts refreshOptions) error {
	agentID := record.agent.ID
	stored := m.readStoredAgent(agentID)
	changed := stored == nil || stored.UpdatedAt != record.agent.UpdatedAt

	if changed {
		runs, err := c.listRuns(ctx, agentID)
		if err != nil {
			return fmt.Errorf("list runs: %w", err)
		}
		for _, run := range runs {
			if err := writeFileAtomic(m.runFile(agentID, run.run.ID), run.raw); err != nil {
				return err
			}
		}
		// A transient failure is returned so agent.json keeps its old updatedAt
		// and the next sync retries; a permanent refusal is logged and the agent
		// is mirrored without that file, since retrying would never help.
		if conv, err := c.conversation(ctx, agentID); err != nil {
			if !permanentAPIError(err) {
				return fmt.Errorf("fetch conversation: %w", err)
			}
			slog.Warn("Conversation unavailable for this agent", "agentId", agentID, "error", err)
		} else if err := writeFileAtomic(filepath.Join(m.agentDir(agentID), "conversation.json"), conv); err != nil {
			return err
		}
		if usage, err := c.usage(ctx, agentID); err != nil {
			if !permanentAPIError(err) {
				return fmt.Errorf("fetch usage: %w", err)
			}
			slog.Warn("Usage unavailable for this agent", "agentId", agentID, "error", err)
		} else if err := writeFileAtomic(filepath.Join(m.agentDir(agentID), "usage.json"), usage); err != nil {
			return err
		}
	}

	if err := m.fetchMissingStreams(ctx, c, agentID, opts); err != nil {
		return err
	}

	if changed {
		return writeFileAtomic(m.agentFile(agentID), record.raw)
	}
	return nil
}

// fetchMissingStreams replays the stream of every finished run whose mirrored
// capture is not whole, while the API still retains it.
func (m mirror) fetchMissingStreams(ctx context.Context, c *apiClient, agentID string, opts refreshOptions) error {
	entries, err := os.ReadDir(m.runsDir(agentID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(m.runsDir(agentID), name))
		if err != nil {
			return err
		}
		record, err := decodeRun(raw)
		if err != nil {
			continue
		}
		run := record.run
		if !run.isTerminal() || opts.skipStreams[run.ID] || !withinRetention(run, opts.now) {
			continue
		}
		events, err := readEventsFile(m.eventsFile(agentID, run.ID))
		if err != nil {
			return err
		}
		if eventsAreWhole(events) {
			continue
		}
		replayed, err := replayStream(ctx, c, agentID, run.ID)
		if errors.Is(err, errStreamExpired) {
			// Expired after all (clock skew or an early purge): the partial
			// capture stays and the v0 conversation fills the exchange.
			slog.Debug("Run stream expired; keeping partial capture", "agentId", agentID, "runId", run.ID)
			continue
		}
		if err != nil {
			return fmt.Errorf("replay stream %s: %w", run.ID, err)
		}
		if err := m.writeEvents(agentID, run.ID, replayed); err != nil {
			return err
		}
	}
	return nil
}

// withinRetention reports whether a finished run's stream should still be
// replayable. now is wall-clock time; this decides whether to make a network
// call and never reaches rendered output.
func withinRetention(run apiRun, now time.Time) bool {
	ended, err := time.Parse(time.RFC3339Nano, run.UpdatedAt)
	if err != nil {
		// Without a parseable end time, trying is cheaper than losing the transcript.
		return true
	}
	return now.Sub(ended) < streamRetention
}

// replayStream reads a finished run's stream from the start to its done event.
func replayStream(ctx context.Context, c *apiClient, agentID, runID string) ([]sseEvent, error) {
	body, err := c.openStream(ctx, agentID, runID, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	var events []sseEvent
	err = readSSE(body, agentID+"/"+runID, func(ev sseEvent) bool {
		events = append(events, ev)
		return ev.Event != "done"
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

// agentDirFingerprint sums sizes and takes the newest mtime across an agent
// directory: one file (agent.json) does not change when a run's events land.
func agentDirFingerprint(dir string) *spi.SessionFingerprint {
	fp := &spi.SessionFingerprint{}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		fp.Size += info.Size()
		if mt := info.ModTime().UnixNano(); mt > fp.Mtime {
			fp.Mtime = mt
		}
		return nil
	})
	return fp
}
