package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func assistantRecord(sidechain bool, tools ...[2]string) JSONLRecord {
	var content []interface{}
	for _, t := range tools {
		content = append(content, map[string]interface{}{
			"type":  "tool_use",
			"id":    t[0],
			"name":  t[1],
			"input": map[string]interface{}{},
		})
	}
	return JSONLRecord{Data: map[string]interface{}{
		"type":        "assistant",
		"isSidechain": sidechain,
		"message":     map[string]interface{}{"role": "assistant", "content": content},
	}}
}

func toolResultRecord(ids ...string) JSONLRecord {
	var content []interface{}
	for _, id := range ids {
		content = append(content, map[string]interface{}{
			"type":        "tool_result",
			"tool_use_id": id,
			"content":     "ok",
		})
	}
	return JSONLRecord{Data: map[string]interface{}{
		"type":    "user",
		"message": map[string]interface{}{"role": "user", "content": content},
	}}
}

func promptRecord(sidechain bool) JSONLRecord {
	return JSONLRecord{Data: map[string]interface{}{
		"type":        "user",
		"isSidechain": sidechain,
		"message":     map[string]interface{}{"role": "user", "content": "hello"},
	}}
}

func TestOpenShellToolUses(t *testing.T) {
	tests := []struct {
		name    string
		records []JSONLRecord
		want    []string
	}{
		{
			name:    "no tool calls",
			records: []JSONLRecord{promptRecord(false), assistantRecord(false)},
			want:    nil,
		},
		{
			name:    "open bash call",
			records: []JSONLRecord{promptRecord(false), assistantRecord(false, [2]string{"t1", "Bash"})},
			want:    []string{"t1"},
		},
		{
			name: "bash call closed by its result",
			records: []JSONLRecord{
				promptRecord(false),
				assistantRecord(false, [2]string{"t1", "Bash"}),
				toolResultRecord("t1"),
			},
			want: nil,
		},
		{
			name:    "open non-shell tool is not gated",
			records: []JSONLRecord{promptRecord(false), assistantRecord(false, [2]string{"t1", "Read"}, [2]string{"t2", "Agent"})},
			want:    nil,
		},
		{
			name: "parallel calls: one closed, one open",
			records: []JSONLRecord{
				promptRecord(false),
				assistantRecord(false, [2]string{"t1", "Bash"}),
				assistantRecord(false, [2]string{"t2", "Bash"}),
				toolResultRecord("t1"),
			},
			want: []string{"t2"},
		},
		{
			name: "new user prompt resets an open call from the previous turn",
			records: []JSONLRecord{
				promptRecord(false),
				assistantRecord(false, [2]string{"t1", "Bash"}),
				promptRecord(false),
			},
			want: nil,
		},
		{
			name: "sidechain prompt does not reset, sidechain bash is gated",
			records: []JSONLRecord{
				promptRecord(false),
				assistantRecord(false, [2]string{"t1", "Agent"}),
				promptRecord(true),
				assistantRecord(true, [2]string{"t2", "Bash"}),
			},
			want: []string{"t2"},
		},
		{
			name:    "tool name matching is case-insensitive and covers PowerShell",
			records: []JSONLRecord{promptRecord(false), assistantRecord(false, [2]string{"t1", "bash"}, [2]string{"t2", "PowerShell"})},
			want:    []string{"t1", "t2"},
		},
		{
			name: "malformed records are ignored",
			records: []JSONLRecord{
				{Data: map[string]interface{}{"type": "assistant"}},
				{Data: map[string]interface{}{"type": "user", "message": "not a map"}},
				{Data: map[string]interface{}{"type": "system", "subtype": "turn_duration"}},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := openShellToolUses(tt.records)
			if len(got) != len(tt.want) {
				t.Fatalf("openShellToolUses() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("openShellToolUses() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// shellGateFixture keeps all parser inputs and debug output isolated from the
// user's Claude sessions. Its first two records describe an unfinished shell.
type shellGateFixture struct {
	dir, file, sessionID string
	records              []JSONLRecord
}

func newShellGateFixture(t *testing.T) *shellGateFixture {
	t.Helper()
	f := &shellGateFixture{dir: t.TempDir(), sessionID: "11111111-1111-1111-1111-111111111111"}
	f.file = filepath.Join(f.dir, f.sessionID+".jsonl")
	spi.SetDebugBaseDir(t.TempDir())
	SetWatcherDebugRaw(true)
	t.Cleanup(func() {
		clearDeferredScan(f.deferredSession())
		ClearWatcherCallback()
		SetWatcherDebugRaw(false)
		spi.SetDebugBaseDir("")
		watcherCancel = nil
	})
	f.append(t, promptRecord(false), assistantRecord(false, [2]string{"t1", "Bash"}))
	return f
}

func (f *shellGateFixture) deferredSession() deferredSession {
	return deferredSession{claudeProjectDir: f.dir, sessionID: f.sessionID}
}

func (f *shellGateFixture) append(t *testing.T, records ...JSONLRecord) {
	t.Helper()
	f.records = append(f.records, records...)
	var data strings.Builder
	for i, record := range f.records {
		record.Data["sessionId"] = f.sessionID
		record.Data["uuid"] = fmt.Sprintf("record-%d", i)
		record.Data["timestamp"] = time.Date(2026, 9, 17, 1, 0, i, 0, time.UTC).Format(time.RFC3339)
		if i > 0 {
			record.Data["parentUuid"] = fmt.Sprintf("record-%d", i-1)
		}
		b, err := json.Marshal(record.Data)
		if err != nil {
			t.Fatal(err)
		}
		data.Write(b)
		data.WriteByte('\n')
	}
	if err := os.WriteFile(f.file, []byte(data.String()), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestShellGateScanAndDeadline(t *testing.T) {
	for _, resultFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("result-before-deadline=%t", resultFirst), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newShellGateFixture(t)
				saves := 0
				SetWatcherCallback(func(s *spi.AgentChatSession) {
					saves++
					if s.SessionID != f.sessionID {
						t.Errorf("unexpected session %s", s.SessionID)
					}
				})
				f.append(t, assistantRecord(false, [2]string{"t2", "PowerShell"}))
				scanJSONLFiles(f.dir, f.file)
				first, pending := deferredScans[f.deferredSession()]
				if !pending || saves != 0 {
					t.Fatalf("open calls: pending=%t saves=%d", pending, saves)
				}
				if _, err := os.Stat(spi.GetDebugDir(f.sessionID)); !os.IsNotExist(err) {
					t.Fatalf("open calls wrote debug output: %v", err)
				}

				// One parallel result must neither save nor extend the original deadline.
				f.append(t, toolResultRecord("t1"))
				scanJSONLFiles(f.dir, f.file)
				if deferredScans[f.deferredSession()].deadline != first.deadline || saves != 0 {
					t.Fatal("partial result saved or changed the original deadline")
				}
				flushExpiredDeferredScans(first.deadline.Add(-time.Nanosecond))
				if saves != 0 {
					t.Fatal("saved before deadline")
				}

				if resultFirst {
					f.append(t, toolResultRecord("t2"))
					scanJSONLFiles(f.dir, f.file)
				}
				// If the command is still open, persistence wins at the deadline.
				flushExpiredDeferredScans(first.deadline)
				if saves != 1 || len(deferredScans) != 0 {
					t.Fatalf("release: saves=%d pending=%d", saves, len(deferredScans))
				}
				entries, err := os.ReadDir(spi.GetDebugDir(f.sessionID))
				if err != nil || len(entries) != len(f.records) {
					t.Fatalf("debug records=%d want=%d err=%v", len(entries), len(f.records), err)
				}
				if !resultFirst {
					f.append(t, toolResultRecord("t2"))
					scanJSONLFiles(f.dir, f.file)
					if saves != 2 {
						t.Fatalf("late result saves=%d, want 2", saves)
					}
				}

				// A cleared deadline must not force a later command on the same file.
				time.Sleep(time.Second) // Advance virtual time before the next command.
				before := saves
				f.append(t, assistantRecord(false, [2]string{"t3", "Bash"}))
				scanJSONLFiles(f.dir, f.file)
				next := deferredScans[f.deferredSession()]
				if !next.deadline.After(first.deadline) {
					t.Fatal("new call reused the old deadline")
				}
				flushExpiredDeferredScans(first.deadline)
				if saves != before {
					t.Fatal("old deadline forced the new call")
				}
			})
		})
	}
}

func TestShellGateSharedSessionDeadline(t *testing.T) {
	for _, release := range []string{"result", "deadline", "shutdown", "forced scan"} {
		t.Run(release, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newShellGateFixture(t)
				saves := 0
				SetWatcherCallback(func(*spi.AgentChatSession) { saves++ })
				scanJSONLFiles(f.dir, f.file)
				first := deferredScans[f.deferredSession()]

				// A resumed file carries the same session and history. Its event
				// must share the first file's deadline, even when it arrives later.
				time.Sleep(time.Second)
				f.file = filepath.Join(f.dir, "resumed.jsonl")
				f.append(t)
				scanJSONLFiles(f.dir, f.file)
				pending := deferredScans[f.deferredSession()]
				if len(deferredScans) != 1 || pending.deadline != first.deadline || saves != 0 {
					t.Fatalf("second file changed session deferral: pending=%d saves=%d deadline=%v want=%v",
						len(deferredScans), saves, pending.deadline, first.deadline)
				}

				switch release {
				case "result":
					f.append(t, toolResultRecord("t1"))
					scanJSONLFiles(f.dir, f.file)
				case "deadline":
					flushExpiredDeferredScans(first.deadline)
				case "shutdown":
					StopWatcher()
				case "forced scan":
					scanJSONLFilesWithOptions(f.dir, f.file, true)
				}
				if saves != 1 || len(deferredScans) != 0 {
					t.Fatalf("release: saves=%d pending=%d", saves, len(deferredScans))
				}

				// Finish any forcibly saved call before opening the next one.
				if release != "result" {
					f.append(t, toolResultRecord("t1"))
				}
				f.append(t, assistantRecord(false, [2]string{"t2", "Bash"}))
				scanJSONLFiles(f.dir, f.file)
				next, exists := deferredScans[f.deferredSession()]
				if !exists || !next.deadline.After(first.deadline) {
					t.Fatal("subsequent shell call did not get a fresh deadline")
				}
				flushExpiredDeferredScans(first.deadline)
				if saves != 1 {
					t.Fatalf("stale deadline saved during subsequent shell call: saves=%d", saves)
				}
				f.append(t, toolResultRecord("t2"))
				scanJSONLFiles(f.dir, f.file)
				if saves != 2 || len(deferredScans) != 0 {
					t.Fatalf("subsequent result: saves=%d pending=%d", saves, len(deferredScans))
				}
			})
		})
	}
}

func TestShellGateShutdownOrdering(t *testing.T) {
	for _, activeSave := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline-save-in-flight=%t", activeSave), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// Associate the wait group with this bubble so Wait observes
				// blocked shutdown deterministically, without wall-clock sleeps.
				oldWG := watcherWg
				watcherWg = new(sync.WaitGroup)
				t.Cleanup(func() { watcherWg = oldWG })
				f := newShellGateFixture(t)
				releaseScan, enteredSave, releaseSave := make(chan struct{}), make(chan struct{}), make(chan struct{})
				cancelled, stopped := make(chan struct{}), make(chan struct{})
				watcherCancel = func() { close(cancelled) }
				saves := 0
				SetWatcherCallback(func(*spi.AgentChatSession) {
					close(enteredSave)
					<-releaseSave
					saves++
				})
				// Model an event/deadline scan already owned by the tracked watcher loop.
				watcherWg.Go(func() {
					<-releaseScan
					scanJSONLFiles(f.dir, f.file)
					if activeSave {
						flushExpiredDeferredScans(deferredScans[f.deferredSession()].deadline)
					}
				})
				if activeSave {
					close(releaseScan)
					<-enteredSave
				}
				var stopWG sync.WaitGroup
				stopWG.Go(func() { StopWatcher(); close(stopped) })
				<-cancelled
				synctest.Wait()
				select {
				case <-stopped:
					t.Error("shutdown returned before the in-flight scan finished")
				default:
				}
				if !activeSave {
					// The event adds its deferral after StopWatcher requested cancellation.
					close(releaseScan)
					<-enteredSave
				}
				synctest.Wait()
				select {
				case <-stopped:
					t.Error("shutdown returned before save completed")
				default:
				}
				close(releaseSave)
				stopWG.Wait()
				if saves != 1 || len(deferredScans) != 0 {
					t.Fatalf("shutdown: saves=%d pending=%d", saves, len(deferredScans))
				}
			})
		})
	}
}

func TestShellGateSyncBypassesDeferral(t *testing.T) {
	f := newShellGateFixture(t)
	testutil.SetHome(t, t.TempDir())
	project := t.TempDir()
	dir, err := resolveClaudeProjectDir(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.dir, dir); err != nil {
		t.Fatal(err)
	}
	f.dir, f.file = dir, filepath.Join(dir, f.sessionID+".jsonl")

	provider := NewProvider()
	all, err := provider.GetAgentChatSessions(project, true, nil)
	if err != nil || len(all) != 1 {
		t.Fatalf("bulk sync: sessions=%d err=%v", len(all), err)
	}
	one, err := provider.GetAgentChatSession(project, f.sessionID, true)
	if err != nil || one == nil {
		t.Fatalf("single sync: session=%v err=%v", one, err)
	}
	if len(deferredScans) != 0 {
		t.Fatal("sync registered deferred work")
	}
	if !strings.Contains(one.RawData, "tool_use") {
		t.Fatal("sync omitted the unfinished shell call")
	}
	if entries, err := os.ReadDir(spi.GetDebugDir(f.sessionID)); err != nil || len(entries) != len(f.records) {
		t.Fatalf("sync debug records=%d want=%d err=%v", len(entries), len(f.records), err)
	}
}

// A child process lets the run path see a new unfinished session immediately
// before agent exit, independently of fsnotify delivery timing.
func TestShellGateExitChild(t *testing.T) {
	destination := os.Getenv("SPECSTORY_SHELL_GATE_EXIT_DESTINATION")
	if destination == "" {
		return
	}
	data, err := os.ReadFile(os.Getenv("SPECSTORY_SHELL_GATE_EXIT_SOURCE"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestShellGateRunFinalSweep(t *testing.T) {
	f := newShellGateFixture(t)
	testutil.SetHome(t, t.TempDir())
	project := t.TempDir()
	t.Chdir(project)
	dir, err := resolveClaudeProjectDir(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPECSTORY_SHELL_GATE_EXIT_SOURCE", f.file)
	t.Setenv("SPECSTORY_SHELL_GATE_EXIT_DESTINATION", filepath.Join(dir, filepath.Base(f.file)))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf("%q -test.run=^TestShellGateExitChild$", exe)
	saves := 0
	err = NewProvider().ExecAgentAndWatch(project, command, "", true, func(s *spi.AgentChatSession) {
		if strings.Contains(s.RawData, "tool_use") {
			saves++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if saves == 0 || len(deferredScans) != 0 {
		t.Fatalf("final sweep: saves=%d pending=%d", saves, len(deferredScans))
	}
}
