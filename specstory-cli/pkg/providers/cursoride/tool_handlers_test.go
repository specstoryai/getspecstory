package cursoride

import (
	"strings"
	"testing"
)

func TestCodeFence(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no backticks uses standard fence",
			input: "plain text",
			want:  "```",
		},
		{
			name:  "empty string uses standard fence",
			input: "",
			want:  "```",
		},
		{
			name:  "inline code span still fits under standard fence",
			input: "use `go build` here",
			want:  "```",
		},
		{
			name:  "double backticks still fit under standard fence",
			input: "a ``literal`` span",
			want:  "```",
		},
		{
			name:  "triple backticks force a four-backtick fence",
			input: "```go\nfmt.Println()\n```",
			want:  "````",
		},
		{
			name:  "longest run wins across multiple runs",
			input: "``` and then `````",
			want:  "``````",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codeFence(tt.input); got != tt.want {
				t.Errorf("codeFence(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCapRunes(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		max           int
		want          string
		wantTruncated bool
	}{
		{
			name:  "short string is unchanged",
			input: "hello",
			max:   10,
			want:  "hello",
		},
		{
			name:  "exact length is unchanged",
			input: "hello",
			max:   5,
			want:  "hello",
		},
		{
			name:          "long string is cut with marker",
			input:         "hello world",
			max:           5,
			want:          "hello\n… (output truncated)",
			wantTruncated: true,
		},
		{
			name: "multi-byte runes are not split",
			// 6 runes but 18 bytes: a byte-based cut at 4 would split a character.
			input:         "日本語日本語",
			max:           4,
			want:          "日本語日\n… (output truncated)",
			wantTruncated: true,
		},
		{
			name:  "multi-byte string within rune cap is unchanged",
			input: "日本語",
			max:   3,
			want:  "日本語",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := capRunes(tt.input, tt.max)
			if got != tt.want {
				t.Errorf("capRunes(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}
			if tt.wantTruncated && !strings.HasSuffix(got, "… (output truncated)") {
				t.Errorf("expected truncation marker on %q", got)
			}
		})
	}
}
