package qwencode

import (
	"reflect"
	"testing"
)

func TestParseQwenCommandDefault(t *testing.T) {
	cmd, args := parseQwenCommand("")
	if cmd != "qwen" {
		t.Errorf("command = %q, want %q", cmd, "qwen")
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want none", args)
	}
}

func TestParseQwenCommandCustom(t *testing.T) {
	cmd, args := parseQwenCommand("/usr/local/bin/qwen --model qwen3-coder")
	if cmd != "/usr/local/bin/qwen" {
		t.Errorf("command = %q, want %q", cmd, "/usr/local/bin/qwen")
	}
	if !reflect.DeepEqual(args, []string{"--model", "qwen3-coder"}) {
		t.Errorf("args = %v, want [--model qwen3-coder]", args)
	}
}

func TestEnsureResumeArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		session  string
		expected []string
	}{
		{
			name:     "empty session id leaves args untouched",
			args:     []string{"--model", "qwen3-coder"},
			session:  "",
			expected: []string{"--model", "qwen3-coder"},
		},
		{
			name:     "appends resume flag",
			args:     []string{"--model", "qwen3-coder"},
			session:  "abc-123",
			expected: []string{"--model", "qwen3-coder", "--resume", "abc-123"},
		},
		{
			name:     "keeps existing --resume with value",
			args:     []string{"--resume", "existing-id"},
			session:  "abc-123",
			expected: []string{"--resume", "existing-id"},
		},
		{
			name:     "keeps existing -r with value",
			args:     []string{"-r", "existing-id"},
			session:  "abc-123",
			expected: []string{"-r", "existing-id"},
		},
		{
			name:     "keeps existing --resume= form",
			args:     []string{"--resume=existing-id"},
			session:  "abc-123",
			expected: []string{"--resume=existing-id"},
		},
		{
			name:     "trailing --resume without value gets the session id",
			args:     []string{"--resume"},
			session:  "abc-123",
			expected: []string{"--resume", "abc-123"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ensureResumeArgs(tc.args, tc.session)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("ensureResumeArgs(%v, %q) = %v, want %v", tc.args, tc.session, got, tc.expected)
			}
		})
	}
}
