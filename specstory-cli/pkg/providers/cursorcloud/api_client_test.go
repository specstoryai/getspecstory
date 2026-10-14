package cursorcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixture is one captured agent from testdata: the API objects as recorded
// and each run's SSE stream.
type fixture struct {
	agent        agentRecord
	runs         []runRecord
	streams      map[string][]byte
	conversation json.RawMessage
	usage        json.RawMessage
}

// loadFixture reads testdata/<name>. A run's stream is stream-<runId>.sse,
// or stream-live.sse in the single-run captures.
func loadFixture(t *testing.T, name string) fixture {
	t.Helper()
	dir := filepath.Join("testdata", name)
	read := func(file string) []byte {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("read fixture %s/%s: %v", name, file, err)
		}
		return data
	}
	agent, err := decodeAgent(read("agent.json"))
	if err != nil {
		t.Fatal(err)
	}
	var runList struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(read("runs.json"), &runList); err != nil {
		t.Fatal(err)
	}
	fx := fixture{agent: agent, streams: map[string][]byte{}, conversation: read("v0-conversation.json"), usage: read("usage.json")}
	for _, item := range runList.Items {
		run, err := decodeRun(item)
		if err != nil {
			t.Fatal(err)
		}
		fx.runs = append(fx.runs, run)
		stream, err := os.ReadFile(filepath.Join(dir, "stream-"+run.run.ID+".sse"))
		if os.IsNotExist(err) && len(runList.Items) == 1 {
			stream, err = os.ReadFile(filepath.Join(dir, "stream-live.sse"))
		}
		if err != nil {
			t.Fatalf("stream for %s: %v", run.run.ID, err)
		}
		fx.streams[run.run.ID] = stream
	}
	return fx
}

// parseSSEBytes parses a captured stream with the production reader.
func parseSSEBytes(t *testing.T, data []byte) []sseEvent {
	t.Helper()
	var events []sseEvent
	if err := readSSE(strings.NewReader(string(data)), "test", func(ev sseEvent) bool {
		events = append(events, ev)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	return events
}

// writeFixtureMirror writes a fixture into a mirror as a whole capture.
func writeFixtureMirror(t *testing.T, m mirror, fx fixture) {
	t.Helper()
	id := fx.agent.agent.ID
	for _, run := range fx.runs {
		if err := writeFileAtomic(m.runFile(id, run.run.ID), run.raw); err != nil {
			t.Fatal(err)
		}
		if err := m.writeEvents(id, run.run.ID, parseSSEBytes(t, fx.streams[run.run.ID])); err != nil {
			t.Fatal(err)
		}
	}
	for file, data := range map[string][]byte{"conversation.json": fx.conversation, "usage.json": fx.usage, "agent.json": fx.agent.raw} {
		if err := writeFileAtomic(filepath.Join(m.agentDir(id), file), data); err != nil {
			t.Fatal(err)
		}
	}
}

// fakeAPI serves captured fixtures as the Cloud Agents API.
type fakeAPI struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	agents   []json.RawMessage
	runs     map[string][]json.RawMessage
	streams  map[string][]byte
	expired  map[string]bool
	convs    map[string]json.RawMessage
	usages   map[string]json.RawMessage
	pageSize int // 0 = one page

	// streamHandler, when set, serves run streams instead of the stored bytes.
	streamHandler func(w http.ResponseWriter, r *http.Request, runID string)
	// onRequest is called (without the lock) before each request is served.
	onRequest func(path string)
	meStatus  int
	// listStatus, when set, makes GET /v1/agents fail with that status.
	listStatus int
	// convStatus, when set, makes GET /v0/agents/{id}/conversation fail with it.
	convStatus int
	requests   map[string]int
}

// newFakeAPI starts a server, points the client at it and lifts the rate
// limit so tests run at full speed.
func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{
		t: t, runs: map[string][]json.RawMessage{}, streams: map[string][]byte{}, expired: map[string]bool{},
		convs: map[string]json.RawMessage{}, usages: map[string]json.RawMessage{}, requests: map[string]int{},
	}
	f.server = httptest.NewServer(f)
	prevBase, prevLimiter := apiBaseURL, sharedLimiter
	apiBaseURL = f.server.URL
	sharedLimiter = newRateLimiter(0)
	t.Cleanup(func() {
		f.server.Close()
		apiBaseURL, sharedLimiter = prevBase, prevLimiter
	})
	return f
}

// addFixture serves a fixture and returns its agent id.
func (f *fakeAPI) addFixture(fx fixture) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fx.agent.agent.ID
	f.agents = append(f.agents, fx.agent.raw)
	for _, run := range fx.runs {
		f.runs[id] = append(f.runs[id], run.raw)
		f.streams[run.run.ID] = fx.streams[run.run.ID]
	}
	f.convs[id] = fx.conversation
	f.usages[id] = fx.usage
	return id
}

// setAgent replaces an agent object (matched by id) or adds it.
func (f *fakeAPI) setAgent(raw json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, err := decodeAgent(raw)
	if err != nil {
		f.t.Fatal(err)
	}
	for i, existing := range f.agents {
		if r, _ := decodeAgent(existing); r.agent.ID == record.agent.ID {
			f.agents[i] = raw
			return
		}
	}
	f.agents = append(f.agents, raw)
}

func (f *fakeAPI) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for path, c := range f.requests {
		if strings.HasPrefix(path, prefix) {
			n += c
		}
	}
	return n
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer test-key" {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "bad key")
		return
	}
	if f.onRequest != nil {
		f.onRequest(r.URL.Path)
	}
	f.mu.Lock()
	f.requests[r.URL.Path]++
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	f.mu.Unlock()

	switch {
	case r.URL.Path == "/v1/me":
		if f.meStatus != 0 {
			writeAPIError(w, f.meStatus, "unauthorized", "Invalid API key")
			return
		}
		data, _ := os.ReadFile(filepath.Join("testdata", "api", "me.json"))
		_, _ = w.Write(data)
	case r.URL.Path == "/v1/agents":
		f.serveAgentList(w, r)
	case len(parts) == 3 && parts[0] == "v1":
		f.serveAgentPart(w, parts[2], "")
	case len(parts) == 4 && parts[0] == "v1" && parts[3] == "runs":
		f.mu.Lock()
		items := f.runs[parts[2]]
		f.mu.Unlock()
		writeJSON(w, map[string]any{"items": items})
	case len(parts) == 4 && parts[0] == "v1" && parts[3] == "usage":
		f.serveAgentPart(w, parts[2], "usage")
	case len(parts) == 4 && parts[0] == "v0" && parts[3] == "conversation":
		f.serveAgentPart(w, parts[2], "conversation")
	case len(parts) == 6 && parts[5] == "stream":
		f.serveStream(w, r, parts[4])
	default:
		writeAPIError(w, http.StatusNotFound, "not_found", "no route")
	}
}

func (f *fakeAPI) serveAgentList(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	if f.listStatus != 0 {
		status := f.listStatus
		f.mu.Unlock()
		writeAPIError(w, status, "forbidden", "listing refused")
		return
	}
	agents := append([]json.RawMessage(nil), f.agents...)
	pageSize := f.pageSize
	f.mu.Unlock()
	start, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
	end := len(agents)
	if pageSize > 0 {
		end = min(start+pageSize, len(agents))
	}
	page := map[string]any{"items": agents[start:end]}
	if end < len(agents) {
		page["nextCursor"] = strconv.Itoa(end)
	}
	writeJSON(w, page)
}

func (f *fakeAPI) serveAgentPart(w http.ResponseWriter, agentID, part string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body json.RawMessage
	switch part {
	case "usage":
		body = f.usages[agentID]
	case "conversation":
		if f.convStatus != 0 {
			writeAPIError(w, f.convStatus, "unavailable", "conversation refused")
			return
		}
		body = f.convs[agentID]
	default:
		for _, raw := range f.agents {
			if r, _ := decodeAgent(raw); r.agent.ID == agentID {
				body = raw
			}
		}
	}
	if body == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "Agent not found")
		return
	}
	_, _ = w.Write(body)
}

func (f *fakeAPI) serveStream(w http.ResponseWriter, r *http.Request, runID string) {
	f.mu.Lock()
	expired, stream, handler := f.expired[runID], f.streams[runID], f.streamHandler
	f.mu.Unlock()
	if expired {
		data, _ := os.ReadFile(filepath.Join("testdata", "api", "stream-expired.json"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write(data)
		return
	}
	if handler != nil {
		handler(w, r, runID)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write(stream)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// useSettings isolates the provider's package settings and key for one test.
func useSettings(t *testing.T, envKey string) {
	t.Helper()
	t.Setenv(apiKeyEnvVar, envKey)
	prevKey, prevMirror, prevOverride := configAPIKey, configMirrorDir, userDataDirOverride
	configAPIKey, configMirrorDir, userDataDirOverride = "", "", ""
	t.Cleanup(func() { configAPIKey, configMirrorDir, userDataDirOverride = prevKey, prevMirror, prevOverride })
}

// fakeClock drives a rateLimiter deterministically: sleeping advances time.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	slept []time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
	return nil
}

func TestRateLimiterSpacesRequests(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_800_000_000, 0)}
	limiter := &rateLimiter{interval: 3 * time.Second, now: clock.Now, sleep: clock.Sleep}

	for range 3 {
		if err := limiter.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// The first request goes at once; each later one waits out the interval.
	want := []time.Duration{3 * time.Second, 3 * time.Second}
	if len(clock.slept) != len(want) || clock.slept[0] != want[0] || clock.slept[1] != want[1] {
		t.Errorf("slept %v, want %v", clock.slept, want)
	}

	// A 429 seen by one caller pushes every later slot past its Retry-After.
	limiter.holdUntil(clock.Now().Add(20 * time.Second))
	clock.slept = nil
	if err := limiter.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(clock.slept) != 1 || clock.slept[0] != 20*time.Second {
		t.Errorf("after holdUntil slept %v, want [20s]", clock.slept)
	}
}

func TestClientRetriesThrottledRequests(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		retryAfter string
		wantDelay  time.Duration
	}{
		{"429 with Retry-After seconds", http.StatusTooManyRequests, "7", 7 * time.Second},
		{"429 without Retry-After backs off", http.StatusTooManyRequests, "", initialBackoff},
		{"503 retried", http.StatusServiceUnavailable, "", initialBackoff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					if tt.retryAfter != "" {
						w.Header().Set("Retry-After", tt.retryAfter)
					}
					writeAPIError(w, tt.status, "rate_limited", "slow down")
					return
				}
				_, _ = w.Write([]byte(`{"apiKeyName":"k"}`))
			}))
			defer server.Close()

			clock := &fakeClock{now: time.Unix(1_800_000_000, 0)}
			c := newAPIClient("test-key")
			c.baseURL = server.URL
			c.limiter = &rateLimiter{interval: 0, now: clock.Now, sleep: clock.Sleep}

			me, err := c.me(context.Background())
			if err != nil {
				t.Fatalf("me: %v", err)
			}
			if me.KeyLabel != "k" || calls != 2 {
				t.Errorf("got key %q after %d calls, want k after 2", me.KeyLabel, calls)
			}
			if len(clock.slept) != 1 || clock.slept[0] != tt.wantDelay {
				t.Errorf("slept %v, want [%v]", clock.slept, tt.wantDelay)
			}
		})
	}
}

// TestClientRetriesTransportErrors closes the socket without a response on the
// first attempt, which the client sees as a transport error rather than a status.
func TestClientRetriesTransportErrors(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(`{"apiKeyName":"k"}`))
	}))
	defer server.Close()

	clock := &fakeClock{now: time.Unix(1_800_000_000, 0)}
	c := newAPIClient("test-key")
	c.baseURL = server.URL
	c.limiter = &rateLimiter{interval: 0, now: clock.Now, sleep: clock.Sleep}

	me, err := c.me(context.Background())
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if me.KeyLabel != "k" || calls != 2 {
		t.Errorf("got key %q after %d calls, want k after 2", me.KeyLabel, calls)
	}
	if len(clock.slept) != 1 || clock.slept[0] != initialBackoff {
		t.Errorf("slept %v, want [%v]", clock.slept, initialBackoff)
	}
}

// TestClientDoesNotRetryCancelledContext checks that a transport error caused by
// the caller giving up is returned at once instead of being backed off.
func TestClientDoesNotRetryCancelledContext(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"apiKeyName":"k"}`))
	}))
	defer server.Close()

	clock := &fakeClock{now: time.Unix(1_800_000_000, 0)}
	c := newAPIClient("test-key")
	c.baseURL = server.URL
	c.limiter = &rateLimiter{interval: 0, now: clock.Now, sleep: clock.Sleep}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.me(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 0 || len(clock.slept) != 0 {
		t.Errorf("calls = %d, slept %v; want no request and no backoff", calls, clock.slept)
	}
}

func TestClientGivesUpAfterMaxRetries(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "slow down")
	}))
	defer server.Close()
	clock := &fakeClock{now: time.Unix(1_800_000_000, 0)}
	c := newAPIClient("test-key")
	c.baseURL = server.URL
	c.limiter = &rateLimiter{interval: 0, now: clock.Now, sleep: clock.Sleep}

	_, err := c.me(context.Background())
	var ae *apiError
	if !errors.As(err, &ae) || ae.Status != http.StatusTooManyRequests {
		t.Fatalf("err = %v, want a 429 apiError", err)
	}
	if calls != maxRetries+1 {
		t.Errorf("calls = %d, want %d", calls, maxRetries+1)
	}
}

func TestOpenStreamExpired(t *testing.T) {
	f := newFakeAPI(t)
	f.expired["run-1"] = true
	_, err := newAPIClient("test-key").openStream(context.Background(), "bc-1", "run-1", "")
	if !errors.Is(err, errStreamExpired) {
		t.Fatalf("err = %v, want errStreamExpired", err)
	}
	if !strings.Contains(err.Error(), "Run stream is no longer available") {
		t.Errorf("err = %v, want the API's message from the captured 410 body", err)
	}
}

func TestOpenStreamIdleTimeout(t *testing.T) {
	prev := streamIdleTimeout
	streamIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { streamIdleTimeout = prev })

	f := newFakeAPI(t)
	release := make(chan struct{})
	defer close(release)
	f.streamHandler = func(w http.ResponseWriter, r *http.Request, runID string) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: heartbeat\ndata: {}\n\n"))
		w.(http.Flusher).Flush()
		// Then silence: a dead connection the client must notice by itself.
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}
	body, err := newAPIClient("test-key").openStream(context.Background(), "bc-1", "run-1", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()

	done := make(chan error, 1)
	go func() {
		done <- readSSE(body, "test", func(sseEvent) bool { return true })
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("readSSE returned nil, want the idle cancellation error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream read did not time out")
	}
}

func TestListAgentsDecodesCapturedList(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "fixture-run", "list.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("limit"); got != strconv.Itoa(listPageSize) {
			t.Errorf("limit = %q, want %d", got, listPageSize)
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	c := newAPIClient("test-key")
	c.baseURL = server.URL
	c.limiter = newRateLimiter(0)

	agents, err := c.listAgents(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 3 {
		t.Fatalf("got %d agents, want 3", len(agents))
	}
	if agents[0].agent.ID != "bc-a2a25972-6529-47c0-9691-8f135d0a6527" || agents[0].agent.Repos[0].URL != "https://github.com/specstoryai/cloud-agent-test" {
		t.Errorf("first agent = %+v", agents[0].agent)
	}
}
