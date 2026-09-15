package musecode

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func TestParseMuseCommand(t *testing.T) {
	tests := []struct {
		name          string
		customCommand string
		expectedCmd   string
		expectedArgs  []string
	}{
		{
			name:          "empty uses the default binary",
			customCommand: "",
			expectedCmd:   "muse",
			expectedArgs:  nil,
		},
		{
			name:          "custom path with no args",
			customCommand: "/opt/muse/bin/muse",
			expectedCmd:   "/opt/muse/bin/muse",
			expectedArgs:  []string{},
		},
		{
			name:          "custom command with args",
			customCommand: "muse --model muse-spark-1.2",
			expectedCmd:   "muse",
			expectedArgs:  []string{"--model", "muse-spark-1.2"},
		},
		{
			name:          "quoted path is kept whole",
			customCommand: `"/Applications/My Tools/muse" exec`,
			expectedCmd:   "/Applications/My Tools/muse",
			expectedArgs:  []string{"exec"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args := parseMuseCommand(tt.customCommand)
			if cmd != tt.expectedCmd {
				t.Errorf("command = %q, want %q", cmd, tt.expectedCmd)
			}
			if !slices.Equal(args, tt.expectedArgs) {
				t.Errorf("args = %v, want %v", args, tt.expectedArgs)
			}
		})
	}
}

// The stand-in waits until a save starts before exiting. This isolates the
// shutdown join from fsnotify delivery timing: returning the exit status must
// never let the caller exit while that save is still in flight.
func TestExecAgentAndWatch_JoinsSaveBeforeReturningExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in Muse is a POSIX shell script")
	}
	for _, code := range []int{0, 7} {
		t.Run(fmt.Sprintf("exit_%d", code), func(t *testing.T) {
			root := seedStore(t)
			project := t.TempDir()
			staged := writeSession(t, t.TempDir(), time.Now().Format("2006/01/02"), basicSessionID, "session-basic.jsonl", project)
			target := filepath.Join(root, time.Now().Format("2006/01/02"), basicSessionID, sessionFileName)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			started := filepath.Join(t.TempDir(), "save-started")
			t.Setenv("FAKE_MUSE_STAGED", staged)
			t.Setenv("FAKE_MUSE_SESSION", target)
			t.Setenv("FAKE_MUSE_SAVE_STARTED", started)
			t.Setenv("FAKE_MUSE_EXIT", fmt.Sprint(code))
			fakeMuse := filepath.Join(t.TempDir(), "muse.sh")
			script := `#!/bin/sh
set -eu
i=0
while [ ! -f "$FAKE_MUSE_SAVE_STARTED" ]; do
  cat "$FAKE_MUSE_STAGED" > "$FAKE_MUSE_SESSION"
  i=$((i + 1))
  if [ "$i" -ge 500 ]; then exit 99; fi
  sleep 0.01
done
exit "$FAKE_MUSE_EXIT"
`
			if err := os.WriteFile(fakeMuse, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			saved := make(chan struct{})
			var once sync.Once
			callback := func(s *spi.AgentChatSession) {
				once.Do(func() {
					if s.SessionID != basicSessionID {
						t.Errorf("session ID = %q, want %q", s.SessionID, basicSessionID)
					}
					if err := os.WriteFile(started, nil, 0o600); err != nil {
						t.Error(err)
					}
					// Keep the save in flight after the child observes the marker.
					time.Sleep(100 * time.Millisecond)
					close(saved)
				})
			}
			t.Cleanup(func() {
				StopWatcher()
				SetWatcherCallback(nil)
				SetWatcherWorkspaceRoot("")
			})
			err := NewProvider().ExecAgentAndWatch(project, fmt.Sprintf("%q", fakeMuse), "", false, callback)
			if code == 0 {
				if err != nil {
					t.Fatalf("ExecAgentAndWatch: %v", err)
				}
			} else {
				var agentExit *spi.AgentExitError
				if !errors.As(err, &agentExit) || agentExit.Code != code || agentExit.Agent != "Muse Code" {
					t.Fatalf("error = %v, want Muse Code AgentExitError with code %d", err, code)
				}
			}
			select {
			case <-saved:
			default:
				t.Fatal("ExecAgentAndWatch returned before the save finished")
			}
		})
	}
}

func TestExecuteMuse_StartFailure(t *testing.T) {
	err := ExecuteMuse(fmt.Sprintf("%q", filepath.Join(t.TempDir(), "missing-muse")), "")
	var agentExit *spi.AgentExitError
	if err == nil || errors.As(err, &agentExit) {
		t.Fatalf("error = %v, want a start failure rather than an agent exit status", err)
	}
}
