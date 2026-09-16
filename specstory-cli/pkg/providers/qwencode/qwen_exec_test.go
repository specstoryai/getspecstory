package qwencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func TestEnsureResumeArgs(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		resumeSessionID string
		want            []string
	}{
		{
			name:            "no resume id leaves args untouched",
			args:            []string{"--model", "qwen3-coder-plus"},
			resumeSessionID: "",
			want:            []string{"--model", "qwen3-coder-plus"},
		},
		{
			name:            "appends resume flag",
			args:            []string{},
			resumeSessionID: "abc-123",
			want:            []string{"--resume", "abc-123"},
		},
		{
			name:            "existing --resume with value is replaced",
			args:            []string{"--resume", "other-id"},
			resumeSessionID: "abc-123",
			want:            []string{"--resume", "abc-123"},
		},
		{
			name:            "existing -r with value is replaced",
			args:            []string{"-r", "other-id"},
			resumeSessionID: "abc-123",
			want:            []string{"-r", "abc-123"},
		},
		{
			name:            "existing --resume=id is replaced",
			args:            []string{"--resume=other-id"},
			resumeSessionID: "abc-123",
			want:            []string{"--resume=abc-123"},
		},
		{
			name:            "bare --resume at end gets the id inserted, not a duplicate flag",
			args:            []string{"--safe-mode", "--resume"},
			resumeSessionID: "abc-123",
			want:            []string{"--safe-mode", "--resume", "abc-123"},
		},
		{
			name:            "bare -r followed by another flag gets the id inserted",
			args:            []string{"-r", "--safe-mode"},
			resumeSessionID: "abc-123",
			want:            []string{"-r", "abc-123", "--safe-mode"},
		},
		{
			name:            "empty --resume= is repaired in place",
			args:            []string{"--resume="},
			resumeSessionID: "abc-123",
			want:            []string{"--resume=abc-123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backing := make([]string, len(tt.args)+4)
			copy(backing, tt.args)
			original := append([]string(nil), backing...)
			got := ensureResumeArgs(backing[:len(tt.args)], tt.resumeSessionID)
			if !reflect.DeepEqual(backing, original) {
				t.Error("mutated caller backing array")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ensureResumeArgs(%v, %q) = %v, want %v", tt.args, tt.resumeSessionID, got, tt.want)
			}
		})
	}
}

func TestParseQwenCommand(t *testing.T) {
	cmd, args := parseQwenCommand(`/custom/qwen --model foo`)
	if cmd != "/custom/qwen" {
		t.Errorf("cmd = %q, want /custom/qwen", cmd)
	}
	if !reflect.DeepEqual(args, []string{"--model", "foo"}) {
		t.Errorf("args = %v", args)
	}

	cmd, args = parseQwenCommand("")
	if cmd != "qwen" {
		t.Errorf("default cmd = %q, want qwen", cmd)
	}
	if len(args) != 0 {
		t.Errorf("default args = %v, want none", args)
	}
}

// This test binary is also the portable child process for the lifecycle test.
func TestQwenExecChild(t *testing.T) {
	home := os.Getenv("QWEN_EXEC_TEST_HOME")
	if home == "" {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(91)
	}
	id := "11111111-2222-3333-4444-555555555555"
	projects := filepath.Join(home, ".qwen", "projects")
	if os.Getenv("QWEN_HOME") != "" || os.Getenv("QWEN_RUNTIME_DIR") != "" {
		projects, err = GetQwenProjectsDir()
		if err != nil {
			os.Exit(96)
		}
	}
	dir := filepath.Join(projects, SanitizeQwenCwd(cwd), "chats")
	if err := os.MkdirAll(dir, 0755); err != nil {
		os.Exit(92)
	}
	record := map[string]any{"uuid": "u", "sessionId": id, "type": "user", "cwd": cwd, "timestamp": "2026-09-15T12:00:00Z", "message": map[string]any{"role": "user", "parts": []map[string]string{{"text": "final child turn"}}}}
	data, err := json.Marshal(record)
	if err != nil {
		os.Exit(93)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), append(data, '\n'), 0644); err != nil {
		os.Exit(94)
	}
	args, _ := json.Marshal(os.Args)
	if err := os.WriteFile(filepath.Join(cwd, "child-args.json"), args, 0644); err != nil {
		os.Exit(95)
	}
	os.Exit(7)
}

func TestExecAgentAndWatchDrainsNonzeroExitInSelectedProject(t *testing.T) {
	home := withFakeHome(t)
	project := t.TempDir()
	t.Setenv("QWEN_EXEC_TEST_HOME", home)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The command parser consumes backslash escapes, including in quoted Windows paths.
	command := fmt.Sprintf(`%q -test.run=^TestQwenExecChild$ -- --resume old`, exe)
	var saved *spi.AgentChatSession
	err = NewProvider().ExecAgentAndWatch(project, command, "requested", false, func(s *spi.AgentChatSession) { saved = s })
	var exit *spi.AgentExitError
	if !errors.As(err, &exit) || exit.Code != 7 {
		t.Fatalf("exit=%v, want agent status 7", err)
	}
	if saved == nil || !strings.Contains(saved.RawData, "final child turn") {
		t.Fatal("child's final session not saved before returning")
	}
	if saved.SessionData.WorkspaceRoot != spi.CanonicalizePathOrClean(project) {
		t.Fatalf("wrong workspace: %s", saved.SessionData.WorkspaceRoot)
	}
	data, err := os.ReadFile(filepath.Join(project, "child-args.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"requested"`) || strings.Contains(string(data), `"old"`) {
		t.Fatalf("wrong resume args: %s", data)
	}
}

func TestExecRelativeStorageOverrideMatchesWatcher(t *testing.T) {
	for _, key := range []string{"QWEN_HOME", "QWEN_RUNTIME_DIR"} {
		t.Run(key, func(t *testing.T) {
			home := withFakeHome(t)
			t.Chdir(t.TempDir())
			project := t.TempDir()
			t.Setenv("QWEN_EXEC_TEST_HOME", home)
			t.Setenv(key, "relative-qwen-store")
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			// Quote spaces and escape Windows path separators for the command parser.
			command := fmt.Sprintf(`%q -test.run=^TestQwenExecChild$`, exe)
			var saved *spi.AgentChatSession
			err = NewProvider().ExecAgentAndWatch(project, command, "", false, func(s *spi.AgentChatSession) { saved = s })
			var exit *spi.AgentExitError
			if !errors.As(err, &exit) || exit.Code != 7 {
				t.Fatalf("exit: %v", err)
			}
			if saved == nil {
				t.Fatal("child and watcher resolved relative storage differently")
			}
		})
	}
}
