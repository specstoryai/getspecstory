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
