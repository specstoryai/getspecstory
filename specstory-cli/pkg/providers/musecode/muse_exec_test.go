package musecode

import (
	"slices"
	"testing"
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

func TestEnsureResumeArgs(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		sessionID string
		expected  []string
	}{
		{
			name:      "no session id leaves args alone",
			args:      []string{"--model", "x"},
			sessionID: "",
			expected:  []string{"--model", "x"},
		},
		{
			name:      "resume is a subcommand, not a flag",
			args:      nil,
			sessionID: "abc-123",
			expected:  []string{"resume", "abc-123"},
		},
		{
			name:      "appended after existing flags",
			args:      []string{"--model", "muse-spark-1.2"},
			sessionID: "abc-123",
			expected:  []string{"--model", "muse-spark-1.2", "resume", "abc-123"},
		},
		{
			name:      "bare resume subcommand gets the id",
			args:      []string{"resume"},
			sessionID: "abc-123",
			expected:  []string{"resume", "abc-123"},
		},
		{
			name:      "resume followed by a flag gets the id inserted",
			args:      []string{"resume", "--verbose"},
			sessionID: "abc-123",
			expected:  []string{"resume", "abc-123", "--verbose"},
		},
		{
			name:      "resume that already names a session is untouched",
			args:      []string{"resume", "existing-id"},
			sessionID: "abc-123",
			expected:  []string{"resume", "existing-id"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := slices.Clone(tt.args)
			got := ensureResumeArgs(tt.args, tt.sessionID)

			if !slices.Equal(got, tt.expected) {
				t.Errorf("ensureResumeArgs(%v, %q) = %v, want %v", tt.args, tt.sessionID, got, tt.expected)
			}
			// The caller's slice must survive: parseMuseCommand hands out a
			// sub-slice of the parsed command line.
			if !slices.Equal(tt.args, input) {
				t.Errorf("input args mutated: %v, want %v", tt.args, input)
			}
		})
	}
}
