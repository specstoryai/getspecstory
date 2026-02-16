package claudecode

import (
	"os"
	"testing"
)

func TestParseClaudeCommand(t *testing.T) {
	tests := []struct {
		name            string
		customCommand   string
		resumeSessionId string
		expectedCmd     string
		expectedArgs    []string
		checkDefaultCmd bool
	}{
		{
			name:            "empty string returns default command",
			customCommand:   "",
			resumeSessionId: "",
			expectedCmd:     "",
			expectedArgs:    nil,
			checkDefaultCmd: true,
		},
		{
			name:            "whitespace only returns default command",
			customCommand:   "   ",
			resumeSessionId: "",
			expectedCmd:     "",
			expectedArgs:    nil,
			checkDefaultCmd: true,
		},
		{
			name:            "single command without args",
			customCommand:   "/opt/local/bin/claude",
			resumeSessionId: "",
			expectedCmd:     "/opt/local/bin/claude",
			expectedArgs:    nil,
			checkDefaultCmd: false,
		},
		{
			name:            "command with single argument",
			customCommand:   "claude --help",
			resumeSessionId: "",
			expectedCmd:     "claude",
			expectedArgs:    []string{"--help"},
			checkDefaultCmd: false,
		},
		{
			name:            "command with multiple arguments",
			customCommand:   "claude --model gpt-4 --verbose",
			resumeSessionId: "",
			expectedCmd:     "claude",
			expectedArgs:    []string{"--model", "gpt-4", "--verbose"},
			checkDefaultCmd: false,
		},
		{
			name:            "command with path and arguments",
			customCommand:   "/usr/local/bin/claude --model opus --dangerously-skip-permissions",
			resumeSessionId: "",
			expectedCmd:     "/usr/local/bin/claude",
			expectedArgs:    []string{"--model", "opus", "--dangerously-skip-permissions"},
			checkDefaultCmd: false,
		},
		{
			name:            "command with extra whitespace",
			customCommand:   "  claude   --model   gpt-4  ",
			resumeSessionId: "",
			expectedCmd:     "claude",
			expectedArgs:    []string{"--model", "gpt-4"},
			checkDefaultCmd: false,
		},
		{
			name:            "empty command with resume session",
			customCommand:   "",
			resumeSessionId: "session-12345",
			expectedCmd:     "",
			expectedArgs:    []string{"--resume", "session-12345"},
			checkDefaultCmd: true,
		},
		{
			name:            "command with args and resume session",
			customCommand:   "claude --model gpt-4",
			resumeSessionId: "session-67890",
			expectedCmd:     "claude",
			expectedArgs:    []string{"--model", "gpt-4", "--resume", "session-67890"},
			checkDefaultCmd: false,
		},
		{
			name:            "complex command with resume session",
			customCommand:   "/usr/local/bin/claude --model opus --dangerously-skip-permissions",
			resumeSessionId: "session-abcdef",
			expectedCmd:     "/usr/local/bin/claude",
			expectedArgs:    []string{"--model", "opus", "--dangerously-skip-permissions", "--resume", "session-abcdef"},
			checkDefaultCmd: false,
		},
		{
			// Note: trimming happens in main.go before calling ExecuteClaude, not in parseClaudeCommand
			name:            "resume session ID with whitespace (parseClaudeCommand doesn't trim)",
			customCommand:   "claude",
			resumeSessionId: " 5c5c2876-febd-4c87-b80c-d0655f1cd3fd ",
			expectedCmd:     "claude",
			expectedArgs:    []string{"--resume", " 5c5c2876-febd-4c87-b80c-d0655f1cd3fd "},
			checkDefaultCmd: false,
		},
		{
			name:            "command with quoted argument containing spaces",
			customCommand:   `claude --config "~/Library/Application Support/claude.json"`,
			resumeSessionId: "",
			expectedCmd:     "claude",
			expectedArgs:    []string{"--config", "~/Library/Application Support/claude.json"},
			checkDefaultCmd: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args := parseClaudeCommand(tt.customCommand, tt.resumeSessionId)

			// For cases where we expect the default command
			if tt.checkDefaultCmd {
				defaultCmd := getDefaultClaudeCommand()
				if cmd != defaultCmd {
					t.Errorf("expected default command %q, got %q", defaultCmd, cmd)
				}
			} else {
				if cmd != tt.expectedCmd {
					t.Errorf("expected command %q, got %q", tt.expectedCmd, cmd)
				}
			}

			// Check arguments
			if len(args) != len(tt.expectedArgs) {
				t.Errorf("expected %d args, got %d", len(tt.expectedArgs), len(args))
			} else {
				for i, arg := range args {
					if arg != tt.expectedArgs[i] {
						t.Errorf("expected arg[%d] to be %q, got %q", i, tt.expectedArgs[i], arg)
					}
				}
			}
		})
	}
}

func TestGetDefaultClaudeCommand(t *testing.T) {
	// Save original functions
	originalHomeDir := osUserHomeDir
	originalStat := osStat
	originalLookPath := execLookPath
	defer func() {
		osUserHomeDir = originalHomeDir
		osStat = originalStat
		execLookPath = originalLookPath
	}()

	tests := []struct {
		name           string
		mockHomeDir    func() (string, error)
		mockStat       func(string) (os.FileInfo, error)
		mockLookPath   func(string) (string, error)
		expectedResult string
	}{
		{
			name: "native installation exists",
			mockHomeDir: func() (string, error) {
				return "/home/user", nil
			},
			mockStat: func(name string) (os.FileInfo, error) {
				if name == "/home/user/.local/bin/claude" {
					return nil, nil
				}
				return nil, os.ErrNotExist
			},
			mockLookPath: func(name string) (string, error) {
				return "", os.ErrNotExist
			},
			expectedResult: "/home/user/.local/bin/claude",
		},
		{
			name: "claude on PATH when native doesn't exist",
			mockHomeDir: func() (string, error) {
				return "/home/user", nil
			},
			mockStat: func(name string) (os.FileInfo, error) {
				return nil, os.ErrNotExist
			},
			mockLookPath: func(name string) (string, error) {
				return "/usr/bin/claude", nil
			},
			expectedResult: "claude",
		},
		{
			name: "legacy npm installation when native and PATH don't exist",
			mockHomeDir: func() (string, error) {
				return "/home/user", nil
			},
			mockStat: func(name string) (os.FileInfo, error) {
				if name == "/home/user/.claude/local/claude" {
					return nil, nil
				}
				return nil, os.ErrNotExist
			},
			mockLookPath: func(name string) (string, error) {
				return "", os.ErrNotExist
			},
			expectedResult: "/home/user/.claude/local/claude",
		},
		{
			name: "native takes precedence over PATH and legacy",
			mockHomeDir: func() (string, error) {
				return "/home/user", nil
			},
			mockStat: func(name string) (os.FileInfo, error) {
				if name == "/home/user/.local/bin/claude" || name == "/home/user/.claude/local/claude" {
					return nil, nil
				}
				return nil, os.ErrNotExist
			},
			mockLookPath: func(name string) (string, error) {
				return "/usr/bin/claude", nil
			},
			expectedResult: "/home/user/.local/bin/claude",
		},
		{
			name: "PATH takes precedence over legacy",
			mockHomeDir: func() (string, error) {
				return "/home/user", nil
			},
			mockStat: func(name string) (os.FileInfo, error) {
				if name == "/home/user/.claude/local/claude" {
					return nil, nil
				}
				return nil, os.ErrNotExist
			},
			mockLookPath: func(name string) (string, error) {
				return "/usr/bin/claude", nil
			},
			expectedResult: "claude",
		},
		{
			name: "nothing found falls back to bare claude",
			mockHomeDir: func() (string, error) {
				return "/home/user", nil
			},
			mockStat: func(name string) (os.FileInfo, error) {
				return nil, os.ErrNotExist
			},
			mockLookPath: func(name string) (string, error) {
				return "", os.ErrNotExist
			},
			expectedResult: "claude",
		},
		{
			name: "home dir error falls back to PATH check then bare claude",
			mockHomeDir: func() (string, error) {
				return "", os.ErrNotExist
			},
			mockStat: func(name string) (os.FileInfo, error) {
				t.Error("Stat should not be called when home dir fails")
				return nil, os.ErrNotExist
			},
			mockLookPath: func(name string) (string, error) {
				return "", os.ErrNotExist
			},
			expectedResult: "claude",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			osUserHomeDir = tt.mockHomeDir
			osStat = tt.mockStat
			execLookPath = tt.mockLookPath

			result := getDefaultClaudeCommand()
			if result != tt.expectedResult {
				t.Errorf("expected %q, got %q", tt.expectedResult, result)
			}
		})
	}
}
