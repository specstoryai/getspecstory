package piagent

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// TestParsePiRunCommand covers the command split (quoting, tilde) and the resume
// flag append (`--session-id <id>`). ExecutePi's exit-status path is covered by
// TestExecAgentAndWatch_NonZeroExitStillSavesSession with a stand-in pi binary.
func TestParsePiRunCommand(t *testing.T) {
	home := expandTilde("~/x") // resolve once so the tilde case is host-independent

	tests := []struct {
		name            string
		customCommand   string
		resumeSessionID string
		expectedCmd     string
		expectedArgs    []string
	}{
		{
			name:         "empty command returns default pi, no args",
			expectedCmd:  "pi",
			expectedArgs: nil,
		},
		{
			name:          "whitespace-only command returns default pi",
			customCommand: "   ",
			expectedCmd:   "pi",
			expectedArgs:  nil,
		},
		{
			name:          "command with args",
			customCommand: "pi --provider openai --model gpt-4o",
			expectedCmd:   "pi",
			expectedArgs:  []string{"--provider", "openai", "--model", "gpt-4o"},
		},
		{
			name:          "quoted argument containing spaces",
			customCommand: `pi --system-prompt "you are helpful"`,
			expectedCmd:   "pi",
			expectedArgs:  []string{"--system-prompt", "you are helpful"},
		},
		{
			name:          "tilde in binary path is expanded",
			customCommand: "~/x --model gpt-4o",
			expectedCmd:   home,
			expectedArgs:  []string{"--model", "gpt-4o"},
		},
		{
			name:            "resume id with empty command appends session-id flag",
			resumeSessionID: "01a067f7-2950-7155-b562-8297e73e3428",
			expectedCmd:     "pi",
			expectedArgs:    []string{"--session-id", "01a067f7-2950-7155-b562-8297e73e3428"},
		},
		{
			name:            "resume id with custom command appends after args",
			customCommand:   "pi --model gpt-4o",
			resumeSessionID: "sess-1",
			expectedCmd:     "pi",
			expectedArgs:    []string{"--model", "gpt-4o", "--session-id", "sess-1"},
		},
		{
			name:            "resume id is trimmed before append",
			resumeSessionID: "  sess-2  ",
			expectedCmd:     "pi",
			expectedArgs:    []string{"--session-id", "sess-2"},
		},
		{
			name:            "whitespace-only resume id appends nothing",
			customCommand:   "pi",
			resumeSessionID: "   ",
			expectedCmd:     "pi",
			expectedArgs:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args := parsePiRunCommand(tt.customCommand, tt.resumeSessionID)
			if cmd != tt.expectedCmd {
				t.Errorf("cmd = %q, want %q", cmd, tt.expectedCmd)
			}
			if len(args) != len(tt.expectedArgs) {
				t.Fatalf("args = %v, want %v", args, tt.expectedArgs)
			}
			for i := range args {
				if args[i] != tt.expectedArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, args[i], tt.expectedArgs[i])
				}
			}
		})
	}
}

// TestExecAgentAndWatch_NonZeroExitStillSavesSession drives `run pi` end to end
// with a stand-in pi that writes one complete session file and then exits 7,
// the way a real pi fails right after its last write. Before the fix ExecutePi
// called os.Exit(7) on that path, so ExecAgentAndWatch never reached
// StopWatcher, the in-flight save was never joined, and this test binary died
// with status 7 before asserting anything. Now the save must have landed by the
// time ExecAgentAndWatch returns, and pi's status must come back as a
// *spi.AgentExitError so the CLI can exit with it.
func TestExecAgentAndWatch_NonZeroExitStillSavesSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in pi is a POSIX shell script")
	}

	tmp := t.TempDir()
	t.Setenv(envAgentDir, tmp)
	projectPath := filepath.FromSlash("/pi-exit-proj")

	targetDir, err := ProjectSessionDir(projectPath)
	if err != nil {
		t.Fatalf("ProjectSessionDir: %v", err)
	}
	if mkErr := os.MkdirAll(targetDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	// The session the fake pi writes, staged outside the watched directory so
	// the script's single write is the only event the watcher sees.
	staged := filepath.Join(tmp, "staged.jsonl")
	if wErr := os.WriteFile(staged, []byte(validSession("sess-exit7", projectPath, "prompt before the crash")), 0o600); wErr != nil {
		t.Fatalf("WriteFile staged: %v", wErr)
	}
	sessionPath := filepath.Join(targetDir, "2026-09-06T12-00-00-000Z_sess-exit7.jsonl")

	// The 1s sleep gives the watcher goroutine time to register its directory
	// watch, as a real pi takes far longer than that to write its first entry.
	fakePi := filepath.Join(tmp, "fakepi.sh")
	script := "#!/bin/sh\nsleep 1\ncat \"$FAKE_PI_STAGED\" > \"$FAKE_PI_SESSION\"\nexit 7\n"
	if wErr := os.WriteFile(fakePi, []byte(script), 0o700); wErr != nil {
		t.Fatalf("WriteFile fakepi: %v", wErr)
	}
	t.Setenv("FAKE_PI_STAGED", staged)
	t.Setenv("FAKE_PI_SESSION", sessionPath)

	// Stand-in for the run command's autosave: write the markdown for the session.
	markdown := filepath.Join(tmp, "saved-session.md")
	callback := func(s *spi.AgentChatSession) {
		if wErr := os.WriteFile(markdown, []byte("# "+s.SessionID+"\n"), 0o600); wErr != nil {
			t.Errorf("WriteFile markdown: %v", wErr)
		}
	}

	err = NewProvider().ExecAgentAndWatch(projectPath, fakePi, "", false, callback)

	var agentExit *spi.AgentExitError
	if !errors.As(err, &agentExit) {
		t.Fatalf("ExecAgentAndWatch error = %v, want *spi.AgentExitError", err)
	}
	if agentExit.Code != 7 {
		t.Fatalf("exit code = %d, want 7", agentExit.Code)
	}
	if _, statErr := os.Stat(sessionPath); statErr != nil {
		t.Fatalf("fake pi did not write its session file: %v", statErr)
	}
	if _, statErr := os.Stat(markdown); statErr != nil {
		t.Fatalf("markdown missing after ExecAgentAndWatch returned on pi exit 7: %v", statErr)
	}
}
