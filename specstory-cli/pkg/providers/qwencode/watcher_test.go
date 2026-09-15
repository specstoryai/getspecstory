package qwencode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// A consumer failure cannot terminate the worker or prevent later saves.
func TestDeliverSessionContainsConsumerPanic(t *testing.T) {
	called := false
	deliverSession(func(*spi.AgentChatSession) { called = true; panic("consumer failed") }, &spi.AgentChatSession{SessionID: "s"})
	if !called {
		t.Fatal("callback not called")
	}
	called = false
	deliverSession(func(*spi.AgentChatSession) { called = true }, &spi.AgentChatSession{SessionID: "s2"})
	if !called {
		t.Fatal("delivery did not recover")
	}
}

func TestWatcherBootstrapRestartAndFinalSave(t *testing.T) {
	home := withFakeHome(t)
	project := t.TempDir()
	const id = "11111111-2222-3333-4444-555555555555"
	for run := 0; run < 2; run++ {
		events := make(chan *spi.AgentChatSession, 20)
		if err := WatchQwenProject(project, func(s *spi.AgentChatSession) { events <- s }); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(StopWatcher)
		// Includes a pre-existing session on the restart, which must stay silent.
		if run == 1 {
			select {
			case <-events:
				t.Fatal("republished history at startup")
			case <-time.After(1200 * time.Millisecond):
			}
		}
		path := seedFakeSession(t, home, project, "session-basic.jsonl", id)
		select {
		case <-events:
		case <-time.After(5 * time.Second):
			t.Fatal("watcher missed new session/update")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "The README says hello.", "Final saved turn")), 0644); err != nil {
			t.Fatal(err)
		}
		StopWatcher()
		final := false
		for len(events) > 0 {
			if strings.Contains((<-events).RawData, "Final saved turn") {
				final = true
			}
		}
		if !final {
			t.Fatal("stop lost the final write")
		}
	}
}

func TestWatcherDoesNotPublishExistingHistory(t *testing.T) {
	home := withFakeHome(t)
	project := t.TempDir()
	seedFakeSession(t, home, project, "session-basic.jsonl", "old")
	events := make(chan *spi.AgentChatSession, 10)
	if err := WatchQwenProject(project, func(s *spi.AgentChatSession) { events <- s }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(StopWatcher)
	// A directory nested under chats is outside the session layout.
	dir, _ := ResolveQwenProjectDir(project)
	if err := os.MkdirAll(filepath.Join(dir, "chats", "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "chats", "old.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chats", "nested", "new.jsonl"), data, 0644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
		t.Fatal("published startup history or nested session")
	case <-time.After(1200 * time.Millisecond):
	}
	StopWatcher()
	if len(events) > 0 {
		t.Fatal("stop republished startup history")
	}
}

func TestWatcherPrunesOldFilesAndReadoptsResumedSessions(t *testing.T) {
	home := withFakeHome(t)
	project := t.TempDir()
	path := seedFakeSession(t, home, project, "session-basic.jsonl", "11111111-2222-3333-4444-555555555555")
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Drain native events; this test drives reconciliation deterministically.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-watcher.Events:
			case <-watcher.Errors:
			}
		}
	}()
	defer func() { cancel(); <-done }()
	calls := 0
	w := qwenWatcher{watcher: watcher, projectPath: project, chatsDir: filepath.Dir(path), stamps: make(map[string]fileStamp), callback: func(*spi.AgentChatSession) { calls++ }}
	if err := w.reconcile(true); err != nil {
		t.Fatal(err)
	}
	if len(watcher.WatchList()) != 2 || calls != 0 {
		t.Fatalf("baseline watches=%v, calls=%d", watcher.WatchList(), calls)
	}
	old := spi.WatchWindowCutoff(time.Now()).Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := w.reconcile(false); err != nil {
		t.Fatal(err)
	}
	if len(watcher.WatchList()) != 1 || len(w.stamps) != 0 {
		t.Fatal("old file not pruned")
	}
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	if err := w.reconcile(false); err != nil {
		t.Fatal(err)
	}
	if len(watcher.WatchList()) != 2 || calls != 1 {
		t.Fatalf("resumed file not adopted: watches=%v calls=%d", watcher.WatchList(), calls)
	}
}

func TestStopWatcherJoinsCallback(t *testing.T) {
	home := withFakeHome(t)
	project := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	if err := WatchQwenProject(project, func(*spi.AgentChatSession) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
	}); err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			close(release)
		}
		StopWatcher()
	}()
	seedFakeSession(t, home, project, "session-basic.jsonl", "11111111-2222-3333-4444-555555555555")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("callback not started")
	}
	stopped := make(chan struct{})
	go func() { StopWatcher(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned before callback completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	released = true
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not join callback")
	}
}
