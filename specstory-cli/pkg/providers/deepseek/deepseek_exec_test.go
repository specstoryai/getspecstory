package deepseek

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEnsureResumeArgs(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		resumeSessionID string
		expected        []string
	}{
		{
			name:            "returns args unchanged when resumeSessionID is empty",
			args:            []string{"--verbose"},
			resumeSessionID: "",
			expected:        []string{"--verbose"},
		},
		{
			name:            "returns args unchanged when --resume already has value",
			args:            []string{"--resume", "existing-session"},
			resumeSessionID: "new-session",
			expected:        []string{"--resume", "existing-session"},
		},
		{
			name:            "returns args unchanged when -r already has value",
			args:            []string{"-r", "existing-session"},
			resumeSessionID: "new-session",
			expected:        []string{"-r", "existing-session"},
		},
		{
			name:            "returns args unchanged when --resume=value exists",
			args:            []string{"--resume=existing-session"},
			resumeSessionID: "new-session",
			expected:        []string{"--resume=existing-session"},
		},
		{
			name:            "appends --resume when not present",
			args:            []string{"--verbose"},
			resumeSessionID: "new-session",
			expected:        []string{"--verbose", "--resume", "new-session"},
		},
		{
			name:            "appends --resume when args is nil",
			args:            nil,
			resumeSessionID: "new-session",
			expected:        []string{"--resume", "new-session"},
		},
		{
			name:            "appends --resume when args is empty",
			args:            []string{},
			resumeSessionID: "new-session",
			expected:        []string{"--resume", "new-session"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureResumeArgs(tt.args, tt.resumeSessionID)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("ensureResumeArgs() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantCmd     string
		wantArgs    []string
		wantHomeFix bool
	}{
		{
			name:     "empty input returns default",
			input:    "",
			wantCmd:  defaultCommand,
			wantArgs: nil,
		},
		{
			name:     "whitespace-only input returns default",
			input:    "   ",
			wantCmd:  defaultCommand,
			wantArgs: nil,
		},
		{
			name:     "single token returns command and no args",
			input:    "/usr/local/bin/deepseek",
			wantCmd:  "/usr/local/bin/deepseek",
			wantArgs: []string{},
		},
		{
			name:     "command plus args splits on whitespace",
			input:    "deepseek --debug --workdir /tmp",
			wantCmd:  "deepseek",
			wantArgs: []string{"--debug", "--workdir", "/tmp"},
		},
		{
			name:        "leading tilde is expanded to home",
			input:       "~/bin/deepseek --verbose",
			wantArgs:    []string{"--verbose"},
			wantHomeFix: true,
		},
	}

	home, _ := os.UserHomeDir()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotArgs := parseCommand(tt.input)

			if tt.wantHomeFix {
				expected := filepath.Join(home, "bin/deepseek")
				if gotCmd != expected {
					t.Errorf("parseCommand() cmd = %q, want %q", gotCmd, expected)
				}
			} else if gotCmd != tt.wantCmd {
				t.Errorf("parseCommand() cmd = %q, want %q", gotCmd, tt.wantCmd)
			}

			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Errorf("parseCommand() args = %v, want %v", gotArgs, tt.wantArgs)
			}
		})
	}
}

func TestExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("UserHomeDir failed, skipping")
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "no tilde returns input", input: "/usr/local/bin/foo", want: "/usr/local/bin/foo"},
		{name: "bare tilde without slash returns input", input: "~tilde", want: "~tilde"},
		{name: "tilde slash expands to home", input: "~/bin/foo", want: filepath.Join(home, "bin/foo")},
		{name: "empty input returns empty", input: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandTilde(tt.input)
			if got != tt.want {
				t.Errorf("expandTilde(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDefaultCommandConstant(t *testing.T) {
	// Sanity: defaults shouldn't drift silently — they're part of the user contract.
	if defaultCommand != "deepseek" {
		t.Errorf("defaultCommand = %q, want %q", defaultCommand, "deepseek")
	}
	if versionFlag != "--version" {
		t.Errorf("versionFlag = %q, want %q", versionFlag, "--version")
	}
}

func TestParseCommand_DoesNotMutate(t *testing.T) {
	// Guard against accidental in-place modification of the input string,
	// which would surprise callers that re-use the same custom-command value.
	input := "deepseek --foo"
	original := input
	_, _ = parseCommand(input)
	if input != original {
		t.Errorf("parseCommand mutated input: got %q, want %q", input, original)
	}
	// Also verify args slice is independent (we can mutate without aliasing into spi internals).
	_, args := parseCommand("deepseek --a --b")
	if len(args) > 0 {
		args[0] = "MUTATED"
		_, args2 := parseCommand("deepseek --a --b")
		if strings.Contains(strings.Join(args2, " "), "MUTATED") {
			t.Errorf("parseCommand returned aliased slice; got %v", args2)
		}
	}
}
