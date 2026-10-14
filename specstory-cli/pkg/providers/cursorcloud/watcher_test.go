package cursorcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// fastWatcher shortens the watcher's timings so acceptance cases run quickly.
func fastWatcher(t *testing.T) {
	t.Helper()
	prevPoll, prevDebounce, prevReconnect := pollInterval, emitDebounce, reconnectDelay
	pollInterval, emitDebounce, reconnectDelay = 40*time.Millisecond, 20*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { pollInterval, emitDebounce, reconnectDelay = prevPoll, prevDebounce, prevReconnect })
}

// emissions records delivered sessions.
type emissions struct {
	mu       sync.Mutex
	sessions []*spi.AgentChatSession
}

func (e *emissions) callback(s *spi.AgentChatSession) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessions = append(e.sessions, s)
}

func (e *emissions) all() []*spi.AgentChatSession {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]*spi.AgentChatSession(nil), e.sessions...)
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// startWatcher starts a watcher for a fresh project and returns it with its
// emissions; stop is the caller's.
func startWatcher(t *testing.T) (*cloudWatcher, *emissions) {
	t.Helper()
	project := makeProject(t, t.TempDir(), testOrigin)
	got := &emissions{}
	w := newCloudWatcher(project, projectOrigin(project), newAPIClient("test-key"), false, got.callback)
	w.start(context.Background())
	return w, got
}

// withUpdatedAt returns a copy of an agent object with a new updatedAt.
func withUpdatedAt(t *testing.T, raw json.RawMessage, updatedAt string) json.RawMessage {
	t.Helper()
	var agent map[string]any
	if err := json.Unmarshal(raw, &agent); err != nil {
		t.Fatal(err)
	}
	agent["updatedAt"] = updatedAt
	out, _ := json.Marshal(agent)
	return out
}

func TestWatcherLeavesUnchangedAgentsAlone(t *testing.T) {
	useSettings(t, "test-key")
	fastWatcher(t)
	f := newFakeAPI(t)
	f.addFixture(loadFixture(t, "fixture-run"))

	w, got := startWatcher(t)
	waitFor(t, "three polls", func() bool { return f.count("/v1/agents") >= 3 })
	w.stop()
	if n := len(got.all()); n != 0 {
		t.Errorf("emitted %d sessions for an agent unchanged since startup", n)
	}
}

func TestWatcherEmitsChangeDuringBaseline(t *testing.T) {
	useSettings(t, "test-key")
	fastWatcher(t)
	f := newFakeAPI(t)
	fx := loadFixture(t, "fixture-run")
	f.addFixture(fx)
	// The agent changes while the baseline is fetching it: the baseline
	// recorded the old updatedAt, so the next poll must deliver the change.
	var once sync.Once
	f.onRequest = func(path string) {
		if strings.HasSuffix(path, "/runs") {
			once.Do(func() { f.setAgent(withUpdatedAt(t, fx.agent.raw, "2026-10-13T15:10:00.000Z")) })
		}
	}

	w, got := startWatcher(t)
	waitFor(t, "the change to be emitted", func() bool { return len(got.all()) > 0 })
	w.stop()
	if s := got.all()[0]; s.SessionID != fx.agent.agent.ID || s.SessionData.UpdatedAt != "2026-10-13T15:10:00.000Z" {
		t.Errorf("emitted %s at %s", s.SessionID, s.SessionData.UpdatedAt)
	}
}

func TestWatcherAdoptsLateArrivingAgent(t *testing.T) {
	useSettings(t, "test-key")
	fastWatcher(t)
	f := newFakeAPI(t)
	f.addFixture(loadFixture(t, "fixture-run"))

	w, got := startWatcher(t)
	waitFor(t, "the baseline", func() bool { return f.count("/v1/agents") >= 2 })
	// Created days ago, but new to this watcher: arrival is activity.
	late := f.addFixture(loadFixture(t, "failure-run"))
	waitFor(t, "the late agent", func() bool { return len(got.all()) > 0 })
	w.stop()
	for _, s := range got.all() {
		if s.SessionID != late {
			t.Errorf("emitted %s, want only the late arrival", s.SessionID)
		}
	}
}

func TestWatcherBaselineSpansEveryPage(t *testing.T) {
	useSettings(t, "test-key")
	fastWatcher(t)
	f := newFakeAPI(t)
	f.pageSize = 1
	f.addFixture(loadFixture(t, "fixture-run"))
	f.addFixture(loadFixture(t, "failure-run"))

	w, got := startWatcher(t)
	// Two pages per poll; wait for the second full poll.
	waitFor(t, "two polls", func() bool { return f.count("/v1/agents") >= 4 })
	w.stop()
	if n := len(got.all()); n != 0 {
		t.Errorf("an agent found on a later page of the baseline was emitted %d times", n)
	}
}

func TestWatcherStopWithNothingPendingEmitsNothing(t *testing.T) {
	useSettings(t, "test-key")
	fastWatcher(t)
	f := newFakeAPI(t)
	f.addFixture(loadFixture(t, "fixture-run"))

	w, got := startWatcher(t)
	waitFor(t, "the baseline", func() bool { return f.count("/v1/agents") >= 1 })
	stopped := make(chan struct{})
	go func() {
		w.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return")
	}
	if n := len(got.all()); n != 0 {
		t.Errorf("stop emitted %d sessions", n)
	}
	// Restartable: a second start gets a fresh context.
	w.start(context.Background())
	waitFor(t, "a poll after restart", func() bool { return f.count("/v1/agents") >= 3 })
	w.stop()
}

// liveStream rewrites a captured stream's ids to times after now, so its
// events count as activity, and splits it into blocks.
func liveStream(t *testing.T, data []byte) []string {
	t.Helper()
	idLine := regexp.MustCompile(`(?m)^id: (\d+)-(\d+)$`)
	first := int64(0)
	base := time.Now().Add(time.Second).UnixMilli()
	rewritten := idLine.ReplaceAllStringFunc(string(data), func(line string) string {
		m := idLine.FindStringSubmatch(line)
		ms, _ := strconv.ParseInt(m[1], 10, 64)
		if first == 0 {
			first = ms
		}
		return fmt.Sprintf("id: %d-%s", base+ms-first, m[2])
	})
	var blocks []string
	for _, block := range strings.Split(rewritten, "\n\n") {
		if strings.TrimSpace(block) != "" {
			blocks = append(blocks, block+"\n\n")
		}
	}
	return blocks
}

func TestWatcherFollowsRunningRunLive(t *testing.T) {
	useSettings(t, "test-key")
	fastWatcher(t)
	f := newFakeAPI(t)
	fx := loadFixture(t, "fixture-run")
	runID := fx.runs[0].run.ID
	blocks := liveStream(t, fx.streams[runID])

	// The agent appears after startup with its run still RUNNING.
	running := func(status string) json.RawMessage {
		var run map[string]any
		_ = json.Unmarshal(fx.runs[0].raw, &run)
		run["status"] = status
		out, _ := json.Marshal(run)
		return out
	}

	pause := make(chan struct{})
	release := make(chan struct{})
	var requests []string
	f.streamHandler = func(w http.ResponseWriter, r *http.Request, id string) {
		f.mu.Lock()
		requests = append(requests, r.Header.Get("Last-Event-ID"))
		attempt := len(requests)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		start := 0
		if attempt > 1 {
			// Resume after the Last-Event-ID, repeating its block as a server may.
			for i, b := range blocks {
				if strings.Contains(b, "id: "+r.Header.Get("Last-Event-ID")+"\n") {
					start = i
					break
				}
			}
		}
		for i := start; i < len(blocks); i++ {
			if attempt == 1 && i == len(blocks)/2 {
				// First connection: go quiet long enough for a debounced save,
				// then drop the connection mid-run.
				close(pause)
				<-release
				return
			}
			_, _ = w.Write([]byte(blocks[i]))
			flusher.Flush()
		}
		f.mu.Lock()
		f.runs[fx.agent.agent.ID] = []json.RawMessage{running("FINISHED")}
		f.mu.Unlock()
	}

	w, got := startWatcher(t)
	waitFor(t, "the baseline", func() bool { return f.count("/v1/agents") >= 1 })
	f.mu.Lock()
	f.agents = append(f.agents, fx.agent.raw)
	f.runs[fx.agent.agent.ID] = []json.RawMessage{running("RUNNING")}
	f.convs[fx.agent.agent.ID] = fx.conversation
	f.usages[fx.agent.agent.ID] = fx.usage
	f.mu.Unlock()

	<-pause
	waitFor(t, "a save during the run", func() bool {
		for _, s := range got.all() {
			if len(s.SessionData.Exchanges) == 1 && len(s.SessionData.Exchanges[0].Messages) > 1 {
				return true
			}
		}
		return false
	})
	midRun := len(got.all()[len(got.all())-1].SessionData.Exchanges[0].Messages)
	close(release)

	waitFor(t, "the finished run", func() bool {
		all := got.all()
		last := all[len(all)-1]
		return last.SessionData.Exchanges[0].Metadata["status"] == "FINISHED"
	})
	w.stop()

	final := got.all()[len(got.all())-1]
	if n := len(final.SessionData.Exchanges[0].Messages); n <= midRun {
		t.Errorf("final save has %d messages, mid-run had %d", n, midRun)
	}
	m, _ := mirrorFor(w.projectPath)
	events, _ := readEventsFile(m.eventsFile(fx.agent.agent.ID, runID))
	if !eventsAreWhole(events) {
		t.Error("the mirrored capture is not whole after the run finished")
	}
	if len(events) != len(blocks) {
		t.Errorf("mirrored %d events, want %d: the resumed stream duplicated or lost events", len(events), len(blocks))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(requests) < 2 || requests[0] != "" || requests[1] == "" {
		t.Errorf("stream requests Last-Event-ID = %q, want a fresh connect then a resume", requests)
	}
}

// TestWatcherBaselineSurvivesListFailure starts the watcher while the agent
// listing is refused. The baseline must stay open until a listing succeeds, so
// the pre-existing agent is not emitted as news when the API recovers.
func TestWatcherBaselineSurvivesListFailure(t *testing.T) {
	useSettings(t, "test-key")
	fastWatcher(t)
	f := newFakeAPI(t)
	fx := loadFixture(t, "fixture-run")
	f.agents = append(f.agents, fx.agent.raw)
	f.runs[fx.agent.agent.ID] = []json.RawMessage{fx.runs[0].raw}
	f.convs[fx.agent.agent.ID] = fx.conversation
	f.usages[fx.agent.agent.ID] = fx.usage
	f.streams = fx.streams
	// 403 is not retried by the client, so the poll itself sees the failure.
	f.listStatus = http.StatusForbidden

	w, got := startWatcher(t)
	waitFor(t, "two refused listings", func() bool { return f.count("/v1/agents") >= 2 })
	f.mu.Lock()
	f.listStatus = 0
	f.mu.Unlock()
	waitFor(t, "the agent to be mirrored", func() bool {
		snap, _ := loadAgentSnapshot(w.mirror.agentDir(fx.agent.agent.ID))
		return snap != nil
	})
	waitFor(t, "one more poll", func() bool { return f.count("/v1/agents") >= 5 })
	w.stop()

	if n := len(got.all()); n != 0 {
		t.Errorf("emitted %d sessions after a failed baseline, want none", n)
	}
}
