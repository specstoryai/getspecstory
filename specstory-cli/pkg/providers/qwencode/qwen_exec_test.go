package qwencode

import (
	"reflect"
	"testing"
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
			name:            "existing --resume with value is respected",
			args:            []string{"--resume", "other-id"},
			resumeSessionID: "abc-123",
			want:            []string{"--resume", "other-id"},
		},
		{
			name:            "existing -r with value is respected",
			args:            []string{"-r", "other-id"},
			resumeSessionID: "abc-123",
			want:            []string{"-r", "other-id"},
		},
		{
			name:            "existing --resume=id is respected",
			args:            []string{"--resume=other-id"},
			resumeSessionID: "abc-123",
			want:            []string{"--resume=other-id"},
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
			got := ensureResumeArgs(tt.args, tt.resumeSessionID)
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
