package piagent

import (
	"testing"
)

// TestParsePiRunCommand covers the command split (quoting, tilde) and the resume
// flag append (`--session-id <id>`). ExecutePi itself is not unit-tested: it
// execs a real binary and calls os.Exit, so coverage goes through this parser,
// exactly as claudecode does.
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
