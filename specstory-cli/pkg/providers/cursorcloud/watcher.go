package cursorcloud

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Watcher tuning. A remote store has no filesystem events, so a poll of the
// agent list is the change signal. Variables so tests can run in milliseconds.
var (
	// pollInterval keeps the poll (one request per page) far inside the
	// 20-requests-per-minute budget, leaving room for refreshes and streams.
	pollInterval = 60 * time.Second
	// emitDebounce coalesces a burst of stream events into one save; the
	// burst's last event is usually the completed text.
	emitDebounce = 2 * time.Second
	// reconnectDelay is the first wait before reattaching a dropped stream.
	reconnectDelay = 5 * time.Second
)

const (
	// maxLiveStreams bounds concurrent stream connections; each holds a
	// connection open for the length of a run.
	maxLiveStreams = 4
	// maxStreamReconnects bounds reattaching one dropped stream.
	maxStreamReconnects = 5
	// maxPollPages bounds the agents scanned per poll. The list is newest
	// first, so 300 agents covers any agent a person is actively running.
	maxPollPages = 3
)

// cloudWatcher polls the agent list and follows running runs live.
type cloudWatcher struct {
	origin       string
	projectPath  string
	mirror       mirror
	projectLocal bool
	client       *apiClient
	debugRaw     bool
	callback     func(*spi.AgentChatSession)

	ctx       context.Context
	cancel    context.CancelFunc
	startedAt time.Time

	mu      sync.Mutex
	seen    map[string]string // agent id -> updatedAt last refreshed successfully
	startup map[string]string // agent id -> updatedAt listed during the baseline
	live    map[string]bool   // run ids with an attached stream
	pending map[string]bool   // agent ids queued for emission
	emitCh  chan string

	workers  sync.WaitGroup // poll loop and stream followers
	emitter  sync.WaitGroup // the single delivery worker
	stopOnce sync.Once
}

func newCloudWatcher(projectPath, origin string, client *apiClient, debugRaw bool, callback func(*spi.AgentChatSession)) *cloudWatcher {
	m, projectLocal := mirrorFor(projectPath)
	return &cloudWatcher{
		origin:       origin,
		projectPath:  projectPath,
		mirror:       m,
		projectLocal: projectLocal,
		client:       client,
		debugRaw:     debugRaw,
		callback:     callback,
		seen:         map[string]string{},
		startup:      map[string]string{},
		live:         map[string]bool{},
		pending:      map[string]bool{},
	}
}

// start begins polling. The context is created per start so a stopped
// watcher can be started again.
func (w *cloudWatcher) start(parent context.Context) {
	w.ctx, w.cancel = context.WithCancel(parent)
	w.startedAt = time.Now()
	w.emitCh = make(chan string, 64)
	w.stopOnce = sync.Once{}
	w.emitter.Go(w.emitLoop)
	w.workers.Go(w.pollLoop)
}

// stop cancels polling and streams, waits for every follower to write its
// final events and queue any update not yet delivered, then drains delivery.
func (w *cloudWatcher) stop() {
	w.stopOnce.Do(func() {
		w.cancel()
		w.workers.Wait()
		close(w.emitCh)
		w.emitter.Wait()
	})
}

// pollLoop runs the baseline poll, then polls every pollInterval.
func (w *cloudWatcher) pollLoop() {
	baseline := true
	for {
		if w.poll(baseline) {
			baseline = false
		}
		if sleepContext(w.ctx, pollInterval) != nil {
			return
		}
	}
}

// poll lists agents and acts on the ones for this project. The baseline poll
// mirrors whatever is new but emits nothing: existing sessions are not news
// because the watcher started. Afterwards an agent seen for the first time or
// whose updatedAt moved is refreshed and emitted. An agent changing during the
// baseline is caught by the next poll, because the baseline records the
// updatedAt it listed, not what the agent became. The result reports whether
// the listing succeeded: pollLoop keeps the baseline open until it has, so a
// failed first poll cannot turn every pre-existing agent into news.
//
// "Listed at startup" and "refreshed" are tracked apart. seen records only
// refreshes that succeeded, so an agent whose baseline refresh failed is
// retried on every poll until its mirror is filled; startup remembers the
// updatedAt the baseline listed, so those retries stay silent while nothing
// about the agent has changed, and become news once it has.
func (w *cloudWatcher) poll(baseline bool) bool {
	agents, err := w.client.listAgents(w.ctx, maxPollPages)
	if err != nil {
		if w.ctx.Err() == nil {
			slog.Warn("WatchAgent: listing cloud agents failed; retrying next poll", "error", err)
		}
		return false
	}
	for _, record := range agents {
		if w.ctx.Err() != nil {
			return false
		}
		agent := record.agent
		if !agentMatchesOrigin(agent, w.origin) {
			continue
		}
		w.mu.Lock()
		prev, known := w.seen[agent.ID]
		if baseline {
			w.startup[agent.ID] = agent.UpdatedAt
		}
		// Quiet while the agent is exactly as the baseline listed it, even if
		// that baseline refresh failed and this is a retry.
		quiet := baseline || w.startup[agent.ID] == agent.UpdatedAt
		skip := w.liveSnapshot()
		w.mu.Unlock()
		if known && prev == agent.UpdatedAt {
			continue
		}

		if err := w.mirror.ensureRoot(w.projectLocal); err != nil {
			slog.Error("WatchAgent: cannot create mirror", "path", w.mirror.root, "error", err)
			return false
		}
		if err := w.mirror.refreshAgent(w.ctx, w.client, record, refreshOptions{skipStreams: skip, now: time.Now()}); err != nil {
			if w.ctx.Err() == nil {
				slog.Error("WatchAgent: refreshing cloud agent failed; retrying next poll", "agentId", agent.ID, "error", err)
			}
			continue
		}
		w.mu.Lock()
		w.seen[agent.ID] = agent.UpdatedAt
		w.mu.Unlock()
		if quiet {
			slog.Debug("WatchAgent: baseline", "agentId", agent.ID, "updatedAt", agent.UpdatedAt)
		} else {
			slog.Info("WatchAgent: cloud agent updated", "agentId", agent.ID, "updatedAt", agent.UpdatedAt)
			w.enqueue(agent.ID)
		}
	}
	w.attachRunning()
	return true
}

// liveSnapshot copies the attached-run set; callers hold w.mu.
func (w *cloudWatcher) liveSnapshot() map[string]bool {
	return maps.Clone(w.live)
}

// attachRunning starts a follower for every unfinished run in this project's
// mirror that has none, up to maxLiveStreams.
func (w *cloudWatcher) attachRunning() {
	for _, snap := range w.mirror.matchingSnapshots(w.origin) {
		for _, run := range snap.runs {
			if run.run.isTerminal() {
				continue
			}
			w.mu.Lock()
			attach := !w.live[run.run.ID] && len(w.live) < maxLiveStreams && w.ctx.Err() == nil
			if attach {
				w.live[run.run.ID] = true
			}
			w.mu.Unlock()
			if !attach {
				continue
			}
			agentID, runID := snap.agent.ID, run.run.ID
			slog.Info("WatchAgent: following live run", "agentId", agentID, "runId", runID)
			w.workers.Go(func() {
				w.followRun(agentID, runID)
				w.mu.Lock()
				delete(w.live, runID)
				w.mu.Unlock()
			})
		}
	}
}

// followRun streams a run into its events file until the run ends or the
// watcher stops. Events are flushed and the session queued for delivery after
// each burst (debounced), once more when the run ends, and on stop if an
// update is still undelivered. Only events stamped after the watcher started
// count as activity: attaching replays the run from its start.
func (w *cloudWatcher) followRun(agentID, runID string) {
	events, err := readEventsFile(w.mirror.eventsFile(agentID, runID))
	if err != nil {
		slog.Error("WatchAgent: reading mirrored events failed", "runId", runID, "error", err)
		return
	}
	if eventsAreWhole(events) {
		return
	}
	undelivered := false
	flush := func() {
		if err := w.mirror.writeEvents(agentID, runID, events); err != nil {
			slog.Error("WatchAgent: writing events failed", "runId", runID, "error", err)
		}
		if undelivered {
			w.enqueue(agentID)
			undelivered = false
		}
	}

	done := false
	delay := reconnectDelay
	for attempt := 0; !done && w.ctx.Err() == nil && attempt <= maxStreamReconnects; attempt++ {
		lastID := lastEventID(events)
		body, err := w.client.openStream(w.ctx, agentID, runID, lastID)
		if errors.Is(err, errStreamExpired) {
			slog.Info("WatchAgent: run stream expired", "runId", runID)
			break
		}
		if err != nil {
			if w.ctx.Err() == nil {
				slog.Warn("WatchAgent: stream connect failed", "runId", runID, "attempt", attempt+1, "error", err)
				_ = sleepContext(w.ctx, delay)
				delay = min(delay*2, maxBackoff)
			}
			continue
		}

		// A resumed stream may repeat events carrying the resume id: ids are
		// not unique (a thinking event and its interaction_update share one).
		repeats := eventsWithID(events, lastID)
		incoming := make(chan sseEvent)
		var reader sync.WaitGroup
		var readErr error
		reader.Go(func() {
			defer close(incoming)
			readErr = readSSE(body, agentID+"/"+runID, func(ev sseEvent) bool {
				select {
				case incoming <- ev:
					return ev.Event != "done"
				case <-w.ctx.Done():
					return false
				}
			})
		})

		debounce := time.NewTimer(emitDebounce)
		debounce.Stop()
	receive:
		for {
			select {
			case ev, ok := <-incoming:
				if !ok {
					break receive
				}
				if lastID != "" && ev.ID == lastID && repeats[eventKey(ev)] {
					continue
				}
				events = append(events, ev)
				if t, ok := eventInstant(ev.ID); ok && !t.Before(w.startedAt) {
					undelivered = true
				}
				if ev.Event == "done" {
					done = true
				}
				debounce.Reset(emitDebounce)
			case <-debounce.C:
				flush()
			case <-w.ctx.Done():
				break receive
			}
		}
		debounce.Stop()
		_ = body.Close()
		reader.Wait()
		if readErr != nil && w.ctx.Err() == nil && !done {
			slog.Warn("WatchAgent: stream dropped; reconnecting", "runId", runID, "error", readErr)
		}
	}

	if done {
		// The run object now says how the run ended; fetch it so the exchange
		// carries the final status and branch.
		if runs, err := w.client.listRuns(w.ctx, agentID); err == nil {
			for _, run := range runs {
				if err := writeFileAtomic(w.mirror.runFile(agentID, run.run.ID), run.raw); err != nil {
					slog.Error("WatchAgent: writing run failed", "runId", run.run.ID, "error", err)
				}
			}
		} else if w.ctx.Err() == nil {
			slog.Warn("WatchAgent: refreshing finished run failed", "runId", runID, "error", err)
		}
		slog.Info("WatchAgent: live run finished", "agentId", agentID, "runId", runID)
	}
	flush()
}

// lastEventID returns the id of the last event that carries one.
func lastEventID(events []sseEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].ID != "" {
			return events[i].ID
		}
	}
	return ""
}

// eventsWithID returns the identities of recorded events with the given id.
func eventsWithID(events []sseEvent, id string) map[string]bool {
	set := map[string]bool{}
	if id == "" {
		return set
	}
	for _, ev := range events {
		if ev.ID == id {
			set[eventKey(ev)] = true
		}
	}
	return set
}

func eventKey(ev sseEvent) string {
	data, _ := json.Marshal(ev)
	return string(data)
}

// enqueue queues an agent for delivery, coalescing repeats still waiting.
func (w *cloudWatcher) enqueue(agentID string) {
	w.mu.Lock()
	if w.pending[agentID] {
		w.mu.Unlock()
		return
	}
	w.pending[agentID] = true
	w.mu.Unlock()
	w.emitCh <- agentID
}

// emitLoop delivers sessions one at a time, in queue order, from the mirror.
func (w *cloudWatcher) emitLoop() {
	for agentID := range w.emitCh {
		w.mu.Lock()
		delete(w.pending, agentID)
		w.mu.Unlock()
		snap, err := loadAgentSnapshot(w.mirror.agentDir(agentID))
		if err != nil || snap == nil {
			slog.Warn("WatchAgent: cannot read mirrored agent", "agentId", agentID, "error", err)
			continue
		}
		session := convertSnapshot(snap, w.projectPath, w.debugRaw)
		if session == nil {
			continue
		}
		spi.DeliverSession("WatchAgent", w.callback, session)
	}
}
